package syncer_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/stillsource/kdrive-fuse/pkg/syncer"
)

var _ = Describe("GuardDeletes", func() {
	dels := func(n int) []syncer.Item {
		var items []syncer.Item
		for i := 0; i < n; i++ {
			items = append(items, syncer.Item{Op: syncer.OpDelete})
		}
		return items
	}

	It("passes when deletes are at or under 20% of the baseline", func() {
		Expect(syncer.GuardDeletes(dels(2), 10, false)).To(Succeed()) // 20%
	})

	It("fails when deletes exceed 20% of the baseline", func() {
		Expect(syncer.GuardDeletes(dels(3), 10, false)).To(HaveOccurred()) // 30%
	})

	It("passes when forced", func() {
		Expect(syncer.GuardDeletes(dels(9), 10, true)).To(Succeed())
	})

	It("passes with an empty baseline", func() {
		Expect(syncer.GuardDeletes(dels(5), 0, false)).To(Succeed())
	})
})
