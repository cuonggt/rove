package probe

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The read-only guarantee is structural, not a habit. internal/action holds
// everything that changes a host; if probe could import it, a single
// convenient call would quietly turn "probes never write" into "probes
// mostly do not write", and the linter next door would still pass because
// it only reads the scripts.
func TestProbeNeverImportsAction(t *testing.T) {
	fset := token.NewFileSet()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}

	var checked int
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") {
			continue
		}
		f, err := parser.ParseFile(fset, e.Name(), nil, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("%s: %v", e.Name(), err)
		}
		checked++
		for _, imp := range f.Imports {
			path := strings.Trim(imp.Path.Value, `"`)
			if strings.Contains(path, "internal/action") {
				t.Errorf("%s imports %s; probes must not reach the writing package", e.Name(), path)
			}
		}
	}
	if checked == 0 {
		t.Fatal("no source files were examined; this test would pass vacuously")
	}
}

// Every script shipped from this package must be listed in the read-only
// linter. A new script that nobody added is a new script nobody checked.
func TestEveryProbeScriptIsLinted(t *testing.T) {
	onDisk, err := filepath.Glob("*.sh")
	if err != nil {
		t.Fatal(err)
	}
	if len(onDisk) == 0 {
		t.Fatal("no scripts found")
	}

	linted := scripts()
	for _, path := range onDisk {
		if _, ok := linted[filepath.Base(path)]; !ok {
			t.Errorf("%s is shipped but not covered by the read-only linter", path)
		}
	}
}
