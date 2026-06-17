package appconfig

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/sethvargo/go-envconfig"
)

var _ = Describe("Load", func() {
	ctx := context.Background()

	It("applies defaults when only required vars are set", func() {
		c, err := load(ctx, envconfig.MapLookuper(map[string]string{
			"KDRIVE_API_TOKEN": "tok",
			"KDRIVE_DRIVE_ID":  "123",
		}))
		Expect(err).NotTo(HaveOccurred())
		Expect(c.APIToken).To(Equal("tok"))
		Expect(c.DriveID).To(Equal("123"))
		Expect(c.RootFolderID).To(Equal(int64(1)))
		Expect(c.BaseURL).To(Equal("https://api.infomaniak.com/2/drive"))
		Expect(c.UploadBaseURL).To(Equal("https://api.kdrive.infomaniak.com/2/drive"))
		Expect(c.CacheTTLSecs).To(Equal(30))
		Expect(c.DiskCacheMaxGB).To(Equal(2))
		Expect(c.DiskCacheDir).To(BeEmpty())
	})

	It("errors when a required var is missing", func() {
		_, err := load(ctx, envconfig.MapLookuper(map[string]string{
			"KDRIVE_API_TOKEN": "tok", // DRIVE_ID missing
		}))
		Expect(err).To(HaveOccurred())
	})

	It("honors overrides", func() {
		c, err := load(ctx, envconfig.MapLookuper(map[string]string{
			"KDRIVE_API_TOKEN":         "tok",
			"KDRIVE_DRIVE_ID":          "123",
			"KDRIVE_ROOT_FOLDER_ID":    "789",
			"KDRIVE_CACHE_TTL_SECONDS": "5",
		}))
		Expect(err).NotTo(HaveOccurred())
		Expect(c.RootFolderID).To(Equal(int64(789)))
		Expect(c.CacheTTLSecs).To(Equal(5))
	})
})
