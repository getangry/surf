package surf

import (
	"context"
	"log/slog"
	"math/rand"
	"sort"
	"sync"
	"time"
)

// This file implements SWIM-lite: a lightweight membership and failure-detection
// protocol that tells the rest of the clustered backplane which peers are
// currently alive. The live set is the input to anti-entropy peer selection
// (KV) and arbiter election (locks).
//
// Three ideas from SWIM are load-bearing and are why naive heartbeating
// flaps under load:
//
//   - Indirect probing: before declaring a peer dead, ask other peers to probe
//     it. A peer is only suspected when nobody can reach it, which absorbs GC
//     pauses, CPU starvation, and asymmetric network blips.
//   - Suspicion: a missed probe marks a peer Suspect, not Dead. It only becomes
//     Dead after a timeout with no refutation.
//   - Incarnation numbers: a node that hears itself suspected bumps its own
//     incarnation and re-asserts Alive, which overrides the stale suspicion
//     everywhere. This stops oscillation.
//
// Membership can still disagree across nodes during a partition — that is
// fundamental without consensus, and is exactly why the distributed lock built
// on top of this is not partition-safe (see backplane_cluster_lock.go).

type memberState uint8

const (
	stateAlive memberState = iota
	stateSuspect
	stateDead
)

func (s memberState) String() string {
	switch s {
	case stateAlive:
		return "alive"
	case stateSuspect:
		return "suspect"
	case stateDead:
		return "dead"
	default:
		return "unknown"
	}
}

// memberInfo is the gossiped view of one node. It is the unit exchanged in ping
// payloads.
type memberInfo struct {
	NodeID      string      `json:"id"`
	Addr        string      `json:"addr"`
	Incarnation uint64      `json:"inc"`
	State       memberState `json:"st"`
}

// member is the local record for a peer, adding bookkeeping not gossiped.
type member struct {
	memberInfo
	// suspectSince is when the node entered Suspect locally; used to promote it
	// to Dead after the suspicion timeout.
	suspectSince time.Time
}

// pinger probes peers on behalf of the membership layer. It is an interface so
// membership is testable without a real transport: the cluster node implements
// it over secureConn, and tests provide an in-memory router.
type pinger interface {
	// ping health-checks addr directly, sending our gossip and returning the
	// peer's gossip on success.
	ping(ctx context.Context, addr string, gossip []memberInfo) ([]memberInfo, error)
	// indirectPing asks relay to probe target on our behalf, returning whether
	// the relay could reach target.
	indirectPing(ctx context.Context, relay, target string) (bool, error)
}

// membershipConfig tunes the failure detector. Zero values fall back to
// production defaults in newMembership.
type membershipConfig struct {
	ProbeTimeout     time.Duration // per-probe deadline
	SuspicionTimeout time.Duration // Suspect -> Dead delay
	IndirectProbes   int           // relays asked when a direct probe fails
	DeadEvictAfter   time.Duration // remove Dead members after this long
}

func (c *membershipConfig) withDefaults() {
	if c.ProbeTimeout <= 0 {
		c.ProbeTimeout = time.Second
	}
	if c.SuspicionTimeout <= 0 {
		c.SuspicionTimeout = 5 * time.Second
	}
	if c.IndirectProbes <= 0 {
		c.IndirectProbes = 3
	}
	if c.DeadEvictAfter <= 0 {
		c.DeadEvictAfter = time.Minute
	}
}

type membership struct {
	mu      sync.Mutex
	self    memberInfo
	members map[string]*member // keyed by NodeID (peers, never self)
	// candidates are addresses learned from discovery whose NodeID we do not
	// yet know; they are probed until they enter members.
	candidates map[string]struct{}

	pinger pinger
	cfg    membershipConfig
	now    func() time.Time

	rngMu  sync.Mutex
	rng    *rand.Rand
	logger *slog.Logger
}

// randIntn returns a random index in [0,n) under the rng mutex. *rand.Rand is
// not safe for concurrent use and several loops draw from it.
func (m *membership) randIntn(n int) int {
	if n <= 0 {
		return 0
	}
	m.rngMu.Lock()
	defer m.rngMu.Unlock()
	return m.rng.Intn(n)
}

func newMembership(self memberInfo, p pinger, cfg membershipConfig, logger *slog.Logger) *membership {
	cfg.withDefaults()
	self.State = stateAlive
	if self.Incarnation == 0 {
		self.Incarnation = 1
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &membership{
		self:       self,
		members:    make(map[string]*member),
		candidates: make(map[string]struct{}),
		pinger:     p,
		cfg:        cfg,
		now:        time.Now,
		rng:        rand.New(rand.NewSource(1)),
		logger:     logger,
	}
}

// addCandidate registers a peer address discovered out of band (e.g. via a
// Discoverer) so it will be probed and, once reachable, learned as a member.
func (m *membership) addCandidate(addr string) {
	if addr == "" || addr == m.self.Addr {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, mem := range m.members {
		if mem.Addr == addr {
			return // already a known member
		}
	}
	m.candidates[addr] = struct{}{}
}

// aliveMembers returns the addresses of all members currently believed alive,
// including self. The result is sorted for determinism (HRW election depends on
// a stable set, not order, but sorted output keeps tests and logs stable).
func (m *membership) aliveMembers() []memberInfo {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]memberInfo, 0, len(m.members)+1)
	out = append(out, m.self)
	for _, mem := range m.members {
		if mem.State == stateAlive {
			out = append(out, mem.memberInfo)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].NodeID < out[j].NodeID })
	return out
}

// snapshot returns self plus all known members as gossip, for sending in a ping.
func (m *membership) snapshot() []memberInfo {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]memberInfo, 0, len(m.members)+1)
	out = append(out, m.self)
	for _, mem := range m.members {
		out = append(out, mem.memberInfo)
	}
	return out
}

// merge applies incoming gossip using SWIM precedence rules. It must be called
// without holding m.mu.
func (m *membership) merge(infos []memberInfo) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, in := range infos {
		if in.NodeID == m.self.NodeID {
			m.refuteLocked(in)
			continue
		}
		m.mergeOneLocked(in)
	}
}

// refuteLocked handles gossip about ourselves: any claim that we are Suspect or
// Dead at an incarnation >= ours is overridden by bumping our incarnation.
func (m *membership) refuteLocked(in memberInfo) {
	if in.State != stateAlive && in.Incarnation >= m.self.Incarnation {
		m.self.Incarnation = in.Incarnation + 1
		m.self.State = stateAlive
		m.logger.Debug("backplane refuting suspicion of self",
			"newIncarnation", m.self.Incarnation)
	}
}

func (m *membership) mergeOneLocked(in memberInfo) {
	cur, ok := m.members[in.NodeID]
	if !ok {
		// First time we hear of this node. Ignore a Dead-on-arrival record so a
		// recently-removed node doesn't resurrect as a tombstone.
		if in.State == stateDead {
			return
		}
		m.members[in.NodeID] = &member{memberInfo: in}
		delete(m.candidates, in.Addr)
		return
	}
	// Address can change across restarts; keep the freshest.
	if in.Incarnation > cur.Incarnation {
		cur.Addr = in.Addr
	}
	switch in.State {
	case stateAlive:
		if in.Incarnation > cur.Incarnation {
			cur.Incarnation = in.Incarnation
			cur.State = stateAlive
			cur.suspectSince = time.Time{}
		}
	case stateSuspect:
		if in.Incarnation > cur.Incarnation ||
			(in.Incarnation == cur.Incarnation && cur.State == stateAlive) {
			cur.Incarnation = in.Incarnation
			cur.State = stateSuspect
			cur.suspectSince = m.now()
		}
	case stateDead:
		if in.Incarnation >= cur.Incarnation {
			cur.Incarnation = in.Incarnation
			cur.State = stateDead
		}
	}
}

// probeAddrs returns the set of addresses worth probing this round: known
// non-dead members and unlearned candidates.
func (m *membership) probeAddrs() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	addrs := make([]string, 0, len(m.members)+len(m.candidates))
	for _, mem := range m.members {
		if mem.State != stateDead {
			addrs = append(addrs, mem.Addr)
		}
	}
	for addr := range m.candidates {
		addrs = append(addrs, addr)
	}
	return addrs
}

// probeOnce picks a random probe target and health-checks it. It is the unit of
// work the background loop repeats; tests can also call it directly.
func (m *membership) probeOnce(ctx context.Context) {
	addrs := m.probeAddrs()
	if len(addrs) == 0 {
		return
	}
	target := addrs[m.randIntn(len(addrs))]
	m.probeAddr(ctx, target)
}

// probeAddr health-checks a specific address: a direct ping, then indirect
// pings through other members, and finally a suspicion if all fail.
func (m *membership) probeAddr(ctx context.Context, addr string) {
	pctx, cancel := context.WithTimeout(ctx, m.cfg.ProbeTimeout)
	gossip, err := m.pinger.ping(pctx, addr, m.snapshot())
	cancel()
	if err == nil {
		m.merge(gossip)
		return
	}

	// Direct probe failed: ask relays before suspecting.
	relays := m.pickRelays(addr, m.cfg.IndirectProbes)
	for _, relay := range relays {
		rctx, rcancel := context.WithTimeout(ctx, m.cfg.ProbeTimeout)
		ok, ierr := m.pinger.indirectPing(rctx, relay, addr)
		rcancel()
		if ierr == nil && ok {
			return // reachable via a relay; not a failure
		}
	}
	m.suspect(addr)
}

// pickRelays returns up to n alive member addresses other than self and target,
// for indirect probing.
func (m *membership) pickRelays(target string, n int) []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	var pool []string
	for _, mem := range m.members {
		if mem.State == stateAlive && mem.Addr != target {
			pool = append(pool, mem.Addr)
		}
	}
	m.rngMu.Lock()
	m.rng.Shuffle(len(pool), func(i, j int) { pool[i], pool[j] = pool[j], pool[i] })
	m.rngMu.Unlock()
	if len(pool) > n {
		pool = pool[:n]
	}
	return pool
}

// suspect marks the member at addr as Suspect (at its current incarnation),
// starting the countdown to Dead.
func (m *membership) suspect(addr string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, mem := range m.members {
		if mem.Addr == addr && mem.State == stateAlive {
			mem.State = stateSuspect
			mem.suspectSince = m.now()
			m.logger.Debug("backplane suspecting peer", "addr", addr, "node", mem.NodeID)
			return
		}
	}
}

// reap promotes Suspect members to Dead once the suspicion timeout elapses and
// evicts long-Dead members so the table does not grow without bound.
func (m *membership) reap() {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := m.now()
	for id, mem := range m.members {
		switch mem.State {
		case stateSuspect:
			if !mem.suspectSince.IsZero() && now.Sub(mem.suspectSince) >= m.cfg.SuspicionTimeout {
				mem.State = stateDead
				mem.suspectSince = now // reuse as time-of-death
				m.logger.Info("backplane peer declared dead", "addr", mem.Addr, "node", mem.NodeID)
			}
		case stateDead:
			if !mem.suspectSince.IsZero() && now.Sub(mem.suspectSince) >= m.cfg.DeadEvictAfter {
				delete(m.members, id)
			}
		}
	}
}

// handlePing is the inbound side of a ping: merge the caller's gossip and
// return our own snapshot for them to merge. The node calls this when it
// receives a ping RPC.
func (m *membership) handlePing(gossip []memberInfo) []memberInfo {
	m.merge(gossip)
	return m.snapshot()
}

// selfInfo returns the node's own gossip record.
func (m *membership) selfInfo() memberInfo {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.self
}
