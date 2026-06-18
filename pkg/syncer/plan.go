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
	OpMove                // renamed/moved file: relocate the remote file by id
)

// Item is one planned push action.
type Item struct {
	Rel      string // destination rel (for OpMove: the new path)
	Op       Op
	RemoteID int64  // OpOverwrite / OpDelete / OpMove
	Size     int64  // OpUpload / OpOverwrite / OpMove (recorded in the manifest on success)
	Mtime    int64  // OpUpload / OpOverwrite / OpMove (local mtime, recorded on success)
	SrcRel   string // OpMove: the manifest key being moved from
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

// DetectMoves rewrites delete+upload pairs that look like a rename/move into a
// single OpMove, avoiding a re-upload. It is a heuristic: it pairs a delete and
// an upload that share the same size and mtime, but does NOT verify content
// identity. Two unrelated files with equal size and mtime could be mis-paired
// (one reason this function is opt-in via PushOptions.DetectMoves). Only
// unambiguous 1:1 matches (exactly one upload and one delete for a (size, mtime)
// key) are paired; any ambiguity falls back to the original delete+upload. Empty
// files (size == 0) are never paired because they all share the key (0, 0).
func DetectMoves(items []Item, m *manifest.Manifest) []Item {
	type key struct{ size, mtime int64 }
	uploads := map[key][]int{}
	deletes := map[key][]int{}
	for i, it := range items {
		switch it.Op {
		case OpUpload:
			k := key{it.Size, it.Mtime}
			if k.size == 0 {
				continue // empty files share (0,0) key — never pair them
			}
			uploads[k] = append(uploads[k], i)
		case OpDelete:
			e, _ := m.Get(it.Rel)
			k := key{e.Size, e.LocalMtime}
			if k.size == 0 {
				continue // skip empty files
			}
			deletes[k] = append(deletes[k], i)
		}
	}
	drop := map[int]bool{}
	var moves []Item
	for k, ups := range uploads {
		dels := deletes[k]
		if len(ups) != 1 || len(dels) != 1 {
			continue // ambiguous (or no delete) — leave as upload/delete
		}
		up := items[ups[0]]
		del := items[dels[0]]
		e, _ := m.Get(del.Rel)
		moves = append(moves, Item{
			Op:       OpMove,
			SrcRel:   del.Rel,
			Rel:      up.Rel,
			RemoteID: e.RemoteID,
			Size:     e.Size,
			Mtime:    up.Mtime,
		})
		drop[ups[0]] = true
		drop[dels[0]] = true
	}
	if len(moves) == 0 {
		return items
	}
	kept := make([]Item, 0, len(items))
	for i, it := range items {
		if !drop[i] {
			kept = append(kept, it)
		}
	}
	return append(kept, moves...)
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
