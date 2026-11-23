package gateway

import (
	"bufio"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// WebSocket constants (RFC 6455). The gateway implements the small server-side
// subset the control plane needs — text frames, fragmentation, ping/pong and
// close — instead of taking a dependency: the binary is offline-built and the
// protocol shape here is fixed and tiny.
const (
	wsOpContinuation = 0x0
	wsOpText         = 0x1
	wsOpBinary       = 0x2
	wsOpClose        = 0x8
	wsOpPing         = 0x9
	wsOpPong         = 0xA

	// wsCloseNormal is the close code of a clean shutdown.
	wsCloseNormal = 1000
	// wsClosePolicy is the close code of an authentication/validation refusal.
	wsClosePolicy = 1008
	// wsCloseTooBig is the close code of a frame over the protocol limit.
	wsCloseTooBig = 1009
	// wsCloseInternal is the close code of an unexpected server failure.
	wsCloseInternal = 1011

	wsAcceptGUID = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"

	// wsWriteTimeout bounds one frame write so a stalled client cannot pin the
	// writer goroutine forever.
	wsWriteTimeout = 10 * time.Second
)

// wsConn is one upgraded connection: newline-framed JSON-RPC messages travel in
// text frames.
type wsConn struct {
	conn     net.Conn
	reader   *bufio.Reader
	writeMu  sync.Mutex
	maxFrame int64
}

// wsUpgrade validates the handshake and hijacks the HTTP connection. header is
// called before hijacking, so an authentication or Origin failure can still be
// answered as a normal HTTP error (the spec requires that: §3.2 C2/C3).
func wsUpgrade(w http.ResponseWriter, r *http.Request, maxFrame int64) (*wsConn, error) {
	if !headerContainsToken(r.Header, "Connection", "upgrade") ||
		!strings.EqualFold(strings.TrimSpace(r.Header.Get("Upgrade")), "websocket") {
		return nil, fmt.Errorf("websocket: not an upgrade request")
	}
	if version := strings.TrimSpace(r.Header.Get("Sec-WebSocket-Version")); version != "13" {
		return nil, fmt.Errorf("websocket: unsupported version %q", version)
	}
	key := strings.TrimSpace(r.Header.Get("Sec-WebSocket-Key"))
	if key == "" {
		return nil, fmt.Errorf("websocket: missing Sec-WebSocket-Key")
	}
	hijacker, ok := w.(http.Hijacker)
	if !ok {
		return nil, fmt.Errorf("websocket: response writer cannot hijack")
	}
	conn, rw, err := hijacker.Hijack()
	if err != nil {
		return nil, fmt.Errorf("websocket: hijack: %w", err)
	}
	accept := wsAcceptKey(key)
	response := "HTTP/1.1 101 Switching Protocols\r\n" +
		"Upgrade: websocket\r\n" +
		"Connection: Upgrade\r\n" +
		"Sec-WebSocket-Accept: " + accept + "\r\n\r\n"
	if _, err := rw.WriteString(response); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("websocket: write handshake: %w", err)
	}
	if err := rw.Flush(); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("websocket: flush handshake: %w", err)
	}
	return &wsConn{conn: conn, reader: rw.Reader, maxFrame: maxFrame}, nil
}

// wsAcceptKey computes the Sec-WebSocket-Accept value.
func wsAcceptKey(key string) string {
	sum := sha1.Sum([]byte(key + wsAcceptGUID))
	return base64.StdEncoding.EncodeToString(sum[:])
}

// headerContainsToken reports whether a comma-separated header contains token,
// case-insensitively.
func headerContainsToken(header http.Header, name, token string) bool {
	for _, value := range header.Values(name) {
		for _, part := range strings.Split(value, ",") {
			if strings.EqualFold(strings.TrimSpace(part), token) {
				return true
			}
		}
	}
	return false
}

// readMessage returns the next complete data message. Control frames are handled
// here: a ping is answered with a pong, a pong is ignored and a close ends the
// connection (the caller sees io.EOF).
func (c *wsConn) readMessage() (opcode byte, payload []byte, err error) {
	messageOpcode := byte(0)
	var message []byte
	fragmented := false
	for {
		fin, opcode, data, err := c.readFrame()
		if err != nil {
			return 0, nil, err
		}
		switch opcode {
		case wsOpPing:
			if err := c.writeFrame(wsOpPong, data); err != nil {
				return 0, nil, err
			}
			continue
		case wsOpPong:
			continue
		case wsOpClose:
			_ = c.writeClose(wsCloseNormal, "")
			return 0, nil, io.EOF
		case wsOpContinuation:
			if !fragmented {
				return 0, nil, errors.New("websocket: continuation without a start frame")
			}
			message = append(message, data...)
			if int64(len(message)) > c.maxFrame {
				_ = c.writeClose(wsCloseTooBig, "frame too large")
				return 0, nil, errors.New("websocket: message exceeds the frame limit")
			}
			if fin {
				return messageOpcode, message, nil
			}
		case wsOpText, wsOpBinary:
			if fragmented {
				return 0, nil, errors.New("websocket: interleaved data frames")
			}
			if fin {
				return opcode, data, nil
			}
			fragmented = true
			messageOpcode = opcode
			message = append(message[:0], data...)
		default:
			return 0, nil, fmt.Errorf("websocket: unsupported opcode %#x", opcode)
		}
	}
}

// readFrame reads one frame and unmasks it. Client frames must be masked.
func (c *wsConn) readFrame() (fin bool, opcode byte, payload []byte, err error) {
	var head [2]byte
	if _, err := io.ReadFull(c.reader, head[:]); err != nil {
		return false, 0, nil, err
	}
	fin = head[0]&0x80 != 0
	opcode = head[0] & 0x0f
	masked := head[1]&0x80 != 0
	length := int64(head[1] & 0x7f)
	switch length {
	case 126:
		var extended [2]byte
		if _, err := io.ReadFull(c.reader, extended[:]); err != nil {
			return false, 0, nil, err
		}
		length = int64(binary.BigEndian.Uint16(extended[:]))
	case 127:
		var extended [8]byte
		if _, err := io.ReadFull(c.reader, extended[:]); err != nil {
			return false, 0, nil, err
		}
		length = int64(binary.BigEndian.Uint64(extended[:]))
	}
	if length < 0 || length > c.maxFrame {
		_ = c.writeClose(wsCloseTooBig, "frame too large")
		return false, 0, nil, errors.New("websocket: frame exceeds the limit")
	}
	if !masked {
		_ = c.writeClose(wsClosePolicy, "client frames must be masked")
		return false, 0, nil, errors.New("websocket: unmasked client frame")
	}
	var mask [4]byte
	if _, err := io.ReadFull(c.reader, mask[:]); err != nil {
		return false, 0, nil, err
	}
	payload = make([]byte, length)
	if _, err := io.ReadFull(c.reader, payload); err != nil {
		return false, 0, nil, err
	}
	for i := range payload {
		payload[i] ^= mask[i%4]
	}
	return fin, opcode, payload, nil
}

// writeFrame writes one unfragmented frame. Only the writer goroutine and the
// read loop (for pong/close) call it, under writeMu.
func (c *wsConn) writeFrame(opcode byte, payload []byte) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if err := c.conn.SetWriteDeadline(time.Now().Add(wsWriteTimeout)); err != nil {
		return err
	}
	head := make([]byte, 0, 10)
	head = append(head, 0x80|opcode)
	switch {
	case len(payload) < 126:
		head = append(head, byte(len(payload)))
	case len(payload) <= 0xffff:
		head = append(head, 126, 0, 0)
		binary.BigEndian.PutUint16(head[len(head)-2:], uint16(len(payload)))
	default:
		head = append(head, 127, 0, 0, 0, 0, 0, 0, 0, 0)
		binary.BigEndian.PutUint64(head[len(head)-8:], uint64(len(payload)))
	}
	if _, err := c.conn.Write(append(head, payload...)); err != nil {
		return err
	}
	return nil
}

// writeText writes one text frame.
func (c *wsConn) writeText(payload []byte) error {
	return c.writeFrame(wsOpText, payload)
}

// writeClose sends a close frame with a code and a short reason.
func (c *wsConn) writeClose(code uint16, reason string) error {
	payload := make([]byte, 2, 2+len(reason))
	binary.BigEndian.PutUint16(payload, code)
	payload = append(payload, reason...)
	return c.writeFrame(wsOpClose, payload)
}

// Close closes the underlying connection.
func (c *wsConn) Close() error {
	return c.conn.Close()
}
