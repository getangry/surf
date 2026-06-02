package surf

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// Through ServeHTTP, Store/Get must use the per-request reqState and leave the
// global fallback map untouched.
func TestRequestStorage_UsesReqStateForSurfRequests(t *testing.T) {
	app := NewApp()

	var got string
	var foundInState bool
	var thisReqInGlobal bool

	app.Get("/x", func(w http.ResponseWriter, r *http.Request) error {
		Store(r, "user_id", "u-42")
		SetMultiple(&r, map[string]any{"role": "admin"})
		v, ok := Get(r, "user_id")
		if ok {
			got = v.(string)
		}
		// The values must live in reqState, not the global fallback map.
		if st := stateFromRequest(r); st != nil {
			if _, ok := st.getData("role"); ok {
				foundInState = true
			}
		}
		storageMutex.RLock()
		_, thisReqInGlobal = requestStorage[r]
		storageMutex.RUnlock()
		return nil
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	app.ServeHTTP(rec, req)

	if got != "u-42" {
		t.Fatalf("Get(user_id) = %q, want u-42", got)
	}
	if !foundInState {
		t.Fatal("value not stored in reqState")
	}
	if thisReqInGlobal {
		t.Fatal("surf request leaked into the global fallback map")
	}
}

// Bare requests (no reqState) still work via the global fallback — the existing
// public-API behavior is preserved.
func TestRequestStorage_FallbackForBareRequests(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	Store(req, "k", "v")
	defer Delete(req)

	if v, ok := Get(req, "k"); !ok || v.(string) != "v" {
		t.Fatalf("Get = %v, %v; want v, true", v, ok)
	}
	storageMutex.RLock()
	_, present := requestStorage[req]
	storageMutex.RUnlock()
	if !present {
		t.Fatal("bare request should use the global fallback map")
	}

	Delete(req)
	storageMutex.RLock()
	_, stillThere := requestStorage[req]
	storageMutex.RUnlock()
	if stillThere {
		t.Fatal("Delete did not remove the bare request from the fallback map")
	}
}

// Get falls back to context values (e.g. those set by RequestIDMiddleware) when
// not present in reqState.
func TestRequestStorage_ContextFallbackForSurfRequests(t *testing.T) {
	app := NewApp()
	var gotID string
	app.Get("/y", func(w http.ResponseWriter, r *http.Request) error {
		gotID = GetRequestID(r)
		return nil
	})
	app.Use(RequestIDMiddleware(""))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/y", nil)
	app.ServeHTTP(rec, req)
	if gotID == "" {
		t.Fatal("request id not retrievable via context fallback through reqState")
	}
}
