package cli

import (
	"context"
	"fmt"
	"io"
	"strconv"

	"github.com/stillsource/kdrive-fuse/pkg/appconfig"
	"github.com/stillsource/kdrive-fuse/pkg/domain"
	"github.com/stillsource/kdrive-fuse/pkg/infrastructure/di"
)

const trashUsage = `kdrive trash — browse and manage the kDrive trash.

Usage:
  kdrive trash list
  kdrive trash restore <FILE_ID>
  kdrive trash purge   <FILE_ID> --yes
  kdrive trash empty   --yes

Subcommands:
  list               list trashed items (id, name, size)
  restore <FILE_ID>  restore a trashed item to its original location
  purge   <FILE_ID>  permanently delete one trashed item (requires --yes)
  empty              permanently empty the whole trash (requires --yes)

Flags:
  --yes   confirm irreversible destructive operations
  -h, --help  show this help
`

// trasher is the set of trash operations the CLI needs. Satisfied by
// *kdriveapi.FilesService.
type trasher interface {
	ListTrash(ctx context.Context) ([]domain.FileInfo, error)
	RestoreTrash(ctx context.Context, fileID int64) error
	PurgeTrash(ctx context.Context, fileID int64) error
	EmptyTrash(ctx context.Context) error
}

// trashBackend builds the trash dependencies. Package var so tests can inject a fake.
var trashBackend = func(ctx context.Context, stderr io.Writer) (trasher, error) {
	app, err := appconfig.Load(ctx)
	if err != nil {
		return nil, err
	}
	log := app.NewLogger(stderr)
	client := di.NewContainer(app.DI(log)).Client()
	return client.Files, nil
}

// runTrash is the "trash" subcommand entry point.
func runTrash(args []string, stdout, stderr io.Writer) int {
	// Handle help flags before any other argument processing.
	for _, a := range args {
		if a == "-h" || a == "--help" {
			_, _ = fmt.Fprint(stdout, trashUsage)
			return 0
		}
	}

	if len(args) == 0 {
		_, _ = fmt.Fprintf(stderr, "kdrive trash: expected a subcommand\n\n%s", trashUsage)
		return 2
	}

	sub := args[0]
	rest := args[1:]

	switch sub {
	case "list":
		return runTrashList(rest, stdout, stderr)
	case "restore":
		return runTrashRestore(rest, stdout, stderr)
	case "purge":
		return runTrashPurge(rest, stdout, stderr)
	case "empty":
		return runTrashEmpty(rest, stdout, stderr)
	default:
		_, _ = fmt.Fprintf(stderr, "kdrive trash: unknown subcommand %q\n\n%s", sub, trashUsage)
		return 2
	}
}

func runTrashList(args []string, stdout, stderr io.Writer) int {
	ctx := context.Background()
	t, err := trashBackend(ctx, stderr)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "kdrive trash: %v\n", err)
		return 1
	}

	items, err := t.ListTrash(ctx)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "kdrive trash: %v\n", err)
		return 1
	}

	for _, item := range items {
		_, _ = fmt.Fprintf(stdout, "\t%d\t%s\t(%d bytes)\n", item.ID, item.Name, item.Size)
	}
	_ = args // list takes no additional arguments
	return 0
}

func runTrashRestore(args []string, stdout, stderr io.Writer) int {
	_ = stdout

	// Strip --yes if present (not meaningful for restore, but tolerated).
	positional, _ := splitYes(args)

	if len(positional) != 1 {
		_, _ = fmt.Fprintf(stderr, "kdrive trash: restore requires exactly one FILE_ID argument\n\n%s", trashUsage)
		return 2
	}

	fileID, err := strconv.ParseInt(positional[0], 10, 64)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "kdrive trash: invalid FILE_ID %q: %v\n", positional[0], err)
		return 1
	}

	ctx := context.Background()
	t, err := trashBackend(ctx, stderr)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "kdrive trash: %v\n", err)
		return 1
	}

	if err := t.RestoreTrash(ctx, fileID); err != nil {
		_, _ = fmt.Fprintf(stderr, "kdrive trash: %v\n", err)
		return 1
	}
	return 0
}

func runTrashPurge(args []string, stdout, stderr io.Writer) int {
	_ = stdout

	positional, hasYes := splitYes(args)

	if len(positional) == 0 {
		_, _ = fmt.Fprintf(stderr, "kdrive trash: purge requires a FILE_ID argument\n\n%s", trashUsage)
		return 2
	}

	fileID, err := strconv.ParseInt(positional[0], 10, 64)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "kdrive trash: invalid FILE_ID %q: %v\n", positional[0], err)
		return 1
	}

	if !hasYes {
		_, _ = fmt.Fprintf(stderr,
			"kdrive trash: purge is irreversible. Pass --yes to confirm permanent deletion of item %d.\n", fileID)
		return 1
	}

	ctx := context.Background()
	t, err := trashBackend(ctx, stderr)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "kdrive trash: %v\n", err)
		return 1
	}

	if err := t.PurgeTrash(ctx, fileID); err != nil {
		_, _ = fmt.Fprintf(stderr, "kdrive trash: %v\n", err)
		return 1
	}
	return 0
}

func runTrashEmpty(args []string, stdout, stderr io.Writer) int {
	_ = stdout

	_, hasYes := splitYes(args)

	if !hasYes {
		_, _ = fmt.Fprintf(stderr,
			"kdrive trash: empty is irreversible. Pass --yes to confirm permanently emptying the entire trash.\n")
		return 1
	}

	ctx := context.Background()
	t, err := trashBackend(ctx, stderr)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "kdrive trash: %v\n", err)
		return 1
	}

	if err := t.EmptyTrash(ctx); err != nil {
		_, _ = fmt.Fprintf(stderr, "kdrive trash: %v\n", err)
		return 1
	}
	return 0
}

// splitYes separates --yes from positional arguments. Returns the positional
// args slice and whether --yes was present.
func splitYes(args []string) (positional []string, hasYes bool) {
	for _, a := range args {
		if a == "--yes" {
			hasYes = true
		} else {
			positional = append(positional, a)
		}
	}
	return positional, hasYes
}
