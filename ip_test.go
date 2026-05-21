package surf

import (
	"net/http/httptest"
	"testing"
)

func TestIPFromRequestNoProxies(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "203.0.113.7:54321"
	r.Header.Set("X-Forwarded-For", "1.2.3.4")
	// With no trusted proxies, the header is ignored.
	if ip := IPFromRequest(r, nil); ip != "203.0.113.7" {
		t.Errorf("ip = %q, want 203.0.113.7", ip)
	}
}

func TestIPFromRequestTrustedProxy(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "10.0.0.5:443"
	r.Header.Set("X-Forwarded-For", "198.51.100.9, 10.0.0.5")
	// Peer is in the trusted range; walk XFF right-to-left past trusted hops.
	if ip := IPFromRequest(r, []string{"10.0.0.0/8"}); ip != "198.51.100.9" {
		t.Errorf("ip = %q, want 198.51.100.9", ip)
	}
}

func TestIPFromRequestUntrustedPeer(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "203.0.113.7:9000"
	r.Header.Set("X-Forwarded-For", "1.2.3.4")
	// Peer is not a trusted proxy; the spoofable header is not honored.
	if ip := IPFromRequest(r, []string{"10.0.0.0/8"}); ip != "203.0.113.7" {
		t.Errorf("ip = %q, want 203.0.113.7", ip)
	}
}

func TestKeyByIP(t *testing.T) {
	key := KeyByIP("10.0.0.0/8")
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "10.0.0.1:1234"
	r.Header.Set("X-Forwarded-For", "192.0.2.50")
	if got := key(r); got != "192.0.2.50" {
		t.Errorf("key = %q, want 192.0.2.50", got)
	}
}
