// Package kdrive is a Go client for the Infomaniak kDrive REST API v2.
//
// # Overview
//
// Client is safe for concurrent use and groups operations into services:
//
//	client := kdrive.New(token, driveID,
//	    kdrive.WithLogger(slog.Default()),
//	)
//	infos, err := client.Files.List(ctx, folderID)
//	link, err := client.Shares.Publish(ctx, fileID)
//
// # Errors
//
// All API calls return typed sentinel errors for common conditions:
//
//	if errors.Is(err, kdrive.ErrNotFound) { ... }
//	if errors.Is(err, kdrive.ErrAuth) { ... }
//
// Unknown HTTP failures are returned as *HTTPError with the status code and
// a truncated response snippet (tokens are never included).
//
// # Upload endpoint quirk
//
// The kDrive binary upload endpoint is hosted on a different domain
// (api.kdrive.infomaniak.com) than list/download/rename/etc.
// (api.infomaniak.com/2/drive). Both base URLs are configurable via options.
package kdrive
