// Package redisbackplane implements surf.Backplane on top of Redis.
//
// It is the strongly-consistent counterpart to surf's built-in peer-to-peer
// cluster backend. Redis is a single source of truth, so unlike the gossip
// backend this provides a REAL lease: the fencing token comes from Redis INCR
// (atomic, globally monotonic), so a downstream resource can reliably reject
// stale holders. Mutual exclusion holds against a single Redis primary; note
// the usual caveat that a Sentinel/Cluster failover can still drop a held lease
// (the Redlock debate), which is exactly why the fencing token matters.
//
// The package depends only on the standard library and surf core — it speaks
// RESP over a stdlib socket — so importing it adds no third-party dependency.
//
//	bp, err := redisbackplane.New("localhost:6379",
//	    redisbackplane.WithKeyPrefix("surf:"),
//	    redisbackplane.WithEncryption([]byte(os.Getenv("SURF_KV_SECRET"))))
//	app := surf.NewApp(surf.WithBackplane(bp))
package redisbackplane

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strconv"
	"time"

	surf "github.com/getangry/surf"
)

// store is the seam between the Backplane and Redis. The production
// implementation (respClient) speaks RESP; tests use an in-memory fake, so the
// Backplane logic (namespacing, encryption, lease handles) is covered without a
// live server.
type store interface {
	get(ctx context.Context, key string) ([]byte, bool, error)
	set(ctx context.Context, key string, val []byte, ttlMs int64) error
	del(ctx context.Context, key string) error
	// acquire sets lockKey to holder iff absent (NX) with a ttl, and on success
	// returns a fresh monotonic fencing token from fenceKey (INCR). ok is false
	// when the key is already held.
	acquire(ctx context.Context, lockKey, fenceKey, holder string, ttlMs int64) (token uint64, ok bool, err error)
	// renew extends the ttl iff holder still owns lockKey.
	renew(ctx context.Context, lockKey, holder string, ttlMs int64) (bool, error)
	// release deletes lockKey iff holder still owns it.
	release(ctx context.Context, lockKey, holder string) error
	close() error
}

// Backplane implements surf.Backplane against Redis.
type Backplane struct {
	store  store
	prefix string
	sealer *sealer // nil when encryption is disabled
}

// Option configures a Backplane.
type Option func(*config)

type config struct {
	password    string
	db          int
	prefix      string
	poolSize    int
	dialTimeout time.Duration
	secret      []byte
}

// WithPassword sets the Redis AUTH password.
func WithPassword(p string) Option { return func(c *config) { c.password = p } }

// WithDB selects the Redis logical database (SELECT).
func WithDB(db int) Option { return func(c *config) { c.db = db } }

// WithKeyPrefix namespaces every key this backplane uses (default none).
func WithKeyPrefix(p string) Option { return func(c *config) { c.prefix = p } }

// WithPoolSize bounds idle pooled connections (default 8).
func WithPoolSize(n int) Option { return func(c *config) { c.poolSize = n } }

// WithDialTimeout sets the connection dial timeout (default 5s).
func WithDialTimeout(d time.Duration) Option { return func(c *config) { c.dialTimeout = d } }

// WithEncryption enables AES-256-GCM encryption of stored values, so the Redis
// operator cannot read them. The secret may be any length; it is hashed to a
// 256-bit key. Lock/fence bookkeeping is not secret and is stored in clear.
func WithEncryption(secret []byte) Option {
	return func(c *config) { c.secret = secret }
}

// New returns a Redis-backed Backplane connecting to addr ("host:port").
// Connections are established lazily on first use.
func New(addr string, opts ...Option) (*Backplane, error) {
	if addr == "" {
		return nil, errors.New("redisbackplane: empty address")
	}
	var cfg config
	for _, opt := range opts {
		opt(&cfg)
	}
	bp := &Backplane{
		store:  newRespClient(addr, cfg.password, cfg.db, cfg.poolSize, cfg.dialTimeout),
		prefix: cfg.prefix,
	}
	if len(cfg.secret) > 0 {
		s, err := newSealer(cfg.secret)
		if err != nil {
			return nil, err
		}
		bp.sealer = s
	}
	return bp, nil
}

// newWithStore builds a Backplane over a custom store (used by tests).
func newWithStore(s store, prefix string, secret []byte) (*Backplane, error) {
	bp := &Backplane{store: s, prefix: prefix}
	if len(secret) > 0 {
		sl, err := newSealer(secret)
		if err != nil {
			return nil, err
		}
		bp.sealer = sl
	}
	return bp, nil
}

func (b *Backplane) kvKey(k string) string    { return b.prefix + "kv:" + k }
func (b *Backplane) lockKey(k string) string  { return b.prefix + "lock:" + k }
func (b *Backplane) fenceKey(k string) string { return b.prefix + "fence:" + k }

func (b *Backplane) Get(ctx context.Context, key string) ([]byte, bool, error) {
	raw, ok, err := b.store.get(ctx, b.kvKey(key))
	if err != nil || !ok {
		return nil, ok, err
	}
	if b.sealer != nil {
		plain, err := b.sealer.open(raw)
		if err != nil {
			return nil, false, err
		}
		return plain, true, nil
	}
	return raw, true, nil
}

func (b *Backplane) Set(ctx context.Context, key string, val []byte, ttl time.Duration) error {
	if b.sealer != nil {
		sealed, err := b.sealer.seal(val)
		if err != nil {
			return err
		}
		val = sealed
	}
	return b.store.set(ctx, b.kvKey(key), val, msOf(ttl))
}

func (b *Backplane) Delete(ctx context.Context, key string) error {
	return b.store.del(ctx, b.kvKey(key))
}

func (b *Backplane) TryLease(ctx context.Context, key string, ttl time.Duration) (surf.Lease, bool, error) {
	holder := randomHex(16)
	token, ok, err := b.store.acquire(ctx, b.lockKey(key), b.fenceKey(key), holder, leaseMs(ttl))
	if err != nil || !ok {
		return nil, false, err
	}
	return &redisLease{bp: b, key: key, holder: holder, token: token}, true, nil
}

func (b *Backplane) Lease(ctx context.Context, key string, ttl time.Duration) (surf.Lease, error) {
	for {
		l, ok, err := b.TryLease(ctx, key, ttl)
		if err != nil {
			return nil, err
		}
		if ok {
			return l, nil
		}
		t := time.NewTimer(10 * time.Millisecond)
		select {
		case <-ctx.Done():
			t.Stop()
			return nil, ctx.Err()
		case <-t.C:
		}
	}
}

func (b *Backplane) Close() error { return b.store.close() }

// redisLease is a held advisory lease backed by a Redis key.
type redisLease struct {
	bp     *Backplane
	key    string
	holder string
	token  uint64
}

func (l *redisLease) Token() uint64 { return l.token }

func (l *redisLease) Renew(ctx context.Context, ttl time.Duration) error {
	ok, err := l.bp.store.renew(ctx, l.bp.lockKey(l.key), l.holder, leaseMs(ttl))
	if err != nil {
		return err
	}
	if !ok {
		return surf.ErrLeaseLost
	}
	return nil
}

func (l *redisLease) Release(ctx context.Context) error {
	return l.bp.store.release(ctx, l.bp.lockKey(l.key), l.holder)
}

// msOf converts a ttl to milliseconds; ttl <= 0 means "no expiry" (0).
func msOf(ttl time.Duration) int64 {
	if ttl <= 0 {
		return 0
	}
	return ttl.Milliseconds()
}

// leaseMs is like msOf but defaults a non-positive ttl to 15s so a lease always
// has an expiry (a lease must not be able to wedge a key forever).
func leaseMs(ttl time.Duration) int64 {
	if ttl <= 0 {
		return (15 * time.Second).Milliseconds()
	}
	return ttl.Milliseconds()
}

func randomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// --- optional at-rest encryption -------------------------------------------

type sealer struct{ aead cipher.AEAD }

func newSealer(secret []byte) (*sealer, error) {
	sum := sha256.Sum256(secret)
	block, err := aes.NewCipher(sum[:])
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &sealer{aead: aead}, nil
}

func (s *sealer) seal(plaintext []byte) ([]byte, error) {
	nonce := make([]byte, s.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return s.aead.Seal(nonce, nonce, plaintext, nil), nil
}

func (s *sealer) open(blob []byte) ([]byte, error) {
	ns := s.aead.NonceSize()
	if len(blob) < ns {
		return nil, errors.New("redisbackplane: ciphertext too short")
	}
	return s.aead.Open(nil, blob[:ns], blob[ns:], nil)
}

// --- respClient implements store -------------------------------------------

// Lua scripts: acquire couples SET NX with the fencing INCR atomically; renew
// and release are owner-checked so a stale holder cannot extend or delete a key
// that has since been re-acquired.
const (
	luaAcquire = `if redis.call('SET', KEYS[1], ARGV[1], 'NX', 'PX', ARGV[2]) then return redis.call('INCR', KEYS[2]) else return 0 end`
	luaRenew   = `if redis.call('GET', KEYS[1]) == ARGV[1] then return redis.call('PEXPIRE', KEYS[1], ARGV[2]) else return 0 end`
	luaRelease = `if redis.call('GET', KEYS[1]) == ARGV[1] then return redis.call('DEL', KEYS[1]) else return 0 end`
)

func bs(s string) []byte { return []byte(s) }

func (rc *respClient) get(ctx context.Context, key string) ([]byte, bool, error) {
	r, err := rc.do(ctx, bs("GET"), bs(key))
	if err != nil {
		return nil, false, err
	}
	if r.isNil {
		return nil, false, nil
	}
	return r.str, true, nil
}

func (rc *respClient) set(ctx context.Context, key string, val []byte, ttlMs int64) error {
	args := [][]byte{bs("SET"), bs(key), val}
	if ttlMs > 0 {
		args = append(args, bs("PX"), bs(strconv.FormatInt(ttlMs, 10)))
	}
	_, err := rc.do(ctx, args...)
	return err
}

func (rc *respClient) del(ctx context.Context, key string) error {
	_, err := rc.do(ctx, bs("DEL"), bs(key))
	return err
}

func (rc *respClient) acquire(ctx context.Context, lockKey, fenceKey, holder string, ttlMs int64) (uint64, bool, error) {
	r, err := rc.do(ctx,
		bs("EVAL"), bs(luaAcquire), bs("2"),
		bs(lockKey), bs(fenceKey),
		bs(holder), bs(strconv.FormatInt(ttlMs, 10)),
	)
	if err != nil {
		return 0, false, err
	}
	if r.num <= 0 {
		return 0, false, nil
	}
	return uint64(r.num), true, nil
}

func (rc *respClient) renew(ctx context.Context, lockKey, holder string, ttlMs int64) (bool, error) {
	r, err := rc.do(ctx,
		bs("EVAL"), bs(luaRenew), bs("1"),
		bs(lockKey), bs(holder), bs(strconv.FormatInt(ttlMs, 10)),
	)
	if err != nil {
		return false, err
	}
	return r.num == 1, nil
}

func (rc *respClient) release(ctx context.Context, lockKey, holder string) error {
	_, err := rc.do(ctx,
		bs("EVAL"), bs(luaRelease), bs("1"),
		bs(lockKey), bs(holder),
	)
	return err
}

// compile-time check that *Backplane satisfies the surf interface.
var _ surf.Backplane = (*Backplane)(nil)
