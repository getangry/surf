package surf

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"sync"
	"time"
)

// transport is the seam between the clustered backplane and the network. The
// production implementation is plain TCP; tests use an in-memory transport that
// can simulate latency and partitions deterministically, with no real sockets.
//
// The backplane uses short-lived request/response connections: dial, run the
// secure handshake, exchange one or a few messages, close. This keeps the model
// simple — a partition is just a failed Dial — at the cost of a handshake per
// exchange. Connection pooling is a possible later optimization.
type transport interface {
	// Dial opens a raw connection to addr. The caller layers a secureConn on
	// top via clientHandshake.
	Dial(ctx context.Context, addr string) (net.Conn, error)

	// Listen returns a listener for inbound connections to LocalAddr. The
	// caller wraps accepted connections via serverHandshake.
	Listen() (net.Listener, error)

	// LocalAddr is the address peers use to reach this node.
	LocalAddr() string

	// Close releases the transport's resources.
	Close() error
}

// errTransportPartitioned is returned by the in-memory transport when a dial is
// blocked by a simulated partition.
var errTransportPartitioned = errors.New("surf: backplane transport partitioned")

// memListenerBacklog mirrors a TCP accept backlog: dials enqueue here and
// return without waiting for Accept, up to this depth.
const memListenerBacklog = 128

// tcpTransport is the production transport: TCP dial and listen.
type tcpTransport struct {
	addr   string
	dialer net.Dialer

	mu sync.Mutex
	ln net.Listener
}

// newTCPTransport returns a transport bound to addr ("host:port"). The listener
// is created lazily by Listen so LocalAddr can be reported before binding.
func newTCPTransport(addr string) *tcpTransport {
	return &tcpTransport{addr: addr}
}

func (t *tcpTransport) Dial(ctx context.Context, addr string) (net.Conn, error) {
	return t.dialer.DialContext(ctx, "tcp", addr)
}

func (t *tcpTransport) Listen() (net.Listener, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.ln != nil {
		return t.ln, nil
	}
	ln, err := net.Listen("tcp", t.addr)
	if err != nil {
		return nil, err
	}
	t.ln = ln
	// Adopt the resolved address (e.g. when addr used :0 for an ephemeral port).
	t.addr = ln.Addr().String()
	return ln, nil
}

func (t *tcpTransport) LocalAddr() string { return t.addr }

func (t *tcpTransport) Close() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.ln != nil {
		return t.ln.Close()
	}
	return nil
}

// memNetwork is an in-memory network shared by several memTransports. It routes
// dials to registered listeners over net.Pipe connections and can drop traffic
// between configured pairs to simulate partitions.
type memNetwork struct {
	mu        sync.Mutex
	listeners map[string]*memListener
	// blocked reports whether a dial from->to should fail. When nil, nothing
	// is blocked.
	blocked func(from, to string) bool
	// latency, if set, delays each successful dial.
	latency time.Duration
}

func newMemNetwork() *memNetwork {
	return &memNetwork{listeners: make(map[string]*memListener)}
}

// transportFor returns a transport endpoint for addr on this network.
func (n *memNetwork) transportFor(addr string) *memTransport {
	return &memTransport{net: n, addr: addr}
}

// partition installs a predicate deciding which directed dials are blocked.
// Pass nil to heal all partitions.
func (n *memNetwork) partition(blocked func(from, to string) bool) {
	n.mu.Lock()
	n.blocked = blocked
	n.mu.Unlock()
}

// isolate is a convenience that blocks all traffic in both directions between
// the two given address sets.
func partitionSets(a, b []string) func(from, to string) bool {
	in := func(s []string, x string) bool {
		for _, v := range s {
			if v == x {
				return true
			}
		}
		return false
	}
	return func(from, to string) bool {
		return (in(a, from) && in(b, to)) || (in(b, from) && in(a, to))
	}
}

type memTransport struct {
	net  *memNetwork
	addr string
}

func (t *memTransport) Dial(ctx context.Context, addr string) (net.Conn, error) {
	t.net.mu.Lock()
	blocked := t.net.blocked
	latency := t.net.latency
	ln := t.net.listeners[addr]
	t.net.mu.Unlock()

	if blocked != nil && blocked(t.addr, addr) {
		return nil, errTransportPartitioned
	}
	if ln == nil {
		return nil, &net.OpError{Op: "dial", Net: "mem", Err: errors.New("connection refused")}
	}
	if latency > 0 {
		timer := time.NewTimer(latency)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
	client, server := newBufferedConnPair(t.addr, addr)
	select {
	case ln.accept <- server:
		return client, nil
	case <-ctx.Done():
		client.Close()
		server.Close()
		return nil, ctx.Err()
	case <-ln.closed:
		client.Close()
		server.Close()
		return nil, errors.New("surf: listener closed")
	}
}

func (t *memTransport) Listen() (net.Listener, error) {
	ln := &memListener{
		addr:   memAddr(t.addr),
		accept: make(chan net.Conn, memListenerBacklog),
		closed: make(chan struct{}),
	}
	t.net.mu.Lock()
	t.net.listeners[t.addr] = ln
	t.net.mu.Unlock()
	return ln, nil
}

func (t *memTransport) LocalAddr() string { return t.addr }

func (t *memTransport) Close() error {
	t.net.mu.Lock()
	ln := t.net.listeners[t.addr]
	delete(t.net.listeners, t.addr)
	t.net.mu.Unlock()
	if ln != nil {
		return ln.Close()
	}
	return nil
}

type memAddr string

func (a memAddr) Network() string { return "mem" }
func (a memAddr) String() string  { return string(a) }

type memListener struct {
	addr      memAddr
	accept    chan net.Conn
	closed    chan struct{}
	closeOnce sync.Once
}

func (l *memListener) Accept() (net.Conn, error) {
	select {
	case c := <-l.accept:
		return c, nil
	case <-l.closed:
		return nil, net.ErrClosed
	}
}

func (l *memListener) Close() error {
	l.closeOnce.Do(func() { close(l.closed) })
	return nil
}

func (l *memListener) Addr() net.Addr { return l.addr }

// bufferedPipe is one direction of an in-memory connection. Unlike net.Pipe it
// buffers, so a Write never blocks waiting for a matching Read — this mirrors a
// TCP socket's send buffer and is what lets the secure handshake (which writes
// then reads on both ends) complete without deadlocking.
type bufferedPipe struct {
	mu     sync.Mutex
	cond   *sync.Cond
	buf    bytes.Buffer
	closed bool
}

func newBufferedPipe() *bufferedPipe {
	p := &bufferedPipe{}
	p.cond = sync.NewCond(&p.mu)
	return p
}

func (p *bufferedPipe) Write(b []byte) (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return 0, io.ErrClosedPipe
	}
	n, _ := p.buf.Write(b)
	p.cond.Signal()
	return n, nil
}

func (p *bufferedPipe) Read(b []byte) (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for p.buf.Len() == 0 && !p.closed {
		p.cond.Wait()
	}
	if p.buf.Len() == 0 && p.closed {
		return 0, io.EOF
	}
	return p.buf.Read(b)
}

func (p *bufferedPipe) close() {
	p.mu.Lock()
	p.closed = true
	p.cond.Broadcast()
	p.mu.Unlock()
}

// memConn is one endpoint of an in-memory connection: it reads from one
// bufferedPipe and writes to the other. The two endpoints share the pipes
// crosswise.
type memConn struct {
	r         *bufferedPipe
	w         *bufferedPipe
	local     memAddr
	remote    memAddr
	closeOnce sync.Once
}

// newBufferedConnPair returns the two ends of a buffered in-memory connection.
func newBufferedConnPair(addrA, addrB string) (clientSide, serverSide net.Conn) {
	a2b := newBufferedPipe()
	b2a := newBufferedPipe()
	client := &memConn{r: b2a, w: a2b, local: memAddr(addrA), remote: memAddr(addrB)}
	server := &memConn{r: a2b, w: b2a, local: memAddr(addrB), remote: memAddr(addrA)}
	return client, server
}

func (c *memConn) Read(b []byte) (int, error)  { return c.r.Read(b) }
func (c *memConn) Write(b []byte) (int, error) { return c.w.Write(b) }

func (c *memConn) Close() error {
	c.closeOnce.Do(func() {
		c.w.close()
		c.r.close()
	})
	return nil
}

func (c *memConn) LocalAddr() net.Addr                { return c.local }
func (c *memConn) RemoteAddr() net.Addr               { return c.remote }
func (c *memConn) SetDeadline(t time.Time) error      { return nil }
func (c *memConn) SetReadDeadline(t time.Time) error  { return nil }
func (c *memConn) SetWriteDeadline(t time.Time) error { return nil }
