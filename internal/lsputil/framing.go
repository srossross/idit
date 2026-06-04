// Package lsputil holds pure helpers for speaking LSP: message framing, URI
// conversions, and normalization of the various result shapes into idit's own
// path-based, 1-based forms. Nothing here does I/O or owns a connection.
package lsputil

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
)

// JSON-RPC over stdio, framed with `Content-Length` headers as defined by the
// Language Server Protocol base protocol.

// EncodeMessage serializes an RPC message into a Content-Length-framed buffer.
func EncodeMessage(msg any) ([]byte, error) {
	body, err := json.Marshal(msg)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	fmt.Fprintf(&buf, "Content-Length: %d\r\n\r\n", len(body))
	buf.Write(body)
	return buf.Bytes(), nil
}

var headerTerminator = []byte("\r\n\r\n")

var contentLengthRe = regexp.MustCompile(`(?i)Content-Length:\s*(\d+)`)

// MessageDecoder is an incremental decoder: feed it raw chunks from the
// server's stdout and it returns whatever complete message bodies have arrived,
// buffering partial ones. Each returned []byte is the JSON body of one message.
type MessageDecoder struct {
	buf []byte
}

// Push appends chunk to the internal buffer and returns the bodies of any
// complete messages that are now available.
func (d *MessageDecoder) Push(chunk []byte) [][]byte {
	d.buf = append(d.buf, chunk...)

	var messages [][]byte
	for {
		headerEnd := bytes.Index(d.buf, headerTerminator)
		if headerEnd == -1 {
			break
		}

		header := d.buf[:headerEnd]
		match := contentLengthRe.FindSubmatch(header)
		if match == nil {
			// Unparseable header — drop it and resync rather than wedging.
			d.buf = d.buf[headerEnd+len(headerTerminator):]
			continue
		}

		var length int
		fmt.Sscanf(string(match[1]), "%d", &length)
		bodyStart := headerEnd + len(headerTerminator)
		if len(d.buf) < bodyStart+length {
			break // body not fully here yet
		}

		body := make([]byte, length)
		copy(body, d.buf[bodyStart:bodyStart+length])
		messages = append(messages, body)
		d.buf = d.buf[bodyStart+length:]
	}
	return messages
}
