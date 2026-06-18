package syncer_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/stillsource/kdrive-fuse/pkg/syncer"
)

var _ = Describe("GuardPullDeletes", func() {
	dels := func(n int) []syncer.PullItem {
		var items []syncer.PullItem
		for range n {
			items = append(items, syncer.PullItem{Op: syncer.PullDeleteLocal})
		}
		return items
	}

	It("passes when local deletes are at or under 20% of the baseline", func() {
		Expect(syncer.GuardPullDeletes(dels(2), 10, 0.20, false)).To(Succeed())
	})

	It("fails when local deletes exceed 20% of the baseline", func() {
		Expect(syncer.GuardPullDeletes(dels(3), 10, 0.20, false)).To(HaveOccurred())
	})

	It("passes when forced", func() {
		Expect(syncer.GuardPullDeletes(dels(9), 10, 0.20, true)).To(Succeed())
	})

	It("passes with an empty baseline", func() {
		Expect(syncer.GuardPullDeletes(dels(5), 0, 0.20, false)).To(Succeed())
	})

	It("honors a custom threshold: 3/10 passes at 30% but 4/10 fails", func() {
		Expect(syncer.GuardPullDeletes(dels(3), 10, 0.30, false)).To(Succeed())
		Expect(syncer.GuardPullDeletes(dels(4), 10, 0.30, false)).To(HaveOccurred())
	})
})
