package syncer_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/stillsource/kdrive-fuse/pkg/domain"
	"github.com/stillsource/kdrive-fuse/pkg/syncer"
)

// fakeRemote is an in-memory remote (folder tree + file content) for Pull tests.
type fakeRemote struct {
	folders map[int64][]domain.FileInfo
	content map[int64][]byte
}

func (f *fakeRemote) List(_ context.Context, folderID int64) ([]domain.FileInfo, error) {
	return f.folders[folderID], nil
}

func (f *fakeRemote) DownloadStream(_ context.Context, fileID, _, _ int64) (io.ReadCloser, error) {
	b, ok := f.content[fileID]
	if !ok {
		return nil, errors.New("missing content")
	}
	return io.NopCloser(bytes.NewReader(b)), nil
}

var _ = Describe("Pull", func() {
	var (
		root  string
		mpath string
		rem   *fakeRemote
	)
	BeforeEach(func() {
		root = GinkgoT().TempDir()
		mpath = filepath.Join(GinkgoT().TempDir(), "m.tsv")
		rem = &fakeRemote{
			folders: map[int64][]domain.FileInfo{
				1: {{ID: 7, Name: "a.jpg", Type: domain.FileTypeFile, Size: 5, LastModifiedAt: 100}},
			},
			content: map[int64][]byte{7: []byte("hello")},
		}
	})
	opts := func() syncer.PullOptions { return syncer.PullOptions{LocalRoot: root, Jobs: 4} }

	It("downloads remote files on a first pull and saves the manifest", func() {
		res, err := syncer.Pull(context.Background(), opts(), rem, 1, mpath, &strings.Builder{})
		Expect(err).NotTo(HaveOccurred())
		Expect(res.Downloaded).To(Equal(1))
		data, err := os.ReadFile(filepath.Join(root, "a.jpg"))
		Expect(err).NotTo(HaveOccurred())
		Expect(string(data)).To(Equal("hello"))
		_, err = os.Stat(mpath)
		Expect(err).NotTo(HaveOccurred())
	})

	It("is a no-op on a second identical pull", func() {
		_, err := syncer.Pull(context.Background(), opts(), rem, 1, mpath, &strings.Builder{})
		Expect(err).NotTo(HaveOccurred())
		res, err := syncer.Pull(context.Background(), opts(), rem, 1, mpath, &strings.Builder{})
		Expect(err).NotTo(HaveOccurred())
		Expect(res).To(Equal(syncer.PullResult{}))
	})

	It("pulls into a not-yet-existing local directory", func() {
		fresh := filepath.Join(GinkgoT().TempDir(), "newdir")
		o := syncer.PullOptions{LocalRoot: fresh, Jobs: 2}
		res, err := syncer.Pull(context.Background(), o, rem, 1, mpath, &strings.Builder{})
		Expect(err).NotTo(HaveOccurred())
		Expect(res.Downloaded).To(Equal(1))
		data, err := os.ReadFile(filepath.Join(fresh, "a.jpg"))
		Expect(err).NotTo(HaveOccurred())
		Expect(string(data)).To(Equal("hello"))
	})

	It("skips a download that would clobber a locally-modified file", func() {
		_, err := syncer.Pull(context.Background(), opts(), rem, 1, mpath, &strings.Builder{})
		Expect(err).NotTo(HaveOccurred())
		// Remote changes (download planned) AND local changes (drift).
		rem.folders[1] = []domain.FileInfo{{ID: 7, Name: "a.jpg", Type: domain.FileTypeFile, Size: 9, LastModifiedAt: 200}}
		rem.content[7] = []byte("newcontent")
		Expect(os.WriteFile(filepath.Join(root, "a.jpg"), []byte("LOCAL EDIT"), 0o644)).To(Succeed())
		var out strings.Builder
		res, err := syncer.Pull(context.Background(), opts(), rem, 1, mpath, &out)
		Expect(err).NotTo(HaveOccurred())
		Expect(res.Downloaded).To(Equal(0))
		Expect(out.String()).To(ContainSubstring("skip (local changed)"))
		data, _ := os.ReadFile(filepath.Join(root, "a.jpg"))
		Expect(string(data)).To(Equal("LOCAL EDIT")) // not clobbered
	})

	It("dry-run prints a plan and changes nothing", func() {
		o := opts()
		o.DryRun = true
		var out strings.Builder
		res, err := syncer.Pull(context.Background(), o, rem, 1, mpath, &out)
		Expect(err).NotTo(HaveOccurred())
		Expect(res).To(Equal(syncer.PullResult{}))
		Expect(out.String()).To(ContainSubstring("dry-run"))
		_, statErr := os.Stat(filepath.Join(root, "a.jpg"))
		Expect(os.IsNotExist(statErr)).To(BeTrue())
	})

	// emptyRemote establishes a baseline (pulls a.jpg), then removes it from the
	// remote so the next pull plans a local delete.
	emptyRemote := func() {
		_, err := syncer.Pull(context.Background(), opts(), rem, 1, mpath, &strings.Builder{})
		Expect(err).NotTo(HaveOccurred())
		rem.folders[1] = nil
		rem.content = map[int64][]byte{}
	}

	It("with --no-delete, keeps a locally-present file that was deleted remotely", func() {
		emptyRemote()
		o := opts()
		o.NoDelete = true
		res, err := syncer.Pull(context.Background(), o, rem, 1, mpath, &strings.Builder{})
		Expect(err).NotTo(HaveOccurred())
		Expect(res.Deleted).To(Equal(0))
		_, statErr := os.Stat(filepath.Join(root, "a.jpg"))
		Expect(statErr).NotTo(HaveOccurred()) // still there
	})

	It("refuses to delete locally beyond the guard threshold", func() {
		emptyRemote() // baseline of 1, one local delete planned -> 1*5 > 1
		_, err := syncer.Pull(context.Background(), opts(), rem, 1, mpath, &strings.Builder{})
		Expect(err).To(MatchError(ContainSubstring("refusing to delete")))
	})

	It("dry-run prints a local delete (with --force past the guard)", func() {
		emptyRemote()
		o := opts()
		o.DryRun = true
		o.Force = true
		var out strings.Builder
		_, err := syncer.Pull(context.Background(), o, rem, 1, mpath, &out)
		Expect(err).NotTo(HaveOccurred())
		Expect(out.String()).To(ContainSubstring("delete-local"))
		_, statErr := os.Stat(filepath.Join(root, "a.jpg"))
		Expect(statErr).NotTo(HaveOccurred()) // dry-run changed nothing
	})
})
