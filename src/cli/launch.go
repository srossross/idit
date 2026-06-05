package cli

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"syscall"
	"time"

	"github.com/srossross/idit/src/workspace"
)

// maxSocketPathLen is a conservative cap on a unix-domain socket path: macOS's
// sun_path holds 104 bytes (including the NUL), Linux 108. Staying under 104
// works on both; we leave a byte of headroom.
const maxSocketPathLen = 103

// ensureSocket resolves a server's daemon socket, starting the daemon (detached)
// and waiting for it if none is running.
func ensureSocket(root string, server workspace.ServerConfig) (string, error) {
	sock := workspace.SocketPath(root, server.Name)
	if canConnect(sock) {
		return sock, nil // already running
	}

	// A unix socket path longer than the OS limit makes the daemon's bind fail
	// with a cryptic "invalid argument"; catch it here so the user gets an
	// actionable message instead of a 20s readiness timeout.
	if len(sock) > maxSocketPathLen {
		return "", fmt.Errorf("daemon socket path is too long for this OS (%d > %d bytes):\n  %s\n  move the workspace to a shorter path",
			len(sock), maxSocketPathLen, sock)
	}

	// Fail fast with clear, actionable messages before spawning a daemon that
	// would otherwise stall the handshake: a missing server binary, or (for
	// Python) a missing interpreter, both otherwise surface as a 20s timeout.
	if err := server.CheckBinary(); err != nil {
		return "", err
	}
	if err := server.ValidateRuntime(); err != nil {
		return "", err
	}

	if os.Getenv("IDIT_NO_AUTOSTART") != "" {
		return "", fmt.Errorf("no %s daemon running for %s.\n  start one with:  idit __serve %s %s",
			server.Name, root, server.Name, root)
	}

	if err := spawnDaemon(server, root); err != nil {
		return "", err
	}
	if err := waitForSocket(sock, 20*time.Second); err != nil {
		return "", err
	}
	return sock, nil
}

// spawnDaemon starts the daemon detached so it outlives this short-lived CLI.
func spawnDaemon(server workspace.ServerConfig, root string) error {
	logPath := workspace.LogPath(root, server.Name)
	fmt.Fprintf(os.Stderr, "idit: starting %s daemon for %s…\n", server.Name, root)

	exe, err := os.Executable()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(workspace.LogDir(root), 0o750); err != nil {
		return err
	}
	//nolint:gosec // logPath is derived from the workspace state dir, not external input
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer func() { _ = logFile.Close() }()

	//nolint:gosec // re-exec of our own binary with the configured server name + workspace root
	cmd := exec.Command(exe, "__serve", server.Name, root)
	cmd.Dir = root
	cmd.Env = os.Environ()
	cmd.Stdin = nil
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	// New session so the daemon isn't killed when the CLI exits and has no
	// controlling terminal.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	// Detach: stop tracking the child so it reparents to init when we exit.
	return cmd.Process.Release()
}

// waitForSocket polls until the socket is accepting connections, or times out.
func waitForSocket(sock string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if canConnect(sock) {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("daemon did not become ready in time (see %s)", workspace.StateDir+"/logs/*.log")
}

func canConnect(sock string) bool {
	conn, err := net.DialTimeout("unix", sock, 400*time.Millisecond)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}
