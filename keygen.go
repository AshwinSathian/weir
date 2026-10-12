package weir

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"net/http"
	"slices"
)

// KeyGeneration returns a digest of the settings that change what an
// unchanged cache key means: Forward.Mode, Forward.Allow and
// Storable.StripSetCookie. An entry stored while one of them was looser may
// hold a response shaped by a header the key never covered, or a Set-Cookie,
// and stays reachable under the same key after the setting is tightened.
// Settings that only change which key a request maps to are left out, since
// entries under the old keys are no longer found.
//
// Use it wherever entries outlive a configuration: put it in the name of the
// memory store's snapshot file, so a restart with different forwarding rules
// starts from an empty cache instead of loading entries stored under the old
// ones (R-3), or in the key prefix of a remote store. The value is 64
// lowercase hex characters and is stable across releases for equal settings.
// The Caddy adapter keeps its own record and does this for you.
func (c Config) KeyGeneration() string {
	allow := make([]string, 0, len(c.Forward.Allow))
	for _, n := range c.Forward.Allow {
		allow = append(allow, http.CanonicalHeaderKey(n))
	}
	slices.Sort(allow)
	allow = slices.Compact(allow)

	h := sha256.New()
	h.Write([]byte("weir/keygen/v1\x00"))
	h.Write([]byte{byte(c.Forward.Mode)})
	var n [binary.MaxVarintLen64]byte
	h.Write(n[:binary.PutUvarint(n[:], uint64(len(allow)))])
	for _, name := range allow {
		// Length prefix: ["X-Ab"] and ["X-A", "b"] must not collide.
		h.Write(n[:binary.PutUvarint(n[:], uint64(len(name)))])
		h.Write([]byte(name))
	}
	if c.Storable.StripSetCookie {
		h.Write([]byte{1})
	} else {
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}
