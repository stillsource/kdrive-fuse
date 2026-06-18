package syncer_test

import (
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

// recordingFiles implements remoteindex.Lister, remoteindex.Mkdirer and
// service.FileWriter/FileManager for executor and push tests.
type recordingFiles struct {
	mu               sync.Mutex
	folders          map[int64][]domain.FileInfo // existing children by folder id
	nextID           int64
	uploads          []service.UploadInput
	deleted          []int64
	moved            [][2]int64                // {fileID, destDirID} per Move call
	renamed          []renameCall              // per Rename call
	failUpload       map[string]bool           // upload of these names returns an error
	conflictOnNew    map[string]bool           // a NEW upload (no ExistingFileID) of these names returns ErrConflict
	notFoundOnDelete map[int64]bool            // Delete of these ids returns domain.ErrNotFound
	listErr          error                     // when set, List returns this error
	byID             map[int64]domain.FileInfo // current remote state by id (for Stat)
	statErr          map[int64]error           // when set for an id, Stat returns this error
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
	return nil
}

func (r *recordingFiles) Rename(_ context.Context, fileID int64, newName string) (domain.FileInfo, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.renamed = append(r.renamed, renameCall{id: fileID, name: newName})
	if info, ok := r.byID[fileID]; ok {
		info.Name = newName
		r.byID[fileID] = info
		return info, nil
	}
	return domain.FileInfo{ID: fileID, Name: newName}, nil
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
		ex = syncer.NewPushExecutor(root, resolver, files, files, files, files)
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
})
