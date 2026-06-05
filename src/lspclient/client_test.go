package lspclient

import (
	"encoding/json"
	"io"
	"testing"
	"time"

	"github.com/srossross/clidit/src/lsputil"
)

func TestDefaultConfigurationReturnsSettings(t *testing.T) {
	settings := map[string]any{"python": map[string]any{"pythonPath": "/v/bin/python"}}
	params := json.RawMessage(`{"items":[{"section":"python"},{"section":"basedpyright"}]}`)
	res, rpcErr := defaultServerRequestHandler(settings, "workspace/configuration", params)
	if rpcErr != nil {
		t.Fatalf("unexpected rpc error: %v", rpcErr)
	}
	arr, ok := res.([]any)
	if !ok || len(arr) != 2 {
		t.Fatalf("want 2-element array, got %#v", res)
	}
	// Configured section returns its value; unknown section returns null.
	py, ok := arr[0].(map[string]any)
	if !ok || py["pythonPath"] != "/v/bin/python" {
		t.Fatalf("python section wrong: %#v", arr[0])
	}
	if arr[1] != nil {
		t.Fatalf("unknown section should be null, got %#v", arr[1])
	}
}

// fakeServer wires a Client to a scripted peer over two io.Pipes, returning the
// client plus a channel of messages the client sent and a function to push a
// raw rpcMessage from the "server" back to the client.
type fakeConn struct {
	c     *Client
	sent  <-chan rpcMessage
	push  func(rpcMessage)
	close func() // closes the server's output, triggering the client close path
}

func fakeServer(t *testing.T, opts Options) (*Client, <-chan rpcMessage, func(rpcMessage)) {
	fc := fakeServerConn(t, opts)
	return fc.c, fc.sent, fc.push
}

func fakeServerConn(t *testing.T, opts Options) fakeConn {
	t.Helper()
	clientStdinR, clientStdinW := io.Pipe() // client writes here; server reads
	serverOutR, serverOutW := io.Pipe()     // server writes here; client reads

	c := newClient(opts, clientStdinW, serverOutR, nil)
	c.start(serverOutR, nil, nil)

	sent := make(chan rpcMessage, 16)
	go func() {
		var dec lsputil.MessageDecoder
		buf := make([]byte, 4096)
		for {
			n, err := clientStdinR.Read(buf)
			for _, body := range dec.Push(buf[:n]) {
				var m rpcMessage
				_ = json.Unmarshal(body, &m)
				sent <- m
			}
			if err != nil {
				return
			}
		}
	}()

	push := func(m rpcMessage) {
		frame, _ := lsputil.EncodeMessage(m)
		_, _ = serverOutW.Write(frame)
	}
	return fakeConn{c: c, sent: sent, push: push, close: func() { _ = serverOutW.Close() }}
}

func raw(v any) *json.RawMessage {
	b, _ := json.Marshal(v)
	r := json.RawMessage(b)
	return &r
}

func TestRequestResponseCorrelation(t *testing.T) {
	c, sent, push := fakeServer(t, Options{})

	resultCh := make(chan json.RawMessage, 1)
	go func() {
		res, err := c.Request("textDocument/definition", map[string]any{"x": 1})
		if err != nil {
			t.Errorf("Request err: %v", err)
		}
		resultCh <- res
	}()

	// Observe what the client sent, reply with a matching id.
	msg := <-sent
	if msg.Method != "textDocument/definition" || msg.ID == nil {
		t.Fatalf("unexpected sent message: %+v", msg)
	}
	push(rpcMessage{JSONRPC: "2.0", ID: msg.ID, Result: json.RawMessage(`{"ok":true}`)})

	select {
	case res := <-resultCh:
		if string(res) != `{"ok":true}` {
			t.Fatalf("bad result: %s", res)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for result")
	}
}

func TestRequestErrorPropagates(t *testing.T) {
	c, sent, push := fakeServer(t, Options{})
	errCh := make(chan error, 1)
	go func() {
		_, err := c.Request("x", nil)
		errCh <- err
	}()
	msg := <-sent
	push(rpcMessage{JSONRPC: "2.0", ID: msg.ID, Error: &RpcError{Code: 42, Message: "boom"}})
	err := <-errCh
	re, ok := err.(*RpcError)
	if !ok || re.Code != 42 {
		t.Fatalf("want RpcError code 42, got %v", err)
	}
}

func TestNotifyHasNoID(t *testing.T) {
	c, sent, _ := fakeServer(t, Options{})
	c.Notify("initialized", map[string]any{})
	msg := <-sent
	if msg.ID != nil || msg.Method != "initialized" {
		t.Fatalf("notify should have no id: %+v", msg)
	}
}

func TestServerRequestAnswered(t *testing.T) {
	var gotMethod string
	_, sent, push := fakeServer(t, Options{
		OnServerRequest: func(method string, params json.RawMessage) (any, *RpcError) {
			gotMethod = method
			return map[string]any{"answered": true}, nil
		},
	})
	// Server sends a request to the client.
	push(rpcMessage{JSONRPC: "2.0", ID: raw(99), Method: "custom/ask", Params: json.RawMessage(`{}`)})
	reply := <-sent
	if gotMethod != "custom/ask" {
		t.Fatalf("handler not called, got %q", gotMethod)
	}
	if reply.ID == nil || string(*reply.ID) != "99" {
		t.Fatalf("reply id mismatch: %+v", reply)
	}
	if string(reply.Result) != `{"answered":true}` {
		t.Fatalf("reply result: %s", reply.Result)
	}
}

func TestApplyEditHandoff(t *testing.T) {
	c, sent, push := fakeServer(t, Options{})
	w := c.NextApplyEdit()

	push(rpcMessage{
		JSONRPC: "2.0", ID: raw(7), Method: "workspace/applyEdit",
		Params: json.RawMessage(`{"edit":{"changes":{"file:///a.ts":[]}}}`),
	})

	select {
	case edit := <-w:
		if string(edit) != `{"changes":{"file:///a.ts":[]}}` {
			t.Fatalf("bad edit handed off: %s", edit)
		}
	case <-time.After(time.Second):
		t.Fatal("no edit handed off")
	}
	// Client must reply {applied:true}.
	reply := <-sent
	if string(reply.Result) != `{"applied":true}` {
		t.Fatalf("apply reply: %s", reply.Result)
	}
}

func TestDefaultWorkspaceConfigurationReply(t *testing.T) {
	_, sent, push := fakeServer(t, Options{})
	push(rpcMessage{
		JSONRPC: "2.0", ID: raw(3), Method: "workspace/configuration",
		Params: json.RawMessage(`{"items":[{"section":"a"},{"section":"b"}]}`),
	})
	reply := <-sent
	if string(reply.Result) != `[null,null]` {
		t.Fatalf("want [null,null], got %s", reply.Result)
	}
}

func TestRejectPendingOnClose(t *testing.T) {
	fc := fakeServerConn(t, Options{})
	errCh := make(chan error, 1)
	go func() {
		_, err := fc.c.Request("hang", nil)
		errCh <- err
	}()
	<-fc.sent  // ensure the request was sent and is pending
	fc.close() // server closes its output → client should reject the pending call

	select {
	case err := <-errCh:
		re, ok := err.(*RpcError)
		if !ok || re.Code != -1 {
			t.Fatalf("want close RpcError, got %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("pending request not rejected on close")
	}

	select {
	case <-fc.c.Exited():
	case <-time.After(time.Second):
		t.Fatal("Exited not closed")
	}
}
