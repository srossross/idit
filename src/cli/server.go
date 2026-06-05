package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/srossross/idit/src/workspace"
)

func newServerCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "server",
		Short: "manage configured language servers",
		Long: fmt.Sprintf("manage configured language servers\n\npresets: %s",
			strings.Join(workspace.PresetNames(), ", ")),
	}
	cmd.AddCommand(newServerAddCmd(), newServerListCmd(), newServerRemoveCmd())
	return cmd
}

func newServerAddCmd() *cobra.Command {
	var command, ext, lang string
	var autoConfig bool
	cmd := &cobra.Command{
		Use:   "add <name>",
		Short: "add a language server (preset or --command/--ext)",
		Long: fmt.Sprintf("add a language server (preset or --command/--ext)\n\npresets: %s",
			strings.Join(workspace.PresetNames(), ", ")),
		Args: cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			name := args[0]

			root := requireRoot()
			cfg, err := workspace.Load(root)
			if err != nil {
				fail("%v", err)
			}
			if _, exists := workspace.ServerByName(cfg, name); exists {
				fail("server %q is already configured", name)
			}

			hasCommand := c.Flags().Changed("command")
			hasExt := c.Flags().Changed("ext")
			hasLang := c.Flags().Changed("lang")
			preset, hasPreset := workspace.Presets[name]

			var server workspace.ServerConfig
			switch {
			case hasPreset && !hasCommand:
				server = preset
				server.Name = name
			case hasCommand && hasExt:
				exts := strings.Split(ext, ",")
				for i := range exts {
					exts[i] = strings.ToLower(strings.TrimSpace(exts[i]))
				}
				langID := name
				if hasLang {
					langID = lang
				}
				server = workspace.ServerConfig{
					Name:       name,
					Kind:       "lsp",
					Command:    strings.Fields(command),
					Extensions: exts,
					LanguageID: langID,
				}
			default:
				fail("unknown preset %q — provide --command and --ext to define a custom server\n  presets: %s",
					name, strings.Join(workspace.PresetNames(), ", "))
			}

			// --auto-config detects language tooling and writes it into the
			// saved config. It is opt-in: idit never auto-detects at query time.
			if autoConfig && server.RequiresInterpreter() {
				py := workspace.DetectPythonInterpreter(root)
				if py == "" {
					fail("--auto-config: no Python interpreter found (looked at $VIRTUAL_ENV, ./.venv, ./venv)\n" +
						"  create one (e.g. `uv venv`) or set settings.python.pythonPath manually")
				}
				if server.Settings == nil {
					server.Settings = map[string]map[string]any{}
				}
				server.Settings["python"] = map[string]any{"pythonPath": py}
				fmt.Printf("auto-config: python.pythonPath = %s\n", py)
			}

			cfg.Servers = append(cfg.Servers, server)
			out, err := workspace.Emit(cfg)
			if err != nil {
				fail("%v", err)
			}
			if err := os.WriteFile(workspace.ConfigPath(root), out, 0o600); err != nil {
				fail("%v", err)
			}
			fmt.Printf("added server %q [%s]\n", name, strings.Join(server.Extensions, " "))
			// Guide now: the binary is needed to run; the project setup (e.g. a
			// clangd compilation database) is needed for accurate results. The
			// query path fails fast on a missing binary later.
			if err := server.CheckBinary(); err != nil {
				fmt.Fprintf(os.Stderr, "warning: %v\n", err)
			}
			if hint := server.ProjectSetupHint(root); hint != "" {
				fmt.Fprintf(os.Stderr, "warning: server %q: %s\n", name, hint)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&command, "command", "", `server command, e.g. "tsserver --stdio"`)
	cmd.Flags().StringVar(&ext, "ext", "", "comma-separated extensions, e.g. .ts,.tsx")
	cmd.Flags().StringVar(&lang, "lang", "", "LSP language id (defaults to the server name)")
	cmd.Flags().BoolVar(&autoConfig, "auto-config", false,
		"detect language tooling (Python interpreter from $VIRTUAL_ENV/.venv/venv) and write it to config")
	return cmd
}

func newServerListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "list running, configured, and available servers",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			root := requireRoot()
			cfg, err := workspace.Load(root)
			if err != nil {
				fail("%v", err)
			}

			// Configured servers split by whether their daemon is alive.
			var running, stopped []string
			for _, s := range cfg.Servers {
				line := formatServerLine(s)
				if canConnect(workspace.SocketPath(root, s.Name)) {
					running = append(running, line)
				} else {
					stopped = append(stopped, line)
				}
			}

			// Orphan daemons: a live socket whose server is no longer configured.
			for _, key := range runningKeys(filepath.Join(root, workspace.StateDir)) {
				if _, ok := workspace.ServerByName(cfg, key); ok {
					continue
				}
				if canConnect(workspace.SocketPath(root, key)) {
					running = append(running, fmt.Sprintf("%s  (not configured)", key))
				}
			}

			// Available presets not yet added to this workspace.
			var available []string
			for _, name := range workspace.PresetNames() {
				if _, ok := workspace.ServerByName(cfg, name); ok {
					continue
				}
				preset := workspace.Presets[name]
				preset.Name = name
				line := fmt.Sprintf("%s   (idit server add %s)", formatServerLine(preset), name)
				if preset.Install != "" {
					line += "\n      install: " + preset.Install
				}
				available = append(available, line)
			}

			printServerSection("running", running)
			printServerSection("configured (stopped)", stopped)
			printServerSection("available (presets)", available)

			if len(running) == 0 && len(stopped) == 0 && len(available) == 0 {
				fmt.Fprintln(os.Stderr, "no servers configured — add one with `idit server add <name>`")
			}
			return nil
		},
	}
}

// formatServerLine renders a server as `name  [exts]  command`.
func formatServerLine(s workspace.ServerConfig) string {
	return fmt.Sprintf("%s  [%s]  %s", s.Name, strings.Join(s.Extensions, " "), strings.Join(s.Command, " "))
}

// printServerSection prints a headed block of indented lines, skipping empties.
func printServerSection(header string, lines []string) {
	if len(lines) == 0 {
		return
	}
	fmt.Printf("%s:\n", header)
	for _, line := range lines {
		fmt.Printf("  %s\n", line)
	}
	fmt.Println()
}

func newServerRemoveCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "remove <name>",
		Aliases: []string{"rm"},
		Short:   "remove a configured server",
		Args:    cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			name := args[0]
			root := requireRoot()
			cfg, err := workspace.Load(root)
			if err != nil {
				fail("%v", err)
			}
			if _, ok := workspace.ServerByName(cfg, name); !ok {
				fail("no server %q configured", name)
			}
			kept := cfg.Servers[:0]
			for _, s := range cfg.Servers {
				if s.Name != name {
					kept = append(kept, s)
				}
			}
			cfg.Servers = kept
			out, err := workspace.Emit(cfg)
			if err != nil {
				fail("%v", err)
			}
			if err := os.WriteFile(workspace.ConfigPath(root), out, 0o600); err != nil {
				fail("%v", err)
			}
			fmt.Printf("removed server %q\n", name)
			return nil
		},
	}
}
