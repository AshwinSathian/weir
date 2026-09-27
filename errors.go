package weir

import (
	"context"
	"errors"
	"math"
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
func (e *RequestError) Error() string {
	if e.Reason == "" {
		return ErrInvalidRequest.Error()
	}
	return "weir: invalid request: " + e.Reason
}

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
	After time.Duration // RetryAfter rounds it up to whole seconds
}

// Error implements error.
func (e *RetryError) Error() string {
	if e == nil || e.Err == nil {
		return "weir: retry later"
	}
	return e.Err.Error()
}

// Unwrap returns Err.
func (e *RetryError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

// RetryAfter returns the Retry-After hint when err classifies as shed or
// circuit open through a *RetryError, rounded up to whole seconds and never
// negative. A hint wrapped inside an *OriginError belongs to the origin and
// is not returned.
func RetryAfter(err error) (time.Duration, bool) {
	_, re := classify(err)
	if re == nil {
		return 0, false
	}
	d := max(re.After, 0)
	if r := d % time.Second; r != 0 {
		if d > math.MaxInt64-time.Second {
			return d - r, true // rounding up would overflow
		}
		d += time.Second - r
	}
	return d, true
}

// statusClientClosed is nginx's 499. It is a log hint only: the client is
// gone and adapters should not write a response.
const statusClientClosed = 499

// StatusCode maps err to the status an adapter should send (01 §4). Unknown
// errors, and nil, map to 502.
//
// The outermost recognized error in the chain decides, walking in the same
// order as errors.Is. An *OriginError maps to 502 whatever it wraps: an
// origin that returns weir errors (another engine) or its own context errors
// must not turn an origin failure into 400, 499, 503 or 504.
func StatusCode(err error) int {
	code, _ := classify(err)
	return code
}

// classify walks err's chain preorder and returns the status of the first
// recognized node. The *RetryError is returned only when it directly wraps
// the shed or circuit-open sentinel that decided the status.
func classify(err error) (int, *RetryError) {
	code := 0
	var re *RetryError
	walk(err, func(e error) bool {
		switch x := e.(type) { //nolint:errorlint // node-local check; walk does the unwrapping
		case *OriginError:
			code = http.StatusBadGateway
			return true
		case *RetryError:
			if x != nil && (is(x.Err, ErrShed) || is(x.Err, ErrCircuitOpen)) {
				code, re = http.StatusServiceUnavailable, x
				return true
			}
			return false
		}
		code = sentinelStatus(e)
		return code != 0
	})
	if code == 0 {
		return http.StatusBadGateway, nil
	}
	return code, re
}

// sentinelStatus returns the status for e itself, without unwrapping, or 0.
func sentinelStatus(e error) int {
	switch {
	case is(e, ErrInvalidRequest):
		return http.StatusBadRequest
	case is(e, ErrUpgradeNotSupported):
		return http.StatusNotImplemented
	case is(e, ErrShed), is(e, ErrCircuitOpen), is(e, ErrClosed):
		return http.StatusServiceUnavailable
	case is(e, ErrOriginTimeout), is(e, ErrMustRevalidate), is(e, ErrOnlyIfCached), is(e, context.DeadlineExceeded):
		return http.StatusGatewayTimeout
	case is(e, ErrOrigin):
		return http.StatusBadGateway
	case is(e, context.Canceled):
		return statusClientClosed
	}
	return 0
}

// is is errors.Is for one node: equality or the node's own Is method. Every
// target has a comparable type, so == cannot panic.
func is(e, target error) bool {
	if e == target { //nolint:errorlint // node-local check; walk does the unwrapping
		return true
	}
	x, ok := e.(interface{ Is(error) bool })
	return ok && x.Is(target)
}

// walk visits err's chain preorder, depth first, like errors.Is, and stops
// when visit returns true.
func walk(err error, visit func(error) bool) bool {
	for err != nil {
		if visit(err) {
			return true
		}
		switch x := err.(type) { //nolint:errorlint // this is the unwrapping
		case interface{ Unwrap() error }:
			err = x.Unwrap()
		case interface{ Unwrap() []error }:
			for _, e := range x.Unwrap() {
				if walk(e, visit) {
					return true
				}
			}
			return false
		default:
			return false
		}
	}
	return false
}
