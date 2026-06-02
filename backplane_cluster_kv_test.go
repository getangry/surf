package surf

import (
	"testing"
	"time"
)

func TestVersion_Ordering(t *testing.T) {
	a := version{Wall: 100, Logical: 0, Node: "a"}
	b := version{Wall: 100, Logical: 1, Node: "a"}
	c := version{Wall: 200, Logical: 0, Node: "a"}
	d := version{Wall: 100, Logical: 0, Node: "b"}
	if !b.after(a) {
		t.Fatal("higher logical should win")
	}
	if !c.after(b) {
		t.Fatal("higher wall should win")
	}
	if !d.after(a) {
		t.Fatal("node tiebreak should make b > a")
	}
	if a.after(a) {
		t.Fatal("after must be strict")
	}
}

func TestHLC_TickMonotonic(t *testing.T) {
	now := time.Unix(0, 1000)
	c := newHLC("n", func() time.Time { return now }, time.Second)
	v1 := c.tick()
	v2 := c.tick() // same physical time -> logical advances
	if !v2.after(v1) {
		t.Fatalf("tick not monotonic: %+v then %+v", v1, v2)
	}
	now = time.Unix(0, 5000)
	v3 := c.tick() // physical advanced -> wall advances, logical resets
	if !v3.after(v2) || v3.Logical != 0 {
		t.Fatalf("tick after physical advance wrong: %+v", v3)
	}
}

func TestHLC_ClampsFutureSkew(t *testing.T) {
	phys := time.Unix(100, 0)
	var skewed bool
	c := newHLC("n", func() time.Time { return phys }, time.Second)
	c.onSkew = func(remoteWall, physical int64) { skewed = true }
	// Remote claims a wall time an hour in the future.
	future := version{Wall: phys.Add(time.Hour).UnixNano(), Logical: 0, Node: "evil"}
	got := c.observe(future)
	if !skewed {
		t.Fatal("expected skew callback to fire")
	}
	// Our clock must not have jumped an hour ahead.
	if got.Wall > phys.Add(2*time.Second).UnixNano() {
		t.Fatalf("clock dragged forward by skewed peer: %d", got.Wall)
	}
}

func TestKVStore_SetGetDelete(t *testing.T) {
	now := time.Unix(1000, 0)
	nowFn := func() time.Time { return now }
	s := newKVStore(newHLC("n", nowFn, time.Second), nowFn, time.Hour)

	s.localSet("k", []byte("v"), 0)
	if v, ok := s.localGet("k"); !ok || string(v) != "v" {
		t.Fatalf("get = %q ok=%v", v, ok)
	}
	s.localDelete("k")
	if _, ok := s.localGet("k"); ok {
		t.Fatal("get after delete should be absent")
	}
}

func TestKVStore_ExpiryHidesValue(t *testing.T) {
	now := time.Unix(1000, 0)
	nowFn := func() time.Time { return now }
	s := newKVStore(newHLC("n", nowFn, time.Second), nowFn, time.Hour)
	s.localSet("k", []byte("v"), 50*time.Millisecond)
	if _, ok := s.localGet("k"); !ok {
		t.Fatal("should be present before expiry")
	}
	now = now.Add(time.Second)
	if _, ok := s.localGet("k"); ok {
		t.Fatal("should be absent after expiry")
	}
}

// twoStores returns two kvStores that share a physical clock pointer so tests
// can advance time for both.
func twoStores(t *testing.T) (a, b *kvStore, advance func(d time.Duration)) {
	t.Helper()
	now := time.Unix(1000, 0)
	nowFn := func() time.Time { return now }
	a = newKVStore(newHLC("a", nowFn, time.Second), nowFn, time.Hour)
	b = newKVStore(newHLC("b", nowFn, time.Second), nowFn, time.Hour)
	return a, b, func(d time.Duration) { now = now.Add(d) }
}

// gossip pushes every record a has that b is missing/older into b, and vice
// versa — one full anti-entropy exchange in both directions.
func gossip(a, b *kvStore) {
	for _, e := range a.entriesNewerThan(b.digest()) {
		b.mergeRemote(e)
	}
	for _, e := range b.entriesNewerThan(a.digest()) {
		a.mergeRemote(e)
	}
}

func TestKVStore_ConvergesAfterConcurrentWrites(t *testing.T) {
	a, b, _ := twoStores(t)
	// Concurrent writes to the same key on different nodes.
	a.localSet("k", []byte("from-a"), 0)
	b.localSet("k", []byte("from-b"), 0)

	gossip(a, b)
	gossip(a, b) // a second round to propagate any pulled-then-pushed values

	va, _ := a.localGet("k")
	vb, _ := b.localGet("k")
	if string(va) != string(vb) {
		t.Fatalf("did not converge: a=%q b=%q", va, vb)
	}
}

func TestKVStore_DeleteDoesNotResurrect(t *testing.T) {
	a, b, _ := twoStores(t)
	// a sets and both converge.
	a.localSet("k", []byte("v"), 0)
	gossip(a, b)
	if _, ok := b.localGet("k"); !ok {
		t.Fatal("b should have the value after gossip")
	}
	// a deletes k. b still holds the live value until they gossip.
	a.localDelete("k")
	gossip(a, b)
	if _, ok := b.localGet("k"); ok {
		t.Fatal("delete did not propagate to b")
	}
	// Now b "re-gossips" its (old) knowledge: the tombstone must win because its
	// version is higher, so the value must NOT resurrect on a.
	gossip(a, b)
	if _, ok := a.localGet("k"); ok {
		t.Fatal("value resurrected on a after delete")
	}
	if _, ok := b.localGet("k"); ok {
		t.Fatal("value resurrected on b after delete")
	}
}

func TestKVStore_ReapTombstone(t *testing.T) {
	now := time.Unix(1000, 0)
	nowFn := func() time.Time { return now }
	s := newKVStore(newHLC("n", nowFn, time.Second), nowFn, time.Minute)
	s.localSet("k", []byte("v"), 0)
	s.localDelete("k")
	if s.len() != 1 {
		t.Fatalf("len = %d, want 1 (tombstone present)", s.len())
	}
	now = now.Add(2 * time.Minute) // past tombstone TTL
	s.reap()
	if s.len() != 0 {
		t.Fatalf("len = %d, want 0 (tombstone reaped)", s.len())
	}
}

func TestKVStore_LateJoinerCatchesUp(t *testing.T) {
	a, b, _ := twoStores(t)
	a.localSet("x", []byte("1"), 0)
	a.localSet("y", []byte("2"), 0)
	a.localDelete("x")
	// b joins late and pulls everything via one exchange.
	gossip(a, b)
	if _, ok := b.localGet("x"); ok {
		t.Fatal("b should see x as deleted")
	}
	if v, ok := b.localGet("y"); !ok || string(v) != "2" {
		t.Fatalf("b should have y: %q ok=%v", v, ok)
	}
}
