package surf

import (
	"bytes"
	"io"
	"net"
	"testing"
)

// readOnly adapts an io.Reader to io.ReadWriter for driving secureConn.ReadMsg
// in tests; writes are never expected.
type readOnly struct{ io.Reader }

func (readOnly) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }

func mustKeyring(t *testing.T, secrets ...epochSecret) *keyring {
	t.Helper()
	kr, err := newKeyring(secrets...)
	if err != nil {
		t.Fatalf("newKeyring: %v", err)
	}
	return kr
}

func TestKeyring_AtRestRoundTrip(t *testing.T) {
	kr := mustKeyring(t, epochSecret{Epoch: 1, Secret: []byte("super-secret-cluster-key")})
	plain := []byte("session={user:42}")
	blob, err := kr.sealAtRest(plain)
	if err != nil {
		t.Fatalf("sealAtRest: %v", err)
	}
	if bytes.Contains(blob, plain) {
		t.Fatal("plaintext visible in sealed blob")
	}
	got, err := kr.openAtRest(blob)
	if err != nil {
		t.Fatalf("openAtRest: %v", err)
	}
	if !bytes.Equal(got, plain) {
		t.Fatalf("openAtRest = %q, want %q", got, plain)
	}
}

func TestKeyring_AtRestWrongSecretFails(t *testing.T) {
	a := mustKeyring(t, epochSecret{Epoch: 1, Secret: []byte("secret-a")})
	b := mustKeyring(t, epochSecret{Epoch: 1, Secret: []byte("secret-b")})
	blob, _ := a.sealAtRest([]byte("data"))
	if _, err := b.openAtRest(blob); err != ErrAuthFailed {
		t.Fatalf("openAtRest with wrong secret = %v, want ErrAuthFailed", err)
	}
}

func TestKeyring_AtRestTamperFails(t *testing.T) {
	kr := mustKeyring(t, epochSecret{Epoch: 1, Secret: []byte("secret")})
	blob, _ := kr.sealAtRest([]byte("data"))
	blob[len(blob)-1] ^= 0xFF // flip a tag bit
	if _, err := kr.openAtRest(blob); err != ErrAuthFailed {
		t.Fatalf("openAtRest tampered = %v, want ErrAuthFailed", err)
	}
}

func TestKeyring_AtRestUnknownEpoch(t *testing.T) {
	a := mustKeyring(t, epochSecret{Epoch: 1, Secret: []byte("secret")})
	b := mustKeyring(t, epochSecret{Epoch: 2, Secret: []byte("secret")})
	blob, _ := a.sealAtRest([]byte("data"))
	if _, err := b.openAtRest(blob); err != ErrUnknownEpoch {
		t.Fatalf("openAtRest unknown epoch = %v, want ErrUnknownEpoch", err)
	}
}

func TestKeyring_RotationAcceptsBothEpochs(t *testing.T) {
	// Sender on old epoch 1; receiver rolling: current epoch 2, still accepts 1.
	old := mustKeyring(t, epochSecret{Epoch: 1, Secret: []byte("old-secret")})
	rolling := mustKeyring(t,
		epochSecret{Epoch: 2, Secret: []byte("new-secret")},
		epochSecret{Epoch: 1, Secret: []byte("old-secret")},
	)
	blob, _ := old.sealAtRest([]byte("legacy"))
	got, err := rolling.openAtRest(blob)
	if err != nil {
		t.Fatalf("rolling open of old epoch: %v", err)
	}
	if string(got) != "legacy" {
		t.Fatalf("got %q", got)
	}
}

func TestNewKeyring_Errors(t *testing.T) {
	if _, err := newKeyring(); err != errEmptySecret {
		t.Fatalf("no secrets = %v, want errEmptySecret", err)
	}
	if _, err := newKeyring(epochSecret{Epoch: 1, Secret: nil}); err != errEmptySecret {
		t.Fatalf("empty secret = %v, want errEmptySecret", err)
	}
	if _, err := newKeyring(
		epochSecret{Epoch: 1, Secret: []byte("a")},
		epochSecret{Epoch: 1, Secret: []byte("b")},
	); err != errDuplicateEpoch {
		t.Fatalf("dup epoch = %v, want errDuplicateEpoch", err)
	}
}

// handshakePair runs client and server handshakes over an in-memory socket
// pair and returns both ends (or the errors).
func handshakePair(t *testing.T, clientKR, serverKR *keyring) (*secureConn, *secureConn, error, error) {
	t.Helper()
	c, s := newBufferedConnPair("client", "server")
	t.Cleanup(func() { c.Close(); s.Close() })

	type res struct {
		sc  *secureConn
		err error
	}
	cc := make(chan res, 1)
	ss := make(chan res, 1)
	go func() {
		sc, err := clientHandshake(c, clientKR, "client-node")
		cc <- res{sc, err}
	}()
	go func() {
		sc, err := serverHandshake(s, serverKR, "server-node")
		ss <- res{sc, err}
	}()
	cr, sr := <-cc, <-ss
	return cr.sc, sr.sc, cr.err, sr.err
}

func TestHandshake_Success(t *testing.T) {
	kr := mustKeyring(t, epochSecret{Epoch: 7, Secret: []byte("cluster-secret")})
	client, server, cerr, serr := handshakePair(t, kr, kr)
	if cerr != nil || serr != nil {
		t.Fatalf("handshake errors: client=%v server=%v", cerr, serr)
	}
	if client.PeerNodeID != "server-node" || server.PeerNodeID != "client-node" {
		t.Fatalf("node IDs not exchanged: client sees %q, server sees %q", client.PeerNodeID, server.PeerNodeID)
	}
	if client.Epoch != 7 {
		t.Fatalf("epoch = %d, want 7", client.Epoch)
	}

	// Bidirectional message exchange.
	for i := 0; i < 5; i++ {
		msg := []byte("ping")
		errCh := make(chan error, 1)
		go func() { errCh <- client.WriteMsg(msg) }()
		got, err := server.ReadMsg()
		if err != nil {
			t.Fatalf("server ReadMsg: %v", err)
		}
		if werr := <-errCh; werr != nil {
			t.Fatalf("client WriteMsg: %v", werr)
		}
		if !bytes.Equal(got, msg) {
			t.Fatalf("got %q, want %q", got, msg)
		}
	}
}

func TestHandshake_WrongSecretFails(t *testing.T) {
	clientKR := mustKeyring(t, epochSecret{Epoch: 1, Secret: []byte("secret-a")})
	serverKR := mustKeyring(t, epochSecret{Epoch: 1, Secret: []byte("secret-b")})
	_, _, cerr, serr := handshakePair(t, clientKR, serverKR)
	if cerr == nil && serr == nil {
		t.Fatal("expected handshake to fail with mismatched secrets")
	}
}

func TestHandshake_EpochMismatchFails(t *testing.T) {
	clientKR := mustKeyring(t, epochSecret{Epoch: 1, Secret: []byte("secret")})
	serverKR := mustKeyring(t, epochSecret{Epoch: 2, Secret: []byte("secret")})
	_, _, cerr, serr := handshakePair(t, clientKR, serverKR)
	if cerr != ErrUnknownEpoch && serr != ErrUnknownEpoch {
		t.Fatalf("expected ErrUnknownEpoch, got client=%v server=%v", cerr, serr)
	}
}

// replayWriter records bytes written so a frame can be replayed verbatim.
func TestSecureConn_ReplayRejected(t *testing.T) {
	kr := mustKeyring(t, epochSecret{Epoch: 1, Secret: []byte("secret")})

	// Establish a real handshake to get matched send/recv AEADs, then drive
	// the receive side manually through a buffer we control.
	client, server, cerr, serr := handshakePair(t, kr, kr)
	if cerr != nil || serr != nil {
		t.Fatalf("handshake: %v %v", cerr, serr)
	}

	// Capture a sealed frame by sealing through the client's send AEAD at the
	// counter the server next expects.
	var buf bytes.Buffer
	probe := &secureConn{rw: &buf, sendAEAD: client.sendAEAD, recvAEAD: client.recvAEAD, sendCtr: server.recvCtr}
	if err := probe.WriteMsg([]byte("transfer $100")); err != nil {
		t.Fatalf("probe write: %v", err)
	}
	frame := append([]byte(nil), buf.Bytes()...)

	// First delivery: server accepts.
	reader := &secureConn{rw: readOnly{bytes.NewReader(frame)}, recvAEAD: server.recvAEAD, recvCtr: server.recvCtr}
	if _, err := reader.ReadMsg(); err != nil {
		t.Fatalf("first delivery should succeed: %v", err)
	}
	// Replay the SAME bytes: the receive counter has advanced, so the nonce no
	// longer matches and the open fails.
	replay := &secureConn{rw: readOnly{bytes.NewReader(frame)}, recvAEAD: server.recvAEAD, recvCtr: reader.recvCtr}
	if _, err := replay.ReadMsg(); err != ErrAuthFailed {
		t.Fatalf("replay = %v, want ErrAuthFailed", err)
	}
}

func TestSecureConn_FrameTooLarge(t *testing.T) {
	// A length prefix beyond the cap must be rejected without allocating it.
	var buf bytes.Buffer
	buf.Write([]byte{0xFF, 0xFF, 0xFF, 0xFF}) // ~4GiB length
	sc := &secureConn{rw: &buf}
	if _, err := sc.ReadMsg(); err != errFrameTooLarge {
		t.Fatalf("oversize frame = %v, want errFrameTooLarge", err)
	}
}

func FuzzReadHello(f *testing.F) {
	f.Add([]byte{cryptoMagic0, cryptoMagic1, cryptoVersion, 1})
	f.Add([]byte("garbage"))
	f.Add([]byte{})
	f.Fuzz(func(t *testing.T, data []byte) {
		// Must never panic regardless of input.
		_, _, _, _ = readHello(bytes.NewReader(data))
	})
}

func FuzzSecureConnRead(f *testing.F) {
	f.Add([]byte{0, 0, 0, 4, 1, 2, 3, 4})
	f.Fuzz(func(t *testing.T, data []byte) {
		kr := mustKeyring(t, epochSecret{Epoch: 1, Secret: []byte("secret")})
		ek := kr.byEpoch[1]
		aead, _ := newGCM(ek.wireKey)
		sc := &secureConn{rw: readOnly{bytes.NewReader(data)}, recvAEAD: aead}
		// Drain frames; must terminate without panic.
		for {
			if _, err := sc.ReadMsg(); err != nil {
				break
			}
		}
	})
}

// ensure net.Pipe deadlocks are caught: io.ReadWriter assertion
var _ io.ReadWriter = (net.Conn)(nil)
