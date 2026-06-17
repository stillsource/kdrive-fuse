package manifest_test

import (
	"path/filepath"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/stillsource/kdrive-fuse/pkg/infrastructure/manifest"
)

var _ = Describe("PathFor", func() {
	It("places the manifest under XDG_STATE_HOME/kdrive when set", func() {
		GinkgoT().Setenv("XDG_STATE_HOME", "/xdg/state")
		p, err := manifest.PathFor("/home/u/Pictures", "Rémanence")
		Expect(err).NotTo(HaveOccurred())
		Expect(p).To(HavePrefix("/xdg/state/kdrive/"))
		Expect(strings.HasSuffix(p, ".tsv")).To(BeTrue())
	})

	It("is stable for the same pair and differs across pairs", func() {
		GinkgoT().Setenv("XDG_STATE_HOME", "/xdg/state")
		a, _ := manifest.PathFor("/home/u/Pictures", "Rémanence")
		again, _ := manifest.PathFor("/home/u/Pictures", "Rémanence")
		other, _ := manifest.PathFor("/home/u/Pictures", "Other")
		Expect(a).To(Equal(again))
		Expect(a).NotTo(Equal(other))
	})

	It("falls back to ~/.local/state/kdrive when XDG_STATE_HOME is unset", func() {
		GinkgoT().Setenv("XDG_STATE_HOME", "")
		p, err := manifest.PathFor("rel/dir", "R")
		Expect(err).NotTo(HaveOccurred())
		Expect(p).To(ContainSubstring(filepath.Join(".local", "state", "kdrive")))
		Expect(strings.HasSuffix(p, ".tsv")).To(BeTrue())
	})
})
