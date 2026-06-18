package syncer_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/stillsource/kdrive-fuse/pkg/domain"
	"github.com/stillsource/kdrive-fuse/pkg/infrastructure/remoteindex"
	"github.com/stillsource/kdrive-fuse/pkg/service"
	"github.com/stillsource/kdrive-fuse/pkg/syncer"
)

// renameCall records one Rename invocation.
type renameCall struct {
	id   int64
	name string
}

// recordingFiles implements remoteindex.Lister, remoteindex.Mkdirer,
// service.FileWriter/FileManager, and Downloader for executor, push, and
// two-way tests. It satisfies syncer.Remote.
type recordingFiles struct {
	mu               sync.Mutex
	folders          map[int64][]domain.FileInfo // existing children by folder id
	nextID           int64
	uploads          []service.UploadInput
	deleted          []int64
	moved            [][2]int64                // {fileID, destDirID} per Move call
	renamed          []renameCall              // per Rename call
	failUpload       map[string]bool           // upload of these names returns an error
	failRename       map[int64]bool            // Rename of these ids returns an error
	conflictOnNew    map[string]bool           // a NEW upload (no ExistingFileID) of these names returns ErrConflict
	notFoundOnDelete map[int64]bool            // Delete of these ids returns domain.ErrNotFound
	listErr          error                     // when set, List returns this error
	byID             map[int64]domain.FileInfo // current remote state by id (for Stat)
	statErr          map[int64]error           // when set for an id, Stat returns this error
	content          map[int64][]byte          // file content by id (for DownloadStream)
	mtimeAfterMove   int64                     // when >0, Move/Rename set the file's LastModifiedAt to this (server touches mtime on metadata change)
}

func (r *recordingFiles) List(_ context.Context, folderID int64) ([]domain.FileInfo, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.listErr != nil {
		return nil, r.listErr
	}
	return r.folders[folderID], nil
}

func (r *recordingFiles) Mkdir(_ context.Context, parentID int64, name string) (domain.FileInfo, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.nextID++
	info := domain.FileInfo{ID: 2000 + r.nextID, Name: name, Type: domain.FileTypeDir}
	r.folders[parentID] = append(r.folders[parentID], info)
	return info, nil
}

func (r *recordingFiles) Upload(_ context.Context, in service.UploadInput) (domain.FileInfo, error) {
	body, _ := io.ReadAll(in.Body)
	r.mu.Lock()
	defer r.mu.Unlock()
	if in.ExistingFileID == 0 && r.conflictOnNew[in.Name] {
		return domain.FileInfo{}, domain.ErrConflict
	}
	if r.failUpload[in.Name] {
		return domain.FileInfo{}, errors.New("upload failed: " + in.Name)
	}
	r.uploads = append(r.uploads, in)
	r.nextID++
	id := int64(3000) + r.nextID
	if in.ExistingFileID != 0 {
		id = in.ExistingFileID
	}
	info := domain.FileInfo{ID: id, Name: in.Name, Size: int64(len(body)), LastModifiedAt: 4242}
	if r.byID == nil {
		r.byID = map[int64]domain.FileInfo{}
	}
	r.byID[id] = info
	return info, nil
}

func (r *recordingFiles) Stat(_ context.Context, fileID int64) (domain.FileInfo, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err, ok := r.statErr[fileID]; ok {
		return domain.FileInfo{}, err
	}
	if info, ok := r.byID[fileID]; ok {
		return info, nil
	}
	// Fall back to scanning folders (for files pre-seeded before any Upload call).
	for _, children := range r.folders {
		for _, info := range children {
			if info.ID == fileID {
				return info, nil
			}
		}
	}
	return domain.FileInfo{}, domain.ErrNotFound
}

func (r *recordingFiles) Delete(_ context.Context, fileID int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.notFoundOnDelete[fileID] {
		return domain.ErrNotFound
	}
	delete(r.byID, fileID)
	r.deleted = append(r.deleted, fileID)
	return nil
}

func (r *recordingFiles) Move(_ context.Context, fileID, destDirID int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.moved = append(r.moved, [2]int64{fileID, destDirID})
	if info, ok := r.byID[fileID]; ok {
		info.ParentID = destDirID
		if r.mtimeAfterMove > 0 {
			info.LastModifiedAt = r.mtimeAfterMove
		}
		r.byID[fileID] = info
	}
	return nil
}

func (r *recordingFiles) Rename(_ context.Context, fileID int64, newName string) (domain.FileInfo, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.failRename[fileID] {
		return domain.FileInfo{}, errors.New("rename failed")
	}
	r.renamed = append(r.renamed, renameCall{id: fileID, name: newName})
	if info, ok := r.byID[fileID]; ok {
		info.Name = newName
		if r.mtimeAfterMove > 0 {
			info.LastModifiedAt = r.mtimeAfterMove
		}
		r.byID[fileID] = info
		return info, nil
	}
	return domain.FileInfo{ID: fileID, Name: newName}, nil
}

// DownloadStream serves byID content as a byte stream, mirroring fakeDownloader.
// It returns domain.ErrNotFound for an unknown id so it satisfies the Remote interface.
func (r *recordingFiles) DownloadStream(_ context.Context, fileID, _, _ int64) (io.ReadCloser, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if b, ok := r.content[fileID]; ok {
		return io.NopCloser(bytes.NewReader(b)), nil
	}
	return nil, domain.ErrNotFound
}

var _ = Describe("PushExecutor", func() {
	var (
		root  string
		files *recordingFiles
		ex    *syncer.PushExecutor
	)
	BeforeEach(func() {
		root = GinkgoT().TempDir()
		files = &recordingFiles{folders: map[int64][]domain.FileInfo{}}
		resolver := remoteindex.NewResolver(files, files, 1)
		ex = syncer.NewPushExecutor(root, resolver, files, files, files, files, files)
	})
	writeLocal := func(rel, data string) {
		p := filepath.Join(root, filepath.FromSlash(rel))
		Expect(os.MkdirAll(filepath.Dir(p), 0o755)).To(Succeed())
		Expect(os.WriteFile(p, []byte(data), 0o644)).To(Succeed())
	}

	It("uploads a new file under its resolved parent folder", func() {
		writeLocal("2025/a.jpg", "hello")
		id, mtime, err := ex.Upload(context.Background(), "2025/a.jpg", 5)
		Expect(err).NotTo(HaveOccurred())
		Expect(id).NotTo(BeZero())
		Expect(mtime).To(Equal(int64(4242)))
		Expect(files.uploads).To(HaveLen(1))
		Expect(files.uploads[0].Name).To(Equal("a.jpg"))
		Expect(files.uploads[0].ParentID).NotTo(BeZero()) // resolved/created "2025"
		Expect(files.uploads[0].ExistingFileID).To(BeZero())
		Expect(files.uploads[0].Size).To(Equal(int64(5)))
	})

	It("overwrites by existing file id", func() {
		writeLocal("a.jpg", "world!")
		mtime, err := ex.Overwrite(context.Background(), "a.jpg", 77, 6)
		Expect(err).NotTo(HaveOccurred())
		Expect(mtime).To(Equal(int64(4242)))
		Expect(files.uploads).To(HaveLen(1))
		Expect(files.uploads[0].ExistingFileID).To(Equal(int64(77)))
		Expect(files.uploads[0].ParentID).To(BeZero())
	})

	It("deletes by remote id", func() {
		Expect(ex.Delete(context.Background(), "x.jpg", 99)).To(Succeed())
		Expect(files.deleted).To(Equal([]int64{99}))
	})

	It("errors when the local file is missing", func() {
		_, _, err := ex.Upload(context.Background(), "missing.jpg", 0)
		Expect(err).To(HaveOccurred())
	})

	It("reconciles a conflicting new upload into an overwrite-by-id (idempotent re-run)", func() {
		writeLocal("a.jpg", "hello")
		// Simulate a prior interrupted run: the file already exists remotely under root (id 1).
		files.folders[1] = []domain.FileInfo{{ID: 77, Name: "a.jpg", Type: domain.FileTypeFile}}
		files.conflictOnNew = map[string]bool{"a.jpg": true}

		id, mtime, err := ex.Upload(context.Background(), "a.jpg", 5)
		Expect(err).NotTo(HaveOccurred())
		Expect(id).To(Equal(int64(77)))      // reconciled to the existing remote id
		Expect(mtime).To(Equal(int64(4242))) // mtime from the overwrite
		Expect(files.uploads).To(HaveLen(1)) // the failed NEW upload was not recorded
		Expect(files.uploads[0].ExistingFileID).To(Equal(int64(77)))
	})

	It("treats deleting an already-gone remote file as success (idempotent re-run)", func() {
		files.notFoundOnDelete = map[int64]bool{99: true}
		Expect(ex.Delete(context.Background(), "x.jpg", 99)).To(Succeed())
	})

	It("surfaces the original conflict when the file can't be found for reconcile", func() {
		writeLocal("a.jpg", "hello")
		files.conflictOnNew = map[string]bool{"a.jpg": true} // folders[1] left empty
		_, _, err := ex.Upload(context.Background(), "a.jpg", 5)
		Expect(err).To(MatchError(domain.ErrConflict))
	})

	It("propagates a listing error during reconcile", func() {
		writeLocal("a.jpg", "hello")
		files.conflictOnNew = map[string]bool{"a.jpg": true}
		files.listErr = errors.New("list boom")
		_, _, err := ex.Upload(context.Background(), "a.jpg", 5)
		Expect(err).To(MatchError(ContainSubstring("list boom")))
	})

	It("skips a same-named directory when reconciling to the file", func() {
		writeLocal("a.jpg", "hello")
		files.folders[1] = []domain.FileInfo{
			{ID: 50, Name: "a.jpg", Type: domain.FileTypeDir},
			{ID: 77, Name: "a.jpg", Type: domain.FileTypeFile},
		}
		files.conflictOnNew = map[string]bool{"a.jpg": true}
		id, _, err := ex.Upload(context.Background(), "a.jpg", 5)
		Expect(err).NotTo(HaveOccurred())
		Expect(id).To(Equal(int64(77))) // the directory was skipped
	})

	Context("Move idempotency on a crash re-run", func() {
		// Each case simulates a re-run after the first run relocated the file but
		// crashed before the manifest was checkpointed, so the plan still asks to
		// move from the old path. The live remote is already (partly) at target.
		BeforeEach(func() {
			files.folders[1] = []domain.FileInfo{{ID: 50, Name: "sub", Type: domain.FileTypeDir}}
		})

		It("issues no mutating call when parent and name already match (cross-folder move re-run)", func() {
			files.byID = map[int64]domain.FileInfo{
				42: {ID: 42, Name: "a.jpg", Type: domain.FileTypeFile, ParentID: 50, LastModifiedAt: 1234},
			}
			mtime, err := ex.Move(context.Background(), "a.jpg", "sub/a.jpg", 42)
			Expect(err).NotTo(HaveOccurred())
			Expect(mtime).To(Equal(int64(1234))) // cur's mtime; no re-Stat after a no-op
			Expect(files.moved).To(BeEmpty())
			Expect(files.renamed).To(BeEmpty())
			Expect(files.folders[1]).To(HaveLen(1)) // resolving the dest created no phantom folder
		})

		It("issues no Move and no Rename when both already match (move+rename re-run)", func() {
			files.byID = map[int64]domain.FileInfo{
				42: {ID: 42, Name: "b.jpg", Type: domain.FileTypeFile, ParentID: 50, LastModifiedAt: 1234},
			}
			mtime, err := ex.Move(context.Background(), "a.jpg", "sub/b.jpg", 42)
			Expect(err).NotTo(HaveOccurred())
			Expect(mtime).To(Equal(int64(1234)))
			Expect(files.moved).To(BeEmpty())
			Expect(files.renamed).To(BeEmpty())
		})

		It("self-heals a partial run: skips the applied Move, issues only the missing Rename", func() {
			// First run moved a.jpg under "sub" but crashed before renaming to b.jpg.
			files.byID = map[int64]domain.FileInfo{
				42: {ID: 42, Name: "a.jpg", Type: domain.FileTypeFile, ParentID: 50, LastModifiedAt: 1234},
			}
			_, err := ex.Move(context.Background(), "a.jpg", "sub/b.jpg", 42)
			Expect(err).NotTo(HaveOccurred())
			Expect(files.moved).To(BeEmpty())                                      // already under sub -> Move skipped
			Expect(files.renamed).To(Equal([]renameCall{{id: 42, name: "b.jpg"}})) // only the Rename ran
		})

		It("still issues the Move when parent_id is absent, and returns the post-mutation mtime", func() {
			// cur.ParentID == 0 (API omitted parent_id) must not skip the Move, and
			// the returned mtime must come from the SECOND Stat (post-mutation), not
			// the stale cur.LastModifiedAt.
			files.mtimeAfterMove = 5555 // server touches mtime when the file is moved
			files.byID = map[int64]domain.FileInfo{
				42: {ID: 42, Name: "a.jpg", Type: domain.FileTypeFile, LastModifiedAt: 1234},
			}
			mtime, err := ex.Move(context.Background(), "a.jpg", "sub/a.jpg", 42)
			Expect(err).NotTo(HaveOccurred())
			Expect(files.moved).To(Equal([][2]int64{{42, 50}})) // fell back to issuing the Move
			Expect(mtime).To(Equal(int64(5555)))                // re-Stat after mutating, not stale 1234
		})

		It("corrects an out-of-band relocation: same-dir rename intent, but the file drifted to another parent", func() {
			// Manifest intent is a pure rename within root (a.jpg -> b.jpg), but
			// another client moved the file into "sub" since the snapshot. The live
			// parent (50) is authoritative, so we move it back to root AND rename it,
			// instead of renaming it in place inside the wrong folder.
			files.byID = map[int64]domain.FileInfo{
				42: {ID: 42, Name: "a.jpg", Type: domain.FileTypeFile, ParentID: 50, LastModifiedAt: 1234},
			}
			_, err := ex.Move(context.Background(), "a.jpg", "b.jpg", 42)
			Expect(err).NotTo(HaveOccurred())
			Expect(files.moved).To(Equal([][2]int64{{42, 1}}))                     // moved back to root (id 1)
			Expect(files.renamed).To(Equal([]renameCall{{id: 42, name: "b.jpg"}})) // and renamed
		})

		It("is a no-op for a same-dir rename re-run already at the target name", func() {
			// Pure rename within root that the first run already applied (Name=b.jpg).
			files.byID = map[int64]domain.FileInfo{
				42: {ID: 42, Name: "b.jpg", Type: domain.FileTypeFile, ParentID: 1, LastModifiedAt: 1234},
			}
			mtime, err := ex.Move(context.Background(), "a.jpg", "b.jpg", 42)
			Expect(err).NotTo(HaveOccurred())
			Expect(mtime).To(Equal(int64(1234)))
			Expect(files.moved).To(BeEmpty())
			Expect(files.renamed).To(BeEmpty())
		})
	})
})
