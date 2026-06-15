package domain_test

import (
	"errors"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/stillsource/kdrive-fuse/pkg/domain"
)

var _ = Describe("ValidateName", func() {
	DescribeTable("rejects invalid names",
		func(name string) {
			err := domain.ValidateName(name)
			Expect(err).To(HaveOccurred())
			Expect(errors.Is(err, domain.ErrValidation)).To(BeTrue())
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
			Expect(domain.ValidateName(name)).To(Succeed())
		},
		Entry("simple", "foo.txt"),
		Entry("with spaces", "my file.pdf"),
		Entry("unicode", "Démarrage.txt"),
		Entry("emoji", "photo 🌞.jpg"),
		Entry("255 bytes", strings.Repeat("a", 255)),
		Entry("single dot prefix", ".hidden"),
	)
})

var _ = Describe("ValidateFolderID", func() {
	It("accepts positive", func() {
		Expect(domain.ValidateFolderID(1)).To(Succeed())
		Expect(domain.ValidateFolderID(9999)).To(Succeed())
	})
	It("rejects zero and negative", func() {
		Expect(errors.Is(domain.ValidateFolderID(0), domain.ErrValidation)).To(BeTrue())
		Expect(errors.Is(domain.ValidateFolderID(-1), domain.ErrValidation)).To(BeTrue())
	})
})

var _ = Describe("ValidateFileID", func() {
	It("accepts positive", func() {
		Expect(domain.ValidateFileID(42)).To(Succeed())
	})
	It("rejects zero and negative", func() {
		Expect(errors.Is(domain.ValidateFileID(0), domain.ErrValidation)).To(BeTrue())
		Expect(errors.Is(domain.ValidateFileID(-5), domain.ErrValidation)).To(BeTrue())
	})
})
