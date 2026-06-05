package lspclient

import (
	"encoding/json"
	"sync"
	"time"

	"github.com/srossross/idit/src/lsputil"
)

// DiagnosticsTracker tracks `textDocument/publishDiagnostics` notifications,
// which the server pushes (unsolicited) whenever it analyzes an open file, often
// in waves (syntactic, then semantic). It keeps the latest set per file and lets
// a caller wait for the waves to settle.
type DiagnosticsTracker struct {
	mu          sync.Mutex
	latest      map[string][]lsputil.Diagnostic
	subscribers map[int]func(uri string)
	nextSubID   int
}

// NewDiagnosticsTracker returns a ready tracker.
func NewDiagnosticsTracker() *DiagnosticsTracker {
	return &DiagnosticsTracker{
		latest:      make(map[string][]lsputil.Diagnostic),
		subscribers: make(map[int]func(string)),
	}
}

// Handle feeds it every notification; it ignores all but publishDiagnostics.
func (d *DiagnosticsTracker) Handle(method string, params json.RawMessage) {
	if method != "textDocument/publishDiagnostics" {
		return
	}
	var p struct {
		URI         string               `json:"uri"`
		Diagnostics []lsputil.Diagnostic `json:"diagnostics"`
	}
	if json.Unmarshal(params, &p) != nil || p.URI == "" {
		return
	}
	d.mu.Lock()
	d.latest[p.URI] = p.Diagnostics
	subs := make([]func(string), 0, len(d.subscribers))
	for _, sub := range d.subscribers {
		subs = append(subs, sub)
	}
	d.mu.Unlock()
	for _, sub := range subs {
		sub(p.URI)
	}
}

// Collect resolves with the diagnostics for uri once they've been quiet for
// debounce. When requireNew is set, ignore any cached set and wait for the next
// publish first — used right after open/edit, when fresh analysis is still
// incoming and the cached set would be stale. It never waits longer than maxWait.
func (d *DiagnosticsTracker) Collect(uri string, requireNew bool, debounce, maxWait time.Duration) []lsputil.Diagnostic {
	done := make(chan []lsputil.Diagnostic, 1)

	var (
		timerMu   sync.Mutex
		debounceT *time.Timer
		once      sync.Once
		subID     int
	)

	finish := func() {
		once.Do(func() {
			timerMu.Lock()
			if debounceT != nil {
				debounceT.Stop()
			}
			timerMu.Unlock()
			d.mu.Lock()
			delete(d.subscribers, subID)
			diags := d.latest[uri]
			d.mu.Unlock()
			done <- diags
		})
	}

	arm := func() {
		timerMu.Lock()
		if debounceT != nil {
			debounceT.Stop()
		}
		debounceT = time.AfterFunc(debounce, finish)
		timerMu.Unlock()
	}

	d.mu.Lock()
	subID = d.nextSubID
	d.nextSubID++
	d.subscribers[subID] = func(u string) {
		if u == uri {
			arm()
		}
	}
	_, haveCached := d.latest[uri]
	d.mu.Unlock()

	hard := time.AfterFunc(maxWait, finish)
	defer hard.Stop()

	// Warm + unchanged: settle from the cached set. Otherwise wait for the first
	// incoming publish before starting the debounce.
	if !requireNew && haveCached {
		arm()
	}

	return <-done
}
