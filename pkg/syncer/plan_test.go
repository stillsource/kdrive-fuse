package syncer_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/stillsource/kdrive-fuse/pkg/infrastructure/manifest"
	"github.com/stillsource/kdrive-fuse/pkg/infrastructure/remoteindex"
	"github.com/stillsource/kdrive-fuse/pkg/syncer"
)

// byRel indexes a plan by relative path for order-independent assertions.
func byRel(items []syncer.Item) map[string]syncer.Item {
	m := map[string]syncer.Item{}
	for _, it := range items {
		m[it.Rel] = it
	}
	return m
}

var _ = Describe("PlanPush", func() {
	It("plans an upload for a file absent from the manifest", func() {
		m := manifest.New()
		items := syncer.PlanPush([]syncer.LocalFile{{Rel: "a.jpg", Size: 10, Mtime: 100}}, m)
		Expect(items).To(HaveLen(1))
		Expect(items[0]).To(Equal(syncer.Item{Rel: "a.jpg", Op: syncer.OpUpload, Size: 10, Mtime: 100}))
	})

	It("plans an overwrite when size or mtime differs", func() {
		m := manifest.New()
		m.Set("a.jpg", manifest.Entry{Size: 10, LocalMtime: 100, RemoteID: 7, RemoteMtime: 200})
		m.Set("b.jpg", manifest.Entry{Size: 5, LocalMtime: 1, RemoteID: 8, RemoteMtime: 2})
		items := syncer.PlanPush([]syncer.LocalFile{
			{Rel: "a.jpg", Size: 11, Mtime: 100}, // size changed
			{Rel: "b.jpg", Size: 5, Mtime: 9},    // mtime changed (same size)
		}, m)
		got := byRel(items)
		Expect(items).To(HaveLen(2))
		Expect(got["a.jpg"]).To(Equal(syncer.Item{Rel: "a.jpg", Op: syncer.OpOverwrite, RemoteID: 7, Size: 11, Mtime: 100}))
		Expect(got["b.jpg"]).To(Equal(syncer.Item{Rel: "b.jpg", Op: syncer.OpOverwrite, RemoteID: 8, Size: 5, Mtime: 9}))
	})

	It("omits unchanged files", func() {
		m := manifest.New()
		m.Set("a.jpg", manifest.Entry{Size: 10, LocalMtime: 100, RemoteID: 7, RemoteMtime: 200})
		items := syncer.PlanPush([]syncer.LocalFile{{Rel: "a.jpg", Size: 10, Mtime: 100}}, m)
		Expect(items).To(BeEmpty())
	})

	It("plans a delete for a manifest entry with no local file", func() {
		m := manifest.New()
		m.Set("gone.jpg", manifest.Entry{Size: 10, LocalMtime: 100, RemoteID: 7, RemoteMtime: 200})
		items := syncer.PlanPush(nil, m)
		Expect(items).To(HaveLen(1))
		Expect(items[0]).To(Equal(syncer.Item{Rel: "gone.jpg", Op: syncer.OpDelete, RemoteID: 7}))
	})
})

var _ = Describe("DetectMoves", func() {
	It("collapses a delete+upload with same size+mtime into an OpMove", func() {
		m := manifest.New()
		m.Set("old.jpg", manifest.Entry{Size: 10, LocalMtime: 100, RemoteID: 7, RemoteMtime: 200})
		// Plan: old.jpg is gone (delete) and new.jpg is new (upload) with matching size+mtime.
		items := []syncer.Item{
			{Rel: "old.jpg", Op: syncer.OpDelete, RemoteID: 7},
			{Rel: "new.jpg", Op: syncer.OpUpload, Size: 10, Mtime: 100},
		}
		got := syncer.DetectMoves(items, m)
		Expect(got).To(HaveLen(1))
		Expect(got[0].Op).To(Equal(syncer.OpMove))
		Expect(got[0].SrcRel).To(Equal("old.jpg"))
		Expect(got[0].Rel).To(Equal("new.jpg"))
		Expect(got[0].RemoteID).To(Equal(int64(7)))
		Expect(got[0].Size).To(Equal(int64(10)))
	})

	It("leaves ambiguous pairs (two deletes + two uploads sharing one key) as-is", func() {
		m := manifest.New()
		m.Set("a.jpg", manifest.Entry{Size: 10, LocalMtime: 100, RemoteID: 7, RemoteMtime: 200})
		m.Set("b.jpg", manifest.Entry{Size: 10, LocalMtime: 100, RemoteID: 8, RemoteMtime: 201})
		items := []syncer.Item{
			{Rel: "a.jpg", Op: syncer.OpDelete, RemoteID: 7},
			{Rel: "b.jpg", Op: syncer.OpDelete, RemoteID: 8},
			{Rel: "c.jpg", Op: syncer.OpUpload, Size: 10, Mtime: 100},
			{Rel: "d.jpg", Op: syncer.OpUpload, Size: 10, Mtime: 100},
		}
		got := syncer.DetectMoves(items, m)
		// All four original items preserved; no OpMove.
		Expect(got).To(HaveLen(4))
		for _, it := range got {
			Expect(it.Op).NotTo(Equal(syncer.OpMove))
		}
	})

	It("leaves a delete+upload with different size as-is (no move)", func() {
		m := manifest.New()
		m.Set("old.jpg", manifest.Entry{Size: 10, LocalMtime: 100, RemoteID: 7, RemoteMtime: 200})
		items := []syncer.Item{
			{Rel: "old.jpg", Op: syncer.OpDelete, RemoteID: 7},
			{Rel: "new.jpg", Op: syncer.OpUpload, Size: 99, Mtime: 100}, // different size
		}
		got := syncer.DetectMoves(items, m)
		Expect(got).To(HaveLen(2))
		for _, it := range got {
			Expect(it.Op).NotTo(Equal(syncer.OpMove))
		}
	})

	It("never pairs empty files (size == 0) to avoid (0,0) key collisions", func() {
		m := manifest.New()
		m.Set("empty-old.txt", manifest.Entry{Size: 0, LocalMtime: 100, RemoteID: 7, RemoteMtime: 200})
		items := []syncer.Item{
			{Rel: "empty-old.txt", Op: syncer.OpDelete, RemoteID: 7},
			{Rel: "empty-new.txt", Op: syncer.OpUpload, Size: 0, Mtime: 100},
		}
		got := syncer.DetectMoves(items, m)
		// Empty files must not be paired — falls back to delete+upload.
		Expect(got).To(HaveLen(2))
		for _, it := range got {
			Expect(it.Op).NotTo(Equal(syncer.OpMove))
		}
	})
})

var _ = Describe("Bootstrap", func() {
	It("seeds same-size files as unchanged and different-size as overwrite, leaving local-only as upload", func() {
		m := manifest.New()
		local := []syncer.LocalFile{
			{Rel: "same.jpg", Size: 10, Mtime: 100},
			{Rel: "diff.jpg", Size: 20, Mtime: 101},
			{Rel: "localonly.jpg", Size: 30, Mtime: 102},
		}
		idx := map[string]remoteindex.Entry{
			"same.jpg": {ID: 7, Size: 10, Mtime: 200},
			"diff.jpg": {ID: 8, Size: 99, Mtime: 201}, // remote size differs
		}
		syncer.Bootstrap(m, idx, local)

		items := byRel(syncer.PlanPush(local, m))
		Expect(items).NotTo(HaveKey("same.jpg")) // unchanged -> skipped
		Expect(items["diff.jpg"]).To(Equal(syncer.Item{Rel: "diff.jpg", Op: syncer.OpOverwrite, RemoteID: 8, Size: 20, Mtime: 101}))
		Expect(items["localonly.jpg"]).To(Equal(syncer.Item{Rel: "localonly.jpg", Op: syncer.OpUpload, Size: 30, Mtime: 102}))
	})
})
