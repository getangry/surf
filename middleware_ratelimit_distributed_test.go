package surf

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// fakeSizer is a Backplane that also reports a fixed cluster size.
type fakeSizer struct {
	Backplane
	n int
}

func (f fakeSizer) Size() int { return f.n }

// notASizer embeds the Backplane interface (so it satisfies Backplane) but does
// not implement ClusterSizer.
type notASizer struct{ Backplane }

func countAllowed(mw Middleware, requests int) int {
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	allowed := 0
	for i := 0; i < requests; i++ {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.RemoteAddr = "1.2.3.4:1111"
		h.ServeHTTP(rec, req)
		if rec.Code == http.StatusOK {
			allowed++
		}
	}
	return allowed
}

func TestRateLimit_DistributedDividesBurstByClusterSize(t *testing.T) {
	// Configured burst of 10 across a 5-instance cluster => each instance
	// admits ~10/5 = 2 immediate requests before throttling. The
	// non-distributed limiter with the same config admits the full 10.
	base := RateLimitConfig{RequestsPerSecond: 1, Burst: 10}

	if got := countAllowed(RateLimit(base), 20); got != 10 {
		t.Fatalf("non-distributed allowed %d, want 10 (burst)", got)
	}

	distCfg := base
	distCfg.Distributed = true
	distCfg.Backplane = fakeSizer{Backplane: NewLocal(), n: 5}
	if got := countAllowed(RateLimit(distCfg), 20); got != 2 {
		t.Fatalf("distributed allowed %d, want 2 (burst/5)", got)
	}
}

func TestRateLimit_DistributedFallsBackWithoutSizer(t *testing.T) {
	// Distributed requested but the backplane is not a ClusterSizer: behaves
	// exactly like the non-distributed limiter.
	cfg := RateLimitConfig{RequestsPerSecond: 1, Burst: 7, Distributed: true, Backplane: notASizer{NewLocal()}}
	if got := countAllowed(RateLimit(cfg), 20); got != 7 {
		t.Fatalf("allowed %d, want 7 (full burst, no sizer)", got)
	}
}

func TestRateLimit_DistributedSingleInstanceMatchesConfigured(t *testing.T) {
	// A one-instance cluster is the configured rate (no division surprises).
	cfg := RateLimitConfig{RequestsPerSecond: 1, Burst: 6, Distributed: true,
		Backplane: fakeSizer{Backplane: NewLocal(), n: 1}}
	if got := countAllowed(RateLimit(cfg), 20); got != 6 {
		t.Fatalf("allowed %d, want 6", got)
	}
}
