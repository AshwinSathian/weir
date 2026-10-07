module github.com/AshwinSathian/weir/observe/prom

go 1.27.0

require (
	github.com/AshwinSathian/weir v0.0.0
	github.com/prometheus/client_golang v1.24.1
)

require (
	github.com/beorn7/perks v1.0.1 // indirect
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/kylelemons/godebug v1.1.0 // indirect
	github.com/munnerz/goautoneg v0.0.0-20191010083416-a7dc8b61c822 // indirect
	github.com/prometheus/client_model v0.6.2 // indirect
	github.com/prometheus/common v0.70.1 // indirect
	github.com/prometheus/procfs v0.21.1 // indirect
	golang.org/x/sys v0.47.0 // indirect
	google.golang.org/protobuf v1.36.11 // indirect
)

// ponytail: the root module has no release tag yet; once one exists, require it
// and drop this replace (a replace is ignored by importers of this module).
replace github.com/AshwinSathian/weir => ../..
