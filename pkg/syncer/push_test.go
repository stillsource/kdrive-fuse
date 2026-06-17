package syncer_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/stillsource/kdrive-fuse/pkg/domain"
	"github.com/stillsource/kdrive-fuse/pkg/syncer"
)

var _ = Describe("Push", func() {
	var (
		root  string
		mpath string
		files *recordingFiles
	)
	BeforeEach(func() {
		root = GinkgoT().TempDir()
		mpath = filepath.Join(GinkgoT().TempDir(), "m.tsv")
		files = &recordingFiles{folders: map[int64][]domain.FileInfo{}}
	})
	writeLocal := func(rel, data string) {
		p := filepath.Join(root, filepath.FromSlash(rel))
		Expect(os.MkdirAll(filepath.Dir(p), 0o755)).To(Succeed())
		Expect(os.WriteFile(p, []byte(data), 0o644)).To(Succeed())
	}
	opts := func() syncer.PushOptions {
		return syncer.PushOptions{LocalRoot: root, Jobs: 4}
	}

	It("uploads every local file on a first push and saves the manifest", func() {
		writeLocal("a.jpg", "aaa")
		writeLocal("2025/b.jpg", "bbbbb")
		var out strings.Builder
		res, err := syncer.Push(context.Background(), opts(), files, 1, mpath, &out)
		Expect(err).NotTo(HaveOccurred())
		Expect(res.Uploaded).To(Equal(2))
		Expect(files.uploads).To(HaveLen(2))
		_, err = os.Stat(mpath)
		Expect(err).NotTo(HaveOccurred()) // manifest saved
	})

	It("is a no-op on a second identical push", func() {
		writeLocal("a.jpg", "aaa")
		_, err := syncer.Push(context.Background(), opts(), files, 1, mpath, &strings.Builder{})
		Expect(err).NotTo(HaveOccurred())
		files.uploads = nil // reset
		res, err := syncer.Push(context.Background(), opts(), files, 1, mpath, &strings.Builder{})
		Expect(err).NotTo(HaveOccurred())
		Expect(res).To(Equal(syncer.Result{}))
		Expect(files.uploads).To(BeEmpty())
	})

	It("dry-run prints a plan and changes nothing", func() {
		writeLocal("a.jpg", "aaa")
		o := opts()
		o.DryRun = true
		var out strings.Builder
		res, err := syncer.Push(context.Background(), o, files, 1, mpath, &out)
		Expect(err).NotTo(HaveOccurred())
		Expect(res).To(Equal(syncer.Result{}))
		Expect(files.uploads).To(BeEmpty())
		Expect(out.String()).To(ContainSubstring("dry-run"))
		Expect(out.String()).To(ContainSubstring("a.jpg"))
		_, statErr := os.Stat(mpath)
		Expect(os.IsNotExist(statErr)).To(BeTrue()) // no manifest written
	})

	It("--no-delete suppresses deletions", func() {
		writeLocal("a.jpg", "aaa")
		_, err := syncer.Push(context.Background(), opts(), files, 1, mpath, &strings.Builder{})
		Expect(err).NotTo(HaveOccurred())
		Expect(os.Remove(filepath.Join(root, "a.jpg"))).To(Succeed()) // local delete
		o := opts()
		o.NoDelete = true
		res, err := syncer.Push(context.Background(), o, files, 1, mpath, &strings.Builder{})
		Expect(err).NotTo(HaveOccurred())
		Expect(res.Deleted).To(Equal(0))
		Expect(files.deleted).To(BeEmpty())
	})
})
