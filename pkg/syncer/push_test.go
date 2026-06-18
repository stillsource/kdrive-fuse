package syncer_test

import (
	"context"
	"errors"
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

	It("dry-run labels overwrite and delete operations", func() {
		// First push: upload a.jpg so it is tracked in the manifest.
		writeLocal("a.jpg", "aaa")
		_, err := syncer.Push(context.Background(), opts(), files, 1, mpath, &strings.Builder{})
		Expect(err).NotTo(HaveOccurred())
		// Modify a.jpg (triggers overwrite) and remove it from root after the
		// dry-run opts are set so the plan has both overwrite and delete items.
		writeLocal("b.jpg", "bbb")
		_, err = syncer.Push(context.Background(), opts(), files, 1, mpath, &strings.Builder{})
		Expect(err).NotTo(HaveOccurred())
		// Now change a.jpg content (overwrite) and delete b.jpg (delete).
		writeLocal("a.jpg", "changed")
		Expect(os.Remove(filepath.Join(root, "b.jpg"))).To(Succeed())
		o := opts()
		o.DryRun = true
		o.Force = true
		var out strings.Builder
		_, err = syncer.Push(context.Background(), o, files, 1, mpath, &out)
		Expect(err).NotTo(HaveOccurred())
		plan := out.String()
		Expect(plan).To(ContainSubstring("overwrite"))
		Expect(plan).To(ContainSubstring("delete"))
	})

	It("returns an error when the local root does not exist", func() {
		_, err := syncer.Push(context.Background(), syncer.PushOptions{
			LocalRoot: filepath.Join(root, "nonexistent"),
			Jobs:      1,
		}, files, 1, mpath, &strings.Builder{})
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("walk"))
	})

	bumpRemoteMtime := func(name string, mtime int64) {
		for id, info := range files.byID {
			if info.Name == name {
				info.LastModifiedAt = mtime
				files.byID[id] = info
			}
		}
	}
	removeRemote := func(name string) {
		for id, info := range files.byID {
			if info.Name == name {
				delete(files.byID, id)
			}
		}
	}
	failStat := func(name string, err error) {
		for id, info := range files.byID {
			if info.Name == name {
				if files.statErr == nil {
					files.statErr = map[int64]error{}
				}
				files.statErr[id] = err
			}
		}
	}

	It("skips an overwrite when the remote changed out-of-band, and warns", func() {
		writeLocal("a.jpg", "aaa")
		_, err := syncer.Push(context.Background(), opts(), files, 1, mpath, &strings.Builder{})
		Expect(err).NotTo(HaveOccurred())
		bumpRemoteMtime("a.jpg", 9999) // out-of-band edit
		writeLocal("a.jpg", "changed") // local edit -> would overwrite
		files.uploads = nil
		var out strings.Builder
		res, err := syncer.Push(context.Background(), opts(), files, 1, mpath, &out)
		Expect(err).NotTo(HaveOccurred())
		Expect(res.Overwritten).To(Equal(0))
		Expect(files.uploads).To(BeEmpty()) // nothing clobbered
		Expect(out.String()).To(ContainSubstring("skip (remote changed): a.jpg"))
	})

	It("--force overrides push drift and overwrites anyway", func() {
		writeLocal("a.jpg", "aaa")
		_, err := syncer.Push(context.Background(), opts(), files, 1, mpath, &strings.Builder{})
		Expect(err).NotTo(HaveOccurred())
		bumpRemoteMtime("a.jpg", 9999)
		writeLocal("a.jpg", "changed")
		files.uploads = nil
		o := opts()
		o.Force = true
		res, err := syncer.Push(context.Background(), o, files, 1, mpath, &strings.Builder{})
		Expect(err).NotTo(HaveOccurred())
		Expect(res.Overwritten).To(Equal(1))
	})

	It("skips a delete when the remote changed out-of-band", func() {
		writeLocal("a.jpg", "aaa")
		_, err := syncer.Push(context.Background(), opts(), files, 1, mpath, &strings.Builder{})
		Expect(err).NotTo(HaveOccurred())
		bumpRemoteMtime("a.jpg", 9999)
		Expect(os.Remove(filepath.Join(root, "a.jpg"))).To(Succeed()) // local delete -> would delete remote
		var out strings.Builder
		res, err := syncer.Push(context.Background(), opts(), files, 1, mpath, &out)
		Expect(err).NotTo(HaveOccurred())
		Expect(res.Deleted).To(Equal(0))
		Expect(files.deleted).To(BeEmpty())
		Expect(out.String()).To(ContainSubstring("skip (remote changed): a.jpg"))
	})

	It("overwrites normally when the remote matches the manifest (no drift)", func() {
		writeLocal("a.jpg", "aaa")
		_, err := syncer.Push(context.Background(), opts(), files, 1, mpath, &strings.Builder{})
		Expect(err).NotTo(HaveOccurred())
		writeLocal("a.jpg", "changed") // local edit only; remote untouched
		files.uploads = nil
		res, err := syncer.Push(context.Background(), opts(), files, 1, mpath, &strings.Builder{})
		Expect(err).NotTo(HaveOccurred())
		Expect(res.Overwritten).To(Equal(1)) // no drift -> proceeds
	})

	It("skips a remote-gone overwrite and warns to re-upload", func() {
		writeLocal("a.jpg", "aaa")
		_, err := syncer.Push(context.Background(), opts(), files, 1, mpath, &strings.Builder{})
		Expect(err).NotTo(HaveOccurred())
		removeRemote("a.jpg")          // remote vanished out-of-band
		writeLocal("a.jpg", "changed") // local edit -> would overwrite
		files.uploads = nil
		var out strings.Builder
		res, err := syncer.Push(context.Background(), opts(), files, 1, mpath, &out)
		Expect(err).NotTo(HaveOccurred())
		Expect(res.Overwritten).To(Equal(0))
		Expect(files.uploads).To(BeEmpty())
		Expect(out.String()).To(ContainSubstring("skip (remote gone"))
	})

	It("aborts the push on an unexpected stat error", func() {
		writeLocal("a.jpg", "aaa")
		_, err := syncer.Push(context.Background(), opts(), files, 1, mpath, &strings.Builder{})
		Expect(err).NotTo(HaveOccurred())
		failStat("a.jpg", errors.New("stat boom"))
		writeLocal("a.jpg", "changed")
		_, err = syncer.Push(context.Background(), opts(), files, 1, mpath, &strings.Builder{})
		Expect(err).To(MatchError(ContainSubstring("stat boom")))
	})

	It("skips only the drifted item in a mixed plan", func() {
		writeLocal("a.jpg", "aaa")
		writeLocal("b.jpg", "bbb")
		_, err := syncer.Push(context.Background(), opts(), files, 1, mpath, &strings.Builder{})
		Expect(err).NotTo(HaveOccurred())
		bumpRemoteMtime("a.jpg", 9999) // a drifted; b clean
		writeLocal("a.jpg", "changed")
		writeLocal("b.jpg", "changed")
		files.uploads = nil
		var out strings.Builder
		res, err := syncer.Push(context.Background(), opts(), files, 1, mpath, &out)
		Expect(err).NotTo(HaveOccurred())
		Expect(res.Overwritten).To(Equal(1)) // only b
		Expect(out.String()).To(ContainSubstring("skip (remote changed): a.jpg"))
	})

	It("relocates a renamed file as a server-side move (no re-upload)", func() {
		writeLocal("a.jpg", "hello")
		// pin a deterministic mtime so the moved file matches the manifest baseline
		ts := time.Unix(1700000000, 0)
		Expect(os.Chtimes(filepath.Join(root, "a.jpg"), ts, ts)).To(Succeed())
		_, err := syncer.Push(context.Background(), opts(), files, 1, mpath, &strings.Builder{})
		Expect(err).NotTo(HaveOccurred())
		// rename locally (preserves size + mtime)
		Expect(os.Rename(filepath.Join(root, "a.jpg"), filepath.Join(root, "b.jpg"))).To(Succeed())
		files.uploads = nil
		res, err := syncer.Push(context.Background(), opts(), files, 1, mpath, &strings.Builder{})
		Expect(err).NotTo(HaveOccurred())
		Expect(res.Moved).To(Equal(1))
		Expect(res.Uploaded).To(Equal(0))
		Expect(res.Deleted).To(Equal(0))
		Expect(files.uploads).To(BeEmpty())  // not re-uploaded
		Expect(files.renamed).To(HaveLen(1)) // same folder -> rename only

		// Verify manifest re-key: old path gone, new path present with same RemoteID.
		mm, err := manifest.Load(mpath)
		Expect(err).NotTo(HaveOccurred())
		_, oldPresent := mm.Get("a.jpg")
		Expect(oldPresent).To(BeFalse())
		e, newPresent := mm.Get("b.jpg")
		Expect(newPresent).To(BeTrue())
		Expect(e.RemoteID).NotTo(BeZero())
	})

	It("moves a file across folders", func() {
		writeLocal("a.jpg", "hello")
		ts := time.Unix(1700000000, 0)
		Expect(os.Chtimes(filepath.Join(root, "a.jpg"), ts, ts)).To(Succeed())
		_, err := syncer.Push(context.Background(), opts(), files, 1, mpath, &strings.Builder{})
		Expect(err).NotTo(HaveOccurred())
		Expect(os.MkdirAll(filepath.Join(root, "sub"), 0o755)).To(Succeed())
		Expect(os.Rename(filepath.Join(root, "a.jpg"), filepath.Join(root, "sub", "a.jpg"))).To(Succeed())
		res, err := syncer.Push(context.Background(), opts(), files, 1, mpath, &strings.Builder{})
		Expect(err).NotTo(HaveOccurred())
		Expect(res.Moved).To(Equal(1))
		Expect(files.moved).To(HaveLen(1)) // different folder -> Move called
	})

	It("falls back to delete+upload when two files share size and mtime (ambiguous)", func() {
		writeLocal("a.jpg", "xxx")
		writeLocal("c.jpg", "yyy") // same size (3)
		ts := time.Unix(1700000000, 0)
		Expect(os.Chtimes(filepath.Join(root, "a.jpg"), ts, ts)).To(Succeed())
		Expect(os.Chtimes(filepath.Join(root, "c.jpg"), ts, ts)).To(Succeed()) // same mtime
		_, err := syncer.Push(context.Background(), opts(), files, 1, mpath, &strings.Builder{})
		Expect(err).NotTo(HaveOccurred())
		Expect(os.Rename(filepath.Join(root, "a.jpg"), filepath.Join(root, "a2.jpg"))).To(Succeed())
		Expect(os.Rename(filepath.Join(root, "c.jpg"), filepath.Join(root, "c2.jpg"))).To(Succeed())
		o := opts()
		o.Force = true // override delete guard: 2/2 = 100% deletion without force
		res, err := syncer.Push(context.Background(), o, files, 1, mpath, &strings.Builder{})
		Expect(err).NotTo(HaveOccurred())
		Expect(res.Moved).To(Equal(0)) // ambiguous -> no move
		Expect(res.Uploaded + res.Deleted).To(BeNumerically(">", 0))
	})
})
