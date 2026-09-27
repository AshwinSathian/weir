package keys

import (
	"crypto/sha256"

	"github.com/AshwinSathian/weir/store"
)

// tagVersion prefixes every tag encoding (04 §2).
const tagVersion = "weir/tag/v1"

// Tag kind bytes (04 §2).
const (
	tagKindOrigin = 0x01
	tagKindURI    = 0x02
	tagKindGroup  = 0x03
)

// TagGlobal returns the tag every stored entry carries, used by a purge
// of everything. Its kind byte is 0x00.
func TagGlobal() store.Tag {
	return sha256.Sum256([]byte(tagVersion + "\x00global"))
}

// TagOrigin returns the tag of origin o, which is scheme://host[:port]
// after host normalization.
func TagOrigin(o string) store.Tag {
	return tagOf(tagKindOrigin, o)
}

// TagURI returns the tag of path p and rewritten query q on origin o.
func TagURI(o, p, q string) store.Tag {
	return tagOf(tagKindURI, o, p, q)
}

// TagGroup returns the tag of purge group name on origin o.
func TagGroup(o, name string) store.Tag {
	return tagOf(tagKindGroup, o, name)
}

// tagOf hashes the version, kind and parts, each length-prefixed (T-3).
func tagOf(kind byte, parts ...string) store.Tag {
	bp := bufPool.Get().(*[]byte)
	*bp = append((*bp)[:0], tagVersion...)
	*bp = append(*bp, kind)
	for _, s := range parts {
		*bp = appendLenPrefixed(*bp, s)
	}
	t := store.Tag(sha256.Sum256(*bp))
	bufPool.Put(bp)
	return t
}
