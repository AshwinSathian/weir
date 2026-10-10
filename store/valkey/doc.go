// Package valkey is the Valkey-backed store.Store (Phase 2.5).
//
// It is a separate module so the root module keeps importing only the Go
// standard library (NFR-6). The design is in docs/05-storage-interface-spec.md
// section 7. Config and error mapping live in config.go and errors.go; Store,
// the lazy connect and the maxmemory-policy check live in store.go and
// client.go. Get, Set, Delete and epochs arrive with later cards.
package valkey
