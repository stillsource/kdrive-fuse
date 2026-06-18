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

var _ = Describe("hardening: push data-integrity", func() {
	var (
		root  string
		mpath string
		files *recordingFiles
	)
	BeforeEach(func() {
		root = GinkgoT().TempDir()
		mpath = filepath.Join(GinkgoT().TempDir(), "m.tsv")
		files = &recordingFiles{folders: map[int64][]domain.FileInfo{}, failUpload: map[string]bool{}}
	})
	write := func(rel, data string) {
		p := filepath.Join(root, filepath.FromSlash(rel))
		Expect(os.MkdirAll(filepath.Dir(p), 0o755)).To(Succeed())
		Expect(os.WriteFile(p, []byte(data), 0o644)).To(Succeed())
	}
	opts := func() syncer.PushOptions { return syncer.PushOptions{LocalRoot: root, Jobs: 4} }

	It("re-uploads a previously-failed file on the next run (H1)", func() {
		write("a.jpg", "aaa")
		write("b.jpg", "bbb")
		files.failUpload["b.jpg"] = true
		res, err := syncer.Push(context.Background(), opts(), files, 1, mpath, &strings.Builder{})
		Expect(err).NotTo(HaveOccurred())
		Expect(res.Uploaded).To(Equal(1))
		Expect(res.Failed).To(Equal(1))
		m, err := manifest.Load(mpath)
		Expect(err).NotTo(HaveOccurred())
		_, okA := m.Get("a.jpg")
		_, okB := m.Get("b.jpg")
		Expect(okA).To(BeTrue())
		Expect(okB).To(BeFalse()) // failed upload not recorded

		// Second run: failure cleared -> exactly b.jpg re-uploads.
		files.failUpload = map[string]bool{}
		files.uploads = nil
		res2, err := syncer.Push(context.Background(), opts(), files, 1, mpath, &strings.Builder{})
		Expect(err).NotTo(HaveOccurred())
		Expect(res2.Uploaded).To(Equal(1))
		Expect(files.uploads).To(HaveLen(1))
		Expect(files.uploads[0].Name).To(Equal("b.jpg"))
	})

	It("overwrites a changed file by its remote id, not as a new upload (H4)", func() {
		write("a.jpg", "aaa")
		// Remote already has a.jpg at id 700 (same size) -> bootstrap tracks it.
		files.folders[1] = []domain.FileInfo{{ID: 700, Name: "a.jpg", Type: domain.FileTypeFile, Size: 3, LastModifiedAt: 100}}
		_, err := syncer.Push(context.Background(), opts(), files, 1, mpath, &strings.Builder{})
		Expect(err).NotTo(HaveOccurred())

		write("a.jpg", "aaaa") // size 4 != 3 -> overwrite
		files.uploads = nil
		res, err := syncer.Push(context.Background(), opts(), files, 1, mpath, &strings.Builder{})
		Expect(err).NotTo(HaveOccurred())
		Expect(res.Overwritten).To(Equal(1))
		Expect(files.uploads).To(HaveLen(1))
		Expect(files.uploads[0].ExistingFileID).To(Equal(int64(700))) // edit, not a duplicate create
		Expect(files.uploads[0].ParentID).To(BeZero())
	})

	It("persists successes and reports failures in a mixed plan (M2)", func() {
		write("ok1.jpg", "x")
		write("bad.jpg", "y")
		write("ok2.jpg", "z")
		files.failUpload["bad.jpg"] = true
		res, err := syncer.Push(context.Background(), opts(), files, 4, mpath, &strings.Builder{})
		Expect(err).NotTo(HaveOccurred())
		Expect(res.Uploaded).To(Equal(2))
		Expect(res.Failed).To(Equal(1))
		m, err := manifest.Load(mpath)
		Expect(err).NotTo(HaveOccurred())
		Expect(m.Len()).To(Equal(2)) // only the successes
	})
})

var _ = Describe("hardening: pull data-integrity", func() {
	var (
		root  string
		mpath string
		rem   *fakeRemote
	)
	BeforeEach(func() {
		root = GinkgoT().TempDir()
		mpath = filepath.Join(GinkgoT().TempDir(), "m.tsv")
		rem = &fakeRemote{
			folders: map[int64][]domain.FileInfo{
				1: {{ID: 7, Name: "a.jpg", Type: domain.FileTypeFile, Size: 5, LastModifiedAt: 100}},
			},
			content: map[int64][]byte{7: []byte("hello")},
		}
	})

	It("does not clobber an untracked local file on pull (H2)", func() {
		Expect(os.WriteFile(filepath.Join(root, "a.jpg"), []byte("MINE"), 0o644)).To(Succeed())
		var out strings.Builder
		res, err := syncer.Pull(context.Background(), syncer.PullOptions{LocalRoot: root, Jobs: 2}, rem, 1, mpath, &out)
		Expect(err).NotTo(HaveOccurred())
		Expect(res.Downloaded).To(Equal(0))
		Expect(out.String()).To(ContainSubstring("skip (local changed)"))
		data, err := os.ReadFile(filepath.Join(root, "a.jpg"))
		Expect(err).NotTo(HaveOccurred())
		Expect(string(data)).To(Equal("MINE")) // untracked file preserved, not overwritten
	})

	It("--force overwrites a drifted local file on pull (H3)", func() {
		Expect(os.WriteFile(filepath.Join(root, "a.jpg"), []byte("MINE"), 0o644)).To(Succeed())
		res, err := syncer.Pull(context.Background(), syncer.PullOptions{LocalRoot: root, Jobs: 2, Force: true}, rem, 1, mpath, &strings.Builder{})
		Expect(err).NotTo(HaveOccurred())
		Expect(res.Downloaded).To(Equal(1))
		data, err := os.ReadFile(filepath.Join(root, "a.jpg"))
		Expect(err).NotTo(HaveOccurred())
		Expect(string(data)).To(Equal("hello")) // --force took the remote copy
	})
})

var _ = Describe("hardening: cancellation and job clamps", func() {
	It("a cancelled context aborts RunPush without invoking the executor (F3)", func() {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		m := manifest.New()
		ex := &fakeExec{fail: map[string]bool{}}
		items := []syncer.Item{
			{Rel: "a", Op: syncer.OpUpload, Size: 1},
			{Rel: "b", Op: syncer.OpUpload, Size: 1},
		}
		res := syncer.RunPush(ctx, items, ex, m, 2, nil)
		Expect(res.Failed).To(Equal(2))
		Expect(res.Uploaded).To(Equal(0))
		Expect(ex.uploads).To(BeEmpty())
		Expect(m.Len()).To(Equal(0))
	})

	It("a cancelled context aborts RunPull without downloading (F3)", func() {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		m := manifest.New()
		actor := &fakePullActor{fail: map[string]bool{}}
		items := []syncer.PullItem{{Rel: "a", Op: syncer.PullDownload, RemoteID: 1}}
		res := syncer.RunPull(ctx, items, actor, m, 2)
		Expect(res.Failed).To(Equal(1))
		Expect(res.Downloaded).To(Equal(0))
		Expect(actor.downloads).To(BeEmpty())
	})

	It("RunPush clamps jobs<1 to 1 and still processes items (L1)", func() {
		m := manifest.New()
		ex := &fakeExec{fail: map[string]bool{}}
		res := syncer.RunPush(context.Background(), []syncer.Item{{Rel: "a", Op: syncer.OpUpload, Size: 1}}, ex, m, 0, nil)
		Expect(res.Uploaded).To(Equal(1))
	})

	It("RunPull clamps jobs<1 to 1 and still processes items (L1)", func() {
		m := manifest.New()
		actor := &fakePullActor{fail: map[string]bool{}}
		res := syncer.RunPull(context.Background(), []syncer.PullItem{{Rel: "a", Op: syncer.PullDeleteLocal}}, actor, m, 0)
		Expect(res.Deleted).To(Equal(1))
	})
})
