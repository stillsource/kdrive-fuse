package syncer_test

import (
	"context"
	"errors"
	"sync"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/stillsource/kdrive-fuse/pkg/infrastructure/manifest"
	"github.com/stillsource/kdrive-fuse/pkg/syncer"
)

// fakeExec is a concurrency-safe Executor that records calls and can fail
// selected paths.
type fakeExec struct {
	mu      sync.Mutex
	uploads []string
	over    []string
	dels    []string
	fail    map[string]bool
	nextID  int64
}

func (f *fakeExec) Upload(_ context.Context, rel string, _ int64) (int64, int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail[rel] {
		return 0, 0, errors.New("upload " + rel)
	}
	f.nextID++
	f.uploads = append(f.uploads, rel)
	return 5000 + f.nextID, 9000, nil
}

func (f *fakeExec) Overwrite(_ context.Context, rel string, remoteID, _ int64) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail[rel] {
		return 0, errors.New("overwrite " + rel)
	}
	f.over = append(f.over, rel)
	return 9100, nil
}

func (f *fakeExec) Delete(_ context.Context, rel string, remoteID int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail[rel] {
		return errors.New("delete " + rel)
	}
	f.dels = append(f.dels, rel)
	return nil
}

func (f *fakeExec) Move(_ context.Context, fromRel, toRel string, remoteID int64) error {
	return nil
}

var _ = Describe("RunPush", func() {
	It("executes each op and updates the manifest", func() {
		m := manifest.New()
		m.Set("gone.jpg", manifest.Entry{Size: 1, LocalMtime: 1, RemoteID: 70, RemoteMtime: 2})
		m.Set("edit.jpg", manifest.Entry{Size: 5, LocalMtime: 5, RemoteID: 80, RemoteMtime: 6})
		items := []syncer.Item{
			{Rel: "new.jpg", Op: syncer.OpUpload, Size: 10, Mtime: 100},
			{Rel: "edit.jpg", Op: syncer.OpOverwrite, RemoteID: 80, Size: 6, Mtime: 101},
			{Rel: "gone.jpg", Op: syncer.OpDelete, RemoteID: 70},
		}
		ex := &fakeExec{fail: map[string]bool{}}
		res := syncer.RunPush(context.Background(), items, ex, m, 4, nil)

		Expect(res.Uploaded).To(Equal(1))
		Expect(res.Overwritten).To(Equal(1))
		Expect(res.Deleted).To(Equal(1))
		Expect(res.Failed).To(Equal(0))

		nw, ok := m.Get("new.jpg")
		Expect(ok).To(BeTrue())
		Expect(nw).To(Equal(manifest.Entry{Size: 10, LocalMtime: 100, RemoteID: 5001, RemoteMtime: 9000}))
		ed, _ := m.Get("edit.jpg")
		Expect(ed).To(Equal(manifest.Entry{Size: 6, LocalMtime: 101, RemoteID: 80, RemoteMtime: 9100}))
		_, present := m.Get("gone.jpg")
		Expect(present).To(BeFalse())
	})

	It("records failures and leaves their manifest entries untouched", func() {
		m := manifest.New()
		m.Set("edit.jpg", manifest.Entry{Size: 5, LocalMtime: 5, RemoteID: 80, RemoteMtime: 6})
		items := []syncer.Item{
			{Rel: "new.jpg", Op: syncer.OpUpload, Size: 10, Mtime: 100},
			{Rel: "edit.jpg", Op: syncer.OpOverwrite, RemoteID: 80, Size: 6, Mtime: 101},
		}
		ex := &fakeExec{fail: map[string]bool{"new.jpg": true, "edit.jpg": true}}
		res := syncer.RunPush(context.Background(), items, ex, m, 2, nil)

		Expect(res.Failed).To(Equal(2))
		Expect(res.Errs).To(HaveLen(2))
		_, present := m.Get("new.jpg")
		Expect(present).To(BeFalse()) // failed upload not recorded
		ed, _ := m.Get("edit.jpg")
		Expect(ed.RemoteMtime).To(Equal(int64(6))) // unchanged baseline
	})

	It("handles an empty plan", func() {
		m := manifest.New()
		res := syncer.RunPush(context.Background(), nil, &fakeExec{fail: map[string]bool{}}, m, 4, nil)
		Expect(res).To(Equal(syncer.Result{}))
	})

	It("processes a large plan concurrently", func() {
		m := manifest.New()
		var items []syncer.Item
		for i := 0; i < 200; i++ {
			items = append(items, syncer.Item{Rel: relName(i), Op: syncer.OpUpload, Size: 1, Mtime: 1})
		}
		ex := &fakeExec{fail: map[string]bool{}}
		res := syncer.RunPush(context.Background(), items, ex, m, 8, nil)
		Expect(res.Uploaded).To(Equal(200))
		Expect(m.Len()).To(Equal(200))
	})

	It("checkpoints the manifest every 64 successful ops", func() {
		m := manifest.New()
		var items []syncer.Item
		for i := 0; i < 130; i++ {
			items = append(items, syncer.Item{Rel: relName(i), Op: syncer.OpUpload, Size: 1, Mtime: 1})
		}
		var lens []int // manifest length captured at each checkpoint
		res := syncer.RunPush(context.Background(), items, &fakeExec{fail: map[string]bool{}}, m, 8, func() {
			lens = append(lens, m.Len())
		})
		Expect(res.Uploaded).To(Equal(130))
		// 130 successes, checkpointInterval = 64 -> fires after the 64th and 128th.
		Expect(lens).To(Equal([]int{64, 128}))
	})
})

func relName(i int) string {
	return "f" + string(rune('0'+i%10)) + "-" + string(rune('a'+i/10%26)) + ".jpg"
}
