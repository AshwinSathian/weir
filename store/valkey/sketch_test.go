package valkey

import (
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/AshwinSathian/weir/store"
)

var _ store.SharedTagEpochs = (*Store)(nil)

func softAt(at time.Time) store.Epoch { return store.Epoch{At: at, Mode: store.EpochSoft} }

// 05 §7 (Sketch positions), T-29: the two cells of a tag are the first two
// big-endian u32 words of SHA-256(seed || tag) modulo the plane size, so the
// secret seed decides them and an attacker who can compute a tag cannot aim
// at its cells.
func TestSketchPositions(t *testing.T) {
	seed := [seedLen]byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16}
	tg := tag(1)
	sum := sha256.Sum256(append(slices.Clone(seed[:]), tg[:]...))
	wantA := binary.BigEndian.Uint32(sum[0:4]) % sketchSlots
	wantB := binary.BigEndian.Uint32(sum[4:8]) % sketchSlots
	if a, b := positions(&seed, tg); a != wantA || b != wantB {
		t.Fatalf("positions = %d, %d; want %d, %d", a, b, wantA, wantB)
	}
	other := seed
	other[0]++
	if a, b := positions(&other, tg); a == wantA && b == wantB {
		t.Error("a different seed gave the same positions")
	}
	if a, b := positions(&seed, tag(2)); a == wantA && b == wantB {
		t.Error("a different tag gave the same positions")
	}
	for i := range 1000 {
		if a, b := positions(&seed, tag(i)); a >= sketchSlots || b >= sketchSlots {
			t.Fatalf("positions %d, %d are outside the plane", a, b)
		}
	}
}

// 05 §7: the seed is drawn from crypto/rand once, stored with HSETNX, cached,
// and reused. Soft and invalid writes carry the positions and the first 8
// bytes of the seed; a hard or global write needs neither.
func TestSketchWriteArguments(t *testing.T) {
	s, cl := connected(t, nil)
	tg := tag(1)
	for range 2 {
		if err := s.SetEpoch(t.Context(), tg, store.Epoch{At: time.Unix(1_800_000_000, 0), Mode: store.EpochInvalid}); err != nil {
			t.Fatal(err)
		}
	}
	if cl.ep.seeds != 1 || len(cl.ep.seedVal) != seedLen {
		t.Fatalf("seed calls = %d, seed length %d; want one call and %d bytes", cl.ep.seeds, len(cl.ep.seedVal), seedLen)
	}
	var seed [seedLen]byte
	copy(seed[:], cl.ep.seedVal)
	a, b := positions(&seed, tg)
	args := cl.ep.writes[1].args
	if args[argMode] != "2" || args[argGlobal] != "0" || args[argPos1] != itoa(int64(a)) || args[argPos2] != itoa(int64(b)) || args[argSeedID] != string(seed[:seedIDLen]) {
		t.Errorf("sketch write arguments = %q", args)
	}
	if err := s.SetEpoch(t.Context(), tg, hardAt(time.Now())); err != nil {
		t.Fatal(err)
	}
	if err := s.SetEpoch(t.Context(), store.TagGlobal(), softAt(time.Now())); err != nil {
		t.Fatal(err)
	}
	if cl.ep.seeds != 1 {
		t.Errorf("hard and global writes fetched the seed (%d calls)", cl.ep.seeds)
	}
}

// 05 §7: a seed that differs on the server (after a flush) makes the script
// reply SEED_CHANGED; the store refetches and retries once. A second reply
// is an error, not a loop.
func TestSeedChangedRetriesOnce(t *testing.T) {
	t.Run("write", func(t *testing.T) {
		s, cl := connected(t, nil)
		if err := s.SetEpoch(t.Context(), tag(1), softAt(time.Now())); err != nil {
			t.Fatal(err)
		}
		old := cl.ep.writes[0].args[argSeedID]
		cl.ep.seedVal = []byte("0123456789abcdef") // the server was flushed and reseeded
		cl.ep.writeErr = []error{errSeedChanged}
		if err := s.SetEpoch(t.Context(), tag(1), softAt(time.Now())); err != nil {
			t.Fatalf("SetEpoch after a reseed = %v", err)
		}
		w, _, _ := cl.counts()
		if w != 3 || cl.ep.seeds != 2 {
			t.Fatalf("writes = %d, seed calls = %d; want 3 and 2", w, cl.ep.seeds)
		}
		if got := cl.ep.writes[2].args[argSeedID]; got == old || got != "01234567" {
			t.Errorf("retry carried seed id %q (old %q), want the new one", got, old)
		}
	})
	t.Run("write fails after a second reply", func(t *testing.T) {
		s, cl := connected(t, nil)
		cl.ep.writeErr = []error{errSeedChanged, errSeedChanged}
		err := s.SetEpoch(t.Context(), tag(1), softAt(time.Now()))
		if !errors.Is(err, store.ErrUnavailable) {
			t.Fatalf("err = %v, want ErrUnavailable", err)
		}
		if w, _, _ := cl.counts(); w != 2 {
			t.Fatalf("writes = %d, want 2", w)
		}
	})
	t.Run("lookup", func(t *testing.T) {
		s, cl := connected(t, nil)
		cl.ep.readRes = [][]int64{{seedReply}, {int64(store.EpochSoft), 1_800_000_000}}
		ep, ok, err := s.NewestEpoch(t.Context(), []store.Tag{tag(1)}, time.Unix(1, 0))
		if err != nil || !ok || ep.Mode != store.EpochSoft {
			t.Fatalf("NewestEpoch = %+v, %v, %v", ep, ok, err)
		}
		if _, r, _ := cl.counts(); r != 2 || cl.ep.seeds != 2 {
			t.Fatalf("reads = %d, seed calls = %d; want 2 and 2", r, cl.ep.seeds)
		}
	})
	t.Run("lookup fails after a second reply", func(t *testing.T) {
		s, cl := connected(t, nil)
		cl.ep.readRes = [][]int64{{seedReply}, {seedReply}}
		if _, _, err := s.NewestEpoch(t.Context(), []store.Tag{tag(1)}, time.Unix(1, 0)); !errors.Is(err, store.ErrUnavailable) {
			t.Fatalf("err = %v, want ErrUnavailable", err)
		}
	})
	t.Run("loss and reseed each get one retry", func(t *testing.T) {
		s, cl := connected(t, nil)
		cl.ep.readRes = [][]int64{{lossReply}, {seedReply}, {int64(store.EpochHard), 1_800_000_000}}
		ep, ok, err := s.NewestEpoch(t.Context(), []store.Tag{tag(1)}, time.Unix(1, 0))
		if err != nil || !ok || ep.Mode != store.EpochHard {
			t.Fatalf("NewestEpoch = %+v, %v, %v", ep, ok, err)
		}
		if w, r, _ := cl.counts(); w != 1 || r != 3 {
			t.Fatalf("writes = %d, reads = %d; want 1 and 3", w, r)
		}
	})
	t.Run("seed fetch failure is reported", func(t *testing.T) {
		s, cl := connected(t, nil)
		cl.ep.seedErr = errors.New("connection reset")
		if err := s.SetEpoch(t.Context(), tag(1), softAt(time.Now())); !errors.Is(err, store.ErrUnavailable) {
			t.Fatalf("SetEpoch = %v, want ErrUnavailable", err)
		}
		if _, _, err := s.NewestEpoch(t.Context(), []store.Tag{tag(1)}, time.Unix(1, 0)); !errors.Is(err, store.ErrUnavailable) {
			t.Fatalf("NewestEpoch = %v, want ErrUnavailable", err)
		}
	})
	t.Run("a stored seed of the wrong length is refused", func(t *testing.T) {
		s, cl := connected(t, nil)
		cl.ep.seedVal = []byte("short")
		if err := s.SetEpoch(t.Context(), tag(1), softAt(time.Now())); !errors.Is(err, store.ErrUnavailable) {
			t.Fatalf("SetEpoch = %v, want ErrUnavailable", err)
		}
	})
}

// E-12: a lookup with shared tags sends each non-global tag with its two
// positions and a flag; the global tag in shared is marked so the script
// skips its invalid field; a tag named in both lists is plain.
func TestSharedReadArguments(t *testing.T) {
	s, cl := connected(t, nil)
	g, uri, grp := store.TagGlobal(), tag(1), tag(2)
	cl.ep.readRes = [][]int64{{}, {}, {}}
	since := time.Unix(1_800_000_000, 0)
	for _, c := range []struct{ plain, shared []store.Tag }{
		{[]store.Tag{g, uri}, []store.Tag{grp}},
		{[]store.Tag{uri}, []store.Tag{g}},
		{[]store.Tag{uri, grp}, []store.Tag{grp}},
	} {
		if _, _, err := s.NewestEpochShared(t.Context(), c.plain, c.shared, since); err != nil {
			t.Fatal(err)
		}
	}
	var seed [seedLen]byte
	copy(seed[:], cl.ep.seedVal)
	chunk := func(tg store.Tag, shared string) []string {
		a, b := positions(&seed, tg)
		return []string{string(tg[:]), itoa(int64(a)), itoa(int64(b)), shared}
	}
	r := cl.ep.reads
	if r[0].args[argHasGlobal] != "1" || r[0].args[argReadSeed] != string(seed[:seedIDLen]) {
		t.Errorf("call 0 fixed arguments = %q", r[0].args[:argFirstTag])
	}
	if want := slices.Concat(chunk(uri, "0"), chunk(grp, "1")); !slices.Equal(r[0].args[argFirstTag:], want) {
		t.Errorf("call 0 tag arguments differ from %d expected chunks", len(want)/tagStride)
	}
	if r[1].args[argHasGlobal] != "2" || !slices.Equal(r[1].args[argFirstTag:], chunk(uri, "0")) {
		t.Errorf("global in shared: hasGlobal=%q, tags %d args", r[1].args[argHasGlobal], len(r[1].args)-argFirstTag)
	}
	if want := slices.Concat(chunk(uri, "0"), chunk(grp, "0")); !slices.Equal(r[2].args[argFirstTag:], want) {
		t.Error("a tag named in both lists was marked shared")
	}
}

// With only the global tag, no position is needed and no seed is fetched.
func TestGlobalOnlyLookupNeedsNoSeed(t *testing.T) {
	s, cl := connected(t, nil)
	cl.ep.readRes = [][]int64{{}}
	if _, _, err := s.NewestEpoch(t.Context(), []store.Tag{store.TagGlobal()}, time.Unix(1, 0)); err != nil {
		t.Fatal(err)
	}
	if cl.ep.seeds != 0 {
		t.Fatalf("seed calls = %d, want 0", cl.ep.seeds)
	}
}

// NewestEpochShared with no tags in either list answers "none" with no
// round trip, like NewestEpoch.
func TestNewestEpochSharedNoTags(t *testing.T) {
	s, cl := connected(t, nil)
	if _, ok, err := s.NewestEpochShared(t.Context(), nil, nil, time.Unix(1, 0)); ok || err != nil {
		t.Fatalf("NewestEpochShared = %v, %v", ok, err)
	}
	if _, r, _ := cl.counts(); r != 0 || cl.ep.seeds != 0 {
		t.Fatal("no tags still called the server")
	}
}
