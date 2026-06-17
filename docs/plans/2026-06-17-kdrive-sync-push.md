# kdrive sync push — Implementation Plan (PR #5)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Wire the `kdrive sync` command (push direction): walk the local tree, bootstrap/plan/run against the manifest, and mirror local → remote, with `--dry-run`, a deletion guard, `--no-delete`, `--assume-new`, and `--jobs`.

**Architecture:** All logic lands in `pkg/syncer` so it is unit-testable end-to-end against an in-memory fake remote; `cli/sync.go` is thin wiring. New pieces: `WalkLocal` (local tree → `[]LocalFile`), `GuardDeletes` (pure safety check), `PushExecutor` (concrete `Executor` over the service ports + a `remoteindex.Resolver`), and `Push` (the orchestration: walk → bootstrap → plan → guard → dry-run/run → save). The command parses flags, builds the API client from `appconfig`, resolves the remote root path to a folder id, and calls `Push`.

**Tech Stack:** Go 1.26 stdlib, `pkg/infrastructure/{manifest,remoteindex,kdriveapi,di}`, `pkg/service`, `pkg/appconfig`, `pkg/syncer`. Ginkgo v2 + Gomega. Coverage ≥ 90 % on `./pkg/...`.

**Conventions (must follow):** English only. **No `Co-Authored-By` trailer.** Tests black-box (`package syncer_test` / `package cli_test`). errcheck enabled for `pkg/` (use `_ =` / `//nolint:errcheck`). Concurrency-touching tests under `-race`. Work on the existing branch `feat/kdrive-sync-push`.

---

### Task 1: `WalkLocal` and `GuardDeletes`

**Files:**
- Create: `pkg/syncer/walk.go`
- Create: `pkg/syncer/guard.go`
- Create: `pkg/syncer/walk_test.go`
- Create: `pkg/syncer/guard_test.go`

- [ ] **Step 1: Write the failing tests**

Create `pkg/syncer/walk_test.go`:

```go
package syncer_test

import (
	"os"
	"path/filepath"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/stillsource/kdrive-fuse/pkg/syncer"
)

func relsOf(files []syncer.LocalFile) map[string]syncer.LocalFile {
	m := map[string]syncer.LocalFile{}
	for _, f := range files {
		m[f.Rel] = f
	}
	return m
}

var _ = Describe("WalkLocal", func() {
	var root string
	BeforeEach(func() { root = GinkgoT().TempDir() })

	write := func(rel string, data string) {
		p := filepath.Join(root, filepath.FromSlash(rel))
		Expect(os.MkdirAll(filepath.Dir(p), 0o755)).To(Succeed())
		Expect(os.WriteFile(p, []byte(data), 0o644)).To(Succeed())
	}

	It("returns regular files with slash relpaths, sizes and mtimes", func() {
		write("a.txt", "abc")
		write("2025/11/b.jpg", "hello")
		mtime := time.Unix(1700000000, 0)
		Expect(os.Chtimes(filepath.Join(root, "a.txt"), mtime, mtime)).To(Succeed())

		files, err := syncer.WalkLocal(root)
		Expect(err).NotTo(HaveOccurred())
		got := relsOf(files)
		Expect(got).To(HaveLen(2))
		Expect(got["a.txt"].Size).To(Equal(int64(3)))
		Expect(got["a.txt"].Mtime).To(Equal(int64(1700000000)))
		Expect(got["2025/11/b.jpg"].Size).To(Equal(int64(5)))
	})

	It("prunes .dtrash and \"à trier\"", func() {
		write("keep.txt", "x")
		write(".dtrash/old.txt", "x")
		write("à trier/pending.jpg", "x")
		files, err := syncer.WalkLocal(root)
		Expect(err).NotTo(HaveOccurred())
		got := relsOf(files)
		Expect(got).To(HaveKey("keep.txt"))
		Expect(got).NotTo(HaveKey(".dtrash/old.txt"))
		Expect(got).NotTo(HaveKey("à trier/pending.jpg"))
	})

	It("returns an error for a missing root", func() {
		_, err := syncer.WalkLocal(filepath.Join(root, "nope"))
		Expect(err).To(HaveOccurred())
	})
})
```

Create `pkg/syncer/guard_test.go`:

```go
package syncer_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/stillsource/kdrive-fuse/pkg/syncer"
)

var _ = Describe("GuardDeletes", func() {
	dels := func(n int) []syncer.Item {
		var items []syncer.Item
		for i := 0; i < n; i++ {
			items = append(items, syncer.Item{Op: syncer.OpDelete})
		}
		return items
	}

	It("passes when deletes are at or under 20% of the baseline", func() {
		Expect(syncer.GuardDeletes(dels(2), 10, false)).To(Succeed()) // 20%
	})

	It("fails when deletes exceed 20% of the baseline", func() {
		Expect(syncer.GuardDeletes(dels(3), 10, false)).To(HaveOccurred()) // 30%
	})

	It("passes when forced", func() {
		Expect(syncer.GuardDeletes(dels(9), 10, true)).To(Succeed())
	})

	It("passes with an empty baseline", func() {
		Expect(syncer.GuardDeletes(dels(5), 0, false)).To(Succeed())
	})
})
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./pkg/syncer/ -v`
Expected: compile failure — `undefined: syncer.WalkLocal`, `undefined: syncer.GuardDeletes`.

- [ ] **Step 3: Write `pkg/syncer/walk.go`**

```go
package syncer

import (
	"io/fs"
	"path/filepath"
)

// prunedDirs are directory names skipped during a local walk: the digiKam trash
// and the culling staging folder, neither of which is part of the archive.
var prunedDirs = map[string]bool{".dtrash": true, "à trier": true}

// WalkLocal walks root and returns every regular file as a LocalFile with its
// path relative to root (slash-separated), size, and mtime (Unix seconds). The
// .dtrash and "à trier" directories are pruned.
func WalkLocal(root string) ([]LocalFile, error) {
	var files []LocalFile
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if p != root && prunedDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		files = append(files, LocalFile{
			Rel:   filepath.ToSlash(rel),
			Size:  info.Size(),
			Mtime: info.ModTime().Unix(),
		})
		return nil
	})
	if err != nil {
		return nil, err
	}
	return files, nil
}
```

- [ ] **Step 4: Write `pkg/syncer/guard.go`**

```go
package syncer

import "fmt"

// deleteDivisor sets the deletion guard threshold: a run refuses to delete more
// than 1/deleteDivisor of the baseline manifest (i.e. 20%) without --force. This
// guards against a lost manifest or a wrong root silently wiping the remote.
const deleteDivisor = 5

// GuardDeletes returns an error when items would delete more than 20% of the
// baseline manifest entries, unless force is set. An empty baseline always
// passes (a first run has nothing to over-delete).
func GuardDeletes(items []Item, baseline int, force bool) error {
	if force || baseline == 0 {
		return nil
	}
	dels := 0
	for _, it := range items {
		if it.Op == OpDelete {
			dels++
		}
	}
	if dels*deleteDivisor > baseline {
		return fmt.Errorf("refusing to delete %d of %d tracked files (>%d%%); re-run with --force to override", dels, baseline, 100/deleteDivisor)
	}
	return nil
}
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test ./pkg/syncer/ -v`
Expected: PASS (existing 9 + 7 new = 16 specs).

- [ ] **Step 6: Commit**

```bash
git add pkg/syncer/walk.go pkg/syncer/guard.go pkg/syncer/walk_test.go pkg/syncer/guard_test.go
git commit -m "feat(syncer): local tree walk and deletion guard"
```

---

### Task 2: `PushExecutor` — the concrete Executor

**Files:**
- Create: `pkg/syncer/executor.go`
- Create: `pkg/syncer/executor_test.go`

- [ ] **Step 1: Write the failing test**

Create `pkg/syncer/executor_test.go`:

```go
package syncer_test

import (
	"context"
	"io"
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/stillsource/kdrive-fuse/pkg/domain"
	"github.com/stillsource/kdrive-fuse/pkg/infrastructure/remoteindex"
	"github.com/stillsource/kdrive-fuse/pkg/service"
	"github.com/stillsource/kdrive-fuse/pkg/syncer"
)

// recordingFiles implements remoteindex.Lister, remoteindex.Mkdirer and
// service.FileWriter/FileManager for executor tests.
type recordingFiles struct {
	folders map[int64][]domain.FileInfo // existing children by folder id
	nextID  int64
	uploads []service.UploadInput
	deleted []int64
}

func (r *recordingFiles) List(_ context.Context, folderID int64) ([]domain.FileInfo, error) {
	return r.folders[folderID], nil
}

func (r *recordingFiles) Mkdir(_ context.Context, parentID int64, name string) (domain.FileInfo, error) {
	r.nextID++
	info := domain.FileInfo{ID: 2000 + r.nextID, Name: name, Type: domain.FileTypeDir}
	r.folders[parentID] = append(r.folders[parentID], info)
	return info, nil
}

func (r *recordingFiles) Upload(_ context.Context, in service.UploadInput) (domain.FileInfo, error) {
	body, _ := io.ReadAll(in.Body)
	r.uploads = append(r.uploads, in)
	r.nextID++
	return domain.FileInfo{ID: 3000 + r.nextID, Name: in.Name, Size: int64(len(body)), LastModifiedAt: 4242}, nil
}

func (r *recordingFiles) Delete(_ context.Context, fileID int64) error {
	r.deleted = append(r.deleted, fileID)
	return nil
}

var _ = Describe("PushExecutor", func() {
	var (
		root  string
		files *recordingFiles
		ex    *syncer.PushExecutor
	)
	BeforeEach(func() {
		root = GinkgoT().TempDir()
		files = &recordingFiles{folders: map[int64][]domain.FileInfo{}}
		resolver := remoteindex.NewResolver(files, files, 1)
		ex = syncer.NewPushExecutor(root, resolver, files, files)
	})
	writeLocal := func(rel, data string) {
		p := filepath.Join(root, filepath.FromSlash(rel))
		Expect(os.MkdirAll(filepath.Dir(p), 0o755)).To(Succeed())
		Expect(os.WriteFile(p, []byte(data), 0o644)).To(Succeed())
	}

	It("uploads a new file under its resolved parent folder", func() {
		writeLocal("2025/a.jpg", "hello")
		id, mtime, err := ex.Upload(context.Background(), "2025/a.jpg", 5)
		Expect(err).NotTo(HaveOccurred())
		Expect(id).NotTo(BeZero())
		Expect(mtime).To(Equal(int64(4242)))
		Expect(files.uploads).To(HaveLen(1))
		Expect(files.uploads[0].Name).To(Equal("a.jpg"))
		Expect(files.uploads[0].ParentID).NotTo(BeZero()) // resolved/created "2025"
		Expect(files.uploads[0].ExistingFileID).To(BeZero())
		Expect(files.uploads[0].Size).To(Equal(int64(5)))
	})

	It("overwrites by existing file id", func() {
		writeLocal("a.jpg", "world!")
		mtime, err := ex.Overwrite(context.Background(), "a.jpg", 77, 6)
		Expect(err).NotTo(HaveOccurred())
		Expect(mtime).To(Equal(int64(4242)))
		Expect(files.uploads).To(HaveLen(1))
		Expect(files.uploads[0].ExistingFileID).To(Equal(int64(77)))
		Expect(files.uploads[0].ParentID).To(BeZero())
	})

	It("deletes by remote id", func() {
		Expect(ex.Delete(context.Background(), "x.jpg", 99)).To(Succeed())
		Expect(files.deleted).To(Equal([]int64{99}))
	})

	It("errors when the local file is missing", func() {
		_, _, err := ex.Upload(context.Background(), "missing.jpg", 0)
		Expect(err).To(HaveOccurred())
	})
})
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./pkg/syncer/ -v`
Expected: compile failure — `undefined: syncer.NewPushExecutor` / `syncer.PushExecutor`.

- [ ] **Step 3: Write `pkg/syncer/executor.go`**

```go
package syncer

import (
	"context"
	"fmt"
	"os"
	"path"
	"path/filepath"

	"github.com/stillsource/kdrive-fuse/pkg/infrastructure/remoteindex"
	"github.com/stillsource/kdrive-fuse/pkg/service"
)

// PushExecutor is the concrete Executor: it resolves the remote parent folder,
// opens the local file, and uploads or deletes through the service ports.
type PushExecutor struct {
	localRoot string
	resolver  *remoteindex.Resolver
	writer    service.FileWriter
	manager   service.FileManager
}

// NewPushExecutor builds a PushExecutor that reads files under localRoot, places
// new files under the folders resolver resolves/creates, uploads via w, and
// deletes via mgr.
func NewPushExecutor(localRoot string, resolver *remoteindex.Resolver, w service.FileWriter, mgr service.FileManager) *PushExecutor {
	return &PushExecutor{localRoot: localRoot, resolver: resolver, writer: w, manager: mgr}
}

// Upload creates a new remote file from localRoot/rel under its resolved parent.
func (e *PushExecutor) Upload(ctx context.Context, rel string, size int64) (int64, int64, error) {
	parentID, err := e.resolver.Resolve(ctx, path.Dir(rel))
	if err != nil {
		return 0, 0, fmt.Errorf("resolve parent of %s: %w", rel, err)
	}
	f, err := os.Open(e.local(rel))
	if err != nil {
		return 0, 0, err
	}
	defer f.Close() //nolint:errcheck // read-only
	info, err := e.writer.Upload(ctx, service.UploadInput{
		ParentID: parentID,
		Name:     path.Base(rel),
		Body:     f,
		Size:     size,
	})
	if err != nil {
		return 0, 0, err
	}
	return info.ID, info.LastModifiedAt, nil
}

// Overwrite replaces remote file remoteID with the content of localRoot/rel.
func (e *PushExecutor) Overwrite(ctx context.Context, rel string, remoteID, size int64) (int64, error) {
	f, err := os.Open(e.local(rel))
	if err != nil {
		return 0, err
	}
	defer f.Close() //nolint:errcheck // read-only
	info, err := e.writer.Upload(ctx, service.UploadInput{
		ExistingFileID: remoteID,
		Name:           path.Base(rel),
		Body:           f,
		Size:           size,
	})
	if err != nil {
		return 0, err
	}
	return info.LastModifiedAt, nil
}

// Delete removes remote file remoteID.
func (e *PushExecutor) Delete(ctx context.Context, _ string, remoteID int64) error {
	return e.manager.Delete(ctx, remoteID)
}

func (e *PushExecutor) local(rel string) string {
	return filepath.Join(e.localRoot, filepath.FromSlash(rel))
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./pkg/syncer/ -v`
Expected: PASS (20 specs total).

- [ ] **Step 5: Commit**

```bash
git add pkg/syncer/executor.go pkg/syncer/executor_test.go
git commit -m "feat(syncer): concrete push executor over the service ports"
```

---

### Task 3: `Push` — the orchestration

**Files:**
- Create: `pkg/syncer/push.go`
- Create: `pkg/syncer/push_test.go`

- [ ] **Step 1: Write the failing test**

Create `pkg/syncer/push_test.go`:

```go
package syncer_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/stillsource/kdrive-fuse/pkg/syncer"
)

var _ = Describe("Push", func() {
	var (
		root  string
		mpath string
		files *recordingFiles
	)
	BeforeEach(func() {
		root = GinkgoT().TempDir()
		mpath = filepath.Join(GinkgoT().TempDir(), "m.tsv")
		files = &recordingFiles{folders: map[int64][]domain.FileInfo{}}
	})
	writeLocal := func(rel, data string) {
		p := filepath.Join(root, filepath.FromSlash(rel))
		Expect(os.MkdirAll(filepath.Dir(p), 0o755)).To(Succeed())
		Expect(os.WriteFile(p, []byte(data), 0o644)).To(Succeed())
	}
	opts := func() syncer.PushOptions {
		return syncer.PushOptions{LocalRoot: root, Jobs: 4}
	}

	It("uploads every local file on a first push and saves the manifest", func() {
		writeLocal("a.jpg", "aaa")
		writeLocal("2025/b.jpg", "bbbbb")
		var out strings.Builder
		res, err := syncer.Push(context.Background(), opts(), files, 1, mpath, &out)
		Expect(err).NotTo(HaveOccurred())
		Expect(res.Uploaded).To(Equal(2))
		Expect(files.uploads).To(HaveLen(2))
		_, err = os.Stat(mpath)
		Expect(err).NotTo(HaveOccurred()) // manifest saved
	})

	It("is a no-op on a second identical push", func() {
		writeLocal("a.jpg", "aaa")
		_, err := syncer.Push(context.Background(), opts(), files, 1, mpath, &strings.Builder{})
		Expect(err).NotTo(HaveOccurred())
		files.uploads = nil // reset
		res, err := syncer.Push(context.Background(), opts(), files, 1, mpath, &strings.Builder{})
		Expect(err).NotTo(HaveOccurred())
		Expect(res).To(Equal(syncer.Result{}))
		Expect(files.uploads).To(BeEmpty())
	})

	It("dry-run prints a plan and changes nothing", func() {
		writeLocal("a.jpg", "aaa")
		o := opts()
		o.DryRun = true
		var out strings.Builder
		res, err := syncer.Push(context.Background(), o, files, 1, mpath, &out)
		Expect(err).NotTo(HaveOccurred())
		Expect(res).To(Equal(syncer.Result{}))
		Expect(files.uploads).To(BeEmpty())
		Expect(out.String()).To(ContainSubstring("dry-run"))
		Expect(out.String()).To(ContainSubstring("a.jpg"))
		_, statErr := os.Stat(mpath)
		Expect(os.IsNotExist(statErr)).To(BeTrue()) // no manifest written
	})

	It("--no-delete suppresses deletions", func() {
		writeLocal("a.jpg", "aaa")
		_, err := syncer.Push(context.Background(), opts(), files, 1, mpath, &strings.Builder{})
		Expect(err).NotTo(HaveOccurred())
		Expect(os.Remove(filepath.Join(root, "a.jpg"))).To(Succeed()) // local delete
		o := opts()
		o.NoDelete = true
		res, err := syncer.Push(context.Background(), o, files, 1, mpath, &strings.Builder{})
		Expect(err).NotTo(HaveOccurred())
		Expect(res.Deleted).To(Equal(0))
		Expect(files.deleted).To(BeEmpty())
	})
})
```

Note: `push_test.go` uses `domain.FileInfo` in the `recordingFiles` literal — add the import `"github.com/stillsource/kdrive-fuse/pkg/domain"` to this file (the `recordingFiles` type itself lives in `executor_test.go`, same `syncer_test` package).

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./pkg/syncer/ -v`
Expected: compile failure — `undefined: syncer.Push` / `syncer.PushOptions`.

- [ ] **Step 3: Write `pkg/syncer/push.go`**

```go
package syncer

import (
	"context"
	"fmt"
	"io"

	"github.com/stillsource/kdrive-fuse/pkg/infrastructure/manifest"
	"github.com/stillsource/kdrive-fuse/pkg/infrastructure/remoteindex"
	"github.com/stillsource/kdrive-fuse/pkg/service"
)

// FilesPort is the remote surface Push needs. It is satisfied by the kDrive API
// client's *FilesService (which implements all four embedded interfaces).
type FilesPort interface {
	remoteindex.Lister
	remoteindex.Mkdirer
	service.FileWriter
	service.FileManager
}

// PushOptions configures a push run.
type PushOptions struct {
	LocalRoot string
	Jobs      int
	Force     bool
	DryRun    bool
	NoDelete  bool
	AssumeNew bool
}

// Push mirrors opts.LocalRoot onto the remote folder rootID, using the manifest
// at manifestPath as the baseline. On an empty manifest (and unless AssumeNew)
// it bootstraps from a remote index so an already-uploaded tree is not re-pushed
// wholesale. In DryRun it prints the plan to out and changes nothing; otherwise
// it executes the plan and saves the updated manifest.
func Push(ctx context.Context, opts PushOptions, files FilesPort, rootID int64, manifestPath string, out io.Writer) (Result, error) {
	local, err := WalkLocal(opts.LocalRoot)
	if err != nil {
		return Result{}, fmt.Errorf("walk %s: %w", opts.LocalRoot, err)
	}
	m, err := manifest.Load(manifestPath)
	if err != nil {
		return Result{}, err
	}
	if m.Len() == 0 && !opts.AssumeNew {
		idx, err := remoteindex.Build(ctx, files, rootID)
		if err != nil {
			return Result{}, fmt.Errorf("remote index: %w", err)
		}
		Bootstrap(m, idx, local)
	}
	items := PlanPush(local, m)
	if opts.NoDelete {
		items = dropDeletes(items)
	}
	if err := GuardDeletes(items, m.Len(), opts.Force); err != nil {
		return Result{}, err
	}
	if opts.DryRun {
		printPlan(out, items)
		return Result{}, nil
	}
	resolver := remoteindex.NewResolver(files, files, rootID)
	exec := NewPushExecutor(opts.LocalRoot, resolver, files, files)
	res := RunPush(ctx, items, exec, m, opts.Jobs)
	if err := m.Save(manifestPath); err != nil {
		return res, fmt.Errorf("save manifest: %w", err)
	}
	return res, nil
}

func dropDeletes(items []Item) []Item {
	kept := make([]Item, 0, len(items))
	for _, it := range items {
		if it.Op != OpDelete {
			kept = append(kept, it)
		}
	}
	return kept
}

func printPlan(out io.Writer, items []Item) {
	var up, ov, del int
	for _, it := range items {
		switch it.Op {
		case OpUpload:
			up++
		case OpOverwrite:
			ov++
		case OpDelete:
			del++
		}
	}
	_, _ = fmt.Fprintf(out, "dry-run: %d to upload, %d to overwrite, %d to delete\n", up, ov, del)
	for _, it := range items {
		_, _ = fmt.Fprintf(out, "  %-9s %s\n", opName(it.Op), it.Rel)
	}
}

func opName(op Op) string {
	switch op {
	case OpUpload:
		return "upload"
	case OpOverwrite:
		return "overwrite"
	case OpDelete:
		return "delete"
	default:
		return "?"
	}
}
```

- [ ] **Step 4: Run the test to verify it passes (with race)**

Run: `go test -race ./pkg/syncer/ -v`
Expected: PASS (24 specs total) under the race detector.

- [ ] **Step 5: Commit**

```bash
git add pkg/syncer/push.go pkg/syncer/push_test.go
git commit -m "feat(syncer): push orchestration (walk, bootstrap, plan, run, save)"
```

---

### Task 4: `kdrive sync` command

**Files:**
- Create: `pkg/presentation/cli/sync.go`
- Create: `pkg/presentation/cli/sync_test.go`
- Modify: `pkg/presentation/cli/root.go`

- [ ] **Step 1: Write the failing test**

Create `pkg/presentation/cli/sync_test.go`:

```go
package cli_test

import (
	"bytes"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/stillsource/kdrive-fuse/pkg/presentation/cli"
)

var _ = Describe("sync command flag handling", func() {
	var out, errb *bytes.Buffer
	BeforeEach(func() {
		out = &bytes.Buffer{}
		errb = &bytes.Buffer{}
	})

	It("prints sync help on --help and exits 0", func() {
		Expect(cli.Run([]string{"sync", "--help"}, "dev", out, errb)).To(Equal(0))
		Expect(out.String()).To(ContainSubstring("kdrive sync"))
		Expect(out.String()).To(ContainSubstring("--dry-run"))
	})

	It("rejects an unknown flag with exit 2", func() {
		Expect(cli.Run([]string{"sync", "--bogus"}, "dev", out, errb)).To(Equal(2))
		Expect(errb.String()).NotTo(BeEmpty())
	})

	It("rejects too many positional arguments with exit 2", func() {
		Expect(cli.Run([]string{"sync", "a", "b", "c"}, "dev", out, errb)).To(Equal(2))
	})
})
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./pkg/presentation/cli/ -v`
Expected: the `sync` cases fail — `sync` is currently dispatched as an unknown command (exit 2 with "unknown command"), so the `--help` expectation (exit 0, "kdrive sync") fails.

- [ ] **Step 3: Write `pkg/presentation/cli/sync.go`**

```go
package cli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"

	"github.com/stillsource/kdrive-fuse/pkg/appconfig"
	"github.com/stillsource/kdrive-fuse/pkg/infrastructure/di"
	"github.com/stillsource/kdrive-fuse/pkg/infrastructure/manifest"
	"github.com/stillsource/kdrive-fuse/pkg/infrastructure/remoteindex"
	"github.com/stillsource/kdrive-fuse/pkg/syncer"
)

const defaultLocalRoot = "~/Pictures/FUJI/112_FUJI"
const defaultRemoteRoot = "Rémanence"

const syncUsage = `kdrive sync — mirror a local tree to its kDrive copy (push).

Usage:
  kdrive sync [flags] [LOCAL] [REMOTE]
      LOCAL   local source dir   (default: ` + defaultLocalRoot + `)
      REMOTE  remote path under the drive root (default: ` + defaultRemoteRoot + `)

Flags:
  --dry-run     classify and print the plan; change nothing
  --no-delete   never delete on the remote
  --force       override the deletion guard (>20% of tracked files)
  --assume-new  skip the first-run bootstrap; treat every local file as new
  --jobs N      concurrent transfers (default 8)
  -h, --help    show this help
`

// syncOptions is the parsed command line for `kdrive sync`.
type syncOptions struct {
	local, remote                     string
	dryRun, noDelete, force, assumeNew bool
	jobs                              int
}

// parseSyncFlags parses the sync subcommand arguments (everything after "sync").
// It returns flag.ErrHelp when help was requested.
func parseSyncFlags(args []string, stderr io.Writer) (syncOptions, error) {
	fs := flag.NewFlagSet("sync", flag.ContinueOnError)
	fs.SetOutput(stderr)
	o := syncOptions{}
	fs.BoolVar(&o.dryRun, "dry-run", false, "")
	fs.BoolVar(&o.noDelete, "no-delete", false, "")
	fs.BoolVar(&o.force, "force", false, "")
	fs.BoolVar(&o.assumeNew, "assume-new", false, "")
	fs.IntVar(&o.jobs, "jobs", 8, "")
	if err := fs.Parse(args); err != nil {
		return o, err
	}
	rest := fs.Args()
	if len(rest) > 2 {
		return o, fmt.Errorf("at most two positional arguments (LOCAL REMOTE), got %d", len(rest))
	}
	o.local = defaultLocalRoot
	o.remote = defaultRemoteRoot
	if len(rest) >= 1 {
		o.local = rest[0]
	}
	if len(rest) >= 2 {
		o.remote = rest[1]
	}
	return o, nil
}

// runSync is the `sync` subcommand entry point.
func runSync(args []string, stdout, stderr io.Writer) int {
	opts, err := parseSyncFlags(args, stderr)
	if err == flag.ErrHelp {
		_, _ = fmt.Fprint(stdout, syncUsage)
		return 0
	}
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "kdrive sync: %v\n", err)
		return 2
	}

	local, err := expandHome(opts.local)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "kdrive sync: %v\n", err)
		return 1
	}

	log := slog.New(slog.NewTextHandler(stderr, nil))
	app, err := appconfig.Load(context.Background())
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "kdrive sync: %v\n", err)
		return 1
	}
	files := di.NewContainer(app.DI(log)).Client().Files

	ctx := context.Background()
	rootID, err := remoteindex.NewResolver(files, files, app.RootFolderID).Resolve(ctx, opts.remote)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "kdrive sync: resolve remote %q: %v\n", opts.remote, err)
		return 1
	}
	mpath, err := manifest.PathFor(local, opts.remote)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "kdrive sync: %v\n", err)
		return 1
	}

	res, err := syncer.Push(ctx, syncer.PushOptions{
		LocalRoot: local,
		Jobs:      opts.jobs,
		Force:     opts.force,
		DryRun:    opts.dryRun,
		NoDelete:  opts.noDelete,
		AssumeNew: opts.assumeNew,
	}, files, rootID, mpath, stdout)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "kdrive sync: %v\n", err)
		return 1
	}
	if !opts.dryRun {
		_, _ = fmt.Fprintf(stdout, "synced: %d uploaded, %d overwritten, %d deleted, %d failed\n",
			res.Uploaded, res.Overwritten, res.Deleted, res.Failed)
	}
	if res.Failed > 0 {
		return 1
	}
	return 0
}

// expandHome expands a leading ~/ to the user's home directory.
func expandHome(p string) (string, error) {
	if p == "~" || len(p) >= 2 && p[:2] == "~/" {
		home, err := osUserHomeDir()
		if err != nil {
			return "", err
		}
		if p == "~" {
			return home, nil
		}
		return home + p[1:], nil
	}
	return p, nil
}
```

Also add, at the bottom of `sync.go`, an indirection so the home lookup is real (kept as a tiny seam):

```go
// osUserHomeDir is os.UserHomeDir, named for clarity at the call site.
func osUserHomeDir() (string, error) {
	return userHomeDir()
}
```

and create the real binding in the same file:

```go
import "os" // add to the import block

func userHomeDir() (string, error) { return os.UserHomeDir() }
```

(Combine the imports into one block; `os` joins the existing imports. The two tiny wrappers keep `expandHome` testable in isolation if needed and document intent.)

- [ ] **Step 4: Register `sync` in the dispatcher**

In `pkg/presentation/cli/root.go`, add a `case "sync":` to the `switch` in `Run`, before the `default`:

```go
	case "sync":
		return runSync(args[1:], stdout, stderr)
```

And update the `usage` constant's body line `Commands are added as the suite grows (next: sync).` to:

```
Commands:
  sync   mirror a local tree to its kDrive copy (push)
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test ./pkg/presentation/cli/ -v`
Expected: PASS (existing 4 + 3 new = 7 specs). The `sync --help` path returns 0 and prints the sync usage; unknown flag and too-many-args return 2.

- [ ] **Step 6: Build the binary and smoke-test help**

Run:
```bash
go build ./... && ./bin/kdrive sync --help 2>/dev/null || go run ./cmd/kdrive sync --help
```
Expected: prints the `kdrive sync` usage block (exit 0).

- [ ] **Step 7: Commit**

```bash
git add pkg/presentation/cli/sync.go pkg/presentation/cli/sync_test.go pkg/presentation/cli/root.go
git commit -m "feat(cli): kdrive sync push command"
```

---

## Verification (end of PR)

- [ ] `go build ./... && go vet ./...` — clean.
- [ ] `golangci-lint run ./...` — 0 issues.
- [ ] `go test -race ./pkg/syncer/... ./pkg/presentation/cli/...` — all pass under race.
- [ ] `go test -coverprofile=coverage.out -covermode=atomic -coverpkg=./pkg/... ./pkg/... ./cmd/...` then `go tool cover -func=coverage.out | awk '/^total:/{print $3}'` — all pass, total ≥ 90 %. (If below, add specs — likely candidates: more `parseSyncFlags` cases, a `Push` overwrite/delete path, the `expandHome` `~/` expansion.)
- [ ] `go run ./cmd/kdrive sync --help` prints the usage; `kdrive` with no args still lists `sync`.
- [ ] Open a PR (base `main`) titled `feat: add the kdrive sync push command`, referencing the design doc.

## Notes for later PRs (not this one)

- PR #6 adds `PlanPull` + a pull runner + `--pull`, reusing `WalkLocal`, the remote index, and the manifest. The `Executor` ctx-cancellation responsibility (noted in PR #4 review) is the executor's; the pull executor should honor `ctx` on downloads.
- PR #7 adds `--verify`, `--refresh`, and updates `README`/`ROADMAP`/`CLAUDE.md` (which still omit the new `appconfig`/`manifest`/`remoteindex`/`syncer`/`cli` packages).
