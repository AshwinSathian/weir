package weir

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"strings"
	"testing"
	"time"
)

// 01 §4 error table; FR-VAL-1, FR-LIM-5, FR-CB-5, FR-STL-4, FR-SRV-6, FR-UPG-1, FR-LCY-1.
func TestStatusCode(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want int
	}{
		{"invalid request", &RequestError{Reason: "bad path"}, 400},
		{"bare invalid request sentinel", ErrInvalidRequest, 400},
		{"shed", &RetryError{Err: ErrShed, After: time.Second}, 503},
		{"circuit open", &RetryError{Err: ErrCircuitOpen, After: time.Second}, 503},
		{"origin timeout", ErrOriginTimeout, 504},
		{"must-revalidate", ErrMustRevalidate, 504},
		{"only-if-cached", ErrOnlyIfCached, 504},
		{"origin transport error", &OriginError{Err: io.ErrUnexpectedEOF}, 502},
		// The engine returns the caller's ctx.Err() unwrapped (timeoutOrOrigin).
		// Context errors inside *OriginError are the origin's own; 499 would
		// tell the adapter not to write to a client that is still there.
		{"origin error wrapping the origin's own cancellation is 502", &OriginError{Err: context.Canceled}, 502},
		{"origin error wrapping an http.Client timeout is 502", &OriginError{Err: fmt.Errorf("get: %w", context.DeadlineExceeded)}, 502},
		// A nested engine used as origin must not leak its classification.
		{"origin error wrapping an invalid request is 502", &OriginError{Err: &RequestError{Reason: "path"}}, 502},
		{"origin error wrapping a shed is 502", &OriginError{Err: &RetryError{Err: ErrShed, After: time.Second}}, 502},
		{"origin error wrapping an upgrade error is 502", &OriginError{Err: ErrUpgradeNotSupported}, 502},
		{"outer must-revalidate wins over its origin cause", fmt.Errorf("%w: %w", ErrMustRevalidate, &OriginError{Err: io.EOF}), 504},
		{"joined errors classify by the first recognized", errors.Join(errors.New("x"), ErrClosed, ErrInvalidRequest), 503},
		{"closed", ErrClosed, 503},
		{"upgrade", ErrUpgradeNotSupported, 501},
		{"caller deadline", context.DeadlineExceeded, 504},
		{"caller cancel", context.Canceled, 499},
		{"wrapped with fmt", fmt.Errorf("serve: %w", ErrClosed), 503},
		{"invalid config has no status and falls back", ErrInvalidConfig, 502},
		{"unknown error", errors.New("boom"), 502},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := StatusCode(tt.err); got != tt.want {
				t.Errorf("StatusCode(%v) = %d, want %d", tt.err, got, tt.want)
			}
		})
	}
}

// FR-LIM-5, FR-CB-5: Retry-After comes from *RetryError only.
func TestRetryAfter(t *testing.T) {
	tests := []struct {
		name   string
		err    error
		want   time.Duration
		wantOK bool
	}{
		{"shed carries its wait", &RetryError{Err: ErrShed, After: 2 * time.Second}, 2 * time.Second, true},
		{"found through fmt wrapping", fmt.Errorf("x: %w", &RetryError{Err: ErrCircuitOpen, After: 5 * time.Second}), 5 * time.Second, true},
		{"sub-second wait rounds up", &RetryError{Err: ErrShed, After: 1500 * time.Millisecond}, 2 * time.Second, true},
		{"negative wait clamps to zero", &RetryError{Err: ErrCircuitOpen, After: -time.Second}, 0, true},
		{"huge wait does not overflow", &RetryError{Err: ErrShed, After: math.MaxInt64}, math.MaxInt64 / time.Second * time.Second, true},
		{"retry wrapper around a non-retry error has no hint", &RetryError{Err: ErrClosed, After: 5 * time.Second}, 0, false},
		{"hint from another join branch is not attached", errors.Join(&RetryError{Err: io.EOF, After: time.Minute}, ErrClosed), 0, false},
		{"typed nil retry error does not panic", fmt.Errorf("x: %w", (*RetryError)(nil)), 0, false},
		{"hint inside an origin error is not ours", &OriginError{Err: &RetryError{Err: ErrShed, After: time.Second}}, 0, false},
		{"bare sentinel has no hint", ErrShed, 0, false},
		{"nil has no hint", nil, 0, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := RetryAfter(tt.err)
			if got != tt.want || ok != tt.wantOK {
				t.Errorf("RetryAfter = (%v, %v), want (%v, %v)", got, ok, tt.want, tt.wantOK)
			}
		})
	}
}

// FR-VAL-1, FR-LIM-5, FR-CB-5 (04 §1.3): every wrapper type satisfies errors.Is
// with its sentinel, and zero values do not panic (NFR-2).
func TestErrorsIs(t *testing.T) {
	cause := io.ErrUnexpectedEOF
	tests := []struct {
		name   string
		err    error
		target error
	}{
		{"request error is invalid request", &RequestError{Reason: "r"}, ErrInvalidRequest},
		{"origin error is origin", &OriginError{Err: cause}, ErrOrigin},
		{"origin error unwraps to its cause", &OriginError{Err: cause}, cause},
		{"retry error is shed", &RetryError{Err: ErrShed}, ErrShed},
		{"retry error is circuit open", &RetryError{Err: ErrCircuitOpen}, ErrCircuitOpen},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if !errors.Is(tt.err, tt.target) {
				t.Errorf("errors.Is(%v, %v) = false", tt.err, tt.target)
			}
		})
	}
	for _, zero := range []error{&OriginError{}, &RetryError{}, &RequestError{}} {
		if msg := zero.Error(); msg == "" || strings.HasSuffix(msg, " ") {
			t.Errorf("%T zero value has an empty message", zero)
		}
	}
	if errors.Is(&OriginError{Err: cause}, ErrInvalidRequest) {
		t.Error("origin error must not match ErrInvalidRequest")
	}
	if _, ok := errors.AsType[*OriginError](fmt.Errorf("x: %w", &OriginError{Err: cause})); !ok {
		t.Error("errors.AsType did not find *OriginError")
	}
}
