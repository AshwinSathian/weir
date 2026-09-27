// Package keys validates requests, normalizes the inputs that reach the
// cache key and rewrites the forwarded request to match (docs/04-lld.md §3).
// The key is a security boundary (P1): changes here answer the checklist in
// docs/06-threat-model.md §6.
package keys
