package di

import "github.com/stillsource/kdrive-fuse/pkg/infrastructure/kdriveapi"

// Client returns the memoized kDrive API client. It applies the base-URL and
// upload-base-URL overrides only when the corresponding Config field is set,
// and the same logger option the binary uses.
func (c *Container) Client() *kdriveapi.Client {
	if c.client == nil {
		opts := []kdriveapi.Option{kdriveapi.WithLogger(c.cfg.Logger)}
		if c.cfg.BaseURL != "" {
			opts = append(opts, kdriveapi.WithBaseURL(c.cfg.BaseURL))
		}
		if c.cfg.UploadBaseURL != "" {
			opts = append(opts, kdriveapi.WithUploadBaseURL(c.cfg.UploadBaseURL))
		}
		if c.cfg.Metrics != nil {
			opts = append(opts, kdriveapi.WithMetrics(c.cfg.Metrics))
		}
		if c.cfg.UploadParallelism > 0 {
			opts = append(opts, kdriveapi.WithUploadParallelism(c.cfg.UploadParallelism))
		}
		c.client = kdriveapi.New(c.cfg.Token, c.cfg.DriveID, opts...)
	}
	return c.client
}
