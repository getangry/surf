package surf

import "net/http"

// Canonical header keys used by surf internally. Pre-canonicalized so a
// direct map write skips net/textproto.CanonicalMIMEHeaderKey.
//
// Adding to this list: the value MUST already be in canonical MIME form
// (Each-Word-Capitalized-And-Hyphen-Joined). Verify with
// textproto.CanonicalMIMEHeaderKey before adding.
const (
	headerContentType     = "Content-Type"
	headerContentLength   = "Content-Length"
	headerContentEncoding = "Content-Encoding"
	headerCacheControl    = "Cache-Control"
	headerAllow           = "Allow"
	headerAcceptQuery     = "Accept-Query"
	headerVary            = "Vary"
	headerXRequestID      = "X-Request-Id"
	headerRetryAfter      = "Retry-After"
)

// contentTypeTextPlain is the content-type for plain-text responses.
// render.go already defines jsonContentType for JSON responses; both are
// passed verbatim to setKnownHeader.
const contentTypeTextPlain = "text/plain; charset=utf-8"

// setKnownHeader writes value under a pre-canonicalized key via direct map
// assignment, bypassing http.Header.Set's per-call canonicalization. The
// caller is responsible for canonKey already being canonical — pass one of
// the header* constants above.
//
// Measured on Apple Silicon: ~8 ns per header write saved versus h.Set;
// ~10 ns end-to-end on the fast path for routes that write Content-Type.
func setKnownHeader(h http.Header, canonKey, value string) {
	h[canonKey] = []string{value}
}
