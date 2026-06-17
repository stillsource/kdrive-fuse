package syncer

import (
	"github.com/stillsource/kdrive-fuse/pkg/infrastructure/manifest"
	"github.com/stillsource/kdrive-fuse/pkg/infrastructure/remoteindex"
)

// PullOp is the kind of action planned for a pull (remote -> local).
type PullOp int

const (
	PullDownload    PullOp = iota // remote file new or changed: download to local
	PullDeleteLocal               // remote gone: delete the local file
)

// PullItem is one planned pull action.
type PullItem struct {
	Rel         string
	Op          PullOp
	RemoteID    int64 // PullDownload
	Size        int64 // PullDownload (remote size; recorded in the manifest)
	RemoteMtime int64 // PullDownload (recorded in the manifest)
}

// PlanPull classifies a pull (remote -> local) of the remote index idx against
// the manifest baseline. A remote file absent from the manifest, or whose size
// or remote mtime differs from the manifest, is a download; a manifest entry
// with no matching remote file is a local delete. Unchanged files are omitted.
func PlanPull(idx map[string]remoteindex.Entry, m *manifest.Manifest) []PullItem {
	var items []PullItem
	for rel, r := range idx {
		e, ok := m.Get(rel)
		if !ok || r.Size != e.Size || r.Mtime != e.RemoteMtime {
			items = append(items, PullItem{Rel: rel, Op: PullDownload, RemoteID: r.ID, Size: r.Size, RemoteMtime: r.Mtime})
		}
	}
	m.Range(func(rel string, _ manifest.Entry) {
		if _, ok := idx[rel]; !ok {
			items = append(items, PullItem{Rel: rel, Op: PullDeleteLocal})
		}
	})
	return items
}
