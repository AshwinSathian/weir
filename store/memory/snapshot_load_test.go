package memory

import (
	"bytes"
	"context"
	"encoding/binary"
	"hash/crc32"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/AshwinSathian/weir/store"
)

// snapFile builds a snapshot file with the writer's framing, so loader tests
// can corrupt single records.
type snapFile struct {
	b     []byte
	count uint64
}

func newSnapFile() *snapFile {
	b := append([]byte(snapMagic), snapVersion)
	return &snapFile{b: binary.BigEndian.AppendUint64(b, uint64(time.Now().UnixNano()))} //nolint:gosec // bit pattern
}

func frame(kind byte, payload []byte) []byte {
	b := binary.AppendUvarint([]byte{kind}, uint64(len(payload)))
	b = append(b, payload...)
	return binary.BigEndian.AppendUint32(b, crc32.Checksum(b, castagnoli))
}

func (f *snapFile) entry(t *testing.T, k store.Key, e *store.Entry) {
	t.Helper()
	enc, err := store.Encode(e)
	if err != nil {
		t.Fatal(err)
	}
	f.b = append(f.b, frame(snapKindEntry, append(k[:], enc...))...)
	f.count++
}

func (f *snapFile) epoch(tag store.Tag, at time.Time) {
	f.b = append(f.b, frame(snapKindEpoch, binary.BigEndian.AppendUint64(tag[:], uint64(at.UnixNano())))...) //nolint:gosec // bit pattern
	f.count++
}

func (f *snapFile) write(t *testing.T, path string) {
	t.Helper()
	b := append(bytes.Clone(f.b), frame(snapKindTrailer, binary.BigEndian.AppendUint64(nil, f.count))...)
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}
}

func liveEntry(body string) *store.Entry {
	return &store.Entry{
		Kind: store.KindResponse, Status: 200, Body: []byte(body),
		RequestTime: time.Now().Add(-time.Minute), Expires: time.Now().Add(time.Hour),
	}
}

func loadFrom(t *testing.T, path string, cfg Config) *Store {
	t.Helper()
	cfg.SnapshotPath = path
	s, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// FR-SNP-1, FR-SNP-2, 05 §5.5 (read half of TestSnapshotRoundTrip above):
// what Close wrote, New loads, and the file is gone afterwards (T-33).
func TestSnapshotLoadRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "weir.snap")
	s, err := New(Config{SnapshotPath: path})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := s.Set(ctx, numKey(1), liveEntry("one")); err != nil {
		t.Fatal(err)
	}
	spec := &store.Entry{Kind: store.KindVarySpec, VaryNames: []string{"Accept"}, Expires: time.Now().Add(time.Hour)}
	if err := s.Set(ctx, numKey(2), spec); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	s2 := loadFrom(t, path, Config{})
	got, err := s2.Get(ctx, numKey(1))
	if err != nil || string(got.Body) != "one" {
		t.Fatalf("Get(1) = %+v, %v; want the stored body", got, err)
	}
	if sp, err := s2.Get(ctx, numKey(2)); err != nil || sp.VaryNames[0] != "Accept" {
		t.Fatalf("Get(2) = %+v, %v; want the vary spec", sp, err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("snapshot still present after load: %v", err)
	}
	if s2.snapLoad.loaded != 2 {
		t.Errorf("loaded = %d, want 2", s2.snapLoad.loaded)
	}
}

// FR-SNP-2, T-33: every loaded entry is stale as of the restart, so a purge
// issued while the node was down cannot be undone by the snapshot.
func TestSnapshotLoadIsSoftStale(t *testing.T) {
	path := filepath.Join(t.TempDir(), "weir.snap")
	f := newSnapFile()
	e := liveEntry("old")
	f.entry(t, numKey(1), e)
	f.write(t, path)

	s := loadFrom(t, path, Config{})
	ctx := context.Background()
	if _, err := s.Get(ctx, numKey(1)); err != nil {
		t.Fatal(err)
	}
	ep, ok, err := s.NewestEpoch(ctx, []store.Tag{store.TagGlobal()}, e.RequestTime)
	if err != nil || !ok || ep.Mode != store.EpochSoft || ep.At.Before(e.RequestTime) {
		t.Fatalf("global epoch = %+v, %v, %v; want soft at or after the entry's request time", ep, ok, err)
	}
}

// FR-SNP-2, T-33: hard epochs, the global one included, survive a restart.
func TestSnapshotHardEpochSurvives(t *testing.T) {
	path := filepath.Join(t.TempDir(), "weir.snap")
	s, err := New(Config{SnapshotPath: path})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	at := time.Now().Add(-time.Minute)
	tag := store.Tag{9}
	for _, tg := range []store.Tag{tag, store.TagGlobal()} {
		if err := s.SetEpoch(ctx, tg, store.Epoch{At: at, Mode: store.EpochHard}); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	s2 := loadFrom(t, path, Config{})
	since := at.Add(-time.Hour)
	for name, tg := range map[string]store.Tag{"tag": tag, "global": store.TagGlobal()} {
		ep, ok, err := s2.NewestEpoch(ctx, []store.Tag{tg}, since)
		if err != nil || !ok || ep.Mode != store.EpochHard || ep.At.Sub(at).Abs() > time.Microsecond {
			t.Errorf("%s epoch = %+v, %v, %v; want hard at %v", name, ep, ok, err, at)
		}
	}
}

// FR-SNP-2: records failing CRC or decoding are skipped and counted; the
// rest load. Records past Expires are dropped.
func TestSnapshotCorruptRecordsSkipped(t *testing.T) {
	path := filepath.Join(t.TempDir(), "weir.snap")
	f := newSnapFile()
	f.entry(t, numKey(1), liveEntry("good"))
	bad := len(f.b)
	f.entry(t, numKey(2), liveEntry("flipped"))
	f.b[bad+len(f.b[bad:])/2] ^= 0xFF // CRC mismatch, framing intact
	k3 := numKey(3)
	f.b = append(f.b, frame(snapKindEntry, append(k3[:], "not a codec record"...))...)
	f.count++
	f.b = append(f.b, frame(0x7E, []byte("unknown kind"))...)
	f.count++
	expired := liveEntry("late")
	expired.Expires = time.Now().Add(-time.Second)
	f.entry(t, numKey(4), expired)
	f.entry(t, numKey(5), liveEntry("also good"))
	f.write(t, path)

	s := loadFrom(t, path, Config{})
	ctx := context.Background()
	for _, n := range []uint64{1, 5} {
		if _, err := s.Get(ctx, numKey(n)); err != nil {
			t.Errorf("Get(%d) = %v, want loaded", n, err)
		}
	}
	for _, n := range []uint64{2, 3, 4} {
		if _, err := s.Get(ctx, numKey(n)); err == nil {
			t.Errorf("Get(%d) found a record that must not load", n)
		}
	}
	if l := s.snapLoad; l.loaded != 2 || l.skipped != 3 || l.expired != 1 {
		t.Errorf("load stats = %+v, want 2 loaded, 3 skipped, 1 expired", l)
	}
}

// 05 §5.5: a file without a valid trailer is incomplete and ignored; bytes
// that are not a snapshot are left alone.
func TestSnapshotIncompleteIgnored(t *testing.T) {
	dir := t.TempDir()
	f := newSnapFile()
	f.entry(t, numKey(1), liveEntry("x"))
	cut := filepath.Join(dir, "cut.snap")
	if err := os.WriteFile(cut, f.b, 0o600); err != nil { // no trailer
		t.Fatal(err)
	}
	s := loadFrom(t, cut, Config{})
	if _, err := s.Get(context.Background(), numKey(1)); err == nil {
		t.Error("record from a file without trailer was loaded")
	}
	if _, err := os.Stat(cut); !os.IsNotExist(err) {
		t.Error("incomplete snapshot was not removed")
	}

	other := filepath.Join(dir, "other")
	if err := os.WriteFile(other, []byte("precious unrelated data, not a snapshot"), 0o600); err != nil {
		t.Fatal(err)
	}
	loadFrom(t, other, Config{})
	if _, err := os.Stat(other); err != nil {
		t.Errorf("a file without the snapshot magic was removed: %v", err)
	}
	loadFrom(t, filepath.Join(dir, "missing.snap"), Config{}) // no file: no error
}

// FR-SNP-3: load stops at MaxBytes, dropping later records in file order.
func TestSnapshotLoadRespectsMaxBytes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "weir.snap")
	f := newSnapFile()
	body := string(make([]byte, 300))
	const n = 40
	for i := uint64(1); i <= n; i++ {
		f.entry(t, numKey(i), liveEntry(body))
	}
	f.write(t, path)

	s := loadFrom(t, path, Config{MaxBytes: 8 << 10, Shards: 1})
	if s.Bytes() > 8<<10 {
		t.Errorf("Bytes = %d, above MaxBytes", s.Bytes())
	}
	if l := s.snapLoad; l.loaded == 0 || l.loaded == n || l.loaded+l.dropped != n {
		t.Errorf("load stats = %+v, want some loaded and the rest dropped", l)
	}
	if _, err := s.Get(context.Background(), numKey(1)); err != nil {
		t.Errorf("first record in file order was dropped: %v", err)
	}
}

// NFR-2: a hostile length prefix cannot allocate or panic.
func TestSnapshotLoadHostileLength(t *testing.T) {
	path := filepath.Join(t.TempDir(), "weir.snap")
	f := newSnapFile()
	f.entry(t, numKey(1), liveEntry("ok"))
	f.b = append(f.b, snapKindEntry)
	f.b = binary.AppendUvarint(f.b, 1<<60)
	f.write(t, path)
	s := loadFrom(t, path, Config{})
	if _, err := s.Get(context.Background(), numKey(1)); err != nil {
		t.Errorf("record before the hostile length lost: %v", err)
	}
}

func FuzzSnapshotLoad(f *testing.F) {
	good := newSnapFile()
	good.b = append(good.b, frame(snapKindEntry, make([]byte, 40))...)
	f.Add(append(bytes.Clone(good.b), frame(snapKindTrailer, binary.BigEndian.AppendUint64(nil, 1))...))
	f.Add([]byte(snapMagic))
	f.Add([]byte{})
	f.Fuzz(func(t *testing.T, b []byte) {
		path := filepath.Join(t.TempDir(), "weir.snap")
		if err := os.WriteFile(path, b, 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := New(Config{SnapshotPath: path, MaxBytes: 1 << 20, Shards: 1}); err != nil {
			t.Fatal(err)
		}
	})
}
