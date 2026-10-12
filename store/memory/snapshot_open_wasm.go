//go:build js || wasip1

package memory

import "os"

// openSnapshot opens the snapshot for reading. These targets have no
// O_NONBLOCK, so anything that is not a regular file is refused before the
// open instead (T-33).
// ponytail: a file swapped for a pipe between Stat and Open would still
// block. WASI has no named pipes; on js the host would have to plant one in
// a directory the embedder chose. Use a host open flag if that changes.
func openSnapshot(path string) (*os.File, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if !fi.Mode().IsRegular() {
		return nil, os.ErrInvalid
	}
	return os.Open(path) //nolint:gosec // G304: the operator's Config.SnapshotPath
}
