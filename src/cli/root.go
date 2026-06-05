// Package cli implements the short-lived `idit` front-end: it parses argv, finds
// the workspace, ensures the right daemon is running, sends one request, and
// renders the reply.
package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/srossross/clidit/src/ipc"
	"github.com/srossross/clidit/src/workspace"
)

const requestTimeout = 15 * time.Second

// Run dispatches a CLI invocation (os.Args[1:]).
func Run(args []string) {
	root := newRootCmd()
	root.SetArgs(args)
	if err := root.Execute(); err != nil {
		// Command handlers report their own errors via fail() (which exits 1)
		// and exit 2 on "not found"; this path is reached only for cobra's own
		// failures (unknown command/flag, wrong arg count), matching fail().
		fmt.Fprintf(os.Stderr, "idit: %v\n", err)
		os.Exit(1)
	}
}

func newRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "idit",
		Short: "expose a language server from the command line",
		Long: "idit exposes a language server from the command line.\n\n" +
			"Daemons start automatically; configure servers in .idit/config.yml.\n\n" +
			locatorsHelp,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.AddCommand(
		newInitCmd(),
		newShutdownCmd(),
		newDefCmd(),
		newRefsCmd(),
		newTypeCmd(),
		newMembersCmd(),
		newOutlineCmd(),
		newSymbolCmd(),
		newFindCmd(),
		newLocateCmd(),
		newCallersCmd(),
		newCheckCmd(),
		newRenameCmd(),
		newMvCmd(),
		newExtractCmd(),
		newServerCmd(),
	)
	// Replace the default help command so bare `idit help` prints a full
	// reference (every command + flags), while `idit help <cmd>` still works.
	root.SetHelpCommand(newHelpCmd(root))
	return root
}

// --- shared helpers ---

func requireRoot() string {
	cwd, _ := os.Getwd()
	root, ok := workspace.FindRoot(cwd)
	if !ok {
		fail("not an idit workspace — run `idit init` first")
	}
	return root
}

// socketForFile resolves the daemon that handles file: find the workspace, load
// its config, pick the server for the file's extension, and ensure it's running.
func socketForFile(file string) (string, workspace.ServerConfig) {
	root, ok := workspace.FindRoot(filepath.Dir(file))
	if !ok {
		fail("not an idit workspace — run `idit init` first")
	}
	cfg, err := workspace.Load(root)
	if err != nil {
		fail("%v", err)
	}
	server, ok := workspace.ServerForFile(cfg, file)
	if !ok {
		ext := ""
		if dot := filepath.Ext(file); dot != "" {
			ext = dot
		}
		known := "(none)"
		if names := serverNames(cfg); names != "" {
			known = names
		}
		fail("no server configured for %s files (configured: %s; add one with `idit server add`)", ext, known)
	}
	sock, err := ensureSocket(root, server)
	if err != nil {
		fail("%v", err)
	}
	return sock, server
}

func sendOp(sock, serverName string, req ipc.Request) ipc.Response {
	resp, err := ipc.RequestDaemon(sock, req, requestTimeout)
	if err != nil {
		if errors.Is(err, ipc.ErrDaemonUnreachable) {
			fail("daemon socket is stale or unreachable (%s).\n  restart it with:  idit __serve %s", sock, serverName)
		}
		fail("%v", err)
	}
	return resp
}

// runOp resolves the daemon owning a target — `file:line:col` or `file#symbol` —
// and runs a positional op. apply may set extra request fields.
func runOp(verb, targetArg string, apply func(*ipc.Request)) ipc.Response {
	target := mustResolve(targetArg)
	file := resolveCwd(target.File)
	sock, server := socketForFile(file)

	req := ipc.Request{Op: verb, File: file, Line: target.Line, Col: target.Col}
	if apply != nil {
		apply(&req)
	}
	resp := sendOp(sock, server.Name, req)
	if !resp.OK {
		fail("%s", resp.Error)
	}
	return resp
}

func resolveCwd(p string) string {
	if filepath.IsAbs(p) {
		return filepath.Clean(p)
	}
	cwd, _ := os.Getwd()
	return filepath.Join(cwd, p)
}

func serverNames(cfg workspace.IditConfig) string {
	var out strings.Builder
	for i, s := range cfg.Servers {
		if i > 0 {
			out.WriteString(", ")
		}
		out.WriteString(s.Name)
	}
	return out.String()
}

func printJSON(v any) {
	b, err := json.Marshal(v)
	if err != nil {
		fail("%v", err)
	}
	fmt.Println(string(b))
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "idit: "+format+"\n", args...)
	os.Exit(1)
}
