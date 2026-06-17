package manifest_test

import (
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/stillsource/kdrive-fuse/pkg/infrastructure/manifest"
)

var _ = Describe("Save hardening", func() {
	It("errors when the parent path is a regular file", func() {
		dir := GinkgoT().TempDir()
		notADir := filepath.Join(dir, "afile")
		Expect(os.WriteFile(notADir, []byte("x"), 0o644)).To(Succeed())

		m := manifest.New()
		m.Set("a", manifest.Entry{Size: 1})
		// Parent "afile" is a file, so MkdirAll for ".../afile/sub" must fail.
		err := m.Save(filepath.Join(notADir, "sub", "m.tsv"))
		Expect(err).To(HaveOccurred())
	})
})
