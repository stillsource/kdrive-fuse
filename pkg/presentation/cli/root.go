// Package cli is the command-line presentation layer for the kdrive binary.
// It dispatches subcommands over the application's use cases, mirroring the
// FUSE presentation layer. Subcommands are added as the suite grows.
package cli

import (
	"fmt"
	"io"
)

const usage = `kdrive — command-line companion to the kdrive-fuse mount.

Usage:
  kdrive <command> [arguments]
  kdrive --help | --version

Commands:
  sync   mirror a local tree and its kDrive copy (push/pull)
  share  print the public share URL for a file (kdrive share REMOTE_PATH)

Run "kdrive <command> --help" for command-specific help.
`

// Run dispatches args (typically os.Args[1:]) and returns a process exit code.
func Run(args []string, version string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		_, _ = fmt.Fprint(stdout, usage)
		return 0
	}
	switch args[0] {
	case "-h", "--help", "help":
		_, _ = fmt.Fprint(stdout, usage)
		return 0
	case "-version", "--version", "version":
		_, _ = fmt.Fprintln(stdout, "kdrive", version)
		return 0
	case "sync":
		return runSync(args[1:], stdout, stderr)
	case "share":
		return runShare(args[1:], stdout, stderr)
	default:
		_, _ = fmt.Fprintf(stderr, "kdrive: unknown command %q\n", args[0])
		_, _ = fmt.Fprint(stderr, usage)
		return 2
	}
}
