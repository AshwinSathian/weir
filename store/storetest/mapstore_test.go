package storetest_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/AshwinSathian/weir/store"
	"github.com/AshwinSathian/weir/store/storetest"
)

// mapStore is the simplest correct in-process store: one map, one mutex and
// exact per-tag, per-mode epochs (E-1). It ignores contexts, which S-2
// allows for stores that never block.
type mapStore struct {
	mu      sync.Mutex
	closed  bool
	entries map[store.Key]*store.Entry
	epochs  map[store.Tag][store.EpochHard + 1]time.Time
}

func newMapStore(*testing.T) store.Store {
	s := &mapStore{entries: map[store.Key]*store.Entry{}, epochs: map[store.Tag][store.EpochHard + 1]time.Time{}}
	return s
}

func (s *mapStore) Get(_ context.Context, k store.Key) (*store.Entry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, store.ErrUnavailable
	}
	e, ok := s.entries[k]
	if !ok || !time.Now().Before(e.Expires) {
		return nil, store.ErrNotFound
	}
	return e, nil
}

func (s *mapStore) Set(_ context.Context, k store.Key, e *store.Entry) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return store.ErrUnavailable
	}
	if time.Now().Before(e.Expires) {
		s.entries[k] = e
	}
	return nil
}

func (s *mapStore) Delete(_ context.Context, k store.Key) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return store.ErrUnavailable
	}
	delete(s.entries, k)
	return nil
}

func (s *mapStore) SetEpoch(_ context.Context, t store.Tag, ep store.Epoch) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return store.ErrUnavailable
	}
	if ep.Mode < store.EpochSoft || ep.Mode > store.EpochHard {
		return errors.New("store: invalid epoch mode")
	}
	latest := s.epochs[t]
	if ep.At.After(latest[ep.Mode]) {
		latest[ep.Mode] = ep.At
		s.epochs[t] = latest
	}
	return nil
}

func (s *mapStore) NewestEpoch(_ context.Context, tags []store.Tag, since time.Time) (store.Epoch, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return store.Epoch{}, false, store.ErrUnavailable
	}
	var best store.Epoch
	for _, t := range tags {
		latest, ok := s.epochs[t]
		if !ok {
			continue
		}
		for m := store.EpochSoft; m <= store.EpochHard; m++ {
			at := latest[m]
			if at.IsZero() || at.Before(since) {
				continue
			}
			if m > best.Mode || (m == best.Mode && at.After(best.At)) {
				best = store.Epoch{At: at, Mode: m}
			}
		}
	}
	return best, best.Mode != 0, nil
}

func (s *mapStore) Info() store.Info { return store.Info{Name: "map"} }

func (s *mapStore) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	return nil
}

// FR-PRG-7 (EpochSinceBoundary: an epoch at the request time applies);
// the other cases check 05 §2 and §4.2 (S-1..S-5, E-1..E-3), which have no
// requirement IDs of their own.
func TestRun(t *testing.T) {
	storetest.Run(t, newMapStore, storetest.Synctest())
}

// The epoch cases skip for a store that declares no epoch support.
func TestRunWithoutEpochs(t *testing.T) {
	storetest.Run(t, newMapStore, storetest.Synctest(), storetest.WithoutEpochs())
}
