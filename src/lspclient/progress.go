package lspclient

import (
	"encoding/json"
	"sync"
	"time"
)

// ProgressTracker tracks `$/progress` work-done tokens so the daemon can wait
// for the language server to finish background work (notably project loading)
// before issuing a positional query. Without this, the first query after opening
// a file races ahead of project load and returns partial results.
type ProgressTracker struct {
	mu           sync.Mutex
	pending      map[string]struct{}
	idleWaiters  []chan struct{}
	beginWaiters []chan struct{}
	loadedOnce   bool // true once at least one progress cycle has completed
}

// NewProgressTracker returns a ready tracker.
func NewProgressTracker() *ProgressTracker {
	return &ProgressTracker{pending: make(map[string]struct{})}
}

// Handle feeds it every notification; it ignores all but `$/progress`.
func (p *ProgressTracker) Handle(method string, params json.RawMessage) {
	if method != "$/progress" {
		return
	}
	var pp struct {
		Token json.RawMessage `json:"token"`
		Value struct {
			Kind string `json:"kind"`
		} `json:"value"`
	}
	if json.Unmarshal(params, &pp) != nil || len(pp.Token) == 0 {
		return
	}
	key := string(pp.Token)

	p.mu.Lock()
	defer p.mu.Unlock()
	switch pp.Value.Kind {
	case "begin":
		p.pending[key] = struct{}{}
		flush(&p.beginWaiters)
	case "end":
		delete(p.pending, key)
		if len(p.pending) == 0 {
			p.loadedOnce = true
			flush(&p.idleWaiters)
		}
	}
}

// Settle resolves once the server is done with any background work that a
// just-issued didOpen may have triggered. If work is already running, wait it
// out. If not, wait briefly for it to start — then wait it out — otherwise give
// up.
func (p *ProgressTracker) Settle(maxWait time.Duration) {
	p.mu.Lock()
	if len(p.pending) > 0 {
		p.mu.Unlock()
		p.awaitIdle(maxWait)
		return
	}
	grace := 1500 * time.Millisecond
	if p.loadedOnce {
		grace = 200 * time.Millisecond
	}
	begin := make(chan struct{}, 1)
	p.beginWaiters = append(p.beginWaiters, begin)
	p.mu.Unlock()

	select {
	case <-begin:
		p.awaitIdle(maxWait)
	case <-time.After(grace):
		// Work never started; nothing to wait for.
	}
}

func (p *ProgressTracker) awaitIdle(timeout time.Duration) {
	p.mu.Lock()
	if len(p.pending) == 0 {
		p.mu.Unlock()
		return
	}
	idle := make(chan struct{}, 1)
	p.idleWaiters = append(p.idleWaiters, idle)
	p.mu.Unlock()

	select {
	case <-idle:
	case <-time.After(timeout):
	}
}

// flush closes and clears all waiters under the caller's held lock, so each
// channel is closed exactly once.
func flush(waiters *[]chan struct{}) {
	for _, w := range *waiters {
		close(w)
	}
	*waiters = nil
}
