package gateway

import (
	"context"
	"io"
	"net/http"
	"time"

	"github.com/coder/websocket"
)

// wsWriteTimeout bounds one message write so a stalled client cannot pin the
// writer goroutine forever.
const wsWriteTimeout = 10 * time.Second

// wsConn is one upgraded connection: newline-framed JSON-RPC messages travel in
// text messages. The RFC 6455 side — the handshake, the frame codec (masking,
// extended lengths, fragmentation), ping/pong and the close handshake — is
// github.com/coder/websocket's job; this type is the gateway's dialect on top
// of it.
type wsConn struct {
	conn *websocket.Conn
}

// wsUpgrade accepts the websocket handshake. The caller has already checked
// Host, Origin and the token (a failure there is still answerable as an ordinary
// HTTP error, RFC 6455 §3.2 C2/C3), so the library's own Origin check is
// switched off: it can only compare Origin against Host and does not know the
// configured allow-list, and double-checking would reject origins the product
// accepts.
func wsUpgrade(w http.ResponseWriter, r *http.Request, maxFrame int64) (*wsConn, error) {
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		InsecureSkipVerify: true,
		CompressionMode:    websocket.CompressionDisabled,
	})
	if err != nil {
		return nil, err
	}
	// The 16 MiB frame budget is a per-message read limit here. It must be set
	// explicitly: the library's own default is 32 KiB, which would refuse
	// perfectly ordinary snapshots and replays.
	conn.SetReadLimit(maxFrame)
	return &wsConn{conn: conn}, nil
}

// readMessage returns the next complete data message. Ping and pong are handled
// inside the library (a ping is answered with the same payload), and a peer
// close ends the connection: the caller sees io.EOF after the library has
// echoed the close frame.
//
// The read deliberately has no context deadline and no cancellable context: a
// cancelled read context would make the library tear the socket down, and the
// gateway's shutdown path writes one last gateway.shutdown frame before the
// connection goes away. Close (CloseNow) is what unblocks this call.
func (c *wsConn) readMessage() (websocket.MessageType, []byte, error) {
	messageType, payload, err := c.conn.Read(context.Background())
	if err != nil {
		if websocket.CloseStatus(err) != -1 {
			return 0, nil, io.EOF
		}
		return 0, nil, err
	}
	return messageType, payload, nil
}

// writeText writes one text message.
func (c *wsConn) writeText(payload []byte) error {
	ctx, cancel := context.WithTimeout(context.Background(), wsWriteTimeout)
	defer cancel()
	return c.conn.Write(ctx, websocket.MessageText, payload)
}

// Close drops the connection. It deliberately skips the close handshake: the
// gateway's own shutdown and failure paths must never wait for a peer, and a
// client that vanished would otherwise pin the handler for the handshake
// timeout.
func (c *wsConn) Close() error {
	return c.conn.CloseNow()
}
