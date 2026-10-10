package weircaddy

import (
	"crypto/sha256"
	"encoding/binary"
	"net/http"
	"slices"

	"github.com/AshwinSathian/weir"
)

// keyGenHash digests the settings that change what an unchanged key means:
// Forward.Mode, Forward.Allow and Storable.StripSetCookie (08 §3, amended by
// P2-00). A reload that changes it writes a hard epoch, because an entry
// stored under the looser setting may hold personalized content (R-3).
// Settings that only change which key a request maps to, and host lists,
// are left out on purpose, so adding a domain never purges the cache.
func keyGenHash(cfg weir.Config) [sha256.Size]byte {
	allow := make([]string, 0, len(cfg.Forward.Allow))
	for _, n := range cfg.Forward.Allow {
		allow = append(allow, http.CanonicalHeaderKey(n))
	}
	slices.Sort(allow)
	allow = slices.Compact(allow)

	h := sha256.New()
	h.Write([]byte("weir/caddy/keygen/v1\x00"))
	h.Write([]byte{byte(cfg.Forward.Mode)})
	var n [binary.MaxVarintLen64]byte
	h.Write(n[:binary.PutUvarint(n[:], uint64(len(allow)))])
	for _, name := range allow {
		// Length prefix: ["X-Ab"] and ["X-A", "b"] must not collide.
		h.Write(n[:binary.PutUvarint(n[:], uint64(len(name)))])
		h.Write([]byte(name))
	}
	if cfg.Storable.StripSetCookie {
		h.Write([]byte{1})
	} else {
		h.Write([]byte{0})
	}
	var out [sha256.Size]byte
	h.Sum(out[:0])
	return out
}
