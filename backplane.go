package surf

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"time"
)

// Backplane is surf's seam for state that must be shared across instances.
//
// In a single process the default Local backend is enough. When surf runs
// across many pods (Kubernetes) or tasks (ECS), a request for the same client
// may land on a different instance each time; anything kept in a process-local
// map or sync.Mutex — sessions, idempotency keys, rate-limiter state, soft
// caches — then diverges between instances. A Backplane gives those primitives
// one shared view without requiring session affinity.
//
// Two primitives are exposed:
//
//   - A key/value store with per-key TTL (Get/Set/Delete).
//   - An advisory lease with an auto-expiring TTL (Lease/TryLease).
//
// # Consistency
//
// The clustered backend (see NewClusterBackplane) is peer-to-peer and
// eventually consistent: the KV store is last-write-wins, and a Set propagates
// to peers asynchronously. This is the right model for sessions, idempotency
// keys, and caches, where a brief propagation window is acceptable. It is not a
// database and not a source of truth for data you cannot afford to lose.
//
// # The lease is ADVISORY, not a lock
//
// A Lease is a best-effort coordination hint, not mutual exclusion. It holds
// exactly one owner only while cluster membership is stable. During a network
// partition (or while membership is changing) it fails completely: measured
// over the in-memory test harness, a symmetric two-node partition double-grants
// the same key 100% of the time, and the fencing token (Lease.Token) collides
// 100% of the time, so it cannot even tiebreak the two holders. You cannot get
// globally-monotonic tokens without consensus, which this backend does not
// have.
//
// Use a Lease for work that is merely wasteful to duplicate (deduplicating a
// background job, electing a soft "primary" for a cache refresh). NEVER use it
// to guard anything you cannot afford to execute twice — money, inventory,
// non-idempotent side effects. For those, use a real coordinator (etcd/Consul);
// the Backplane interface lets you supply one behind these methods.
type Backplane interface {
	// Get returns the value stored under key. The boolean is false (with a nil
	// error) when the key is absent or has expired.
	Get(ctx context.Context, key string) ([]byte, bool, error)

	// Set stores val under key. A positive ttl expires the entry after that
	// duration; a ttl of zero or less stores the entry without expiry.
	Set(ctx context.Context, key string, val []byte, ttl time.Duration) error

	// Delete removes key. Deleting a missing key is not an error.
	Delete(ctx context.Context, key string) error

	// Lease acquires the named advisory lease, blocking until it is held or ctx
	// is done. ttl bounds how long it is held if the caller neither renews nor
	// releases it: once the ttl elapses it auto-releases, so a crashed holder
	// cannot wedge the key forever. Read the type doc: this is NOT a lock and is
	// not partition-safe.
	Lease(ctx context.Context, key string, ttl time.Duration) (Lease, error)

	// TryLease attempts to acquire the named advisory lease without blocking.
	// The boolean reports whether it was acquired; when false the returned Lease
	// is nil and err is nil.
	TryLease(ctx context.Context, key string, ttl time.Duration) (Lease, bool, error)

	// Close releases resources held by the backplane (network connections,
	// background goroutines). It is safe to call more than once.
	Close() error
}

// Lease is a held advisory lease. Release it with Release and extend it with
// Renew. It is NOT a lock — see the Backplane doc comment for the (severe)
// partition caveats.
type Lease interface {
	// Token returns a fencing token that increases with each successful
	// acquisition of the same key UNDER STABLE MEMBERSHIP only. It is NOT a
	// reliable fence: during a partition two holders receive colliding tokens
	// (measured: 100%), so a downstream resource cannot use it to serialize
	// them. Treat it as a best-effort hint, not a correctness mechanism.
	Token() uint64

	// Renew extends the lease by ttl, measured from now. It returns an error if
	// the lease has already been lost (ttl expired or acquired by another
	// holder), in which case the caller must stop using it.
	Renew(ctx context.Context, ttl time.Duration) error

	// Release releases the lease. Releasing one that has already been lost is
	// not an error. After Release the Lease must not be reused.
	Release(ctx context.Context) error
}

// ClusterSizer is implemented by backplanes that may span multiple instances.
// Size reports how many instances are currently believed alive (including
// self). It is the building block for an approximate distributed rate limiter:
// divide a global budget by Size so each instance self-limits to its share. The
// Local backend always reports 1.
//
//	n := 1
//	if cs, ok := app.Backplane().(surf.ClusterSizer); ok {
//	    n = cs.Size()
//	}
//	perInstanceRate := globalRate / float64(n)
type ClusterSizer interface {
	Size() int
}

// Discoverer reports the current set of peer addresses for a clustered
// Backplane. It is consulted periodically, so implementations may return a
// changing set as instances scale up and down. Addresses are "host:port"
// strings reachable by the cluster transport.
type Discoverer interface {
	Peers(ctx context.Context) ([]string, error)
}

// Sentinel errors returned across backplane backends.
var (
	// ErrBackplaneClosed is returned by operations on a closed backplane.
	ErrBackplaneClosed = errors.New("surf: backplane closed")

	// ErrLeaseLost reports that a lease expired or was taken over by another
	// holder before Renew or Release could complete.
	ErrLeaseLost = errors.New("surf: lease lost")
)

// backplaneStarter is implemented by backends that run background goroutines.
// NewApp calls start with the app context so the goroutines stop on shutdown;
// the Local backend does not implement it.
type backplaneStarter interface {
	start(ctx context.Context) error
}

// BackplaneFromRequest returns the backplane serving r, or nil if r did not
// pass through surf's ServeHTTP. It is the standard-path counterpart to
// Context.Backplane.
func BackplaneFromRequest(r *http.Request) Backplane {
	st := stateFromRequest(r)
	if st == nil || st.app == nil {
		return nil
	}
	return st.app.backplane
}

// Local is the default in-process Backplane. It keeps all state in guarded maps
// and is correct for a single instance — it is also the right choice in tests
// and for deployments that genuinely run one replica. Sharing state across
// instances requires a clustered backend; see NewClusterBackplane.
//
// The zero value is not usable; construct one with NewLocal.
type Local struct {
	mu       sync.Mutex
	kv       map[string]localEntry
	leases   map[string]*localLeaseState
	tokenSeq uint64
	closed   bool

	// now is the time source, indirected for deterministic tests. Defaults to
	// time.Now.
	now func() time.Time
}

type localEntry struct {
	val      []byte
	expireAt time.Time // zero means no expiry
}

type localLeaseState struct {
	held     bool
	token    uint64
	expireAt time.Time
}

// NewLocal returns an in-process Backplane backed by guarded maps.
func NewLocal() *Local {
	return &Local{
		kv:     make(map[string]localEntry),
		leases: make(map[string]*localLeaseState),
		now:    time.Now,
	}
}

// leasePollInterval bounds how often a blocking Lease re-checks for the key
// becoming available. It trades a little latency for not needing a condition
// variable that understands context cancellation.
const leasePollInterval = 5 * time.Millisecond

func (l *Local) Get(ctx context.Context, key string) ([]byte, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return nil, false, ErrBackplaneClosed
	}
	e, ok := l.kv[key]
	if !ok {
		return nil, false, nil
	}
	if !e.expireAt.IsZero() && !l.now().Before(e.expireAt) {
		// Expired: reclaim lazily and report absent.
		delete(l.kv, key)
		return nil, false, nil
	}
	// Return a copy so callers cannot mutate stored bytes.
	out := make([]byte, len(e.val))
	copy(out, e.val)
	return out, true, nil
}

func (l *Local) Set(ctx context.Context, key string, val []byte, ttl time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return ErrBackplaneClosed
	}
	cp := make([]byte, len(val))
	copy(cp, val)
	e := localEntry{val: cp}
	if ttl > 0 {
		e.expireAt = l.now().Add(ttl)
	}
	l.kv[key] = e
	return nil
}

func (l *Local) Delete(ctx context.Context, key string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return ErrBackplaneClosed
	}
	delete(l.kv, key)
	return nil
}

func (l *Local) TryLease(ctx context.Context, key string, ttl time.Duration) (Lease, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return nil, false, ErrBackplaneClosed
	}
	now := l.now()
	st := l.leases[key]
	if st != nil && st.held && now.Before(st.expireAt) {
		return nil, false, nil
	}
	if st == nil {
		st = &localLeaseState{}
		l.leases[key] = st
	}
	l.tokenSeq++
	st.held = true
	st.token = l.tokenSeq
	st.expireAt = now.Add(ttl)
	return &localLease{bp: l, key: key, token: st.token}, true, nil
}

func (l *Local) Lease(ctx context.Context, key string, ttl time.Duration) (Lease, error) {
	for {
		lk, ok, err := l.TryLease(ctx, key, ttl)
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

// Size implements ClusterSizer. A Local backend is a single instance.
func (l *Local) Size() int { return 1 }

func (l *Local) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.closed = true
	l.kv = nil
	l.leases = nil
	return nil
}

// localLease is a handle to a lease held in a Local backplane.
type localLease struct {
	bp    *Local
	key   string
	token uint64
}

func (lk *localLease) Token() uint64 { return lk.token }

func (lk *localLease) Renew(ctx context.Context, ttl time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	lk.bp.mu.Lock()
	defer lk.bp.mu.Unlock()
	if lk.bp.closed {
		return ErrBackplaneClosed
	}
	st := lk.bp.leases[lk.key]
	now := lk.bp.now()
	if st == nil || !st.held || st.token != lk.token || !now.Before(st.expireAt) {
		return ErrLeaseLost
	}
	st.expireAt = now.Add(ttl)
	return nil
}

func (lk *localLease) Release(ctx context.Context) error {
	lk.bp.mu.Lock()
	defer lk.bp.mu.Unlock()
	if lk.bp.closed {
		return nil
	}
	st := lk.bp.leases[lk.key]
	// Only release if we still own the lease; a newer holder must not be
	// disturbed by a stale handle's Release.
	if st != nil && st.held && st.token == lk.token {
		st.held = false
	}
	return nil
}
