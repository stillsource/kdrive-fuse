package kdrive

// FileType enumerates kDrive node types.
type FileType string

const (
	FileTypeDir  FileType = "dir"
	FileTypeFile FileType = "file"
)

// FileInfo is the metadata kDrive returns for a file or directory.
// All timestamps are Unix seconds.
type FileInfo struct {
	ID             int64    `json:"id"`
	Name           string   `json:"name"`
	Type           FileType `json:"type"`
	Size           int64    `json:"size"`
	CreatedAt      int64    `json:"created_at"`
	LastModifiedAt int64    `json:"last_modified_at"`
	MimeType       string   `json:"mime_type,omitempty"`
	ParentID       int64    `json:"parent_id,omitempty"`
}

// IsDir reports whether this entry is a directory.
func (f FileInfo) IsDir() bool { return f.Type == FileTypeDir }
