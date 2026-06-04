package workspace

import (
	"bytes"
	"fmt"
	"os"
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
	},
	"gopls": {
		Kind:       "lsp",
		Command:    []string{"gopls"},
		Extensions: []string{".go"},
		LanguageID: "go",
	},
}

// PresetNames returns preset keys in a stable order for help text.
func PresetNames() []string {
	return []string{"tsserver", "gopls"}
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
			Kind       string   `yaml:"kind"`
			Command    []string `yaml:"command"`
			Extensions []string `yaml:"extensions"`
			LanguageID string   `yaml:"languageId"`
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
