// Package storetest is the conformance suite for store.Store implementations
// (docs/05-storage-interface-spec.md section 8). Call Run from a test with a
// function that builds a fresh store; Run exercises each case against its own
// store. The package imports "testing", so import it from test files
// only.
package storetest
