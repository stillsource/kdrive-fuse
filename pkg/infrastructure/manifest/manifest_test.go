package manifest_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/stillsource/kdrive-fuse/pkg/infrastructure/manifest"
)

var _ = Describe("Manifest", func() {
	It("starts empty", func() {
		m := manifest.New()
		Expect(m.Len()).To(Equal(0))
		_, ok := m.Get("a")
		Expect(ok).To(BeFalse())
	})

	It("sets and gets an entry", func() {
		m := manifest.New()
		e := manifest.Entry{Size: 10, LocalMtime: 100, RemoteID: 7, RemoteMtime: 200}
		m.Set("dir/a.jpg", e)
		got, ok := m.Get("dir/a.jpg")
		Expect(ok).To(BeTrue())
		Expect(got).To(Equal(e))
		Expect(m.Len()).To(Equal(1))
	})

	It("replaces an existing entry on Set", func() {
		m := manifest.New()
		m.Set("a", manifest.Entry{Size: 1})
		m.Set("a", manifest.Entry{Size: 2})
		got, _ := m.Get("a")
		Expect(got.Size).To(Equal(int64(2)))
		Expect(m.Len()).To(Equal(1))
	})

	It("deletes an entry and is a no-op for a missing key", func() {
		m := manifest.New()
		m.Set("a", manifest.Entry{Size: 1})
		m.Delete("a")
		_, ok := m.Get("a")
		Expect(ok).To(BeFalse())
		Expect(m.Len()).To(Equal(0))
		m.Delete("missing") // must not panic
	})

	It("ranges over all entries", func() {
		m := manifest.New()
		m.Set("a", manifest.Entry{Size: 1})
		m.Set("b", manifest.Entry{Size: 2})
		seen := map[string]int64{}
		m.Range(func(rel string, e manifest.Entry) { seen[rel] = e.Size })
		Expect(seen).To(Equal(map[string]int64{"a": 1, "b": 2}))
	})
})
