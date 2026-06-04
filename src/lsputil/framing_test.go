package lsputil

import (
	"encoding/json"
	"testing"
)

func frame(t *testing.T, msg any) []byte {
	t.Helper()
	b, err := EncodeMessage(msg)
	if err != nil {
		t.Fatalf("EncodeMessage: %v", err)
	}
	return b
}

func TestEncodeMessageRoundTrip(t *testing.T) {
	b := frame(t, map[string]any{"jsonrpc": "2.0", "id": 1, "method": "ping"})
	want := "Content-Length: 40\r\n\r\n{\"id\":1,\"jsonrpc\":\"2.0\",\"method\":\"ping\"}"
	if string(b) != want {
		t.Fatalf("got %q want %q", string(b), want)
	}
}

func TestDecoderSingleMessage(t *testing.T) {
	var d MessageDecoder
	msgs := d.Push(frame(t, map[string]any{"method": "a"}))
	if len(msgs) != 1 {
		t.Fatalf("want 1 message, got %d", len(msgs))
	}
	var m map[string]any
	if err := json.Unmarshal(msgs[0], &m); err != nil || m["method"] != "a" {
		t.Fatalf("bad body: %s err=%v", msgs[0], err)
	}
}

func TestDecoderTwoMessagesOneBuffer(t *testing.T) {
	var d MessageDecoder
	buf := append(frame(t, map[string]any{"method": "a"}), frame(t, map[string]any{"method": "b"})...)
	msgs := d.Push(buf)
	if len(msgs) != 2 {
		t.Fatalf("want 2 messages, got %d", len(msgs))
	}
}

func TestDecoderBodySplitAcrossReads(t *testing.T) {
	var d MessageDecoder
	full := frame(t, map[string]any{"method": "hello"})
	split := len(full) - 3
	if got := d.Push(full[:split]); len(got) != 0 {
		t.Fatalf("partial body should yield 0 messages, got %d", len(got))
	}
	got := d.Push(full[split:])
	if len(got) != 1 {
		t.Fatalf("want 1 message after completion, got %d", len(got))
	}
}

func TestDecoderHeaderSplitAcrossReads(t *testing.T) {
	var d MessageDecoder
	full := frame(t, map[string]any{"method": "x"})
	if got := d.Push(full[:8]); len(got) != 0 { // mid-header
		t.Fatalf("partial header should yield 0, got %d", len(got))
	}
	if got := d.Push(full[8:]); len(got) != 1 {
		t.Fatalf("want 1, got %d", len(got))
	}
}

func TestDecoderUnparseableHeaderResync(t *testing.T) {
	var d MessageDecoder
	junk := append([]byte("Garbage-Header: nope\r\n\r\n"), frame(t, map[string]any{"method": "ok"})...)
	msgs := d.Push(junk)
	if len(msgs) != 1 {
		t.Fatalf("want 1 message after resync, got %d", len(msgs))
	}
	var m map[string]any
	_ = json.Unmarshal(msgs[0], &m)
	if m["method"] != "ok" {
		t.Fatalf("resync got wrong message: %s", msgs[0])
	}
}

func TestDecoderByteByByte(t *testing.T) {
	var d MessageDecoder
	full := frame(t, map[string]any{"method": "drip", "n": 7})
	var count int
	for i := range full {
		count += len(d.Push(full[i : i+1]))
	}
	if count != 1 {
		t.Fatalf("byte-by-byte want 1 message, got %d", count)
	}
}
