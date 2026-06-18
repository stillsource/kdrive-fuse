package service

import (
	"context"
	"io"

	"github.com/stillsource/kdrive-fuse/pkg/domain"
)

// FileReader reads file metadata and content from the remote.
type FileReader interface {
	List(ctx context.Context, folderID int64) ([]domain.FileInfo, error)
	Stat(ctx context.Context, fileID int64) (domain.FileInfo, error)
	DownloadStream(ctx context.Context, fileID, off, length int64) (io.ReadCloser, error)
}

// FileWriter creates or replaces remote file content.
type FileWriter interface {
	Upload(ctx context.Context, in UploadInput) (domain.FileInfo, error)
}

// FileManager performs structural operations on files and directories.
type FileManager interface {
	Mkdir(ctx context.Context, parentID int64, name string) (domain.FileInfo, error)
	Delete(ctx context.Context, fileID int64) error
	Rename(ctx context.Context, fileID int64, newName string) (domain.FileInfo, error)
	Move(ctx context.Context, fileID, destDirID int64) error
	SetModifiedAt(ctx context.Context, fileID, modifiedAt int64) (domain.FileInfo, error)
}
