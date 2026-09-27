package weir

import (
	"context"
	"errors"
	"net/http"
	"time"
)

// Sentinel errors. Compare with errors.Is; the engine returns them wrapped.
// StatusCode maps each to the status an adapter should send (01 §4).
var (
	// ErrInvalidConfig is returned by New for any invalid Config field.
	ErrInvalidConfig = errors.New("weir: invalid config")
	// ErrInvalidRequest means the request failed input validation (FR-VAL).
	// Serve returns it wrapped in *RequestError.
	ErrInvalidRequest = errors.New("weir: invalid request")
	// ErrShed means no origin slot was free within the queue budget and
	// nothing stale could be served. Serve returns it wrapped in *RetryError.
	ErrShed = errors.New("weir: origin capacity exhausted")
	// ErrCircuitOpen means the breaker is open and nothing stale could be
	// served. Serve returns it wrapped in *RetryError.
	ErrCircuitOpen = errors.New("weir: origin circuit open")
	// ErrOriginTimeout means the origin did not answer within Timeouts.Origin.
	ErrOriginTimeout = errors.New("weir: origin timeout")
	// ErrMustRevalidate means a must-revalidate entry could not be validated.
	ErrMustRevalidate = errors.New("weir: must-revalidate response could not be validated")
	// ErrOnlyIfCached means an only-if-cached request found no usable stored
	// response.
	ErrOnlyIfCached = errors.New("weir: only-if-cached and no stored response")
	// ErrOrigin means the origin returned a transport error. Serve returns it
	// wrapped in *OriginError.
	ErrOrigin = errors.New("weir: origin error")
	// ErrClosed means the engine is closed.
	ErrClosed = errors.New("weir: engine closed")
	// ErrUpgradeNotSupported means a CONNECT or protocol upgrade request
	// reached Serve (FR-UPG-1). Adapters route those around the engine.
	ErrUpgradeNotSupported = errors.New("weir: connect and protocol upgrades not supported")
	// ErrEagerUnsupported means an eager purge wrote its epoch but the store
	// cannot scrub, so matching records were not deleted (FR-PRG-8).
	ErrEagerUnsupported = errors.New("weir: store cannot scrub; epoch written, delete skipped")
)

// RequestError reports why a request failed validation. It matches
// ErrInvalidRequest under errors.Is.
type RequestError struct {
	Reason string
}

// Error implements error.
func (e *RequestError) Error() string { return "weir: invalid request: " + e.Reason }

// Unwrap returns ErrInvalidRequest.
func (e *RequestError) Unwrap() error { return ErrInvalidRequest }

// OriginError wraps a transport error from Origin.Fetch. It matches ErrOrigin
// under errors.Is and unwraps to Err.
type OriginError struct {
	Err error
}

// Error implements error.
func (e *OriginError) Error() string {
	if e.Err == nil {
		return ErrOrigin.Error()
	}
	return "weir: origin error: " + e.Err.Error()
}

// Unwrap returns the origin's error.
func (e *OriginError) Unwrap() error { return e.Err }

// Is reports whether target is ErrOrigin.
func (e *OriginError) Is(target error) bool { return target == ErrOrigin }

// RetryError carries a Retry-After hint for shed and circuit-open errors.
type RetryError struct {
	Err   error         // ErrShed or ErrCircuitOpen
	After time.Duration // rounded up to whole seconds by the engine
}

// Error implements error.
func (e *RetryError) Error() string {
	if e.Err == nil {
		return "weir: retry later"
	}
	return e.Err.Error()
}

// Unwrap returns Err.
func (e *RetryError) Unwrap() error { return e.Err }

// RetryAfter returns the Retry-After hint from a *RetryError anywhere in
// err's chain.
func RetryAfter(err error) (time.Duration, bool) {
	re, ok := errors.AsType[*RetryError](err)
	if !ok {
		return 0, false
	}
	return re.After, true
}

// statusClientClosed is nginx's 499. It is a log hint only: the client is
// gone and adapters should not write a response.
const statusClientClosed = 499

// StatusCode maps err to the status an adapter should send (01 §4). Unknown
// errors map to 502.
func StatusCode(err error) int {
	switch {
	case errors.Is(err, ErrInvalidRequest):
		return http.StatusBadRequest
	case errors.Is(err, ErrUpgradeNotSupported):
		return http.StatusNotImplemented
	case errors.Is(err, ErrShed), errors.Is(err, ErrCircuitOpen), errors.Is(err, ErrClosed):
		return http.StatusServiceUnavailable
	case errors.Is(err, ErrOriginTimeout), errors.Is(err, ErrMustRevalidate), errors.Is(err, ErrOnlyIfCached):
		return http.StatusGatewayTimeout
	// Context errors before ErrOrigin: a fetch on the request context that
	// fails because the client left comes back as *OriginError wrapping
	// context.Canceled, and that is not the origin's fault.
	case errors.Is(err, context.DeadlineExceeded):
		return http.StatusGatewayTimeout
	case errors.Is(err, context.Canceled):
		return statusClientClosed
	case errors.Is(err, ErrOrigin):
		return http.StatusBadGateway
	default:
		return http.StatusBadGateway
	}
}
