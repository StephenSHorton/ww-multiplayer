package log

import (
	"fmt"
	"testing"
)

type recorder struct {
	name  string
	lines *[]string
}

func (r recorder) Log(level Level, msg string) {
	*r.lines = append(*r.lines, fmt.Sprintf("%s:%d:%s", r.name, level, msg))
}

// TestMulti: every non-nil reporter gets every line, in argument order; nil
// entries are skipped, and none at all discards.
func TestMulti(t *testing.T) {
	var lines []string
	a, b := recorder{"a", &lines}, recorder{"b", &lines}

	Multi(a, nil, b).Log(Warn, "x")
	Logf(Multi(nil, a), Err, "n=%d", 2)
	Multi().Log(Info, "dropped")
	Multi(nil, nil).Log(Info, "dropped")

	got := fmt.Sprint(lines)
	if want := "[a:2:x b:2:x a:3:n=2]"; got != want {
		t.Errorf("lines = %s, want %s", got, want)
	}
	if _, ok := Multi(a).(recorder); !ok {
		t.Error("Multi of one reporter should return it unwrapped")
	}
}

// TestPrefix: the label and one space go in front of every line; the level
// passes through.
func TestPrefix(t *testing.T) {
	var lines []string
	p := Prefix(recorder{"a", &lines}, "[server]")
	p.Log(Warn, "up")
	p.Log(Info, "")
	want := []string{"a:2:[server] up", "a:0:[server] "}
	if fmt.Sprint(lines) != fmt.Sprint(want) {
		t.Errorf("lines = %q, want %q", lines, want)
	}
}
