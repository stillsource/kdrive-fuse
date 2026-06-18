package servicefakes

import (
	"bytes"
	"context"
	"io"
	"strconv"
	"sync"

	"github.com/stillsource/kdrive-fuse/pkg/domain"
	"github.com/stillsource/kdrive-fuse/pkg/service"
)

// FilesFake implements the service file ports for tests.
type FilesFake struct {
	mu sync.Mutex

	// Stubs — if non-nil, handle every call for that method.
	ListStub           func(ctx context.Context, folderID int64) ([]domain.FileInfo, error)
	StatStub           func(ctx context.Context, fileID int64) (domain.FileInfo, error)
	DownloadStub       func(ctx context.Context, fileID int64) ([]byte, error)
	DownloadStreamStub func(ctx context.Context, fileID, off, length int64) (io.ReadCloser, error)
	UploadStub         func(ctx context.Context, in service.UploadInput) (domain.FileInfo, error)
	MkdirStub          func(ctx context.Context, parentID int64, name string) (domain.FileInfo, error)
	DeleteStub         func(ctx context.Context, fileID int64) error
	RenameStub         func(ctx context.Context, fileID int64, newName string) (domain.FileInfo, error)
	MoveStub           func(ctx context.Context, fileID, destDirID int64) error
	SetModifiedAtStub  func(ctx context.Context, fileID, modifiedAt int64) (domain.FileInfo, error)

	// Results — keyed by the primary identifier.
	ListResults           map[int64]ListResult
	StatResults           map[int64]StatResult
	DownloadResults       map[int64]DownloadResult
	DownloadStreamResults map[int64]DownloadStreamResult
	UploadResults         map[string]UploadResult // keyed by Name (or "id:N" for edit mode)
	MkdirResults          map[string]MkdirResult  // keyed by "parentID/name"
	DeleteResults         map[int64]error
	RenameResults         map[int64]RenameResult
	MoveResults           map[int64]error
	SetModifiedAtResults  map[int64]SetModifiedAtResult

	// Calls — inspected by tests.
	ListCalls           []ListCall
	StatCalls           []int64
	DownloadCalls       []int64
	DownloadStreamCalls []DownloadStreamCall
	UploadCalls         []service.UploadInput
	MkdirCalls          []MkdirCall
	DeleteCalls         []int64
	RenameCalls         []RenameCall
	MoveCalls           []MoveCall
	SetModifiedAtCalls  []SetModifiedAtCall
}

type ListCall struct{ FolderID int64 }
type ListResult struct {
	Files []domain.FileInfo
	Err   error
}

type StatResult struct {
	Info domain.FileInfo
	Err  error
}

type DownloadResult struct {
	Data []byte
	Err  error
}

type DownloadStreamCall struct {
	FileID, Off, Length int64
}
type DownloadStreamResult struct {
	Data []byte
	Err  error
}

type UploadResult struct {
	Info domain.FileInfo
	Err  error
}

type MkdirCall struct {
	ParentID int64
	Name     string
}
type MkdirResult struct {
	Info domain.FileInfo
	Err  error
}

type RenameCall struct {
	FileID  int64
	NewName string
}
type RenameResult struct {
	Info domain.FileInfo
	Err  error
}

type MoveCall struct {
	FileID, DestDirID int64
}

type SetModifiedAtCall struct {
	FileID, ModifiedAt int64
}
type SetModifiedAtResult struct {
	Info domain.FileInfo
	Err  error
}

var (
	_ service.FileReader  = (*FilesFake)(nil)
	_ service.FileWriter  = (*FilesFake)(nil)
	_ service.FileManager = (*FilesFake)(nil)
)

func (f *FilesFake) List(ctx context.Context, folderID int64) ([]domain.FileInfo, error) {
	f.mu.Lock()
	f.ListCalls = append(f.ListCalls, ListCall{FolderID: folderID})
	stub := f.ListStub
	res, ok := f.ListResults[folderID]
	f.mu.Unlock()
	if stub != nil {
		return stub(ctx, folderID)
	}
	if ok {
		return res.Files, res.Err
	}
	return nil, nil
}

func (f *FilesFake) Stat(ctx context.Context, fileID int64) (domain.FileInfo, error) {
	f.mu.Lock()
	f.StatCalls = append(f.StatCalls, fileID)
	stub := f.StatStub
	res, ok := f.StatResults[fileID]
	f.mu.Unlock()
	if stub != nil {
		return stub(ctx, fileID)
	}
	if ok {
		return res.Info, res.Err
	}
	return domain.FileInfo{}, nil
}

func (f *FilesFake) Download(ctx context.Context, fileID int64) ([]byte, error) {
	f.mu.Lock()
	f.DownloadCalls = append(f.DownloadCalls, fileID)
	stub := f.DownloadStub
	res, ok := f.DownloadResults[fileID]
	f.mu.Unlock()
	if stub != nil {
		return stub(ctx, fileID)
	}
	if ok {
		return res.Data, res.Err
	}
	return nil, nil
}

func (f *FilesFake) DownloadStream(ctx context.Context, fileID, off, length int64) (io.ReadCloser, error) {
	f.mu.Lock()
	f.DownloadStreamCalls = append(f.DownloadStreamCalls, DownloadStreamCall{FileID: fileID, Off: off, Length: length})
	stub := f.DownloadStreamStub
	res, ok := f.DownloadStreamResults[fileID]
	f.mu.Unlock()
	if stub != nil {
		return stub(ctx, fileID, off, length)
	}
	if ok {
		if res.Err != nil {
			return nil, res.Err
		}
		return io.NopCloser(bytes.NewReader(res.Data)), nil
	}
	return io.NopCloser(bytes.NewReader(nil)), nil
}

func (f *FilesFake) Upload(ctx context.Context, in service.UploadInput) (domain.FileInfo, error) {
	f.mu.Lock()
	f.UploadCalls = append(f.UploadCalls, in)
	stub := f.UploadStub
	key := in.Name
	if in.ExistingFileID > 0 {
		key = "id:" + strconv.FormatInt(in.ExistingFileID, 10)
	}
	res, ok := f.UploadResults[key]
	f.mu.Unlock()
	if stub != nil {
		return stub(ctx, in)
	}
	if ok {
		return res.Info, res.Err
	}
	return domain.FileInfo{}, nil
}

func (f *FilesFake) Mkdir(ctx context.Context, parentID int64, name string) (domain.FileInfo, error) {
	f.mu.Lock()
	f.MkdirCalls = append(f.MkdirCalls, MkdirCall{ParentID: parentID, Name: name})
	stub := f.MkdirStub
	res, ok := f.MkdirResults[strconv.FormatInt(parentID, 10)+"/"+name]
	f.mu.Unlock()
	if stub != nil {
		return stub(ctx, parentID, name)
	}
	if ok {
		return res.Info, res.Err
	}
	return domain.FileInfo{}, nil
}

func (f *FilesFake) Delete(ctx context.Context, fileID int64) error {
	f.mu.Lock()
	f.DeleteCalls = append(f.DeleteCalls, fileID)
	stub := f.DeleteStub
	err, ok := f.DeleteResults[fileID]
	f.mu.Unlock()
	if stub != nil {
		return stub(ctx, fileID)
	}
	if ok {
		return err
	}
	return nil
}

func (f *FilesFake) Rename(ctx context.Context, fileID int64, newName string) (domain.FileInfo, error) {
	f.mu.Lock()
	f.RenameCalls = append(f.RenameCalls, RenameCall{FileID: fileID, NewName: newName})
	stub := f.RenameStub
	res, ok := f.RenameResults[fileID]
	f.mu.Unlock()
	if stub != nil {
		return stub(ctx, fileID, newName)
	}
	if ok {
		return res.Info, res.Err
	}
	return domain.FileInfo{}, nil
}

// Snapshot getters — concurrency-safe copies of call records.
// Tests should use these instead of reading the Calls slices directly.

// GetListCalls returns a copy of ListCalls.
func (f *FilesFake) GetListCalls() []ListCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]ListCall(nil), f.ListCalls...)
}

// GetMkdirCalls returns a copy of MkdirCalls.
func (f *FilesFake) GetMkdirCalls() []MkdirCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]MkdirCall(nil), f.MkdirCalls...)
}

// GetDeleteCalls returns a copy of DeleteCalls.
func (f *FilesFake) GetDeleteCalls() []int64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]int64(nil), f.DeleteCalls...)
}

// GetRenameCalls returns a copy of RenameCalls.
func (f *FilesFake) GetRenameCalls() []RenameCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]RenameCall(nil), f.RenameCalls...)
}

// GetMoveCalls returns a copy of MoveCalls.
func (f *FilesFake) GetMoveCalls() []MoveCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]MoveCall(nil), f.MoveCalls...)
}

// GetUploadCalls returns a copy of UploadCalls.
func (f *FilesFake) GetUploadCalls() []service.UploadInput {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]service.UploadInput(nil), f.UploadCalls...)
}

func (f *FilesFake) Move(ctx context.Context, fileID, destDirID int64) error {
	f.mu.Lock()
	f.MoveCalls = append(f.MoveCalls, MoveCall{FileID: fileID, DestDirID: destDirID})
	stub := f.MoveStub
	err, ok := f.MoveResults[fileID]
	f.mu.Unlock()
	if stub != nil {
		return stub(ctx, fileID, destDirID)
	}
	if ok {
		return err
	}
	return nil
}

func (f *FilesFake) SetModifiedAt(ctx context.Context, fileID, modifiedAt int64) (domain.FileInfo, error) {
	f.mu.Lock()
	f.SetModifiedAtCalls = append(f.SetModifiedAtCalls, SetModifiedAtCall{FileID: fileID, ModifiedAt: modifiedAt})
	stub := f.SetModifiedAtStub
	res, ok := f.SetModifiedAtResults[fileID]
	f.mu.Unlock()
	if stub != nil {
		return stub(ctx, fileID, modifiedAt)
	}
	if ok {
		return res.Info, res.Err
	}
	return domain.FileInfo{}, nil
}

// GetSetModifiedAtCalls returns a copy of SetModifiedAtCalls.
func (f *FilesFake) GetSetModifiedAtCalls() []SetModifiedAtCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]SetModifiedAtCall(nil), f.SetModifiedAtCalls...)
}
