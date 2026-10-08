package weir_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// NFR-7: every exported identifier of the public packages has a doc comment
// that starts with its name. go vet and golangci-lint cleanliness is the CI
// gate itself.
func TestExportedIdentifiersDocumented(t *testing.T) {
	fset := token.NewFileSet()
	err := filepath.WalkDir(".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "testdata", "internal", "examples", "loadtest", "scripts", "docs", ".claude":
				return fs.SkipDir
			}
			// Other modules (observe/prom, caddy, store/valkey) are checked by their own make check.
			if path != "." {
				if _, err := os.Stat(filepath.Join(path, "go.mod")); err == nil {
					return fs.SkipDir
				}
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		f, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
		if err != nil {
			return err
		}
		checkFileDocs(t, fset, f)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func checkFileDocs(t *testing.T, fset *token.FileSet, f *ast.File) {
	t.Helper()
	bad := func(pos token.Pos, name string, doc *ast.CommentGroup) {
		if doc == nil || !strings.HasPrefix(strings.TrimSpace(doc.Text()), name) {
			t.Errorf("%s: exported %s has no doc comment starting with its name", fset.Position(pos), name)
		}
	}
	for _, decl := range f.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			if !d.Name.IsExported() {
				continue
			}
			if d.Recv != nil && !recvExported(d.Recv) {
				continue
			}
			bad(d.Pos(), d.Name.Name, d.Doc)
		case *ast.GenDecl:
			for _, spec := range d.Specs {
				switch s := spec.(type) {
				case *ast.TypeSpec:
					if s.Name.IsExported() {
						bad(s.Pos(), s.Name.Name, firstDoc(s.Doc, d.Doc))
					}
				case *ast.ValueSpec:
					// A grouped const or var block is documented by its group
					// comment or by each exported name.
					for _, n := range s.Names {
						if n.IsExported() && s.Doc == nil && d.Doc == nil {
							t.Errorf("%s: exported %s has no doc comment", fset.Position(n.Pos()), n.Name)
						}
					}
				}
			}
		}
	}
}

func firstDoc(a, b *ast.CommentGroup) *ast.CommentGroup {
	if a != nil {
		return a
	}
	return b
}

func recvExported(r *ast.FieldList) bool {
	if len(r.List) == 0 {
		return false
	}
	t := r.List[0].Type
	if s, ok := t.(*ast.StarExpr); ok {
		t = s.X
	}
	if ix, ok := t.(*ast.IndexExpr); ok {
		t = ix.X
	}
	id, ok := t.(*ast.Ident)
	return ok && id.IsExported()
}
