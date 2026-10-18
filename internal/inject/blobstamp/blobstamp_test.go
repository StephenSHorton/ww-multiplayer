package blobstamp

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestBlobFresh fails when the C inputs or blob.go changed since the last
// regen (see the package doc for what this can and cannot prove).
func TestBlobFresh(t *testing.T) {
	root, err := Root(".")
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(StampPath)))
	if err != nil {
		t.Fatal(err)
	}
	want, err := Parse(b)
	if err != nil {
		t.Fatal(err)
	}
	have, err := Current(root)
	if err != nil {
		t.Fatal(err)
	}
	if d := Diff(want, have); len(d) > 0 {
		t.Fatalf("internal/inject/blob.go is stale:\n  %s\n"+
			"Rebuild the C side (inject/build.py), then run scripts/extract_blob.py: it rewrites blob.go and the stamp.\n"+
			"A change that cannot alter the PPC bytes (comments only) still needs that rebuild, to confirm blob.go's payload is unchanged.",
			strings.Join(d, "\n  "))
	}
	if len(have.Inputs) < 4 {
		t.Errorf("only %d C inputs found: the walk is missing files", len(have.Inputs))
	}
}

// TestStampRoundTrip: Format and Parse agree, and Diff names each kind of
// staleness.
func TestStampRoundTrip(t *testing.T) {
	s := Stamp{Blob: "aa", Rel: "cc", Inputs: map[string]string{"inject/build.py": "01", "inject/src/x.c": "02"}}
	got, err := Parse(s.Format())
	if err != nil {
		t.Fatal(err)
	}
	if d := Diff(s, got); len(d) != 0 {
		t.Fatalf("round trip differs: %v", d)
	}
	have := Stamp{Blob: "bb", Rel: "dd", Inputs: map[string]string{"inject/build.py": "01", "inject/src/x.c": "03", "inject/src/y.c": "04"}}
	d := strings.Join(Diff(s, have), "\n")
	for _, w := range []string{"changed since the last regen: inject/src/x.c", "new C input since the last regen: inject/src/y.c", "payload", "REL module"} {
		if !strings.Contains(d, w) {
			t.Errorf("Diff misses %q:\n%s", w, d)
		}
	}
	delete(have.Inputs, "inject/build.py")
	if !strings.Contains(strings.Join(Diff(s, have), "\n"), "removed since the last regen: inject/build.py") {
		t.Error("Diff misses a removed input")
	}
	for _, bad := range []string{"", "blob aa\n", "input 01 a\n", "blob aa\ninput 01 a\ninput 02 a\n", "blob aa\nwhat\n"} {
		if _, err := Parse([]byte(bad)); err == nil {
			t.Errorf("Parse(%q) accepted", bad)
		}
	}
}

// TestCRLFIsNotAChange: a CRLF checkout hashes like the LF repo, and only
// source files count as inputs.
func TestCRLFIsNotAChange(t *testing.T) {
	dir := t.TempDir()
	write := func(eol string) Stamp {
		for _, f := range []string{"go.mod", "inject/build.py", "inject/src/a.c", "inject/include/a.h", "inject/src/notes.txt",
			"inject/rel/r.ld", "inject/rel/README.md"} {
			p := filepath.Join(dir, filepath.FromSlash(f))
			if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(p, []byte("line one"+eol+"line two"+eol), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		s, err := Current(dir)
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	lf, crlf := write("\n"), write("\r\n")
	if d := Diff(lf, crlf); len(d) != 0 {
		t.Errorf("CRLF reads as a change: %v", d)
	}
	if len(lf.Inputs) != 4 {
		t.Errorf("inputs %v: want build.py, a.c, a.h and r.ld only", lf.Inputs)
	}
	// A tree from before the REL module has no inject/rel: that is not an
	// error, just no inputs from it.
	if err := os.RemoveAll(filepath.Join(dir, "inject", "rel")); err != nil {
		t.Fatal(err)
	}
	old, err := Current(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(old.Inputs) != 3 {
		t.Errorf("inputs without inject/rel %v: want build.py, a.c and a.h", old.Inputs)
	}
	if d := strings.Join(Diff(lf, old), "\n"); !strings.Contains(d, "removed since the last regen: inject/rel/r.ld") {
		t.Errorf("Diff misses the removed REL input:\n%s", d)
	}
}
