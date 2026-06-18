package syncer_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/stillsource/kdrive-fuse/pkg/infrastructure/manifest"
	"github.com/stillsource/kdrive-fuse/pkg/infrastructure/remoteindex"
	"github.com/stillsource/kdrive-fuse/pkg/syncer"
)

var _ = Describe("PlanTwoWay", func() {
	// Helper: build a manifest with a single entry
	makeManifest := func(entries map[string]manifest.Entry) *manifest.Manifest {
		m := manifest.New()
		for rel, e := range entries {
			m.Set(rel, e)
		}
		return m
	}

	It("local-only modified (remote == baseline) → Push OpOverwrite; no pull, no conflict", func() {
		m := makeManifest(map[string]manifest.Entry{
			"a.jpg": {Size: 10, LocalMtime: 100, RemoteID: 7, RemoteMtime: 200},
		})
		local := []syncer.LocalFile{{Rel: "a.jpg", Size: 15, Mtime: 100}} // size changed
		idx := map[string]remoteindex.Entry{
			"a.jpg": {ID: 7, Size: 10, Mtime: 200}, // remote unchanged
		}
		plan := syncer.PlanTwoWay(local, idx, m)
		Expect(plan.Push).To(HaveLen(1))
		Expect(plan.Push[0].Op).To(Equal(syncer.OpOverwrite))
		Expect(plan.Push[0].Rel).To(Equal("a.jpg"))
		Expect(plan.Push[0].RemoteID).To(Equal(int64(7)))
		Expect(plan.Pull).To(BeEmpty())
		Expect(plan.Conflicts).To(BeEmpty())
	})

	It("remote-only modified (local == baseline) → Pull PullDownload; no push, no conflict", func() {
		m := makeManifest(map[string]manifest.Entry{
			"b.jpg": {Size: 10, LocalMtime: 100, RemoteID: 8, RemoteMtime: 200},
		})
		local := []syncer.LocalFile{{Rel: "b.jpg", Size: 10, Mtime: 100}} // unchanged locally
		idx := map[string]remoteindex.Entry{
			"b.jpg": {ID: 8, Size: 20, Mtime: 300}, // remote changed
		}
		plan := syncer.PlanTwoWay(local, idx, m)
		Expect(plan.Pull).To(HaveLen(1))
		Expect(plan.Pull[0].Op).To(Equal(syncer.PullDownload))
		Expect(plan.Pull[0].Rel).To(Equal("b.jpg"))
		Expect(plan.Push).To(BeEmpty())
		Expect(plan.Conflicts).To(BeEmpty())
	})

	It("both unchanged → empty plan", func() {
		m := makeManifest(map[string]manifest.Entry{
			"c.jpg": {Size: 10, LocalMtime: 100, RemoteID: 9, RemoteMtime: 200},
		})
		local := []syncer.LocalFile{{Rel: "c.jpg", Size: 10, Mtime: 100}}
		idx := map[string]remoteindex.Entry{
			"c.jpg": {ID: 9, Size: 10, Mtime: 200},
		}
		plan := syncer.PlanTwoWay(local, idx, m)
		Expect(plan.Push).To(BeEmpty())
		Expect(plan.Pull).To(BeEmpty())
		Expect(plan.Conflicts).To(BeEmpty())
	})

	It("both modified → Conflicts entry; no push, no pull", func() {
		m := makeManifest(map[string]manifest.Entry{
			"d.jpg": {Size: 10, LocalMtime: 100, RemoteID: 10, RemoteMtime: 200},
		})
		local := []syncer.LocalFile{{Rel: "d.jpg", Size: 15, Mtime: 110}} // local changed
		idx := map[string]remoteindex.Entry{
			"d.jpg": {ID: 10, Size: 12, Mtime: 300}, // remote also changed
		}
		plan := syncer.PlanTwoWay(local, idx, m)
		Expect(plan.Conflicts).To(ConsistOf("d.jpg"))
		Expect(plan.Push).To(BeEmpty())
		Expect(plan.Pull).To(BeEmpty())
	})

	It("local new only (absent remote, untracked) → Push OpUpload", func() {
		m := manifest.New() // empty manifest
		local := []syncer.LocalFile{{Rel: "new.jpg", Size: 5, Mtime: 50}}
		idx := map[string]remoteindex.Entry{} // no remote
		plan := syncer.PlanTwoWay(local, idx, m)
		Expect(plan.Push).To(HaveLen(1))
		Expect(plan.Push[0].Op).To(Equal(syncer.OpUpload))
		Expect(plan.Push[0].Rel).To(Equal("new.jpg"))
		Expect(plan.Pull).To(BeEmpty())
		Expect(plan.Conflicts).To(BeEmpty())
	})

	It("remote new only → Pull PullDownload", func() {
		m := manifest.New() // empty manifest
		local := []syncer.LocalFile{}
		idx := map[string]remoteindex.Entry{
			"remote-new.jpg": {ID: 11, Size: 8, Mtime: 400},
		}
		plan := syncer.PlanTwoWay(local, idx, m)
		Expect(plan.Pull).To(HaveLen(1))
		Expect(plan.Pull[0].Op).To(Equal(syncer.PullDownload))
		Expect(plan.Pull[0].Rel).To(Equal("remote-new.jpg"))
		Expect(plan.Push).To(BeEmpty())
		Expect(plan.Conflicts).To(BeEmpty())
	})

	It("both new (same rel, untracked, present both, differing size) → Conflicts", func() {
		m := manifest.New() // empty manifest — both sides untracked
		local := []syncer.LocalFile{{Rel: "conflict.jpg", Size: 5, Mtime: 50}}
		idx := map[string]remoteindex.Entry{
			"conflict.jpg": {ID: 12, Size: 9, Mtime: 500}, // different size
		}
		plan := syncer.PlanTwoWay(local, idx, m)
		Expect(plan.Conflicts).To(ConsistOf("conflict.jpg"))
		Expect(plan.Push).To(BeEmpty())
		Expect(plan.Pull).To(BeEmpty())
	})

	It("local deleted, remote unchanged → Push OpDelete", func() {
		m := makeManifest(map[string]manifest.Entry{
			"old.jpg": {Size: 10, LocalMtime: 100, RemoteID: 13, RemoteMtime: 200},
		})
		local := []syncer.LocalFile{} // local gone
		idx := map[string]remoteindex.Entry{
			"old.jpg": {ID: 13, Size: 10, Mtime: 200}, // remote unchanged
		}
		plan := syncer.PlanTwoWay(local, idx, m)
		Expect(plan.Push).To(HaveLen(1))
		Expect(plan.Push[0].Op).To(Equal(syncer.OpDelete))
		Expect(plan.Push[0].RemoteID).To(Equal(int64(13)))
		Expect(plan.Pull).To(BeEmpty())
		Expect(plan.Conflicts).To(BeEmpty())
	})

	It("remote deleted, local unchanged → Pull PullDeleteLocal", func() {
		m := makeManifest(map[string]manifest.Entry{
			"remote-gone.jpg": {Size: 10, LocalMtime: 100, RemoteID: 14, RemoteMtime: 200},
		})
		local := []syncer.LocalFile{{Rel: "remote-gone.jpg", Size: 10, Mtime: 100}} // local unchanged
		idx := map[string]remoteindex.Entry{}                                       // remote gone
		plan := syncer.PlanTwoWay(local, idx, m)
		Expect(plan.Pull).To(HaveLen(1))
		Expect(plan.Pull[0].Op).To(Equal(syncer.PullDeleteLocal))
		Expect(plan.Pull[0].Rel).To(Equal("remote-gone.jpg"))
		Expect(plan.Push).To(BeEmpty())
		Expect(plan.Conflicts).To(BeEmpty())
	})

	It("both deleted → Pull PullDeleteLocal (cleanup), NOT a conflict", func() {
		m := makeManifest(map[string]manifest.Entry{
			"both-gone.jpg": {Size: 10, LocalMtime: 100, RemoteID: 15, RemoteMtime: 200},
		})
		local := []syncer.LocalFile{}         // local gone
		idx := map[string]remoteindex.Entry{} // remote gone
		plan := syncer.PlanTwoWay(local, idx, m)
		Expect(plan.Pull).To(HaveLen(1))
		Expect(plan.Pull[0].Op).To(Equal(syncer.PullDeleteLocal))
		Expect(plan.Push).To(BeEmpty())
		Expect(plan.Conflicts).To(BeEmpty())
	})

	It("local deleted + remote modified → Conflicts", func() {
		m := makeManifest(map[string]manifest.Entry{
			"mixed1.jpg": {Size: 10, LocalMtime: 100, RemoteID: 16, RemoteMtime: 200},
		})
		local := []syncer.LocalFile{} // local deleted
		idx := map[string]remoteindex.Entry{
			"mixed1.jpg": {ID: 16, Size: 20, Mtime: 300}, // remote changed
		}
		plan := syncer.PlanTwoWay(local, idx, m)
		Expect(plan.Conflicts).To(ConsistOf("mixed1.jpg"))
		Expect(plan.Push).To(BeEmpty())
		Expect(plan.Pull).To(BeEmpty())
	})

	It("remote deleted + local modified → Conflicts", func() {
		m := makeManifest(map[string]manifest.Entry{
			"mixed2.jpg": {Size: 10, LocalMtime: 100, RemoteID: 17, RemoteMtime: 200},
		})
		local := []syncer.LocalFile{{Rel: "mixed2.jpg", Size: 15, Mtime: 110}} // local changed
		idx := map[string]remoteindex.Entry{}                                  // remote deleted
		plan := syncer.PlanTwoWay(local, idx, m)
		Expect(plan.Conflicts).To(ConsistOf("mixed2.jpg"))
		Expect(plan.Push).To(BeEmpty())
		Expect(plan.Pull).To(BeEmpty())
	})

	It("multiple paths are classified independently and conflicts are sorted", func() {
		m := makeManifest(map[string]manifest.Entry{
			"z.jpg": {Size: 10, LocalMtime: 100, RemoteID: 20, RemoteMtime: 200},
			"a.jpg": {Size: 10, LocalMtime: 100, RemoteID: 21, RemoteMtime: 200},
		})
		local := []syncer.LocalFile{
			{Rel: "z.jpg", Size: 15, Mtime: 110}, // local changed
			{Rel: "a.jpg", Size: 15, Mtime: 110}, // local changed
		}
		idx := map[string]remoteindex.Entry{
			"z.jpg": {ID: 20, Size: 12, Mtime: 300}, // remote also changed → conflict
			"a.jpg": {ID: 21, Size: 10, Mtime: 200}, // remote unchanged → push
		}
		plan := syncer.PlanTwoWay(local, idx, m)
		Expect(plan.Conflicts).To(Equal([]string{"z.jpg"})) // sorted
		push := byRel(plan.Push)
		Expect(push).To(HaveKey("a.jpg"))
		Expect(push["a.jpg"].Op).To(Equal(syncer.OpOverwrite))
	})

	It("pull item for remote-only modified carries correct metadata", func() {
		m := makeManifest(map[string]manifest.Entry{
			"meta.jpg": {Size: 10, LocalMtime: 100, RemoteID: 30, RemoteMtime: 200},
		})
		local := []syncer.LocalFile{{Rel: "meta.jpg", Size: 10, Mtime: 100}}
		idx := map[string]remoteindex.Entry{
			"meta.jpg": {ID: 30, Size: 25, Mtime: 999},
		}
		plan := syncer.PlanTwoWay(local, idx, m)
		Expect(plan.Pull).To(HaveLen(1))
		Expect(plan.Pull[0].RemoteID).To(Equal(int64(30)))
		Expect(plan.Pull[0].Size).To(Equal(int64(25)))
		Expect(plan.Pull[0].RemoteMtime).To(Equal(int64(999)))
	})
})
