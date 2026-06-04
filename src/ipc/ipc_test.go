package ipc

import (
	"net"
	"path/filepath"
	"testing"
	"time"
)

func TestRequestResponseOverSocket(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "t.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	var activity int
	srv := Serve(ln, func(req Request) Response {
		if req.Op == "ping" {
			return Response{OK: true, Pong: true}
		}
		return Errorf("unknown op: %s", req.Op)
	}, func() { activity++ })
	defer srv.Stop()

	resp, err := RequestDaemon(sock, Request{Op: "ping"}, time.Second)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	if !resp.OK || !resp.Pong {
		t.Fatalf("bad response: %+v", resp)
	}

	resp, err = RequestDaemon(sock, Request{Op: "bogus"}, time.Second)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	if resp.OK || resp.Error != "unknown op: bogus" {
		t.Fatalf("want unknown-op error, got %+v", resp)
	}
	if activity < 2 {
		t.Fatalf("onActivity should fire per request, got %d", activity)
	}
}

func TestRequestUnreachable(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "nope.sock")
	_, err := RequestDaemon(sock, Request{Op: "ping"}, time.Second)
	if err != ErrDaemonUnreachable {
		t.Fatalf("want ErrDaemonUnreachable, got %v", err)
	}
}
