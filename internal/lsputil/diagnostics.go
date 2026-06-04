package lsputil

import "encoding/json"

// A `code` in a diagnostic may be a string or a number. RawCode preserves
// whichever the server sent so it round-trips through idit's output unchanged.
type RawCode = json.RawMessage

// Diagnostic is one problem as published by the server (LSP shape).
type Diagnostic struct {
	Range    Range           `json:"range"`
	Severity int             `json:"severity,omitempty"` // 1=Error 2=Warning 3=Info 4=Hint
	Code     json.RawMessage `json:"code,omitempty"`
	Source   string          `json:"source,omitempty"`
	Message  string          `json:"message"`
}

// CheckDiagnostic is a diagnostic in idit's 1-based form.
type CheckDiagnostic struct {
	Line     int             `json:"line"`
	Col      int             `json:"col"`
	EndLine  int             `json:"endLine"`
	EndCol   int             `json:"endCol"`
	Severity string          `json:"severity"`
	Code     json.RawMessage `json:"code,omitempty"`
	Source   string          `json:"source,omitempty"`
	Message  string          `json:"message"`
}

// CodeString renders a diagnostic code for human output: a string code without
// its JSON quotes, a numeric code as-is, and "" when absent.
func (d CheckDiagnostic) CodeString() string {
	if len(d.Code) == 0 {
		return ""
	}
	var s string
	if json.Unmarshal(d.Code, &s) == nil {
		return s
	}
	return string(d.Code)
}

var severityName = map[int]string{1: "error", 2: "warning", 3: "info", 4: "hint"}

// NormalizeDiagnostics converts LSP diagnostics into idit's 1-based form.
func NormalizeDiagnostics(diags []Diagnostic) []CheckDiagnostic {
	out := make([]CheckDiagnostic, len(diags))
	for i, d := range diags {
		sev := severityName[d.Severity]
		if sev == "" {
			sev = "error"
		}
		out[i] = CheckDiagnostic{
			Line:     d.Range.Start.Line + 1,
			Col:      d.Range.Start.Character + 1,
			EndLine:  d.Range.End.Line + 1,
			EndCol:   d.Range.End.Character + 1,
			Severity: sev,
			Code:     d.Code,
			Source:   d.Source,
			Message:  d.Message,
		}
	}
	return out
}
