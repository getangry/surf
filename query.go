package surf

import (
	"net/http"
	"strings"
)

// defaultAcceptQuery is the media-type list surf advertises in the Accept-Query
// response header when a path exposes a QUERY route. It matches Bind's
// JSON-only decoding; override the app-wide value with WithAcceptQuery.
const defaultAcceptQuery = "application/json"

// WithAcceptQuery sets the media types advertised in the Accept-Query response
// header (RFC 10008 §3) on the automatic OPTIONS/405 responses for any path
// that has a QUERY route. The default is "application/json". Pass the formats
// your QUERY handlers actually accept, or call it with no arguments to suppress
// the header entirely.
//
//	app := surf.NewApp(surf.WithAcceptQuery("application/json", "application/sql"))
func WithAcceptQuery(mediaTypes ...string) Option {
	joined := strings.Join(mediaTypes, ", ")
	return func(app *App) {
		app.acceptQuery = joined
	}
}

// SetAcceptQuery writes the Accept-Query response header (RFC 10008 §3) to
// signal that the resource supports the QUERY method and which query-format
// media types it accepts. Use it to attach the header to a response the
// framework does not generate for you — most usefully alongside a 415 when a
// QUERY body's Content-Type is unsupported, so the client learns which formats
// to retry with.
//
// It takes explicit media types rather than reading the app-wide
// WithAcceptQuery value, because it has no reference to the App; pass the same
// formats you configured there. With no arguments it advertises
// "application/json", matching surf.Bind. The automatic OPTIONS/405 responses
// already emit the configured value, so you rarely need this for discovery.
//
//	app.Query("/users", func(w http.ResponseWriter, r *http.Request) error {
//	    var q UserQuery
//	    if err := surf.Bind(r, &q); err != nil {
//	        surf.SetAcceptQuery(w, "application/json") // help the client on 415
//	        return err
//	    }
//	    return surf.JSONData(w, findUsers(q))
//	})
func SetAcceptQuery(w http.ResponseWriter, mediaTypes ...string) {
	value := defaultAcceptQuery
	if len(mediaTypes) > 0 {
		value = strings.Join(mediaTypes, ", ")
	}
	setKnownHeader(w.Header(), headerAcceptQuery, value)
}
