package memory

import (
	"context"
	"encoding/binary"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/AshwinSathian/weir/store"
)

// taggedEntry is liveEntry with the tags the engine gives a response:
// [global, URI, groups...] (04 §3).
func taggedEntry(body string, uri byte) *store.Entry {
	e := liveEntry(body)
	e.Tags = []store.Tag{store.TagGlobal(), {uri}}
	return e
}

// invalidated reports whether the store holds an epoch that covers an entry
// requested at since, and its mode.
func invalidated(t *testing.T, s *Store, since time.Time) (store.EpochMode, bool) {
	t.Helper()
	ep, ok, err := s.NewestEpoch(context.Background(), []store.Tag{store.TagGlobal()}, since)
	if err != nil {
		t.Fatal(err)
	}
	return ep.Mode, ok
}

// FR-SNP-5 (v1.2), CMP-F8: with SnapshotKeepFreshness on at the write and at
// the load, loaded entries keep the freshness they had: no epoch is written
// at load time, so a restart does not send every stored URL to the origin.
func TestSnapshotKeepsFreshness(t *testing.T) {
	path := filepath.Join(t.TempDir(), "weir.snap")
	ctx := context.Background()
	s, err := New(Config{SnapshotPath: path, SnapshotKeepFreshness: true})
	if err != nil {
		t.Fatal(err)
	}
	e := taggedEntry("kept", 1)
	if err := s.Set(ctx, numKey(1), e); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s2 := loadFrom(t, path, Config{SnapshotKeepFreshness: true})
	if got, err := s2.Get(ctx, numKey(1)); err != nil || string(got.Body) != "kept" {
		t.Fatalf("Get after restart: %v, %v", got, err)
	}
	if mode, ok := invalidated(t, s2, e.RequestTime); ok {
		t.Fatalf("an epoch of mode %v covers the loaded entry; want none", mode)
	}
}

// FR-SNP-5, T-33, FR-STL-5: soft and invalidating epochs are not in the file
// and cannot be rebuilt, so an entry one of them covers at shutdown is left
// out of a kept snapshot. It comes back as a miss, never as a fresh hit.
func TestSnapshotKeepLeavesOutInvalidatedEntries(t *testing.T) {
	for name, mode := range map[string]store.EpochMode{"soft purge": store.EpochSoft, "unsafe method": store.EpochInvalid, "hard purge": store.EpochHard} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "weir.snap")
			ctx := context.Background()
			s, err := New(Config{SnapshotPath: path, SnapshotKeepFreshness: true})
			if err != nil {
				t.Fatal(err)
			}
			for i, e := range []*store.Entry{taggedEntry("clean", 1), taggedEntry("purged", 2)} {
				if err := s.Set(ctx, numKey(uint64(i+1)), e); err != nil { //nolint:gosec // 1 or 2
					t.Fatal(err)
				}
			}
			if err := s.SetEpoch(ctx, store.Tag{2}, store.Epoch{At: time.Now(), Mode: mode}); err != nil {
				t.Fatal(err)
			}
			if err := s.Close(); err != nil {
				t.Fatal(err)
			}
			s2 := loadFrom(t, path, Config{SnapshotKeepFreshness: true})
			if _, err := s2.Get(ctx, numKey(1)); err != nil {
				t.Fatalf("the clean entry: %v", err)
			}
			if _, err := s2.Get(ctx, numKey(2)); !errors.Is(err, store.ErrNotFound) {
				t.Fatalf("the entry an epoch covered at shutdown: %v, want ErrNotFound", err)
			}
		})
	}
}

// FR-SNP-5, T-33: freshness is kept only when everything vouches for it.
// Otherwise the load is as before: every entry invalid as of the restart.
func TestSnapshotKeepFallsBack(t *testing.T) {
	ctx := context.Background()
	kept := func(t *testing.T, path string) *store.Entry { // a snapshot written with the option on
		t.Helper()
		s, err := New(Config{SnapshotPath: path, SnapshotKeepFreshness: true})
		if err != nil {
			t.Fatal(err)
		}
		e := taggedEntry("x", 1)
		if err := s.Set(ctx, numKey(1), e); err != nil {
			t.Fatal(err)
		}
		if err := s.CloseContext(ctx); err != nil {
			t.Fatal(err)
		}
		return e
	}
	t.Run("the loader does not ask for it", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "weir.snap")
		e := kept(t, path)
		if mode, ok := invalidated(t, loadFrom(t, path, Config{}), e.RequestTime); !ok || mode != store.EpochInvalid {
			t.Fatalf("epoch %v, %v; want invalid", mode, ok)
		}
	})
	t.Run("the writer did not vouch for the file", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "weir.snap")
		f := newSnapFile() // as v1.1, or the option off, writes it: no marker
		e := liveEntry("old")
		f.entry(t, numKey(1), e)
		f.write(t, path)
		if mode, ok := invalidated(t, loadFrom(t, path, Config{SnapshotKeepFreshness: true}), e.RequestTime); !ok || mode != store.EpochInvalid {
			t.Fatalf("epoch %v, %v; want invalid", mode, ok)
		}
	})
	t.Run("the clock went backwards", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "weir.snap")
		f := newSnapFile()
		binary.BigEndian.PutUint64(f.b[len(snapMagic)+1:], uint64(time.Now().Add(time.Hour).UnixNano())) //nolint:gosec // bit pattern
		e := liveEntry("future")
		f.entry(t, numKey(1), e)
		f.keep()
		f.write(t, path)
		if mode, ok := invalidated(t, loadFrom(t, path, Config{SnapshotKeepFreshness: true}), e.RequestTime); !ok || mode != store.EpochInvalid {
			t.Fatalf("epoch %v, %v; want invalid: the file says it was written an hour from now", mode, ok)
		}
	})
	t.Run("a record was lost", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "weir.snap")
		f := newSnapFile()
		e := liveEntry("x")
		f.entry(t, numKey(1), e)
		f.keep()
		f.b = append(f.b, frame(0x7e, []byte("?"))...) // a kind this version does not know
		f.count++
		f.write(t, path)
		if mode, ok := invalidated(t, loadFrom(t, path, Config{SnapshotKeepFreshness: true}), e.RequestTime); !ok || mode != store.EpochHard {
			t.Fatalf("epoch %v, %v; want hard, as for any lost record", mode, ok)
		}
	})
}

// FR-SNP-5, T-33: every tag of an entry is read in every plane when a kept
// snapshot decides what to hold, also tags past the second, which the engine
// reads as shared. A store used directly may invalidate through any of them.
func TestSnapshotKeepReadsEveryTag(t *testing.T) {
	path := filepath.Join(t.TempDir(), "weir.snap")
	ctx := context.Background()
	s, err := New(Config{SnapshotPath: path, SnapshotKeepFreshness: true})
	if err != nil {
		t.Fatal(err)
	}
	e := taggedEntry("grouped", 1)
	e.Tags = append(e.Tags, store.Tag{9})
	if err := s.Set(ctx, numKey(1), e); err != nil {
		t.Fatal(err)
	}
	if err := s.SetEpoch(ctx, store.Tag{9}, store.Epoch{At: time.Now(), Mode: store.EpochInvalid}); err != nil {
		t.Fatal(err)
	}
	if err := s.CloseContext(ctx); err != nil {
		t.Fatal(err)
	}
	if err := s.SetEpoch(ctx, store.Tag{1}, store.Epoch{At: time.Now(), Mode: store.EpochSoft}); !errors.Is(err, store.ErrUnavailable) {
		t.Fatalf("SetEpoch after Close: %v, want ErrUnavailable: an epoch the snapshot cannot hold must not be acknowledged", err)
	}
	if _, err := loadFrom(t, path, Config{SnapshotKeepFreshness: true}).Get(ctx, numKey(1)); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("entry invalidated through its third tag: %v, want ErrNotFound", err)
	}
}

func (f *snapFile) keep() {
	f.b = append(f.b, frame(snapKindKept, []byte{1})...)
	f.count++
}
