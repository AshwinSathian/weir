// Package weir is a shared HTTP cache engine that sits between an HTTP server
// and an origin. It implements RFC 9111 caching and adds defenses against
// origin stampedes, origin and storage outages, and cache-key manipulation.
//
// The design is specified in docs/ at the repository root; start with
// docs/README.md. Implementation progress is tracked in docs/progress/.
package weir
