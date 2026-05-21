package surf

import (
	"bufio"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestComputeAcceptKey(t *testing.T) {
	// Canonical example from RFC 6455 section 1.3.
	got := computeAcceptKey("dGhlIHNhbXBsZSBub25jZQ==")
	want := "s3pPLMBiTxaQ9kYGzzhZRbK+xOo="
	if got != want {
		t.Errorf("computeAcceptKey = %q, want %q", got, want)
	}
}

func TestIsWebSocketUpgrade(t *testing.T) {
	r := httptest.NewRequest("GET", "/ws", nil)
	if IsWebSocketUpgrade(r) {
		t.Error("plain GET reported as upgrade")
	}
	r.Header.Set("Connection", "keep-alive, Upgrade")
	r.Header.Set("Upgrade", "websocket")
	r.Header.Set("Sec-WebSocket-Version", "13")
	r.Header.Set("Sec-WebSocket-Key", "dGhlIHNhbXBsZSBub25jZQ==")
	if !IsWebSocketUpgrade(r) {
		t.Error("valid upgrade request not recognized")
	}
}

func newTestWSConn(c net.Conn) *WSConn {
	return &WSConn{
		conn:           c,
		r:              bufio.NewReader(c),
		w:              bufio.NewWriter(c),
		maxMessageSize: defaultWSMaxMessage,
	}
}

// maskedFrame builds a final, masked client frame (payload <= 125 bytes).
func maskedFrame(opcode byte, payload []byte) []byte {
	key := []byte{0x12, 0x34, 0x56, 0x78}
	f := []byte{0x80 | opcode, 0x80 | byte(len(payload))}
	f = append(f, key...)
	for i, b := range payload {
		f = append(f, b^key[i%4])
	}
	return f
}

func TestWSReadMaskedMessage(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	ws := newTestWSConn(server)

	type result struct {
		mt   int
		data []byte
		err  error
	}
	resc := make(chan result, 1)
	go func() {
		mt, data, err := ws.ReadMessage()
		resc <- result{mt, data, err}
	}()

	if _, err := client.Write(maskedFrame(TextMessage, []byte("hello"))); err != nil {
		t.Fatalf("client write: %v", err)
	}
	res := <-resc
	if res.err != nil {
		t.Fatalf("ReadMessage: %v", res.err)
	}
	if res.mt != TextMessage || string(res.data) != "hello" {
		t.Errorf("got type=%d data=%q", res.mt, res.data)
	}
}

func TestWSWriteUnmaskedFrame(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	ws := newTestWSConn(server)

	go func() { _ = ws.WriteText("pong") }()

	head := make([]byte, 2)
	if _, err := io.ReadFull(client, head); err != nil {
		t.Fatalf("read head: %v", err)
	}
	if head[0] != 0x81 { // FIN + text opcode
		t.Errorf("byte0 = 0x%x, want 0x81", head[0])
	}
	if head[1]&0x80 != 0 {
		t.Error("server frame must not be masked")
	}
	payload := make([]byte, int(head[1]&0x7f))
	if _, err := io.ReadFull(client, payload); err != nil {
		t.Fatalf("read payload: %v", err)
	}
	if string(payload) != "pong" {
		t.Errorf("payload = %q, want pong", payload)
	}
}

func TestWSCloseFrame(t *testing.T) {
	client, server := net.Pipe()
	ws := newTestWSConn(server)

	go func() { _, _ = io.Copy(io.Discard, client) }() // drain server's close reply

	go func() { _, _ = client.Write(maskedFrame(closeOpcode, nil)) }()

	_, _, err := ws.ReadMessage()
	if err != ErrWebSocketClosed {
		t.Errorf("err = %v, want ErrWebSocketClosed", err)
	}
	client.Close()
}

func TestWSUpgradeEcho(t *testing.T) {
	app := NewApp()
	app.Get("/ws", func(w http.ResponseWriter, r *http.Request) error {
		conn, err := Upgrade(w, r)
		if err != nil {
			return err
		}
		defer conn.Close()
		mt, data, err := conn.ReadMessage()
		if err != nil {
			return Abort
		}
		_ = conn.WriteMessage(mt, data)
		return Abort
	})
	srv := httptest.NewServer(app)
	defer srv.Close()

	host := strings.TrimPrefix(srv.URL, "http://")
	conn, err := net.Dial("tcp", host)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	handshake := "GET /ws HTTP/1.1\r\n" +
		"Host: " + host + "\r\n" +
		"Upgrade: websocket\r\n" +
		"Connection: Upgrade\r\n" +
		"Sec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\n" +
		"Sec-WebSocket-Version: 13\r\n\r\n"
	if _, err := conn.Write([]byte(handshake)); err != nil {
		t.Fatalf("write handshake: %v", err)
	}

	br := bufio.NewReader(conn)
	statusLine, err := br.ReadString('\n')
	if err != nil || !strings.Contains(statusLine, "101") {
		t.Fatalf("handshake status = %q (err %v)", statusLine, err)
	}
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			t.Fatalf("read headers: %v", err)
		}
		if line == "\r\n" {
			break
		}
	}

	if _, err := conn.Write(maskedFrame(TextMessage, []byte("echo me"))); err != nil {
		t.Fatalf("write frame: %v", err)
	}

	head := make([]byte, 2)
	if _, err := io.ReadFull(br, head); err != nil {
		t.Fatalf("read echo head: %v", err)
	}
	payload := make([]byte, int(head[1]&0x7f))
	if _, err := io.ReadFull(br, payload); err != nil {
		t.Fatalf("read echo payload: %v", err)
	}
	if string(payload) != "echo me" {
		t.Errorf("echo = %q, want %q", payload, "echo me")
	}
}
