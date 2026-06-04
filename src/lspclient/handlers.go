package lspclient

import (
	"bufio"
	"encoding/json"
	"io"
)

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
		_ = json.Unmarshal(params, &p)
		out := make([]any, len(p.Items))
		return out, nil
	case "workspace/applyEdit":
		return map[string]any{"applied": false}, nil
	default:
		return nil, nil
	}
}
