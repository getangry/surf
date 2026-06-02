package surf

// wireMsg is the single message type exchanged between cluster nodes, sealed
// inside a secureConn frame. One struct covers every RPC; unused fields stay at
// their zero value and are omitted by the JSON encoder. Keeping it in one type
// keeps the dispatch (backplane_cluster.go) trivial.
type wireMsg struct {
	Kind msgKind `json:"kind"`

	// Membership (ping / indirect ping).
	Gossip []memberInfo `json:"gossip,omitempty"`
	Target string       `json:"target,omitempty"`
	OK     bool         `json:"ok,omitempty"`

	// KV replication (push / sync).
	Entries []entryMsg         `json:"entries,omitempty"`
	Digest  map[string]version `json:"digest,omitempty"`

	// Distributed locks.
	LockKey string `json:"lockKey,omitempty"`
	LeaseMs int64  `json:"leaseMs,omitempty"`
	Token   uint64 `json:"token,omitempty"`
	Holder  string `json:"holder,omitempty"`

	// Err carries an application-level error string back to the caller.
	Err string `json:"err,omitempty"`
}

// msgKind identifies the RPC carried by a wireMsg.
type msgKind uint8

const (
	kindPing msgKind = iota + 1
	kindIndirectPing
	kindPush // replicate KV records (Set propagation + anti-entropy push)
	kindSync // anti-entropy pull: exchange digests, receive newer records
	kindLockAcquire
	kindLockRefresh
	kindLockRelease
)
