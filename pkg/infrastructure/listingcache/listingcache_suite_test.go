package listingcache

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestListingCache(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "ListingCache Suite")
}
