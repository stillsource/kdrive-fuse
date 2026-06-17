package cli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

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

// syncOptions is the parsed command line for "kdrive sync".
type syncOptions struct {
	local, remote                      string
	dryRun, noDelete, force, assumeNew bool
	jobs                               int
}

// parseSyncFlags parses the arguments after "sync". It returns flag.ErrHelp when
// help was requested.
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
