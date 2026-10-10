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

// runKeyGen drives reconcileServerKeyGen with a purge that records its calls and
// the record's value when it ran.
type keyGenRun struct {
	purges      int
	recAtPurge  []byte
	purgeErr    error
	err         error
	log         strings.Builder
	recAfterRun []byte
}

func runKeyGen(t *testing.T, f *fakeRecorder, h [32]byte, keyChanged bool, purgeErr error) *keyGenRun {
	t.Helper()
	r := &keyGenRun{purgeErr: purgeErr}
	r.err = reconcileServerKeyGen(f, h, keyChanged, func(context.Context) error {
		r.purges++
		f.mu.Lock()
		r.recAtPurge = bytes.Clone(f.rec)
		f.mu.Unlock()
		return purgeErr
	}, warnLog(&r.log), "n")
	f.mu.Lock()
	r.recAfterRun = bytes.Clone(f.rec)
	f.mu.Unlock()
	return r
}

// R-3, 08 §3: the record decides, not the process. A first record and an
// equal one purge nothing; a different one purges, warns that nodes sharing
// the prefix must agree, and replaces the record only after the purge ran.
func TestReconcileServerKeyGen(t *testing.T) {
	loose := keyGenHash((&Handler{Name: "n"}).weirConfig())
	tight := keyGenHash((&Handler{Name: "n", Forward: ForwardConfig{Allow: []string{"X-A"}}}).weirConfig())
	if loose == tight {
		t.Fatal("test needs two different hashes")
	}
	f := &fakeRecorder{}
	if r := runKeyGen(t, f, loose, false, nil); r.err != nil || r.purges != 0 || !bytes.Equal(r.recAfterRun, loose[:]) {
		t.Fatalf("first record: %+v", r)
	}
	if r := runKeyGen(t, f, loose, false, nil); r.err != nil || r.purges != 0 {
		t.Fatalf("equal record purged: %+v", r)
	}
	r := runKeyGen(t, f, tight, false, nil)
	if r.err != nil || r.purges != 1 {
		t.Fatalf("tighter forward: %+v", r)
	}
	if !bytes.Equal(r.recAtPurge, loose[:]) || !bytes.Equal(r.recAfterRun, tight[:]) {
		t.Fatal("the record must still be the old value while the purge runs and the new one after")
	}
	if !strings.Contains(r.log.String(), "purge each other") {
		t.Fatalf("no shared-prefix warning: %q", r.log.String())
	}
	if r := runKeyGen(t, f, tight, false, nil); r.purges != 0 {
		t.Fatal("the recorded hash purged again")
	}
	// An in-process change with an equal record still purges, once.
	if r := runKeyGen(t, f, tight, true, nil); r.purges != 1 || r.err != nil {
		t.Fatalf("keyChanged: %+v", r)
	}
}

// R-3: a failed purge returns the error and leaves the old record, so the
// next start purges again.
func TestReconcileServerKeyGenFailedPurgeKeepsRecord(t *testing.T) {
	loose, tight := [32]byte{1}, [32]byte{2}
	f := &fakeRecorder{rec: loose[:]}
	boom := errors.New("boom")
	r := runKeyGen(t, f, tight, false, boom)
	if !errors.Is(r.err, boom) || !bytes.Equal(r.recAfterRun, loose[:]) {
		t.Fatalf("err = %v, record = %x; want the purge error and the old record", r.err, r.recAfterRun)
	}
	if r := runKeyGen(t, f, tight, false, nil); r.purges != 1 {
		t.Fatal("the retry did not purge")
	}
}

// FR-STF-2, R-3: a check that cannot be made neither fails the load nor
// overwrites the record (that would lose the purge for good); a record write
// that fails is only logged. A memory store has no record and says nothing.
func TestReconcileServerKeyGenOutageAndMemory(t *testing.T) {
	loose, tight := [32]byte{1}, [32]byte{2}
	f := &fakeRecorder{rec: loose[:], checkErr: errors.New("down")}
	r := runKeyGen(t, f, tight, false, nil)
	if r.err != nil || r.purges != 0 || !bytes.Equal(r.recAfterRun, loose[:]) {
		t.Fatalf("check failed: %+v, record %x", r, r.recAfterRun)
	}
	if !strings.Contains(r.log.String(), "not checked") {
		t.Fatalf("skipped check not logged: %q", r.log.String())
	}
	// A known change still purges even though the check failed.
	if r := runKeyGen(t, f, tight, true, nil); r.purges != 1 || !bytes.Equal(r.recAfterRun, loose[:]) {
		t.Fatalf("keyChanged with a failed check: %+v", r)
	}
	f = &fakeRecorder{writeErr: errors.New("down")}
	if r := runKeyGen(t, f, tight, false, nil); r.err != nil || !strings.Contains(r.log.String(), "not written") {
		t.Fatalf("write failure: %+v %q", r.err, r.log.String())
	}
	mem, err := memory.New(memory.Config{})
	if err != nil {
		t.Fatal(err)
	}
	defer mem.Close()
	var sb strings.Builder
	purges := 0
	for _, kc := range []bool{false, true} {
		if err := reconcileServerKeyGen(mem, tight, kc, func(context.Context) error { purges++; return nil }, warnLog(&sb), "n"); err != nil {
			t.Fatal(err)
		}
	}
	if purges != 1 || sb.Len() != 0 {
		t.Fatalf("memory store: %d purges, log %q", purges, sb.String())
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
