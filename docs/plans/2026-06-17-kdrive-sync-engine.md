# kdrive sync engine — Implementation Plan (PR #4)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add `pkg/syncer`: a pure push **planner** (`PlanPush` + `Bootstrap`) that classifies local-vs-manifest into a list of actions, and a concurrent **runner** (`RunPush`) that executes those actions through an `Executor` interface and updates the manifest. No CLI, no real API calls — the concrete executor and command come in PR #5.

**Architecture:** A new application-layer package `pkg/syncer` (named `syncer`, not `sync`, to avoid clashing with the standard library). It sits above `usecase`/`infrastructure` and may import `manifest` + `remoteindex`. `plan.go` is pure (no IO). `run.go` runs a fixed worker pool over the plan; workers call the injected `Executor` concurrently, and the single main goroutine applies manifest mutations as outcomes arrive (the manifest is not thread-safe, so all mutation is serialized there).

**Tech Stack:** Go 1.26 stdlib (`context`, `sync`), `pkg/infrastructure/manifest`, `pkg/infrastructure/remoteindex`. Ginkgo v2 + Gomega. Coverage gate ≥ 90 % on `./pkg/...`.

**Conventions (must follow):** English only. **No `Co-Authored-By` trailer.** Tests are Ginkgo specs in the **black-box** package `package syncer_test`. Concurrency tests must pass under `-race`. errcheck enabled for `pkg/`. Work on the existing branch `feat/kdrive-sync-engine`.

**Design note for the PR description:** the design doc sketched these under `pkg/usecase/`, but `usecase` must not depend on the `manifest`/`remoteindex` infrastructure packages (inward-dependency rule). The engine is an application-orchestration concern, so it lives in its own `pkg/syncer` package above both.

---

### Task 1: `plan.go` — pure push planner + bootstrap

**Files:**
- Create: `pkg/syncer/plan.go`
- Create: `pkg/syncer/syncer_suite_test.go`
- Create: `pkg/syncer/plan_test.go`

- [ ] **Step 1: Write the suite entry point**

Create `pkg/syncer/syncer_suite_test.go`:

```go
package syncer_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestSyncer(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Syncer Suite")
}
```

- [ ] **Step 2: Write the failing test**

Create `pkg/syncer/plan_test.go`:

```go
package syncer_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/stillsource/kdrive-fuse/pkg/infrastructure/manifest"
	"github.com/stillsource/kdrive-fuse/pkg/infrastructure/remoteindex"
	"github.com/stillsource/kdrive-fuse/pkg/syncer"
)

// byRel indexes a plan by relative path for order-independent assertions.
func byRel(items []syncer.Item) map[string]syncer.Item {
	m := map[string]syncer.Item{}
	for _, it := range items {
		m[it.Rel] = it
	}
	return m
}

var _ = Describe("PlanPush", func() {
	It("plans an upload for a file absent from the manifest", func() {
		m := manifest.New()
		items := syncer.PlanPush([]syncer.LocalFile{{Rel: "a.jpg", Size: 10, Mtime: 100}}, m)
		Expect(items).To(HaveLen(1))
		Expect(items[0]).To(Equal(syncer.Item{Rel: "a.jpg", Op: syncer.OpUpload, Size: 10, Mtime: 100}))
	})

	It("plans an overwrite when size or mtime differs", func() {
		m := manifest.New()
		m.Set("a.jpg", manifest.Entry{Size: 10, LocalMtime: 100, RemoteID: 7, RemoteMtime: 200})
		m.Set("b.jpg", manifest.Entry{Size: 5, LocalMtime: 1, RemoteID: 8, RemoteMtime: 2})
		items := syncer.PlanPush([]syncer.LocalFile{
			{Rel: "a.jpg", Size: 11, Mtime: 100}, // size changed
			{Rel: "b.jpg", Size: 5, Mtime: 9},    // mtime changed (same size)
		}, m)
		got := byRel(items)
		Expect(items).To(HaveLen(2))
		Expect(got["a.jpg"]).To(Equal(syncer.Item{Rel: "a.jpg", Op: syncer.OpOverwrite, RemoteID: 7, Size: 11, Mtime: 100}))
		Expect(got["b.jpg"]).To(Equal(syncer.Item{Rel: "b.jpg", Op: syncer.OpOverwrite, RemoteID: 8, Size: 5, Mtime: 9}))
	})

	It("omits unchanged files", func() {
		m := manifest.New()
		m.Set("a.jpg", manifest.Entry{Size: 10, LocalMtime: 100, RemoteID: 7, RemoteMtime: 200})
		items := syncer.PlanPush([]syncer.LocalFile{{Rel: "a.jpg", Size: 10, Mtime: 100}}, m)
		Expect(items).To(BeEmpty())
	})

	It("plans a delete for a manifest entry with no local file", func() {
		m := manifest.New()
		m.Set("gone.jpg", manifest.Entry{Size: 10, LocalMtime: 100, RemoteID: 7, RemoteMtime: 200})
		items := syncer.PlanPush(nil, m)
		Expect(items).To(HaveLen(1))
		Expect(items[0]).To(Equal(syncer.Item{Rel: "gone.jpg", Op: syncer.OpDelete, RemoteID: 7}))
	})
})

var _ = Describe("Bootstrap", func() {
	It("seeds same-size files as unchanged and different-size as overwrite, leaving local-only as upload", func() {
		m := manifest.New()
		local := []syncer.LocalFile{
			{Rel: "same.jpg", Size: 10, Mtime: 100},
			{Rel: "diff.jpg", Size: 20, Mtime: 101},
			{Rel: "localonly.jpg", Size: 30, Mtime: 102},
		}
		idx := map[string]remoteindex.Entry{
			"same.jpg": {ID: 7, Size: 10, Mtime: 200},
			"diff.jpg": {ID: 8, Size: 99, Mtime: 201}, // remote size differs
		}
		syncer.Bootstrap(m, idx, local)

		items := byRel(syncer.PlanPush(local, m))
		Expect(items).NotTo(HaveKey("same.jpg")) // unchanged -> skipped
		Expect(items["diff.jpg"]).To(Equal(syncer.Item{Rel: "diff.jpg", Op: syncer.OpOverwrite, RemoteID: 8, Size: 20, Mtime: 101}))
		Expect(items["localonly.jpg"]).To(Equal(syncer.Item{Rel: "localonly.jpg", Op: syncer.OpUpload, Size: 30, Mtime: 102}))
	})
})
```

- [ ] **Step 3: Run the test to verify it fails**

Run: `go test ./pkg/syncer/ -v`
Expected: compile failure — `undefined: syncer.PlanPush` etc.

- [ ] **Step 4: Write the implementation**

Create `pkg/syncer/plan.go`:

```go
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
```

- [ ] **Step 5: Run the test to verify it passes**

Run: `go test ./pkg/syncer/ -v`
Expected: PASS (5 specs).

- [ ] **Step 6: Commit**

```bash
git add pkg/syncer/plan.go pkg/syncer/syncer_suite_test.go pkg/syncer/plan_test.go
git commit -m "feat(syncer): pure push planner and manifest bootstrap"
```

---

### Task 2: `run.go` — concurrent runner over an Executor

**Files:**
- Create: `pkg/syncer/run.go`
- Create: `pkg/syncer/run_test.go`

- [ ] **Step 1: Write the failing test**

Create `pkg/syncer/run_test.go`:

```go
package syncer_test

import (
	"context"
	"errors"
	"sync"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/stillsource/kdrive-fuse/pkg/infrastructure/manifest"
	"github.com/stillsource/kdrive-fuse/pkg/syncer"
)

// fakeExec is a concurrency-safe Executor that records calls and can fail
// selected paths.
type fakeExec struct {
	mu      sync.Mutex
	uploads []string
	over    []string
	dels    []string
	fail    map[string]bool
	nextID  int64
}

func (f *fakeExec) Upload(_ context.Context, rel string, _ int64) (int64, int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail[rel] {
		return 0, 0, errors.New("upload " + rel)
	}
	f.nextID++
	f.uploads = append(f.uploads, rel)
	return 5000 + f.nextID, 9000, nil
}

func (f *fakeExec) Overwrite(_ context.Context, rel string, remoteID, _ int64) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail[rel] {
		return 0, errors.New("overwrite " + rel)
	}
	f.over = append(f.over, rel)
	return 9100, nil
}

func (f *fakeExec) Delete(_ context.Context, rel string, remoteID int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail[rel] {
		return errors.New("delete " + rel)
	}
	f.dels = append(f.dels, rel)
	return nil
}

var _ = Describe("RunPush", func() {
	It("executes each op and updates the manifest", func() {
		m := manifest.New()
		m.Set("gone.jpg", manifest.Entry{Size: 1, LocalMtime: 1, RemoteID: 70, RemoteMtime: 2})
		m.Set("edit.jpg", manifest.Entry{Size: 5, LocalMtime: 5, RemoteID: 80, RemoteMtime: 6})
		items := []syncer.Item{
			{Rel: "new.jpg", Op: syncer.OpUpload, Size: 10, Mtime: 100},
			{Rel: "edit.jpg", Op: syncer.OpOverwrite, RemoteID: 80, Size: 6, Mtime: 101},
			{Rel: "gone.jpg", Op: syncer.OpDelete, RemoteID: 70},
		}
		ex := &fakeExec{fail: map[string]bool{}}
		res := syncer.RunPush(context.Background(), items, ex, m, 4)

		Expect(res.Uploaded).To(Equal(1))
		Expect(res.Overwritten).To(Equal(1))
		Expect(res.Deleted).To(Equal(1))
		Expect(res.Failed).To(Equal(0))

		nw, ok := m.Get("new.jpg")
		Expect(ok).To(BeTrue())
		Expect(nw).To(Equal(manifest.Entry{Size: 10, LocalMtime: 100, RemoteID: 5001, RemoteMtime: 9000}))
		ed, _ := m.Get("edit.jpg")
		Expect(ed).To(Equal(manifest.Entry{Size: 6, LocalMtime: 101, RemoteID: 80, RemoteMtime: 9100}))
		_, present := m.Get("gone.jpg")
		Expect(present).To(BeFalse())
	})

	It("records failures and leaves their manifest entries untouched", func() {
		m := manifest.New()
		m.Set("edit.jpg", manifest.Entry{Size: 5, LocalMtime: 5, RemoteID: 80, RemoteMtime: 6})
		items := []syncer.Item{
			{Rel: "new.jpg", Op: syncer.OpUpload, Size: 10, Mtime: 100},
			{Rel: "edit.jpg", Op: syncer.OpOverwrite, RemoteID: 80, Size: 6, Mtime: 101},
		}
		ex := &fakeExec{fail: map[string]bool{"new.jpg": true, "edit.jpg": true}}
		res := syncer.RunPush(context.Background(), items, ex, m, 2)

		Expect(res.Failed).To(Equal(2))
		Expect(res.Errs).To(HaveLen(2))
		_, present := m.Get("new.jpg")
		Expect(present).To(BeFalse()) // failed upload not recorded
		ed, _ := m.Get("edit.jpg")
		Expect(ed.RemoteMtime).To(Equal(int64(6))) // unchanged baseline
	})

	It("handles an empty plan", func() {
		m := manifest.New()
		res := syncer.RunPush(context.Background(), nil, &fakeExec{fail: map[string]bool{}}, m, 4)
		Expect(res).To(Equal(syncer.Result{}))
	})

	It("processes a large plan concurrently", func() {
		m := manifest.New()
		var items []syncer.Item
		for i := 0; i < 200; i++ {
			items = append(items, syncer.Item{Rel: relName(i), Op: syncer.OpUpload, Size: 1, Mtime: 1})
		}
		ex := &fakeExec{fail: map[string]bool{}}
		res := syncer.RunPush(context.Background(), items, ex, m, 8)
		Expect(res.Uploaded).To(Equal(200))
		Expect(m.Len()).To(Equal(200))
	})
})

func relName(i int) string {
	return "f" + string(rune('0'+i%10)) + "-" + string(rune('a'+i/10%26)) + ".jpg"
}
```

Note: `relName` just needs to produce 200 distinct strings — verify distinctness holds for i in [0,200): the pair (i%10, i/10%26) is unique for i in [0,200) since i/10 ranges 0..19 < 26. Good.

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./pkg/syncer/ -v`
Expected: compile failure — `undefined: syncer.RunPush`, `undefined: syncer.Result`.

- [ ] **Step 3: Write the implementation**

Create `pkg/syncer/run.go`:

```go
package syncer

import (
	"context"
	"sync"

	"github.com/stillsource/kdrive-fuse/pkg/infrastructure/manifest"
)

// Executor performs the side-effecting half of a push: it carries out one
// planned action against the remote (resolving the parent folder, opening the
// local file, calling the upload/delete use cases — see PR #5). It is called
// concurrently and must be safe for concurrent use.
type Executor interface {
	// Upload creates a new remote file from the local file at rel and returns
	// its new remote id and remote mtime.
	Upload(ctx context.Context, rel string, size int64) (remoteID, remoteMtime int64, err error)
	// Overwrite replaces the content of remote file remoteID from the local file
	// at rel and returns the new remote mtime.
	Overwrite(ctx context.Context, rel string, remoteID, size int64) (remoteMtime int64, err error)
	// Delete removes the remote file remoteID.
	Delete(ctx context.Context, rel string, remoteID int64) error
}

// Result summarizes a push run.
type Result struct {
	Uploaded    int
	Overwritten int
	Deleted     int
	Failed      int
	Errs        []error
}

type outcome struct {
	item        Item
	remoteID    int64
	remoteMtime int64
	err         error
}

// RunPush executes items with up to jobs concurrent Executor calls, updating the
// manifest as each action succeeds. A failed action leaves its manifest entry
// untouched so a re-run retries it. Manifest mutation is serialized on the
// calling goroutine (the manifest is not safe for concurrent writes); the
// in-memory manifest is updated but not persisted (the caller saves it).
func RunPush(ctx context.Context, items []Item, ex Executor, m *manifest.Manifest, jobs int) Result {
	if jobs < 1 {
		jobs = 1
	}
	in := make(chan Item)
	out := make(chan outcome)

	var wg sync.WaitGroup
	for range jobs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for it := range in {
				o := outcome{item: it, remoteID: it.RemoteID}
				switch it.Op {
				case OpUpload:
					o.remoteID, o.remoteMtime, o.err = ex.Upload(ctx, it.Rel, it.Size)
				case OpOverwrite:
					o.remoteMtime, o.err = ex.Overwrite(ctx, it.Rel, it.RemoteID, it.Size)
				case OpDelete:
					o.err = ex.Delete(ctx, it.Rel, it.RemoteID)
				}
				out <- o
			}
		}()
	}

	go func() {
		for _, it := range items {
			in <- it
		}
		close(in)
	}()
	go func() {
		wg.Wait()
		close(out)
	}()

	var res Result
	for o := range out {
		if o.err != nil {
			res.Failed++
			res.Errs = append(res.Errs, o.err)
			continue
		}
		switch o.item.Op {
		case OpUpload, OpOverwrite:
			m.Set(o.item.Rel, manifest.Entry{
				Size:        o.item.Size,
				LocalMtime:  o.item.Mtime,
				RemoteID:    o.remoteID,
				RemoteMtime: o.remoteMtime,
			})
			if o.item.Op == OpUpload {
				res.Uploaded++
			} else {
				res.Overwritten++
			}
		case OpDelete:
			m.Delete(o.item.Rel)
			res.Deleted++
		}
	}
	return res
}
```

- [ ] **Step 4: Run the test to verify it passes (with race)**

Run: `go test -race ./pkg/syncer/ -v`
Expected: PASS (9 specs total) under the race detector.

- [ ] **Step 5: Commit**

```bash
git add pkg/syncer/run.go pkg/syncer/run_test.go
git commit -m "feat(syncer): concurrent push runner with serialized manifest updates"
```

---

## Verification (end of PR)

- [ ] `go build ./... && go vet ./...` — clean.
- [ ] `golangci-lint run ./...` — 0 issues.
- [ ] `go test -race ./pkg/syncer/...` — all pass under race.
- [ ] `go test -coverprofile=coverage.out -covermode=atomic -coverpkg=./pkg/... ./pkg/... ./cmd/...` then `go tool cover -func=coverage.out | awk '/^total:/{print $3}'` — all pass, total ≥ 90 %.
- [ ] Open a PR (base `main`) titled `feat: add the kdrive sync engine (planner + runner)`, referencing the design doc and noting the `pkg/syncer` placement.

## Notes for later PRs (not this one)

- PR #5 implements a concrete `Executor` (resolves the parent via `remoteindex.Resolver`, opens the local file, calls `CommitWrite`), walks the local tree into `[]LocalFile`, builds the remote index for bootstrap, applies the deletion guard, and wires the `kdrive sync` command (push) — saving the manifest after `RunPush`.
- PR #6 adds `PlanPull` + a pull runner (download via `DownloadStream`, delete-local), reusing this structure.
