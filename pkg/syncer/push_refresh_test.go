package syncer_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/stillsource/kdrive-fuse/pkg/domain"
	"github.com/stillsource/kdrive-fuse/pkg/infrastructure/manifest"
	"github.com/stillsource/kdrive-fuse/pkg/syncer"
)

var _ = Describe("Push --refresh", func() {
	It("re-bootstraps and reconciles a stale remote id without re-uploading", func() {
		root := GinkgoT().TempDir()
		mpath := filepath.Join(GinkgoT().TempDir(), "m.tsv")
		Expect(os.WriteFile(filepath.Join(root, "a.jpg"), []byte("aaa"), 0o644)).To(Succeed())

		// Manifest pre-seeded with a STALE remote id for a.jpg.
		stale := manifest.New()
		stale.Set("a.jpg", manifest.Entry{Size: 3, LocalMtime: 1, RemoteID: 999, RemoteMtime: 50})
		Expect(stale.Save(mpath)).To(Succeed())

		// The remote actually has a.jpg as id 7, same size.
		files := &recordingFiles{folders: map[int64][]domain.FileInfo{
			1: {{ID: 7, Name: "a.jpg", Type: domain.FileTypeFile, Size: 3, LastModifiedAt: 100}},
		}}

		res, err := syncer.Push(context.Background(), syncer.PushOptions{LocalRoot: root, Jobs: 2, Refresh: true}, files, 1, mpath, &strings.Builder{})
		Expect(err).NotTo(HaveOccurred())
		Expect(res.Uploaded).To(Equal(0)) // size matches after re-bootstrap -> no upload

		got, err := manifest.Load(mpath)
		Expect(err).NotTo(HaveOccurred())
		e, ok := got.Get("a.jpg")
		Expect(ok).To(BeTrue())
		Expect(e.RemoteID).To(Equal(int64(7))) // reconciled from the fresh index
	})
})
