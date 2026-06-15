package contentcache

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestContentCache(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "ContentCache Suite")
}
