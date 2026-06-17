package remoteindex_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestRemoteIndex(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "RemoteIndex Suite")
}
