package surf

import (
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// defaultLatencyBuckets are the histogram upper bounds, in seconds, used by a
// MetricsRegistry created without custom buckets. They mirror the Prometheus
// client default buckets.
var defaultLatencyBuckets = []float64{
	0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10,
}

// counterKey identifies a request counter by method and response status.
type counterKey struct {
	method string
	status int
}

// MetricsRegistry collects per-request HTTP metrics and exposes them in the
// Prometheus text exposition format. It has no external dependencies.
//
//	m := surf.NewMetricsRegistry()
//	app.Use(m.Middleware())
//	app.Get("/metrics", m.Handler())
type MetricsRegistry struct {
	mu         sync.Mutex
	counts     map[counterKey]uint64
	buckets    []float64
	bucketHits []uint64 // cumulative: bucketHits[i] = requests with latency <= buckets[i]
	durSum     float64
	durCount   uint64
	inFlight   int64 // accessed atomically
}

// NewMetricsRegistry creates a MetricsRegistry with default latency buckets.
func NewMetricsRegistry() *MetricsRegistry {
	return NewMetricsRegistryWithBuckets(defaultLatencyBuckets)
}

// NewMetricsRegistryWithBuckets creates a MetricsRegistry with custom latency
// histogram buckets (upper bounds in seconds). The buckets are sorted.
func NewMetricsRegistryWithBuckets(buckets []float64) *MetricsRegistry {
	b := append([]float64{}, buckets...)
	sort.Float64s(b)
	return &MetricsRegistry{
		counts:     make(map[counterKey]uint64),
		buckets:    b,
		bucketHits: make([]uint64, len(b)),
	}
}

// record folds a single completed request into the registry.
func (m *MetricsRegistry) record(method string, status int, d time.Duration) {
	secs := d.Seconds()
	m.mu.Lock()
	m.counts[counterKey{method: method, status: status}]++
	m.durSum += secs
	m.durCount++
	for i, upper := range m.buckets {
		if secs <= upper {
			m.bucketHits[i]++
		}
	}
	m.mu.Unlock()
}

// Middleware returns a middleware that records every request it wraps.
func (m *MetricsRegistry) Middleware() Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			atomic.AddInt64(&m.inFlight, 1)
			start := time.Now()
			rw := NewResponseWriter(w)

			defer func() {
				atomic.AddInt64(&m.inFlight, -1)
				m.record(r.Method, rw.Status(), time.Since(start))
			}()

			next.ServeHTTP(rw, r)
		})
	}
}

// Handler returns a HandlerFunc that writes the collected metrics in the
// Prometheus text exposition format. Register it with app.Get("/metrics", …).
func (m *MetricsRegistry) Handler() HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) error {
		body := m.exposition()
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(body))
		return nil
	}
}

// exposition renders the current metrics as Prometheus text.
func (m *MetricsRegistry) exposition() string {
	m.mu.Lock()
	keys := make([]counterKey, 0, len(m.counts))
	for k := range m.counts {
		keys = append(keys, k)
	}
	counts := make(map[counterKey]uint64, len(m.counts))
	for k, v := range m.counts {
		counts[k] = v
	}
	bucketHits := append([]uint64{}, m.bucketHits...)
	buckets := m.buckets
	durSum := m.durSum
	durCount := m.durCount
	m.mu.Unlock()

	sort.Slice(keys, func(i, j int) bool {
		if keys[i].method != keys[j].method {
			return keys[i].method < keys[j].method
		}
		return keys[i].status < keys[j].status
	})

	var b strings.Builder
	b.WriteString("# HELP surf_http_requests_total Total number of HTTP requests handled.\n")
	b.WriteString("# TYPE surf_http_requests_total counter\n")
	for _, k := range keys {
		fmt.Fprintf(&b, "surf_http_requests_total{method=%q,status=\"%d\"} %d\n",
			k.method, k.status, counts[k])
	}

	b.WriteString("# HELP surf_http_requests_in_flight Current number of in-flight HTTP requests.\n")
	b.WriteString("# TYPE surf_http_requests_in_flight gauge\n")
	fmt.Fprintf(&b, "surf_http_requests_in_flight %d\n", atomic.LoadInt64(&m.inFlight))

	b.WriteString("# HELP surf_http_request_duration_seconds HTTP request latency in seconds.\n")
	b.WriteString("# TYPE surf_http_request_duration_seconds histogram\n")
	for i, upper := range buckets {
		fmt.Fprintf(&b, "surf_http_request_duration_seconds_bucket{le=%q} %d\n",
			strconv.FormatFloat(upper, 'g', -1, 64), bucketHits[i])
	}
	fmt.Fprintf(&b, "surf_http_request_duration_seconds_bucket{le=\"+Inf\"} %d\n", durCount)
	fmt.Fprintf(&b, "surf_http_request_duration_seconds_sum %s\n",
		strconv.FormatFloat(durSum, 'g', -1, 64))
	fmt.Fprintf(&b, "surf_http_request_duration_seconds_count %d\n", durCount)

	return b.String()
}
