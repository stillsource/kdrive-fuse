// Command kdrive-fuse mounts an Infomaniak kDrive remote as a FUSE filesystem.
package main

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

	attrTTL := 30 * time.Second
	server, err := fs.Mount(mnt.Mount, root, &fs.Options{
		MountOptions: fuse.MountOptions{
			Name:   "kdrive",
			FsName: "kdrive",
		},
		AttrTimeout:  &attrTTL,
		EntryTimeout: &attrTTL,
	})
	if err != nil {
		log.Error("mount failed", "path", mnt.Mount, "err", err)
		os.Exit(1)
	}

	log.Info("kDrive mounted", "version", version, "path", mnt.Mount, "cache", app.CacheDir(), "cache_max_gb", app.DiskCacheMaxGB)

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
