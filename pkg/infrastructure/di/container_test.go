package di_test

import (
	"log/slog"
	"os"
	"path/filepath"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/stillsource/kdrive-fuse/pkg/infrastructure/di"
)

func baseConfig(cacheDir string) di.Config {
	return di.Config{
		Token:          "test-token",
		DriveID:        "12345",
		RootFolderID:   1,
		CacheTTL:       time.Minute,
		DiskCacheDir:   cacheDir,
		DiskCacheBytes: 1 << 20,
		Logger:         slog.Default(),
	}
}

// blockedCacheDir returns a cache dir path whose creation must fail, because a
// parent path component is a regular file (os.MkdirAll cannot descend into it).
func blockedCacheDir() string {
	tmp := GinkgoT().TempDir()
	blocker := filepath.Join(tmp, "not-a-dir")
	Expect(os.WriteFile(blocker, []byte("x"), 0o600)).To(Succeed())
	return filepath.Join(blocker, "sub")
}

var _ = Describe("DI Container", func() {
	It("Client builds once and is memoized", func() {
		c := di.NewContainer(baseConfig(GinkgoT().TempDir()))
		first := c.Client()
		Expect(first).NotTo(BeNil())
		Expect(c.Client()).To(BeIdenticalTo(first))
	})

	It("Client applies base-URL overrides without panicking", func() {
		cfg := baseConfig(GinkgoT().TempDir())
		cfg.BaseURL = "https://example.test/2/drive"
		cfg.UploadBaseURL = "https://upload.example.test/2/drive"
		c := di.NewContainer(cfg)
		Expect(c.Client()).NotTo(BeNil())
	})

	It("ContentCache creates the cache dir and memoizes", func() {
		dir := filepath.Join(GinkgoT().TempDir(), "cache")
		c := di.NewContainer(baseConfig(dir))

		cache, err := c.ContentCache()
		Expect(err).NotTo(HaveOccurred())
		Expect(cache).NotTo(BeNil())

		info, statErr := os.Stat(dir)
		Expect(statErr).NotTo(HaveOccurred())
		Expect(info.IsDir()).To(BeTrue())

		again, err2 := c.ContentCache()
		Expect(err2).NotTo(HaveOccurred())
		Expect(again).To(BeIdenticalTo(cache))
	})

	It("ContentCache returns an error when the cache dir can't be created", func() {
		c := di.NewContainer(baseConfig(blockedCacheDir()))
		_, err := c.ContentCache()
		Expect(err).To(HaveOccurred())
	})

	It("KDriveFS wires all use cases and memoizes", func() {
		c := di.NewContainer(baseConfig(GinkgoT().TempDir()))

		kdfs, err := c.KDriveFS()
		Expect(err).NotTo(HaveOccurred())
		Expect(kdfs).NotTo(BeNil())
		Expect(kdfs.ListDir).NotTo(BeNil())
		Expect(kdfs.ReadFile).NotTo(BeNil())
		Expect(kdfs.SeedContent).NotTo(BeNil())
		Expect(kdfs.CommitWrite).NotTo(BeNil())
		Expect(kdfs.DeleteEntry).NotTo(BeNil())
		Expect(kdfs.RenameEntry).NotTo(BeNil())
		Expect(kdfs.MakeDir).NotTo(BeNil())

		again, err2 := c.KDriveFS()
		Expect(err2).NotTo(HaveOccurred())
		Expect(again).To(BeIdenticalTo(kdfs))
	})

	It("KDriveFS propagates a content-cache error", func() {
		c := di.NewContainer(baseConfig(blockedCacheDir()))
		_, err := c.KDriveFS()
		Expect(err).To(HaveOccurred())
	})

	It("RootNode returns a root directory node", func() {
		c := di.NewContainer(baseConfig(GinkgoT().TempDir()))
		root, err := c.RootNode()
		Expect(err).NotTo(HaveOccurred())
		Expect(root).NotTo(BeNil())
	})

	It("RootNode propagates a content-cache error", func() {
		c := di.NewContainer(baseConfig(blockedCacheDir()))
		_, err := c.RootNode()
		Expect(err).To(HaveOccurred())
	})
})
