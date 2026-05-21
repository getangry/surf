package surf

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
	"net/url"
	"strings"
	"sync"
)

// WebSocket message types, matching RFC 6455 opcodes.
const (
	TextMessage   = 0x1
	BinaryMessage = 0x2
	closeOpcode   = 0x8
	pingOpcode    = 0x9
	pongOpcode    = 0xA
	contOpcode    = 0x0
)

// wsGUID is the magic value from RFC 6455 used to derive the accept key.
const wsGUID = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"

// defaultWSMaxMessage caps the size of a single inbound message (8 MiB).
const defaultWSMaxMessage = 8 << 20

// ErrWebSocketClosed is returned by WSConn.ReadMessage once the peer has sent
// a close frame.
var ErrWebSocketClosed = errors.New("surf: websocket closed by peer")

// IsWebSocketUpgrade reports whether r is a valid RFC 6455 upgrade request.
func IsWebSocketUpgrade(r *http.Request) bool {
	return r.Method == http.MethodGet &&
		headerListContains(r.Header.Get("Connection"), "upgrade") &&
		strings.EqualFold(r.Header.Get("Upgrade"), "websocket") &&
		r.Header.Get("Sec-WebSocket-Version") == "13" &&
		r.Header.Get("Sec-WebSocket-Key") != ""
}

// headerListContains reports whether a comma-separated header value contains
// token (case-insensitive).
func headerListContains(value, token string) bool {
	for _, part := range strings.Split(value, ",") {
		if strings.EqualFold(strings.TrimSpace(part), token) {
			return true
		}
	}
	return false
}

// computeAcceptKey derives the Sec-WebSocket-Accept value from the client key.
func computeAcceptKey(key string) string {
	h := sha1.New()
	_, _ = io.WriteString(h, key+wsGUID)
	return base64.StdEncoding.EncodeToString(h.Sum(nil))
}

// WSConn is a minimal WebSocket connection. It supports text and binary
// messages (including fragmented messages) and transparently answers pings.
// It is safe for one reader and one writer goroutine; writes are serialized.
type WSConn struct {
	conn           net.Conn
	r              *bufio.Reader
	w              *bufio.Writer
	writeMu        sync.Mutex
	maxMessageSize int64
}

// UpgradeConfig configures the WebSocket handshake.
type UpgradeConfig struct {
	// CheckOrigin decides whether a handshake from the request's Origin is
	// allowed. It returns true to permit the upgrade. When nil, SameOriginCheck
	// is used: the handshake is allowed only if the Origin header is absent or
	// its host matches the request Host.
	//
	// WebSocket connections are not subject to the Same-Origin Policy and CORS
	// does not apply to them, yet browsers attach cookies to cross-origin
	// handshakes. Without an origin check, a cookie-authenticated WebSocket
	// endpoint is open to cross-site WebSocket hijacking. Override CheckOrigin
	// only when you intend to accept cross-origin clients.
	CheckOrigin func(r *http.Request) bool
}

// SameOriginCheck is the default CheckOrigin: it permits a handshake when the
// request carries no Origin header (non-browser clients) or when the Origin's
// host equals the request Host.
func SameOriginCheck(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	u, err := url.Parse(origin)
	if err != nil {
		return false
	}
	return strings.EqualFold(u.Host, r.Host)
}

// AllowOrigins returns a CheckOrigin function permitting handshakes whose
// Origin header exactly matches one of the given origins (compared
// case-insensitively, e.g. "https://app.example.com"). A request without an
// Origin header is allowed.
func AllowOrigins(origins ...string) func(r *http.Request) bool {
	allowed := append([]string{}, origins...)
	return func(r *http.Request) bool {
		origin := r.Header.Get("Origin")
		if origin == "" {
			return true
		}
		for _, o := range allowed {
			if strings.EqualFold(o, origin) {
				return true
			}
		}
		return false
	}
}

// Upgrade completes the RFC 6455 handshake on r and returns a WSConn, applying
// the default same-origin policy (see SameOriginCheck). Use UpgradeWithConfig
// to accept cross-origin clients. The caller owns the returned connection and
// must Close it; the http.Server no longer manages it after a successful
// upgrade.
func Upgrade(w http.ResponseWriter, r *http.Request) (*WSConn, error) {
	return UpgradeWithConfig(w, r, UpgradeConfig{})
}

// UpgradeWithConfig is Upgrade with an explicit UpgradeConfig.
//
// A request that is not a valid upgrade fails with a 400 *HTTPError; a request
// rejected by CheckOrigin fails with a 403 *HTTPError — in both cases the
// connection is not hijacked, so a handler can simply `return err` and let the
// error renderer respond.
func UpgradeWithConfig(w http.ResponseWriter, r *http.Request, config UpgradeConfig) (*WSConn, error) {
	if !IsWebSocketUpgrade(r) {
		return nil, NewHTTPError(http.StatusBadRequest, "request is not a websocket upgrade")
	}

	checkOrigin := config.CheckOrigin
	if checkOrigin == nil {
		checkOrigin = SameOriginCheck
	}
	if !checkOrigin(r) {
		return nil, NewHTTPError(http.StatusForbidden, "websocket origin not allowed")
	}

	hj, ok := w.(http.Hijacker)
	if !ok {
		return nil, errors.New("surf: ResponseWriter does not support hijacking")
	}
	conn, brw, err := hj.Hijack()
	if err != nil {
		return nil, err
	}

	accept := computeAcceptKey(r.Header.Get("Sec-WebSocket-Key"))
	resp := "HTTP/1.1 101 Switching Protocols\r\n" +
		"Upgrade: websocket\r\n" +
		"Connection: Upgrade\r\n" +
		"Sec-WebSocket-Accept: " + accept + "\r\n\r\n"
	if _, err := brw.WriteString(resp); err != nil {
		_ = conn.Close()
		return nil, err
	}
	if err := brw.Flush(); err != nil {
		_ = conn.Close()
		return nil, err
	}

	return &WSConn{
		conn:           conn,
		r:              brw.Reader,
		w:              brw.Writer,
		maxMessageSize: defaultWSMaxMessage,
	}, nil
}

// SetMaxMessageSize overrides the inbound message size limit (bytes).
func (c *WSConn) SetMaxMessageSize(n int64) {
	if n > 0 {
		c.maxMessageSize = n
	}
}

// ReadMessage reads the next complete text or binary message, reassembling
// fragments and answering ping frames automatically. It returns
// ErrWebSocketClosed when the peer closes the connection.
func (c *WSConn) ReadMessage() (messageType int, payload []byte, err error) {
	var (
		buf     []byte
		msgType int
		started bool
	)
	for {
		fin, opcode, data, err := c.readFrame()
		if err != nil {
			return 0, nil, err
		}
		switch opcode {
		case pingOpcode:
			if err := c.writeFrame(pongOpcode, data); err != nil {
				return 0, nil, err
			}
			continue
		case pongOpcode:
			continue
		case closeOpcode:
			_ = c.writeFrame(closeOpcode, nil)
			return 0, nil, ErrWebSocketClosed
		case TextMessage, BinaryMessage:
			if started {
				return 0, nil, errors.New("surf: unexpected new message before fragment finished")
			}
			started = true
			msgType = opcode
			buf = append(buf, data...)
		case contOpcode:
			if !started {
				return 0, nil, errors.New("surf: continuation frame without an initial frame")
			}
			buf = append(buf, data...)
		default:
			return 0, nil, fmt.Errorf("surf: unsupported websocket opcode 0x%x", opcode)
		}
		if int64(len(buf)) > c.maxMessageSize {
			return 0, nil, errors.New("surf: websocket message exceeds size limit")
		}
		if fin {
			return msgType, buf, nil
		}
	}
}

// readFrame reads a single frame, unmasking the payload if the client masked
// it (clients always must, per RFC 6455).
func (c *WSConn) readFrame() (fin bool, opcode int, payload []byte, err error) {
	head := make([]byte, 2)
	if _, err = io.ReadFull(c.r, head); err != nil {
		return false, 0, nil, err
	}
	fin = head[0]&0x80 != 0
	opcode = int(head[0] & 0x0f)
	masked := head[1]&0x80 != 0
	length := int64(head[1] & 0x7f)

	switch length {
	case 126:
		ext := make([]byte, 2)
		if _, err = io.ReadFull(c.r, ext); err != nil {
			return false, 0, nil, err
		}
		length = int64(binary.BigEndian.Uint16(ext))
	case 127:
		ext := make([]byte, 8)
		if _, err = io.ReadFull(c.r, ext); err != nil {
			return false, 0, nil, err
		}
		length = int64(binary.BigEndian.Uint64(ext))
	}
	if length < 0 || length > c.maxMessageSize {
		return false, 0, nil, errors.New("surf: websocket frame exceeds size limit")
	}

	var maskKey [4]byte
	if masked {
		if _, err = io.ReadFull(c.r, maskKey[:]); err != nil {
			return false, 0, nil, err
		}
	}

	payload = make([]byte, length)
	if _, err = io.ReadFull(c.r, payload); err != nil {
		return false, 0, nil, err
	}
	if masked {
		for i := range payload {
			payload[i] ^= maskKey[i%4]
		}
	}
	return fin, opcode, payload, nil
}

// WriteMessage sends a complete, unfragmented message. messageType must be
// TextMessage or BinaryMessage.
func (c *WSConn) WriteMessage(messageType int, data []byte) error {
	if messageType != TextMessage && messageType != BinaryMessage {
		return fmt.Errorf("surf: invalid websocket message type %d", messageType)
	}
	return c.writeFrame(messageType, data)
}

// WriteText sends a UTF-8 text message.
func (c *WSConn) WriteText(s string) error {
	return c.writeFrame(TextMessage, []byte(s))
}

// WriteBinary sends a binary message.
func (c *WSConn) WriteBinary(data []byte) error {
	return c.writeFrame(BinaryMessage, data)
}

// writeFrame writes a single, final, unmasked frame (servers must not mask).
func (c *WSConn) writeFrame(opcode int, data []byte) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()

	header := make([]byte, 0, 10)
	header = append(header, byte(0x80|opcode)) // FIN set

	n := len(data)
	switch {
	case n <= 125:
		header = append(header, byte(n))
	case n <= 0xFFFF:
		header = append(header, 126)
		header = binary.BigEndian.AppendUint16(header, uint16(n))
	default:
		header = append(header, 127)
		header = binary.BigEndian.AppendUint64(header, uint64(n))
	}

	if _, err := c.w.Write(header); err != nil {
		return err
	}
	if _, err := c.w.Write(data); err != nil {
		return err
	}
	return c.w.Flush()
}

// Close sends a close frame and closes the underlying connection.
func (c *WSConn) Close() error {
	_ = c.writeFrame(closeOpcode, nil)
	return c.conn.Close()
}
