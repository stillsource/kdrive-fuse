# kdrive remoteindex — Implementation Plan (PR #3)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add `pkg/infrastructure/remoteindex`: `Build`, a bounded-parallel recursive snapshot of a kDrive folder tree (`map[relpath]Entry{ID,Size,Mtime}`, files only), and `Resolver`, which maps a relative directory path to a remote folder id, creating missing directories. These are the remote-side inputs the sync engine (PR #4) consumes.

**Architecture:** Two focused files over minimal interfaces (`Lister`, `Mkdirer`) that the existing `kdriveapi` `*FilesService` already satisfies (`List`, `Mkdir`). `Build` uses `errgroup` + a semaphore bounding concurrent `List` calls (goroutines themselves are unbounded, so recursive spawning never deadlocks). `Resolver` serializes resolution under a mutex so a directory is never created twice, and caches resolved paths for the run.

**Tech Stack:** Go 1.26, `golang.org/x/sync/errgroup` (already vendored as indirect — `go mod tidy` promotes it to direct), `pkg/domain`. Ginkgo v2 + Gomega. Coverage gate ≥ 90 % on `./pkg/...`.

**Conventions (must follow):** English only. **No `Co-Authored-By` trailer.** Tests are Ginkgo specs in the **black-box** package `package remoteindex_test` (the package's `Entry` type would collide with Ginkgo's dot-imported `Entry` in a white-box test). errcheck is enabled for `pkg/` (ignore an error only via explicit `_ =` or `//nolint:errcheck`). Concurrency tests must pass under `-race`. Work on the existing branch `feat/kdrive-remoteindex`.

---

### Task 1: `Build` — recursive parallel index

**Files:**
- Create: `pkg/infrastructure/remoteindex/index.go`
- Create: `pkg/infrastructure/remoteindex/remoteindex_suite_test.go`
- Create: `pkg/infrastructure/remoteindex/index_test.go`

- [ ] **Step 1: Write the suite entry point**

Create `pkg/infrastructure/remoteindex/remoteindex_suite_test.go`:

```go
package remoteindex_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestRemoteIndex(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "RemoteIndex Suite")
}
```

- [ ] **Step 2: Write the failing test**

Create `pkg/infrastructure/remoteindex/index_test.go`:

```go
package remoteindex_test

import (
	"context"
	"errors"
	"sync"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/stillsource/kdrive-fuse/pkg/domain"
	"github.com/stillsource/kdrive-fuse/pkg/infrastructure/remoteindex"
)

// fakeLister is a concurrency-safe canned directory tree. Shared with resolver_test.go.
type fakeLister struct {
	mu      sync.Mutex
	folders map[int64][]domain.FileInfo
	errs    map[int64]error
	calls   map[int64]int
}

func (f *fakeLister) List(_ context.Context, folderID int64) ([]domain.FileInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls[folderID]++
	if f.errs != nil {
		if err := f.errs[folderID]; err != nil {
			return nil, err
		}
	}
	return f.folders[folderID], nil
}

var _ = Describe("Build", func() {
	dir := func(id int64, name string) domain.FileInfo {
		return domain.FileInfo{ID: id, Name: name, Type: domain.FileTypeDir}
	}
	file := func(id int64, name string, size, mtime int64) domain.FileInfo {
		return domain.FileInfo{ID: id, Name: name, Type: domain.FileTypeFile, Size: size, LastModifiedAt: mtime}
	}

	It("indexes files across a nested tree by relative path", func() {
		fl := &fakeLister{
			calls: map[int64]int{},
			folders: map[int64][]domain.FileInfo{
				1: {file(10, "root.txt", 3, 100), dir(2, "2025")},
				2: {dir(3, "11"), file(11, "top.jpg", 5, 101)},
				3: {file(20, "a.jpg", 7, 200), file(21, "b.jpg", 9, 201)},
			},
		}
		idx, err := remoteindex.Build(context.Background(), fl, 1)
		Expect(err).NotTo(HaveOccurred())
		Expect(idx).To(HaveLen(4))
		Expect(idx["root.txt"]).To(Equal(remoteindex.Entry{ID: 10, Size: 3, Mtime: 100}))
		Expect(idx["2025/top.jpg"]).To(Equal(remoteindex.Entry{ID: 11, Size: 5, Mtime: 101}))
		Expect(idx["2025/11/a.jpg"]).To(Equal(remoteindex.Entry{ID: 20, Size: 7, Mtime: 200}))
		Expect(idx["2025/11/b.jpg"]).To(Equal(remoteindex.Entry{ID: 21, Size: 9, Mtime: 201}))
	})

	It("returns an empty index for an empty root", func() {
		fl := &fakeLister{calls: map[int64]int{}, folders: map[int64][]domain.FileInfo{1: nil}}
		idx, err := remoteindex.Build(context.Background(), fl, 1)
		Expect(err).NotTo(HaveOccurred())
		Expect(idx).To(BeEmpty())
	})

	It("propagates a listing error", func() {
		fl := &fakeLister{
			calls:   map[int64]int{},
			errs:    map[int64]error{1: errors.New("boom")},
			folders: map[int64][]domain.FileInfo{},
		}
		_, err := remoteindex.Build(context.Background(), fl, 1)
		Expect(err).To(HaveOccurred())
	})
})
```

- [ ] **Step 3: Run the test to verify it fails**

Run: `go test ./pkg/infrastructure/remoteindex/ -v`
Expected: compile failure — `undefined: remoteindex.Build`, `undefined: remoteindex.Entry`.

- [ ] **Step 4: Write the implementation**

Create `pkg/infrastructure/remoteindex/index.go`:

```go
// Package remoteindex builds a recursive snapshot of a kDrive folder tree and
// resolves (creating as needed) a relative path to a folder id. It is the
// remote-side input to kdrive sync: Build maps every remote file to its id,
// size and mtime; Resolver turns a local file's directory into the remote
// folder id to upload into.
package remoteindex

import (
	"context"
	"sync"

	"golang.org/x/sync/errgroup"

	"github.com/stillsource/kdrive-fuse/pkg/domain"
)

// Entry is the remote metadata of one file, keyed by its path relative to the
// index root.
type Entry struct {
	ID    int64
	Size  int64
	Mtime int64 // remote last_modified_at (Unix seconds)
}

// Lister lists the direct children of a folder.
type Lister interface {
	List(ctx context.Context, folderID int64) ([]domain.FileInfo, error)
}

// defaultParallelism bounds the number of concurrent List calls during Build.
const defaultParallelism = 8

// Build walks the folder tree rooted at rootID and returns a map from each
// file's path (relative to the root, slash-separated) to its Entry. Directories
// are traversed but not themselves recorded. Listings run concurrently, bounded
// to defaultParallelism in-flight calls.
func Build(ctx context.Context, l Lister, rootID int64) (map[string]Entry, error) {
	idx := make(map[string]Entry)
	var mu sync.Mutex
	sem := make(chan struct{}, defaultParallelism)

	g, ctx := errgroup.WithContext(ctx)
	var walk func(folderID int64, prefix string)
	walk = func(folderID int64, prefix string) {
		g.Go(func() error {
			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
				return ctx.Err()
			}
			children, err := l.List(ctx, folderID)
			<-sem
			if err != nil {
				return err
			}
			for _, c := range children {
				rel := c.Name
				if prefix != "" {
					rel = prefix + "/" + c.Name
				}
				if c.IsDir() {
					walk(c.ID, rel)
				} else {
					mu.Lock()
					idx[rel] = Entry{ID: c.ID, Size: c.Size, Mtime: c.LastModifiedAt}
					mu.Unlock()
				}
			}
			return nil
		})
	}
	walk(rootID, "")
	if err := g.Wait(); err != nil {
		return nil, err
	}
	return idx, nil
}
```

- [ ] **Step 5: Tidy modules, then run the test to verify it passes (with race)**

Run:
```bash
go mod tidy
go test -race ./pkg/infrastructure/remoteindex/ -v
```
Expected: `go mod tidy` promotes `golang.org/x/sync` to a direct require (drops the `// indirect` comment); 3 specs PASS under the race detector.

- [ ] **Step 6: Commit**

```bash
git add pkg/infrastructure/remoteindex/index.go pkg/infrastructure/remoteindex/remoteindex_suite_test.go pkg/infrastructure/remoteindex/index_test.go go.mod go.sum
git commit -m "feat(remoteindex): bounded-parallel recursive folder index"
```

---

### Task 2: `Resolver` — resolve-or-create a directory path

**Files:**
- Create: `pkg/infrastructure/remoteindex/resolver.go`
- Create: `pkg/infrastructure/remoteindex/resolver_test.go`

- [ ] **Step 1: Write the failing test**

Create `pkg/infrastructure/remoteindex/resolver_test.go`:

```go
package remoteindex_test

import (
	"context"
	"fmt"
	"sync"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/stillsource/kdrive-fuse/pkg/domain"
	"github.com/stillsource/kdrive-fuse/pkg/infrastructure/remoteindex"
)

// fakeMkdirer records created directories and hands out fresh ids.
type fakeMkdirer struct {
	mu      sync.Mutex
	nextID  int64
	created []string // "parentID/name"
}

func (f *fakeMkdirer) Mkdir(_ context.Context, parentID int64, name string) (domain.FileInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.nextID++
	f.created = append(f.created, fmt.Sprintf("%d/%s", parentID, name))
	return domain.FileInfo{ID: 1000 + f.nextID, Name: name, Type: domain.FileTypeDir}, nil
}

var _ = Describe("Resolver", func() {
	It("resolves the root for empty, dot and slash", func() {
		r := remoteindex.NewResolver(&fakeLister{calls: map[int64]int{}}, &fakeMkdirer{}, 1)
		for _, p := range []string{"", ".", "/"} {
			id, err := r.Resolve(context.Background(), p)
			Expect(err).NotTo(HaveOccurred())
			Expect(id).To(Equal(int64(1)))
		}
	})

	It("returns an existing directory without creating it", func() {
		fl := &fakeLister{
			calls: map[int64]int{},
			folders: map[int64][]domain.FileInfo{
				1: {{ID: 2, Name: "2025", Type: domain.FileTypeDir}},
			},
		}
		mk := &fakeMkdirer{}
		r := remoteindex.NewResolver(fl, mk, 1)
		id, err := r.Resolve(context.Background(), "2025")
		Expect(err).NotTo(HaveOccurred())
		Expect(id).To(Equal(int64(2)))
		Expect(mk.created).To(BeEmpty())
	})

	It("creates missing directories along a nested path", func() {
		fl := &fakeLister{calls: map[int64]int{}, folders: map[int64][]domain.FileInfo{}}
		mk := &fakeMkdirer{}
		r := remoteindex.NewResolver(fl, mk, 1)
		id, err := r.Resolve(context.Background(), "2025/11/05")
		Expect(err).NotTo(HaveOccurred())
		Expect(id).NotTo(BeZero())
		Expect(mk.created).To(HaveLen(3)) // 2025, then 11, then 05
	})

	It("caches resolved paths so repeated resolves do no extra work", func() {
		fl := &fakeLister{calls: map[int64]int{}, folders: map[int64][]domain.FileInfo{}}
		mk := &fakeMkdirer{}
		r := remoteindex.NewResolver(fl, mk, 1)
		_, _ = r.Resolve(context.Background(), "a/b")
		_, _ = r.Resolve(context.Background(), "a/b")
		Expect(mk.created).To(HaveLen(2)) // a and b created once total
	})

	It("creates each directory exactly once under concurrent resolves", func() {
		fl := &fakeLister{calls: map[int64]int{}, folders: map[int64][]domain.FileInfo{}}
		mk := &fakeMkdirer{}
		r := remoteindex.NewResolver(fl, mk, 1)
		var wg sync.WaitGroup
		for i := 0; i < 20; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				_, _ = r.Resolve(context.Background(), "x/y/z")
			}()
		}
		wg.Wait()
		Expect(mk.created).To(HaveLen(3)) // x, y, z exactly once each
	})
})
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./pkg/infrastructure/remoteindex/ -v`
Expected: compile failure — `undefined: remoteindex.NewResolver`.

- [ ] **Step 3: Write the implementation**

Create `pkg/infrastructure/remoteindex/resolver.go`:

```go
package remoteindex

import (
	"context"
	"path"
	"sync"

	"github.com/stillsource/kdrive-fuse/pkg/domain"
)

// Mkdirer creates a directory under a parent folder.
type Mkdirer interface {
	Mkdir(ctx context.Context, parentID int64, name string) (domain.FileInfo, error)
}

// Resolver maps a directory path (relative to a root folder) to its remote
// folder id, creating any missing directories along the way. Resolved paths are
// cached for the resolver's lifetime. It is safe for concurrent use: resolution
// is serialized so a directory is never created twice.
type Resolver struct {
	lister Lister
	mkdir  Mkdirer
	rootID int64

	mu    sync.Mutex
	cache map[string]int64
}

// NewResolver returns a Resolver rooted at rootID.
func NewResolver(l Lister, mk Mkdirer, rootID int64) *Resolver {
	return &Resolver{lister: l, mkdir: mk, rootID: rootID, cache: map[string]int64{}}
}

// Resolve returns the folder id for relDir (slash-separated, relative to the
// root), creating directories that do not yet exist. An empty, "." or "/"
// relDir resolves to the root.
func (r *Resolver) Resolve(ctx context.Context, relDir string) (int64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.resolve(ctx, path.Clean(relDir))
}

// resolve resolves a cleaned path. The caller must hold r.mu.
func (r *Resolver) resolve(ctx context.Context, clean string) (int64, error) {
	switch clean {
	case "", ".", "/":
		return r.rootID, nil
	}
	if id, ok := r.cache[clean]; ok {
		return id, nil
	}
	parentID, err := r.resolve(ctx, path.Dir(clean))
	if err != nil {
		return 0, err
	}
	name := path.Base(clean)
	children, err := r.lister.List(ctx, parentID)
	if err != nil {
		return 0, err
	}
	for _, c := range children {
		if c.IsDir() && c.Name == name {
			r.cache[clean] = c.ID
			return c.ID, nil
		}
	}
	info, err := r.mkdir.Mkdir(ctx, parentID, name)
	if err != nil {
		return 0, err
	}
	r.cache[clean] = info.ID
	return info.ID, nil
}
```

- [ ] **Step 4: Run the test to verify it passes (with race)**

Run: `go test -race ./pkg/infrastructure/remoteindex/ -v`
Expected: PASS (8 specs total) under the race detector.

- [ ] **Step 5: Commit**

```bash
git add pkg/infrastructure/remoteindex/resolver.go pkg/infrastructure/remoteindex/resolver_test.go
git commit -m "feat(remoteindex): resolve-or-create directory path resolver"
```

---

## Verification (end of PR)

- [ ] `go build ./... && go vet ./...` — clean.
- [ ] `golangci-lint run ./...` — 0 issues.
- [ ] `go test -race ./pkg/infrastructure/remoteindex/...` — all pass under race.
- [ ] `go test -coverprofile=coverage.out -covermode=atomic -coverpkg=./pkg/... ./pkg/... ./cmd/...` then `go tool cover -func=coverage.out | awk '/^total:/{print $3}'` — all pass, total ≥ 90 %.
- [ ] `go mod tidy` left a clean tree (x/sync now a direct require; commit included go.mod/go.sum).
- [ ] Open a PR (base `main`) titled `feat: add the kdrive sync remote index`, referencing the design doc.

## Notes for later PRs (not this one)

- PR #4 (`sync_plan`/`sync_run`) consumes `Build` (bootstrap + pull) and `Resolver` (push, to place new files).
- `Resolver` serializes resolution; folder creation is rare relative to file transfers, so this is not a throughput concern.
