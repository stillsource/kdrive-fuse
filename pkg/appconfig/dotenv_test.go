package appconfig

import (
	"context"
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("parseDotEnv", func() {
	It("parses KEY=VALUE, skipping blanks and comments", func() {
		m, err := parseDotEnv("# a comment\n\nKDRIVE_API_TOKEN=abc123\n   # indented comment\nKDRIVE_DRIVE_ID=42\n")
		Expect(err).NotTo(HaveOccurred())
		Expect(m).To(Equal(map[string]string{"KDRIVE_API_TOKEN": "abc123", "KDRIVE_DRIVE_ID": "42"}))
	})

	It("trims whitespace around key and value and strips a leading export", func() {
		m, err := parseDotEnv("export  KDRIVE_BASE_URL = https://example.test \n")
		Expect(err).NotTo(HaveOccurred())
		Expect(m).To(Equal(map[string]string{"KDRIVE_BASE_URL": "https://example.test"}))
	})

	It("strips one layer of matching quotes", func() {
		m, err := parseDotEnv("A=\"quoted value\"\nB='single'\nC=\"unbalanced\n")
		Expect(err).NotTo(HaveOccurred())
		Expect(m["A"]).To(Equal("quoted value"))
		Expect(m["B"]).To(Equal("single"))
		Expect(m["C"]).To(Equal("\"unbalanced")) // unbalanced quote left as-is
	})

	It("keeps a '#' inside a value literally (no inline-comment stripping)", func() {
		m, err := parseDotEnv("KDRIVE_API_TOKEN=tok#en\n")
		Expect(err).NotTo(HaveOccurred())
		Expect(m["KDRIVE_API_TOKEN"]).To(Equal("tok#en"))
	})

	It("accepts an empty value", func() {
		m, err := parseDotEnv("KDRIVE_METRICS_ADDR=\n")
		Expect(err).NotTo(HaveOccurred())
		Expect(m).To(HaveKeyWithValue("KDRIVE_METRICS_ADDR", ""))
	})

	It("errors on a line without '='", func() {
		_, err := parseDotEnv("KDRIVE_API_TOKEN\n")
		Expect(err).To(MatchError(ContainSubstring("expected KEY=VALUE")))
	})

	It("errors on an empty key", func() {
		_, err := parseDotEnv("=value\n")
		Expect(err).To(MatchError(ContainSubstring("empty key")))
	})
})

var _ = Describe("readEnvFile", func() {
	It("parses an existing file", func() {
		dir := GinkgoT().TempDir()
		path := filepath.Join(dir, ".env")
		Expect(os.WriteFile(path, []byte("KDRIVE_API_TOKEN=fromfile\n"), 0o600)).To(Succeed())
		m, err := readEnvFile(path, true)
		Expect(err).NotTo(HaveOccurred())
		Expect(m).To(HaveKeyWithValue("KDRIVE_API_TOKEN", "fromfile"))
	})

	It("returns nil for a missing default file (best-effort)", func() {
		m, err := readEnvFile(filepath.Join(GinkgoT().TempDir(), "absent.env"), false)
		Expect(err).NotTo(HaveOccurred())
		Expect(m).To(BeNil())
	})

	It("errors for a missing explicit file (user asked for it)", func() {
		_, err := readEnvFile(filepath.Join(GinkgoT().TempDir(), "absent.env"), true)
		Expect(err).To(HaveOccurred())
	})
})

var _ = Describe("dotEnvVars discovery", func() {
	It("reads the file named by KDRIVE_ENV_FILE", func() {
		dir := GinkgoT().TempDir()
		path := filepath.Join(dir, "custom.env")
		Expect(os.WriteFile(path, []byte("KDRIVE_API_TOKEN=fromcustom\n"), 0o600)).To(Succeed())
		GinkgoT().Setenv("KDRIVE_ENV_FILE", path)
		m, err := dotEnvVars()
		Expect(err).NotTo(HaveOccurred())
		Expect(m).To(HaveKeyWithValue("KDRIVE_API_TOKEN", "fromcustom"))
	})

	It("returns nil (best-effort) when KDRIVE_ENV_FILE is unset and no ./.env exists", func() {
		GinkgoT().Setenv("KDRIVE_ENV_FILE", "") // unset -> ".env" in the (test) working dir, which has none
		m, err := dotEnvVars()
		Expect(err).NotTo(HaveOccurred())
		Expect(m).To(BeNil())
	})

	It("makes Load fail when KDRIVE_ENV_FILE points at a missing file", func() {
		GinkgoT().Setenv("KDRIVE_ENV_FILE", filepath.Join(GinkgoT().TempDir(), "absent.env"))
		_, err := Load(context.Background())
		Expect(err).To(HaveOccurred())
	})
})

var _ = Describe("lookuperFor / .env precedence", func() {
	It("uses the OS lookuper alone when there are no file vars", func() {
		GinkgoT().Setenv("KDRIVE_API_TOKEN", "osTok")
		GinkgoT().Setenv("KDRIVE_DRIVE_ID", "7")
		c, err := load(context.Background(), lookuperFor(nil))
		Expect(err).NotTo(HaveOccurred())
		Expect(c.APIToken).To(Equal("osTok"))
		Expect(c.DriveID).To(Equal("7"))
	})

	It("lets a file value satisfy a required field the OS lacks", func() {
		GinkgoT().Setenv("KDRIVE_DRIVE_ID", "7")
		// KDRIVE_API_TOKEN intentionally not set in the OS env.
		c, err := load(context.Background(), lookuperFor(map[string]string{"KDRIVE_API_TOKEN": "fileTok"}))
		Expect(err).NotTo(HaveOccurred())
		Expect(c.APIToken).To(Equal("fileTok"))
	})

	It("prefers the OS value over the file value for the same key", func() {
		GinkgoT().Setenv("KDRIVE_API_TOKEN", "osWins")
		GinkgoT().Setenv("KDRIVE_DRIVE_ID", "7")
		c, err := load(context.Background(), lookuperFor(map[string]string{
			"KDRIVE_API_TOKEN": "fileLoses",
			"KDRIVE_DRIVE_ID":  "999",
		}))
		Expect(err).NotTo(HaveOccurred())
		Expect(c.APIToken).To(Equal("osWins"))
		Expect(c.DriveID).To(Equal("7"))
	})
})
