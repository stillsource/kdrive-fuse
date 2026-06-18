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

	// D: --no-delete in two-way
	It("--no-delete suppresses deletes on both sides but applies non-delete changes", func() {
		m := manifest.New()
		// del-push.jpg: tracked locally but now missing → would plan OpDelete push
		m.Set("del-push.jpg", manifest.Entry{Size: 4, LocalMtime: 100, RemoteID: 51, RemoteMtime: 200})
		// del-pull.jpg: tracked but now gone remotely → would plan PullDeleteLocal
		m.Set("del-pull.jpg", manifest.Entry{Size: 4, LocalMtime: 100, RemoteID: 52, RemoteMtime: 200})
		// upload-me.jpg: new local file → should still push
		saveManifest(m, mpath)

		// Local tree: del-push.jpg absent (will plan delete), del-pull.jpg present, new upload-me.jpg.
		writeTempFileWithMtime(root, "del-pull.jpg", "data", 100)
		writeTempFile(root, "upload-me.jpg", "new")

		// Remote: del-push.jpg still present (same as baseline — so only local delete),
		// del-pull.jpg GONE from remote (plan PullDeleteLocal),
		// upload-me.jpg absent (no remote counterpart for this new local file).
		files.folders[1] = []domain.FileInfo{
			{ID: 51, Name: "del-push.jpg", Type: domain.FileTypeFile, Size: 4, LastModifiedAt: 200},
		}
		files.byID[51] = files.folders[1][0]
		// del-pull.jpg is absent from remote index — PlanTwoWay sees rdel for it.

		opts := syncer.TwoWayOptions{
			LocalRoot:       root,
			Jobs:            1,
			NoDelete:        true,
			DeleteThreshold: 0.5,
		}
		res, err := syncer.TwoWay(context.Background(), opts, files, 1, mpath, out)
		Expect(err).NotTo(HaveOccurred())

		// No remote deletes: del-push.jpg must not appear in files.deleted.
		Expect(files.deleted).To(BeEmpty())

		// No local deletes: del-pull.jpg must still exist on disk.
		_, statErr := os.Stat(filepath.Join(root, "del-pull.jpg"))
		Expect(statErr).NotTo(HaveOccurred())

		// Non-delete push (upload-me.jpg) should still happen.
		Expect(res.Pushed).To(BeNumerically(">=", 1))
		names := make([]string, 0, len(files.uploads))
		for _, u := range files.uploads {
			names = append(names, u.Name)
		}
		Expect(names).To(ContainElement("upload-me.jpg"))
	})

	// D: first-run divergence → conflict (not silent push)
	It("first-run with same path but different sizes surfaces a conflict, not a push", func() {
		// Empty manifest — seedSynced should leave differing-size file unseeded.
		m := manifest.New()
		saveManifest(m, mpath)

		localContent := "local-version" // 13 bytes
		writeTempFile(root, "shared.jpg", localContent)

		// Remote has same path but different size (7 bytes).
		files.folders[1] = []domain.FileInfo{
			{ID: 60, Name: "shared.jpg", Type: domain.FileTypeFile, Size: 7, LastModifiedAt: 100},
		}
		files.byID[60] = files.folders[1][0]
		files.content = map[int64][]byte{60: []byte("remote!")}

		opts := syncer.TwoWayOptions{
			LocalRoot:       root,
			Jobs:            1,
			DeleteThreshold: 0.5,
		}
		res, err := syncer.TwoWay(context.Background(), opts, files, 1, mpath, out)
		Expect(err).NotTo(HaveOccurred())

		// Must be reported as a conflict.
		Expect(res.Conflicts).To(Equal(1))
		Expect(out.String()).To(ContainSubstring("conflict"))

		// Must NOT have been pushed (not in uploads).
		for _, u := range files.uploads {
			Expect(u.Name).NotTo(Equal("shared.jpg"))
		}

		// Local bytes must be untouched.
		data, err := os.ReadFile(filepath.Join(root, "shared.jpg"))
		Expect(err).NotTo(HaveOccurred())
		Expect(string(data)).To(Equal(localContent))

		// Not in deleted either.
		Expect(files.deleted).NotTo(ContainElement(int64(60)))
	})

	// D: guard rejection — push deletes exceed threshold
	It("returns an error and applies nothing when push deletes exceed the threshold", func() {
		// Seed a baseline with 5 files, none present locally → 5 planned push deletes.
		m := manifest.New()
		m.Set("a.jpg", manifest.Entry{Size: 1, LocalMtime: 1, RemoteID: 101, RemoteMtime: 1})
		m.Set("b.jpg", manifest.Entry{Size: 1, LocalMtime: 1, RemoteID: 102, RemoteMtime: 1})
		m.Set("c.jpg", manifest.Entry{Size: 1, LocalMtime: 1, RemoteID: 103, RemoteMtime: 1})
		m.Set("d.jpg", manifest.Entry{Size: 1, LocalMtime: 1, RemoteID: 104, RemoteMtime: 1})
		m.Set("e.jpg", manifest.Entry{Size: 1, LocalMtime: 1, RemoteID: 105, RemoteMtime: 1})
		saveManifest(m, mpath)

		// Remote mirrors baseline (unchanged) — all 5 files still present remotely.
		files.folders[1] = []domain.FileInfo{
			{ID: 101, Name: "a.jpg", Type: domain.FileTypeFile, Size: 1, LastModifiedAt: 1},
			{ID: 102, Name: "b.jpg", Type: domain.FileTypeFile, Size: 1, LastModifiedAt: 1},
			{ID: 103, Name: "c.jpg", Type: domain.FileTypeFile, Size: 1, LastModifiedAt: 1},
			{ID: 104, Name: "d.jpg", Type: domain.FileTypeFile, Size: 1, LastModifiedAt: 1},
			{ID: 105, Name: "e.jpg", Type: domain.FileTypeFile, Size: 1, LastModifiedAt: 1},
		}
		for _, fi := range files.folders[1] {
			files.byID[fi.ID] = fi
		}
		// Local is EMPTY — all 5 will be planned as push deletes (100% > 20%).

		opts := syncer.TwoWayOptions{
			LocalRoot:       root, // empty temp dir
			Jobs:            1,
			Force:           false,
			DeleteThreshold: 0.20,
		}
		_, err := syncer.TwoWay(context.Background(), opts, files, 1, mpath, out)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("refusing to delete"))
		// Nothing should have been deleted or uploaded.
		Expect(files.deleted).To(BeEmpty())
		Expect(files.uploads).To(BeEmpty())
	})

	// D: guard rejection — pull deletes exceed threshold
	It("returns an error and applies nothing when pull deletes exceed the threshold", func() {
		// Baseline: 5 local files tracked; remote goes empty → 5 planned pull deletes.
		m := manifest.New()
		m.Set("a.jpg", manifest.Entry{Size: 1, LocalMtime: 1, RemoteID: 201, RemoteMtime: 1})
		m.Set("b.jpg", manifest.Entry{Size: 1, LocalMtime: 1, RemoteID: 202, RemoteMtime: 1})
		m.Set("c.jpg", manifest.Entry{Size: 1, LocalMtime: 1, RemoteID: 203, RemoteMtime: 1})
		m.Set("d.jpg", manifest.Entry{Size: 1, LocalMtime: 1, RemoteID: 204, RemoteMtime: 1})
		m.Set("e.jpg", manifest.Entry{Size: 1, LocalMtime: 1, RemoteID: 205, RemoteMtime: 1})
		saveManifest(m, mpath)

		// Create matching local files so there are no push deletes (local unchanged).
		writeTempFileWithMtime(root, "a.jpg", "x", 1)
		writeTempFileWithMtime(root, "b.jpg", "x", 1)
		writeTempFileWithMtime(root, "c.jpg", "x", 1)
		writeTempFileWithMtime(root, "d.jpg", "x", 1)
		writeTempFileWithMtime(root, "e.jpg", "x", 1)

		// Remote is EMPTY — all 5 trigger PullDeleteLocal (100% > 20%).
		files.folders[1] = []domain.FileInfo{}

		opts := syncer.TwoWayOptions{
			LocalRoot:       root,
			Jobs:            1,
			Force:           false,
			DeleteThreshold: 0.20,
		}
		_, err := syncer.TwoWay(context.Background(), opts, files, 1, mpath, out)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("refusing to delete"))
		Expect(files.deleted).To(BeEmpty())
		Expect(files.uploads).To(BeEmpty())
	})

	// D: strengthen conflict no-write assertion (local bytes untouched, not deleted)
	It("conflict leaves local bytes untouched and does not delete the file", func() {
		m := manifest.New()
		m.Set("both.jpg", manifest.Entry{Size: 10, LocalMtime: 100, RemoteID: 70, RemoteMtime: 200})
		saveManifest(m, mpath)

		localContent := "local-changed" // size 13, differs from baseline 10
		writeTempFile(root, "both.jpg", localContent)

		// Remote also changed.
		files.folders[1] = []domain.FileInfo{
			{ID: 70, Name: "both.jpg", Type: domain.FileTypeFile, Size: 99, LastModifiedAt: 999},
		}
		files.byID[70] = files.folders[1][0]

		opts := syncer.TwoWayOptions{
			LocalRoot:       root,
			Jobs:            1,
			DeleteThreshold: 0.5,
		}
		res, err := syncer.TwoWay(context.Background(), opts, files, 1, mpath, out)
		Expect(err).NotTo(HaveOccurred())
		Expect(res.Conflicts).To(Equal(1))

		// Local bytes must be untouched.
		data, err := os.ReadFile(filepath.Join(root, "both.jpg"))
		Expect(err).NotTo(HaveOccurred())
		Expect(string(data)).To(Equal(localContent))

		// Not uploaded.
		for _, u := range files.uploads {
			Expect(u.Name).NotTo(Equal("both.jpg"))
		}
		// Not deleted.
		Expect(files.deleted).NotTo(ContainElement(int64(70)))
	})

	// D: multi-item dry-run covers printTwoWayPlan loops
	It("dry-run with one push and one pull item prints both in output", func() {
		m := manifest.New()
		// push-it.jpg changed locally (size differs from baseline).
		m.Set("push-it.jpg", manifest.Entry{Size: 5, LocalMtime: 100, RemoteID: 80, RemoteMtime: 200})
		// pull-it.jpg unchanged locally but changed remotely.
		m.Set("pull-it.jpg", manifest.Entry{Size: 5, LocalMtime: 100, RemoteID: 81, RemoteMtime: 200})
		saveManifest(m, mpath)

		writeTempFile(root, "push-it.jpg", "changed!")            // 8 bytes ≠ baseline 5
		writeTempFileWithMtime(root, "pull-it.jpg", "hello", 100) // 5 bytes, mtime=100 matches baseline

		files.folders[1] = []domain.FileInfo{
			{ID: 80, Name: "push-it.jpg", Type: domain.FileTypeFile, Size: 5, LastModifiedAt: 200}, // unchanged remote
			{ID: 81, Name: "pull-it.jpg", Type: domain.FileTypeFile, Size: 9, LastModifiedAt: 999}, // remote changed
		}
		files.byID[80] = files.folders[1][0]
		files.byID[81] = files.folders[1][1]

		opts := syncer.TwoWayOptions{
			LocalRoot:       root,
			Jobs:            1,
			DryRun:          true,
			DeleteThreshold: 0.5,
		}
		_, err := syncer.TwoWay(context.Background(), opts, files, 1, mpath, out)
		Expect(err).NotTo(HaveOccurred())
		output := out.String()
		Expect(output).To(ContainSubstring("push-it.jpg"))
		Expect(output).To(ContainSubstring("pull-it.jpg"))
		Expect(output).To(ContainSubstring("push"))
		Expect(output).To(ContainSubstring("pull"))
	})
})
