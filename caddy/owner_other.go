//go:build !unix

package weircaddy

import "io/fs"

// ownedByUs has no portable check off unix; the mode check still applies.
func ownedByUs(fs.FileInfo) bool { return true }
