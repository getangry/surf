package surf

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"sync"
	"sync/atomic"
	"time"
)

// clusterBackplane is the peer-to-peer Backplane: it shares KV state and locks
// across instances with no external datastore. Each node gossips membership,
// replicates writes to peers, and runs anti-entropy to converge after blips.
// All peer traffic is authenticated and encrypted (see backplane_crypto.go).
//
// It implements both Backplane (the public surface) and pinger (so the
// membership layer can probe peers through it).
type clusterBackplane struct {
	nodeID  string
	keyring *keyring
	keyErr  error // key-derivation error from construction, surfaced at start
	cfg     clusterConfig
	disc    Discoverer
	logger  *slog.Logger

	tr      transport
	members *membership
	kv      *kvStore
	clock   *hlc

	// lock arbitration state (see backplane_cluster_lock.go). grants maps a
	// lock key to its current grant; tokenSeq is the per-key fencing-token
	// high-water mark this node has issued.
	lockMu   sync.Mutex
	grants   map[string]*lockGrant
	tokenSeq map[string]uint64

	ctx       context.Context
	cancel    context.CancelFunc
	wg        sync.WaitGroup
	ln        net.Listener
	startOnce sync.Once
	closeOnce sync.Once
	started   atomic.Bool
}

// clusterConfig tunes the clustered backplane. Zero values get defaults.
type clusterConfig struct {
	BindAddr      string // address to listen on (e.g. ":7946" or "0.0.0.0:0")
	AdvertiseAddr string // address peers use to reach us; defaults to resolved listen addr
	NodeID        string // stable id; generated if empty

	ProbeInterval    time.Duration
	GossipInterval   time.Duration // anti-entropy period
	DiscoverInterval time.Duration
	ReapInterval     time.Duration
	TombstoneTTL     time.Duration
	MaxClockDrift    time.Duration
	LeaseDefault     time.Duration

	Membership membershipConfig
}

func (c *clusterConfig) withDefaults() {
	if c.BindAddr == "" {
		c.BindAddr = ":7946"
	}
	if c.ProbeInterval <= 0 {
		c.ProbeInterval = time.Second
	}
	if c.GossipInterval <= 0 {
		c.GossipInterval = 2 * time.Second
	}
	if c.DiscoverInterval <= 0 {
		c.DiscoverInterval = 5 * time.Second
	}
	if c.ReapInterval <= 0 {
		c.ReapInterval = 10 * time.Second
	}
	if c.TombstoneTTL <= 0 {
		c.TombstoneTTL = 24 * time.Hour
	}
	if c.MaxClockDrift <= 0 {
		c.MaxClockDrift = 500 * time.Millisecond
	}
	if c.LeaseDefault <= 0 {
		c.LeaseDefault = 15 * time.Second
	}
}

// ClusterOption configures a clustered backplane.
type ClusterOption func(*clusterConfig)

// WithClusterBindAddr sets the listen address for peer traffic.
func WithClusterBindAddr(addr string) ClusterOption {
	return func(c *clusterConfig) { c.BindAddr = addr }
}

// WithClusterAdvertiseAddr sets the address peers use to reach this node. Use
// it when the bind address is not reachable as-is (e.g. binding 0.0.0.0 but
// advertising a pod IP).
func WithClusterAdvertiseAddr(addr string) ClusterOption {
	return func(c *clusterConfig) { c.AdvertiseAddr = addr }
}

// WithClusterNodeID sets a stable node identifier. If unset, a random id is
// generated at startup.
func WithClusterNodeID(id string) ClusterOption {
	return func(c *clusterConfig) { c.NodeID = id }
}

// WithClusterTuning overrides the timing parameters. Mainly for tests.
func WithClusterTuning(probe, gossip, reap time.Duration) ClusterOption {
	return func(c *clusterConfig) {
		c.ProbeInterval, c.GossipInterval, c.ReapInterval = probe, gossip, reap
	}
}

// WithClusterFailureDetection tunes the failure detector: per-probe timeout,
// how long a peer stays Suspect before being declared Dead, how long a Dead
// peer is retained, and how many relays to use for indirect probes.
func WithClusterFailureDetection(probeTimeout, suspicion, deadEvict time.Duration, indirect int) ClusterOption {
	return func(c *clusterConfig) {
		c.Membership.ProbeTimeout = probeTimeout
		c.Membership.SuspicionTimeout = suspicion
		c.Membership.DeadEvictAfter = deadEvict
		c.Membership.IndirectProbes = indirect
	}
}

// NewClusterBackplane builds a peer-to-peer backplane secured by secret (the
// shared cluster key, e.g. from a Kubernetes Secret). Peers are found via disc.
// Pass it to surf.WithBackplane; NewApp starts and stops it with the app.
//
//	bp := surf.NewClusterBackplane(secret, surf.K8sHeadless("surf", "default", 7946),
//	    surf.WithClusterBindAddr(":7946"))
//	app := surf.NewApp(surf.WithBackplane(bp))
func NewClusterBackplane(secret []byte, disc Discoverer, opts ...ClusterOption) *clusterBackplane {
	return newClusterBackplaneWithKeyring(nil, secret, disc, opts...)
}

// newClusterBackplaneWithKeyring is the internal constructor; transport is
// injected (nil means real TCP) so tests can supply an in-memory network.
func newClusterBackplaneWithKeyring(tr transport, secret []byte, disc Discoverer, opts ...ClusterOption) *clusterBackplane {
	var cfg clusterConfig
	for _, opt := range opts {
		opt(&cfg)
	}
	cfg.withDefaults()
	// The keyring is derived from the single shared secret at epoch 0. Rotation
	// (multiple epochs) is supported by newKeyring but not yet exposed as an
	// option; the epoch byte is reserved on the wire for it.
	kr, err := newKeyring(epochSecret{Epoch: 0, Secret: secret})
	bp := &clusterBackplane{
		keyring:  kr,
		cfg:      cfg,
		disc:     disc,
		tr:       tr,
		grants:   make(map[string]*lockGrant),
		tokenSeq: make(map[string]uint64),
		logger:   slog.Default(),
	}
	// Defer reporting a bad secret to start, where we can log; the zero keyring
	// makes all ops fail closed.
	bp.keyErr = err
	return bp
}

func generateNodeID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// SetLogger lets the embedding app share its logger.
func (n *clusterBackplane) SetLogger(l *slog.Logger) {
	if l != nil {
		n.logger = l
	}
}

// start brings the node up: bind, derive identity, start background loops, and
// seed membership from discovery. It satisfies backplaneStarter so NewApp wires
// it to the app context.
func (n *clusterBackplane) start(ctx context.Context) error {
	var startErr error
	n.startOnce.Do(func() {
		if n.keyErr != nil {
			startErr = n.keyErr
			return
		}
		n.ctx, n.cancel = context.WithCancel(ctx)
		n.nodeID = n.cfg.NodeID
		if n.nodeID == "" {
			n.nodeID = generateNodeID()
		}
		if n.tr == nil {
			n.tr = newTCPTransport(n.cfg.BindAddr)
		}
		ln, err := n.tr.Listen()
		if err != nil {
			startErr = err
			return
		}
		n.ln = ln

		advertise := n.cfg.AdvertiseAddr
		if advertise == "" {
			advertise = n.tr.LocalAddr()
		}
		n.clock = newHLC(n.nodeID, time.Now, n.cfg.MaxClockDrift)
		n.clock.onSkew = func(remoteWall, physical int64) {
			n.logger.Warn("backplane peer clock skew clamped",
				"node", n.nodeID, "remoteWallNanos", remoteWall, "localWallNanos", physical)
		}
		n.kv = newKVStore(n.clock, time.Now, n.cfg.TombstoneTTL)
		n.members = newMembership(
			memberInfo{NodeID: n.nodeID, Addr: advertise},
			n, n.cfg.Membership, n.logger,
		)

		n.started.Store(true)
		n.wg.Add(1)
		go n.acceptLoop()
		n.spawn(n.probeLoop)
		n.spawn(n.gossipLoop)
		n.spawn(n.discoverLoop)
		n.spawn(n.reapLoop)
		n.spawn(n.lockJanitorLoop)

		n.logger.Info("backplane cluster node started",
			"node", n.nodeID, "advertise", advertise)
	})
	return startErr
}

func (n *clusterBackplane) spawn(fn func()) {
	n.wg.Add(1)
	go func() {
		defer n.wg.Done()
		fn()
	}()
}

func (n *clusterBackplane) acceptLoop() {
	defer n.wg.Done()
	for {
		conn, err := n.ln.Accept()
		if err != nil {
			return // listener closed
		}
		go n.handleConn(conn)
	}
}

func (n *clusterBackplane) handleConn(conn net.Conn) {
	defer conn.Close()
	sc, err := serverHandshake(conn, n.keyring, n.nodeID)
	if err != nil {
		n.logger.Debug("backplane inbound handshake failed", "error", err)
		return
	}
	for {
		data, err := sc.ReadMsg()
		if err != nil {
			return
		}
		var req wireMsg
		if err := json.Unmarshal(data, &req); err != nil {
			return
		}
		resp := n.dispatch(&req)
		out, err := json.Marshal(resp)
		if err != nil {
			return
		}
		if err := sc.WriteMsg(out); err != nil {
			return
		}
	}
}

func (n *clusterBackplane) dispatch(req *wireMsg) *wireMsg {
	switch req.Kind {
	case kindPing:
		return &wireMsg{Kind: kindPing, Gossip: n.members.handlePing(req.Gossip)}
	case kindIndirectPing:
		return &wireMsg{Kind: kindIndirectPing, OK: n.doIndirectPing(req.Target)}
	case kindPush:
		for _, e := range req.Entries {
			n.kv.mergeRemote(e)
		}
		return &wireMsg{Kind: kindPush}
	case kindSync:
		return &wireMsg{
			Kind:    kindSync,
			Entries: n.kv.entriesNewerThan(req.Digest),
			Digest:  n.kv.digest(),
		}
	case kindLockAcquire:
		return n.handleLockAcquire(req)
	case kindLockRefresh:
		return n.handleLockRefresh(req)
	case kindLockRelease:
		return n.handleLockRelease(req)
	default:
		return &wireMsg{Err: "unknown message kind"}
	}
}

// rpc dials addr, runs the secure handshake, sends req, and returns the
// response. The connection is short-lived: one request/response per dial.
func (n *clusterBackplane) rpc(ctx context.Context, addr string, req *wireMsg) (*wireMsg, error) {
	conn, err := n.tr.Dial(ctx, addr)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	sc, err := clientHandshake(conn, n.keyring, n.nodeID)
	if err != nil {
		return nil, err
	}
	data, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	if err := sc.WriteMsg(data); err != nil {
		return nil, err
	}
	respData, err := sc.ReadMsg()
	if err != nil {
		return nil, err
	}
	var resp wireMsg
	if err := json.Unmarshal(respData, &resp); err != nil {
		return nil, err
	}
	if resp.Err != "" {
		return &resp, errors.New(resp.Err)
	}
	return &resp, nil
}

// --- pinger implementation -------------------------------------------------

func (n *clusterBackplane) ping(ctx context.Context, addr string, gossip []memberInfo) ([]memberInfo, error) {
	resp, err := n.rpc(ctx, addr, &wireMsg{Kind: kindPing, Gossip: gossip})
	if err != nil {
		return nil, err
	}
	return resp.Gossip, nil
}

func (n *clusterBackplane) indirectPing(ctx context.Context, relay, target string) (bool, error) {
	resp, err := n.rpc(ctx, relay, &wireMsg{Kind: kindIndirectPing, Target: target})
	if err != nil {
		return false, err
	}
	return resp.OK, nil
}

// doIndirectPing is the relay side: probe target directly and report whether it
// answered.
func (n *clusterBackplane) doIndirectPing(target string) bool {
	ctx, cancel := context.WithTimeout(n.ctx, n.cfg.Membership.ProbeTimeout+time.Second)
	defer cancel()
	_, err := n.ping(ctx, target, n.members.snapshot())
	return err == nil
}

// --- KV replication --------------------------------------------------------

// replicate pushes a locally-originated record to all alive peers,
// best-effort. Anti-entropy backstops any push that fails.
func (n *clusterBackplane) replicate(e entryMsg) {
	for _, m := range n.members.aliveMembers() {
		if m.NodeID == n.nodeID {
			continue
		}
		addr := m.Addr
		n.spawn(func() {
			ctx, cancel := context.WithTimeout(n.ctx, 2*time.Second)
			defer cancel()
			if _, err := n.rpc(ctx, addr, &wireMsg{Kind: kindPush, Entries: []entryMsg{e}}); err != nil {
				n.logger.Debug("backplane push failed", "addr", addr, "error", err)
			}
		})
	}
}

// --- Backplane: KV ---------------------------------------------------------

func (n *clusterBackplane) Get(ctx context.Context, key string) ([]byte, bool, error) {
	if !n.started.Load() {
		return nil, false, ErrBackplaneClosed
	}
	sealed, ok := n.kv.localGet(key)
	if !ok {
		return nil, false, nil
	}
	plain, err := n.keyring.openAtRest(sealed)
	if err != nil {
		return nil, false, err
	}
	return plain, true, nil
}

func (n *clusterBackplane) Set(ctx context.Context, key string, val []byte, ttl time.Duration) error {
	if !n.started.Load() {
		return ErrBackplaneClosed
	}
	sealed, err := n.keyring.sealAtRest(val)
	if err != nil {
		return err
	}
	e := n.kv.localSet(key, sealed, ttl)
	n.replicate(e)
	return nil
}

func (n *clusterBackplane) Delete(ctx context.Context, key string) error {
	if !n.started.Load() {
		return ErrBackplaneClosed
	}
	e := n.kv.localDelete(key)
	n.replicate(e)
	return nil
}

// Size implements ClusterSizer, returning the number of instances currently
// believed alive (including self).
func (n *clusterBackplane) Size() int {
	if !n.started.Load() {
		return 0
	}
	return len(n.members.aliveMembers())
}

func (n *clusterBackplane) Close() error {
	n.closeOnce.Do(func() {
		if n.cancel != nil {
			n.cancel()
		}
		if n.ln != nil {
			n.ln.Close()
		}
		if n.tr != nil {
			n.tr.Close()
		}
		n.wg.Wait()
		n.started.Store(false)
	})
	return nil
}

// --- background loops ------------------------------------------------------

func (n *clusterBackplane) probeLoop() {
	t := time.NewTicker(n.cfg.ProbeInterval)
	defer t.Stop()
	for {
		select {
		case <-n.ctx.Done():
			return
		case <-t.C:
			n.members.probeOnce(n.ctx)
		}
	}
}

func (n *clusterBackplane) gossipLoop() {
	t := time.NewTicker(n.cfg.GossipInterval)
	defer t.Stop()
	for {
		select {
		case <-n.ctx.Done():
			return
		case <-t.C:
			n.antiEntropyRound()
		}
	}
}

// antiEntropyRound pulls newer entries from a random peer and pushes back any
// entries that peer is missing, converging both directions in one round.
func (n *clusterBackplane) antiEntropyRound() {
	peers := n.members.aliveMembers()
	var addrs []string
	for _, m := range peers {
		if m.NodeID != n.nodeID {
			addrs = append(addrs, m.Addr)
		}
	}
	if len(addrs) == 0 {
		return
	}
	addr := addrs[n.members.randIntn(len(addrs))]

	ctx, cancel := context.WithTimeout(n.ctx, 3*time.Second)
	defer cancel()
	resp, err := n.rpc(ctx, addr, &wireMsg{Kind: kindSync, Digest: n.kv.digest()})
	if err != nil {
		return
	}
	for _, e := range resp.Entries {
		n.kv.mergeRemote(e)
	}
	// Push entries the peer is missing/older on, per the digest it returned.
	if mine := n.kv.entriesNewerThan(resp.Digest); len(mine) > 0 {
		_, _ = n.rpc(ctx, addr, &wireMsg{Kind: kindPush, Entries: mine})
	}
}

func (n *clusterBackplane) discoverLoop() {
	n.discoverOnce() // seed immediately on start
	t := time.NewTicker(n.cfg.DiscoverInterval)
	defer t.Stop()
	for {
		select {
		case <-n.ctx.Done():
			return
		case <-t.C:
			n.discoverOnce()
		}
	}
}

func (n *clusterBackplane) discoverOnce() {
	if n.disc == nil {
		return
	}
	ctx, cancel := context.WithTimeout(n.ctx, 3*time.Second)
	defer cancel()
	peers, err := n.disc.Peers(ctx)
	if err != nil {
		n.logger.Debug("backplane discovery failed", "error", err)
		return
	}
	for _, addr := range peers {
		n.members.addCandidate(addr)
	}
}

func (n *clusterBackplane) reapLoop() {
	t := time.NewTicker(n.cfg.ReapInterval)
	defer t.Stop()
	for {
		select {
		case <-n.ctx.Done():
			return
		case <-t.C:
			n.members.reap()
			n.kv.reap()
		}
	}
}
