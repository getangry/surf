package surf

import (
	"bufio"
	"errors"
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
	return maskedFrameFin(true, opcode, payload)
}

// maskedFrameFin builds a masked client frame with an explicit FIN bit.
func maskedFrameFin(fin bool, opcode byte, payload []byte) []byte {
	b0 := opcode
	if fin {
		b0 |= 0x80
	}
	key := []byte{0x12, 0x34, 0x56, 0x78}
	f := []byte{b0, 0x80 | byte(len(payload))}
	f = append(f, key...)
	for i, b := range payload {
		f = append(f, b^key[i%4])
	}
	return f
}

// readServerFrame reads one unmasked server frame (payload <= 125 bytes).
func readServerFrame(t *testing.T, r io.Reader) (opcode byte, payload []byte) {
	t.Helper()
	head := make([]byte, 2)
	if _, err := io.ReadFull(r, head); err != nil {
		t.Fatalf("read server frame head: %v", err)
	}
	payload = make([]byte, int(head[1]&0x7f))
	if _, err := io.ReadFull(r, payload); err != nil {
		t.Fatalf("read server frame payload: %v", err)
	}
	return head[0] & 0x0f, payload
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

func TestWSFragmentedMessage(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	ws := newTestWSConn(server)

	type result struct {
		data []byte
		err  error
	}
	resc := make(chan result, 1)
	go func() {
		_, data, err := ws.ReadMessage()
		resc <- result{data, err}
	}()

	go func() {
		_, _ = client.Write(maskedFrameFin(false, TextMessage, []byte("Hello ")))
		_, _ = client.Write(maskedFrameFin(true, contOpcode, []byte("World")))
	}()

	res := <-resc
	if res.err != nil {
		t.Fatalf("ReadMessage: %v", res.err)
	}
	if string(res.data) != "Hello World" {
		t.Errorf("reassembled = %q, want %q", res.data, "Hello World")
	}
}

func TestWSPingIsAnswered(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	ws := newTestWSConn(server)

	type result struct {
		data []byte
		err  error
	}
	resc := make(chan result, 1)
	go func() {
		_, data, err := ws.ReadMessage()
		resc <- result{data, err}
	}()

	go func() {
		_, _ = client.Write(maskedFrame(pingOpcode, []byte("hi")))
		// The server auto-replies with a pong before delivering the message.
		opcode, payload := readServerFrame(t, client)
		if opcode != pongOpcode || string(payload) != "hi" {
			t.Errorf("expected pong{hi}, got opcode=0x%x payload=%q", opcode, payload)
		}
		_, _ = client.Write(maskedFrame(TextMessage, []byte("after-ping")))
	}()

	res := <-resc
	if res.err != nil {
		t.Fatalf("ReadMessage: %v", res.err)
	}
	if string(res.data) != "after-ping" {
		t.Errorf("message = %q, want after-ping", res.data)
	}
}

func TestWSWriteBinary(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	ws := newTestWSConn(server)

	go func() { _ = ws.WriteBinary([]byte{0xDE, 0xAD}) }()

	opcode, payload := readServerFrame(t, client)
	if opcode != BinaryMessage {
		t.Errorf("opcode = 0x%x, want binary", opcode)
	}
	if len(payload) != 2 || payload[0] != 0xDE || payload[1] != 0xAD {
		t.Errorf("payload = % x", payload)
	}
}

func TestWSMaxMessageSize(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	ws := newTestWSConn(server)
	ws.SetMaxMessageSize(4)

	errc := make(chan error, 1)
	go func() {
		_, _, err := ws.ReadMessage()
		errc <- err
	}()
	go func() { _, _ = client.Write(maskedFrame(TextMessage, []byte("too-long-payload"))) }()

	if err := <-errc; err == nil {
		t.Error("expected error for oversized message, got nil")
	}
}

func TestWSWriteMessageRejectsBadType(t *testing.T) {
	_, server := net.Pipe()
	ws := newTestWSConn(server)
	if err := ws.WriteMessage(0x9, []byte("x")); err == nil {
		t.Error("expected error for non-data message type")
	}
}

func wsUpgradeRequest(host, origin string) *http.Request {
	r := httptest.NewRequest("GET", "http://"+host+"/ws", nil)
	r.Host = host
	r.Header.Set("Connection", "Upgrade")
	r.Header.Set("Upgrade", "websocket")
	r.Header.Set("Sec-WebSocket-Version", "13")
	r.Header.Set("Sec-WebSocket-Key", "dGhlIHNhbXBsZSBub25jZQ==")
	if origin != "" {
		r.Header.Set("Origin", origin)
	}
	return r
}

func is403(err error) bool {
	var he *HTTPError
	return errors.As(err, &he) && he.Code == http.StatusForbidden
}

func TestSameOriginCheck(t *testing.T) {
	cases := []struct {
		host, origin string
		want         bool
	}{
		{"app.example", "", true},                          // no Origin: non-browser client
		{"app.example", "https://app.example", true},       // same host
		{"app.example", "http://app.example", true},        // scheme is not compared
		{"app.example", "https://evil.example", false},     // different host
		{"app.example", "https://app.example:8443", false}, // different port
		{"app.example", "garbage", false},                  // unparseable -> empty host
	}
	for _, c := range cases {
		r := httptest.NewRequest("GET", "http://"+c.host+"/", nil)
		r.Host = c.host
		if c.origin != "" {
			r.Header.Set("Origin", c.origin)
		}
		if got := SameOriginCheck(r); got != c.want {
			t.Errorf("SameOriginCheck(host=%q origin=%q) = %v, want %v", c.host, c.origin, got, c.want)
		}
	}
}

func TestAllowOrigins(t *testing.T) {
	check := AllowOrigins("https://a.example", "https://b.example")
	mk := func(origin string) *http.Request {
		r := httptest.NewRequest("GET", "/", nil)
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		return r
	}
	if !check(mk("")) {
		t.Error("missing Origin should be allowed")
	}
	if !check(mk("https://b.example")) {
		t.Error("listed origin should be allowed")
	}
	if check(mk("https://evil.example")) {
		t.Error("unlisted origin must be rejected")
	}
}

func TestUpgradeRejectsCrossOrigin(t *testing.T) {
	r := wsUpgradeRequest("victim.example", "https://evil.example")
	_, err := Upgrade(httptest.NewRecorder(), r)
	if !is403(err) {
		t.Fatalf("cross-origin upgrade: err = %v, want 403 HTTPError", err)
	}
}

func TestUpgradeAllowsSameOrigin(t *testing.T) {
	r := wsUpgradeRequest("victim.example", "http://victim.example")
	// httptest.Recorder cannot hijack, so the upgrade still fails — but it must
	// get past the origin check, i.e. not be rejected with 403.
	_, err := Upgrade(httptest.NewRecorder(), r)
	if is403(err) {
		t.Fatalf("same-origin upgrade was rejected: %v", err)
	}
}

func TestUpgradeWithConfigCustomCheckOrigin(t *testing.T) {
	r := wsUpgradeRequest("victim.example", "https://partner.example")
	cfg := UpgradeConfig{CheckOrigin: AllowOrigins("https://partner.example")}
	_, err := UpgradeWithConfig(httptest.NewRecorder(), r, cfg)
	if is403(err) {
		t.Fatalf("custom CheckOrigin did not permit the configured origin: %v", err)
	}
}

func TestUpgradeRejectsNonWebSocket(t *testing.T) {
	r := httptest.NewRequest("GET", "/ws", nil)
	_, err := Upgrade(httptest.NewRecorder(), r)
	var he *HTTPError
	if !errors.As(err, &he) || he.Code != http.StatusBadRequest {
		t.Fatalf("non-websocket request: err = %v, want 400 HTTPError", err)
	}
}
