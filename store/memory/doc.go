// Package memory is the default in-process store: sharded, byte-weighted
// S3-FIFO (ADR-6). The design is in docs/05-storage-interface-spec.md §5.
//
// This package imports only the standard library (NFR-6).
package memory
