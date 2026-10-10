package valkey

import (
	"context"
	"encoding/hex"
	"errors"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/AshwinSathian/weir/store"
)

// fakeScan stands in for the scrub commands. SCAN pages through the sorted
// keys that start with the pattern's prefix, pageSize at a time, the cursor
// being an index plus one.
type fakeScan struct {
	nodes     []string // nil: one primary, "n1"
	noPrimary bool     // every node is a replica
	pageSize  int
	scans     int
	gets      []string // every key read by getMulti
	maxBatch  int      // largest getMulti call
	dels      []string
	err       error
	snap      []string // keys listed at cursor 0; real SCAN is stable under deletes
	onScan    func()   // runs at the end of each SCAN step, with the key lock held
}

func (f *fakeClient) primaries(context.Context) ([]string, error) {
	if f.scan.err != nil {
		return nil, f.scan.err
	}
	if f.scan.noPrimary {
		return nil, nil
	}
	if f.scan.nodes == nil {
		return []string{"n1"}, nil
	}
	return f.scan.nodes, nil
}

func (f *fakeClient) scanNode(_ context.Context, _ string, cursor uint64, match string, _ int64) ([]string, uint64, error) {
	f.kv.mu.Lock()
	defer f.kv.mu.Unlock()
	f.scan.scans++
	if f.scan.onScan != nil {
		defer f.scan.onScan()
	}
	if f.scan.err != nil {
		return nil, 0, f.scan.err
	}
	if cursor == 0 {
		f.scan.snap = nil
		for k := range f.kv.vals {
			if strings.HasPrefix(k, strings.TrimSuffix(match, "*")) {
				f.scan.snap = append(f.scan.snap, k)
			}
		}
		sort.Strings(f.scan.snap)
	}
	all := f.scan.snap
	size := f.scan.pageSize
	if size == 0 {
		size = len(all) + 1
	}
	from := int(cursor)
	to := min(from+size, len(all))
	next := uint64(to)
	if to >= len(all) {
		next = 0
	}
	return slices.Clone(all[from:to]), next, nil
}

func (f *fakeClient) getMulti(_ context.Context, keys []string) ([][]byte, error) {
	f.kv.mu.Lock()
	defer f.kv.mu.Unlock()
	if f.scan.err != nil {
		return nil, f.scan.err
	}
	f.scan.gets = append(f.scan.gets, keys...)
	f.scan.maxBatch = max(f.scan.maxBatch, len(keys))
	out := make([][]byte, len(keys))
	for i, k := range keys {
		out[i] = f.kv.vals[k]
	}
	return out, nil
}

func (f *fakeClient) delMulti(_ context.Context, keys []string) (int64, error) {
	f.kv.mu.Lock()
	defer f.kv.mu.Unlock()
	if f.scan.err != nil {
		return 0, f.scan.err
	}
	var n int64
	for _, k := range keys {
		if _, ok := f.kv.vals[k]; ok {
			n++
		}
		delete(f.kv.vals, k)
		f.scan.dels = append(f.scan.dels, k)
	}
	return n, nil
}

func tagN(b byte) store.Tag { return store.Tag{b} }

func keyN(b byte) store.Key { return store.Key{b} }

func putResp(t *testing.T, s *Store, k store.Key, kind store.Kind, tags ...store.Tag) {
	t.Helper()
	e := testEntry(time.Now())
	e.Kind = kind
	e.Tags = tags
	if kind != store.KindResponse {
		e.Status, e.Body = 0, nil
	}
	if err := s.Set(t.Context(), k, e); err != nil {
		t.Fatalf("Set: %v", err)
	}
}

func present(cl *fakeClient, s *Store, k store.Key) bool {
	cl.kv.mu.Lock()
	defer cl.kv.mu.Unlock()
	_, ok := cl.kv.vals[s.entryKey(k)]
	return ok
}

// FR-PRG-8: Scrub removes the response records that carry a scrubbed tag and
// counts them; SCAN pages and a second tag in the list are both honoured.
func TestScrubDeletesMatchingResponses(t *testing.T) {
	s, cl := connected(t, nil)
	cl.scan.pageSize = 2
	for i := byte(0); i < 5; i++ {
		putResp(t, s, keyN(i), store.KindResponse, store.TagGlobal(), tagN(1))
	}
	putResp(t, s, keyN(5), store.KindResponse, store.TagGlobal(), tagN(2))
	putResp(t, s, keyN(6), store.KindResponse, store.TagGlobal(), tagN(3))
	n, err := s.Scrub(t.Context(), []store.Tag{tagN(1), tagN(2)})
	if err != nil || n != 6 {
		t.Fatalf("Scrub = %d, %v; want 6, nil", n, err)
	}
	for i := byte(0); i < 6; i++ {
		if present(cl, s, keyN(i)) {
			t.Fatalf("key %d survived", i)
		}
	}
	if !present(cl, s, keyN(6)) {
		t.Fatal("record with an unrelated tag was deleted")
	}
	if cl.scan.scans < 4 {
		t.Fatalf("SCAN steps = %d, want the cursor followed to the end (>= 4)", cl.scan.scans)
	}
}

// FR-PRG-8, T-29, 05 §7: epoch keys, sketch planes and non-hex keys under the
// prefix are never read, let alone deleted.
func TestScrubSkipsNonEntryKeys(t *testing.T) {
	for _, colo := range []bool{false, true} {
		name := "plain layout"
		if colo {
			name = "co-located layout"
		}
		t.Run(name, func(t *testing.T) {
			s, cl := connected(t, func(c *Config) { c.CoLocateEntries = colo })
			putResp(t, s, keyN(1), store.KindResponse, tagN(1))
			h := hex.EncodeToString(make([]byte, 32))
			foreign := []string{
				"weir:{e}:hardidx", "weir:{e}:global", "weir:{e}:sketch:soft", "weir:{e}:newest", "weir:{e}:meta",
				"weir:zz" + h[2:],  // right length, not hex
				"weir:" + h[:62],   // too short
				"weir:" + h + "00", // too long
				"weir:" + strings.ToUpper(hex.EncodeToString([]byte{0xab})+h[2:]), // upper case is not what entryKey writes
				"weirx:" + h, // another prefix
				"weir:{other}:" + h,
			}
			if !colo {
				foreign = append(foreign, "weir:{e}:"+h)
			}
			// Each foreign key holds a record that decodes and carries the
			// scrubbed tag, so a filter that lets one through deletes it.
			rec, err := store.Encode(func() *store.Entry { e := testEntry(time.Now()); e.Tags = []store.Tag{tagN(1)}; return e }())
			if err != nil {
				t.Fatal(err)
			}
			cl.kv.mu.Lock()
			for _, k := range foreign {
				cl.kv.vals[k] = rec
			}
			cl.kv.mu.Unlock()
			n, err := s.Scrub(t.Context(), []store.Tag{tagN(1)})
			if err != nil || n != 1 {
				t.Fatalf("Scrub = %d, %v; want 1, nil", n, err)
			}
			if want := []string{s.entryKey(keyN(1))}; !slices.Equal(cl.scan.gets, want) {
				t.Errorf("keys read = %q, want exactly %q", cl.scan.gets, want)
			}
			for _, k := range foreign {
				if _, ok := cl.kv.vals[k]; !ok {
					t.Errorf("foreign key %q was deleted", k)
				}
			}
		})
	}
}

// FR-PRG-8: only response records go; a vary spec or marker is covered by the
// epoch the caller wrote first (05 §5.3). A value that does not decode is left
// for the server to expire and does not fail the scrub.
func TestScrubLeavesOtherKindsAndUndecodable(t *testing.T) {
	s, cl := connected(t, nil)
	putResp(t, s, keyN(1), store.KindResponse, tagN(1))
	putResp(t, s, keyN(2), store.KindVarySpec, tagN(1))
	cl.kv.mu.Lock()
	cl.kv.vals[s.entryKey(keyN(3))] = []byte("not a record")
	cl.kv.mu.Unlock()
	n, err := s.Scrub(t.Context(), []store.Tag{tagN(1)})
	if err != nil || n != 1 {
		t.Fatalf("Scrub = %d, %v; want 1, nil", n, err)
	}
	if !present(cl, s, keyN(2)) || !present(cl, s, keyN(3)) {
		t.Fatal("a vary spec or an undecodable value was deleted")
	}
}

// FR-PRG-8: no tags is no work and no round trip; a key that expired between
// SCAN and GET is skipped.
func TestScrubEmptyAndVanished(t *testing.T) {
	s, cl := connected(t, nil)
	putResp(t, s, keyN(1), store.KindResponse, tagN(1))
	if n, err := s.Scrub(t.Context(), nil); err != nil || n != 0 || cl.scan.scans != 0 {
		t.Fatalf("Scrub(nil) = %d, %v after %d scans; want 0, nil, 0", n, err, cl.scan.scans)
	}
	cl.scan.onScan = func() { delete(cl.kv.vals, s.entryKey(keyN(1))) } // the key expires after SCAN listed it
	if n, err := s.Scrub(t.Context(), []store.Tag{tagN(1)}); err != nil || n != 0 {
		t.Fatalf("Scrub of a vanished key = %d, %v; want 0, nil", n, err)
	}
}

// FR-PRG-8, 05 §7: a cancelled context stops between batches and returns the
// count so far with ErrUnavailable.
func TestScrubCancelledStopsBetweenBatches(t *testing.T) {
	s, cl := connected(t, nil)
	cl.scan.pageSize = 2
	for i := byte(0); i < 6; i++ {
		putResp(t, s, keyN(i), store.KindResponse, tagN(1))
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	cl.scan.onScan = func() {
		if cl.scan.scans == 2 {
			cancel() // after the second batch has been read
		}
	}
	n, err := s.Scrub(ctx, []store.Tag{tagN(1)})
	if !errors.Is(err, store.ErrUnavailable) {
		t.Fatalf("err = %v, want ErrUnavailable", err)
	}
	if n != 4 {
		t.Fatalf("count so far = %d, want 4 (two batches of 2)", n)
	}
}

// FR-PRG-8, S-3: a server error is ErrUnavailable with the count so far; a
// scrub across several primaries visits each once.
func TestScrubErrorsAndNodes(t *testing.T) {
	t.Run("server error maps to ErrUnavailable", func(t *testing.T) {
		s, cl := connected(t, nil)
		putResp(t, s, keyN(1), store.KindResponse, tagN(1))
		cl.scan.err = errors.New("LOADING")
		if _, err := s.Scrub(t.Context(), []store.Tag{tagN(1)}); !errors.Is(err, store.ErrUnavailable) {
			t.Fatalf("err = %v, want ErrUnavailable", err)
		}
	})
	t.Run("every primary is scanned", func(t *testing.T) {
		s, cl := connected(t, nil)
		cl.scan.nodes = []string{"a", "b", "c"}
		putResp(t, s, keyN(1), store.KindResponse, tagN(1))
		if _, err := s.Scrub(t.Context(), []store.Tag{tagN(1)}); err != nil {
			t.Fatal(err)
		}
		if cl.scan.scans != 3 {
			t.Fatalf("SCAN steps = %d, want one per primary (3)", cl.scan.scans)
		}
	})
	t.Run("after Close", func(t *testing.T) {
		s, _ := connected(t, nil)
		_ = s.Close()
		if _, err := s.Scrub(t.Context(), []store.Tag{tagN(1)}); !errors.Is(err, store.ErrUnavailable) {
			t.Fatalf("err = %v, want ErrUnavailable", err)
		}
	})
}

// FR-PRG-8, P5: with no primary node Scrub fails instead of reporting an
// empty success, and a large batch is read in chunks of at most scrubChunk.
func TestScrubNoPrimaryAndChunking(t *testing.T) {
	t.Run("no primary is ErrUnavailable", func(t *testing.T) {
		s, cl := connected(t, nil)
		putResp(t, s, keyN(1), store.KindResponse, tagN(1))
		cl.scan.noPrimary = true
		n, err := s.Scrub(t.Context(), []store.Tag{tagN(1)})
		if !errors.Is(err, store.ErrUnavailable) || n != 0 {
			t.Fatalf("Scrub = %d, %v; want 0, ErrUnavailable", n, err)
		}
		if !present(cl, s, keyN(1)) {
			t.Fatal("entry deleted with no primary")
		}
	})
	t.Run("GET pipelines are chunked", func(t *testing.T) {
		s, cl := connected(t, nil)
		const n = 3*scrubChunk + 7
		for i := range n {
			putResp(t, s, store.Key{byte(i), byte(i >> 8), 1}, store.KindResponse, tagN(1))
		}
		got, err := s.Scrub(t.Context(), []store.Tag{tagN(1)})
		if err != nil || got != n {
			t.Fatalf("Scrub = %d, %v; want %d, nil", got, err, n)
		}
		if cl.scan.maxBatch > scrubChunk {
			t.Fatalf("largest GET pipeline = %d keys, want at most %d", cl.scan.maxBatch, scrubChunk)
		}
	})
}
