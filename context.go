package surf

// contextKey is the unexported key type under which every request-scoped value
// surf stores (Set / SetMultiple / Store / Get, including request_id and
// user_id) is filed in the request's context.Context. Being unexported, no
// other package can construct one, so surf's keys cannot collide with a
// consumer's.
type contextKey string
