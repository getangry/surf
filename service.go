package surf

import (
	"fmt"
	"net/http"
	"reflect"
)

// serviceTypeKey keys a service in the container by its concrete (or
// interface) type, so registration and retrieval cannot disagree on a string.
type serviceTypeKey struct{ t reflect.Type }

// typeKey returns the container key for type T.
func typeKey[T any]() serviceTypeKey {
	return serviceTypeKey{t: reflect.TypeOf((*T)(nil)).Elem()}
}

// Provide registers a service in the application's container keyed by its type
// T. It is the type-safe counterpart to App.Set: retrieval with Service[T] or
// MustService[T] cannot silently return a zero value because of a key typo or
// a mismatched type.
//
//	surf.Provide[*sql.DB](app, db)
//	surf.Provide[Authenticator](app, oktaAuth) // register under an interface
func Provide[T any](app *App, service T) {
	app.Set(typeKey[T](), service)
}

// Service retrieves a service registered with Provide[T]. The boolean reports
// whether a service of that exact type was found.
func Service[T any](r *http.Request) (T, bool) {
	var zero T
	app, ok := r.Context().Value(appKey{}).(*App)
	if !ok {
		return zero, false
	}
	v := app.GetService(typeKey[T]())
	if v == nil {
		return zero, false
	}
	typed, ok := v.(T)
	if !ok {
		return zero, false
	}
	return typed, true
}

// MustService retrieves a service registered with Provide[T], panicking if it
// is missing. Use it for dependencies the application cannot run without; the
// panic is recovered by the Recovery middleware and surfaces as a clear error
// instead of a downstream nil dereference.
func MustService[T any](r *http.Request) T {
	v, ok := Service[T](r)
	if !ok {
		panic(fmt.Sprintf("surf: no service registered for type %s", typeKey[T]().t))
	}
	return v
}
