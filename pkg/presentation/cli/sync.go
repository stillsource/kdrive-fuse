package cli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/stillsource/kdrive-fuse/pkg/appconfig"
	"github.com/stillsource/kdrive-fuse/pkg/infrastructure/di"
	"github.com/stillsource/kdrive-fuse/pkg/infrastructure/manifest"
	"github.com/stillsource/kdrive-fuse/pkg/infrastructure/remoteindex"
	"github.com/stillsource/kdrive-fuse/pkg/syncer"
)

const defaultLocalRoot = "~/Pictures/FUJI/112_FUJI"
const defaultRemoteRoot = "Rémanence"

const syncUsage = `kdrive sync — mirror a local tree and its kDrive copy.

Usage:
  kdrive sync [flags] [LOCAL] [REMOTE]
      LOCAL   local dir          (default: ` + defaultLocalRoot + `)
      REMOTE  remote path under the drive root (default: ` + defaultRemoteRoot + `)

By default sync pushes (local -> remote). With --pull it mirrors the other way
(remote -> local).

Flags:
  --pull        mirror remote -> local instead of local -> remote
  --dry-run     classify and print the plan; change nothing
  --no-delete   never delete on the destination
  --force       override the deletion guard, the push remote-drift guard, and (on pull) the local-drift guard
  --delete-threshold F  refuse to delete more than fraction F of the baseline (default 0.20)
  --assume-new  (push only) skip the first-run bootstrap; treat every local file as new
  --refresh     (push only) re-bootstrap the manifest from a fresh remote index
  --detect-moves  treat a delete+add of a same-size, same-mtime file as a server-side move instead of a re-upload (heuristic; off by default)
  --verify      after the run, report local vs remote presence + size differences
  --jobs N      concurrent transfers (default 8)
  -h, --help    show this help
`

// syncOptions is the parsed command line for "kdrive sync".
type syncOptions struct {
	local, remote                                                          string
	pull, dryRun, noDelete, force, assumeNew, verify, refresh, detectMoves bool
	jobs                                                                   int
	deleteThreshold                                                        float64
}

// parseSyncFlags parses the arguments after "sync". It returns flag.ErrHelp when
// help was requested.
func parseSyncFlags(args []string, stderr io.Writer) (syncOptions, error) {
	fs := flag.NewFlagSet("sync", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {} // runSync prints the full usage; suppress flag's terse dump
	o := syncOptions{}
	fs.BoolVar(&o.pull, "pull", false, "")
	fs.BoolVar(&o.dryRun, "dry-run", false, "")
	fs.BoolVar(&o.noDelete, "no-delete", false, "")
	fs.BoolVar(&o.force, "force", false, "")
	fs.BoolVar(&o.assumeNew, "assume-new", false, "")
	fs.BoolVar(&o.verify, "verify", false, "")
	fs.BoolVar(&o.refresh, "refresh", false, "")
	fs.BoolVar(&o.detectMoves, "detect-moves", false, "")
	fs.IntVar(&o.jobs, "jobs", 8, "")
	fs.Float64Var(&o.deleteThreshold, "delete-threshold", 0.20, "")
	if err := fs.Parse(args); err != nil {
		return o, err
	}
	if o.deleteThreshold <= 0 || o.deleteThreshold > 1 {
		return o, fmt.Errorf("delete-threshold must be in (0, 1], got %g", o.deleteThreshold)
	}
	rest := fs.Args()
	if len(rest) > 2 {
		return o, fmt.Errorf("at most two positional arguments (LOCAL REMOTE), got %d", len(rest))
	}
	o.local, o.remote = defaultLocalRoot, defaultRemoteRoot
	if len(rest) >= 1 {
		o.local = rest[0]
	}
	if len(rest) >= 2 {
		o.remote = rest[1]
	}
	return o, nil
}

// runSync is the "sync" subcommand entry point.
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

	// Cancel the sync on SIGINT/SIGTERM (Ctrl-C): in-flight transfers honor the
	// context and abort, and the runners stop processing the remaining plan.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	files, rootID, mpath, err := syncBackend(ctx, local, opts.remote, stderr)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "kdrive sync: %v\n", err)
		return 1
	}

	if opts.pull {
		res, err := syncer.Pull(ctx, syncer.PullOptions{
			LocalRoot:       local,
			Jobs:            opts.jobs,
			Force:           opts.force,
			DryRun:          opts.dryRun,
			NoDelete:        opts.noDelete,
			DeleteThreshold: opts.deleteThreshold,
		}, files, rootID, mpath, stdout)
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "kdrive sync: %v\n", err)
			return 1
		}
		if !opts.dryRun {
			_, _ = fmt.Fprintf(stdout, "pulled: %d downloaded, %d deleted, %d failed\n",
				res.Downloaded, res.Deleted, res.Failed)
			if opts.verify {
				runVerify(ctx, local, files, rootID, stdout, stderr)
			}
		}
		if res.Failed > 0 {
			return 1
		}
		return 0
	}

	res, err := syncer.Push(ctx, syncer.PushOptions{
		LocalRoot:       local,
		Jobs:            opts.jobs,
		Force:           opts.force,
		DryRun:          opts.dryRun,
		NoDelete:        opts.noDelete,
		AssumeNew:       opts.assumeNew,
		Refresh:         opts.refresh,
		DetectMoves:     opts.detectMoves,
		DeleteThreshold: opts.deleteThreshold,
	}, files, rootID, mpath, stdout)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "kdrive sync: %v\n", err)
		return 1
	}
	if !opts.dryRun {
		_, _ = fmt.Fprintf(stdout, "synced: %d uploaded, %d overwritten, %d moved, %d deleted, %d failed\n",
			res.Uploaded, res.Overwritten, res.Moved, res.Deleted, res.Failed)
		if opts.verify {
			runVerify(ctx, local, files, rootID, stdout, stderr)
		}
	}
	if res.Failed > 0 {
		return 1
	}
	return 0
}

// runVerify reports presence + size differences between the local tree and the
// remote after a sync. Verification failure is non-fatal — it is only logged.
func runVerify(ctx context.Context, local string, files syncer.Remote, rootID int64, stdout, stderr io.Writer) {
	vr, err := syncer.Verify(ctx, local, files, rootID, stdout)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "kdrive sync: verify: %v\n", err)
		return
	}
	_, _ = fmt.Fprintf(stdout, "verify: ok=%d missing-remote=%d missing-local=%d sizediff=%d\n",
		vr.OK, vr.MissingRemote, vr.MissingLocal, vr.SizeDiff)
}

// syncBackend builds the remote files port, resolves the remote root path to a
// folder id, and computes the manifest path for a sync run. It is a package var
// so tests can substitute an in-memory backend.
var syncBackend = func(ctx context.Context, local, remote string, stderr io.Writer) (syncer.Remote, int64, string, error) {
	log := slog.New(slog.NewTextHandler(stderr, nil))
	app, err := appconfig.Load(ctx)
	if err != nil {
		return nil, 0, "", err
	}
	files := di.NewContainer(app.DI(log)).Client().Files
	rootID, err := remoteindex.NewResolver(files, files, app.RootFolderID).Resolve(ctx, remote)
	if err != nil {
		return nil, 0, "", fmt.Errorf("resolve remote %q: %w", remote, err)
	}
	mpath, err := manifest.PathFor(local, remote)
	if err != nil {
		return nil, 0, "", err
	}
	return files, rootID, mpath, nil
}

// expandHome expands a leading ~ or ~/ to the user's home directory.
func expandHome(p string) (string, error) {
	if p != "~" && !strings.HasPrefix(p, "~/") {
		return p, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	if p == "~" {
		return home, nil
	}
	return filepath.Join(home, p[2:]), nil
}
