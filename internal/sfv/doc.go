// Package sfv parses the HTTP structured fields (RFC 9651) that Weir reads
// from origin responses: the List of Strings carried by Cache-Groups and
// Cache-Group-Invalidation (docs/04-lld.md §8.5), and the Dictionary of
// targeted cache control fields such as CDN-Cache-Control (§13.2).
package sfv
