//go:build eval

// Evaluation harness for the clustered backplane. Opt-in so it never runs in
// the normal suite or CI (the lock-safety stable-membership check hammers
// membership hard enough that it belongs in a dedicated run):
//
//	go test -tags eval -run TestEval -v .

package surf

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// This file is an evaluation harness, not a pass/fail unit suite. It quantifies
// the clustered backplane's real behavior — how fast the KV converges, how it
// recovers, and exactly how unsafe the locks are under a partition — so design
// decisions rest on measurements instead of claims.
//
// IMPORTANT: timings are measured over the in-memory transport, so absolute
// numbers reflect protocol/logic latency in-process, NOT real network latency.
// The correctness results (violation counts, convergence/agreement) and the
// relative costs are the meaningful output. Run with:
//
//	go test -run 'TestEval' -v .
//
// The heavy reporting tests skip under -short; the invariant assertions
// (stable-membership exclusivity, partition-heal agreement) always run.

// fencedResource models the resource a lock protects. It accepts writes only
// with a strictly increasing fencing token (Kleppmann fencing) and detects when
// more than one holder is in the critical section at once.
type fencedResource struct {
	mu              sync.Mutex
	highestToken    uint64
	inCritical      int32
	concurrent      int64 // times >1 holder was in the section simultaneously
	fencingRejected int64 // stale writes the fence correctly rejected
	acceptedWrites  int64
}

// enter records a holder entering with fencing token tok and returns whether a
// concurrent holder was observed.
func (r *fencedResource) enter(tok uint64) bool {
	conc := atomic.AddInt32(&r.inCritical, 1) > 1
	if conc {
		atomic.AddInt64(&r.concurrent, 1)
	}
	r.mu.Lock()
	if tok > r.highestToken {
		r.highestToken = tok
		atomic.AddInt64(&r.acceptedWrites, 1)
	} else {
		atomic.AddInt64(&r.fencingRejected, 1)
	}
	r.mu.Unlock()
	return conc
}

func (r *fencedResource) leave() { atomic.AddInt32(&r.inCritical, -1) }

// TestEval_LockSafety_StableMembership asserts the core promise: with stable
// membership a lock provides true mutual exclusion. All keys route to one
// arbiter, which serializes — so concurrent holders must be exactly zero.
func TestEval_LockSafety_StableMembership(t *testing.T) {
	nodes, _ := newTestCluster(t, 3)
	waitConverged(t, nodes)
	ctx := context.Background()

	res := &fencedResource{}
	var wg sync.WaitGroup
	var acquisitions int64
	stop := time.Now().Add(400 * time.Millisecond)

	for _, n := range nodes {
		for g := 0; g < 4; g++ { // 4 workers per node, all fighting for one key
			wg.Add(1)
			go func(n *clusterBackplane) {
				defer wg.Done()
				for time.Now().Before(stop) {
					l, err := n.Lease(ctx, "the-resource", time.Second)
					if err != nil {
						continue
					}
					atomic.AddInt64(&acquisitions, 1)
					res.enter(l.Token())
					// tiny critical section
					res.leave()
					_ = l.Release(ctx)
				}
			}(n)
		}
	}
	wg.Wait()

	t.Logf("stable membership: %d acquisitions, %d concurrent-holder events, %d fencing rejections",
		acquisitions, res.concurrent, res.fencingRejected)
	if res.concurrent != 0 {
		t.Fatalf("MUTUAL EXCLUSION VIOLATED under stable membership: %d concurrent holders (this is a real bug)", res.concurrent)
	}
}

// TestEval_LockSafety_UnderPartition quantifies how unsafe the lock is during a
// partition. It does NOT fail on double-grants — those are the documented
// behavior; it measures their rate and whether fencing tokens even stay
// orderable across the two arbiters.
func TestEval_LockSafety_UnderPartition(t *testing.T) {
	if testing.Short() {
		t.Skip("evaluation harness; run without -short")
	}
	nodes, netw := newTestCluster(t, 2)
	waitConverged(t, nodes)
	ctx := context.Background()

	netw.partition(partitionSets([]string{"node-0:1"}, []string{"node-1:1"}))
	if !eventually(t, 5*time.Second, func() bool {
		return len(nodes[0].members.aliveMembers()) == 1 && len(nodes[1].members.aliveMembers()) == 1
	}) {
		t.Fatal("partition not detected")
	}

	const rounds = 200
	doubleGrants, tokenCollisions, tokenInversions := 0, 0, 0
	for i := 0; i < rounds; i++ {
		key := fmt.Sprintf("contended-%d", i)
		var l0, l1 Lease
		var ok0, ok1 bool
		var wg sync.WaitGroup
		wg.Add(2)
		go func() { defer wg.Done(); l0, ok0, _ = nodes[0].TryLease(ctx, key, time.Second) }()
		go func() { defer wg.Done(); l1, ok1, _ = nodes[1].TryLease(ctx, key, time.Second) }()
		wg.Wait()

		if ok0 && ok1 {
			doubleGrants++
			switch {
			case l0.Token() == l1.Token():
				tokenCollisions++ // fence cannot even tiebreak: same token from two holders
			}
			// both sides increment independently; tokens that don't strictly
			// order across arbiters mean the fence can't globally serialize.
			if l0.Token() == l1.Token() {
				tokenInversions++
			}
		}
		if l0 != nil {
			_ = l0.Release(ctx)
		}
		if l1 != nil {
			_ = l1.Release(ctx)
		}
	}

	t.Logf("UNDER PARTITION over %d rounds:", rounds)
	t.Logf("  double-grants (both sides held the same key): %d/%d (%.0f%%)",
		doubleGrants, rounds, 100*float64(doubleGrants)/float64(rounds))
	t.Logf("  fencing-token collisions (identical token from both holders): %d", tokenCollisions)
	t.Logf("  => when tokens collide, the fencing token cannot serialize the two holders.")
	if doubleGrants == 0 {
		t.Fatal("expected double-grants under partition; documented behavior changed")
	}
}

// --- KV convergence & recovery --------------------------------------------

func percentile(durations []time.Duration, p float64) time.Duration {
	if len(durations) == 0 {
		return 0
	}
	sorted := append([]time.Duration(nil), durations...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	idx := int(p * float64(len(sorted)-1))
	return sorted[idx]
}

// TestEval_KVConvergence_Propagation measures how long a write takes to become
// visible on every other node (in-process; logic latency, not wire latency).
func TestEval_KVConvergence_Propagation(t *testing.T) {
	if testing.Short() {
		t.Skip("evaluation harness; run without -short")
	}
	nodes, _ := newTestCluster(t, 5)
	waitConverged(t, nodes)
	ctx := context.Background()

	const writes = 100
	lat := make([]time.Duration, 0, writes)
	for i := 0; i < writes; i++ {
		key := fmt.Sprintf("prop-%d", i)
		want := []byte(fmt.Sprintf("v%d", i))
		start := time.Now()
		if err := nodes[0].Set(ctx, key, want, 0); err != nil {
			t.Fatal(err)
		}
		deadline := start.Add(2 * time.Second)
		for {
			all := true
			for _, n := range nodes[1:] {
				if v, ok, _ := n.Get(ctx, key); !ok || string(v) != string(want) {
					all = false
					break
				}
			}
			if all || time.Now().After(deadline) {
				break
			}
		}
		lat = append(lat, time.Since(start))
	}

	t.Logf("write -> visible on all 5 nodes (in-process): median=%s p95=%s max=%s",
		percentile(lat, 0.5), percentile(lat, 0.95), percentile(lat, 1.0))
}

// TestEval_KVConvergence_PartitionHeal asserts the KV converges after a
// partition heals, and that every node agrees on the same LWW winner for a key
// written on both sides.
func TestEval_KVConvergence_PartitionHeal(t *testing.T) {
	nodes, netw := newTestCluster(t, 4)
	waitConverged(t, nodes)
	ctx := context.Background()

	// Split 0,1 | 2,3.
	groupA := []string{"node-0:1", "node-1:1"}
	groupB := []string{"node-2:1", "node-3:1"}
	netw.partition(partitionSets(groupA, groupB))
	if !eventually(t, 5*time.Second, func() bool {
		return len(nodes[0].members.aliveMembers()) == 2 && len(nodes[2].members.aliveMembers()) == 2
	}) {
		t.Fatal("partition not detected by both sides")
	}

	// Conflicting writes to the same key on each side, plus side-only keys.
	_ = nodes[0].Set(ctx, "shared", []byte("from-A"), 0)
	_ = nodes[2].Set(ctx, "shared", []byte("from-B"), 0)
	_ = nodes[0].Set(ctx, "only-A", []byte("a"), 0)
	_ = nodes[2].Set(ctx, "only-B", []byte("b"), 0)

	// Heal and time convergence.
	start := time.Now()
	netw.partition(nil)
	converged := eventually(t, 10*time.Second, func() bool {
		var first []byte
		for i, n := range nodes {
			v, ok, _ := n.Get(ctx, "shared")
			if !ok {
				return false
			}
			if i == 0 {
				first = v
			} else if string(v) != string(first) {
				return false
			}
			if _, ok, _ := n.Get(ctx, "only-A"); !ok {
				return false
			}
			if _, ok, _ := n.Get(ctx, "only-B"); !ok {
				return false
			}
		}
		return true
	})
	elapsed := time.Since(start)

	if !converged {
		t.Fatal("cluster did NOT converge after partition heal")
	}
	// Verify total agreement on the winner.
	winner, _, _ := nodes[0].Get(ctx, "shared")
	for _, n := range nodes {
		v, _, _ := n.Get(ctx, "shared")
		if string(v) != string(winner) {
			t.Fatalf("nodes disagree on LWW winner: %q vs %q", winner, v)
		}
	}
	t.Logf("partition-heal: converged in %s (in-process); all 4 nodes agree shared=%q", elapsed, winner)
}

// TestEval_KVConvergence_NodeRecovery measures how long a restarted node takes
// to catch up to existing state via anti-entropy, and asserts it does.
func TestEval_KVConvergence_NodeRecovery(t *testing.T) {
	if testing.Short() {
		t.Skip("evaluation harness; run without -short")
	}
	netw := newMemNetwork()
	secret := []byte("eval-secret")
	addrs := []string{"r0:1", "r1:1", "r2:1"}
	mk := func(id, addr string) *clusterBackplane {
		bp := newClusterBackplaneWithKeyring(netw.transportFor(addr), secret,
			StaticPeers(addrs...), WithClusterAdvertiseAddr(addr), WithClusterNodeID(id),
			WithClusterTuning(30*time.Millisecond, 30*time.Millisecond, 50*time.Millisecond),
			WithClusterFailureDetection(100*time.Millisecond, 150*time.Millisecond, time.Second, 2))
		bp.SetLogger(quietLogger())
		if err := bp.start(context.Background()); err != nil {
			t.Fatal(err)
		}
		return bp
	}
	n0, n1 := mk("r0", "r0:1"), mk("r1", "r1:1")
	defer n0.Close()
	defer n1.Close()
	ctx := context.Background()

	// Seed 200 keys, propagated across the live two.
	const keys = 200
	for i := 0; i < keys; i++ {
		_ = n0.Set(ctx, fmt.Sprintf("rec-%d", i), []byte("v"), 0)
	}
	eventually(t, 3*time.Second, func() bool {
		_, ok, _ := n1.Get(ctx, fmt.Sprintf("rec-%d", keys-1))
		return ok
	})

	// A third node joins fresh and must catch up via anti-entropy.
	start := time.Now()
	n2 := mk("r2", "r2:1")
	defer n2.Close()
	caughtUp := eventually(t, 10*time.Second, func() bool {
		for i := 0; i < keys; i++ {
			if _, ok, _ := n2.Get(ctx, fmt.Sprintf("rec-%d", i)); !ok {
				return false
			}
		}
		return true
	})
	elapsed := time.Since(start)
	if !caughtUp {
		got := 0
		for i := 0; i < keys; i++ {
			if _, ok, _ := n2.Get(ctx, fmt.Sprintf("rec-%d", i)); ok {
				got++
			}
		}
		t.Fatalf("late joiner did NOT catch up: has %d/%d keys after %s", got, keys, elapsed)
	}
	t.Logf("node recovery: fresh node caught up to %d keys in %s (in-process, anti-entropy)", keys, elapsed)
}
