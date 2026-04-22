package kdrive

import "io"

// UploadInput describes a single-shot file upload.
//
// Set ExistingFileID > 0 to replace the content of an existing file (edit mode).
// Otherwise, ParentID must be > 0 and Name the desired filename.
//
// Body must be seekable — the hash is computed in one pass, then the body
// is rewound to zero for the upload itself. Size is mandatory (kDrive
// rejects chunked-encoded uploads).
type UploadInput struct {
	ParentID       int64
	ExistingFileID int64
	Name           string
	Body           io.ReadSeeker
	Size           int64
}
