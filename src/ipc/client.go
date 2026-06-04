package ipc

import (
	"bufio"
	"encoding/json"
	"errors"
	"net"
	"time"
)

// ErrDaemonUnreachable is returned when no daemon is listening on the socket.
var ErrDaemonUnreachable = errors.New("daemon unreachable")

// RequestDaemon sends one op to the daemon and returns its single-line JSON
// reply. It opens a fresh connection per request — the daemon holds the warm
// state. A connect failure is reported as ErrDaemonUnreachable.
func RequestDaemon(socketPath string, req Request, timeout time.Duration) (Response, error) {
	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		return Response{}, ErrDaemonUnreachable
	}
	defer func() { _ = conn.Close() }()

	_ = conn.SetDeadline(time.Now().Add(timeout))

	payload, err := json.Marshal(req)
	if err != nil {
		return Response{}, err
	}
	if _, err := conn.Write(append(payload, '\n')); err != nil {
		return Response{}, err
	}

	line, err := bufio.NewReader(conn).ReadBytes('\n')
	if err != nil && len(line) == 0 {
		return Response{}, err
	}
	var resp Response
	if err := json.Unmarshal(trimRight(line), &resp); err != nil {
		return Response{}, err
	}
	return resp, nil
}

func trimRight(b []byte) []byte {
	for len(b) > 0 && (b[len(b)-1] == '\n' || b[len(b)-1] == '\r') {
		b = b[:len(b)-1]
	}
	return b
}
