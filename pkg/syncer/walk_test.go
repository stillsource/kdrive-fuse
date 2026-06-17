package syncer_test

import (
	"os"
	"path/filepath"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/stillsource/kdrive-fuse/pkg/syncer"
)

func relsOf(files []syncer.LocalFile) map[string]syncer.LocalFile {
	m := map[string]syncer.LocalFile{}
	for _, f := range files {
		m[f.Rel] = f
	}
	return m
}

var _ = Describe("WalkLocal", func() {
	var root string
	BeforeEach(func() { root = GinkgoT().TempDir() })

	write := func(rel string, data string) {
		p := filepath.Join(root, filepath.FromSlash(rel))
		Expect(os.MkdirAll(filepath.Dir(p), 0o755)).To(Succeed())
		Expect(os.WriteFile(p, []byte(data), 0o644)).To(Succeed())
	}

	It("returns regular files with slash relpaths, sizes and mtimes", func() {
		write("a.txt", "abc")
		write("2025/11/b.jpg", "hello")
		mtime := time.Unix(1700000000, 0)
		Expect(os.Chtimes(filepath.Join(root, "a.txt"), mtime, mtime)).To(Succeed())

		files, err := syncer.WalkLocal(root)
		Expect(err).NotTo(HaveOccurred())
		got := relsOf(files)
		Expect(got).To(HaveLen(2))
		Expect(got["a.txt"].Size).To(Equal(int64(3)))
		Expect(got["a.txt"].Mtime).To(Equal(int64(1700000000)))
		Expect(got["2025/11/b.jpg"].Size).To(Equal(int64(5)))
	})

	It("prunes .dtrash and \"à trier\"", func() {
		write("keep.txt", "x")
		write(".dtrash/old.txt", "x")
		write("à trier/pending.jpg", "x")
		files, err := syncer.WalkLocal(root)
		Expect(err).NotTo(HaveOccurred())
		got := relsOf(files)
		Expect(got).To(HaveKey("keep.txt"))
		Expect(got).NotTo(HaveKey(".dtrash/old.txt"))
		Expect(got).NotTo(HaveKey("à trier/pending.jpg"))
	})

	It("returns an error for a missing root", func() {
		_, err := syncer.WalkLocal(filepath.Join(root, "nope"))
		Expect(err).To(HaveOccurred())
	})
})
