package syncer_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/stillsource/kdrive-fuse/pkg/domain"
	"github.com/stillsource/kdrive-fuse/pkg/syncer"
)

var _ = Describe("Verify", func() {
	var root string
	BeforeEach(func() { root = GinkgoT().TempDir() })

	write := func(rel, data string) {
		p := filepath.Join(root, filepath.FromSlash(rel))
		Expect(os.MkdirAll(filepath.Dir(p), 0o755)).To(Succeed())
		Expect(os.WriteFile(p, []byte(data), 0o644)).To(Succeed())
	}

	It("reports ok, size diffs, and both-sided missing files", func() {
		write("ok.jpg", "abc")        // matches remote (size 3)
		write("big.jpg", "abcdef")    // remote says size 3 -> size diff
		write("localonly.jpg", "xyz") // absent remotely
		rem := &fakeRemote{
			folders: map[int64][]domain.FileInfo{
				1: {
					{ID: 1, Name: "ok.jpg", Type: domain.FileTypeFile, Size: 3},
					{ID: 2, Name: "big.jpg", Type: domain.FileTypeFile, Size: 3},
					{ID: 3, Name: "remoteonly.jpg", Type: domain.FileTypeFile, Size: 5},
				},
			},
		}
		var out strings.Builder
		res, err := syncer.Verify(context.Background(), root, rem, 1, &out)
		Expect(err).NotTo(HaveOccurred())
		Expect(res.OK).To(Equal(1))
		Expect(res.SizeDiff).To(Equal(1))
		Expect(res.MissingRemote).To(Equal(1)) // localonly.jpg
		Expect(res.MissingLocal).To(Equal(1))  // remoteonly.jpg
		Expect(res.Issues()).To(Equal(3))
		// Assert the specific lines, so a swap of the classification branches is caught.
		Expect(out.String()).To(ContainSubstring("MISSING remote   localonly.jpg"))
		Expect(out.String()).To(ContainSubstring("MISSING local    remoteonly.jpg"))
		Expect(out.String()).To(ContainSubstring("SIZE 6->3   big.jpg"))
	})

	It("treats a missing local root as empty (all remote files missing locally)", func() {
		rem := &fakeRemote{
			folders: map[int64][]domain.FileInfo{
				1: {{ID: 1, Name: "a.jpg", Type: domain.FileTypeFile, Size: 3}},
			},
		}
		res, err := syncer.Verify(context.Background(), filepath.Join(root, "does-not-exist"), rem, 1, &strings.Builder{})
		Expect(err).NotTo(HaveOccurred())
		Expect(res.MissingLocal).To(Equal(1))
		Expect(res.OK).To(Equal(0))
	})

	It("reports no issues when the trees match", func() {
		write("a.jpg", "hello")
		rem := &fakeRemote{
			folders: map[int64][]domain.FileInfo{
				1: {{ID: 1, Name: "a.jpg", Type: domain.FileTypeFile, Size: 5}},
			},
		}
		res, err := syncer.Verify(context.Background(), root, rem, 1, &strings.Builder{})
		Expect(err).NotTo(HaveOccurred())
		Expect(res.Issues()).To(Equal(0))
		Expect(res.OK).To(Equal(1))
	})
})
