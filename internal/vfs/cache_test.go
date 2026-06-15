package vfs

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/stillsource/kdrive-fuse/pkg/domain"
)

var _ = Describe("DirCache", func() {
	var c *DirCache
	sample := []domain.FileInfo{{ID: 1, Name: "a"}, {ID: 2, Name: "b"}}

	BeforeEach(func() {
		c = NewDirCache(50 * time.Millisecond)
	})

	It("returns nothing for unknown keys", func() {
		_, ok := c.Get(42)
		Expect(ok).To(BeFalse())
	})

	It("returns stored value when not expired", func() {
		c.Set(1, sample)
		got, ok := c.Get(1)
		Expect(ok).To(BeTrue())
		Expect(got).To(Equal(sample))
	})

	It("expires entries past TTL", func() {
		c.Set(1, sample)
		time.Sleep(60 * time.Millisecond)
		_, ok := c.Get(1)
		Expect(ok).To(BeFalse())
	})

	It("Invalidate drops the entry", func() {
		c.Set(1, sample)
		c.Invalidate(1)
		_, ok := c.Get(1)
		Expect(ok).To(BeFalse())
	})
})
