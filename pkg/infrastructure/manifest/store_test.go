package manifest_test

import (
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/stillsource/kdrive-fuse/pkg/infrastructure/manifest"
)

var _ = Describe("Load and Save", func() {
	var dir string
	BeforeEach(func() { dir = GinkgoT().TempDir() })

	It("returns an empty manifest when the file is missing", func() {
		m, err := manifest.Load(filepath.Join(dir, "nope.tsv"))
		Expect(err).NotTo(HaveOccurred())
		Expect(m.Len()).To(Equal(0))
	})

	It("round-trips entries through Save and Load", func() {
		path := filepath.Join(dir, "m.tsv")
		m := manifest.New()
		m.Set("2025/a.jpg", manifest.Entry{Size: 10, LocalMtime: 100, RemoteID: 7, RemoteMtime: 200})
		m.Set("b.xmp", manifest.Entry{Size: 3, LocalMtime: 1, RemoteID: 8, RemoteMtime: 2})
		Expect(m.Save(path)).To(Succeed())

		got, err := manifest.Load(path)
		Expect(err).NotTo(HaveOccurred())
		Expect(got.Len()).To(Equal(2))
		a, ok := got.Get("2025/a.jpg")
		Expect(ok).To(BeTrue())
		Expect(a).To(Equal(manifest.Entry{Size: 10, LocalMtime: 100, RemoteID: 7, RemoteMtime: 200}))
	})

	It("creates the parent directory on Save", func() {
		path := filepath.Join(dir, "sub", "deep", "m.tsv")
		m := manifest.New()
		m.Set("a", manifest.Entry{Size: 1})
		Expect(m.Save(path)).To(Succeed())
		_, err := os.Stat(path)
		Expect(err).NotTo(HaveOccurred())
	})

	It("writes entries in sorted path order", func() {
		path := filepath.Join(dir, "m.tsv")
		m := manifest.New()
		m.Set("c", manifest.Entry{Size: 1})
		m.Set("a", manifest.Entry{Size: 1})
		m.Set("b", manifest.Entry{Size: 1})
		Expect(m.Save(path)).To(Succeed())
		data, err := os.ReadFile(path)
		Expect(err).NotTo(HaveOccurred())
		Expect(string(data)).To(Equal("1\t0\t0\t0\ta\n1\t0\t0\t0\tb\n1\t0\t0\t0\tc\n"))
	})

	It("preserves a tab inside a path name", func() {
		path := filepath.Join(dir, "m.tsv")
		rel := "weird\tname.jpg"
		m := manifest.New()
		m.Set(rel, manifest.Entry{Size: 5, LocalMtime: 1, RemoteID: 2, RemoteMtime: 3})
		Expect(m.Save(path)).To(Succeed())
		got, err := manifest.Load(path)
		Expect(err).NotTo(HaveOccurred())
		e, ok := got.Get(rel)
		Expect(ok).To(BeTrue())
		Expect(e.Size).To(Equal(int64(5)))
	})

	It("errors on a line with too few fields", func() {
		path := filepath.Join(dir, "bad.tsv")
		Expect(os.WriteFile(path, []byte("not\tenough\tfields\n"), 0o644)).To(Succeed())
		_, err := manifest.Load(path)
		Expect(err).To(HaveOccurred())
	})

	It("errors on a non-integer numeric field", func() {
		path := filepath.Join(dir, "bad2.tsv")
		Expect(os.WriteFile(path, []byte("x\t0\t0\t0\ta\n"), 0o644)).To(Succeed())
		_, err := manifest.Load(path)
		Expect(err).To(HaveOccurred())
	})
})
