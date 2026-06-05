package cli

import (
	"fmt"
	"strconv"
	"strings"
)

// Locator is a parsed `path:line:col` target.
type Locator struct {
	File string
	Line int
	Col  int
}

// RangeLocator is a parsed `file:startLine:startCol-endLine:endCol`.
type RangeLocator struct {
	File      string
	StartLine int
	StartCol  int
	EndLine   int
	EndCol    int
}

// parseLocator parses `path:line:col` (col optional, defaults to 1). The path may
// contain colons.
func parseLocator(arg string) (Locator, error) {
	parts := strings.Split(arg, ":")
	if len(parts) < 2 {
		return Locator{}, fmt.Errorf("expected file:line[:col], got: %s", arg)
	}

	col := 1
	var line int
	last, lastOK := atoi(parts[len(parts)-1])
	var secondLast int
	var secondOK bool
	if len(parts) >= 2 {
		secondLast, secondOK = atoi(parts[len(parts)-2])
	}

	switch {
	case len(parts) >= 3 && lastOK && secondOK:
		col = last
		line = secondLast
		parts = parts[:len(parts)-2]
	case lastOK:
		line = last
		parts = parts[:len(parts)-1]
	default:
		return Locator{}, fmt.Errorf("expected file:line[:col], got: %s", arg)
	}

	file := strings.Join(parts, ":")
	if file == "" {
		return Locator{}, fmt.Errorf("missing file in: %s", arg)
	}
	return Locator{File: file, Line: line, Col: col}, nil
}

// parseRange parses `file:startLine:startCol-endLine:endCol`. The path may
// contain dashes.
func parseRange(arg string) (RangeLocator, error) {
	dash := strings.LastIndexByte(arg, '-')
	if dash == -1 {
		return RangeLocator{}, fmt.Errorf("expected file:line:col-line:col, got: %s", arg)
	}
	endLine, endCol, ok := parseLineCol(arg[dash+1:])
	if !ok {
		return RangeLocator{}, fmt.Errorf("expected file:line:col-line:col, got: %s", arg)
	}
	start, err := parseLocator(arg[:dash])
	if err != nil {
		return RangeLocator{}, fmt.Errorf("expected file:line:col-line:col, got: %s", arg)
	}
	return RangeLocator{
		File:      start.File,
		StartLine: start.Line,
		StartCol:  start.Col,
		EndLine:   endLine,
		EndCol:    endCol,
	}, nil
}

// parseLineCol parses exactly `line:col` (both integers).
func parseLineCol(s string) (line, col int, ok bool) {
	parts := strings.Split(s, ":")
	if len(parts) != 2 {
		return 0, 0, false
	}
	l, ok1 := atoi(parts[0])
	c, ok2 := atoi(parts[1])
	if !ok1 || !ok2 {
		return 0, 0, false
	}
	return l, c, true
}

// stripPosition reduces a whole-file target to just its path: it drops a
// `#symbol.path` suffix and/or a trailing `:line[:col]`, so file commands accept
// the same target forms as the positional ones.
func stripPosition(arg string) string {
	if before, _, found := strings.Cut(arg, "#"); found {
		return before
	}
	parts := strings.Split(arg, ":")
	for len(parts) > 1 {
		if _, ok := atoi(parts[len(parts)-1]); !ok {
			break
		}
		parts = parts[:len(parts)-1]
	}
	return strings.Join(parts, ":")
}

func atoi(s string) (int, bool) {
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0, false
	}
	return n, true
}
