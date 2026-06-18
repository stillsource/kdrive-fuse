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
		Expect(syncer.GuardDeletes(dels(2), 10, 0.20, false)).To(Succeed()) // 20%
	})

	It("fails when deletes exceed 20% of the baseline", func() {
		Expect(syncer.GuardDeletes(dels(3), 10, 0.20, false)).To(HaveOccurred()) // 30%
	})

	It("passes when forced", func() {
		Expect(syncer.GuardDeletes(dels(9), 10, 0.20, true)).To(Succeed())
	})

	It("passes with an empty baseline", func() {
		Expect(syncer.GuardDeletes(dels(5), 0, 0.20, false)).To(Succeed())
	})

	It("honors a custom threshold: 3/10 passes at 30% but 4/10 fails", func() {
		Expect(syncer.GuardDeletes(dels(3), 10, 0.30, false)).To(Succeed())
		Expect(syncer.GuardDeletes(dels(4), 10, 0.30, false)).To(HaveOccurred())
	})
})
