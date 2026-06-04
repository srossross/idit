package cli

import (
	"slices"
	"sort"
	"testing"
)

func TestResolveKind(t *testing.T) {
	tests := []struct {
		in   string
		want []string
	}{
		{"func", []string{"function"}},
		{"fn", []string{"function"}},
		{"FUNC", []string{"function"}},
		{"  func  ", []string{"function"}},
		{"type", []string{"class", "interface", "struct", "type-param"}},
		{"class", []string{"class"}},
		{"function", []string{"function"}},
		{"var", []string{"variable"}},
		{"const", []string{"constant"}},
	}
	for _, tt := range tests {
		got, err := resolveKind(tt.in)
		if err != nil {
			t.Errorf("resolveKind(%q) unexpected error: %v", tt.in, err)
			continue
		}
		names := make([]string, 0, len(got))
		for n := range got {
			names = append(names, n)
		}
		sort.Strings(names)
		if !slices.Equal(names, tt.want) {
			t.Errorf("resolveKind(%q) = %v, want %v", tt.in, names, tt.want)
		}
	}
}

func TestResolveKindUnknown(t *testing.T) {
	for _, in := range []string{"bogus", "", "funcs"} {
		if _, err := resolveKind(in); err == nil {
			t.Errorf("resolveKind(%q) expected error, got nil", in)
		}
	}
}
