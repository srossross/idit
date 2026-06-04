// Package workspace owns the on-disk layout of an idit workspace: the `.idit/`
// state directory, the socket/pid/log paths under it, workspace-root discovery,
// and the YAML config that lives there. It is a leaf package (no internal
// dependencies), which breaks the config↔paths↔daemon import cycle the
// TypeScript version tolerated.
package workspace

import (
	"os"
	"path/filepath"
	"strings"
)

// StateDir is the per-workspace state directory, created by `idit init`. The CLI
// finds the right socket from any cwd by walking up to the directory holding it.
const StateDir = ".idit"

// SocketPath is the Unix socket path for a workspace + language.
func SocketPath(root, languageKey string) string {
	return filepath.Join(root, StateDir, languageKey+".sock")
}

// PidPath is the pid-file path for a workspace + language.
func PidPath(root, languageKey string) string {
	return filepath.Join(root, StateDir, languageKey+".pid")
}

// LogPath is the daemon log path for a workspace + language.
func LogPath(root, languageKey string) string {
	return filepath.Join(root, StateDir, languageKey+".log")
}

// FindRoot walks up from start for a directory containing a `.idit/` marker.
// Returns the root and true, or "" and false if none is found.
func FindRoot(start string) (string, bool) {
	dir := start
	for {
		if info, err := os.Stat(filepath.Join(dir, StateDir)); err == nil && info.IsDir() {
			return dir, true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", false
		}
		dir = parent
	}
}

// FindSocket walks up from start looking for `.idit/<key>.sock`.
func FindSocket(start, languageKey string) (string, bool) {
	dir := start
	for {
		candidate := SocketPath(dir, languageKey)
		if _, err := os.Stat(candidate); err == nil {
			return candidate, true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", false
		}
		dir = parent
	}
}

// SpawnPath builds the PATH for spawning language servers: prefer binaries from
// the workspace's `node_modules/.bin`, then fall back to the inherited PATH.
func SpawnPath(root string) string {
	var parts []string
	for _, c := range []string{filepath.Join(root, "node_modules", ".bin"), os.Getenv("PATH")} {
		if c != "" {
			parts = append(parts, c)
		}
	}
	return strings.Join(parts, string(os.PathListSeparator))
}
