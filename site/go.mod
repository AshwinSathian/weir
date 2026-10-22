// This file keeps site/ out of the Go module github.com/AshwinSathian/weir.
// The go command leaves a directory with its own go.mod out of the parent
// module's zip, so the website's sources are not downloaded by `go get`.
// There is no Go code here, nothing imports this path, and it is never tagged.
module github.com/AshwinSathian/weir/site

go 1.26.0
