package service

import "io"

// UploadInput describes a file upload. Small files go single-shot; files larger
// than the upload-session threshold are uploaded in chunks (the adapter decides
// by Size). Set ExistingFileID > 0 to replace an existing file's content (edit
// mode); otherwise ParentID must be > 0 and Name the desired filename.
//
// Body must be seekable — it is rewound to zero and read in one or more passes.
// Size is mandatory (kDrive rejects chunked transfer-encoding on each request).
type UploadInput struct {
	ParentID       int64
	ExistingFileID int64
	Name           string
	Body           io.ReadSeeker
	Size           int64
	// Conflict selects the upload conflict mode for a NEW file (ignored in edit
	// mode). "" defaults to "error" (fail on a duplicate name); "version" keeps
	// the existing file as a prior version; "rename" appends " (1)" to the name.
	Conflict string
}
