package gateway

import (
	"encoding/json"
	"fmt"
)

// Protocol limits and shape constants (docs/TRANSPORT.md §4.1, §6.4).
const (
	// ProtocolVersion is the control-plane version this gateway speaks.
	ProtocolVersion = 1
	// MaxFrameBytes is the JSON-RPC frame limit, the same 16 MiB the ACP
	// transport uses (rig acp.DefaultMaxFrameBytes).
	MaxFrameBytes = 16 << 20
	// MaxAttachmentBytes is the largest single attachment/blob accepted.
	MaxAttachmentBytes = 10 << 20
	// SnapshotTailDefault is how many recent messages session.snapshot returns
	// when the client does not ask for a window (§6.4).
	SnapshotTailDefault = 60
	// DeltaRingEntries and DeltaRingBytes bound one domain's replay ring: a
	// reconnect inside either bound resumes from deltas, beyond it resyncs.
	DeltaRingEntries = 256
	DeltaRingBytes   = 256 << 10
)

// JSON-RPC 2.0 and application error codes (docs/TRANSPORT.md §4.3).
const (
	codeParseError     = -32700
	codeInvalidRequest = -32600
	codeMethodNotFound = -32601
	codeInvalidParams  = -32602
	codeInternalError  = -32603

	codeSessionNotFound       = 1001
	codeSessionRunning        = 1002
	codeRevisionGone          = 1019
	codeUnauthorized          = 1020
	codeUnsupportedProto      = 1021
	codeBadOrigin             = 1022
	codeCapabilityUnsupported = 1023
	codeNotReady              = 1024
	codeBackpressure          = 1025
	codeCancelled             = 1026
)

// rpcRequest is one request or notification frame.
type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// isNotification reports a frame without an id.
func (r rpcRequest) isNotification() bool {
	return len(r.ID) == 0 || string(r.ID) == "null"
}

// rpcResponse is one response frame.
type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *protocolError  `json:"error,omitempty"`
}

// rpcNotification is one server-push frame (no id).
type rpcNotification struct {
	JSONRPC string `json:"jsonrpc"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

// protocolError is the JSON-RPC error object. Applications code additionally
// carry a stable name and a retryable hint so a browser (which has no
// errors.Is) can branch on the code instead of parsing English text.
type protocolError struct {
	Code    int            `json:"code"`
	Message string         `json:"message"`
	Data    map[string]any `json:"data,omitempty"`
}

// Error implements the error interface so handlers can return *protocolError.
func (e *protocolError) Error() string {
	return fmt.Sprintf("jsonrpc error %d: %s", e.Code, e.Message)
}

// errParse reports a frame that is not valid JSON.
func errParse(message string) *protocolError {
	return &protocolError{Code: codeParseError, Message: message}
}

// errInvalidRequest reports a frame missing a method or an id.
func errInvalidRequest(message string) *protocolError {
	return &protocolError{Code: codeInvalidRequest, Message: message}
}

// errMethodNotFound reports an unimplemented method.
func errMethodNotFound(method string) *protocolError {
	return &protocolError{Code: codeMethodNotFound, Message: "unknown method: " + method}
}

// errInvalidParams reports a bad parameter with the failing field.
func errInvalidParams(field, reason string) *protocolError {
	return &protocolError{
		Code:    codeInvalidParams,
		Message: "invalid params: " + field + ": " + reason,
		Data:    map[string]any{"field": field, "reason": reason},
	}
}

// errInternal reports a gateway failure.
func errInternal(message string) *protocolError {
	return &protocolError{Code: codeInternalError, Message: message}
}

// appError builds one documented application error (§4.3).
func appError(code int, name string, retryable bool) *protocolError {
	return &protocolError{
		Code:    code,
		Message: name,
		Data:    map[string]any{"name": name, "retryable": retryable},
	}
}

// errNotReady reports that the assembly is still loading or shutting down.
func errNotReady() *protocolError {
	return appError(codeNotReady, "not_ready", true)
}

// encodeFrame marshals one frame without the trailing newline.
func encodeFrame(value any) ([]byte, error) {
	return json.Marshal(value)
}

// encodeNotification marshals one server notification frame.
func encodeNotification(method string, params any) ([]byte, error) {
	return encodeFrame(rpcNotification{JSONRPC: "2.0", Method: method, Params: params})
}

// splitLines feeds one client frame's payload through a newline splitter and
// returns the complete lines plus the unterminated remainder. JSON-RPC over the
// gateway is one message per line (§4.1); a client is free to batch several
// lines into one WebSocket frame.
func splitLines(buffer, payload []byte, limit int) (lines [][]byte, rest []byte, err error) {
	combined := make([]byte, 0, len(buffer)+len(payload))
	combined = append(combined, buffer...)
	combined = append(combined, payload...)
	start := 0
	for i := 0; i < len(combined); i++ {
		if combined[i] != '\n' {
			continue
		}
		line := combined[start:i]
		start = i + 1
		line = trimSpaceBytes(line)
		if len(line) == 0 {
			continue
		}
		lines = append(lines, line)
	}
	rest = combined[start:]
	if len(rest) > limit {
		return nil, nil, fmt.Errorf("gateway: client frame exceeds %d bytes", limit)
	}
	// The caller stores rest in its own buffer.
	out := make([]byte, len(rest))
	copy(out, rest)
	return lines, out, nil
}

// trimSpaceBytes trims ASCII whitespace (CR included: an \r\n client works).
func trimSpaceBytes(value []byte) []byte {
	start := 0
	for start < len(value) {
		switch value[start] {
		case ' ', '\t', '\r', '\n':
			start++
			continue
		}
		break
	}
	end := len(value)
	for end > start {
		switch value[end-1] {
		case ' ', '\t', '\r', '\n':
			end--
			continue
		}
		break
	}
	return value[start:end]
}
