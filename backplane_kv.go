package surf

import (
	"context"
	"encoding/json"
	"time"
)

// KV is a typed view over a Backplane's key/value store. It JSON-encodes values
// of type T on the way in and decodes them on the way out, so callers work with
// domain types instead of byte slices. Keys are namespaced by a prefix to keep
// unrelated data sets from colliding in the shared store.
//
// It mirrors the type-safe service container (Provide[T]/Service[T]): the type
// parameter cannot be confused at the call site, so a Get can never silently
// decode into the wrong shape.
//
//	type Session struct{ UserID string; CSRF string }
//	sessions := surf.NewKV[Session](app.Backplane(), "sess:")
//	sessions.Set(ctx, sid, Session{UserID: "u1"}, 30*time.Minute)
//	s, ok, err := sessions.Get(ctx, sid)
type KV[T any] struct {
	bp     Backplane
	prefix string
}

// NewKV returns a typed view over bp. All keys are stored under prefix, which
// may be empty.
func NewKV[T any](bp Backplane, prefix string) KV[T] {
	return KV[T]{bp: bp, prefix: prefix}
}

func (k KV[T]) key(id string) string { return k.prefix + id }

// Get decodes the value stored under id. The boolean is false (with a nil
// error) when the key is absent or expired.
func (k KV[T]) Get(ctx context.Context, id string) (T, bool, error) {
	var v T
	raw, ok, err := k.bp.Get(ctx, k.key(id))
	if err != nil || !ok {
		return v, ok, err
	}
	if err := json.Unmarshal(raw, &v); err != nil {
		return v, false, err
	}
	return v, true, nil
}

// Set encodes v and stores it under id with the given ttl (see Backplane.Set
// for ttl semantics).
func (k KV[T]) Set(ctx context.Context, id string, v T, ttl time.Duration) error {
	raw, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return k.bp.Set(ctx, k.key(id), raw, ttl)
}

// Delete removes the value stored under id.
func (k KV[T]) Delete(ctx context.Context, id string) error {
	return k.bp.Delete(ctx, k.key(id))
}
