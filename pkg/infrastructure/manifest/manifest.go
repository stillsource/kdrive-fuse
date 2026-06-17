// Package manifest stores the per-sync baseline that kdrive sync compares each
// side against: for every path (relative to the sync root) it records the size
// and local mtime last pushed, plus the remote file id and remote mtime last
// seen. Carrying the remote id lets a steady-state push overwrite or delete by
// id with no remote listing.
package manifest

// Entry is the last-synced state of one file.
type Entry struct {
	Size        int64 // content size in bytes
	LocalMtime  int64 // local file mtime (Unix seconds) at last sync
	RemoteID    int64 // kDrive file id
	RemoteMtime int64 // remote last_modified_at (Unix seconds) at last sync
}

// Manifest maps a path (relative to the sync root) to its last-synced Entry.
type Manifest struct {
	entries map[string]Entry
}

// New returns an empty Manifest.
func New() *Manifest {
	return &Manifest{entries: make(map[string]Entry)}
}

// Get returns the entry for rel and whether it exists.
func (m *Manifest) Get(rel string) (Entry, bool) {
	e, ok := m.entries[rel]
	return e, ok
}

// Set records (or replaces) the entry for rel.
func (m *Manifest) Set(rel string, e Entry) {
	m.entries[rel] = e
}

// Delete removes the entry for rel, if present.
func (m *Manifest) Delete(rel string) {
	delete(m.entries, rel)
}

// Len returns the number of entries.
func (m *Manifest) Len() int {
	return len(m.entries)
}

// Range calls fn for every entry, in unspecified order.
func (m *Manifest) Range(fn func(rel string, e Entry)) {
	for rel, e := range m.entries {
		fn(rel, e)
	}
}
