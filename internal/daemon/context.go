// Package daemon is the long-lived per-workspace+language process: it owns the
// language-server subprocess via lspclient, holds warm document state, and
// serves CLI requests over the Unix socket.
package daemon

import (
	"sync"

	"github.com/srossross/clidit/internal/lspclient"
	"github.com/srossross/clidit/internal/workspace"
)

// openDoc is what we last sent the server for an open document, for change
// detection.
type openDoc struct {
	version int
	text    string
}

// Context is the shared state the op handlers operate on.
type Context struct {
	Server       workspace.ServerConfig
	Root         string
	SocketPath   string
	Lsp          *lspclient.Client
	Capabilities map[string]any
	Progress     *lspclient.ProgressTracker
	Diagnostics  *lspclient.DiagnosticsTracker
	Shutdown     func(code int)

	mu     sync.Mutex
	opened map[string]openDoc // keyed by file URI
}

func (c *Context) getOpen(uri string) (openDoc, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	d, ok := c.opened[uri]
	return d, ok
}

func (c *Context) setOpen(uri string, d openDoc) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.opened[uri] = d
}

func (c *Context) deleteOpen(uri string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.opened, uri)
}

func (c *Context) hasOpen(uri string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	_, ok := c.opened[uri]
	return ok
}
