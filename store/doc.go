// Package store defines the storage contract the engine caches through: the
// Store interface, the Entry record, purge epochs and the optional capability
// interfaces. The contract is specified in docs/05-storage-interface-spec.md;
// the types follow docs/04-lld.md §2.
//
// This package imports only the standard library (NFR-6).
package store
