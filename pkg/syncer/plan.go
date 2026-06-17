// Package syncer orchestrates one-way mirroring between a local tree and a
// kDrive folder, using a manifest baseline (pkg/infrastructure/manifest) and a
// remote index (pkg/infrastructure/remoteindex). It is named "syncer" rather
// than "sync" to avoid clashing with the standard library.
//
// This file is the pure planner: it classifies work without performing any IO.
package syncer

import (
	"github.com/stillsource/kdrive-fuse/pkg/infrastructure/manifest"
	"github.com/stillsource/kdrive-fuse/pkg/infrastructure/remoteindex"
)

// LocalFile is the local-side metadata the planner needs for one file.
type LocalFile struct {
	Rel   string
	Size  int64
	Mtime int64 // local mtime, Unix seconds
}

// Op is the kind of action planned for a path.
type Op int

const (
	OpUpload    Op = iota // new file: create remotely
	OpOverwrite           // changed file: replace remote content by id
	OpDelete              // local gone: delete the remote file by id
)

// Item is one planned push action.
type Item struct {
	Rel      string
	Op       Op
	RemoteID int64 // OpOverwrite / OpDelete
	Size     int64 // OpUpload / OpOverwrite (recorded in the manifest on success)
	Mtime    int64 // OpUpload / OpOverwrite (local mtime, recorded on success)
}

// PlanPush classifies a push (local -> remote) against the manifest baseline.
// A local file absent from the manifest is an upload; one whose size or mtime
// differs from the manifest is an overwrite (by the manifest's remote id); a
// manifest entry with no matching local file is a delete. Unchanged files are
// omitted.
func PlanPush(local []LocalFile, m *manifest.Manifest) []Item {
	var items []Item
	seen := make(map[string]bool, len(local))
	for _, f := range local {
		seen[f.Rel] = true
		e, ok := m.Get(f.Rel)
		switch {
		case !ok:
			items = append(items, Item{Rel: f.Rel, Op: OpUpload, Size: f.Size, Mtime: f.Mtime})
		case f.Size != e.Size || f.Mtime != e.LocalMtime:
			items = append(items, Item{Rel: f.Rel, Op: OpOverwrite, RemoteID: e.RemoteID, Size: f.Size, Mtime: f.Mtime})
		}
	}
	m.Range(func(rel string, e manifest.Entry) {
		if !seen[rel] {
			items = append(items, Item{Rel: rel, Op: OpDelete, RemoteID: e.RemoteID})
		}
	})
	return items
}

// Bootstrap seeds an empty (or partial) manifest from a remote index so an
// already-uploaded tree is not re-pushed wholesale on the first run. For each
// local file present in the index it records a baseline entry: the remote id
// and mtime from the index, the remote size, and the local mtime. PlanPush then
// treats a same-size file as unchanged and a different-size file as an overwrite
// (it has the remote id). Files absent from the index are left unseeded, so they
// plan as uploads.
func Bootstrap(m *manifest.Manifest, idx map[string]remoteindex.Entry, local []LocalFile) {
	for _, f := range local {
		r, ok := idx[f.Rel]
		if !ok {
			continue
		}
		m.Set(f.Rel, manifest.Entry{
			Size:        r.Size,
			LocalMtime:  f.Mtime,
			RemoteID:    r.ID,
			RemoteMtime: r.Mtime,
		})
	}
}
