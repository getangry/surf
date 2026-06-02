package surf

import (
	"context"
	"net/http"
	"sync"
)

// stateKey is the single context key under which per-request framework state
// is stored.
type stateKey struct{}

// paramKV is one resolved path parameter. Parameters are kept in a slice
// rather than a map: routes have very few of them, so a linear scan is faster
// and avoids a map allocation per request.
type paramKV struct {
	key string
	val string
}

// inlineParams is the number of path parameters a request can resolve without
// a separate slice allocation. Routes rarely exceed this.
const inlineParams = 8

// reqState holds all per-request framework data: the owning App, the wrapped
// ResponseWriter, and resolved path parameters. Exactly one is attached to
// each request under stateKey.
//
// reqState implements context.Context by embedding the request's original
// context and overriding Value. This lets surf attach every piece of
// per-request state with a single r.WithContext call instead of one
// context.WithValue per datum — the difference between ~12 allocations and 2.
//
// reqState is intentionally NOT pooled. Middleware such as Timeout detaches
// the handler into a goroutine that can outlive ServeHTTP; recycling the
// state would let that goroutine write through a freed ResponseWriter. A
// per-request allocation keeps the object alive for exactly as long as
// something references it.
type reqState struct {
	context.Context // the request's original context; provides Deadline/Done/Err
	app             *App
	rw              ResponseWriter
	params          []paramKV
	paramsBuf       [inlineParams]paramKV

	// data holds per-request key/value storage written via Store/Set/Get. It
	// lives here rather than in a global map so it is freed with the request
	// and needs no process-wide lock. Lazily allocated; guarded by dataMu
	// because Timeout middleware can run the handler in a separate goroutine.
	dataMu sync.Mutex
	data   map[string]any
}

// setData stores a per-request value.
func (st *reqState) setData(key string, value any) {
	st.dataMu.Lock()
	defer st.dataMu.Unlock()
	if st.data == nil {
		st.data = make(map[string]any)
	}
	st.data[key] = value
}

// getData retrieves a per-request value.
func (st *reqState) getData(key string) (any, bool) {
	st.dataMu.Lock()
	defer st.dataMu.Unlock()
	v, ok := st.data[key]
	return v, ok
}

// clearData drops all per-request values.
func (st *reqState) clearData() {
	st.dataMu.Lock()
	st.data = nil
	st.dataMu.Unlock()
}

// Value returns the reqState itself for stateKey, delegating everything else
// to the embedded parent context.
func (st *reqState) Value(key any) any {
	if _, ok := key.(stateKey); ok {
		return st
	}
	return st.Context.Value(key)
}

// newReqState creates the per-request state, parenting it on the request's
// current context and pointing params at the inline buffer.
func newReqState(app *App, parent context.Context) *reqState {
	st := &reqState{Context: parent, app: app}
	st.params = st.paramsBuf[:0]
	return st
}

// stateFromRequest returns the reqState attached to r, or nil if r did not
// pass through surf's ServeHTTP.
func stateFromRequest(r *http.Request) *reqState {
	st, _ := r.Context().Value(stateKey{}).(*reqState)
	return st
}
