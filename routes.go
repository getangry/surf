package surf

import "reflect"

// RouteStyle distinguishes the handler signature a route was registered with.
type RouteStyle int

const (
	// StyleStandard — registered with App.Get / Post / Put / etc.,
	// taking a HandlerFunc with the (w, r) error signature.
	StyleStandard RouteStyle = iota

	// StyleContext — registered with App.Handle, taking a CtxHandler with
	// the (c *Context) error signature.
	StyleContext
)

// String returns "standard" or "context".
func (s RouteStyle) String() string {
	switch s {
	case StyleStandard:
		return "standard"
	case StyleContext:
		return "context"
	default:
		return "unknown"
	}
}

// RouteInfo describes a single registered route. It is captured at
// registration time and never updated, so reading it is allocation-free and
// safe to call concurrently with serving traffic.
//
// ReqType and RespType are populated only for typed handlers (a future
// HandleJSON[Req, Resp] API). For routes registered with Get/Post/Handle
// they are nil.
type RouteInfo struct {
	Method   string
	Pattern  string
	Params   []string
	Style    RouteStyle
	ReqType  reflect.Type
	RespType reflect.Type
}

// Routes returns a copy of the registered route metadata in registration
// order. Useful for building introspection endpoints, generated OpenAPI
// specs, or debugging output. The result is a snapshot — register new
// routes and call Routes() again to see them.
func (app *App) Routes() []RouteInfo {
	src := app.router.routeInfo
	out := make([]RouteInfo, len(src))
	copy(out, src)
	return out
}
