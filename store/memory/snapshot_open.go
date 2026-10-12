//go:build !js && !wasip1

package memory

import (
	"os"
	"syscall"
)

// openSnapshot opens the snapshot for reading. O_NONBLOCK: opening a named
// pipe planted at the path would otherwise block New forever (T-33).
func openSnapshot(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0) //nolint:gosec // G304: the operator's Config.SnapshotPath
}
