package surf

import (
	"bytes"
	"io"
	"io/fs"
	"net/http"
	"path"
	"strings"
)

// SPAConfig configures App.SPAWithConfig for serving a single-page application.
type SPAConfig struct {
	// Prefix is the URL path the SPA is mounted at, e.g. "/" or "/app".
	Prefix string

	// FS is the filesystem holding the built SPA. An embed.FS sub-tree
	// (fs.Sub(embedded, "dist")) is the typical value.
	FS fs.FS

	// Index is the fallback document served for the mount root and for any
	// unknown path. Defaults to "index.html".
	Index string

	// ImmutablePrefixes lists path segments whose files are fingerprinted and
	// safe to cache forever. Defaults to ["assets"]. Matching files are served
	// with "Cache-Control: public, max-age=31536000, immutable"; everything
	// else (including the index) is served with "no-cache".
	ImmutablePrefixes []string

	// ExcludePrefixes lists path segments (relative to Prefix) that must 404
	// instead of falling back to the index — typically "api" so that unknown
	// API routes do not return HTML.
	ExcludePrefixes []string
}

// SPA mounts a single-page application at prefix using default settings:
// "index.html" as the fallback document and "assets" as the immutable
// directory. See SPAWithConfig for full control.
func (app *App) SPA(prefix string, fsys fs.FS) {
	app.SPAWithConfig(SPAConfig{Prefix: prefix, FS: fsys})
}

// SPAWithConfig mounts a single-page application described by config. Unknown
// paths fall back to the index document so client-side routing works, static
// assets get long-lived caching, and directory traversal is blocked.
func (app *App) SPAWithConfig(config SPAConfig) {
	if config.FS == nil {
		panic("surf: SPAWithConfig requires a non-nil FS")
	}
	index := config.Index
	if index == "" {
		index = "index.html"
	}
	immutable := config.ImmutablePrefixes
	if immutable == nil {
		immutable = []string{"assets"}
	}
	exclude := config.ExcludePrefixes
	fsys := config.FS

	prefix := strings.TrimSuffix(config.Prefix, "/")

	serveIndex := func(w http.ResponseWriter, r *http.Request) error {
		data, err := fs.ReadFile(fsys, index)
		if err != nil {
			http.Error(w, "SPA index document not found", http.StatusInternalServerError)
			return nil
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(data)
		return nil
	}

	handler := func(w http.ResponseWriter, r *http.Request) error {
		rel := strings.TrimPrefix(Param(r, "*"), "/")
		// path.Clean collapses any "." / ".." so traversal cannot escape FS.
		clean := strings.TrimPrefix(path.Clean("/"+rel), "/")
		if clean == "" || clean == "." {
			return serveIndex(w, r)
		}

		first := clean
		if i := strings.IndexByte(clean, '/'); i != -1 {
			first = clean[:i]
		}
		for _, ex := range exclude {
			if first == ex {
				http.NotFound(w, r)
				return nil
			}
		}

		f, err := fsys.Open(clean)
		if err != nil {
			return serveIndex(w, r) // SPA fallback
		}
		defer f.Close()

		stat, err := f.Stat()
		if err != nil || stat.IsDir() {
			return serveIndex(w, r)
		}

		data, err := io.ReadAll(f)
		if err != nil {
			http.Error(w, "failed to read asset", http.StatusInternalServerError)
			return nil
		}

		cacheControl := "no-cache"
		for _, p := range immutable {
			if first == p {
				cacheControl = "public, max-age=31536000, immutable"
				break
			}
		}
		w.Header().Set("Cache-Control", cacheControl)
		http.ServeContent(w, r, clean, stat.ModTime(), bytes.NewReader(data))
		return nil
	}

	// Exact mount root (no wildcard match) plus the catch-all below it.
	rootPattern := prefix
	if rootPattern == "" {
		rootPattern = "/"
	}
	app.Get(rootPattern, serveIndex)
	app.Get(prefix+"/*", handler)
}
