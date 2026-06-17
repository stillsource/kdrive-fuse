package appconfig

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestAppconfig(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Appconfig Suite")
}
