package appconfig

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/sethvargo/go-envconfig"
)

var _ = Describe("Load from the OS environment", func() {
	It("reads the required vars set in the environment", func() {
		GinkgoT().Setenv("KDRIVE_API_TOKEN", "tok")
		GinkgoT().Setenv("KDRIVE_DRIVE_ID", "123")
		c, err := Load(context.Background())
		Expect(err).NotTo(HaveOccurred())
		Expect(c.APIToken).To(Equal("tok"))
		Expect(c.DriveID).To(Equal("123"))
	})

	It("errors when a numeric var is not an integer", func() {
		_, err := load(context.Background(), envconfig.MapLookuper(map[string]string{
			"KDRIVE_API_TOKEN":      "tok",
			"KDRIVE_DRIVE_ID":       "123",
			"KDRIVE_ROOT_FOLDER_ID": "not-a-number",
		}))
		Expect(err).To(HaveOccurred())
	})
})
