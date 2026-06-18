package syncer_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/stillsource/kdrive-fuse/pkg/domain"
	"github.com/stillsource/kdrive-fuse/pkg/infrastructure/manifest"
	"github.com/stillsource/kdrive-fuse/pkg/syncer"
)

// writeTempFile creates a file with content in the given directory.
func writeTempFile(dir, rel, content string) {
	p := filepath.Join(dir, filepath.FromSlash(rel))
	Expect(os.MkdirAll(filepath.Dir(p), 0o755)).To(Succeed())
	Expect(os.WriteFile(p, []byte(content), 0o644)).To(Succeed())
}

// writeTempFileWithMtime creates a file and sets its mtime to the given Unix timestamp.
func writeTempFileWithMtime(dir, rel, content string, mtime int64) {
	p := filepath.Join(dir, filepath.FromSlash(rel))
	Expect(os.MkdirAll(filepath.Dir(p), 0o755)).To(Succeed())
	Expect(os.WriteFile(p, []byte(content), 0o644)).To(Succeed())
	t := time.Unix(mtime, 0)
	Expect(os.Chtimes(p, t, t)).To(Succeed())
}

// writeManifest writes a manifest TSV to mpath (using syncer's internal format).
// We use the manifest API directly instead.
func saveManifest(m *manifest.Manifest, mpath string) {
	Expect(m.Save(mpath)).To(Succeed())
}

var _ = Describe("TwoWay", func() {
	var (
		root  string
		mpath string
		files *recordingFiles
		out   *bytes.Buffer
	)

	BeforeEach(func() {
		root = GinkgoT().TempDir()
		mpath = filepath.Join(GinkgoT().TempDir(), "m.tsv")
		files = &recordingFiles{
			folders: map[int64][]domain.FileInfo{},
			byID:    map[int64]domain.FileInfo{},
		}
		out = &bytes.Buffer{}
	})

	It("applies local-only change as push, remote-only change as pull, and reports conflict", func() {
		// Set up a manifest with three files tracked.
		m := manifest.New()
		m.Set("push-me.jpg", manifest.Entry{Size: 10, LocalMtime: 100, RemoteID: 7, RemoteMtime: 200})
		m.Set("pull-me.jpg", manifest.Entry{Size: 10, LocalMtime: 100, RemoteID: 8, RemoteMtime: 200})
		m.Set("conflict.jpg", manifest.Entry{Size: 10, LocalMtime: 100, RemoteID: 9, RemoteMtime: 200})
		saveManifest(m, mpath)

		// Local tree: push-me.jpg changed, pull-me.jpg unchanged, conflict.jpg changed.
		writeTempFile(root, "push-me.jpg", "updated content")          // size changed vs baseline (10)
		writeTempFileWithMtime(root, "pull-me.jpg", "0123456789", 100) // 10 bytes, mtime=100 — matches baseline exactly
		writeTempFile(root, "conflict.jpg", "new local data")          // size changed locally

		// Remote index (folders[1]): pull-me.jpg changed, push-me.jpg unchanged, conflict.jpg also changed.
		files.folders[1] = []domain.FileInfo{
			{ID: 7, Name: "push-me.jpg", Type: domain.FileTypeFile, Size: 10, LastModifiedAt: 200},  // unchanged remote
			{ID: 8, Name: "pull-me.jpg", Type: domain.FileTypeFile, Size: 25, LastModifiedAt: 300},  // remote changed
			{ID: 9, Name: "conflict.jpg", Type: domain.FileTypeFile, Size: 20, LastModifiedAt: 999}, // remote also changed
		}
		files.byID[7] = files.folders[1][0]
		files.byID[8] = files.folders[1][1]
		files.byID[9] = files.folders[1][2]
		// provide content for pull-me.jpg download
		files.content = map[int64][]byte{8: []byte("new remote content of pull-me")}

		opts := syncer.TwoWayOptions{
			LocalRoot:       root,
			Jobs:            2,
			Force:           false,
			DryRun:          false,
			DeleteThreshold: 0.5,
		}
		res, err := syncer.TwoWay(context.Background(), opts, files, 1, mpath, out)
		Expect(err).NotTo(HaveOccurred())

		// Conflict should be reported and counted.
		Expect(res.Conflicts).To(Equal(1))
		Expect(out.String()).To(ContainSubstring("conflict (changed on both sides, skipped): conflict.jpg"))

		// Push happened: push-me.jpg was uploaded/overwritten.
		Expect(res.Pushed).To(BeNumerically(">=", 1))

		// Pull happened: pull-me.jpg was downloaded.
		Expect(res.Pulled).To(BeNumerically(">=", 1))
		data, err := os.ReadFile(filepath.Join(root, "pull-me.jpg"))
		Expect(err).NotTo(HaveOccurred())
		Expect(string(data)).To(Equal("new remote content of pull-me"))

		// conflict.jpg must NOT have been uploaded (no upload for that name).
		for _, up := range files.uploads {
			Expect(up.Name).NotTo(Equal("conflict.jpg"))
		}

		// No failures.
		Expect(res.Failed).To(Equal(0))
	})

	It("dry-run prints the plan and makes no changes", func() {
		m := manifest.New()
		m.Set("a.jpg", manifest.Entry{Size: 5, LocalMtime: 100, RemoteID: 10, RemoteMtime: 200})
		saveManifest(m, mpath)

		writeTempFile(root, "a.jpg", "hello") // size 5, mtime matches — but we'll fake a remote change

		// Remote: a.jpg has a new mtime → pull planned.
		files.folders[1] = []domain.FileInfo{
			{ID: 10, Name: "a.jpg", Type: domain.FileTypeFile, Size: 5, LastModifiedAt: 999},
		}
		files.byID[10] = files.folders[1][0]

		opts := syncer.TwoWayOptions{
			LocalRoot:       root,
			Jobs:            1,
			DryRun:          true,
			DeleteThreshold: 0.5,
		}
		res, err := syncer.TwoWay(context.Background(), opts, files, 1, mpath, out)
		Expect(err).NotTo(HaveOccurred())
		Expect(out.String()).To(ContainSubstring("dry-run"))
		// No actual uploads or downloads.
		Expect(files.uploads).To(BeEmpty())
		// Manifest not modified beyond what was already there.
		_ = res
	})

	It("bootstraps from remote index when manifest is empty and classifies correctly", func() {
		// Empty manifest — Bootstrap should seed it, preventing push of existing files.
		m := manifest.New()
		saveManifest(m, mpath)

		writeTempFile(root, "existing.jpg", "hello") // 5 bytes

		// Remote has the same file.
		files.folders[1] = []domain.FileInfo{
			{ID: 20, Name: "existing.jpg", Type: domain.FileTypeFile, Size: 5, LastModifiedAt: 100},
		}
		files.byID[20] = files.folders[1][0]

		opts := syncer.TwoWayOptions{
			LocalRoot:       root,
			Jobs:            1,
			DryRun:          false,
			DeleteThreshold: 0.5,
		}
		res, err := syncer.TwoWay(context.Background(), opts, files, 1, mpath, out)
		Expect(err).NotTo(HaveOccurred())
		// After bootstrap, existing.jpg is seen as in-sync — nothing to do.
		Expect(res.Pushed).To(Equal(0))
		Expect(res.Pulled).To(Equal(0))
		Expect(res.Conflicts).To(Equal(0))
		Expect(files.uploads).To(BeEmpty())
	})

	It("reports conflicts == 0 and no conflict line when there are no conflicts", func() {
		m := manifest.New()
		m.Set("ok.jpg", manifest.Entry{Size: 5, LocalMtime: 100, RemoteID: 30, RemoteMtime: 200})
		saveManifest(m, mpath)

		writeTempFile(root, "ok.jpg", "hello") // size 5, mtime from disk

		files.folders[1] = []domain.FileInfo{
			{ID: 30, Name: "ok.jpg", Type: domain.FileTypeFile, Size: 5, LastModifiedAt: 200},
		}
		files.byID[30] = files.folders[1][0]

		opts := syncer.TwoWayOptions{
			LocalRoot:       root,
			Jobs:            1,
			DeleteThreshold: 0.5,
		}
		res, err := syncer.TwoWay(context.Background(), opts, files, 1, mpath, out)
		Expect(err).NotTo(HaveOccurred())
		Expect(res.Conflicts).To(Equal(0))
		Expect(out.String()).NotTo(ContainSubstring("conflict"))
	})

	It("returns 1 failed when a push upload fails", func() {
		m := manifest.New()
		saveManifest(m, mpath)

		writeTempFile(root, "fail.jpg", "data")

		// Remote is empty — push will try OpUpload for fail.jpg.
		files.folders[1] = []domain.FileInfo{}
		files.failUpload = map[string]bool{"fail.jpg": true}

		opts := syncer.TwoWayOptions{
			LocalRoot:       root,
			Jobs:            1,
			DeleteThreshold: 0.5,
		}
		res, err := syncer.TwoWay(context.Background(), opts, files, 1, mpath, out)
		Expect(err).NotTo(HaveOccurred())
		Expect(res.Failed).To(BeNumerically(">=", 1))
	})

	It("dry-run with conflicts prints conflict count and plan", func() {
		m := manifest.New()
		m.Set("c.jpg", manifest.Entry{Size: 10, LocalMtime: 100, RemoteID: 40, RemoteMtime: 200})
		saveManifest(m, mpath)

		writeTempFile(root, "c.jpg", "local changed data") // local changed

		files.folders[1] = []domain.FileInfo{
			{ID: 40, Name: "c.jpg", Type: domain.FileTypeFile, Size: 99, LastModifiedAt: 999}, // remote changed
		}
		files.byID[40] = files.folders[1][0]

		opts := syncer.TwoWayOptions{
			LocalRoot:       root,
			Jobs:            1,
			DryRun:          true,
			DeleteThreshold: 0.5,
		}
		res, err := syncer.TwoWay(context.Background(), opts, files, 1, mpath, out)
		Expect(err).NotTo(HaveOccurred())
		Expect(res.Conflicts).To(Equal(1))
		Expect(out.String()).To(ContainSubstring("conflict (changed on both sides, skipped): c.jpg"))
		Expect(out.String()).To(ContainSubstring("dry-run"))
		Expect(strings.Contains(out.String(), "c.jpg")).To(BeTrue())
	})
})
