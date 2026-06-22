// Command kdrive-fuse mounts an Infomaniak kDrive remote as a FUSE filesystem.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/hanwen/go-fuse/v2/fs"
	"github.com/hanwen/go-fuse/v2/fuse"

	"github.com/stillsource/kdrive-fuse/cmd/kdrive-fuse/config"
	"github.com/stillsource/kdrive-fuse/pkg/appconfig"
	"github.com/stillsource/kdrive-fuse/pkg/infrastructure/di"
	"github.com/stillsource/kdrive-fuse/pkg/infrastructure/metrics"
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

	log = app.NewLogger(os.Stderr)
	slog.SetDefault(log)

	dicfg := app.DI(log)
	if app.MetricsAddr != "" {
		reg := metrics.New()
		dicfg.Metrics = reg
		mux := http.NewServeMux()
		mux.Handle("/metrics", reg.Handler())
		srv := &http.Server{Addr: app.MetricsAddr, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
		go func() {
			log.Info("metrics listening", "addr", app.MetricsAddr)
			if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				log.Error("metrics server", "err", err)
			}
		}()
	}
	c := di.NewContainer(dicfg)
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
			// Bigger kernel<->daemon requests = far fewer round-trips on bulk
			// transfers. The default MaxWrite is 128 KiB; 1 MiB is go-fuse's
			// MAX_KERNEL_WRITE ceiling (Linux 4.20+ via CAP_MAX_PAGES). go-fuse
			// v2.9 negotiates no writeback cache, so writes stay synchronous —
			// larger writes are the available throughput lever (a `cp` into the
			// mount was capped well below a direct CLI upload because each write
			// was a separate 128 KiB round-trip). MaxReadAhead does the same for
			// buffered downloads.
			MaxWrite:     1 << 20,
			MaxReadAhead: 1 << 20,
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
