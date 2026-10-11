//go:build unix

package memory

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// T-33: a named pipe planted at the snapshot path must not hang New.
func TestSnapshotFIFOAtPathDoesNotHangNew(t *testing.T) {
	path := filepath.Join(t.TempDir(), "snap")
	if err := syscall.Mkfifo(path, 0o600); err != nil {
		t.Skipf("mkfifo: %v", err)
	}
	done := make(chan error, 1)
	go func() {
		s, err := New(Config{SnapshotPath: path, Shards: 1})
		if err == nil {
			err = s.Close()
		}
		done <- err
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second): // guard only; New returns at once
		t.Fatal("New blocked on a named pipe at the snapshot path")
	}
}

// T-33: temp files left by a crashed write hold cache contents with 0600 and
// are removed by the next New, unless a live writer may still own them.
func TestStaleSnapshotTempFilesRemoved(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "snap")
	old := filepath.Join(dir, ".weir-snap-old")
	fresh := filepath.Join(dir, ".weir-snap-fresh")
	other := filepath.Join(dir, "keep.txt")
	for _, p := range []string{old, fresh, other} {
		if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	past := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(old, past, past); err != nil {
		t.Fatal(err)
	}
	s, err := New(Config{SnapshotPath: path, Shards: 1})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close() //nolint:errcheck // nothing to write
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Errorf("stale temp file survived: %v", err)
	}
	for _, p := range []string{fresh, other} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("%s was removed: %v", filepath.Base(p), err)
		}
	}
}
