// The comparison harness of docs/11-comparison-spec.md. It has its own module
// so it stays out of the root module's zip, and it imports only the standard
// library: every cache under test, Weir included, is reached over HTTP.
module github.com/AshwinSathian/weir/bench/compare

go 1.26.0
