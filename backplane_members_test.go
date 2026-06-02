package surf

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

// routerPinger routes pings between in-memory membership instances, optionally
// dropping traffic to simulate partitions. It is the test stand-in for the
// transport-backed pinger.
type routerPinger struct {
	mu      sync.Mutex
	nodes   map[string]*membership // addr -> membership
	blocked func(from, to string) bool
	self    string
}

func (p *routerPinger) reachable(from, to string) (*membership, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.blocked != nil && p.blocked(from, to) {
		return nil, false
	}
	m, ok := p.nodes[to]
	return m, ok
}

func (p *routerPinger) ping(ctx context.Context, addr string, gossip []memberInfo) ([]memberInfo, error) {
	target, ok := p.reachable(p.self, addr)
	if !ok {
		return nil, errors.New("unreachable")
	}
	return target.handlePing(gossip), nil
}

func (p *routerPinger) indirectPing(ctx context.Context, relay, target string) (bool, error) {
	if _, ok := p.reachable(p.self, relay); !ok {
		return false, errors.New("relay unreachable")
	}
	// The relay probes the target from its own vantage point.
	_, ok := p.reachable(relay, target)
	return ok, nil
}

func newClock(start time.Time) (*time.Time, func() time.Time) {
	t := start
	return &t, func() time.Time { return t }
}

func TestMembership_MergeAddsNewMember(t *testing.T) {
	m := newMembership(memberInfo{NodeID: "self", Addr: "self:1"}, nil, membershipConfig{}, nil)
	m.merge([]memberInfo{{NodeID: "b", Addr: "b:1", Incarnation: 1, State: stateAlive}})
	alive := m.aliveMembers()
	if len(alive) != 2 {
		t.Fatalf("alive = %d, want 2 (self + b)", len(alive))
	}
}

func TestMembership_AliveHigherIncarnationWins(t *testing.T) {
	m := newMembership(memberInfo{NodeID: "self", Addr: "self:1"}, nil, membershipConfig{}, nil)
	m.merge([]memberInfo{{NodeID: "b", Addr: "b:1", Incarnation: 1, State: stateSuspect}})
	// A higher-incarnation Alive overrides the suspicion.
	m.merge([]memberInfo{{NodeID: "b", Addr: "b:1", Incarnation: 2, State: stateAlive}})
	if got := m.members["b"].State; got != stateAlive {
		t.Fatalf("state = %v, want alive", got)
	}
}

func TestMembership_SuspectEqualIncarnationOverridesAlive(t *testing.T) {
	m := newMembership(memberInfo{NodeID: "self", Addr: "self:1"}, nil, membershipConfig{}, nil)
	m.merge([]memberInfo{{NodeID: "b", Addr: "b:1", Incarnation: 3, State: stateAlive}})
	m.merge([]memberInfo{{NodeID: "b", Addr: "b:1", Incarnation: 3, State: stateSuspect}})
	if got := m.members["b"].State; got != stateSuspect {
		t.Fatalf("state = %v, want suspect", got)
	}
	// But a stale (lower-incarnation) suspicion must not override Alive.
	m.merge([]memberInfo{{NodeID: "b", Addr: "b:1", Incarnation: 5, State: stateAlive}})
	m.merge([]memberInfo{{NodeID: "b", Addr: "b:1", Incarnation: 4, State: stateSuspect}})
	if got := m.members["b"].State; got != stateAlive {
		t.Fatalf("stale suspicion applied: state = %v, want alive", got)
	}
}

func TestMembership_RefutesSelfSuspicion(t *testing.T) {
	m := newMembership(memberInfo{NodeID: "self", Addr: "self:1", Incarnation: 4}, nil, membershipConfig{}, nil)
	m.merge([]memberInfo{{NodeID: "self", Addr: "self:1", Incarnation: 4, State: stateSuspect}})
	if got := m.selfInfo().Incarnation; got <= 4 {
		t.Fatalf("self incarnation = %d, want > 4 after refutation", got)
	}
	if m.selfInfo().State != stateAlive {
		t.Fatal("self not alive after refutation")
	}
}

func TestMembership_ProbeSuccessMergesGossip(t *testing.T) {
	router := &routerPinger{nodes: map[string]*membership{}, self: "a:1"}
	a := newMembership(memberInfo{NodeID: "a", Addr: "a:1"}, router, membershipConfig{}, nil)
	b := newMembership(memberInfo{NodeID: "b", Addr: "b:1"}, &routerPinger{nodes: nil, self: "b:1"}, membershipConfig{}, nil)
	router.nodes["a:1"] = a
	router.nodes["b:1"] = b

	a.addCandidate("b:1")
	a.probeAddr(context.Background(), "b:1")

	if _, ok := a.members["b"]; !ok {
		t.Fatal("a did not learn b via probe")
	}
	if len(a.candidates) != 0 {
		t.Fatalf("candidate not promoted: %v", a.candidates)
	}
}

func TestMembership_ProbeFailureSuspects(t *testing.T) {
	router := &routerPinger{nodes: map[string]*membership{}, self: "a:1"}
	a := newMembership(memberInfo{NodeID: "a", Addr: "a:1"}, router, membershipConfig{}, nil)
	router.nodes["a:1"] = a
	// b is a known alive member but has no live node behind it.
	a.merge([]memberInfo{{NodeID: "b", Addr: "b:1", Incarnation: 1, State: stateAlive}})

	a.probeAddr(context.Background(), "b:1")
	if got := a.members["b"].State; got != stateSuspect {
		t.Fatalf("state = %v, want suspect after unanswered probe", got)
	}
}

func TestMembership_IndirectProbeRescuesFromSuspicion(t *testing.T) {
	// a cannot reach b directly (partitioned a->b), but relay c can.
	router := &routerPinger{nodes: map[string]*membership{}, self: "a:1"}
	a := newMembership(memberInfo{NodeID: "a", Addr: "a:1"}, router, membershipConfig{}, nil)
	b := newMembership(memberInfo{NodeID: "b", Addr: "b:1"}, &routerPinger{self: "b:1"}, membershipConfig{}, nil)
	c := newMembership(memberInfo{NodeID: "c", Addr: "c:1"}, &routerPinger{self: "c:1"}, membershipConfig{}, nil)
	router.nodes["a:1"], router.nodes["b:1"], router.nodes["c:1"] = a, b, c
	router.blocked = func(from, to string) bool {
		return from == "a:1" && to == "b:1" // only a's direct path to b is down
	}

	a.merge([]memberInfo{
		{NodeID: "b", Addr: "b:1", Incarnation: 1, State: stateAlive},
		{NodeID: "c", Addr: "c:1", Incarnation: 1, State: stateAlive},
	})
	a.probeAddr(context.Background(), "b:1")
	if got := a.members["b"].State; got != stateAlive {
		t.Fatalf("state = %v, want alive (rescued by indirect probe via c)", got)
	}
}

func TestMembership_ReapSuspectToDeadAndEvict(t *testing.T) {
	clkPtr, nowFn := newClock(time.Unix(1000, 0))
	m := newMembership(memberInfo{NodeID: "self", Addr: "self:1"}, nil,
		membershipConfig{SuspicionTimeout: 5 * time.Second, DeadEvictAfter: 30 * time.Second}, nil)
	m.now = nowFn

	m.merge([]memberInfo{{NodeID: "b", Addr: "b:1", Incarnation: 1, State: stateAlive}})
	m.suspect("b:1")
	if m.members["b"].State != stateSuspect {
		t.Fatal("b not suspected")
	}

	// Not yet past the suspicion timeout.
	*clkPtr = time.Unix(1003, 0)
	m.reap()
	if m.members["b"].State != stateSuspect {
		t.Fatal("b promoted to dead too early")
	}

	// Past the suspicion timeout: becomes Dead.
	*clkPtr = time.Unix(1006, 0)
	m.reap()
	if m.members["b"].State != stateDead {
		t.Fatalf("b state = %v, want dead", m.members["b"].State)
	}

	// Past the eviction window: removed entirely.
	*clkPtr = time.Unix(1040, 0)
	m.reap()
	if _, ok := m.members["b"]; ok {
		t.Fatal("dead member not evicted")
	}
}

func TestMembership_TwoNodeConvergence(t *testing.T) {
	router := &routerPinger{nodes: map[string]*membership{}, self: ""}
	// Each membership needs its own router view with the right "self" address.
	ra := &routerPinger{nodes: router.nodes, self: "a:1"}
	rb := &routerPinger{nodes: router.nodes, self: "b:1"}
	a := newMembership(memberInfo{NodeID: "a", Addr: "a:1"}, ra, membershipConfig{}, nil)
	b := newMembership(memberInfo{NodeID: "b", Addr: "b:1"}, rb, membershipConfig{}, nil)
	router.nodes["a:1"], router.nodes["b:1"] = a, b

	a.addCandidate("b:1")
	a.probeOnce(context.Background()) // a learns b, b learns a via gossip

	if _, ok := a.members["b"]; !ok {
		t.Fatal("a did not learn b")
	}
	if _, ok := b.members["a"]; !ok {
		t.Fatal("b did not learn a from a's gossip")
	}
}
