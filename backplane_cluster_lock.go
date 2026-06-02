package surf

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"hash/fnv"
	"time"
)

// Advisory leases for the clustered backplane. See the Lease interface doc for
// the user-facing contract; this file is the mechanism.
//
// # How a lease is arbitrated
//
// Each key is mapped to one live member by rendezvous (highest-random-weight)
// hashing: every node computes the same score for (member, key) and the
// top-scoring live member is the key's arbiter. Acquisition is a single RPC to
// that arbiter, which grants the lease and a fencing token. The lease
// auto-expires, so a crashed holder — or a crashed arbiter — frees the key
// without operator action.
//
// # What it guarantees, and what it does not
//
// Under stable membership exactly one holder has the key at a time, and each
// successive grant carries a strictly higher fencing token.
//
// It is NOT safe across a network partition, and this is not a soft caveat:
// measured over the in-memory harness, a symmetric two-node partition
// double-grants the same key 100% of the time, AND the per-arbiter fencing
// tokens collide 100% of the time (both arbiters seed from identical state and
// hand out the same number), so the token cannot even tiebreak. Globally
// monotonic tokens require consensus, which this backend does not have. The
// lease is therefore advisory only — safe to duplicate, never to guard
// non-idempotent side effects.

// lockGrant is the arbiter's record of who holds a key.
type lockGrant struct {
	holder   string // unique per acquisition: nodeID + "/" + random
	token    uint64
	expireAt time.Time
}

func randomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// hrwScore is the rendezvous-hashing weight of (node, key). The highest scorer
// among live members arbitrates the key.
func hrwScore(node, key string) uint64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(node))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(key))
	return h.Sum64()
}

// arbiterFor returns the live member responsible for key. aliveMembers always
// includes self, so the result is never empty.
func (n *clusterBackplane) arbiterFor(key string) memberInfo {
	members := n.members.aliveMembers()
	best := members[0]
	bestScore := hrwScore(best.NodeID, key)
	for _, m := range members[1:] {
		if s := hrwScore(m.NodeID, key); s > bestScore {
			best, bestScore = m, s
		}
	}
	return best
}

func lockMetaKey(key string) string { return "\x00bp/lock/" + key }

// persistLockToken records the latest fencing token for key in the replicated
// KV so a future arbiter (after a membership change) continues from here rather
// than reusing low tokens. Best-effort.
func (n *clusterBackplane) persistLockToken(key string, token uint64) {
	var b [8]byte
	binary.BigEndian.PutUint64(b[:], token)
	e := n.kv.localSet(lockMetaKey(key), b[:], 0)
	n.replicate(e)
}

func (n *clusterBackplane) readPersistedToken(key string) uint64 {
	v, ok := n.kv.localGet(lockMetaKey(key))
	if !ok || len(v) < 8 {
		return 0
	}
	return binary.BigEndian.Uint64(v)
}

// nextTokenLocked returns the next fencing token for key, advancing past any
// value persisted by a prior arbiter. Caller holds lockMu.
func (n *clusterBackplane) nextTokenLocked(key string) uint64 {
	seq := n.tokenSeq[key]
	if persisted := n.readPersistedToken(key); persisted > seq {
		seq = persisted
	}
	seq++
	n.tokenSeq[key] = seq
	return seq
}

// grantLocal is the arbiter-side acquire. It returns the fencing token and
// whether the grant succeeded.
func (n *clusterBackplane) grantLocal(key, holder string, leaseMs int64) (uint64, bool) {
	n.lockMu.Lock()
	now := time.Now()
	g, ok := n.grants[key]
	if ok && now.Before(g.expireAt) && g.holder != holder {
		n.lockMu.Unlock()
		return 0, false // currently held by someone else
	}
	var token uint64
	if ok && g.holder == holder && now.Before(g.expireAt) {
		token = g.token // idempotent re-acquire by the same holder
	} else {
		token = n.nextTokenLocked(key)
	}
	n.grants[key] = &lockGrant{
		holder:   holder,
		token:    token,
		expireAt: now.Add(time.Duration(leaseMs) * time.Millisecond),
	}
	n.lockMu.Unlock()
	n.persistLockToken(key, token)
	return token, true
}

func (n *clusterBackplane) refreshLocal(key, holder string, token uint64, leaseMs int64) bool {
	n.lockMu.Lock()
	defer n.lockMu.Unlock()
	g, ok := n.grants[key]
	now := time.Now()
	if !ok || g.holder != holder || g.token != token || !now.Before(g.expireAt) {
		return false
	}
	g.expireAt = now.Add(time.Duration(leaseMs) * time.Millisecond)
	return true
}

func (n *clusterBackplane) releaseLocal(key, holder string, token uint64) {
	n.lockMu.Lock()
	defer n.lockMu.Unlock()
	if g, ok := n.grants[key]; ok && g.holder == holder && g.token == token {
		delete(n.grants, key)
	}
}

// --- RPC handlers (arbiter side) ------------------------------------------

func (n *clusterBackplane) handleLockAcquire(req *wireMsg) *wireMsg {
	token, ok := n.grantLocal(req.LockKey, req.Holder, req.LeaseMs)
	return &wireMsg{Kind: kindLockAcquire, OK: ok, Token: token}
}

func (n *clusterBackplane) handleLockRefresh(req *wireMsg) *wireMsg {
	ok := n.refreshLocal(req.LockKey, req.Holder, req.Token, req.LeaseMs)
	return &wireMsg{Kind: kindLockRefresh, OK: ok}
}

func (n *clusterBackplane) handleLockRelease(req *wireMsg) *wireMsg {
	n.releaseLocal(req.LockKey, req.Holder, req.Token)
	return &wireMsg{Kind: kindLockRelease, OK: true}
}

// lockJanitorLoop drops expired grants from memory. Correctness does not depend
// on it (acquire re-checks expiry); it just keeps the map from growing.
func (n *clusterBackplane) lockJanitorLoop() {
	t := time.NewTicker(n.cfg.ReapInterval)
	defer t.Stop()
	for {
		select {
		case <-n.ctx.Done():
			return
		case <-t.C:
			n.lockMu.Lock()
			now := time.Now()
			for k, g := range n.grants {
				if !now.Before(g.expireAt) {
					delete(n.grants, k)
				}
			}
			n.lockMu.Unlock()
		}
	}
}

// --- Backplane: locks ------------------------------------------------------

func (n *clusterBackplane) leaseMillis(lease time.Duration) int64 {
	if lease <= 0 {
		lease = n.cfg.LeaseDefault
	}
	return lease.Milliseconds()
}

func (n *clusterBackplane) TryLease(ctx context.Context, key string, ttl time.Duration) (Lease, bool, error) {
	if !n.started.Load() {
		return nil, false, ErrBackplaneClosed
	}
	holder := n.nodeID + "/" + randomHex(8)
	arb := n.arbiterFor(key)
	leaseMs := n.leaseMillis(ttl)

	var token uint64
	var ok bool
	if arb.NodeID == n.nodeID {
		token, ok = n.grantLocal(key, holder, leaseMs)
	} else {
		resp, err := n.rpc(ctx, arb.Addr, &wireMsg{
			Kind: kindLockAcquire, LockKey: key, Holder: holder, LeaseMs: leaseMs,
		})
		if err != nil {
			return nil, false, err
		}
		token, ok = resp.Token, resp.OK
	}
	if !ok {
		return nil, false, nil
	}
	return &clusterLease{bp: n, key: key, holder: holder, token: token, arbiter: arb}, true, nil
}

func (n *clusterBackplane) Lease(ctx context.Context, key string, ttl time.Duration) (Lease, error) {
	for {
		lk, ok, err := n.TryLease(ctx, key, ttl)
		if err != nil {
			return nil, err
		}
		if ok {
			return lk, nil
		}
		timer := time.NewTimer(leasePollInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
}

// clusterLease is a held advisory lease. Renew/Release target the arbiter
// chosen at acquisition; if the arbiter has since changed or died, Renew
// reports the lease lost (the ttl will expire) and Release is a best-effort
// no-op.
type clusterLease struct {
	bp      *clusterBackplane
	key     string
	holder  string
	token   uint64
	arbiter memberInfo
}

func (l *clusterLease) Token() uint64 { return l.token }

func (l *clusterLease) Renew(ctx context.Context, ttl time.Duration) error {
	leaseMs := l.bp.leaseMillis(ttl)
	if l.arbiter.NodeID == l.bp.nodeID {
		if l.bp.refreshLocal(l.key, l.holder, l.token, leaseMs) {
			return nil
		}
		return ErrLeaseLost
	}
	resp, err := l.bp.rpc(ctx, l.arbiter.Addr, &wireMsg{
		Kind: kindLockRefresh, LockKey: l.key, Holder: l.holder, Token: l.token, LeaseMs: leaseMs,
	})
	if err != nil {
		return err
	}
	if !resp.OK {
		return ErrLeaseLost
	}
	return nil
}

func (l *clusterLease) Release(ctx context.Context) error {
	if l.arbiter.NodeID == l.bp.nodeID {
		l.bp.releaseLocal(l.key, l.holder, l.token)
		return nil
	}
	// Best-effort: if the arbiter is unreachable the lease will expire anyway.
	_, _ = l.bp.rpc(ctx, l.arbiter.Addr, &wireMsg{
		Kind: kindLockRelease, LockKey: l.key, Holder: l.holder, Token: l.token,
	})
	return nil
}
