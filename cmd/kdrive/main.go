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
