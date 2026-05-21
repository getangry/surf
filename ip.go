package surf

import (
	"net"
	"net/http"
	"strings"
)

// parseProxyNets converts a list of CIDR blocks or bare IP addresses into
// *net.IPNet values. Invalid entries are skipped.
func parseProxyNets(entries []string) []*net.IPNet {
	nets := make([]*net.IPNet, 0, len(entries))
	for _, entry := range entries {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		if _, ipNet, err := net.ParseCIDR(entry); err == nil {
			nets = append(nets, ipNet)
			continue
		}
		if ip := net.ParseIP(entry); ip != nil {
			bits := 32
			if ip.To4() == nil {
				bits = 128
			}
			nets = append(nets, &net.IPNet{IP: ip, Mask: net.CIDRMask(bits, bits)})
		}
	}
	return nets
}

// ipInNets reports whether ip (a string) falls within any of nets.
func ipInNets(ip string, nets []*net.IPNet) bool {
	parsed := net.ParseIP(ip)
	if parsed == nil {
		return false
	}
	for _, n := range nets {
		if n.Contains(parsed) {
			return true
		}
	}
	return false
}

// hostOnly strips the port from a host:port address, leaving the host.
func hostOnly(addr string) string {
	if host, _, err := net.SplitHostPort(addr); err == nil {
		return host
	}
	return addr
}

// IPFromRequest returns the client IP address for r.
//
// When trustedProxies is empty, the connecting peer's address (RemoteAddr) is
// returned and X-Forwarded-For is ignored — the safe default, since the header
// is client-controlled. When trustedProxies is non-empty and the peer is one
// of them, X-Forwarded-For is walked from right to left and the first address
// outside the trusted set is returned.
func IPFromRequest(r *http.Request, trustedProxies []string) string {
	remoteIP := hostOnly(r.RemoteAddr)
	if len(trustedProxies) == 0 {
		return remoteIP
	}

	nets := parseProxyNets(trustedProxies)
	if !ipInNets(remoteIP, nets) {
		// The direct peer is not a trusted proxy; do not trust the header.
		return remoteIP
	}

	xff := r.Header.Get("X-Forwarded-For")
	if xff == "" {
		return remoteIP
	}
	parts := strings.Split(xff, ",")
	for i := len(parts) - 1; i >= 0; i-- {
		candidate := strings.TrimSpace(parts[i])
		if candidate == "" {
			continue
		}
		if !ipInNets(candidate, nets) {
			return candidate
		}
	}
	return remoteIP
}

// KeyByIP returns a rate-limiter KeyFunc that identifies clients by IP address,
// honoring X-Forwarded-For only for the given trusted proxy CIDRs/addresses.
func KeyByIP(trustedProxies ...string) func(r *http.Request) string {
	proxies := append([]string{}, trustedProxies...)
	return func(r *http.Request) string {
		return IPFromRequest(r, proxies)
	}
}
