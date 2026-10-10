// Package valkey is the Valkey-backed store.Store (Phase 2.5).
//
// It is a separate module so the root module keeps importing only the Go
// standard library (NFR-6). The design is in docs/05-storage-interface-spec.md
// section 7. This file set holds the configuration and error mapping; the
// Store type arrives with card P25-01b.
package valkey
