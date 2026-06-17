package syncer_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/stillsource/kdrive-fuse/pkg/infrastructure/manifest"
	"github.com/stillsource/kdrive-fuse/pkg/infrastructure/remoteindex"
	"github.com/stillsource/kdrive-fuse/pkg/syncer"
)

func pullByRel(items []syncer.PullItem) map[string]syncer.PullItem {
	m := map[string]syncer.PullItem{}
	for _, it := range items {
		m[it.Rel] = it
	}
	return m
}

var _ = Describe("PlanPull", func() {
	It("downloads remote files absent from the manifest", func() {
		m := manifest.New()
		idx := map[string]remoteindex.Entry{"a.jpg": {ID: 7, Size: 10, Mtime: 100}}
		items := syncer.PlanPull(idx, m)
		Expect(items).To(HaveLen(1))
		Expect(items[0]).To(Equal(syncer.PullItem{Rel: "a.jpg", Op: syncer.PullDownload, RemoteID: 7, Size: 10, RemoteMtime: 100}))
	})

	It("downloads when the remote size or mtime differs from the manifest", func() {
		m := manifest.New()
		m.Set("a.jpg", manifest.Entry{Size: 10, RemoteMtime: 100, RemoteID: 7})
		m.Set("b.jpg", manifest.Entry{Size: 5, RemoteMtime: 50, RemoteID: 8})
		idx := map[string]remoteindex.Entry{
			"a.jpg": {ID: 7, Size: 11, Mtime: 100}, // size differs
			"b.jpg": {ID: 8, Size: 5, Mtime: 51},   // mtime differs
		}
		got := pullByRel(syncer.PlanPull(idx, m))
		Expect(got).To(HaveLen(2))
		Expect(got["a.jpg"].Op).To(Equal(syncer.PullDownload))
		Expect(got["b.jpg"].Op).To(Equal(syncer.PullDownload))
	})

	It("omits unchanged files", func() {
		m := manifest.New()
		m.Set("a.jpg", manifest.Entry{Size: 10, RemoteMtime: 100, RemoteID: 7})
		idx := map[string]remoteindex.Entry{"a.jpg": {ID: 7, Size: 10, Mtime: 100}}
		Expect(syncer.PlanPull(idx, m)).To(BeEmpty())
	})

	It("deletes local for manifest entries gone from the remote", func() {
		m := manifest.New()
		m.Set("gone.jpg", manifest.Entry{Size: 10, RemoteMtime: 100, RemoteID: 7})
		items := syncer.PlanPull(map[string]remoteindex.Entry{}, m)
		Expect(items).To(HaveLen(1))
		Expect(items[0]).To(Equal(syncer.PullItem{Rel: "gone.jpg", Op: syncer.PullDeleteLocal}))
	})
})
