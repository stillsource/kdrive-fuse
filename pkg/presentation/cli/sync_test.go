package cli_test

import (
	"bytes"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/stillsource/kdrive-fuse/pkg/presentation/cli"
)

var _ = Describe("sync command flag handling", func() {
	var out, errb *bytes.Buffer
	BeforeEach(func() {
		out = &bytes.Buffer{}
		errb = &bytes.Buffer{}
	})

	It("prints sync help on --help and exits 0", func() {
		Expect(cli.Run([]string{"sync", "--help"}, "dev", out, errb)).To(Equal(0))
		Expect(out.String()).To(ContainSubstring("kdrive sync"))
		Expect(out.String()).To(ContainSubstring("--dry-run"))
	})

	It("rejects an unknown flag with exit 2", func() {
		Expect(cli.Run([]string{"sync", "--bogus"}, "dev", out, errb)).To(Equal(2))
		Expect(errb.String()).NotTo(BeEmpty())
	})

	It("rejects too many positional arguments with exit 2", func() {
		Expect(cli.Run([]string{"sync", "a", "b", "c"}, "dev", out, errb)).To(Equal(2))
	})
})
