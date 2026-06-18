package cli

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/stillsource/kdrive-fuse/pkg/appconfig"
	"github.com/stillsource/kdrive-fuse/pkg/domain"
	"github.com/stillsource/kdrive-fuse/pkg/infrastructure/di"
	"github.com/stillsource/kdrive-fuse/pkg/infrastructure/remoteindex"
	"github.com/stillsource/kdrive-fuse/pkg/service"
	"github.com/stillsource/kdrive-fuse/pkg/usecase"
)

const shareUsage = `kdrive share — print the public share URL for a file.

Usage:
  kdrive share REMOTE_PATH

  REMOTE_PATH  path to the file under the drive root (e.g. "Photos/2024/cat.jpg")

Flags:
  -h, --help  show this help
`

// shareBackend builds the share dependencies. Package var so tests can inject a fake.
var shareBackend = func(ctx context.Context, stderr io.Writer) (service.Sharer, remoteindex.Lister, int64, error) {
	app, err := appconfig.Load(ctx)
	if err != nil {
		return nil, nil, 0, err
	}
	log := app.NewLogger(stderr)
	client := di.NewContainer(app.DI(log)).Client()
	return client.Shares, client.Files, app.RootFolderID, nil
}

// runShare is the "share" subcommand entry point.
func runShare(args []string, stdout, stderr io.Writer) int {
	// Handle help flags before any other argument processing.
	for _, a := range args {
		if a == "-h" || a == "--help" {
			_, _ = fmt.Fprint(stdout, shareUsage)
			return 0
		}
	}

	if len(args) != 1 {
		_, _ = fmt.Fprintf(stderr, "kdrive share: expected exactly one REMOTE_PATH argument\n\n%s", shareUsage)
		return 2
	}
	remotePath := args[0]

	ctx := context.Background()
	sharer, lister, rootID, err := shareBackend(ctx, stderr)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "kdrive share: %v\n", err)
		return 1
	}

	fileID, err := resolveFile(ctx, lister, rootID, remotePath)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "kdrive share: %v\n", err)
		return 1
	}

	info, err := usecase.NewShareFile(sharer).Execute(ctx, fileID)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "kdrive share: %v\n", err)
		return 1
	}

	_, _ = fmt.Fprintln(stdout, info.ShareURL)
	return 0
}

// resolveFile resolves a slash-separated path under rootID to a non-directory
// file id by listing each path segment. It never creates directories.
// Returns the file's id, or an error if the path is missing or resolves to a
// directory.
func resolveFile(ctx context.Context, l remoteindex.Lister, rootID int64, p string) (int64, error) {
	// Normalise: strip leading and trailing slashes, then split on "/".
	p = strings.Trim(p, "/")
	segments := strings.Split(p, "/")

	currentID := rootID
	for i, seg := range segments {
		children, err := l.List(ctx, currentID)
		if err != nil {
			return 0, err
		}

		found, ok := findChild(children, seg)
		if !ok {
			return 0, fmt.Errorf("not found: %s", strings.Join(segments[:i+1], "/"))
		}

		isLast := i == len(segments)-1
		if isLast {
			if found.IsDir() {
				return 0, fmt.Errorf("%s is a directory", p)
			}
			return found.ID, nil
		}

		// Intermediate segment must be a directory so we can traverse into it.
		if !found.IsDir() {
			return 0, fmt.Errorf("not found: %s", strings.Join(segments[:i+2], "/"))
		}
		currentID = found.ID
	}

	// len(segments) is always >= 1 after Split on a non-empty string, so the
	// loop always returns inside. This is a safety net for an empty path.
	return 0, fmt.Errorf("not found: %s", p)
}

// findChild locates the first child in infos whose Name equals name.
func findChild(infos []domain.FileInfo, name string) (domain.FileInfo, bool) {
	for _, info := range infos {
		if info.Name == name {
			return info, true
		}
	}
	return domain.FileInfo{}, false
}
