package lspclient

import (
	"encoding/json"
	"testing"
	"time"
)

func progress(token, kind string) (string, json.RawMessage) {
	p, _ := json.Marshal(map[string]any{"token": token, "value": map[string]any{"kind": kind}})
	return "$/progress", p
}

func TestProgressSettleWaitsForRunningWork(t *testing.T) {
	p := NewProgressTracker()
	p.Handle(progress("t1", "begin"))

	done := make(chan struct{})
	go func() {
		p.Settle(2 * time.Second)
		close(done)
	}()

	select {
	case <-done:
		t.Fatal("Settle returned while work still pending")
	case <-time.After(50 * time.Millisecond):
	}

	p.Handle(progress("t1", "end"))
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Settle did not return after work ended")
	}
}

func TestProgressSettleNoWorkColdGrace(t *testing.T) {
	p := NewProgressTracker()
	start := time.Now()
	p.Settle(2 * time.Second) // nothing pending, never loaded → ~1500ms grace
	elapsed := time.Since(start)
	if elapsed < 1400*time.Millisecond {
		t.Fatalf("cold grace too short: %v", elapsed)
	}
}

func TestProgressSettleWarmGraceShorter(t *testing.T) {
	p := NewProgressTracker()
	// Complete one cycle so loadedOnce becomes true.
	p.Handle(progress("a", "begin"))
	p.Handle(progress("a", "end"))

	start := time.Now()
	p.Settle(2 * time.Second) // warm → ~200ms grace
	elapsed := time.Since(start)
	if elapsed > 800*time.Millisecond {
		t.Fatalf("warm grace too long: %v", elapsed)
	}
}

func TestProgressSettleWorkStartsDuringGrace(t *testing.T) {
	p := NewProgressTracker()
	done := make(chan struct{})
	go func() {
		p.Settle(2 * time.Second)
		close(done)
	}()
	// Work begins shortly after Settle starts waiting (within cold grace).
	time.Sleep(100 * time.Millisecond)
	p.Handle(progress("x", "begin"))
	time.Sleep(50 * time.Millisecond)

	select {
	case <-done:
		t.Fatal("Settle returned before work ended")
	case <-time.After(50 * time.Millisecond):
	}
	p.Handle(progress("x", "end"))
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Settle did not return after work ended")
	}
}

func publish(uri string, n int) (string, json.RawMessage) {
	diags := make([]map[string]any, n)
	for i := range diags {
		diags[i] = map[string]any{
			"range":   map[string]any{"start": map[string]int{"line": 0, "character": 0}, "end": map[string]int{"line": 0, "character": 1}},
			"message": "x",
		}
	}
	p, _ := json.Marshal(map[string]any{"uri": uri, "diagnostics": diags})
	return "textDocument/publishDiagnostics", p
}

func TestDiagnosticsCollectFromCache(t *testing.T) {
	d := NewDiagnosticsTracker()
	d.Handle(publish("file:///a.ts", 2))
	got := d.Collect("file:///a.ts", false, 20*time.Millisecond, time.Second)
	if len(got) != 2 {
		t.Fatalf("want 2 cached diagnostics, got %d", len(got))
	}
}

func TestDiagnosticsCollectRequireNewWaitsForPublish(t *testing.T) {
	d := NewDiagnosticsTracker()
	d.Handle(publish("file:///b.ts", 5)) // stale cache, should be ignored

	resCh := make(chan int, 1)
	go func() {
		got := d.Collect("file:///b.ts", true, 30*time.Millisecond, 2*time.Second)
		resCh <- len(got)
	}()

	// requireNew: must not settle from the stale cache.
	select {
	case <-resCh:
		t.Fatal("settled from stale cache despite requireNew")
	case <-time.After(60 * time.Millisecond):
	}

	d.Handle(publish("file:///b.ts", 1)) // fresh wave
	select {
	case n := <-resCh:
		if n != 1 {
			t.Fatalf("want 1 fresh diagnostic, got %d", n)
		}
	case <-time.After(time.Second):
		t.Fatal("did not settle after fresh publish")
	}
}

func TestDiagnosticsCollectHardCap(t *testing.T) {
	d := NewDiagnosticsTracker()
	start := time.Now()
	got := d.Collect("file:///never.ts", true, time.Second, 150*time.Millisecond)
	if time.Since(start) > 500*time.Millisecond {
		t.Fatal("hard cap did not fire")
	}
	if got != nil {
		t.Fatalf("expected nil for unknown uri, got %v", got)
	}
}
