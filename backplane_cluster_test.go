package surf

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"testing"
	"time"
)

// eventually retries cond until it returns true or the deadline passes.
func eventually(t *testing.T, within time.Duration, cond func() bool) bool {
	t.Helper()
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(5 * time.Millisecond)
	}
	return cond()
}

// quietLogger discards backplane logs so failure-detection chatter doesn't
// flood test output.
func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// newTestCluster starts n cluster nodes wired over a single in-memory network,
// each discovering the others via StaticPeers. It returns the nodes and the
// shared network (for partition injection).
func newTestCluster(t *testing.T, n int) ([]*clusterBackplane, *memNetwork) {
	t.Helper()
	netw := newMemNetwork()
	secret := []byte("integration-test-cluster-secret")
	addrs := make([]string, n)
	for i := range addrs {
		addrs[i] = fmt.Sprintf("node-%d:1", i)
	}
	nodes := make([]*clusterBackplane, n)
	for i := range nodes {
		tr := netw.transportFor(addrs[i])
		bp := newClusterBackplaneWithKeyring(tr, secret, StaticPeers(addrs...),
			WithClusterAdvertiseAddr(addrs[i]),
			WithClusterNodeID(fmt.Sprintf("n%d", i)),
			WithClusterTuning(30*time.Millisecond, 30*time.Millisecond, 50*time.Millisecond),
			WithClusterFailureDetection(100*time.Millisecond, 150*time.Millisecond, time.Second, 2),
		)
		bp.SetLogger(quietLogger())
		if err := bp.start(context.Background()); err != nil {
			t.Fatalf("start node %d: %v", i, err)
		}
		t.Cleanup(func() { _ = bp.Close() })
		nodes[i] = bp
	}
	return nodes, netw
}

// waitConverged waits until every node sees all n members alive.
func waitConverged(t *testing.T, nodes []*clusterBackplane) {
	t.Helper()
	ok := eventually(t, 5*time.Second, func() bool {
		for _, n := range nodes {
			if len(n.members.aliveMembers()) != len(nodes) {
				return false
			}
		}
		return true
	})
	if !ok {
		for _, n := range nodes {
			t.Logf("node %s sees %d alive", n.nodeID, len(n.members.aliveMembers()))
		}
		t.Fatal("cluster did not converge on full membership")
	}
}

func TestCluster_Conformance(t *testing.T) {
	// A single-node cluster: reads-after-writes are immediate, so it satisfies
	// the same contract as Local.
	backplaneConformance(t, func(t *testing.T) Backplane {
		nodes, _ := newTestCluster(t, 1)
		return nodes[0]
	})
}

func TestCluster_KVReplicates(t *testing.T) {
	nodes, _ := newTestCluster(t, 3)
	waitConverged(t, nodes)
	ctx := context.Background()

	if err := nodes[0].Set(ctx, "greeting", []byte("hello"), 0); err != nil {
		t.Fatalf("Set: %v", err)
	}
	// The other two nodes should converge on the value via push/anti-entropy.
	for i := 1; i < 3; i++ {
		i := i
		ok := eventually(t, 3*time.Second, func() bool {
			v, found, _ := nodes[i].Get(ctx, "greeting")
			return found && string(v) == "hello"
		})
		if !ok {
			t.Fatalf("node %d did not receive replicated value", i)
		}
	}
}

func TestCluster_DeleteReplicates(t *testing.T) {
	nodes, _ := newTestCluster(t, 2)
	waitConverged(t, nodes)
	ctx := context.Background()

	_ = nodes[0].Set(ctx, "k", []byte("v"), 0)
	if !eventually(t, 3*time.Second, func() bool {
		_, ok, _ := nodes[1].Get(ctx, "k")
		return ok
	}) {
		t.Fatal("value did not replicate")
	}
	_ = nodes[0].Delete(ctx, "k")
	if !eventually(t, 3*time.Second, func() bool {
		_, ok, _ := nodes[1].Get(ctx, "k")
		return !ok
	}) {
		t.Fatal("delete did not replicate")
	}
}

func TestCluster_AtRestEncrypted(t *testing.T) {
	// The value must be sealed in every node's store: the plaintext must not be
	// findable in the raw stored bytes.
	nodes, _ := newTestCluster(t, 2)
	waitConverged(t, nodes)
	ctx := context.Background()
	secretVal := []byte("PLAINTEXT-SESSION-TOKEN")
	_ = nodes[0].Set(ctx, "sess", secretVal, 0)
	eventually(t, 3*time.Second, func() bool {
		_, ok := nodes[1].kv.localGet("sess")
		return ok
	})
	raw, ok := nodes[1].kv.localGet("sess")
	if !ok {
		t.Fatal("replica missing entry")
	}
	if string(raw) == string(secretVal) {
		t.Fatal("value stored in plaintext on replica")
	}
	// But a proper Get round-trips correctly.
	got, ok, err := nodes[1].Get(ctx, "sess")
	if err != nil || !ok || string(got) != string(secretVal) {
		t.Fatalf("Get = %q ok=%v err=%v", got, ok, err)
	}
}

func TestCluster_LockMutualExclusion(t *testing.T) {
	nodes, _ := newTestCluster(t, 3)
	waitConverged(t, nodes)
	ctx := context.Background()

	// Node 0 holds the lock.
	l0, err := nodes[0].Lease(ctx, "resource", time.Second)
	if err != nil {
		t.Fatalf("node0 Lock: %v", err)
	}
	// Nodes 1 and 2 route to the same arbiter and must fail to acquire.
	for i := 1; i < 3; i++ {
		if _, ok, err := nodes[i].TryLease(ctx, "resource", time.Second); err != nil || ok {
			t.Fatalf("node%d TryLock = ok:%v err:%v, want ok:false", i, ok, err)
		}
	}
	// Release; another node can now acquire.
	if err := l0.Release(ctx); err != nil {
		t.Fatalf("Unlock: %v", err)
	}
	if !eventually(t, time.Second, func() bool {
		l, ok, _ := nodes[1].TryLease(ctx, "resource", time.Second)
		if ok {
			_ = l.Release(ctx)
		}
		return ok
	}) {
		t.Fatal("node1 could not acquire after release")
	}
}

func TestCluster_FencingTokenMonotonicAcrossNodes(t *testing.T) {
	nodes, _ := newTestCluster(t, 3)
	waitConverged(t, nodes)
	ctx := context.Background()

	l0, err := nodes[0].Lease(ctx, "fkey", time.Second)
	if err != nil {
		t.Fatalf("node0 Lock: %v", err)
	}
	t0 := l0.Token()
	_ = l0.Release(ctx)

	l1, err := nodes[1].Lease(ctx, "fkey", time.Second)
	if err != nil {
		t.Fatalf("node1 Lock: %v", err)
	}
	t1 := l1.Token()
	_ = l1.Release(ctx)

	if t1 <= t0 {
		t.Fatalf("fencing token not monotonic across nodes: t0=%d t1=%d", t0, t1)
	}
}

func TestCluster_SplitBrainDemonstratesLockUnsafety(t *testing.T) {
	// This test documents the honest guarantee: under a partition the lock can
	// be double-granted. It is here to prove the limitation is real, not to
	// assert correctness.
	nodes, netw := newTestCluster(t, 2)
	waitConverged(t, nodes)
	ctx := context.Background()

	// Partition the two nodes from each other.
	netw.partition(partitionSets([]string{"node-0:1"}, []string{"node-1:1"}))

	// Wait until each node has declared the other dead, so each becomes the
	// sole arbiter for every key on its side.
	if !eventually(t, 5*time.Second, func() bool {
		return len(nodes[0].members.aliveMembers()) == 1 &&
			len(nodes[1].members.aliveMembers()) == 1
	}) {
		t.Fatal("partition not detected by both nodes")
	}

	// Both nodes can now acquire the SAME lock — the documented split-brain.
	l0, ok0, err0 := nodes[0].TryLease(ctx, "money", 5*time.Second)
	l1, ok1, err1 := nodes[1].TryLease(ctx, "money", 5*time.Second)
	if err0 != nil || err1 != nil {
		t.Fatalf("unexpected errors: %v %v", err0, err1)
	}
	if !(ok0 && ok1) {
		t.Fatalf("expected both sides to acquire under partition (ok0=%v ok1=%v) — "+
			"if this fails the documented behavior changed", ok0, ok1)
	}
	// Cleanup.
	if l0 != nil {
		_ = l0.Release(ctx)
	}
	if l1 != nil {
		_ = l1.Release(ctx)
	}
}

func TestCluster_CloseStopsGoroutines(t *testing.T) {
	nodes, _ := newTestCluster(t, 2)
	waitConverged(t, nodes)
	// Close returns only after wg.Wait, i.e. all loops have exited.
	done := make(chan struct{})
	go func() {
		_ = nodes[0].Close()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("Close did not return; a background goroutine leaked")
	}
	if nodes[0].started.Load() {
		t.Fatal("node still marked started after Close")
	}
	// Operations after close fail closed.
	if err := nodes[0].Set(context.Background(), "k", []byte("v"), 0); err != ErrBackplaneClosed {
		t.Fatalf("Set after close = %v, want ErrBackplaneClosed", err)
	}
}

func TestCluster_BadSecretFailsToStart(t *testing.T) {
	bp := NewClusterBackplane(nil, StaticPeers())
	if err := bp.start(context.Background()); err == nil {
		t.Fatal("expected start to fail with empty secret")
	}
}
