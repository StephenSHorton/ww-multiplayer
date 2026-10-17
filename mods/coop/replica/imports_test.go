package replica

import (
	"go/parser"
	"go/token"
	"os"
	"strconv"
	"strings"
	"testing"
)

// TestNoHubImport pins the cut of Phase D D2a: the replica shares its
// vocabulary with the hub through package proto (Feature bits, policies,
// scoped filters) and never imports the hub itself, so a replica builds
// without the host's side. Every non-test file is read whatever its build
// constraints; only the tests that run a real hub (the converge and
// loopback ones) may import it.
func TestNoHubImport(t *testing.T) {
	const hub = "github.com/StephenSHorton/ww-multiplayer/mods/coop/hub"
	ents, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	n := 0
	for _, e := range ents {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, name, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatal(err)
		}
		n++
		for _, im := range f.Imports {
			p, err := strconv.Unquote(im.Path.Value)
			if err != nil {
				t.Fatal(err)
			}
			if p == hub || strings.HasPrefix(p, hub+"/") {
				t.Errorf("%s imports %s; use mods/coop/proto", name, p)
			}
		}
	}
	if n == 0 {
		t.Fatal("no non-test files read")
	}
}
