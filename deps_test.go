package weir

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"testing"
)

// NFR-6, P7: the root module imports only the standard library and itself,
// test-only imports included.
func TestNoThirdPartyImports(t *testing.T) {
	cmd := exec.CommandContext(t.Context(), "go", "list", "-deps", "-test", "-json=ImportPath,Standard,Module", "./...")
	// Workspace mode would pull in the sibling modules and their legitimate dependencies.
	cmd.Env = append(os.Environ(), "GOWORK=off")
	out, err := cmd.Output()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			t.Fatalf("go list: %v\n%s", err, ee.Stderr)
		}
		t.Fatalf("go list: %v", err)
	}
	const self = "github.com/AshwinSathian/weir"
	dec := json.NewDecoder(bytes.NewReader(out))
	for {
		var p struct {
			ImportPath string
			Standard   bool
			Module     *struct{ Path string }
		}
		if err := dec.Decode(&p); errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			t.Fatalf("decode go list output: %v", err)
		}
		if p.Standard || (p.Module != nil && p.Module.Path == self) {
			continue
		}
		t.Errorf("third-party dependency %q", p.ImportPath)
	}
}
