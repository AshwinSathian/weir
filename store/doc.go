// Package store defines the storage contract the engine caches through: the
// Store interface, the Entry record, purge epochs and the optional capability
// interfaces. The contract is specified in docs/05-storage-interface-spec.md;
// the types follow docs/04-lld.md §2.
//
// Construct the structs in this package with field names, not positionally:
// minor releases may add fields. The Store interface itself is fixed for v1;
// new abilities are added as optional capability interfaces that the engine
// finds with a type assertion.
//
// This package imports only the standard library.
package store
