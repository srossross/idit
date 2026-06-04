package lsputil

import (
	"regexp"
	"strings"
)

// NameMatcher filters symbol/member names by an optional case-sensitive prefix
// and an optional RE2 regex (--grep). With ignoreCase, both become
// case-insensitive (the regex gains an implicit (?i)).
type NameMatcher struct {
	prefix     string
	re         *regexp.Regexp
	ignoreCase bool
}

// NewNameMatcher builds a matcher from the --prefix / --grep / --ignore-case
// inputs, returning an error only when grep is not a valid RE2 regex.
func NewNameMatcher(prefix, grep string, ignoreCase bool) (NameMatcher, error) {
	m := NameMatcher{prefix: prefix, ignoreCase: ignoreCase}
	if ignoreCase {
		m.prefix = strings.ToLower(prefix)
	}
	if grep != "" {
		pat := grep
		if ignoreCase {
			pat = "(?i)" + pat
		}
		re, err := regexp.Compile(pat)
		if err != nil {
			return NameMatcher{}, err
		}
		m.re = re
	}
	return m, nil
}

// Active reports whether the matcher would filter anything.
func (m NameMatcher) Active() bool { return m.prefix != "" || m.re != nil }

// Match reports whether name passes every active filter.
func (m NameMatcher) Match(name string) bool {
	if m.prefix != "" {
		hay := name
		if m.ignoreCase {
			hay = strings.ToLower(name)
		}
		if !strings.HasPrefix(hay, m.prefix) {
			return false
		}
	}
	if m.re != nil && !m.re.MatchString(name) {
		return false
	}
	return true
}

// FilterCompletion keeps items whose Label matches; returns items unchanged when
// the matcher is inactive.
func (m NameMatcher) FilterCompletion(items []CompletionItem) []CompletionItem {
	if !m.Active() {
		return items
	}
	out := items[:0]
	for _, it := range items {
		if m.Match(it.Label) {
			out = append(out, it)
		}
	}
	return out
}
