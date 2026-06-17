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
