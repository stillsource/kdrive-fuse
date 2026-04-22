package kdrive

// ShareInfo is the subset of share metadata returned by kDrive that this client exposes.
type ShareInfo struct {
	ID       int64  `json:"id,omitempty"`
	ShareURL string `json:"share_url"`
}
