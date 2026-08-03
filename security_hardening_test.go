package surf

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// The default rate-limit key must be the connecting peer, so a client cannot
// bypass the limiter by rotating a spoofed X-Forwarded-For header.
func TestRateLimit_DefaultKeyIgnoresSpoofedXFF(t *testing.T) {
	app := NewApp()
	app.Use(RateLimit(RateLimitConfig{RequestsPerSecond: 1, Burst: 1}))
	app.Get("/x", func(w http.ResponseWriter, r *http.Request) error { return nil })

	mk := func(xff string) *http.Request {
		r := httptest.NewRequest("GET", "/x", nil)
		r.RemoteAddr = "203.0.113.9:5000" // same peer every time
		r.Header.Set("X-Forwarded-For", xff)
		return r
	}

	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, mk("1.1.1.1"))
	if rec.Code != http.StatusOK {
		t.Fatalf("first request: code = %d, want 200", rec.Code)
	}

	// Same peer, different spoofed XFF: must still be limited (429), because the
	// header is not trusted without TrustedProxies.
	rec = httptest.NewRecorder()
	app.ServeHTTP(rec, mk("2.2.2.2"))
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("spoofed-XFF second request: code = %d, want 429", rec.Code)
	}
}

func TestDefaultRateLimitConfigKeyFuncIsPeer(t *testing.T) {
	kf := DefaultRateLimitConfig().KeyFunc
	if kf == nil {
		t.Fatal("default KeyFunc is nil")
	}
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "198.51.100.4:1111"
	r.Header.Set("X-Forwarded-For", "1.2.3.4")
	if got := kf(r); got != "198.51.100.4" {
		t.Errorf("default key = %q, want peer 198.51.100.4 (XFF must be ignored)", got)
	}
}

// A trusted proxy forwarding a non-IP token must not have that token surface as
// the client address.
func TestIPFromRequest_InvalidXFFTokenSkipped(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "10.0.0.5:443"
	r.Header.Set("X-Forwarded-For", "not-an-ip")
	if ip := IPFromRequest(r, []string{"10.0.0.0/8"}); ip != "10.0.0.5" {
		t.Errorf("ip = %q, want peer 10.0.0.5 (garbage token must be skipped)", ip)
	}

	// A valid client IP preceding the garbage token is still returned.
	r.Header.Set("X-Forwarded-For", "203.0.113.1, garbage")
	if ip := IPFromRequest(r, []string{"10.0.0.0/8"}); ip != "203.0.113.1" {
		t.Errorf("ip = %q, want 203.0.113.1", ip)
	}
}

// Unknown HTTP methods are folded into a single "other" label so the metrics
// map cannot grow without bound.
func TestMetrics_UnknownMethodFoldedToOther(t *testing.T) {
	m := NewMetricsRegistry()
	m.record("FOOBAR", 200, 0)
	m.record("BAZ", 200, 0)
	m.record("GET", 200, 0)

	body := m.exposition()
	if !contains(body, `surf_http_requests_total{method="other",status="200"} 2`) {
		t.Errorf("unknown methods not folded to \"other\":\n%s", body)
	}
	if !contains(body, `surf_http_requests_total{method="GET",status="200"} 1`) {
		t.Errorf("known method miscounted:\n%s", body)
	}
	if contains(body, "FOOBAR") || contains(body, "BAZ") {
		t.Errorf("raw attacker method leaked into metrics:\n%s", body)
	}
}

// With a wildcard origin and credentials enabled, the middleware must reflect
// the concrete origin (never "*") and set Vary: Origin.
func TestCORS_CredentialsWildcardReflectsOrigin(t *testing.T) {
	app := NewApp()
	app.Use(CORS(CORSConfig{AllowOrigins: []string{"*"}, AllowCredentials: true}))
	app.Get("/x", func(w http.ResponseWriter, r *http.Request) error { return nil })

	req := httptest.NewRequest("GET", "/x", nil)
	req.Header.Set("Origin", "https://app.example.com")
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)

	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "https://app.example.com" {
		t.Errorf("Allow-Origin = %q, want reflected origin (not *)", got)
	}
	if got := rec.Header().Get("Access-Control-Allow-Credentials"); got != "true" {
		t.Errorf("Allow-Credentials = %q, want true", got)
	}
	if got := rec.Header().Get("Vary"); !contains(got, "Origin") {
		t.Errorf("Vary = %q, want it to include Origin", got)
	}
}

// An MCP tool call whose path-parameter value contains "/" must be rejected,
// not substituted into the path where it would inject extra segments and route
// the in-process request to a different endpoint than the tool declares.
func TestMCP_PathParamSlashRejected(t *testing.T) {
	app := newMCPApp(t, MCPOptions{})

	resp := rpc(t, app, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":`+
		`{"name":"create_user","arguments":{"team":"a/../b","name":"x"}}}`)
	if resp.Error != nil {
		t.Fatalf("unexpected JSON-RPC error: %+v", resp.Error)
	}
	result := resp.Result.(map[string]any)
	if isErr, _ := result["isError"].(bool); !isErr {
		t.Errorf("isError = false, want true for slash-bearing path param; result=%v", result)
	}
	content := result["content"].([]any)
	text := content[0].(map[string]any)["text"].(string)
	if !contains(text, "must not contain") {
		t.Errorf("body = %q, want a path-parameter rejection", text)
	}
}

// A normal path-parameter value still dispatches correctly through in-process
// MCP invocation (guards against the slash check over-rejecting).
func TestMCP_NormalPathParamStillWorks(t *testing.T) {
	app := newMCPApp(t, MCPOptions{})

	resp := rpc(t, app, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":`+
		`{"name":"create_user","arguments":{"team":"acme","name":"Ada"}}}`)
	if resp.Error != nil {
		t.Fatalf("unexpected JSON-RPC error: %+v", resp.Error)
	}
	result := resp.Result.(map[string]any)
	if isErr, _ := result["isError"].(bool); isErr {
		t.Fatalf("isError = true, want successful dispatch; result=%v", result)
	}
	text := result["content"].([]any)[0].(map[string]any)["text"].(string)
	if !contains(text, `"id":"acme"`) || !contains(text, `"name":"Ada"`) {
		t.Errorf("body = %q, want team/name echoed back", text)
	}
}

// Omitting a path-parameter argument must be rejected outright. The unfilled
// ":team" segment would otherwise stay in the path literally, still match the
// route, and run the handler with ":team" as the parameter value.
func TestMCP_MissingPathParamRejected(t *testing.T) {
	app := newMCPApp(t, MCPOptions{})

	resp := rpc(t, app, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":`+
		`{"name":"create_user","arguments":{"name":"Ada"}}}`)
	if resp.Error != nil {
		t.Fatalf("unexpected JSON-RPC error: %+v", resp.Error)
	}
	result := resp.Result.(map[string]any)
	if isErr, _ := result["isError"].(bool); !isErr {
		t.Errorf("isError = false, want true for omitted path param; result=%v", result)
	}
	text := result["content"].([]any)[0].(map[string]any)["text"].(string)
	if !contains(text, "missing required path parameter: team") {
		t.Errorf("body = %q, want a missing-path-parameter rejection", text)
	}
}

// The in-process sub-request must carry the caller's RemoteAddr, or IP-keyed
// middleware (rate limiting, logging, trusted-proxy resolution) would see an
// empty address and a tool call would become a way around it.
func TestMCP_SubRequestCarriesRemoteAddr(t *testing.T) {
	var gotAddr, gotHost string
	app := NewApp()
	MCPHandle(app, "POST", "/teams/:team/users",
		func(c *Context, req mcpCreateReq) (mcpUser, error) {
			gotAddr = c.Request.RemoteAddr
			gotHost = c.Request.Host
			return mcpUser{ID: c.Param("team"), Name: req.Name}, nil
		})
	app.MCP("/mcp", MCPOptions{})

	resp := rpc(t, app, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":`+
		`{"name":"create_user","arguments":{"team":"acme","name":"Ada"}}}`)
	if resp.Error != nil {
		t.Fatalf("unexpected JSON-RPC error: %+v", resp.Error)
	}
	// httptest.NewRequest, used by the rpc helper, sets these on the outer call.
	if gotAddr != "192.0.2.1:1234" {
		t.Errorf("sub-request RemoteAddr = %q, want the caller's 192.0.2.1:1234", gotAddr)
	}
	if gotHost != "example.com" {
		t.Errorf("sub-request Host = %q, want the caller's example.com", gotHost)
	}
}

// A specific allowed origin must be echoed with Vary: Origin to prevent shared
// caches from serving one origin's response to another.
func TestCORS_SpecificOriginSetsVary(t *testing.T) {
	app := NewApp()
	app.Use(CORS(CORSConfig{AllowOrigins: []string{"https://ok.example.com"}}))
	app.Get("/x", func(w http.ResponseWriter, r *http.Request) error { return nil })

	req := httptest.NewRequest("GET", "/x", nil)
	req.Header.Set("Origin", "https://ok.example.com")
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)

	if got := rec.Header().Get("Vary"); !contains(got, "Origin") {
		t.Errorf("Vary = %q, want it to include Origin", got)
	}
}
