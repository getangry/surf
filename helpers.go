package surf

import (
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
)

// Query returns the value of the specified query parameter.
// Returns empty string if the parameter doesn't exist.
func Query(r *http.Request, key string) string {
	return r.URL.Query().Get(key)
}

// QueryDefault returns the value of the specified query parameter,
// or the default value if the parameter doesn't exist or is empty.
func QueryDefault(r *http.Request, key, defaultVal string) string {
	val := r.URL.Query().Get(key)
	if val == "" {
		return defaultVal
	}
	return val
}

// QueryInt returns the value of the specified query parameter as an integer.
// Returns the default value if the parameter doesn't exist or cannot be parsed.
func QueryInt(r *http.Request, key string, defaultVal int) int {
	val := r.URL.Query().Get(key)
	if val == "" {
		return defaultVal
	}
	i, err := strconv.Atoi(val)
	if err != nil {
		return defaultVal
	}
	return i
}

// QueryInt64 returns the value of the specified query parameter as an int64.
// Returns the default value if the parameter doesn't exist or cannot be parsed.
func QueryInt64(r *http.Request, key string, defaultVal int64) int64 {
	val := r.URL.Query().Get(key)
	if val == "" {
		return defaultVal
	}
	i, err := strconv.ParseInt(val, 10, 64)
	if err != nil {
		return defaultVal
	}
	return i
}

// QueryFloat returns the value of the specified query parameter as a float64.
// Returns the default value if the parameter doesn't exist or cannot be parsed.
func QueryFloat(r *http.Request, key string, defaultVal float64) float64 {
	val := r.URL.Query().Get(key)
	if val == "" {
		return defaultVal
	}
	f, err := strconv.ParseFloat(val, 64)
	if err != nil {
		return defaultVal
	}
	return f
}

// QueryBool returns the value of the specified query parameter as a boolean.
// Accepts "true", "1", "yes", "on" as true values (case-insensitive).
// Returns the default value if the parameter doesn't exist.
func QueryBool(r *http.Request, key string, defaultVal bool) bool {
	val := r.URL.Query().Get(key)
	if val == "" {
		return defaultVal
	}
	val = strings.ToLower(val)
	return val == "true" || val == "1" || val == "yes" || val == "on"
}

// QuerySlice returns all values of the specified query parameter as a slice.
// Returns nil if the parameter doesn't exist.
func QuerySlice(r *http.Request, key string) []string {
	values := r.URL.Query()[key]
	if len(values) == 0 {
		return nil
	}
	return values
}

// Redirect sends an HTTP redirect response to the client.
// The code should be a redirect status code (3xx).
func Redirect(w http.ResponseWriter, r *http.Request, url string, code int) {
	http.Redirect(w, r, url, code)
}

// RedirectPermanent sends a 301 Moved Permanently redirect.
func RedirectPermanent(w http.ResponseWriter, r *http.Request, url string) {
	http.Redirect(w, r, url, http.StatusMovedPermanently)
}

// RedirectTemporary sends a 302 Found redirect.
func RedirectTemporary(w http.ResponseWriter, r *http.Request, url string) {
	http.Redirect(w, r, url, http.StatusFound)
}

// RedirectSeeOther sends a 303 See Other redirect.
// This is the appropriate redirect after a POST request.
func RedirectSeeOther(w http.ResponseWriter, r *http.Request, url string) {
	http.Redirect(w, r, url, http.StatusSeeOther)
}

// Static registers a handler for serving static files from a directory.
// The prefix is the URL path prefix (e.g., "/static").
// The dir is the filesystem directory to serve files from.
//
// Path traversal is prevented at the kernel level by os.Root: any "..",
// absolute paths, or symlinks escaping dir return an error from the
// filesystem before any bytes are served. Panics if dir cannot be opened.
func (app *App) Static(prefix, dir string) {
	if !strings.HasPrefix(prefix, "/") {
		prefix = "/" + prefix
	}
	prefix = strings.TrimSuffix(prefix, "/")

	root, err := os.OpenRoot(dir)
	if err != nil {
		panic(fmt.Sprintf("surf: Static(%q, %q): %v", prefix, dir, err))
	}

	handler := http.StripPrefix(prefix, http.FileServerFS(root.FS()))

	app.Get(prefix+"/*", func(w http.ResponseWriter, r *http.Request) error {
		handler.ServeHTTP(w, r)
		return nil
	})

	// Redirect bare prefix to prefix/ so relative links resolve correctly.
	app.Get(prefix, func(w http.ResponseWriter, r *http.Request) error {
		http.Redirect(w, r, prefix+"/", http.StatusMovedPermanently)
		return nil
	})
}

// StaticFile registers a handler for serving a single static file.
// The path is the URL path (e.g., "/favicon.ico").
// The file is the filesystem path to the file.
func (app *App) StaticFile(path, file string) {
	app.Get(path, func(w http.ResponseWriter, r *http.Request) error {
		http.ServeFile(w, r, file)
		return nil
	})
}

// NotFoundHandler is the type for custom 404 handlers
type NotFoundHandler func(w http.ResponseWriter, r *http.Request)

// MethodNotAllowedHandler is the type for custom 405 handlers
type MethodNotAllowedHandler func(w http.ResponseWriter, r *http.Request, allowedMethods []string)
