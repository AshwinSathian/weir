package weircaddy

import (
	"bytes"
	"context"
	"errors"
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
	mu       sync.Mutex
	rec      []byte
	checkErr error
	writeErr error
}

func (f *fakeRecorder) CheckKeyGen(_ context.Context, h []byte) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.checkErr != nil {
		return false, f.checkErr
	}
	return f.rec != nil && !bytes.Equal(f.rec, h), nil
}

func (f *fakeRecorder) RecordKeyGen(_ context.Context, h []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.writeErr != nil {
		return f.writeErr
	}
	f.rec = bytes.Clone(h)
	return nil
}

func warnLog(sb *strings.Builder) *slog.Logger {
	return slog.New(slog.NewTextHandler(sb, &slog.HandlerOptions{Level: slog.LevelWarn}))
}

// R-3, 08 §3: the record decides, not the process. A first record and an
// equal one purge nothing; a different one reports a change and warns that
// nodes sharing the prefix must agree. Checking alone never replaces the
// record, so a start that dies before its purge repeats it.
func TestSyncKeyGen(t *testing.T) {
	loose := keyGenHash((&Handler{Name: "n"}).weirConfig())
	tight := keyGenHash((&Handler{Name: "n", Forward: ForwardConfig{Allow: []string{"X-A"}}}).weirConfig())
	if loose == tight {
		t.Fatal("test needs two different hashes")
	}
	f := &fakeRecorder{}
	var sb strings.Builder
	log := warnLog(&sb)
	step := func(h [32]byte, want, record bool) {
		t.Helper()
		if got := checkKeyGen(t.Context(), f, h, log, "n"); got != want {
			t.Fatalf("changed = %v, want %v", got, want)
		}
		if record {
			recordKeyGen(t.Context(), f, h, log, "n")
		}
	}
	step(loose, false, true)
	step(loose, false, true)
	step(tight, true, false) // the purge never finished
	step(tight, true, true)  // the retry purges again, then records
	step(tight, false, true)
	if n := strings.Count(sb.String(), "purge each other"); n != 2 {
		t.Fatalf("want two warnings naming the shared-prefix rule, got %d in %q", n, sb.String())
	}
}

// FR-STF-2: a server that cannot answer neither fails the load nor purges;
// the skipped check, and a record write that fails, are logged. A memory
// store has no record and says nothing.
func TestSyncKeyGenOutageAndMemory(t *testing.T) {
	var sb strings.Builder
	f := &fakeRecorder{checkErr: errors.New("down"), writeErr: errors.New("down")}
	if checkKeyGen(t.Context(), f, [32]byte{1}, warnLog(&sb), "n") {
		t.Fatal("an unreachable server reported a change")
	}
	recordKeyGen(t.Context(), f, [32]byte{1}, warnLog(&sb), "n")
	for _, want := range []string{"not checked", "not written"} {
		if !strings.Contains(sb.String(), want) {
			t.Fatalf("missing %q in %q", want, sb.String())
		}
	}
	mem, err := memory.New(memory.Config{})
	if err != nil {
		t.Fatal(err)
	}
	defer mem.Close()
	sb.Reset()
	if checkKeyGen(t.Context(), mem, [32]byte{1}, warnLog(&sb), "n") {
		t.Fatal("memory store reported a change")
	}
	recordKeyGen(t.Context(), mem, [32]byte{1}, warnLog(&sb), "n")
	if sb.Len() != 0 {
		t.Fatalf("memory store logged %q", sb.String())
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
