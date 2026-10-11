package weir

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
	"testing"
)

// P3, CLAUDE.md rule 2: the origin is reached from exactly one place.
// (*Engine).fetch calls safeFetch, and safeFetch holds the only
// Origin.Fetch call. Every other path (foreground, background, warm,
// pass-through) goes through (*Engine).fetch.
func TestOriginReachedOnlyThroughFetch(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	var fetchCalls, safeCalls int
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, d := range f.Decls {
			fd, ok := d.(*ast.FuncDecl)
			if !ok || fd.Body == nil {
				continue
			}
			ast.Inspect(fd.Body, func(n ast.Node) bool {
				switch x := n.(type) {
				case *ast.SelectorExpr:
					// Any mention of .Fetch, a call or a method value
					// (f := o.Fetch), counts.
					if x.Sel.Name == "Fetch" {
						fetchCalls++
						if fd.Name.Name != "safeFetch" {
							t.Errorf("%s: .Fetch used in %s, want safeFetch", fset.Position(x.Pos()), fd.Name.Name)
						}
					}
				case *ast.CallExpr:
					if id, ok := x.Fun.(*ast.Ident); ok && id.Name == "safeFetch" {
						safeCalls++
						if fd.Name.Name != "fetch" {
							t.Errorf("%s: safeFetch called in %s, want (*Engine).fetch", fset.Position(x.Pos()), fd.Name.Name)
						}
					}
				}
				return true
			})
		}
	}
	if fetchCalls != 1 || safeCalls != 1 {
		t.Errorf("Origin.Fetch calls = %d, safeFetch calls = %d; want 1 and 1", fetchCalls, safeCalls)
	}
}
