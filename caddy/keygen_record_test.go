package weircaddy

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"

	"github.com/AshwinSathian/weir/store"
	"github.com/AshwinSathian/weir/store/memory"
)

// fakeRecorder is a store with a key-generation record, like the Valkey store.
type fakeRecorder struct {
	store.Store
	mu   sync.Mutex
	rec  []byte
	err  error
	seen [][]byte
}

func (f *fakeRecorder) RecordKeyGen(_ context.Context, h []byte) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.seen = append(f.seen, bytes.Clone(h))
	if f.err != nil {
		return false, f.err
	}
	prev := f.rec
	f.rec = bytes.Clone(h)
	return prev != nil && !bytes.Equal(prev, h), nil
}

func warnLog(sb *strings.Builder) *slog.Logger {
	return slog.New(slog.NewTextHandler(sb, &slog.HandlerOptions{Level: slog.LevelWarn}))
}

// R-3, 08 §3: the record decides, not the process. A first record and an
// equal one purge nothing; a different one reports a change and warns that
// nodes sharing the prefix must agree.
func TestSyncKeyGen(t *testing.T) {
	loose := keyGenHash((&Handler{Name: "n"}).weirConfig())
	tight := keyGenHash((&Handler{Name: "n", Forward: ForwardConfig{Allow: []string{"X-A"}}}).weirConfig())
	if loose == tight {
		t.Fatal("test needs two different hashes")
	}
	f := &fakeRecorder{}
	var sb strings.Builder
	for i, tc := range []struct {
		h    [32]byte
		want bool
	}{{loose, false}, {loose, false}, {tight, true}, {tight, false}} {
		if got := syncKeyGen(t.Context(), f, tc.h, warnLog(&sb), "n"); got != tc.want {
			t.Fatalf("step %d: changed = %v, want %v", i, got, tc.want)
		}
	}
	if n := strings.Count(sb.String(), "purge each other"); n != 1 {
		t.Fatalf("want one warning naming the shared-prefix rule, got %d in %q", n, sb.String())
	}
}

// FR-STF-2: a server that cannot answer neither fails the load nor purges;
// the skipped check is logged. A memory store has no record and says nothing.
func TestSyncKeyGenOutageAndMemory(t *testing.T) {
	var sb strings.Builder
	f := &fakeRecorder{err: errors.New("down")}
	if syncKeyGen(t.Context(), f, [32]byte{1}, warnLog(&sb), "n") {
		t.Fatal("an unreachable server reported a change")
	}
	if !strings.Contains(sb.String(), "not checked") {
		t.Fatalf("skipped check not logged: %q", sb.String())
	}
	mem, err := memory.New(memory.Config{})
	if err != nil {
		t.Fatal(err)
	}
	defer mem.Close()
	sb.Reset()
	if syncKeyGen(t.Context(), mem, [32]byte{1}, warnLog(&sb), "n") || sb.Len() != 0 {
		t.Fatalf("memory store: changed or logged %q", sb.String())
	}
}

// R-3: a failed purge leaves a record no hash equals, so the next start
// purges again instead of trusting a record for entries still on the server.
func TestMarkKeyGenPendingNeverMatches(t *testing.T) {
	f := &fakeRecorder{}
	h := keyGenHash((&Handler{Name: "n"}).weirConfig())
	syncKeyGen(t.Context(), f, h, slog.New(slog.NewTextHandler(io.Discard, nil)), "n")
	markKeyGenPending(t.Context(), f)
	if !syncKeyGen(t.Context(), f, h, slog.New(slog.NewTextHandler(io.Discard, nil)), "n") {
		t.Fatal("the same config after a failed purge must purge again")
	}
}

// R-3: a password rotation or any other store setting changes the pool key
// but not the hash recorded on the server, so it never purges by itself.
func TestRecordedHashExcludesStoreSettings(t *testing.T) {
	a := &Handler{Name: "n", Store: &StoreConfig{Type: "valkey", Addrs: []string{"h:1"}, Password: "one"}}
	b := &Handler{Name: "n", Store: &StoreConfig{Type: "valkey", Addrs: []string{"h:1"}, Password: "two"}}
	if a.storeSpec().valkey == b.storeSpec().valkey {
		t.Fatal("test needs different pool keys")
	}
	if keyGenHash(a.weirConfig()) != keyGenHash(b.weirConfig()) {
		t.Fatal("a store setting reached the recorded forwarding hash")
	}
}
