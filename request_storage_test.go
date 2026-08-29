package surf

import (
	"context"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// probeKey is an unrelated context key used by the "downstream middleware"
// stand-ins below. Its only job is to force an r.WithContext call, which is
// what produces a fresh *http.Request pointer.
type probeKey struct{}

// deriveRequest is any middleware that attaches something to the context —
// a tracing span, a deadline, an RFC 9470 step-up recorder. It hands the next
// handler a DIFFERENT *http.Request than the one it received, which is the
// only thing needed to reproduce the bug this file guards.
func deriveRequest(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), probeKey{}, "recorder")))
	})
}

// TestSetSurvivesDownstreamWithContext is the regression test for a production
// incident: an authentication middleware called surf.SetUserID(&r, id), a
// middleware wired inside it called r.WithContext, and every handler
// downstream read surf.GetUserID(r) as "" — because the value had been filed
// in a package-level map under the PREVIOUS request pointer. Roughly fifty
// endpoints answered "you are not a member of this organization" to their own
// signed-in user, and no error was raised anywhere: the caller got a wrong
// answer, not a failure.
//
// Values must ride in the request's context, which r.WithContext preserves by
// construction.
func TestSetSurvivesDownstreamWithContext(t *testing.T) {
	auth := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			SetUserID(&r, "user-123")
			SetRequestID(&r, "req-abc")
			Set(&r, "tenant_id", "tenant-456")
			next.ServeHTTP(w, r)
		})
	}

	var userID, requestID string
	var tenantID any
	var tenantOK bool
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		userID = GetUserID(r)
		requestID = GetRequestID(r)
		tenantID, tenantOK = Get(r, "tenant_id")
	})

	// auth -> deriveRequest -> handler: the handler's request is two
	// WithContext copies away from the one auth wrote to.
	auth(deriveRequest(handler)).ServeHTTP(
		httptest.NewRecorder(),
		httptest.NewRequest(http.MethodGet, "/orgs/acme/projects", nil),
	)

	if userID != "user-123" {
		t.Errorf("GetUserID after downstream WithContext = %q, want %q", userID, "user-123")
	}
	if requestID != "req-abc" {
		t.Errorf("GetRequestID after downstream WithContext = %q, want %q", requestID, "req-abc")
	}
	if !tenantOK || tenantID != "tenant-456" {
		t.Errorf("Get(tenant_id) after downstream WithContext = %v, %v; want %q, true", tenantID, tenantOK, "tenant-456")
	}
}

// TestSetMultipleSurvivesDownstreamWithContext is the same guarantee for the
// bulk setter.
func TestSetMultipleSurvivesDownstreamWithContext(t *testing.T) {
	var got map[string]any
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = map[string]any{}
		for _, k := range []string{"a", "b", "c"} {
			if v, ok := Get(r, k); ok {
				got[k] = v
			}
		}
	})

	outer := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			SetMultiple(&r, map[string]any{"a": 1, "b": "two", "c": 3.0})
			next.ServeHTTP(w, r)
		})
	}

	outer(deriveRequest(handler)).ServeHTTP(
		httptest.NewRecorder(),
		httptest.NewRequest(http.MethodGet, "/", nil),
	)

	if len(got) != 3 || got["a"] != 1 || got["b"] != "two" || got["c"] != 3.0 {
		t.Errorf("SetMultiple values after downstream WithContext = %v, want a=1 b=two c=3", got)
	}
}

// TestWithFluentSurvivesDownstreamWithContext covers the With(&r) fluent API,
// which is just sugar over Set and must inherit the same guarantee.
func TestWithFluentSurvivesDownstreamWithContext(t *testing.T) {
	var got string
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = GetString(r, "role", "")
	})

	outer := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			With(&r).Set("role", "admin").SetUserID("u-1")
			next.ServeHTTP(w, r)
		})
	}

	outer(deriveRequest(handler)).ServeHTTP(
		httptest.NewRecorder(),
		httptest.NewRequest(http.MethodGet, "/", nil),
	)

	if got != "admin" {
		t.Errorf("GetString(role) after downstream WithContext = %q, want %q", got, "admin")
	}
}

// TestStoreSurvivesDownstreamWithContext covers Store, which receives the
// request rather than a pointer to it and so cannot rebind the caller's
// variable. It attaches the value to the request it was given, which every
// later r.WithContext copy inherits.
func TestStoreSurvivesDownstreamWithContext(t *testing.T) {
	var got string
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = GetString(r, "operation", "")
	})

	outer := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			Store(r, "operation", "list_users")
			next.ServeHTTP(w, r)
		})
	}

	outer(deriveRequest(handler)).ServeHTTP(
		httptest.NewRecorder(),
		httptest.NewRequest(http.MethodGet, "/", nil),
	)

	if got != "list_users" {
		t.Errorf("GetString(operation) after downstream WithContext = %q, want %q", got, "list_users")
	}
}

// TestSetThroughAppMiddlewareReachesHandler exercises the same path through a
// real App: an app-level middleware sets the user id, a second app-level
// middleware derives a new request, and the route handler must still see it.
func TestSetThroughAppMiddlewareReachesHandler(t *testing.T) {
	app := NewApp()
	app.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			SetUserID(&r, "user-789")
			next.ServeHTTP(w, r)
		})
	})
	app.Use(deriveRequest)

	var got string
	app.Get("/whoami", func(w http.ResponseWriter, r *http.Request) error {
		got = GetUserID(r)
		return nil
	})

	app.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/whoami", nil))

	if got != "user-789" {
		t.Errorf("GetUserID in handler = %q, want %q", got, "user-789")
	}
}

// TestDeleteIsRetainedNoOp pins the documented compatibility contract: Delete
// remains exported so existing callers keep compiling, but there is no longer
// any process-global state for it to remove, and it must not disturb the
// request it is handed.
func TestDeleteIsRetainedNoOp(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	Store(req, "key", "value")

	Delete(req)

	if v, ok := Get(req, "key"); !ok || v != "value" {
		t.Errorf("after Delete, Get(key) = %v, %v; want %q, true (Delete is a retained no-op)", v, ok, "value")
	}

	Delete(nil) // must not panic
}

// TestRequestsAreNotRetainedAfterUse is the leak test, asserted structurally:
// with no package-level map keyed by *http.Request, a request that has had
// values attached becomes garbage as soon as the caller drops it. Under the
// old implementation every Set pinned the whole *http.Request — headers,
// context and body — for the life of the process unless a consumer happened to
// wire surf's logging middleware, which is where the only Delete lived.
func TestRequestsAreNotRetainedAfterUse(t *testing.T) {
	const n = 64
	var collected atomic.Int64

	// A function, so the loop's stack slots are gone before the GC runs.
	func() {
		for i := 0; i < n; i++ {
			r := httptest.NewRequest(http.MethodGet, "/leak/"+strconv.Itoa(i), nil)
			runtime.SetFinalizer(r, func(*http.Request) { collected.Add(1) })

			// Exactly what a middleware does: attach values, hand the
			// (rebound) request downstream, then let it go.
			SetUserID(&r, "user-"+strconv.Itoa(i))
			Set(&r, "tenant_id", i)
			if GetUserID(r) == "" {
				t.Errorf("request %d: value not readable after Set", i)
			}
		}
	}()

	deadline := time.Now().Add(10 * time.Second)
	for collected.Load() < n && time.Now().Before(deadline) {
		runtime.GC()
		time.Sleep(5 * time.Millisecond)
	}

	if got := collected.Load(); got != n {
		t.Errorf("%d/%d handled requests were collected; the rest are still reachable from package state", got, n)
	}
}

// TestConcurrentRequestsAreIsolated replaces the old TestStorageConcurrency.
//
// The old test had 100 goroutines Store into ONE *http.Request, which only
// tested the global map's mutex — net/http serves each request from a single
// goroutine, and *http.Request has never been safe to mutate concurrently.
// The property that actually matters is that concurrent REQUESTS never see
// each other's values, and that a request's values are safe to read from
// several goroutines at once. Both hold without any lock, because each value
// lives in its own request's immutable context.
func TestConcurrentRequestsAreIsolated(t *testing.T) {
	const n = 100
	var wg sync.WaitGroup
	errs := make(chan string, n*4)

	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			want := "user-" + strconv.Itoa(i)
			r := httptest.NewRequest(http.MethodGet, "/", nil)
			SetUserID(&r, want)
			r = r.WithContext(context.WithValue(r.Context(), probeKey{}, i))

			// Several readers on one request, in parallel.
			var inner sync.WaitGroup
			for j := 0; j < 4; j++ {
				inner.Add(1)
				go func() {
					defer inner.Done()
					if got := GetUserID(r); got != want {
						errs <- "GetUserID = " + got + ", want " + want
					}
				}()
			}
			inner.Wait()
		}(i)
	}

	wg.Wait()
	close(errs)
	for msg := range errs {
		t.Error(msg)
	}
}

// TestLoggerStartTimeSurvivesWithContext guards the second pointer-keyed map
// that used to live in this package: LoggerMiddleware recorded a start time in
// a map keyed by *http.Request that nothing ever drained, and LoggerAfter —
// holding a different map instance, and often a different request pointer —
// never found it, so every latency it reported was ~0.
func TestLoggerStartTimeSurvivesWithContext(t *testing.T) {
	before := LoggerMiddleware("{method} {path}")
	after := LoggerAfter("{method} {path} {latency_ms}ms")

	r := httptest.NewRequest(http.MethodGet, "/slow", nil)
	rw := NewResponseWriter(httptest.NewRecorder())
	rw.StartTime = time.Now()

	if err := before(rw, r); err != nil {
		t.Fatalf("LoggerMiddleware returned %v", err)
	}

	start, ok := requestStartTime(r)
	if !ok {
		t.Fatal("LoggerMiddleware did not record a start time on the request")
	}

	// A downstream middleware derives a new request; the start time must ride
	// along with it.
	r2 := r.WithContext(context.WithValue(r.Context(), probeKey{}, "derived"))
	start2, ok := requestStartTime(r2)
	if !ok {
		t.Fatal("start time lost across r.WithContext")
	}
	if !start2.Equal(start) {
		t.Errorf("start time after WithContext = %v, want %v", start2, start)
	}

	if err := after(rw, r2); err != nil {
		t.Fatalf("LoggerAfter returned %v", err)
	}
}
