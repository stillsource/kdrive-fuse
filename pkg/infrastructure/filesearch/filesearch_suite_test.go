package filesearch_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestFileSearch(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "FileSearch Suite")
}
