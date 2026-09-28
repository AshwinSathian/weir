package weirhttp

import (
	"net/http"

	"github.com/AshwinSathian/weir"
	"github.com/AshwinSathian/weir/internal/keys"
)

// Middleware serves requests through e, using origin for misses. CONNECT
// and protocol upgrades go to next directly, outside the engine and its
// limiter (FR-UPG-1).
func Middleware(e *weir.Engine, origin weir.Origin) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if keys.IsUpgrade(r.Method, r.Header) {
				next.ServeHTTP(w, r)
				return
			}
			resp, err := e.Serve(r.Context(), RequestFrom(r), origin)
			if err != nil {
				WriteError(w, err)
				return
			}
			_ = WriteResponse(w, resp) // the client is gone; nothing left to tell it
		})
	}
}

// Handler is Middleware without a next handler: every request goes through
// e, and CONNECT and upgrades get 501.
func Handler(e *weir.Engine, origin weir.Origin) http.Handler {
	return Middleware(e, origin)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		WriteError(w, weir.ErrUpgradeNotSupported)
	}))
}
