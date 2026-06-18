package syncer

import (
	"sort"

	"github.com/stillsource/kdrive-fuse/pkg/infrastructure/manifest"
	"github.com/stillsource/kdrive-fuse/pkg/infrastructure/remoteindex"
)

// TwoWayPlan is the classified work for a two-way sync. Push and Pull touch
// disjoint paths; Conflicts are paths that changed on both sides since the
// baseline and are reported without being touched.
type TwoWayPlan struct {
	Push      []Item
	Pull      []PullItem
	Conflicts []string
}

// PlanTwoWay classifies each path (the union of local, remote, and manifest
// paths) by comparing both sides to the manifest baseline. A change on only one
// side is applied in that direction; a change on both sides (other than a
// both-deleted, which is just cleaned up) is a conflict — reported, not touched.
func PlanTwoWay(local []LocalFile, idx map[string]remoteindex.Entry, m *manifest.Manifest) TwoWayPlan {
	localIdx := indexLocal(local)
	rels := map[string]bool{}
	for rel := range localIdx {
		rels[rel] = true
	}
	for rel := range idx {
		rels[rel] = true
	}
	m.Range(func(rel string, _ manifest.Entry) { rels[rel] = true })

	var p TwoWayPlan
	for rel := range rels {
		l, lok := localIdx[rel]
		r, rok := idx[rel]
		b, bok := m.Get(rel)

		lnew := !bok && lok
		ldel := bok && !lok
		lmod := bok && lok && (l.Size != b.Size || l.Mtime != b.LocalMtime)
		localChanged := lnew || ldel || lmod

		rnew := !bok && rok
		rdel := bok && !rok
		rmod := bok && rok && (r.Size != b.Size || r.Mtime != b.RemoteMtime)
		remoteChanged := rnew || rdel || rmod

		switch {
		case !localChanged && !remoteChanged:
			// in sync — nothing to do
		case localChanged && !remoteChanged:
			switch {
			case lnew:
				p.Push = append(p.Push, Item{Rel: rel, Op: OpUpload, Size: l.Size, Mtime: l.Mtime})
			case lmod:
				p.Push = append(p.Push, Item{Rel: rel, Op: OpOverwrite, RemoteID: b.RemoteID, Size: l.Size, Mtime: l.Mtime})
			case ldel:
				p.Push = append(p.Push, Item{Rel: rel, Op: OpDelete, RemoteID: b.RemoteID})
			}
		case remoteChanged && !localChanged:
			switch {
			case rnew, rmod:
				p.Pull = append(p.Pull, PullItem{Rel: rel, Op: PullDownload, RemoteID: r.ID, Size: r.Size, RemoteMtime: r.Mtime})
			case rdel:
				p.Pull = append(p.Pull, PullItem{Rel: rel, Op: PullDeleteLocal})
			}
		case ldel && rdel:
			// gone on both sides: clean up the stale baseline via an idempotent
			// local delete (the local file is already gone).
			p.Pull = append(p.Pull, PullItem{Rel: rel, Op: PullDeleteLocal})
		default:
			p.Conflicts = append(p.Conflicts, rel)
		}
	}
	sort.Strings(p.Conflicts) // stable, diffable reporting
	return p
}
