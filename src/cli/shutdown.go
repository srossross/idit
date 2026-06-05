package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/srossross/clidit/src/ipc"
	"github.com/srossross/clidit/src/workspace"
)

func killSignal() syscall.Signal { return syscall.SIGKILL }

func newShutdownCmd() *cobra.Command {
	var force bool
	cmd := &cobra.Command{
		Use:   "shutdown [path]",
		Short: "stop the daemon(s) for a workspace (defaults to the current directory)",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			start := "."
			if len(args) > 0 {
				start = args[0]
			}
			root, ok := workspace.FindRoot(resolveCwd(start))
			if !ok {
				fail("not an idit workspace — nothing to shut down")
			}

			keys := runningKeys(filepath.Join(root, workspace.StateDir))
			if len(keys) == 0 {
				fmt.Fprintln(os.Stderr, "no daemons running")
				return nil
			}

			for _, key := range keys {
				sock := workspace.SocketPath(root, key)
				if force {
					forceKill(root, key)
					continue
				}
				_, err := ipc.RequestDaemon(sock, ipc.Request{Op: "shutdown"}, requestTimeout)
				switch err {
				case nil:
					fmt.Fprintf(os.Stderr, "%s: shutting down\n", key)
				case ipc.ErrDaemonUnreachable:
					_ = os.Remove(sock) // stale socket — best effort
					fmt.Fprintf(os.Stderr, "%s: not running (cleared stale socket)\n", key)
				default:
					fmt.Fprintf(os.Stderr, "%s: %v\n", key, err)
				}
			}
			return nil
		},
	}
	cmd.Flags().BoolVarP(&force, "force", "f", false, "kill daemons via their pid file")
	return cmd
}

// runningKeys lists language keys with a socket or pid file in the state dir.
func runningKeys(stateDir string) []string {
	entries, err := os.ReadDir(stateDir)
	if err != nil {
		return nil
	}
	seen := map[string]struct{}{}
	var keys []string
	add := func(k string) {
		if _, ok := seen[k]; !ok {
			seen[k] = struct{}{}
			keys = append(keys, k)
		}
	}
	for _, e := range entries {
		name := e.Name()
		if before, ok := strings.CutSuffix(name, ".sock"); ok {
			add(before)
		} else if before, ok := strings.CutSuffix(name, ".pid"); ok {
			add(before)
		}
	}
	return keys
}

// forceKill kills a daemon via its pid file (for a wedged, unresponsive daemon).
func forceKill(root, key string) {
	pidFile := workspace.PidPath(root, key)
	//nolint:gosec // pidFile is derived from the workspace state dir, not external input
	data, err := os.ReadFile(pidFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s: no pid file\n", key)
		return
	}
	pid, _ := atoi(strings.TrimSpace(string(data)))
	if proc, err := os.FindProcess(pid); err == nil && proc.Signal(killSignal()) == nil {
		fmt.Fprintf(os.Stderr, "%s: killed (pid %d)\n", key, pid)
	} else {
		fmt.Fprintf(os.Stderr, "%s: already gone (pid %d)\n", key, pid)
	}
	_ = os.Remove(pidFile)
	_ = os.Remove(workspace.SocketPath(root, key))
}
