package cli

import (
	"bytes"
	"context"
	"errors"
	"io"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/stillsource/kdrive-fuse/pkg/domain"
	"github.com/stillsource/kdrive-fuse/pkg/infrastructure/remoteindex"
	"github.com/stillsource/kdrive-fuse/pkg/service"
	"github.com/stillsource/kdrive-fuse/pkg/service/servicefakes"
)

// stubShareBackend replaces shareBackend with one that returns the given fakes
// and root id.
func stubShareBackend(sharer service.Sharer, lister remoteindex.Lister, rootID int64) func() {
	orig := shareBackend
	shareBackend = func(context.Context, io.Writer) (service.Sharer, remoteindex.Lister, int64, error) {
		return sharer, lister, rootID, nil
	}
	return func() { shareBackend = orig }
}

var _ = Describe("runShare with a fake backend", func() {
	var (
		sharer *servicefakes.SharesFake
		files  *servicefakes.FilesFake
		out    *bytes.Buffer
		errb   *bytes.Buffer
	)

	// rootID=1; tree: root -> dir(id=2) -> foo.jpg(id=3)
	//                      -> bar(id=4, dir) -> sub.jpg(id=5)
	//                      -> plain.jpg(id=6)
	BeforeEach(func() {
		sharer = &servicefakes.SharesFake{
			PublishResults: map[int64]servicefakes.PublishResult{
				3: {Info: domain.ShareInfo{ShareURL: "https://kdrive.example.com/share/abc"}},
				5: {Info: domain.ShareInfo{ShareURL: "https://kdrive.example.com/share/sub"}},
				6: {Info: domain.ShareInfo{ShareURL: "https://kdrive.example.com/share/plain"}},
			},
		}
		files = &servicefakes.FilesFake{
			ListResults: map[int64]servicefakes.ListResult{
				1: {Files: []domain.FileInfo{
					{ID: 2, Name: "dir", Type: domain.FileTypeDir},
					{ID: 4, Name: "bar", Type: domain.FileTypeDir},
					{ID: 6, Name: "plain.jpg", Type: domain.FileTypeFile},
				}},
				2: {Files: []domain.FileInfo{
					{ID: 3, Name: "foo.jpg", Type: domain.FileTypeFile},
				}},
				4: {Files: []domain.FileInfo{
					{ID: 5, Name: "sub.jpg", Type: domain.FileTypeFile},
				}},
			},
		}
		out = &bytes.Buffer{}
		errb = &bytes.Buffer{}
	})

	It("resolves a nested path and prints the share URL", func() {
		restore := stubShareBackend(sharer, files, 1)
		defer restore()

		code := runShare([]string{"dir/foo.jpg"}, out, errb)

		Expect(code).To(Equal(0))
		Expect(out.String()).To(ContainSubstring("https://kdrive.example.com/share/abc"))
		Expect(errb.String()).To(BeEmpty())
		Expect(sharer.PublishCalls).To(ConsistOf(int64(3)))
	})

	It("resolves a top-level file and prints the share URL", func() {
		restore := stubShareBackend(sharer, files, 1)
		defer restore()

		code := runShare([]string{"plain.jpg"}, out, errb)

		Expect(code).To(Equal(0))
		Expect(out.String()).To(ContainSubstring("https://kdrive.example.com/share/plain"))
		Expect(sharer.PublishCalls).To(ConsistOf(int64(6)))
	})

	It("trims leading and trailing slashes from the path", func() {
		restore := stubShareBackend(sharer, files, 1)
		defer restore()

		code := runShare([]string{"/dir/foo.jpg/"}, out, errb)

		Expect(code).To(Equal(0))
		Expect(out.String()).To(ContainSubstring("https://kdrive.example.com/share/abc"))
	})

	It("returns 1 and prints 'not found' when the path does not exist", func() {
		restore := stubShareBackend(sharer, files, 1)
		defer restore()

		code := runShare([]string{"dir/missing.jpg"}, out, errb)

		Expect(code).To(Equal(1))
		Expect(errb.String()).To(ContainSubstring("not found"))
		Expect(sharer.PublishCalls).To(BeEmpty())
	})

	It("returns 1 and prints 'not found' when an intermediate directory is missing", func() {
		restore := stubShareBackend(sharer, files, 1)
		defer restore()

		code := runShare([]string{"nosuchdir/foo.jpg"}, out, errb)

		Expect(code).To(Equal(1))
		Expect(errb.String()).To(ContainSubstring("not found"))
		Expect(sharer.PublishCalls).To(BeEmpty())
	})

	It("returns 1 when the path points at a directory", func() {
		restore := stubShareBackend(sharer, files, 1)
		defer restore()

		code := runShare([]string{"dir"}, out, errb)

		Expect(code).To(Equal(1))
		Expect(errb.String()).To(ContainSubstring("is a directory"))
		Expect(sharer.PublishCalls).To(BeEmpty())
	})

	It("returns 1 when a Publish error occurs", func() {
		sharer.PublishResults[6] = servicefakes.PublishResult{Err: errors.New("publish failed")}
		restore := stubShareBackend(sharer, files, 1)
		defer restore()

		code := runShare([]string{"plain.jpg"}, out, errb)

		Expect(code).To(Equal(1))
		Expect(errb.String()).To(ContainSubstring("publish failed"))
	})

	It("returns 1 when the backend fails", func() {
		orig := shareBackend
		shareBackend = func(context.Context, io.Writer) (service.Sharer, remoteindex.Lister, int64, error) {
			return nil, nil, 0, errors.New("no credentials")
		}
		defer func() { shareBackend = orig }()

		code := runShare([]string{"plain.jpg"}, out, errb)

		Expect(code).To(Equal(1))
		Expect(errb.String()).To(ContainSubstring("no credentials"))
	})
})

var _ = Describe("runShare flag handling", func() {
	var out, errb *bytes.Buffer
	BeforeEach(func() {
		out = &bytes.Buffer{}
		errb = &bytes.Buffer{}
	})

	It("prints share usage and exits 0 on --help", func() {
		code := runShare([]string{"--help"}, out, errb)
		Expect(code).To(Equal(0))
		Expect(out.String()).To(ContainSubstring("kdrive share"))
	})

	It("prints share usage and exits 0 on -h", func() {
		code := runShare([]string{"-h"}, out, errb)
		Expect(code).To(Equal(0))
		Expect(out.String()).To(ContainSubstring("kdrive share"))
	})

	It("exits 2 with usage when no path is given", func() {
		code := runShare([]string{}, out, errb)
		Expect(code).To(Equal(2))
		Expect(errb.String()).To(ContainSubstring("kdrive share"))
	})

	It("exits 2 with usage when more than one positional arg is given", func() {
		code := runShare([]string{"a", "b"}, out, errb)
		Expect(code).To(Equal(2))
		Expect(errb.String()).To(ContainSubstring("kdrive share"))
	})
})

var _ = Describe("Run dispatches to share", func() {
	var out, errb *bytes.Buffer
	BeforeEach(func() {
		out = &bytes.Buffer{}
		errb = &bytes.Buffer{}
	})

	It("routes 'share --help' to runShare and exits 0", func() {
		// Use the public Run entry point to confirm dispatch.
		code := Run([]string{"share", "--help"}, "dev", out, errb)
		Expect(code).To(Equal(0))
		Expect(out.String()).To(ContainSubstring("kdrive share"))
	})

	It("routes 'share' with no args to runShare and exits 2", func() {
		code := Run([]string{"share"}, "dev", out, errb)
		Expect(code).To(Equal(2))
	})
})

var _ = Describe("resolveFile", func() {
	var files *servicefakes.FilesFake

	BeforeEach(func() {
		files = &servicefakes.FilesFake{
			ListResults: map[int64]servicefakes.ListResult{
				1: {Files: []domain.FileInfo{
					{ID: 2, Name: "photos", Type: domain.FileTypeDir},
					{ID: 7, Name: "readme.txt", Type: domain.FileTypeFile},
				}},
				2: {Files: []domain.FileInfo{
					{ID: 3, Name: "cat.jpg", Type: domain.FileTypeFile},
					{ID: 4, Name: "sub", Type: domain.FileTypeDir},
				}},
				4: {Files: []domain.FileInfo{
					{ID: 5, Name: "deep.png", Type: domain.FileTypeFile},
				}},
			},
		}
	})

	It("resolves a top-level file", func() {
		id, err := resolveFile(context.Background(), files, 1, "readme.txt")
		Expect(err).NotTo(HaveOccurred())
		Expect(id).To(Equal(int64(7)))
	})

	It("resolves a two-level path", func() {
		id, err := resolveFile(context.Background(), files, 1, "photos/cat.jpg")
		Expect(err).NotTo(HaveOccurred())
		Expect(id).To(Equal(int64(3)))
	})

	It("resolves a three-level path", func() {
		id, err := resolveFile(context.Background(), files, 1, "photos/sub/deep.png")
		Expect(err).NotTo(HaveOccurred())
		Expect(id).To(Equal(int64(5)))
	})

	It("returns 'not found' for a missing file", func() {
		_, err := resolveFile(context.Background(), files, 1, "photos/ghost.jpg")
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("not found"))
	})

	It("returns 'not found' for a missing intermediate directory", func() {
		_, err := resolveFile(context.Background(), files, 1, "missing/cat.jpg")
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("not found"))
	})

	It("returns 'is a directory' when the path resolves to a dir", func() {
		_, err := resolveFile(context.Background(), files, 1, "photos")
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("is a directory"))
	})

	It("returns 'not found' when an intermediate segment is not a dir", func() {
		// readme.txt is a file, not a dir; trying to traverse into it must fail.
		_, err := resolveFile(context.Background(), files, 1, "readme.txt/ghost.jpg")
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("not found"))
	})

	It("returns a List error when listing fails", func() {
		files.ListStub = func(_ context.Context, _ int64) ([]domain.FileInfo, error) {
			return nil, errors.New("api down")
		}
		_, err := resolveFile(context.Background(), files, 1, "readme.txt")
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("api down"))
	})
})
