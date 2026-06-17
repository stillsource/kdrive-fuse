package manifest

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
)

// PathFor returns the on-disk location of the manifest for a given
// (local root, remote root) pair: $XDG_STATE_HOME/kdrive/<key>.tsv, falling
// back to ~/.local/state/kdrive when XDG_STATE_HOME is unset. The key is a hash
// of the absolute local root and the remote root, so each pairing has its own
// stable manifest kept outside the synced tree.
func PathFor(localRoot, remoteRoot string) (string, error) {
	abs, err := filepath.Abs(localRoot)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(abs + "\n" + remoteRoot))
	key := hex.EncodeToString(sum[:])
	return filepath.Join(stateDir(), "kdrive", key+".tsv"), nil
}

// stateDir returns the XDG state base directory.
func stateDir() string {
	if d := os.Getenv("XDG_STATE_HOME"); d != "" {
		return d
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "state")
}
