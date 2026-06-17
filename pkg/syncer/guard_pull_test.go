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
		Expect(syncer.GuardPullDeletes(dels(2), 10, false)).To(Succeed())
	})

	It("fails when local deletes exceed 20% of the baseline", func() {
		Expect(syncer.GuardPullDeletes(dels(3), 10, false)).To(HaveOccurred())
	})

	It("passes when forced", func() {
		Expect(syncer.GuardPullDeletes(dels(9), 10, true)).To(Succeed())
	})

	It("passes with an empty baseline", func() {
		Expect(syncer.GuardPullDeletes(dels(5), 0, false)).To(Succeed())
	})
})
