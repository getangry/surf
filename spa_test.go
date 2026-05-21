package surf

import (
	"net/http/httptest"
	"testing"
	"testing/fstest"
)

func spaFS() fstest.MapFS {
	return fstest.MapFS{
		"index.html":    {Data: []byte("<html>spa</html>")},
		"assets/app.js": {Data: []byte("console.log(1)")},
		"robots.txt":    {Data: []byte("User-agent: *")},
	}
}

func TestSPAServesIndexAtRoot(t *testing.T) {
	app := NewApp()
	app.SPA("/", spaFS())

	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
	if rec.Code != 200 || rec.Body.String() != "<html>spa</html>" {
		t.Fatalf("code=%d body=%q", rec.Code, rec.Body.String())
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "no-cache" {
		t.Errorf("index Cache-Control = %q, want no-cache", cc)
	}
}

func TestSPAServesAssetWithImmutableCache(t *testing.T) {
	app := NewApp()
	app.SPA("/", spaFS())

	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, httptest.NewRequest("GET", "/assets/app.js", nil))
	if rec.Code != 200 || rec.Body.String() != "console.log(1)" {
		t.Fatalf("code=%d body=%q", rec.Code, rec.Body.String())
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "public, max-age=31536000, immutable" {
		t.Errorf("asset Cache-Control = %q", cc)
	}
}

func TestSPAFallbackToIndex(t *testing.T) {
	app := NewApp()
	app.SPA("/", spaFS())

	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, httptest.NewRequest("GET", "/some/client/route", nil))
	if rec.Code != 200 || rec.Body.String() != "<html>spa</html>" {
		t.Errorf("fallback failed: code=%d body=%q", rec.Code, rec.Body.String())
	}
}

func TestSPANonImmutableAsset(t *testing.T) {
	app := NewApp()
	app.SPA("/", spaFS())

	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, httptest.NewRequest("GET", "/robots.txt", nil))
	if rec.Code != 200 || rec.Body.String() != "User-agent: *" {
		t.Fatalf("code=%d body=%q", rec.Code, rec.Body.String())
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "no-cache" {
		t.Errorf("robots.txt Cache-Control = %q, want no-cache", cc)
	}
}

func TestSPAExcludePrefixes(t *testing.T) {
	app := NewApp()
	app.SPAWithConfig(SPAConfig{
		Prefix:          "/",
		FS:              spaFS(),
		ExcludePrefixes: []string{"api"},
	})

	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, httptest.NewRequest("GET", "/api/unknown", nil))
	if rec.Code != 404 {
		t.Errorf("excluded prefix code = %d, want 404", rec.Code)
	}
}

func TestSPATraversalBlocked(t *testing.T) {
	app := NewApp()
	app.SPA("/", spaFS())

	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, httptest.NewRequest("GET", "/../../etc/passwd", nil))
	// path.Clean neutralizes traversal; the request falls back to the index.
	if rec.Code != 200 || rec.Body.String() != "<html>spa</html>" {
		t.Errorf("traversal not contained: code=%d body=%q", rec.Code, rec.Body.String())
	}
}
