//go:build unix

package weircaddy

import (
	"io/fs"
	"os"
	"syscall"
)

// ownedByUs reports whether the directory belongs to the effective user. A
// directory someone else created could be swapped under the snapshot even
// when its mode is tight.
func ownedByUs(fi fs.FileInfo) bool {
	st, ok := fi.Sys().(*syscall.Stat_t)
	return !ok || int(st.Uid) == os.Geteuid()
}
