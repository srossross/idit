// Package lspclient owns a spawned language-server subprocess: it frames
// JSON-RPC over the process's stdio, correlates responses to requests, answers
// the handful of requests servers make during startup, and tracks the
// background-work + diagnostics state the daemon waits on.
package lspclient

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"sync"

	"github.com/srossross/clidit/internal/lsputil"
)

// RpcError is a JSON-RPC error object.
type RpcError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func (e *RpcError) Error() string {
	if e.Code != 0 {
		return fmt.Sprintf("%s (code %d)", e.Message, e.Code)
	}
	return e.Message
}

// ServerRequestHandler answers a server→client request; its result is sent back.
type ServerRequestHandler func(method string, params json.RawMessage) (any, *RpcError)

// NotificationHandler observes a server→client notification.
type NotificationHandler func(method string, params json.RawMessage)

// Options configures a Client.
type Options struct {
	Cmd             []string
	Cwd             string
	Env             []string
	OnServerRequest ServerRequestHandler
	OnNotification  NotificationHandler
	OnStderr        func(line string)
}

type rpcMessage struct {
	JSONRPC string           `json:"jsonrpc"`
	ID      *json.RawMessage `json:"id,omitempty"`
	Method  string           `json:"method,omitempty"`
	Params  json.RawMessage  `json:"params,omitempty"`
	Result  json.RawMessage  `json:"result,omitempty"`
	Error   *RpcError        `json:"error,omitempty"`
}

type rpcResult struct {
	result json.RawMessage
	err    *RpcError
}

// Client is a thin LSP client over a spawned language-server subprocess.
type Client struct {
	cmd     *exec.Cmd
	stdin   io.WriteCloser
	writeMu sync.Mutex
	dec     lsputil.MessageDecoder

	mu               sync.Mutex
	pending          map[int64]chan rpcResult
	nextID           int64
	applyEditWaiters []chan json.RawMessage
	closed           bool

	onServerRequest ServerRequestHandler
	onNotification  NotificationHandler

	exited   chan struct{}
	exitCode int
}

// New spawns the language server and starts the I/O pumps.
func New(opts Options) (*Client, error) {
	cmd := exec.Command(opts.Cmd[0], opts.Cmd[1:]...)
	cmd.Dir = opts.Cwd
	cmd.Env = opts.Env

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}

	c := newClient(opts, stdin, stdout, stderr)
	c.cmd = cmd
	go c.cmd.Wait()
	c.start(stdout, stderr, opts.OnStderr)
	return c, nil
}

// newClient builds a Client around already-open streams. New wires in exec
// pipes; tests wire in io.Pipe to exercise correlation without a subprocess.
func newClient(opts Options, stdin io.WriteCloser, stdout, stderr io.Reader) *Client {
	srh := opts.OnServerRequest
	if srh == nil {
		srh = defaultServerRequestHandler
	}
	return &Client{
		stdin:           stdin,
		pending:         make(map[int64]chan rpcResult),
		nextID:          1,
		onServerRequest: srh,
		onNotification:  opts.OnNotification,
		exited:          make(chan struct{}),
	}
}

func (c *Client) start(stdout, stderr io.Reader, onStderr func(string)) {
	go c.pumpStdout(stdout)
	if onStderr != nil {
		go pumpStderr(stderr, onStderr)
	} else if stderr != nil {
		go io.Copy(io.Discard, stderr)
	}
}

// Request sends a request and blocks for its result (or error).
func (c *Client) Request(method string, params any) (json.RawMessage, error) {
	return c.RequestCtx(context.Background(), method, params)
}

// RequestCtx is Request bounded by ctx; if ctx is cancelled first it returns
// ctx.Err() and stops tracking the call.
func (c *Client) RequestCtx(ctx context.Context, method string, params any) (json.RawMessage, error) {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil, &RpcError{Code: -1, Message: "language server closed the connection"}
	}
	id := c.nextID
	c.nextID++
	ch := make(chan rpcResult, 1)
	c.pending[id] = ch
	c.mu.Unlock()

	idRaw := json.RawMessage(fmt.Sprintf("%d", id))
	if err := c.send(rpcMessage{JSONRPC: "2.0", ID: &idRaw, Method: method, Params: mustRaw(params)}); err != nil {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return nil, err
	}

	select {
	case res := <-ch:
		if res.err != nil {
			return nil, res.err
		}
		return res.result, nil
	case <-ctx.Done():
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return nil, ctx.Err()
	}
}

// Notify sends a fire-and-forget notification.
func (c *Client) Notify(method string, params any) {
	c.send(rpcMessage{JSONRPC: "2.0", Method: method, Params: mustRaw(params)})
}

// NextApplyEdit returns a channel that receives the `edit` of the next
// server→client workspace/applyEdit. Some servers (typescript-language-server)
// deliver refactor edits this way, in response to workspace/executeCommand.
func (c *Client) NextApplyEdit() <-chan json.RawMessage {
	ch := make(chan json.RawMessage, 1)
	c.mu.Lock()
	c.applyEditWaiters = append(c.applyEditWaiters, ch)
	c.mu.Unlock()
	return ch
}

// Exited is closed when the server's stdout ends (the process is gone).
func (c *Client) Exited() <-chan struct{} { return c.exited }

// ExitCode is valid after Exited is closed.
func (c *Client) ExitCode() int { return c.exitCode }

// Kill terminates the language server process.
func (c *Client) Kill() {
	if c.cmd.Process != nil {
		c.cmd.Process.Kill()
	}
}

func (c *Client) send(msg rpcMessage) error {
	frame, err := lsputil.EncodeMessage(msg)
	if err != nil {
		return err
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	_, err = c.stdin.Write(frame)
	return err
}

func (c *Client) pumpStdout(stdout io.Reader) {
	buf := make([]byte, 64*1024)
	for {
		n, err := stdout.Read(buf)
		if n > 0 {
			for _, body := range c.dec.Push(buf[:n]) {
				var msg rpcMessage
				if json.Unmarshal(body, &msg) == nil {
					c.dispatch(msg)
				}
			}
		}
		if err != nil {
			break
		}
	}

	// Server closed its output: fail anything still waiting.
	c.mu.Lock()
	c.closed = true
	pending := c.pending
	c.pending = make(map[int64]chan rpcResult)
	waiters := c.applyEditWaiters
	c.applyEditWaiters = nil
	c.mu.Unlock()

	for _, ch := range pending {
		ch <- rpcResult{err: &RpcError{Code: -1, Message: "language server closed the connection"}}
	}
	for _, w := range waiters {
		close(w)
	}
	if c.cmd != nil && c.cmd.ProcessState != nil {
		c.exitCode = c.cmd.ProcessState.ExitCode()
	}
	close(c.exited)
}

func (c *Client) dispatch(msg rpcMessage) {
	hasID := msg.ID != nil && string(*msg.ID) != "null"
	hasMethod := msg.Method != ""

	switch {
	case hasID && !hasMethod:
		// Response to one of our requests.
		var id int64
		if json.Unmarshal(*msg.ID, &id) != nil {
			return
		}
		c.mu.Lock()
		ch, ok := c.pending[id]
		delete(c.pending, id)
		c.mu.Unlock()
		if !ok {
			return
		}
		ch <- rpcResult{result: msg.Result, err: msg.Error}

	case hasID && hasMethod:
		// A refactor edit arriving via applyEdit, with someone waiting for it:
		// hand over the edit and tell the server we applied it.
		if msg.Method == "workspace/applyEdit" {
			c.mu.Lock()
			var waiter chan json.RawMessage
			if len(c.applyEditWaiters) > 0 {
				waiter = c.applyEditWaiters[0]
				c.applyEditWaiters = c.applyEditWaiters[1:]
			}
			c.mu.Unlock()
			if waiter != nil {
				var p struct {
					Edit json.RawMessage `json:"edit"`
				}
				json.Unmarshal(msg.Params, &p)
				waiter <- p.Edit
				c.replyResult(msg.ID, map[string]any{"applied": true})
				return
			}
		}
		// Server→client request: must reply.
		result, rpcErr := c.onServerRequest(msg.Method, msg.Params)
		if rpcErr != nil {
			c.replyError(msg.ID, rpcErr)
		} else {
			c.replyResult(msg.ID, result)
		}

	case hasMethod:
		// Notification.
		if c.onNotification != nil {
			c.onNotification(msg.Method, msg.Params)
		}
	}
}

func (c *Client) replyResult(id *json.RawMessage, result any) {
	raw := mustRaw(result)
	if raw == nil {
		// A reply must carry a result (or error); omitempty would drop a nil one,
		// producing a message the server rejects. Send explicit JSON null.
		raw = json.RawMessage("null")
	}
	c.send(rpcMessage{JSONRPC: "2.0", ID: id, Result: raw})
}

func (c *Client) replyError(id *json.RawMessage, rpcErr *RpcError) {
	c.send(rpcMessage{JSONRPC: "2.0", ID: id, Error: rpcErr})
}

func pumpStderr(stderr io.Reader, onLine func(string)) {
	r := bufio.NewReader(stderr)
	for {
		line, err := r.ReadString('\n')
		if len(line) > 0 {
			onLine(trimNewline(line))
		}
		if err != nil {
			return
		}
	}
}

func trimNewline(s string) string {
	for len(s) > 0 && (s[len(s)-1] == '\n' || s[len(s)-1] == '\r') {
		s = s[:len(s)-1]
	}
	return s
}

func mustRaw(v any) json.RawMessage {
	if v == nil {
		return nil
	}
	if raw, ok := v.(json.RawMessage); ok {
		return raw
	}
	b, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	return b
}

// defaultServerRequestHandler gives minimal answers to the requests a server
// makes while starting up, so the handshake doesn't stall waiting on us.
func defaultServerRequestHandler(method string, params json.RawMessage) (any, *RpcError) {
	switch method {
	case "client/registerCapability", "client/unregisterCapability", "window/workDoneProgress/create":
		return nil, nil
	case "workspace/configuration":
		// Reply with one entry per requested item; null = "use your defaults".
		var p struct {
			Items []json.RawMessage `json:"items"`
		}
		json.Unmarshal(params, &p)
		out := make([]any, len(p.Items))
		return out, nil
	case "workspace/applyEdit":
		return map[string]any{"applied": false}, nil
	default:
		return nil, nil
	}
}
