package cli

import (
	"bytes"
	"context"
	"errors"
	"io"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/stillsource/kdrive-fuse/pkg/domain"
)

// fakeTrash is an in-memory trasher for runTrash tests.
type fakeTrash struct {
	listResult   []domain.FileInfo
	listErr      error
	restoreCalls []int64
	restoreErr   error
	purgeCalls   []int64
	purgeErr     error
	emptyCalled  bool
	emptyErr     error
}

func (f *fakeTrash) ListTrash(_ context.Context) ([]domain.FileInfo, error) {
	return f.listResult, f.listErr
}

func (f *fakeTrash) RestoreTrash(_ context.Context, fileID int64) error {
	f.restoreCalls = append(f.restoreCalls, fileID)
	return f.restoreErr
}

func (f *fakeTrash) PurgeTrash(_ context.Context, fileID int64) error {
	f.purgeCalls = append(f.purgeCalls, fileID)
	return f.purgeErr
}

func (f *fakeTrash) EmptyTrash(_ context.Context) error {
	f.emptyCalled = true
	return f.emptyErr
}

// stubTrashBackend replaces trashBackend with one that returns the given fake.
func stubTrashBackend(t trasher) func() {
	orig := trashBackend
	trashBackend = func(context.Context, io.Writer) (trasher, error) {
		return t, nil
	}
	return func() { trashBackend = orig }
}

var _ = Describe("runTrash list subcommand", func() {
	var (
		fake *fakeTrash
		out  *bytes.Buffer
		errb *bytes.Buffer
	)

	BeforeEach(func() {
		fake = &fakeTrash{
			listResult: []domain.FileInfo{
				{ID: 10, Name: "photo.jpg", Type: domain.FileTypeFile, Size: 1024},
				{ID: 11, Name: "old-dir", Type: domain.FileTypeDir, Size: 0},
			},
		}
		out = &bytes.Buffer{}
		errb = &bytes.Buffer{}
	})

	It("prints each trashed item with id, name and size", func() {
		restore := stubTrashBackend(fake)
		defer restore()

		code := runTrash([]string{"list"}, out, errb)

		Expect(code).To(Equal(0))
		Expect(errb.String()).To(BeEmpty())
		Expect(out.String()).To(ContainSubstring("10"))
		Expect(out.String()).To(ContainSubstring("photo.jpg"))
		Expect(out.String()).To(ContainSubstring("1024"))
		Expect(out.String()).To(ContainSubstring("11"))
		Expect(out.String()).To(ContainSubstring("old-dir"))
	})

	It("returns 1 and prints error when ListTrash fails", func() {
		fake.listErr = errors.New("api down")
		restore := stubTrashBackend(fake)
		defer restore()

		code := runTrash([]string{"list"}, out, errb)

		Expect(code).To(Equal(1))
		Expect(errb.String()).To(ContainSubstring("api down"))
	})
})

var _ = Describe("runTrash restore subcommand", func() {
	var (
		fake *fakeTrash
		out  *bytes.Buffer
		errb *bytes.Buffer
	)

	BeforeEach(func() {
		fake = &fakeTrash{}
		out = &bytes.Buffer{}
		errb = &bytes.Buffer{}
	})

	It("calls RestoreTrash with the parsed file id", func() {
		restore := stubTrashBackend(fake)
		defer restore()

		code := runTrash([]string{"restore", "42"}, out, errb)

		Expect(code).To(Equal(0))
		Expect(errb.String()).To(BeEmpty())
		Expect(fake.restoreCalls).To(ConsistOf(int64(42)))
	})

	It("returns 1 when RestoreTrash fails", func() {
		fake.restoreErr = errors.New("restore failed")
		restore := stubTrashBackend(fake)
		defer restore()

		code := runTrash([]string{"restore", "42"}, out, errb)

		Expect(code).To(Equal(1))
		Expect(errb.String()).To(ContainSubstring("restore failed"))
	})

	It("returns 1 with error message when file id is not a number", func() {
		restore := stubTrashBackend(fake)
		defer restore()

		code := runTrash([]string{"restore", "notanumber"}, out, errb)

		Expect(code).NotTo(Equal(0))
		Expect(errb.String()).To(ContainSubstring("notanumber"))
		Expect(fake.restoreCalls).To(BeEmpty())
	})

	It("returns 2 when no file id is given", func() {
		restore := stubTrashBackend(fake)
		defer restore()

		code := runTrash([]string{"restore"}, out, errb)

		Expect(code).To(Equal(2))
		Expect(errb.String()).To(ContainSubstring("FILE_ID"))
	})
})

var _ = Describe("runTrash purge subcommand", func() {
	var (
		fake *fakeTrash
		out  *bytes.Buffer
		errb *bytes.Buffer
	)

	BeforeEach(func() {
		fake = &fakeTrash{}
		out = &bytes.Buffer{}
		errb = &bytes.Buffer{}
	})

	It("refuses without --yes and does NOT call PurgeTrash", func() {
		restore := stubTrashBackend(fake)
		defer restore()

		code := runTrash([]string{"purge", "55"}, out, errb)

		Expect(code).To(Equal(1))
		Expect(errb.String()).To(ContainSubstring("irreversible"))
		Expect(fake.purgeCalls).To(BeEmpty())
	})

	It("calls PurgeTrash with the parsed id when --yes is provided", func() {
		restore := stubTrashBackend(fake)
		defer restore()

		code := runTrash([]string{"purge", "55", "--yes"}, out, errb)

		Expect(code).To(Equal(0))
		Expect(errb.String()).To(BeEmpty())
		Expect(fake.purgeCalls).To(ConsistOf(int64(55)))
	})

	It("also accepts --yes before the id", func() {
		restore := stubTrashBackend(fake)
		defer restore()

		code := runTrash([]string{"purge", "--yes", "55"}, out, errb)

		Expect(code).To(Equal(0))
		Expect(fake.purgeCalls).To(ConsistOf(int64(55)))
	})

	It("returns 1 when PurgeTrash fails", func() {
		fake.purgeErr = errors.New("purge failed")
		restore := stubTrashBackend(fake)
		defer restore()

		code := runTrash([]string{"purge", "55", "--yes"}, out, errb)

		Expect(code).To(Equal(1))
		Expect(errb.String()).To(ContainSubstring("purge failed"))
	})

	It("returns 1 with error message when file id is not a number", func() {
		restore := stubTrashBackend(fake)
		defer restore()

		code := runTrash([]string{"purge", "--yes", "bad"}, out, errb)

		Expect(code).NotTo(Equal(0))
		Expect(errb.String()).To(ContainSubstring("bad"))
		Expect(fake.purgeCalls).To(BeEmpty())
	})

	It("returns 2 when no file id is given", func() {
		restore := stubTrashBackend(fake)
		defer restore()

		code := runTrash([]string{"purge"}, out, errb)

		Expect(code).To(Equal(2))
	})
})

var _ = Describe("runTrash empty subcommand", func() {
	var (
		fake *fakeTrash
		out  *bytes.Buffer
		errb *bytes.Buffer
	)

	BeforeEach(func() {
		fake = &fakeTrash{}
		out = &bytes.Buffer{}
		errb = &bytes.Buffer{}
	})

	It("refuses without --yes and does NOT call EmptyTrash", func() {
		restore := stubTrashBackend(fake)
		defer restore()

		code := runTrash([]string{"empty"}, out, errb)

		Expect(code).To(Equal(1))
		Expect(errb.String()).To(ContainSubstring("irreversible"))
		Expect(fake.emptyCalled).To(BeFalse())
	})

	It("calls EmptyTrash when --yes is provided", func() {
		restore := stubTrashBackend(fake)
		defer restore()

		code := runTrash([]string{"empty", "--yes"}, out, errb)

		Expect(code).To(Equal(0))
		Expect(errb.String()).To(BeEmpty())
		Expect(fake.emptyCalled).To(BeTrue())
	})

	It("returns 1 when EmptyTrash fails", func() {
		fake.emptyErr = errors.New("empty failed")
		restore := stubTrashBackend(fake)
		defer restore()

		code := runTrash([]string{"empty", "--yes"}, out, errb)

		Expect(code).To(Equal(1))
		Expect(errb.String()).To(ContainSubstring("empty failed"))
	})
})

var _ = Describe("runTrash flag/usage handling", func() {
	var out, errb *bytes.Buffer
	BeforeEach(func() {
		out = &bytes.Buffer{}
		errb = &bytes.Buffer{}
	})

	It("prints trash usage and exits 0 on --help", func() {
		code := runTrash([]string{"--help"}, out, errb)
		Expect(code).To(Equal(0))
		Expect(out.String()).To(ContainSubstring("kdrive trash"))
	})

	It("prints trash usage and exits 0 on -h", func() {
		code := runTrash([]string{"-h"}, out, errb)
		Expect(code).To(Equal(0))
		Expect(out.String()).To(ContainSubstring("kdrive trash"))
	})

	It("prints trash usage and exits 2 when no subcommand is given", func() {
		code := runTrash([]string{}, out, errb)
		Expect(code).To(Equal(2))
		Expect(errb.String()).To(ContainSubstring("kdrive trash"))
	})

	It("exits 2 with usage on unknown subcommand", func() {
		code := runTrash([]string{"unknown"}, out, errb)
		Expect(code).To(Equal(2))
		Expect(errb.String()).To(ContainSubstring("unknown"))
	})

	It("returns 1 when the backend fails", func() {
		orig := trashBackend
		trashBackend = func(context.Context, io.Writer) (trasher, error) {
			return nil, errors.New("no credentials")
		}
		defer func() { trashBackend = orig }()

		code := runTrash([]string{"list"}, out, errb)

		Expect(code).To(Equal(1))
		Expect(errb.String()).To(ContainSubstring("no credentials"))
	})
})

var _ = Describe("Run dispatches to trash", func() {
	var out, errb *bytes.Buffer
	BeforeEach(func() {
		out = &bytes.Buffer{}
		errb = &bytes.Buffer{}
	})

	It("routes 'trash --help' to runTrash and exits 0", func() {
		code := Run([]string{"trash", "--help"}, "dev", out, errb)
		Expect(code).To(Equal(0))
		Expect(out.String()).To(ContainSubstring("kdrive trash"))
	})

	It("routes 'trash' with no subcommand to runTrash and exits 2", func() {
		code := Run([]string{"trash"}, "dev", out, errb)
		Expect(code).To(Equal(2))
	})
})
