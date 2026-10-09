package memory

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"os"
	"path/filepath"
	"testing"
	"testing/synctest"
	"time"

	"github.com/AshwinSathian/weir/store"
)

type snapRecord struct {
	kind    byte
	payload []byte
}

// readSnapshot parses path per 05 §5.5, failing the test on a bad CRC, a
// missing trailer or a count mismatch.
func readSnapshot(t *testing.T, path string) []snapRecord {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read snapshot: %v", err)
	}
	if len(b) < 17 || string(b[:8]) != snapMagic || b[8] != snapVersion {
		t.Fatalf("bad header % x", b[:min(len(b), 17)])
	}
	b = b[17:]
	var recs []snapRecord
	for len(b) > 0 {
		n, k := binary.Uvarint(b[1:])
		if k <= 0 || uint64(len(b)) < 1+uint64(k)+n+4 {
			t.Fatalf("truncated record")
		}
		end := 1 + uint64(k) + n
		if got, want := binary.BigEndian.Uint32(b[end:]), crc32.Checksum(b[:end], castagnoli); got != want {
			t.Fatalf("crc %08x, want %08x", got, want)
		}
		recs = append(recs, snapRecord{b[0], b[1+uint64(k) : end]})
		b = b[end+4:]
	}
	last := recs[len(recs)-1]
	if last.kind != snapKindTrailer || binary.BigEndian.Uint64(last.payload) != uint64(len(recs)-1) {
		t.Fatalf("bad trailer %+v for %d records", last, len(recs)-1)
	}
	return recs[:len(recs)-1]
}

func dirNames(t *testing.T, dir string) []string {
	t.Helper()
	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range ents {
		names = append(names, e.Name())
	}
	return names
}

// FR-SNP-1, 05 §5.5 (write half of TestSnapshotRoundTrip): live responses
// and vary specs plus hard epochs, in a 0600 file, with a trailer; markers,
// negative entries, expired records and soft epochs are left out.
func TestSnapshotRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "weir.snap")
	s, err := New(Config{SnapshotPath: path, Shards: 2})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	exp := time.Now().Add(time.Hour)
	resp := &store.Entry{Kind: store.KindResponse, Status: 200, Body: []byte("hello"), Expires: exp}
	spec := &store.Entry{Kind: store.KindVarySpec, VaryNames: []string{"Accept"}, Expires: exp}
	for k, e := range map[store.Key]*store.Entry{
		numKey(1): resp,
		numKey(2): spec,
		numKey(3): {Kind: store.KindNegative, Status: 502, Expires: exp},
		numKey(4): {Kind: store.KindHitForMiss, Expires: exp},
	} {
		if err := s.Set(ctx, k, e); err != nil {
			t.Fatal(err)
		}
	}
	hard, soft := store.Tag{7}, store.Tag{8}
	at := time.Now().Add(-time.Minute).Truncate(time.Nanosecond)
	if err := s.SetEpoch(ctx, hard, store.Epoch{At: at, Mode: store.EpochHard}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetEpoch(ctx, soft, store.Epoch{At: at, Mode: store.EpochSoft}); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}

	if fi, err := os.Stat(path); err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("snapshot stat = %v, %v; want mode 0600", fi, err)
	}
	if names := dirNames(t, dir); len(names) != 1 {
		t.Fatalf("directory holds %v, want only the snapshot", names)
	}
	got := map[store.Key]*store.Entry{}
	var epochs int
	for _, r := range readSnapshot(t, path) {
		switch r.kind {
		case snapKindEntry:
			var k store.Key
			copy(k[:], r.payload)
			e, err := store.Decode(r.payload[32:], 1<<20)
			if err != nil {
				t.Fatalf("decode: %v", err)
			}
			got[k] = e
		case snapKindEpoch:
			epochs++
			if store.Tag(r.payload[:32]) != hard || int64(binary.BigEndian.Uint64(r.payload[32:])) != at.UnixNano() {
				t.Errorf("epoch record % x, want tag %x at %d", r.payload, hard, at.UnixNano())
			}
		default:
			t.Errorf("unexpected record kind %#x", r.kind)
		}
	}
	if len(got) != 2 || got[numKey(1)] == nil || got[numKey(2)] == nil {
		t.Fatalf("snapshot keys = %d, want responses and vary spec only", len(got))
	}
	if !bytes.Equal(got[numKey(1)].Body, resp.Body) || got[numKey(2)].VaryNames[0] != "Accept" {
		t.Errorf("entries did not round-trip: %+v %+v", got[numKey(1)], got[numKey(2)])
	}
	if epochs != 1 {
		t.Errorf("%d epoch records, want 1 (hard only)", epochs)
	}
}

// FR-SNP-1, 05 §5.5: main-queue records precede small-queue records.
func TestSnapshotMainQueueFirst(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "weir.snap")
	s := newStore(t, Config{SnapshotPath: path, Shards: 1})
	ctx := context.Background()
	for i := uint64(1); i <= 2; i++ {
		_ = s.Set(ctx, numKey(i), entry(10))
	}
	sh := &s.shards[0]
	sh.mu.Lock()
	n := sh.m[numKey(1)]
	sh.small.remove(n)
	n.queue = queueMain
	sh.main.push(n)
	sh.mu.Unlock()
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	recs := readSnapshot(t, path)
	if len(recs) != 2 || store.Key(recs[0].payload[:32]) != numKey(1) {
		t.Fatalf("first record is not the main-queue key: %d records", len(recs))
	}
}

// ctxAfter reports Canceled once Err has been called n times.
type ctxAfter struct {
	context.Context
	n int
}

func (c *ctxAfter) Err() error {
	if c.n--; c.n < 0 {
		return context.Canceled
	}
	return nil
}

// FR-SNP-1: an incomplete snapshot is discarded, never renamed; no temp file
// is left behind.
func TestSnapshotRespectsDeadline(t *testing.T) {
	for name, ctx := range map[string]context.Context{
		"expired before the first record": canceled(),
		"expires midway":                  &ctxAfter{context.Background(), 3},
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "weir.snap")
			s, err := New(Config{SnapshotPath: path})
			if err != nil {
				t.Fatal(err)
			}
			for i := uint64(1); i <= 10; i++ {
				_ = s.Set(context.Background(), numKey(i), entry(10))
			}
			if err := s.CloseContext(ctx); !errors.Is(err, context.Canceled) {
				t.Fatalf("CloseContext = %v, want context.Canceled", err)
			}
			if names := dirNames(t, dir); len(names) != 0 {
				t.Fatalf("directory holds %v, want nothing", names)
			}
			if _, err := s.Get(context.Background(), numKey(1)); !errors.Is(err, store.ErrUnavailable) {
				t.Errorf("Get after close = %v, want ErrUnavailable", err)
			}
		})
	}
}

func canceled() context.Context {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	return ctx
}

// FR-SNP-1: without SnapshotPath nothing is written; a negative timeout is
// rejected.
func TestSnapshotOffAndConfig(t *testing.T) {
	dir := t.TempDir()
	s, err := New(Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil || len(dirNames(t, dir)) != 0 {
		t.Fatalf("Close = %v", err)
	}
	if _, err := New(Config{SnapshotTimeout: -1}); err == nil {
		t.Error("New accepted a negative SnapshotTimeout")
	}
}

// FR-SNP-1: Close applies SnapshotTimeout, and a snapshot it cuts off leaves
// no file.
func TestSnapshotCloseUsesTimeout(t *testing.T) {
	dir := t.TempDir()
	s, err := New(Config{SnapshotPath: filepath.Join(dir, "weir.snap"), SnapshotTimeout: time.Nanosecond})
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(time.Millisecond)
	if err := s.Close(); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Close = %v, want DeadlineExceeded", err)
	}
	if names := dirNames(t, dir); len(names) != 0 {
		t.Fatalf("directory holds %v, want nothing", names)
	}
}

// FR-SNP-1: a record that expired before Close is not written.
func TestSnapshotSkipsExpired(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "weir.snap")
		s, err := New(Config{SnapshotPath: path})
		if err != nil {
			t.Fatal(err)
		}
		short := &store.Entry{Kind: store.KindResponse, Status: 200, Expires: time.Now().Add(time.Minute)}
		long := &store.Entry{Kind: store.KindResponse, Status: 200, Expires: time.Now().Add(time.Hour)}
		_ = s.Set(context.Background(), numKey(1), short)
		_ = s.Set(context.Background(), numKey(2), long)
		time.Sleep(10 * time.Minute)
		if err := s.Close(); err != nil {
			t.Fatal(err)
		}
		recs := readSnapshot(t, path)
		if len(recs) != 1 || store.Key(recs[0].payload[:32]) != numKey(2) {
			t.Fatalf("snapshot holds %d records, want only the unexpired key", len(recs))
		}
	})
}
