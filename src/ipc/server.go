package ipc

import (
	"bufio"
	"encoding/json"
	"net"
	"strings"
)

// Handler processes one decoded request and returns its response.
type Handler func(req Request) Response

// Server is a running Unix-socket IPC listener.
type Server struct {
	ln net.Listener
}

// Serve starts accepting connections on ln. For each newline-delimited JSON
// request it calls onActivity (so the daemon can re-arm its idle timer) and then
// handler, writing the response back as one JSON line. Accept runs in a
// background goroutine; Serve returns immediately.
func Serve(ln net.Listener, handler Handler, onActivity func()) *Server {
	s := &Server{ln: ln}
	go s.acceptLoop(handler, onActivity)
	return s
}

// Stop closes the listener.
func (s *Server) Stop() {
	_ = s.ln.Close()
}

func (s *Server) acceptLoop(handler Handler, onActivity func()) {
	for {
		conn, err := s.ln.Accept()
		if err != nil {
			return
		}
		go handleConn(conn, handler, onActivity)
	}
}

func handleConn(conn net.Conn, handler Handler, onActivity func()) {
	defer func() { _ = conn.Close() }()
	scanner := bufio.NewScanner(conn)
	scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.TrimSpace(line) == "" {
			continue
		}
		if onActivity != nil {
			onActivity()
		}
		var resp Response
		var req Request
		if err := json.Unmarshal([]byte(line), &req); err != nil {
			resp = Errorf("%v", err)
		} else {
			resp = handler(req)
		}
		out, _ := json.Marshal(resp)
		_, _ = conn.Write(append(out, '\n'))
	}
}
