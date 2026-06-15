// Command kdrive-fuse mounts an Infomaniak kDrive remote as a FUSE filesystem.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/hanwen/go-fuse/v2/fs"
	"github.com/hanwen/go-fuse/v2/fuse"

	"github.com/stillsource/kdrive-fuse/cmd/kdrive-fuse/config"
	"github.com/stillsource/kdrive-fuse/pkg/infrastructure/contentcache"
	"github.com/stillsource/kdrive-fuse/pkg/infrastructure/kdriveapi"
	kdrivefuse "github.com/stillsource/kdrive-fuse/pkg/presentation/fuse"
)

// version is the build version, overridden at release time via
// -ldflags "-X main.version=...". See .goreleaser.yaml.
var version = "dev"

// wantsVersion reports whether the args request a version print.
func wantsVersion(args []string) bool {
	for _, a := range args {
		if a == "--version" || a == "-version" {
			return true
		}
	}
	return false
}

func main() {
	if wantsVersion(os.Args[1:]) {
		fmt.Println("kdrive-fuse", version)
		return
	}

	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	slog.SetDefault(log)

	cfg, err := config.Load(context.Background())
	if err != nil {
		log.Error("config", "err", err)
		os.Exit(1)
	}

	client := kdriveapi.New(cfg.APIToken, cfg.DriveID,
		kdriveapi.WithBaseURL(cfg.BaseURL),
		kdriveapi.WithUploadBaseURL(cfg.UploadBaseURL),
		kdriveapi.WithLogger(log),
	)

	cacheDir := cfg.DiskCacheDir
	if cacheDir == "" {
		home, _ := os.UserHomeDir()
		cacheDir = filepath.Join(home, ".cache", "kdrive-fuse")
	}
	maxBytes := int64(cfg.DiskCacheMaxGB) * 1024 * 1024 * 1024
	disk, err := contentcache.NewDiskCache(cacheDir, maxBytes, client.Files)
	if err != nil {
		log.Error("disk cache", "err", err)
		os.Exit(1)
	}

	cacheTTL := time.Duration(cfg.CacheTTLSecs) * time.Second
	kdfs := kdrivefuse.NewKDriveFS(client.Files, cacheTTL, disk)
	root := kdrivefuse.NewRootDirNode(kdfs, cfg.RootFolderID)

	attrTTL := 30 * time.Second
	server, err := fs.Mount(cfg.Mount, root, &fs.Options{
		MountOptions: fuse.MountOptions{
			Name:   "kdrive",
			FsName: "kdrive",
		},
		AttrTimeout:  &attrTTL,
		EntryTimeout: &attrTTL,
	})
	if err != nil {
		log.Error("mount failed", "path", cfg.Mount, "err", err)
		os.Exit(1)
	}

	log.Info("kDrive mounted", "version", version, "path", cfg.Mount, "cache", cacheDir, "cache_max_gb", cfg.DiskCacheMaxGB)

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sig
		log.Info("unmounting...")
		_ = server.Unmount()
	}()

	server.Wait()
	log.Info("unmounted")
}
