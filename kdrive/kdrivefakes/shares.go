package kdrivefakes

import (
	"context"
	"sync"

	"github.com/stillsource/kdrive-fuse/kdrive"
)

// SharesFake implements kdrive.Shares for tests.
type SharesFake struct {
	mu sync.Mutex

	PublishStub    func(ctx context.Context, fileID int64) (kdrive.ShareInfo, error)
	PublishResults map[int64]PublishResult
	PublishCalls   []int64
}

type PublishResult struct {
	Info kdrive.ShareInfo
	Err  error
}

var _ kdrive.Shares = (*SharesFake)(nil)

func (f *SharesFake) Publish(ctx context.Context, fileID int64) (kdrive.ShareInfo, error) {
	f.mu.Lock()
	f.PublishCalls = append(f.PublishCalls, fileID)
	stub := f.PublishStub
	res, ok := f.PublishResults[fileID]
	f.mu.Unlock()
	if stub != nil {
		return stub(ctx, fileID)
	}
	if ok {
		return res.Info, res.Err
	}
	return kdrive.ShareInfo{}, nil
}
