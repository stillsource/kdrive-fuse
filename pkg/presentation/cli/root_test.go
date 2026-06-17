package cli_test

import (
	"bytes"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/stillsource/kdrive-fuse/pkg/presentation/cli"
)

var _ = Describe("Run", func() {
	var out, errb *bytes.Buffer

	BeforeEach(func() {
		out = &bytes.Buffer{}
		errb = &bytes.Buffer{}
	})

	It("prints usage and exits 0 with no args", func() {
		Expect(cli.Run(nil, "dev", out, errb)).To(Equal(0))
		Expect(out.String()).To(ContainSubstring("Usage:"))
		Expect(errb.String()).To(BeEmpty())
	})

	It("prints usage on --help", func() {
		Expect(cli.Run([]string{"--help"}, "dev", out, errb)).To(Equal(0))
		Expect(out.String()).To(ContainSubstring("kdrive"))
		Expect(errb.String()).To(BeEmpty())
	})

	It("prints the version on --version", func() {
		Expect(cli.Run([]string{"--version"}, "1.2.3", out, errb)).To(Equal(0))
		Expect(out.String()).To(ContainSubstring("kdrive 1.2.3"))
		Expect(errb.String()).To(BeEmpty())
	})

	It("rejects an unknown command with exit 2 and prints usage", func() {
		Expect(cli.Run([]string{"bogus"}, "dev", out, errb)).To(Equal(2))
		Expect(errb.String()).To(ContainSubstring("unknown command"))
		Expect(errb.String()).To(ContainSubstring("Usage:")) // usage is shown on error
	})
})
