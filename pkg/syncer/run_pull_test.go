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

	"github.com/stillsource/kdrive-fuse/pkg/infrastructure/manifest"
	"github.com/stillsource/kdrive-fuse/pkg/syncer"
)

// fakeDownloader serves file content by id.
type fakeDownloader struct {
	content map[int64][]byte
}

func (f *fakeDownloader) DownloadStream(_ context.Context, fileID, _, _ int64) (io.ReadCloser, error) {
	b, ok := f.content[fileID]
	if !ok {
		return nil, errors.New("not found")
	}
	return io.NopCloser(bytes.NewReader(b)), nil
}

var _ = Describe("PullExecutor", func() {
	var root string
	BeforeEach(func() { root = GinkgoT().TempDir() })

	It("downloads into the local tree and returns size + mtime", func() {
		dl := &fakeDownloader{content: map[int64][]byte{7: []byte("hello")}}
		ex := syncer.NewPullExecutor(root, dl)
		size, mtime, err := ex.Download(context.Background(), "2025/a.jpg", 7)
		Expect(err).NotTo(HaveOccurred())
		Expect(size).To(Equal(int64(5)))
		Expect(mtime).NotTo(BeZero())
		data, err := os.ReadFile(filepath.Join(root, "2025", "a.jpg"))
		Expect(err).NotTo(HaveOccurred())
		Expect(string(data)).To(Equal("hello"))
	})

	It("errors when the remote content is unavailable", func() {
		ex := syncer.NewPullExecutor(root, &fakeDownloader{content: map[int64][]byte{}})
		_, _, err := ex.Download(context.Background(), "x.jpg", 99)
		Expect(err).To(HaveOccurred())
	})

	It("deletes a local file", func() {
		p := filepath.Join(root, "d.jpg")
		Expect(os.WriteFile(p, []byte("x"), 0o644)).To(Succeed())
		ex := syncer.NewPullExecutor(root, &fakeDownloader{})
		Expect(ex.DeleteLocal("d.jpg")).To(Succeed())
		_, err := os.Stat(p)
		Expect(os.IsNotExist(err)).To(BeTrue())
	})

	It("treats deleting an already-gone local file as success (idempotent re-run)", func() {
		ex := syncer.NewPullExecutor(root, &fakeDownloader{})
		Expect(ex.DeleteLocal("missing.jpg")).To(Succeed())
	})
})

// fakePullActor records actions and can fail selected paths.
type fakePullActor struct {
	mu        sync.Mutex
	downloads []string
	deletes   []string
	fail      map[string]bool
}

func (f *fakePullActor) Download(_ context.Context, rel string, _ int64) (int64, int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail[rel] {
		return 0, 0, errors.New("dl " + rel)
	}
	f.downloads = append(f.downloads, rel)
	return 3, 77, nil
}

func (f *fakePullActor) DeleteLocal(rel string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail[rel] {
		return errors.New("del " + rel)
	}
	f.deletes = append(f.deletes, rel)
	return nil
}

var _ = Describe("RunPull", func() {
	It("executes downloads + local deletes and updates the manifest", func() {
		m := manifest.New()
		m.Set("gone.jpg", manifest.Entry{Size: 1, RemoteID: 9, RemoteMtime: 2})
		items := []syncer.PullItem{
			{Rel: "new.jpg", Op: syncer.PullDownload, RemoteID: 7, Size: 10, RemoteMtime: 100},
			{Rel: "gone.jpg", Op: syncer.PullDeleteLocal},
		}
		actor := &fakePullActor{fail: map[string]bool{}}
		res := syncer.RunPull(context.Background(), items, actor, m, 4, nil)
		Expect(res.Downloaded).To(Equal(1))
		Expect(res.Deleted).To(Equal(1))
		Expect(res.Failed).To(Equal(0))
		got, ok := m.Get("new.jpg")
		Expect(ok).To(BeTrue())
		Expect(got).To(Equal(manifest.Entry{Size: 3, LocalMtime: 77, RemoteID: 7, RemoteMtime: 100}))
		_, present := m.Get("gone.jpg")
		Expect(present).To(BeFalse())
	})

	It("records failures and leaves the manifest untouched", func() {
		m := manifest.New()
		items := []syncer.PullItem{{Rel: "x.jpg", Op: syncer.PullDownload, RemoteID: 7}}
		actor := &fakePullActor{fail: map[string]bool{"x.jpg": true}}
		res := syncer.RunPull(context.Background(), items, actor, m, 2, nil)
		Expect(res.Failed).To(Equal(1))
		_, present := m.Get("x.jpg")
		Expect(present).To(BeFalse())
	})

	It("handles an empty plan", func() {
		m := manifest.New()
		Expect(syncer.RunPull(context.Background(), nil, &fakePullActor{fail: map[string]bool{}}, m, 4, nil)).To(Equal(syncer.PullResult{}))
	})

	It("checkpoints the manifest every 64 successful ops", func() {
		m := manifest.New()
		var items []syncer.PullItem
		for i := 0; i < 130; i++ {
			items = append(items, syncer.PullItem{Rel: relName(i), Op: syncer.PullDownload, RemoteID: int64(i + 1)})
		}
		var lens []int
		res := syncer.RunPull(context.Background(), items, &fakePullActor{fail: map[string]bool{}}, m, 8, func() {
			lens = append(lens, m.Len())
		})
		Expect(res.Downloaded).To(Equal(130))
		Expect(lens).To(Equal([]int{64, 128}))
	})
})
