package weir

import (
	"context"
	"errors"
	"fmt"
	"io"
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
		{"origin error wrapping a client cancellation is 499", &OriginError{Err: context.Canceled}, 499},
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
		if zero.Error() == "" {
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
