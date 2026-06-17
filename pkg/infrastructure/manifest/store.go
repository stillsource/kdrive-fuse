package manifest

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// Load reads a manifest from path. A missing file yields an empty manifest and
// a nil error — the first sync has no baseline yet.
func Load(path string) (*Manifest, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return New(), nil
		}
		return nil, fmt.Errorf("manifest: open %s: %w", path, err)
	}
	defer f.Close() //nolint:errcheck // read-only

	m := New()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024) // tolerate long path lines
	n := 0
	for sc.Scan() {
		n++
		text := sc.Text()
		if text == "" {
			continue
		}
		e, rel, err := parseLine(text)
		if err != nil {
			return nil, fmt.Errorf("manifest: %s line %d: %w", path, n, err)
		}
		m.entries[rel] = e
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("manifest: read %s: %w", path, err)
	}
	return m, nil
}

// parseLine parses one TSV record: size, local_mtime, remote_id, remote_mtime,
// relpath. relpath is everything after the fourth tab, so tabs inside a path
// are preserved.
func parseLine(text string) (Entry, string, error) {
	f := strings.SplitN(text, "\t", 5)
	if len(f) < 5 {
		return Entry{}, "", fmt.Errorf("expected 5 tab-separated fields, got %d", len(f))
	}
	var n [4]int64
	for i := 0; i < 4; i++ {
		v, err := strconv.ParseInt(f[i], 10, 64)
		if err != nil {
			return Entry{}, "", fmt.Errorf("field %d %q is not an integer", i+1, f[i])
		}
		n[i] = v
	}
	if f[4] == "" {
		return Entry{}, "", fmt.Errorf("empty relpath")
	}
	return Entry{Size: n[0], LocalMtime: n[1], RemoteID: n[2], RemoteMtime: n[3]}, f[4], nil
}

// Save writes the manifest to path atomically (temp file in the same directory,
// then rename), creating the parent directory if needed. Entries are written in
// sorted path order for stable, diffable output.
func (m *Manifest) Save(path string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("manifest: mkdir %s: %w", dir, err)
	}

	var data []byte
	for _, rel := range m.sortedKeys() {
		e := m.entries[rel]
		data = fmt.Appendf(data, "%d\t%d\t%d\t%d\t%s\n", e.Size, e.LocalMtime, e.RemoteID, e.RemoteMtime, rel)
	}

	tmp := path + ".tmp"
	out, err := os.Create(tmp)
	if err != nil {
		return fmt.Errorf("manifest: create temp: %w", err)
	}
	if _, err := out.Write(data); err != nil {
		_ = out.Close()
		_ = os.Remove(tmp)
		return fmt.Errorf("manifest: write: %w", err)
	}
	if err := out.Close(); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("manifest: close temp: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("manifest: rename: %w", err)
	}
	return nil
}

// sortedKeys returns the entry paths in lexical order.
func (m *Manifest) sortedKeys() []string {
	keys := make([]string, 0, len(m.entries))
	for k := range m.entries {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
