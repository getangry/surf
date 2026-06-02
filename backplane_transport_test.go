package surf

import (
	"context"
	"errors"
	"net"
	"testing"
)

// echoServer accepts one secure connection, reads one message, and echoes it.
func echoServer(t *testing.T, tr transport, kr *keyring, nodeID string) {
	t.Helper()
	ln, err := tr.Listen()
	if err != nil {
		t.Errorf("Listen: %v", err)
		return
	}
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		sc, err := serverHandshake(conn, kr, nodeID)
		if err != nil {
			return
		}
		msg, err := sc.ReadMsg()
		if err != nil {
			return
		}
		_ = sc.WriteMsg(msg)
	}()
}

func TestMemTransport_SecureRoundTrip(t *testing.T) {
	ctx := context.Background()
	kr := mustKeyring(t, epochSecret{Epoch: 1, Secret: []byte("cluster-secret")})
	netw := newMemNetwork()

	srvTr := netw.transportFor("node-b:1")
	echoServer(t, srvTr, kr, "node-b")

	cliTr := netw.transportFor("node-a:1")
	conn, err := cliTr.Dial(ctx, "node-b:1")
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.Close()
	sc, err := clientHandshake(conn, kr, "node-a")
	if err != nil {
		t.Fatalf("clientHandshake: %v", err)
	}
	if sc.PeerNodeID != "node-b" {
		t.Fatalf("peer node id = %q, want node-b", sc.PeerNodeID)
	}
	if err := sc.WriteMsg([]byte("hello")); err != nil {
		t.Fatalf("WriteMsg: %v", err)
	}
	got, err := sc.ReadMsg()
	if err != nil {
		t.Fatalf("ReadMsg: %v", err)
	}
	if string(got) != "hello" {
		t.Fatalf("echo = %q, want hello", got)
	}
}

func TestMemTransport_DialRefusedWhenNoListener(t *testing.T) {
	netw := newMemNetwork()
	tr := netw.transportFor("a:1")
	_, err := tr.Dial(context.Background(), "missing:1")
	if err == nil {
		t.Fatal("expected dial to a missing listener to fail")
	}
}

func TestMemTransport_Partition(t *testing.T) {
	ctx := context.Background()
	netw := newMemNetwork()

	// b listens; reachable initially.
	bTr := netw.transportFor("b:1")
	if _, err := bTr.Listen(); err != nil {
		t.Fatalf("Listen: %v", err)
	}
	aTr := netw.transportFor("a:1")

	conn, err := aTr.Dial(ctx, "b:1")
	if err != nil {
		t.Fatalf("dial before partition: %v", err)
	}
	conn.Close()

	// Partition a|b.
	netw.partition(partitionSets([]string{"a:1"}, []string{"b:1"}))
	if _, err := aTr.Dial(ctx, "b:1"); !errors.Is(err, errTransportPartitioned) {
		t.Fatalf("dial during partition = %v, want errTransportPartitioned", err)
	}

	// Heal.
	netw.partition(nil)
	conn2, err := aTr.Dial(ctx, "b:1")
	if err != nil {
		t.Fatalf("dial after heal: %v", err)
	}
	conn2.Close()
}

func TestBufferedConnPair_WriteThenRead(t *testing.T) {
	// A buffered write must not block before a read happens — this is the
	// property net.Pipe lacks that caused the handshake deadlock.
	c, s := newBufferedConnPair("a", "b")
	defer c.Close()
	defer s.Close()

	done := make(chan struct{})
	go func() {
		_, _ = c.Write([]byte("one"))
		_, _ = c.Write([]byte("two"))
		close(done)
	}()
	<-done // writes completed without a reader

	buf := make([]byte, 6)
	n, err := s.Read(buf)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if string(buf[:n]) != "onetwo" {
		t.Fatalf("read %q, want onetwo", buf[:n])
	}
}

func TestBufferedConnPair_CloseUnblocksRead(t *testing.T) {
	c, s := newBufferedConnPair("a", "b")
	go func() { c.Close() }()
	buf := make([]byte, 4)
	if _, err := s.Read(buf); err == nil {
		t.Fatal("expected error reading from closed pipe")
	}
}

var _ net.Conn = (*memConn)(nil)
