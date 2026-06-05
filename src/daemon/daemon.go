package daemon

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/srossross/clidit/src/ipc"
	"github.com/srossross/clidit/src/lspclient"
	"github.com/srossross/clidit/src/lsputil"
	"github.com/srossross/clidit/src/workspace"
)

var debug = os.Getenv("IDIT_DEBUG") != ""

// Run is the daemon entry point: `idit __serve <name> [root]`. It spawns the
// language server, completes the LSP handshake, then serves CLI requests over a
// Unix socket until idle or shut down.
func Run(args []string) {
	if len(args) < 1 {
		fail("usage: idit __serve <server> [project-root]")
	}
	name := args[0]

	cwd, _ := os.Getwd()
	root := cwd
	if len(args) >= 2 && args[1] != "" {
		root = absFrom(cwd, args[1])
	}
	if info, err := os.Stat(root); err != nil || !info.IsDir() {
		fail("not a directory: %s", root)
	}

	cfg, err := workspace.Load(root)
	if err != nil {
		fail("%v", err)
	}
	server, ok := workspace.ServerByName(cfg, name)
	if !ok {
		fail("no server %q in this workspace's config.\n  configured: %s", name, configuredNames(cfg))
	}

	sock := workspace.SocketPath(root, server.Name)
	pidFile := workspace.PidPath(root, server.Name)

	ensureSocketFree(sock)
	if err := os.MkdirAll(filepath.Dir(sock), 0o750); err != nil {
		fail("%v", err)
	}
	if err := os.WriteFile(pidFile, []byte(strconv.Itoa(os.Getpid())), 0o600); err != nil {
		logf("could not write pid file: %v", err)
	}

	logf("starting %s for %s", server.Name, root)

	progress := lspclient.NewProgressTracker()
	diagnostics := lspclient.NewDiagnosticsTracker()

	lsp, err := lspclient.New(lspclient.Options{
		Cmd: server.Command,
		Cwd: root,
		Env: spawnEnv(root),
		OnNotification: func(method string, params json.RawMessage) {
			progress.Handle(method, params)
			diagnostics.Handle(method, params)
			if debug {
				logf("notify %s %s", method, truncate(params))
			}
		},
		OnStderr: func(line string) {
			if debug && line != "" {
				logf("[server] %s", line)
			}
		},
	})
	if err != nil {
		fail("could not start language server: %v", err)
	}

	go func() {
		<-lsp.Exited()
		fail("language server exited (code %d)", lsp.ExitCode())
	}()

	initResult := mustRequest(lsp, "initialize", initializeParams(root))
	var initParsed struct {
		Capabilities map[string]any `json:"capabilities"`
	}
	// A malformed initialize result just leaves capabilities nil, handled below.
	_ = json.Unmarshal(initResult, &initParsed)
	lsp.Notify("initialized", map[string]any{})

	capabilities := initParsed.Capabilities
	if capabilities == nil {
		capabilities = map[string]any{}
	}

	var (
		once        sync.Once
		idleTimer   *time.Timer
		idleTimerMu sync.Mutex
		srv         *ipc.Server
	)

	idleMs := 5 * 60 * 1000
	if v, err := strconv.Atoi(os.Getenv("IDIT_IDLE_MS")); err == nil && v != 0 {
		idleMs = v
	}

	shutdown := func(code int) {
		once.Do(func() {
			idleTimerMu.Lock()
			if idleTimer != nil {
				idleTimer.Stop()
			}
			idleTimerMu.Unlock()
			logf("shutting down")
			lsp.Notify("exit", nil)
			lsp.Kill()
			if srv != nil {
				srv.Stop()
			}
			_ = os.Remove(sock)
			_ = os.Remove(pidFile)
			os.Exit(code)
		})
	}

	resetIdle := func() {
		if idleMs <= 0 {
			return
		}
		idleTimerMu.Lock()
		defer idleTimerMu.Unlock()
		if idleTimer != nil {
			idleTimer.Stop()
		}
		idleTimer = time.AfterFunc(time.Duration(idleMs)*time.Millisecond, func() {
			logf("idle for %ds — shutting down", idleMs/1000)
			shutdown(0)
		})
	}

	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigs
		shutdown(0)
	}()

	ctx := &Context{
		Server:       server,
		Root:         root,
		SocketPath:   sock,
		Lsp:          lsp,
		Capabilities: capabilities,
		Progress:     progress,
		Diagnostics:  diagnostics,
		Shutdown:     shutdown,
		opened:       make(map[string]openDoc),
	}

	ln, err := net.Listen("unix", sock)
	if err != nil {
		fail("could not listen on %s: %v", sock, err)
	}
	srv = ipc.Serve(ln, func(req ipc.Request) ipc.Response {
		return Dispatch(ctx, req)
	}, resetIdle)
	resetIdle()

	logf("ready — listening on %s", sock)
	logf("idle shutdown after %ds; `idit shutdown` to stop now", idleMs/1000)

	// Block forever; shutdown calls os.Exit.
	select {}
}

// ensureSocketFree clears a stale socket file, or fails if a live daemon owns it.
func ensureSocketFree(sock string) {
	if _, err := os.Stat(sock); err != nil {
		return
	}
	conn, err := net.DialTimeout("unix", sock, 500*time.Millisecond)
	if err == nil {
		_ = conn.Close()
		fail("a daemon is already serving this workspace (%s)", sock)
	}
	// Nothing is listening — the file is stale. Clear it.
	_ = os.Remove(sock)
}

func mustRequest(lsp *lspclient.Client, method string, params any) json.RawMessage {
	res, err := lsp.Request(method, params)
	if err != nil {
		fail("%s failed: %v", method, err)
	}
	return res
}

// spawnEnv builds the child env: the inherited environment with PATH overridden
// to prefer the workspace's node_modules/.bin.
func spawnEnv(root string) []string {
	env := os.Environ()
	out := make([]string, 0, len(env)+1)
	for _, e := range env {
		if len(e) >= 5 && e[:5] == "PATH=" {
			continue
		}
		out = append(out, e)
	}
	return append(out, "PATH="+workspace.SpawnPath(root))
}

func initializeParams(root string) map[string]any {
	rootURI := lsputil.FileToURI(root)
	return map[string]any{
		"processId":  os.Getpid(),
		"clientInfo": map[string]any{"name": "idit", "version": "0.1.0"},
		"rootUri":    rootURI,
		"workspaceFolders": []map[string]any{
			{"uri": rootURI, "name": filepath.Base(root)},
		},
		"capabilities": map[string]any{
			"textDocument": map[string]any{
				"synchronization": map[string]any{"dynamicRegistration": false},
				"definition":      map[string]any{"dynamicRegistration": false, "linkSupport": true},
				"references":      map[string]any{"dynamicRegistration": false},
				"hover":           map[string]any{"dynamicRegistration": false, "contentFormat": []string{"markdown", "plaintext"}},
				"rename":          map[string]any{"dynamicRegistration": false, "prepareSupport": true},
				"codeAction": map[string]any{
					"dynamicRegistration": false,
					"codeActionLiteralSupport": map[string]any{
						"codeActionKind": map[string]any{
							"valueSet": []string{"quickfix", "refactor", "refactor.extract", "source.organizeImports"},
						},
					},
					"resolveSupport": map[string]any{"properties": []string{"edit"}},
					"dataSupport":    true,
				},
				"publishDiagnostics": map[string]any{
					"relatedInformation": true,
					"tagSupport":         map[string]any{"valueSet": []int{1, 2}},
				},
			},
			"workspace": map[string]any{
				"workspaceFolders": true,
				"configuration":    true,
				"workspaceEdit": map[string]any{
					"documentChanges":    true,
					"resourceOperations": []string{"create", "rename", "delete"},
				},
			},
			"window": map[string]any{"workDoneProgress": true},
		},
	}
}

func configuredNames(cfg workspace.IditConfig) string {
	if len(cfg.Servers) == 0 {
		return "(none)"
	}
	names := make([]string, len(cfg.Servers))
	for i, s := range cfg.Servers {
		names[i] = s.Name
	}
	return joinComma(names)
}

func joinComma(items []string) string {
	var out strings.Builder
	for i, s := range items {
		if i > 0 {
			out.WriteString(", ")
		}
		out.WriteString(s)
	}
	return out.String()
}

func truncate(v json.RawMessage) string {
	s := string(v)
	if len(s) > 120 {
		return s[:117] + "..."
	}
	return s
}

func logf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "idit: "+format+"\n", args...)
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "idit: "+format+"\n", args...)
	os.Exit(1)
}

func absFrom(cwd, p string) string {
	if filepath.IsAbs(p) {
		return filepath.Clean(p)
	}
	return filepath.Join(cwd, p)
}
