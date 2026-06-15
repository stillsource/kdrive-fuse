package servicefakes

import (
	"context"
	"sync"

	"github.com/stillsource/kdrive-fuse/pkg/domain"
	"github.com/stillsource/kdrive-fuse/pkg/service"
)

// SharesFake implements service.Sharer for tests.
type SharesFake struct {
	mu sync.Mutex

	PublishStub    func(ctx context.Context, fileID int64) (domain.ShareInfo, error)
	PublishResults map[int64]PublishResult
	PublishCalls   []int64
}

type PublishResult struct {
	Info domain.ShareInfo
	Err  error
}

var _ service.Sharer = (*SharesFake)(nil)

func (f *SharesFake) Publish(ctx context.Context, fileID int64) (domain.ShareInfo, error) {
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
	return domain.ShareInfo{}, nil
}
