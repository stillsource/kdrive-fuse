package kdrive

import (
	"errors"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("validateName", func() {
	DescribeTable("rejects invalid names",
		func(name string) {
			err := validateName(name)
			Expect(err).To(HaveOccurred())
			Expect(errors.Is(err, ErrValidation)).To(BeTrue())
		},
		Entry("empty", ""),
		Entry("slash", "foo/bar"),
		Entry("dot", "."),
		Entry("dotdot", ".."),
		Entry("null byte", "foo\x00bar"),
		Entry("newline", "foo\nbar"),
		Entry("carriage return", "foo\rbar"),
		Entry("tab (control)", "foo\tbar"),
		Entry("DEL", "foo\x7fbar"),
		Entry("too long", strings.Repeat("a", 256)),
	)

	DescribeTable("accepts valid names",
		func(name string) {
			Expect(validateName(name)).To(Succeed())
		},
		Entry("simple", "foo.txt"),
		Entry("with spaces", "my file.pdf"),
		Entry("unicode", "Démarrage.txt"),
		Entry("emoji", "photo 🌞.jpg"),
		Entry("255 bytes", strings.Repeat("a", 255)),
		Entry("single dot prefix", ".hidden"),
	)
})

var _ = Describe("validateFolderID", func() {
	It("accepts positive", func() {
		Expect(validateFolderID(1)).To(Succeed())
		Expect(validateFolderID(9999)).To(Succeed())
	})
	It("rejects zero and negative", func() {
		Expect(errors.Is(validateFolderID(0), ErrValidation)).To(BeTrue())
		Expect(errors.Is(validateFolderID(-1), ErrValidation)).To(BeTrue())
	})
})

var _ = Describe("validateFileID", func() {
	It("accepts positive", func() {
		Expect(validateFileID(42)).To(Succeed())
	})
	It("rejects zero and negative", func() {
		Expect(errors.Is(validateFileID(0), ErrValidation)).To(BeTrue())
		Expect(errors.Is(validateFileID(-5), ErrValidation)).To(BeTrue())
	})
})
