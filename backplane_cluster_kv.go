package surf

import (
	"sync"
	"time"
)

// This file is the replicated key/value store behind the clustered backplane.
// It is a last-write-wins (LWW) register map: each key holds a value plus a
// version, and a higher version always wins a merge. Versions come from a
// Hybrid Logical Clock so that:
//
//   - concurrent writes from different nodes order deterministically (every
//     node picks the same winner), and
//   - a node with a skewed wall clock cannot permanently win or lose every
//     write — observed remote times advance the local clock, and a remote time
//     too far in the future is clamped.
//
// Deletes are tombstones: a delete writes a versioned "deleted" marker that
// propagates like any other write and is reaped only after a TTL longer than
// any expected partition, so a delete cannot be undone by a lagging replica
// re-gossiping the old value.
//
// Stored values are opaque bytes. The node layer seals them at rest (AES-GCM)
// before they reach the store and opens them on read, so a replica that merely
// holds a key it never serves keeps it encrypted in memory.

// version is a Hybrid Logical Clock timestamp plus the originating node, used to
// totally order writes.
type version struct {
	Wall    int64  `json:"w"` // unix nanoseconds (logical, HLC-advanced)
	Logical uint32 `json:"l"` // tiebreaker for same-wall events
	Node    string `json:"n"` // final tiebreaker; makes merge commutative
}

func (v version) isZero() bool { return v.Wall == 0 && v.Logical == 0 && v.Node == "" }

// after reports whether v strictly succeeds o in the total order.
func (v version) after(o version) bool {
	if v.Wall != o.Wall {
		return v.Wall > o.Wall
	}
	if v.Logical != o.Logical {
		return v.Logical > o.Logical
	}
	return v.Node > o.Node
}

// hlc is a Hybrid Logical Clock. tick stamps a local event; observe advances the
// clock toward a remote timestamp seen in gossip.
type hlc struct {
	mu       sync.Mutex
	node     string
	wall     int64
	logical  uint32
	now      func() time.Time
	maxDrift time.Duration
	onSkew   func(remoteWall, physical int64)
}

func newHLC(node string, now func() time.Time, maxDrift time.Duration) *hlc {
	if now == nil {
		now = time.Now
	}
	if maxDrift <= 0 {
		maxDrift = 500 * time.Millisecond
	}
	return &hlc{node: node, now: now, maxDrift: maxDrift}
}

func (c *hlc) tick() version {
	c.mu.Lock()
	defer c.mu.Unlock()
	phys := c.now().UnixNano()
	if phys > c.wall {
		c.wall = phys
		c.logical = 0
	} else {
		c.logical++
	}
	return version{Wall: c.wall, Logical: c.logical, Node: c.node}
}

// observe advances the clock having seen a remote version, per the HLC update
// rule, and returns the new local time. A remote wall time more than maxDrift
// ahead of the physical clock is clamped so a badly-skewed peer cannot drag the
// whole cluster's clock forward.
func (c *hlc) observe(remote version) version {
	c.mu.Lock()
	defer c.mu.Unlock()
	phys := c.now().UnixNano()
	rWall := remote.Wall
	if rWall-phys > int64(c.maxDrift) {
		if c.onSkew != nil {
			c.onSkew(rWall, phys)
		}
		rWall = phys
	}

	newWall := c.wall
	if phys > newWall {
		newWall = phys
	}
	if rWall > newWall {
		newWall = rWall
	}

	switch {
	case newWall == c.wall && newWall == rWall:
		if remote.Logical > c.logical {
			c.logical = remote.Logical
		}
		c.logical++
	case newWall == c.wall:
		c.logical++
	case newWall == rWall:
		c.logical = remote.Logical + 1
	default:
		c.logical = 0
	}
	c.wall = newWall
	return version{Wall: c.wall, Logical: c.logical, Node: c.node}
}

// entryMsg is both the stored record and its wire form. Value is opaque (sealed
// by the node layer); it is nil for a tombstone.
type entryMsg struct {
	Key       string  `json:"k"`
	Value     []byte  `json:"v,omitempty"`
	Version   version `json:"ver"`
	ExpireAt  int64   `json:"exp,omitempty"` // unix nanos; 0 = no expiry
	Deleted   bool    `json:"del,omitempty"`
	DeletedAt int64   `json:"dat,omitempty"` // unix nanos; for tombstone reaping
}

// kvStore is the replicated map. Methods are safe for concurrent use.
type kvStore struct {
	mu           sync.Mutex
	clock        *hlc
	entries      map[string]entryMsg
	now          func() time.Time
	tombstoneTTL time.Duration
}

func newKVStore(clock *hlc, now func() time.Time, tombstoneTTL time.Duration) *kvStore {
	if now == nil {
		now = time.Now
	}
	if tombstoneTTL <= 0 {
		tombstoneTTL = 24 * time.Hour
	}
	return &kvStore{
		clock:        clock,
		entries:      make(map[string]entryMsg),
		now:          now,
		tombstoneTTL: tombstoneTTL,
	}
}

// localSet stores a locally-originated value and returns the record to
// replicate to peers.
func (s *kvStore) localSet(key string, sealed []byte, ttl time.Duration) entryMsg {
	s.mu.Lock()
	defer s.mu.Unlock()
	e := entryMsg{Key: key, Value: sealed, Version: s.clock.tick()}
	if ttl > 0 {
		e.ExpireAt = s.now().Add(ttl).UnixNano()
	}
	s.entries[key] = e
	return e
}

// localDelete writes a tombstone and returns it for replication.
func (s *kvStore) localDelete(key string) entryMsg {
	s.mu.Lock()
	defer s.mu.Unlock()
	e := entryMsg{Key: key, Version: s.clock.tick(), Deleted: true, DeletedAt: s.now().UnixNano()}
	s.entries[key] = e
	return e
}

// localGet returns the sealed value for key, or ok=false if absent, deleted, or
// expired.
func (s *kvStore) localGet(key string) ([]byte, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.entries[key]
	if !ok || e.Deleted {
		return nil, false
	}
	if e.ExpireAt > 0 && s.now().UnixNano() >= e.ExpireAt {
		return nil, false
	}
	return e.Value, true
}

// mergeRemote applies a record received from a peer using LWW. It returns true
// if the local state changed. The local clock observes the remote version
// regardless, so causality is tracked even for ignored (older) updates.
func (s *kvStore) mergeRemote(e entryMsg) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.clock.observe(e.Version)
	cur, ok := s.entries[e.Key]
	if !ok || e.Version.after(cur.Version) {
		s.entries[e.Key] = e
		return true
	}
	return false
}

// digest returns key -> version for every record (including tombstones), the
// input to anti-entropy.
func (s *kvStore) digest() map[string]version {
	s.mu.Lock()
	defer s.mu.Unlock()
	d := make(map[string]version, len(s.entries))
	for k, e := range s.entries {
		d[k] = e.Version
	}
	return d
}

// entriesNewerThan returns the records this store holds that a peer (described
// by its digest) is missing or has an older version of — i.e. what to push to
// bring that peer up to date.
func (s *kvStore) entriesNewerThan(remote map[string]version) []entryMsg {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []entryMsg
	for k, e := range s.entries {
		rv, ok := remote[k]
		if !ok || e.Version.after(rv) {
			out = append(out, e)
		}
	}
	return out
}

// reap drops tombstones and long-expired entries once they are older than the
// tombstone TTL, by which time every reachable peer has converged on the delete.
func (s *kvStore) reap() {
	s.mu.Lock()
	defer s.mu.Unlock()
	cutoff := s.now().Add(-s.tombstoneTTL).UnixNano()
	for k, e := range s.entries {
		switch {
		case e.Deleted && e.DeletedAt > 0 && e.DeletedAt < cutoff:
			delete(s.entries, k)
		case !e.Deleted && e.ExpireAt > 0 && e.ExpireAt < cutoff:
			delete(s.entries, k)
		}
	}
}

// len reports the number of records held (including tombstones); used in tests
// and for a soft size warning.
func (s *kvStore) len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.entries)
}
