package valkey

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"testing"

	"github.com/AshwinSathian/weir/store"
	"github.com/valkey-io/valkey-go"
)

// S-2, S-3, 05 §7: every error from the client other than valkey.Nil means
// "could not answer", wrapped with %w so the original stays inspectable.
func TestMapError(t *testing.T) {
	// ValkeyError has no public constructor; the zero value is a non-Nil
	// *ValkeyError, and mapError must not depend on the message text.
	var serverErr error = &valkey.ValkeyError{}
	cases := []struct {
		name string
		err  error
	}{
		{"context canceled", context.Canceled},
		{"deadline exceeded", context.DeadlineExceeded},
		{"client closing", valkey.ErrClosing},
		{"net.Error timeout", &net.DNSError{IsTimeout: true}},
		{"net.OpError", &net.OpError{Op: "dial", Err: errors.New("refused")}},
		{"io.EOF", io.EOF},
		{"io.ErrUnexpectedEOF", io.ErrUnexpectedEOF},
		{"no slot", valkey.ErrNoSlot},
		{"server error (OOM, READONLY, CLUSTERDOWN, LOADING, BUSY)", serverErr},
		{"wrapped server error", fmt.Errorf("script: %w", serverErr)},
		{"unknown error", errors.New("something else")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := mapError(tc.err)
			if !errors.Is(got, store.ErrUnavailable) {
				t.Fatalf("mapError(%v) = %v, want ErrUnavailable", tc.err, got)
			}
			if !errors.Is(got, tc.err) {
				t.Fatalf("mapError(%v) lost the cause: %v", tc.err, got)
			}
		})
	}
	t.Run("nil stays nil", func(t *testing.T) {
		if got := mapError(nil); got != nil {
			t.Fatalf("got %v", got)
		}
	})
	t.Run("already unavailable is not wrapped twice", func(t *testing.T) {
		in := fmt.Errorf("store: x: %w", store.ErrUnavailable)
		got := mapError(in)
		if !errors.Is(got, in) || got.Error() != in.Error() {
			t.Fatalf("got %v, want the same error", got)
		}
	})
	t.Run("message starts with store: valkey:", func(t *testing.T) {
		if got := mapError(io.EOF).Error(); got[:14] != "store: valkey:" {
			t.Fatalf("message %q", got)
		}
	})
}
