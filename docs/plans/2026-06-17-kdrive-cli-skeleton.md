# kdrive CLI skeleton — Implementation Plan (PR #1)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a second binary, `kdrive`, with subcommand dispatch and `--help`/`--version`, and factor the shared `KDRIVE_*` configuration into one package both binaries use — no sync behaviour yet.

**Architecture:** A new `pkg/appconfig` loads the environment knobs common to every kdrive binary and maps them to a `di.Config`; the FUSE daemon keeps only its mount-specific config. A new `pkg/presentation/cli` is the command-line presentation layer (mirroring `pkg/presentation/fuse`); `cmd/kdrive` is a thin entry point that calls `cli.Run`. This is the foundation for the `kdrive sync` command in later PRs.

**Tech Stack:** Go 1.26, `github.com/sethvargo/go-envconfig` v1.3.0, Ginkgo v2 + Gomega. Module path `github.com/stillsource/kdrive-fuse`. Coverage gate ≥ 90 % on `./pkg/...`.

**Conventions (must follow):** All repo content — commit messages, comments, docs — in **English**. **No `Co-Authored-By` trailer** on commits. Tests are Ginkgo specs; each package has a `<pkg>_suite_test.go` entry point. Work on the existing branch `feat/kdrive-cli-sync`.

---

### Task 1: `appconfig` — shared env Config + Load

Loads the `KDRIVE_*` variables common to both binaries. Uses a `Lookuper` so tests are hermetic (no global env mutation).

**Files:**
- Create: `pkg/appconfig/appconfig.go`
- Create: `pkg/appconfig/appconfig_suite_test.go`
- Create: `pkg/appconfig/appconfig_test.go`

- [ ] **Step 1: Write the suite entry point**

Create `pkg/appconfig/appconfig_suite_test.go`:

```go
package appconfig

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestAppconfig(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Appconfig Suite")
}
```

- [ ] **Step 2: Write the failing test**

Create `pkg/appconfig/appconfig_test.go`:

```go
package appconfig

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/sethvargo/go-envconfig"
)

var _ = Describe("Load", func() {
	ctx := context.Background()

	It("applies defaults when only required vars are set", func() {
		c, err := load(ctx, envconfig.MapLookuper(map[string]string{
			"KDRIVE_API_TOKEN": "tok",
			"KDRIVE_DRIVE_ID":  "123",
		}))
		Expect(err).NotTo(HaveOccurred())
		Expect(c.APIToken).To(Equal("tok"))
		Expect(c.DriveID).To(Equal("123"))
		Expect(c.RootFolderID).To(Equal(int64(1)))
		Expect(c.BaseURL).To(Equal("https://api.infomaniak.com/2/drive"))
		Expect(c.UploadBaseURL).To(Equal("https://api.kdrive.infomaniak.com/2/drive"))
		Expect(c.CacheTTLSecs).To(Equal(30))
		Expect(c.DiskCacheMaxGB).To(Equal(2))
		Expect(c.DiskCacheDir).To(BeEmpty())
	})

	It("errors when a required var is missing", func() {
		_, err := load(ctx, envconfig.MapLookuper(map[string]string{
			"KDRIVE_API_TOKEN": "tok", // DRIVE_ID missing
		}))
		Expect(err).To(HaveOccurred())
	})

	It("honors overrides", func() {
		c, err := load(ctx, envconfig.MapLookuper(map[string]string{
			"KDRIVE_API_TOKEN":         "tok",
			"KDRIVE_DRIVE_ID":          "123",
			"KDRIVE_ROOT_FOLDER_ID":    "789",
			"KDRIVE_CACHE_TTL_SECONDS": "5",
		}))
		Expect(err).NotTo(HaveOccurred())
		Expect(c.RootFolderID).To(Equal(int64(789)))
		Expect(c.CacheTTLSecs).To(Equal(5))
	})
})
```

- [ ] **Step 3: Run the test to verify it fails**

Run: `go test ./pkg/appconfig/ -v`
Expected: compile failure — `undefined: load` and `undefined: Config`.

- [ ] **Step 4: Write the minimal implementation**

Create `pkg/appconfig/appconfig.go`:

```go
// Package appconfig loads the KDRIVE_* runtime configuration shared by the
// kdrive-fuse daemon and the kdrive CLI.
package appconfig

import (
	"context"
	"fmt"

	"github.com/sethvargo/go-envconfig"
)

// Config holds the environment knobs common to every kdrive binary.
type Config struct {
	APIToken       string `env:"KDRIVE_API_TOKEN,required"`
	DriveID        string `env:"KDRIVE_DRIVE_ID,required"`
	RootFolderID   int64  `env:"KDRIVE_ROOT_FOLDER_ID,default=1"`
	BaseURL        string `env:"KDRIVE_BASE_URL,default=https://api.infomaniak.com/2/drive"`
	UploadBaseURL  string `env:"KDRIVE_UPLOAD_BASE_URL,default=https://api.kdrive.infomaniak.com/2/drive"`
	CacheTTLSecs   int    `env:"KDRIVE_CACHE_TTL_SECONDS,default=30"`
	DiskCacheDir   string `env:"KDRIVE_DISK_CACHE_DIR,default="`
	DiskCacheMaxGB int    `env:"KDRIVE_DISK_CACHE_MAX_GB,default=2"`
}

// Load reads the shared KDRIVE_* environment into a Config.
func Load(ctx context.Context) (*Config, error) {
	return load(ctx, envconfig.OsLookuper())
}

// load is the testable core: it reads from an explicit Lookuper.
func load(ctx context.Context, l envconfig.Lookuper) (*Config, error) {
	var c Config
	if err := envconfig.ProcessWith(ctx, &envconfig.Config{Target: &c, Lookuper: l}); err != nil {
		return nil, fmt.Errorf("appconfig: %w", err)
	}
	return &c, nil
}
```

- [ ] **Step 5: Run the test to verify it passes**

Run: `go test ./pkg/appconfig/ -v`
Expected: PASS (3 specs).

- [ ] **Step 6: Commit**

```bash
git add pkg/appconfig/appconfig.go pkg/appconfig/appconfig_suite_test.go pkg/appconfig/appconfig_test.go
git commit -m "feat(appconfig): shared KDRIVE_* environment loader"
```

---

### Task 2: `appconfig` — CacheDir() and DI() helpers

Derive the disk-cache directory default and map the shared config to a `di.Config`, so both binaries build their container identically (DRY).

**Files:**
- Modify: `pkg/appconfig/appconfig.go`
- Modify: `pkg/appconfig/appconfig_test.go`

- [ ] **Step 1: Add the failing tests**

Append to `pkg/appconfig/appconfig_test.go` (add the new imports `"log/slog"` and `"time"` to the existing import block):

```go
var _ = Describe("Config helpers", func() {
	It("CacheDir returns the explicit dir when set", func() {
		c := &Config{DiskCacheDir: "/tmp/x"}
		Expect(c.CacheDir()).To(Equal("/tmp/x"))
	})

	It("CacheDir defaults under the home cache dir when unset", func() {
		c := &Config{}
		Expect(c.CacheDir()).To(HaveSuffix("/.cache/kdrive-fuse"))
	})

	It("DI maps to a di.Config with derived units", func() {
		c := &Config{
			APIToken: "tok", DriveID: "123", RootFolderID: 7,
			BaseURL: "b", UploadBaseURL: "u", CacheTTLSecs: 10,
			DiskCacheDir: "/c", DiskCacheMaxGB: 3,
		}
		log := slog.Default()
		d := c.DI(log)
		Expect(d.Token).To(Equal("tok"))
		Expect(d.DriveID).To(Equal("123"))
		Expect(d.RootFolderID).To(Equal(int64(7)))
		Expect(d.BaseURL).To(Equal("b"))
		Expect(d.UploadBaseURL).To(Equal("u"))
		Expect(d.CacheTTL).To(Equal(10 * time.Second))
		Expect(d.DiskCacheDir).To(Equal("/c"))
		Expect(d.DiskCacheBytes).To(Equal(int64(3) * 1024 * 1024 * 1024))
		Expect(d.Logger).To(Equal(log))
	})
})
```

The import block of `appconfig_test.go` becomes:

```go
import (
	"context"
	"log/slog"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/sethvargo/go-envconfig"
)
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./pkg/appconfig/ -v`
Expected: compile failure — `c.CacheDir undefined` and `c.DI undefined`.

- [ ] **Step 3: Add the helpers**

Add to `pkg/appconfig/appconfig.go` — extend the import block and append the two methods:

```go
import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/sethvargo/go-envconfig"

	"github.com/stillsource/kdrive-fuse/pkg/infrastructure/di"
)
```

```go
// CacheDir returns the configured disk-cache directory, or the default
// ~/.cache/kdrive-fuse when unset.
func (c *Config) CacheDir() string {
	if c.DiskCacheDir != "" {
		return c.DiskCacheDir
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".cache", "kdrive-fuse")
}

// DI builds the di.Config used to construct the application container,
// attaching the given logger.
func (c *Config) DI(logger *slog.Logger) di.Config {
	return di.Config{
		Token:          c.APIToken,
		DriveID:        c.DriveID,
		RootFolderID:   c.RootFolderID,
		BaseURL:        c.BaseURL,
		UploadBaseURL:  c.UploadBaseURL,
		CacheTTL:       time.Duration(c.CacheTTLSecs) * time.Second,
		DiskCacheDir:   c.CacheDir(),
		DiskCacheBytes: int64(c.DiskCacheMaxGB) * 1024 * 1024 * 1024,
		Logger:         logger,
	}
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./pkg/appconfig/ -v`
Expected: PASS (6 specs).

- [ ] **Step 5: Commit**

```bash
git add pkg/appconfig/appconfig.go pkg/appconfig/appconfig_test.go
git commit -m "feat(appconfig): CacheDir default and di.Config mapping"
```

---

### Task 3: Point kdrive-fuse at the shared config

Remove the duplicated config from the daemon: it now uses `appconfig` for the shared knobs and keeps only its mount-specific loader.

**Files:**
- Modify: `cmd/kdrive-fuse/config/env.go`
- Modify: `cmd/kdrive-fuse/main.go`

- [ ] **Step 1: Slim the daemon's config to mount-only**

Replace the entire contents of `cmd/kdrive-fuse/config/env.go` with:

```go
// Package config loads the kdrive-fuse daemon's FUSE-specific configuration.
package config

import (
	"context"
	"fmt"
	"os"

	"github.com/sethvargo/go-envconfig"
)

// FUSE holds the mount-only configuration specific to the daemon.
type FUSE struct {
	Mount string `env:"KDRIVE_MOUNT,required"`
}

// LoadFUSE reads KDRIVE_MOUNT and ensures the mount directory exists.
func LoadFUSE(ctx context.Context) (*FUSE, error) {
	var c FUSE
	if err := envconfig.Process(ctx, &c); err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}
	if _, err := os.Stat(c.Mount); os.IsNotExist(err) {
		if err := os.MkdirAll(c.Mount, 0o755); err != nil {
			return nil, fmt.Errorf("create mount dir %s: %w", c.Mount, err)
		}
	}
	return &c, nil
}
```

- [ ] **Step 2: Update the daemon main to use both loaders**

In `cmd/kdrive-fuse/main.go`, update the import block (add `appconfig`, drop the now-unused `path/filepath`):

```go
import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/hanwen/go-fuse/v2/fs"
	"github.com/hanwen/go-fuse/v2/fuse"

	"github.com/stillsource/kdrive-fuse/cmd/kdrive-fuse/config"
	"github.com/stillsource/kdrive-fuse/pkg/appconfig"
	"github.com/stillsource/kdrive-fuse/pkg/infrastructure/di"
)
```

Replace the config-load + container-build block (the lines from `cfg, err := config.Load(...)` through `root, err := c.RootNode()` and its error check) with:

```go
	app, err := appconfig.Load(context.Background())
	if err != nil {
		log.Error("config", "err", err)
		os.Exit(1)
	}
	mnt, err := config.LoadFUSE(context.Background())
	if err != nil {
		log.Error("config", "err", err)
		os.Exit(1)
	}

	c := di.NewContainer(app.DI(log))
	root, err := c.RootNode()
	if err != nil {
		log.Error("disk cache", "err", err)
		os.Exit(1)
	}
```

Then update the two later references that used the old `cfg`:

- The mount call: `fs.Mount(cfg.Mount, root, ...)` becomes `fs.Mount(mnt.Mount, root, ...)`.
- The final log line becomes:

```go
	log.Info("kDrive mounted", "version", version, "path", mnt.Mount, "cache", app.CacheDir(), "cache_max_gb", app.DiskCacheMaxGB)
```

(The old `cacheDir` local and its `filepath.Join` fallback are removed — `app.CacheDir()` replaces them.)

- [ ] **Step 3: Verify the daemon builds, vets, and existing tests pass**

Run: `go build ./... && go vet ./... && go test ./pkg/... ./cmd/...`
Expected: build OK, vet clean, all existing suites PASS.

- [ ] **Step 4: Commit**

```bash
git add cmd/kdrive-fuse/config/env.go cmd/kdrive-fuse/main.go
git commit -m "refactor(kdrive-fuse): use the shared appconfig loader"
```

---

### Task 4: CLI dispatcher `pkg/presentation/cli`

The command-line presentation layer: a `Run` that handles `--help`, `--version`, no-args, and unknown commands. Subcommands are added in later PRs.

**Files:**
- Create: `pkg/presentation/cli/root.go`
- Create: `pkg/presentation/cli/cli_suite_test.go`
- Create: `pkg/presentation/cli/root_test.go`

- [ ] **Step 1: Write the suite entry point**

Create `pkg/presentation/cli/cli_suite_test.go`:

```go
package cli_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestCLI(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "CLI Suite")
}
```

- [ ] **Step 2: Write the failing test**

Create `pkg/presentation/cli/root_test.go`:

```go
package cli_test

import (
	"bytes"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/stillsource/kdrive-fuse/pkg/presentation/cli"
)

var _ = Describe("Run", func() {
	var out, errb *bytes.Buffer

	BeforeEach(func() {
		out = &bytes.Buffer{}
		errb = &bytes.Buffer{}
	})

	It("prints usage and exits 0 with no args", func() {
		Expect(cli.Run(nil, "dev", out, errb)).To(Equal(0))
		Expect(out.String()).To(ContainSubstring("Usage:"))
		Expect(errb.String()).To(BeEmpty())
	})

	It("prints usage on --help", func() {
		Expect(cli.Run([]string{"--help"}, "dev", out, errb)).To(Equal(0))
		Expect(out.String()).To(ContainSubstring("kdrive"))
	})

	It("prints the version on --version", func() {
		Expect(cli.Run([]string{"--version"}, "1.2.3", out, errb)).To(Equal(0))
		Expect(out.String()).To(ContainSubstring("kdrive 1.2.3"))
	})

	It("rejects an unknown command with exit 2", func() {
		Expect(cli.Run([]string{"bogus"}, "dev", out, errb)).To(Equal(2))
		Expect(errb.String()).To(ContainSubstring("unknown command"))
	})
})
```

- [ ] **Step 3: Run the test to verify it fails**

Run: `go test ./pkg/presentation/cli/ -v`
Expected: compile failure — `undefined: cli.Run`.

- [ ] **Step 4: Write the minimal implementation**

Create `pkg/presentation/cli/root.go`:

```go
// Package cli is the command-line presentation layer for the kdrive binary.
// It dispatches subcommands over the application's use cases, mirroring the
// FUSE presentation layer. Subcommands are added as the suite grows.
package cli

import (
	"fmt"
	"io"
)

const usage = `kdrive — command-line companion to the kdrive-fuse mount.

Usage:
  kdrive <command> [arguments]
  kdrive --help | --version

Commands are added as the suite grows (next: sync).

Run "kdrive <command> --help" for command-specific help.
`

// Run dispatches args (typically os.Args[1:]) and returns a process exit code.
func Run(args []string, version string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stdout, usage)
		return 0
	}
	switch args[0] {
	case "-h", "--help", "help":
		fmt.Fprint(stdout, usage)
		return 0
	case "-version", "--version", "version":
		fmt.Fprintln(stdout, "kdrive", version)
		return 0
	default:
		fmt.Fprintf(stderr, "kdrive: unknown command %q\n", args[0])
		fmt.Fprint(stderr, usage)
		return 2
	}
}
```

- [ ] **Step 5: Run the test to verify it passes**

Run: `go test ./pkg/presentation/cli/ -v`
Expected: PASS (4 specs).

- [ ] **Step 6: Commit**

```bash
git add pkg/presentation/cli/root.go pkg/presentation/cli/cli_suite_test.go pkg/presentation/cli/root_test.go
git commit -m "feat(cli): kdrive subcommand dispatcher with help and version"
```

---

### Task 5: `cmd/kdrive` entry point + Makefile

Wire the binary and make `make build`/`make install` produce both binaries.

**Files:**
- Create: `cmd/kdrive/main.go`
- Modify: `Makefile`

- [ ] **Step 1: Create the thin entry point**

Create `cmd/kdrive/main.go`:

```go
// Command kdrive is the command-line companion to the kdrive-fuse mount.
package main

import (
	"os"

	"github.com/stillsource/kdrive-fuse/pkg/presentation/cli"
)

// version is the build version, overridden at release time via
// -ldflags "-X main.version=...".
var version = "dev"

func main() {
	os.Exit(cli.Run(os.Args[1:], version, os.Stdout, os.Stderr))
}
```

- [ ] **Step 2: Update the Makefile build/install targets**

In `Makefile`, replace the `build` and `install` targets with:

```make
build:
	mkdir -p bin
	go build -o bin/kdrive-fuse ./cmd/kdrive-fuse
	go build -o bin/kdrive ./cmd/kdrive

install: build
	install -m 0755 bin/kdrive-fuse $${HOME}/bin/kdrive-fuse
	install -m 0755 bin/kdrive $${HOME}/bin/kdrive
```

And update the two matching `help` lines to:

```make
	@echo "  build          - build the kdrive-fuse and kdrive binaries into ./bin"
	@echo "  install        - build and install both binaries to ~/bin"
```

- [ ] **Step 3: Build and smoke-test the binary**

Run:
```bash
go build ./... && go vet ./...
make build
./bin/kdrive --version
./bin/kdrive --help
./bin/kdrive bogus; echo "exit=$?"
```
Expected: builds clean; `--version` prints `kdrive dev`; `--help` prints the usage block; `bogus` prints `kdrive: unknown command "bogus"` to stderr and `exit=2`.

- [ ] **Step 4: Run the full test suite + coverage gate**

Run:
```bash
go test -coverprofile=coverage.out -covermode=atomic -coverpkg=./pkg/... ./pkg/... ./cmd/...
go tool cover -func=coverage.out | awk '/^total:/{print "coverage: "$3}'
```
Expected: all suites PASS; coverage ≥ 90 %.

- [ ] **Step 5: Commit**

```bash
git add cmd/kdrive/main.go Makefile
git commit -m "feat(kdrive): add the kdrive CLI binary and build targets"
```

---

## Verification (end of PR)

- [ ] `go build ./... && go vet ./...` — clean.
- [ ] `golangci-lint run ./...` — clean.
- [ ] `make test-coverage` — all suites pass, total ≥ 90 %.
- [ ] `./bin/kdrive --version`, `--help`, and an unknown command behave as specified.
- [ ] `./bin/kdrive-fuse --version` still prints `kdrive-fuse dev` (daemon untouched in behaviour).
- [ ] Open a PR (base `main`) titled `feat: add the kdrive CLI binary skeleton + shared config`, referencing the design doc.

## Notes for later PRs (not this one)

- The `cli` package will grow a command registry when `sync` lands (PR #4/#5).
- `.goreleaser.yaml` will need a build entry for `cmd/kdrive` when the CLI is first released — intentionally left out here (skeleton only, nothing to ship yet).
