package valkey

import (
	"errors"
	"fmt"

	"github.com/AshwinSathian/weir/store"
	"github.com/valkey-io/valkey-go"
)

// mapError turns any error from the Valkey client into one that wraps
// store.ErrUnavailable (S-3, 05 §7). Context errors, a closing client,
// network failures, io.EOF, a missing cluster slot and server errors (OOM,
// READONLY, CLUSTERDOWN, LOADING, BUSY) all mean "could not answer". The
// cause stays in the chain for errors.Is and errors.As.
//
// valkey.Nil (also wrapped) and store.ErrNotFound are not outages and are
// returned unchanged; Get turns Nil into store.ErrNotFound.
func mapError(err error) error {
	if err == nil || valkey.IsValkeyNil(err) || errors.Is(err, valkey.Nil) || errors.Is(err, store.ErrNotFound) {
		return err
	}
	if errors.Is(err, store.ErrUnavailable) {
		return err
	}
	return fmt.Errorf("store: valkey: %w: %w", store.ErrUnavailable, err)
}
