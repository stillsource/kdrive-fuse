package fuse

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestFUSE(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "FUSE Suite")
}
