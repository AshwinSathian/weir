module github.com/AshwinSathian/weir/store/valkey

go 1.27.0

require (
	github.com/AshwinSathian/weir v0.0.0
	github.com/valkey-io/valkey-go v1.0.78
)

require golang.org/x/sys v0.47.0 // indirect

// ponytail: the root module has no release tag yet; once one exists, require it
// and drop this replace (a replace is ignored by importers of this module).
replace github.com/AshwinSathian/weir => ../..
