package lsp

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/textproto"
	"strconv"
	"strings"
)

// JSON-RPC framing for the Language Server Protocol.
//
// Every message is a Content-Length header, a blank line, then exactly that
// many bytes of UTF-8 JSON: "Content-Length: 42\r\n\r\n{...}".
const (
	// maxMessageBytes is a hard cap on one inbound message. It keeps a broken
	// or hostile server from making the client allocate unbounded memory.
	maxMessageBytes = 16 << 20
)

// wireMessage is one LSP JSON-RPC envelope: a request/notification from the
// client, a response from the server, or a request/notification initiated by
// the server.
type wireMessage struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *wireError      `json:"error,omitempty"`
}

// wireError is a JSON-RPC error object returned by a language server.
type wireError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

// Error implements error.
func (e *wireError) Error() string {
	if strings.TrimSpace(e.Message) == "" {
		return fmt.Sprintf("language server error (code %d)", e.Code)
	}
	return fmt.Sprintf("language server error (code %d): %s", e.Code, strings.TrimSpace(e.Message))
}

// writeMessage writes one Content-Length framed JSON-RPC message.
func writeMessage(w io.Writer, msg wireMessage) error {
	if w == nil {
		return errors.New("lsp: cannot write to a nil stream")
	}
	body, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("lsp: encode %s: %w", msg.Method, err)
	}
	header := "Content-Length: " + strconv.Itoa(len(body)) + "\r\n\r\n"
	if _, err := io.WriteString(w, header); err != nil {
		return fmt.Errorf("lsp: write message header: %w", err)
	}
	if _, err := w.Write(body); err != nil {
		return fmt.Errorf("lsp: write message body: %w", err)
	}
	return nil
}

// readMessage reads one Content-Length framed JSON-RPC message.
func readMessage(r *bufio.Reader) (wireMessage, error) {
	if r == nil {
		return wireMessage{}, errors.New("lsp: cannot read from a nil stream")
	}
	header, err := textproto.NewReader(r).ReadMIMEHeader()
	if err != nil {
		if errors.Is(err, io.EOF) {
			return wireMessage{}, io.EOF
		}
		return wireMessage{}, fmt.Errorf("lsp: read message header: %w", err)
	}
	rawLength := strings.TrimSpace(header.Get("Content-Length"))
	if rawLength == "" {
		return wireMessage{}, errors.New("lsp: message is missing the Content-Length header")
	}
	length, err := strconv.Atoi(rawLength)
	if err != nil || length < 0 || length > maxMessageBytes {
		return wireMessage{}, fmt.Errorf("lsp: invalid Content-Length %q", rawLength)
	}
	body := make([]byte, length)
	if _, err := io.ReadFull(r, body); err != nil {
		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
			return wireMessage{}, fmt.Errorf("lsp: truncated message body: Content-Length %d does not match the readable bytes: %w", length, err)
		}
		return wireMessage{}, fmt.Errorf("lsp: read message body: %w", err)
	}
	var msg wireMessage
	if err := json.Unmarshal(body, &msg); err != nil {
		return wireMessage{}, fmt.Errorf("lsp: message body is not valid JSON: %w", err)
	}
	return msg, nil
}

// isJSONNull reports whether raw is the JSON literal null.
func isJSONNull(raw json.RawMessage) bool {
	return string(bytes.TrimSpace(raw)) == "null"
}
