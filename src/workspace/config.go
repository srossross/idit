package workspace

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"
)

// Per-workspace configuration lives in `.idit/config.yml`. It lists the language
// servers to run, keyed by name. For now every server is `kind: lsp`.

type ServerConfig struct {
	Name string
	Kind string // always "lsp"
	// Command is the command + args to launch the server (speaks LSP over stdio).
	Command []string
	// Extensions are the file extensions (with leading dot) this server handles.
	Extensions []string
	// LanguageID is the LSP languageId for documents this server opens.
	LanguageID string
	// Settings are LSP configuration sections returned to the server when it
	// issues a workspace/configuration request, keyed by section name (e.g.
	// "python" → {"pythonPath": "/abs/.venv/bin/python"}). Persisted in
	// .idit/config.yml; the daemon serves them verbatim.
	Settings map[string]map[string]any
	// Install is a human-facing command that installs this server's binary. It
	// is preset metadata shown when the binary is missing; not persisted.
	Install string
}

// IditConfig keeps servers in an ordered slice so add-order is preserved on emit.
type IditConfig struct {
	Servers []ServerConfig
}

// Presets are built-in server configs, so `idit server add <name>` needs no flags.
var Presets = map[string]ServerConfig{
	"tsserver": {
		Kind:       "lsp",
		Command:    []string{"typescript-language-server", "--stdio"},
		Extensions: []string{".ts", ".tsx", ".js", ".jsx", ".mts", ".cts", ".mjs", ".cjs"},
		LanguageID: "typescript",
		Install:    "npm install -g typescript-language-server typescript",
	},
	"gopls": {
		Kind:       "lsp",
		Command:    []string{"gopls"},
		Extensions: []string{".go"},
		LanguageID: "go",
		Install:    "go install golang.org/x/tools/gopls@latest",
	},
	"clangd": {
		Kind:       "lsp",
		Command:    []string{"clangd"},
		Extensions: []string{".c", ".h", ".cpp", ".cc", ".cxx", ".hpp", ".hh"},
		// clangd resolves the actual C vs C++ mode from the file and
		// compile_commands.json, not this languageId.
		LanguageID: "cpp",
		Install:    "install via your OS package manager (brew install llvm | apt install clangd)",
	},
	// basedpyright is the pyright fork whose open-source language server includes
	// the features Microsoft keeps in closed-source Pylance (rename, references,
	// completion, inlay hints) — the ones idit's commands rely on.
	"basedpyright": {
		Kind:       "lsp",
		Command:    []string{"basedpyright-langserver", "--stdio"},
		Extensions: []string{".py", ".pyi"},
		LanguageID: "python",
		Install:    "uv tool install basedpyright  (or: npm install -g basedpyright)",
	},
}

// PresetNames returns preset keys in a stable order for help text.
func PresetNames() []string {
	return []string{"tsserver", "gopls", "clangd", "basedpyright"}
}

func ConfigPath(root string) string {
	return filepath.Join(root, StateDir, "config.yml")
}

// Load reads and validates `.idit/config.yml`. A missing file yields an empty
// config. Server order from the file is preserved.
func Load(root string) (IditConfig, error) {
	path := ConfigPath(root)
	//nolint:gosec // path is the workspace's own .idit/config.yml, derived from root
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return IditConfig{}, nil
	}
	if err != nil {
		return IditConfig{}, err
	}

	var doc struct {
		Servers yaml.Node `yaml:"servers"`
	}
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return IditConfig{}, err
	}

	var cfg IditConfig
	if doc.Servers.Kind != yaml.MappingNode {
		return cfg, nil
	}
	// MappingNode content alternates key, value, key, value… in file order.
	for i := 0; i+1 < len(doc.Servers.Content); i += 2 {
		name := doc.Servers.Content[i].Value
		var entry struct {
			Kind       string                    `yaml:"kind"`
			Command    []string                  `yaml:"command"`
			Extensions []string                  `yaml:"extensions"`
			LanguageID string                    `yaml:"languageId"`
			Settings   map[string]map[string]any `yaml:"settings"`
		}
		if err := doc.Servers.Content[i+1].Decode(&entry); err != nil {
			return cfg, fmt.Errorf("server %q: %w", name, err)
		}
		kind := entry.Kind
		if kind == "" {
			kind = "lsp"
		}
		if kind != "lsp" {
			return cfg, fmt.Errorf("server %q: unsupported kind %q (only \"lsp\" is supported)", name, kind)
		}
		if len(entry.Command) == 0 {
			return cfg, fmt.Errorf("server %q: missing command", name)
		}
		if len(entry.Extensions) == 0 {
			return cfg, fmt.Errorf("server %q: missing extensions", name)
		}
		exts := make([]string, len(entry.Extensions))
		for j, e := range entry.Extensions {
			exts[j] = strings.ToLower(e)
		}
		langID := entry.LanguageID
		if langID == "" {
			langID = name
		}
		cfg.Servers = append(cfg.Servers, ServerConfig{
			Name:       name,
			Kind:       "lsp",
			Command:    entry.Command,
			Extensions: exts,
			LanguageID: langID,
			Settings:   entry.Settings,
		})
	}
	return cfg, nil
}

// Emit serializes config to YAML, preserving server insertion order by building
// the mapping node by hand (a Go map would reorder keys).
func Emit(cfg IditConfig) ([]byte, error) {
	servers := &yaml.Node{Kind: yaml.MappingNode}
	for _, s := range cfg.Servers {
		body := &yaml.Node{Kind: yaml.MappingNode}
		addField(body, "kind", scalar(s.Kind))
		addField(body, "command", seq(s.Command))
		addField(body, "extensions", seq(s.Extensions))
		addField(body, "languageId", scalar(s.LanguageID))
		if len(s.Settings) > 0 {
			sn := &yaml.Node{}
			if err := sn.Encode(s.Settings); err != nil {
				return nil, err
			}
			addField(body, "settings", sn)
		}
		servers.Content = append(servers.Content, scalar(s.Name), body)
	}
	root := &yaml.Node{Kind: yaml.MappingNode, Content: []*yaml.Node{
		scalar("servers"), servers,
	}}

	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(root); err != nil {
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func addField(m *yaml.Node, key string, value *yaml.Node) {
	m.Content = append(m.Content, scalar(key), value)
}

func scalar(v string) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Value: v}
}

func seq(items []string) *yaml.Node {
	n := &yaml.Node{Kind: yaml.SequenceNode}
	for _, item := range items {
		n.Content = append(n.Content, scalar(item))
	}
	return n
}

// ServerForFile returns the server that handles a file, by extension.
func ServerForFile(cfg IditConfig, path string) (ServerConfig, bool) {
	dot := strings.LastIndexByte(path, '.')
	if dot == -1 {
		return ServerConfig{}, false
	}
	ext := strings.ToLower(path[dot:])
	for _, s := range cfg.Servers {
		if slices.Contains(s.Extensions, ext) {
			return s, true
		}
	}
	return ServerConfig{}, false
}

// ServerByName returns the configured server with the given name.
func ServerByName(cfg IditConfig, name string) (ServerConfig, bool) {
	for _, s := range cfg.Servers {
		if s.Name == name {
			return s, true
		}
	}
	return ServerConfig{}, false
}

// InstallHint returns the install command for a known preset, or "" for a
// custom (non-preset) server we have no guidance for.
func InstallHint(name string) string {
	return Presets[name].Install
}

// CheckBinary reports whether the server's executable resolves on PATH. A bare
// command name is looked up; an absolute/relative path is checked directly. It
// returns a guiding error (with the install hint when known) when missing, so
// callers can fail fast instead of timing out on a daemon that can't start.
func (s ServerConfig) CheckBinary() error {
	if len(s.Command) == 0 {
		return fmt.Errorf("server %q: no command configured", s.Name)
	}
	bin := s.Command[0]
	if _, err := exec.LookPath(bin); err == nil {
		return nil
	}
	msg := fmt.Sprintf("server %q: %q is not installed or not on PATH", s.Name, bin)
	if hint := InstallHint(s.Name); hint != "" {
		msg += "\n  install it with:  " + hint
	}
	return errors.New(msg)
}

// ProjectSetupHint returns guidance when a server's project-side prerequisites
// look unmet (today: a clangd-family server with no compilation database), or
// "" when things look fine. Surfaced at `server add` time so the user sets the
// project up then, rather than getting silently degraded results later.
func (s ServerConfig) ProjectSetupHint(root string) string {
	if s.LanguageID == "cpp" && !hasCompileDB(root) {
		return "no compilation database found (compile_commands.json / compile_flags.txt / .clangd).\n" +
			"  clangd needs one for accurate results — generate it with:\n" +
			"    cmake -S . -B build -DCMAKE_EXPORT_COMPILE_COMMANDS=1"
	}
	return ""
}

// hasCompileDB reports whether clangd would find a compilation database rooted
// at root: compile_commands.json (root or build/), compile_flags.txt, or .clangd.
func hasCompileDB(root string) bool {
	for _, p := range []string{
		filepath.Join(root, "compile_commands.json"),
		filepath.Join(root, "build", "compile_commands.json"),
		filepath.Join(root, "compile_flags.txt"),
		filepath.Join(root, ".clangd"),
	} {
		if _, err := os.Stat(p); err == nil {
			return true
		}
	}
	return false
}

// LSPSettings flattens Settings to section→value for an lspclient
// workspace/configuration response. Returns nil when no settings are set.
func (s ServerConfig) LSPSettings() map[string]any {
	if len(s.Settings) == 0 {
		return nil
	}
	out := make(map[string]any, len(s.Settings))
	for sec, obj := range s.Settings {
		out[sec] = obj
	}
	return out
}

// RequiresInterpreter reports whether this server needs a Python interpreter
// configured. Pyright-family servers resolve imports and types through the
// interpreter's site-packages, so without one they report spurious errors;
// idit fails fast rather than letting that happen silently.
func (s ServerConfig) RequiresInterpreter() bool {
	return s.LanguageID == "python"
}

// InterpreterPath returns the configured settings.python.pythonPath, or "".
func (s ServerConfig) InterpreterPath() string {
	py, ok := s.Settings["python"]
	if !ok {
		return ""
	}
	p, _ := py["pythonPath"].(string)
	return p
}

// ValidateRuntime fails fast when a server is missing configuration it needs to
// run correctly. For Python servers that means a resolvable interpreter path.
func (s ServerConfig) ValidateRuntime() error {
	if !s.RequiresInterpreter() {
		return nil
	}
	p := s.InterpreterPath()
	if p == "" {
		return fmt.Errorf("server %q: no Python interpreter configured.\n"+
			"  set settings.python.pythonPath in .idit/config.yml, or re-add with auto-detect:\n"+
			"    idit server remove %s && idit server add %s --auto-config",
			s.Name, s.Name, s.Name)
	}
	if st, err := os.Stat(p); err != nil || st.IsDir() {
		return fmt.Errorf("server %q: configured Python interpreter not found at %s\n"+
			"  fix settings.python.pythonPath in .idit/config.yml", s.Name, p)
	}
	return nil
}

// DetectPythonInterpreter resolves a Python interpreter for --auto-config,
// checking $VIRTUAL_ENV, then ./.venv, then ./venv under root. Returns "" if
// none is found. This runs only on explicit opt-in, never at query time.
func DetectPythonInterpreter(root string) string {
	if ve := os.Getenv("VIRTUAL_ENV"); ve != "" {
		if p := venvPython(ve); p != "" {
			return p
		}
	}
	for _, d := range []string{".venv", "venv"} {
		if p := venvPython(filepath.Join(root, d)); p != "" {
			return p
		}
	}
	return ""
}

// venvPython returns the interpreter inside a virtualenv dir, or "" if absent.
func venvPython(venvDir string) string {
	for _, rel := range []string{
		filepath.Join("bin", "python"),
		filepath.Join("bin", "python3"),
		filepath.Join("Scripts", "python.exe"),
	} {
		p := filepath.Join(venvDir, rel)
		//nolint:gosec // probing a user-named venv dir for its interpreter is the point
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p
		}
	}
	return ""
}
