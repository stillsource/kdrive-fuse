package fuse

import (
	"sort"
	"strconv"
	"syscall"

	"github.com/stillsource/kdrive-fuse/pkg/domain"
)

// kdriveXattrs returns the read-only extended attributes exposed for a file.
func kdriveXattrs(info domain.FileInfo) map[string]string {
	x := map[string]string{
		"user.kdrive.id":         strconv.FormatInt(info.ID, 10),
		"user.kdrive.created_at": strconv.FormatInt(info.CreatedAt, 10),
	}
	if info.MimeType != "" {
		x["user.kdrive.mime_type"] = info.MimeType
	}
	return x
}

// getXattrValue serves one attribute with the FUSE size-probe protocol: a
// zero-length dest returns the size; a too-small dest returns ERANGE; otherwise
// the value is copied. An unknown attribute returns ENODATA (== ENOATTR on Linux).
func getXattrValue(attrs map[string]string, attr string, dest []byte) (uint32, syscall.Errno) {
	v, ok := attrs[attr]
	if !ok {
		return 0, syscall.ENODATA // unknown attribute (ENOATTR on Linux == ENODATA)
	}
	if len(dest) == 0 {
		return uint32(len(v)), 0
	}
	if len(dest) < len(v) {
		return 0, syscall.ERANGE
	}
	return uint32(copy(dest, v)), 0
}

// listXattrNames serves the NUL-separated attribute-name list with the same
// size-probe protocol. Names are sorted for stable output.
func listXattrNames(attrs map[string]string, dest []byte) (uint32, syscall.Errno) {
	names := make([]string, 0, len(attrs))
	for k := range attrs {
		names = append(names, k)
	}
	sort.Strings(names)
	var buf []byte
	for _, n := range names {
		buf = append(buf, n...)
		buf = append(buf, 0)
	}
	if len(dest) == 0 {
		return uint32(len(buf)), 0
	}
	if len(dest) < len(buf) {
		return 0, syscall.ERANGE
	}
	return uint32(copy(dest, buf)), 0
}
