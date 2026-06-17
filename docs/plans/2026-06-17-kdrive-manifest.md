# kdrive manifest store — Implementation Plan (PR #2)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add `pkg/infrastructure/manifest`, the local store that records the last-synced baseline (per relative path: size, local mtime, remote file id, remote mtime) and persists it as a TSV at an XDG-keyed path. No sync engine yet — this is a self-contained, fully unit-tested store.

**Architecture:** A small infrastructure package with three focused files: the in-memory `Manifest` type and its accessors (`manifest.go`), TSV serialization + atomic file IO (`store.go`), and the XDG path keying (`path.go`). It depends on nothing internal. Carrying `RemoteID` in each entry is what later lets a steady-state push overwrite/delete by id with no remote listing.

**Tech Stack:** Go 1.26 stdlib only (`crypto/sha256`, `bufio`, `os`, `path/filepath`, `sort`, `strconv`, `strings`, `fmt`). Ginkgo v2 + Gomega. Module `github.com/stillsource/kdrive-fuse`. Coverage gate ≥ 90 % on `./pkg/...`.

**Conventions (must follow):** English only (comments, commit messages). **No `Co-Authored-By` trailer.** Tests are Ginkgo specs in the **black-box** external package `package manifest_test`, importing `manifest` and qualifying references (`manifest.New`, `manifest.Entry`, …). Black-box is required here: Ginkgo v2's dot-import exports a function named `Entry`, which would collide with this package's `Entry` type in a white-box (`package manifest`) test. errcheck is enabled for `pkg/` code: ignore an error only with an explicit `_ =` or a `//nolint:errcheck // <reason>` comment (matching `pkg/infrastructure/contentcache/disk.go`). Work on the existing branch `feat/kdrive-manifest`.

---

### Task 1: `Manifest` type and accessors

The in-memory model: a map from relative path to its last-synced `Entry`.

**Files:**
- Create: `pkg/infrastructure/manifest/manifest.go`
- Create: `pkg/infrastructure/manifest/manifest_suite_test.go`
- Create: `pkg/infrastructure/manifest/manifest_test.go`

- [ ] **Step 1: Write the suite entry point**

Create `pkg/infrastructure/manifest/manifest_suite_test.go`:

```go
package manifest_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestManifest(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Manifest Suite")
}
```

- [ ] **Step 2: Write the failing test**

Create `pkg/infrastructure/manifest/manifest_test.go`:

```go
package manifest_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/stillsource/kdrive-fuse/pkg/infrastructure/manifest"
)

var _ = Describe("Manifest", func() {
	It("starts empty", func() {
		m := manifest.New()
		Expect(m.Len()).To(Equal(0))
		_, ok := m.Get("a")
		Expect(ok).To(BeFalse())
	})

	It("sets and gets an entry", func() {
		m := manifest.New()
		e := manifest.Entry{Size: 10, LocalMtime: 100, RemoteID: 7, RemoteMtime: 200}
		m.Set("dir/a.jpg", e)
		got, ok := m.Get("dir/a.jpg")
		Expect(ok).To(BeTrue())
		Expect(got).To(Equal(e))
		Expect(m.Len()).To(Equal(1))
	})

	It("replaces an existing entry on Set", func() {
		m := manifest.New()
		m.Set("a", manifest.Entry{Size: 1})
		m.Set("a", manifest.Entry{Size: 2})
		got, _ := m.Get("a")
		Expect(got.Size).To(Equal(int64(2)))
		Expect(m.Len()).To(Equal(1))
	})

	It("deletes an entry and is a no-op for a missing key", func() {
		m := manifest.New()
		m.Set("a", manifest.Entry{Size: 1})
		m.Delete("a")
		_, ok := m.Get("a")
		Expect(ok).To(BeFalse())
		Expect(m.Len()).To(Equal(0))
		m.Delete("missing") // must not panic
	})

	It("ranges over all entries", func() {
		m := manifest.New()
		m.Set("a", manifest.Entry{Size: 1})
		m.Set("b", manifest.Entry{Size: 2})
		seen := map[string]int64{}
		m.Range(func(rel string, e manifest.Entry) { seen[rel] = e.Size })
		Expect(seen).To(Equal(map[string]int64{"a": 1, "b": 2}))
	})
})
```

- [ ] **Step 3: Run the test to verify it fails**

Run: `go test ./pkg/infrastructure/manifest/ -v`
Expected: compile failure — `undefined: manifest.New`, `undefined: manifest.Entry`.

- [ ] **Step 4: Write the minimal implementation**

Create `pkg/infrastructure/manifest/manifest.go`:

```go
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
```

- [ ] **Step 5: Run the test to verify it passes**

Run: `go test ./pkg/infrastructure/manifest/ -v`
Expected: PASS (5 specs).

- [ ] **Step 6: Commit**

```bash
git add pkg/infrastructure/manifest/manifest.go pkg/infrastructure/manifest/manifest_suite_test.go pkg/infrastructure/manifest/manifest_test.go
git commit -m "feat(manifest): in-memory baseline store with accessors"
```

---

### Task 2: TSV serialization + atomic file IO

`Load` and `Save` persist the manifest as a tab-separated file, written atomically. The relative path is the last field so tabs in names survive a round-trip.

**Files:**
- Create: `pkg/infrastructure/manifest/store.go`
- Create: `pkg/infrastructure/manifest/store_test.go`

- [ ] **Step 1: Write the failing test**

Create `pkg/infrastructure/manifest/store_test.go`:

```go
package manifest_test

import (
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/stillsource/kdrive-fuse/pkg/infrastructure/manifest"
)

var _ = Describe("Load and Save", func() {
	var dir string
	BeforeEach(func() { dir = GinkgoT().TempDir() })

	It("returns an empty manifest when the file is missing", func() {
		m, err := manifest.Load(filepath.Join(dir, "nope.tsv"))
		Expect(err).NotTo(HaveOccurred())
		Expect(m.Len()).To(Equal(0))
	})

	It("round-trips entries through Save and Load", func() {
		path := filepath.Join(dir, "m.tsv")
		m := manifest.New()
		m.Set("2025/a.jpg", manifest.Entry{Size: 10, LocalMtime: 100, RemoteID: 7, RemoteMtime: 200})
		m.Set("b.xmp", manifest.Entry{Size: 3, LocalMtime: 1, RemoteID: 8, RemoteMtime: 2})
		Expect(m.Save(path)).To(Succeed())

		got, err := manifest.Load(path)
		Expect(err).NotTo(HaveOccurred())
		Expect(got.Len()).To(Equal(2))
		a, ok := got.Get("2025/a.jpg")
		Expect(ok).To(BeTrue())
		Expect(a).To(Equal(manifest.Entry{Size: 10, LocalMtime: 100, RemoteID: 7, RemoteMtime: 200}))
	})

	It("creates the parent directory on Save", func() {
		path := filepath.Join(dir, "sub", "deep", "m.tsv")
		m := manifest.New()
		m.Set("a", manifest.Entry{Size: 1})
		Expect(m.Save(path)).To(Succeed())
		_, err := os.Stat(path)
		Expect(err).NotTo(HaveOccurred())
	})

	It("writes entries in sorted path order", func() {
		path := filepath.Join(dir, "m.tsv")
		m := manifest.New()
		m.Set("c", manifest.Entry{Size: 1})
		m.Set("a", manifest.Entry{Size: 1})
		m.Set("b", manifest.Entry{Size: 1})
		Expect(m.Save(path)).To(Succeed())
		data, err := os.ReadFile(path)
		Expect(err).NotTo(HaveOccurred())
		Expect(string(data)).To(Equal("1\t0\t0\t0\ta\n1\t0\t0\t0\tb\n1\t0\t0\t0\tc\n"))
	})

	It("preserves a tab inside a path name", func() {
		path := filepath.Join(dir, "m.tsv")
		rel := "weird\tname.jpg"
		m := manifest.New()
		m.Set(rel, manifest.Entry{Size: 5, LocalMtime: 1, RemoteID: 2, RemoteMtime: 3})
		Expect(m.Save(path)).To(Succeed())
		got, err := manifest.Load(path)
		Expect(err).NotTo(HaveOccurred())
		e, ok := got.Get(rel)
		Expect(ok).To(BeTrue())
		Expect(e.Size).To(Equal(int64(5)))
	})

	It("errors on a line with too few fields", func() {
		path := filepath.Join(dir, "bad.tsv")
		Expect(os.WriteFile(path, []byte("not\tenough\tfields\n"), 0o644)).To(Succeed())
		_, err := manifest.Load(path)
		Expect(err).To(HaveOccurred())
	})

	It("errors on a non-integer numeric field", func() {
		path := filepath.Join(dir, "bad2.tsv")
		Expect(os.WriteFile(path, []byte("x\t0\t0\t0\ta\n"), 0o644)).To(Succeed())
		_, err := manifest.Load(path)
		Expect(err).To(HaveOccurred())
	})
})
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./pkg/infrastructure/manifest/ -v`
Expected: compile failure — `undefined: manifest.Load`, `m.Save undefined`.

- [ ] **Step 3: Write the implementation**

Create `pkg/infrastructure/manifest/store.go`:

```go
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
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./pkg/infrastructure/manifest/ -v`
Expected: PASS (12 specs total).

- [ ] **Step 5: Commit**

```bash
git add pkg/infrastructure/manifest/store.go pkg/infrastructure/manifest/store_test.go
git commit -m "feat(manifest): TSV load/save with atomic write"
```

---

### Task 3: XDG-keyed manifest path

`PathFor` maps a (local root, remote root) pair to a stable on-disk location so each sync pairing has its own manifest, kept outside the synced tree.

**Files:**
- Create: `pkg/infrastructure/manifest/path.go`
- Create: `pkg/infrastructure/manifest/path_test.go`

- [ ] **Step 1: Write the failing test**

Create `pkg/infrastructure/manifest/path_test.go`:

```go
package manifest_test

import (
	"path/filepath"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/stillsource/kdrive-fuse/pkg/infrastructure/manifest"
)

var _ = Describe("PathFor", func() {
	It("places the manifest under XDG_STATE_HOME/kdrive when set", func() {
		GinkgoT().Setenv("XDG_STATE_HOME", "/xdg/state")
		p, err := manifest.PathFor("/home/u/Pictures", "Rémanence")
		Expect(err).NotTo(HaveOccurred())
		Expect(p).To(HavePrefix("/xdg/state/kdrive/"))
		Expect(strings.HasSuffix(p, ".tsv")).To(BeTrue())
	})

	It("is stable for the same pair and differs across pairs", func() {
		GinkgoT().Setenv("XDG_STATE_HOME", "/xdg/state")
		a, _ := manifest.PathFor("/home/u/Pictures", "Rémanence")
		again, _ := manifest.PathFor("/home/u/Pictures", "Rémanence")
		other, _ := manifest.PathFor("/home/u/Pictures", "Other")
		Expect(a).To(Equal(again))
		Expect(a).NotTo(Equal(other))
	})

	It("falls back to ~/.local/state/kdrive when XDG_STATE_HOME is unset", func() {
		GinkgoT().Setenv("XDG_STATE_HOME", "")
		p, err := manifest.PathFor("rel/dir", "R")
		Expect(err).NotTo(HaveOccurred())
		Expect(p).To(ContainSubstring(filepath.Join(".local", "state", "kdrive")))
		Expect(strings.HasSuffix(p, ".tsv")).To(BeTrue())
	})
})
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./pkg/infrastructure/manifest/ -v`
Expected: compile failure — `undefined: manifest.PathFor`.

- [ ] **Step 3: Write the implementation**

Create `pkg/infrastructure/manifest/path.go`:

```go
package manifest

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
)

// PathFor returns the on-disk location of the manifest for a given
// (local root, remote root) pair: $XDG_STATE_HOME/kdrive/<key>.tsv, falling
// back to ~/.local/state/kdrive when XDG_STATE_HOME is unset. The key is a hash
// of the absolute local root and the remote root, so each pairing has its own
// stable manifest kept outside the synced tree.
func PathFor(localRoot, remoteRoot string) (string, error) {
	abs, err := filepath.Abs(localRoot)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(abs + "\n" + remoteRoot))
	key := hex.EncodeToString(sum[:])
	return filepath.Join(stateDir(), "kdrive", key+".tsv"), nil
}

// stateDir returns the XDG state base directory.
func stateDir() string {
	if d := os.Getenv("XDG_STATE_HOME"); d != "" {
		return d
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "state")
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./pkg/infrastructure/manifest/ -v`
Expected: PASS (15 specs total).

- [ ] **Step 5: Commit**

```bash
git add pkg/infrastructure/manifest/path.go pkg/infrastructure/manifest/path_test.go
git commit -m "feat(manifest): XDG-keyed manifest path resolver"
```

---

## Verification (end of PR)

- [ ] `go build ./... && go vet ./...` — clean.
- [ ] `golangci-lint run ./...` — 0 issues. (errcheck is enabled for `pkg/`; the unchecked closes/removes use `_ =` or `//nolint:errcheck`, and content is built with `fmt.Appendf` which returns no error.)
- [ ] `go test -coverprofile=coverage.out -covermode=atomic -coverpkg=./pkg/... ./pkg/... ./cmd/...` then `go tool cover -func=coverage.out | awk '/^total:/{print $3}'` — all suites pass, total ≥ 90 %.
- [ ] Open a PR (base `main`) titled `feat: add the kdrive sync manifest store`, referencing the design doc (`docs/design/2026-06-17-kdrive-cli-sync.md`).

## Notes for later PRs (not this one)

- PR #3 adds `remoteindex` (recursive parallel listing + path resolver).
- PR #4 adds `sync_plan` / `sync_run`, which consume this manifest (`Get`/`Set`/`Delete`/`Range` + `Load`/`Save`/`PathFor`).
- The `Manifest` is not safe for concurrent mutation; the sync engine will own it from a single goroutine and hand transfer results back to it serially.
