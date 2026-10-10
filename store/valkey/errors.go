package valkey

import (
	"errors"
	"fmt"

	"github.com/AshwinSathian/weir/store"
)

// mapError turns any error from the Valkey client into one that wraps
// store.ErrUnavailable (S-3, 05 §7). Context errors, a closing client,
// network failures, io.EOF, a missing cluster slot and server errors (OOM,
// READONLY, CLUSTERDOWN, LOADING, BUSY) all mean "could not answer". The
// cause stays in the chain for errors.Is and errors.As.
//
// valkey.Nil is not an outage; Get checks for it before calling mapError.
func mapError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, store.ErrUnavailable) {
		return err
	}
	return fmt.Errorf("store: valkey: %w: %w", store.ErrUnavailable, err)
}
