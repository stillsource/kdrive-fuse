package cli

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/stillsource/kdrive-fuse/pkg/appconfig"
	"github.com/stillsource/kdrive-fuse/pkg/infrastructure/di"
	"github.com/stillsource/kdrive-fuse/pkg/service"
	"github.com/stillsource/kdrive-fuse/pkg/usecase"
)

const searchUsage = `kdrive search — full-text search across the drive.

Usage:
  kdrive search QUERY...

  QUERY  one or more words to search for (joined with spaces)

Flags:
  -h, --help  show this help
`

// searchBackend builds the search dependencies. Package var so tests can inject a fake.
var searchBackend = func(ctx context.Context, stderr io.Writer) (service.Searcher, error) {
	app, err := appconfig.Load(ctx)
	if err != nil {
		return nil, err
	}
	log := app.NewLogger(stderr)
	client := di.NewContainer(app.DI(log)).Client()
	return client.Files, nil
}

// runSearch is the "search" subcommand entry point.
func runSearch(args []string, stdout, stderr io.Writer) int {
	// Handle help flags before any other argument processing.
	for _, a := range args {
		if a == "-h" || a == "--help" {
			_, _ = fmt.Fprint(stdout, searchUsage)
			return 0
		}
	}

	if len(args) == 0 {
		_, _ = fmt.Fprintf(stderr, "kdrive search: expected at least one QUERY argument\n\n%s", searchUsage)
		return 2
	}

	query := strings.Join(args, " ")

	ctx := context.Background()
	searcher, err := searchBackend(ctx, stderr)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "kdrive search: %v\n", err)
		return 1
	}

	files, err := usecase.NewSearchFiles(searcher).Execute(ctx, query)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "kdrive search: %v\n", err)
		return 1
	}

	if len(files) == 0 {
		_, _ = fmt.Fprintln(stdout, "no matches")
		return 0
	}

	for _, f := range files {
		_, _ = fmt.Fprintf(stdout, "\t%d\t%s\t(%d bytes)\n", f.ID, f.Name, f.Size)
	}
	return 0
}
