package treesitter

import "sort"

// langExtensions maps a language name (and common aliases) to its file
// extensions, for `--lang`. It is kept explicit rather than derived from
// registry so deliberate choices stay visible — notably that .h is treated as
// C++ (a near-superset of C), so the cpp language owns it.
var langExtensions = map[string][]string{
	"go":         {".go"},
	"javascript": {".js", ".mjs", ".cjs", ".jsx"},
	"js":         {".js", ".mjs", ".cjs", ".jsx"},
	"typescript": {".ts", ".mts", ".cts", ".tsx"},
	"ts":         {".ts", ".mts", ".cts", ".tsx"},
	"python":     {".py", ".pyi"},
	"py":         {".py", ".pyi"},
	"c":          {".c"},
	"cpp":        {".cpp", ".cc", ".cxx", ".hpp", ".hh", ".h"},
	"c++":        {".cpp", ".cc", ".cxx", ".hpp", ".hh", ".h"},
	"sql":        {".sql"},
}

// CanonicalLang resolves a language name or alias to its file extensions,
// reporting whether it is recognized.
func CanonicalLang(name string) (exts []string, ok bool) {
	exts, ok = langExtensions[name]
	return exts, ok
}

// LangNames returns the recognized language names (canonical and aliases),
// sorted — for error messages.
func LangNames() []string {
	names := make([]string, 0, len(langExtensions))
	for name := range langExtensions {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
