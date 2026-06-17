package syncer_test

import (
	"context"
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

// recordingFiles implements remoteindex.Lister, remoteindex.Mkdirer and
// service.FileWriter/FileManager for executor and push tests.
type recordingFiles struct {
	mu      sync.Mutex
	folders map[int64][]domain.FileInfo // existing children by folder id
	nextID  int64
	uploads []service.UploadInput
	deleted []int64
}

func (r *recordingFiles) List(_ context.Context, folderID int64) ([]domain.FileInfo, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
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
	r.uploads = append(r.uploads, in)
	r.nextID++
	return domain.FileInfo{ID: 3000 + r.nextID, Name: in.Name, Size: int64(len(body)), LastModifiedAt: 4242}, nil
}

func (r *recordingFiles) Delete(_ context.Context, fileID int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.deleted = append(r.deleted, fileID)
	return nil
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
		ex = syncer.NewPushExecutor(root, resolver, files, files)
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
})
