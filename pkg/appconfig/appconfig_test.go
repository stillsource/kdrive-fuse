package appconfig

import (
	"bytes"
	"context"
	"log/slog"
	"time"

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

var _ = Describe("Config helpers", func() {
	It("CacheDir returns the explicit dir when set", func() {
		c := &Config{DiskCacheDir: "/tmp/x"}
		Expect(c.CacheDir()).To(Equal("/tmp/x"))
	})

	It("CacheDir defaults under the home cache dir when unset", func() {
		c := &Config{}
		Expect(c.CacheDir()).To(HaveSuffix("/.cache/kdrive-fuse"))
	})

	It("DI maps to a di.Config with derived units", func() {
		c := &Config{
			APIToken: "tok", DriveID: "123", RootFolderID: 7,
			BaseURL: "b", UploadBaseURL: "u", CacheTTLSecs: 10,
			DiskCacheDir: "/c", DiskCacheMaxGB: 3,
		}
		log := slog.Default()
		d := c.DI(log)
		Expect(d.Token).To(Equal("tok"))
		Expect(d.DriveID).To(Equal("123"))
		Expect(d.RootFolderID).To(Equal(int64(7)))
		Expect(d.BaseURL).To(Equal("b"))
		Expect(d.UploadBaseURL).To(Equal("u"))
		Expect(d.CacheTTL).To(Equal(10 * time.Second))
		Expect(d.DiskCacheDir).To(Equal("/c"))
		Expect(d.DiskCacheBytes).To(Equal(int64(3) * 1024 * 1024 * 1024))
		Expect(d.Logger).To(Equal(log))
	})

	It("DI propagates ReadOnly=true when set", func() {
		c := &Config{
			APIToken: "tok", DriveID: "123",
			ReadOnly: true,
		}
		d := c.DI(slog.Default())
		Expect(d.ReadOnly).To(BeTrue())
	})

	It("DI propagates ReadOnly=false (default)", func() {
		c := &Config{APIToken: "tok", DriveID: "123"}
		d := c.DI(slog.Default())
		Expect(d.ReadOnly).To(BeFalse())
	})
})

var _ = Describe("KDRIVE_READONLY env", func() {
	ctx := context.Background()

	It("defaults to false when unset", func() {
		c, err := load(ctx, envconfig.MapLookuper(map[string]string{
			"KDRIVE_API_TOKEN": "tok",
			"KDRIVE_DRIVE_ID":  "123",
		}))
		Expect(err).NotTo(HaveOccurred())
		Expect(c.ReadOnly).To(BeFalse())
	})

	It("is true when KDRIVE_READONLY=1", func() {
		c, err := load(ctx, envconfig.MapLookuper(map[string]string{
			"KDRIVE_API_TOKEN": "tok",
			"KDRIVE_DRIVE_ID":  "123",
			"KDRIVE_READONLY":  "1",
		}))
		Expect(err).NotTo(HaveOccurred())
		Expect(c.ReadOnly).To(BeTrue())
	})

	It("is true when KDRIVE_READONLY=true", func() {
		c, err := load(ctx, envconfig.MapLookuper(map[string]string{
			"KDRIVE_API_TOKEN": "tok",
			"KDRIVE_DRIVE_ID":  "123",
			"KDRIVE_READONLY":  "true",
		}))
		Expect(err).NotTo(HaveOccurred())
		Expect(c.ReadOnly).To(BeTrue())
	})
})

var _ = Describe("KDRIVE_LOG_FORMAT env", func() {
	ctx := context.Background()

	It("defaults to text when unset", func() {
		c, err := load(ctx, envconfig.MapLookuper(map[string]string{
			"KDRIVE_API_TOKEN": "tok",
			"KDRIVE_DRIVE_ID":  "123",
		}))
		Expect(err).NotTo(HaveOccurred())
		Expect(c.LogFormat).To(Equal("text"))
	})

	It("parses json when KDRIVE_LOG_FORMAT=json", func() {
		c, err := load(ctx, envconfig.MapLookuper(map[string]string{
			"KDRIVE_API_TOKEN":  "tok",
			"KDRIVE_DRIVE_ID":   "123",
			"KDRIVE_LOG_FORMAT": "json",
		}))
		Expect(err).NotTo(HaveOccurred())
		Expect(c.LogFormat).To(Equal("json"))
	})
})

var _ = Describe("Config.NewLogger", func() {
	It("returns a JSON logger when LogFormat is json", func() {
		var buf bytes.Buffer
		c := &Config{LogFormat: "json"}
		logger := c.NewLogger(&buf)
		logger.Info("hello")
		line := buf.String()
		Expect(line).To(HavePrefix("{"))
		Expect(line).To(ContainSubstring(`"msg"`))
	})

	It("returns a text logger when LogFormat is text", func() {
		var buf bytes.Buffer
		c := &Config{LogFormat: "text"}
		logger := c.NewLogger(&buf)
		logger.Info("hello")
		line := buf.String()
		Expect(line).To(ContainSubstring("msg="))
		Expect(line).NotTo(HavePrefix("{"))
	})

	It("falls back to text for an unrecognized value", func() {
		var buf bytes.Buffer
		c := &Config{LogFormat: "xml"}
		logger := c.NewLogger(&buf)
		logger.Info("hello")
		line := buf.String()
		Expect(line).To(ContainSubstring("msg="))
		Expect(line).NotTo(HavePrefix("{"))
	})

	It("is case-insensitive for JSON", func() {
		var buf bytes.Buffer
		c := &Config{LogFormat: "JSON"}
		logger := c.NewLogger(&buf)
		logger.Info("hello")
		line := buf.String()
		Expect(line).To(HavePrefix("{"))
		Expect(line).To(ContainSubstring(`"msg"`))
	})
})
