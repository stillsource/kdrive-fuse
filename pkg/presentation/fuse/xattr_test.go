package fuse

import (
	"context"
	"syscall"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/stillsource/kdrive-fuse/pkg/domain"
)

var _ = Describe("kdriveXattrs", func() {
	It("includes id and created_at", func() {
		info := domain.FileInfo{ID: 42, CreatedAt: 1700000000, MimeType: ""}
		x := kdriveXattrs(info)
		Expect(x).To(HaveKeyWithValue("user.kdrive.id", "42"))
		Expect(x).To(HaveKeyWithValue("user.kdrive.created_at", "1700000000"))
	})

	It("includes mime_type when non-empty", func() {
		info := domain.FileInfo{ID: 1, CreatedAt: 0, MimeType: "text/plain"}
		x := kdriveXattrs(info)
		Expect(x).To(HaveKeyWithValue("user.kdrive.mime_type", "text/plain"))
	})

	It("omits mime_type when empty", func() {
		info := domain.FileInfo{ID: 1, CreatedAt: 0, MimeType: ""}
		x := kdriveXattrs(info)
		Expect(x).NotTo(HaveKey("user.kdrive.mime_type"))
	})
})

var _ = Describe("getXattrValue", func() {
	var attrs map[string]string

	BeforeEach(func() {
		attrs = map[string]string{"user.kdrive.id": "99"}
	})

	It("size probe (dest nil/zero-len) returns the value length and no error", func() {
		n, errno := getXattrValue(attrs, "user.kdrive.id", nil)
		Expect(errno).To(BeZero())
		Expect(n).To(Equal(uint32(2))) // "99" is 2 bytes
	})

	It("size probe with zero-length slice returns the value length", func() {
		n, errno := getXattrValue(attrs, "user.kdrive.id", []byte{})
		Expect(errno).To(BeZero())
		Expect(n).To(Equal(uint32(2)))
	})

	It("exact-size dest copies the value", func() {
		dest := make([]byte, 2)
		n, errno := getXattrValue(attrs, "user.kdrive.id", dest)
		Expect(errno).To(BeZero())
		Expect(n).To(Equal(uint32(2)))
		Expect(string(dest[:n])).To(Equal("99"))
	})

	It("larger dest copies the value and returns length", func() {
		dest := make([]byte, 10)
		n, errno := getXattrValue(attrs, "user.kdrive.id", dest)
		Expect(errno).To(BeZero())
		Expect(n).To(Equal(uint32(2)))
		Expect(string(dest[:n])).To(Equal("99"))
	})

	It("too-small dest returns ERANGE", func() {
		dest := make([]byte, 1)
		_, errno := getXattrValue(attrs, "user.kdrive.id", dest)
		Expect(errno).To(Equal(syscall.ERANGE))
	})

	It("unknown attribute returns ENODATA", func() {
		_, errno := getXattrValue(attrs, "user.kdrive.nonexistent", nil)
		Expect(errno).To(Equal(syscall.ENODATA))
	})
})

var _ = Describe("listXattrNames", func() {
	It("size probe returns correct total byte count", func() {
		attrs := map[string]string{
			"user.kdrive.id":         "1",
			"user.kdrive.created_at": "0",
		}
		// sorted: "user.kdrive.created_at\0" (22+1=23) + "user.kdrive.id\0" (14+1=15) = 38
		n, errno := listXattrNames(attrs, nil)
		Expect(errno).To(BeZero())
		Expect(n).To(Equal(uint32(38)))
	})

	It("returns NUL-separated sorted names in dest", func() {
		attrs := map[string]string{
			"user.kdrive.id":         "1",
			"user.kdrive.created_at": "0",
		}
		dest := make([]byte, 38)
		n, errno := listXattrNames(attrs, dest)
		Expect(errno).To(BeZero())
		Expect(n).To(Equal(uint32(38)))
		got := string(dest[:n])
		Expect(got).To(Equal("user.kdrive.created_at\x00user.kdrive.id\x00"))
	})

	It("too-small dest returns ERANGE", func() {
		attrs := map[string]string{"user.kdrive.id": "1"}
		dest := make([]byte, 1)
		_, errno := listXattrNames(attrs, dest)
		Expect(errno).To(Equal(syscall.ERANGE))
	})

	It("empty attrs returns zero size with no error", func() {
		n, errno := listXattrNames(map[string]string{}, nil)
		Expect(errno).To(BeZero())
		Expect(n).To(BeZero())
	})

	It("names are sorted stably", func() {
		attrs := map[string]string{
			"user.kdrive.mime_type":  "text/plain",
			"user.kdrive.id":         "1",
			"user.kdrive.created_at": "0",
		}
		// sorted: created_at, id, mime_type
		dest := make([]byte, 512)
		n, errno := listXattrNames(attrs, dest)
		Expect(errno).To(BeZero())
		got := string(dest[:n])
		Expect(got).To(Equal(
			"user.kdrive.created_at\x00" +
				"user.kdrive.id\x00" +
				"user.kdrive.mime_type\x00",
		))
	})
})

var _ = Describe("FileNode xattr methods — unit", func() {
	It("Getxattr returns id value for user.kdrive.id", func() {
		f := &FileNode{
			kdfs: &KDriveFS{},
			info: domain.FileInfo{ID: 7, CreatedAt: 1234, MimeType: ""},
		}
		dest := make([]byte, 32)
		n, errno := f.Getxattr(context.TODO(), "user.kdrive.id", dest)
		Expect(errno).To(BeZero())
		Expect(string(dest[:n])).To(Equal("7"))
	})

	It("Getxattr returns ENODATA for unknown attribute", func() {
		f := &FileNode{kdfs: &KDriveFS{}, info: domain.FileInfo{ID: 1}}
		_, errno := f.Getxattr(context.TODO(), "user.kdrive.share_url", nil)
		Expect(errno).To(Equal(syscall.ENODATA))
	})

	It("Listxattr size probe returns non-zero for a file with id+created_at", func() {
		f := &FileNode{
			kdfs: &KDriveFS{},
			info: domain.FileInfo{ID: 1, CreatedAt: 0},
		}
		n, errno := f.Listxattr(context.TODO(), nil)
		Expect(errno).To(BeZero())
		Expect(n).To(BeNumerically(">", 0))
	})

	It("Listxattr includes mime_type only when non-empty", func() {
		fWith := &FileNode{
			kdfs: &KDriveFS{},
			info: domain.FileInfo{ID: 1, MimeType: "image/png"},
		}
		fWithout := &FileNode{
			kdfs: &KDriveFS{},
			info: domain.FileInfo{ID: 1, MimeType: ""},
		}

		dest := make([]byte, 512)
		nWith, errno := fWith.Listxattr(context.TODO(), dest)
		Expect(errno).To(BeZero())
		gotWith := string(dest[:nWith])
		Expect(gotWith).To(ContainSubstring("user.kdrive.mime_type"))

		nWithout, errno := fWithout.Listxattr(context.TODO(), dest)
		Expect(errno).To(BeZero())
		gotWithout := string(dest[:nWithout])
		Expect(gotWithout).NotTo(ContainSubstring("user.kdrive.mime_type"))
	})
})
