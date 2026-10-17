package semver

import (
	"errors"
	"strings"
	"testing"
)

func TestParse(t *testing.T) {
	for _, c := range []struct {
		in, want string
	}{
		{"0.0.0", "0.0.0"},
		{"1.2.3", "1.2.3"},
		{"0.1.0-rc.1", "0.1.0-rc.1"},
		{"1.0.0+build.7", "1.0.0+build.7"},
		{"1.0.0-alpha-1.x+0001", "1.0.0-alpha-1.x+0001"},
		{"2147483647.0.0", "2147483647.0.0"},
	} {
		v, err := Parse(c.in)
		if err != nil {
			t.Errorf("Parse(%q): %v", c.in, err)
			continue
		}
		if v.String() != c.want {
			t.Errorf("Parse(%q) = %s, want %s", c.in, v, c.want)
		}
	}
	for _, bad := range []string{
		"", "1", "1.2", "1.2.3.4", "v1.2.3", "01.2.3", "1.02.3", "1.2.03",
		"1.2.3-", "1.2.3-01", "1.2.3-a..b", "1.2.3+", "1.2.3+a_b", "-1.2.3",
		"1.2.x", "a.b.c", " 1.2.3", "2147483648.0.0", "99999999999999999999.0.0",
		strings.Repeat("1", MaxLen+1),
	} {
		if _, err := Parse(bad); !errors.Is(err, ErrVersion) {
			t.Errorf("Parse(%q) err = %v, want ErrVersion", bad, err)
		}
	}
}

// TestCompare: the precedence example from semver.org 2.0.0 §11, plus
// build metadata being ignored.
func TestCompare(t *testing.T) {
	order := []string{
		"1.0.0-alpha", "1.0.0-alpha.1", "1.0.0-alpha.beta", "1.0.0-beta",
		"1.0.0-beta.2", "1.0.0-beta.11", "1.0.0-rc.1", "1.0.0", "1.0.1", "1.1.0", "2.0.0",
	}
	for i := range order {
		for j := range order {
			a, b := MustParse(order[i]), MustParse(order[j])
			want := sign(i - j)
			if got := a.Compare(b); got != want {
				t.Errorf("%s vs %s = %d, want %d", a, b, got, want)
			}
		}
	}
	if MustParse("1.0.0+a").Compare(MustParse("1.0.0+b")) != 0 {
		t.Error("build metadata changed precedence")
	}
}

func TestRange(t *testing.T) {
	for _, c := range []struct {
		r   string
		in  []string
		out []string
	}{
		{"*", []string{"0.0.0", "9.9.9"}, []string{"1.0.0-rc.1"}},
		{"x", []string{"1.2.3"}, nil},
		{">=*", []string{"1.2.3"}, nil},
		{"1.2.3", []string{"1.2.3", "1.2.3+b"}, []string{"1.2.4", "1.2.3-rc.1"}},
		{"=1.2.3", []string{"1.2.3"}, []string{"1.2.2"}},
		{"1.2", []string{"1.2.0", "1.2.99"}, []string{"1.3.0", "1.1.9"}},
		{"1.2.x", []string{"1.2.0", "1.2.99"}, []string{"1.3.0"}},
		{"1", []string{"1.0.0", "1.99.0"}, []string{"2.0.0", "0.9.9"}},
		{"1.x.x", []string{"1.5.0"}, []string{"2.0.0"}},
		{"^1.2.3", []string{"1.2.3", "1.9.0"}, []string{"1.2.2", "2.0.0", "2.0.0-rc.1"}},
		{"^1.0", []string{"1.0.0", "1.5.2"}, []string{"2.0.0", "0.9.0"}},
		{"^0.1", []string{"0.1.0", "0.1.9"}, []string{"0.2.0", "0.0.9"}},
		{"^0.1.2", []string{"0.1.2", "0.1.9"}, []string{"0.1.1", "0.2.0"}},
		{"^0.0.3", []string{"0.0.3"}, []string{"0.0.4", "0.0.2"}},
		{"^0.0", []string{"0.0.0", "0.0.9"}, []string{"0.1.0"}},
		{"^0", []string{"0.0.0", "0.9.0"}, []string{"1.0.0"}},
		{"^1.x", []string{"1.0.0", "1.9.9"}, []string{"2.0.0"}},
		{"~1.2.3", []string{"1.2.3", "1.2.9"}, []string{"1.3.0", "1.2.2"}},
		{"~1.2", []string{"1.2.0", "1.2.9"}, []string{"1.3.0"}},
		{"~1", []string{"1.0.0", "1.9.0"}, []string{"2.0.0"}},
		{"~0.1", []string{"0.1.5"}, []string{"0.2.0"}},
		{">1.2.3", []string{"1.2.4"}, []string{"1.2.3"}},
		{">1.2", []string{"1.3.0"}, []string{"1.2.9"}},
		{">1", []string{"2.0.0"}, []string{"1.9.9"}},
		{"<1.2", []string{"1.1.9"}, []string{"1.2.0"}},
		{"<=1.2", []string{"1.2.9"}, []string{"1.3.0"}},
		{"<=1.2.3", []string{"1.2.3"}, []string{"1.2.4"}},
		{">=1.2", []string{"1.2.0"}, []string{"1.1.9"}},
		{">=1.0.0 <2.0.0", []string{"1.0.0", "1.9.9"}, []string{"2.0.0", "0.9.9"}},
		{"<1.0.0 || >=2.0.0", []string{"0.5.0", "2.0.0"}, []string{"1.0.0", "1.5.0"}},
		{"^1.2.3-beta.2", []string{"1.2.3-beta.2", "1.2.3-beta.3", "1.2.3", "1.9.0"}, []string{"1.2.3-beta.1", "1.2.4-beta.9", "2.0.0"}},
		{">=1.0.0-rc.1 <1.0.0", []string{"1.0.0-rc.2"}, []string{"1.0.0", "0.9.0-rc.9"}},
		{"  ^1.0   ", []string{"1.0.0"}, nil},
	} {
		r, err := ParseRange(c.r)
		if err != nil {
			t.Errorf("ParseRange(%q): %v", c.r, err)
			continue
		}
		if r.String() != c.r {
			t.Errorf("String() = %q, want %q", r.String(), c.r)
		}
		for _, v := range c.in {
			if !r.Contains(MustParse(v)) {
				t.Errorf("%q should contain %s", c.r, v)
			}
		}
		for _, v := range c.out {
			if r.Contains(MustParse(v)) {
				t.Errorf("%q should not contain %s", c.r, v)
			}
		}
	}
}

func TestParseRangeErrors(t *testing.T) {
	for _, bad := range []string{
		"", "   ", "||", "^1.0 ||", "|| 1.0", "1.2 - 1.4", "^", ">", "~v1",
		"^1.x.y", "1.x.3", "^1.2.3.4", "^01.2", ">*", "<*", "<=x", ">x",
		"1.2-beta", "1.x-beta", "^1.0.0-", "1.2.3+", "=>1.0", "!1.0",
		"^1.0,^2.0", "^2147483648", strings.Repeat("1 ", MaxLen),
	} {
		if _, err := ParseRange(bad); !errors.Is(err, ErrRange) {
			t.Errorf("ParseRange(%q) err = %v, want ErrRange", bad, err)
		}
	}
	var zero Range
	if !zero.IsZero() || zero.Contains(Version{}) {
		t.Error("the zero Range must match nothing")
	}
}

// TestMaxPartBump: the largest part still has a representable upper bound.
func TestMaxPartBump(t *testing.T) {
	r := MustParseRange("^2147483647")
	if !r.Contains(MustParse("2147483647.5.0")) {
		t.Error("^MaxPart lost its own versions")
	}
}

func TestText(t *testing.T) {
	var v Version
	if err := v.UnmarshalText([]byte("1.2.3-rc.1")); err != nil || v.String() != "1.2.3-rc.1" {
		t.Fatalf("UnmarshalText: %v %v", v, err)
	}
	if err := v.UnmarshalText([]byte("nope")); err == nil {
		t.Fatal("UnmarshalText accepted nope")
	}
	var r Range
	if err := r.UnmarshalText([]byte("^0.1")); err != nil {
		t.Fatal(err)
	}
	if b, _ := r.MarshalText(); string(b) != "^0.1" {
		t.Fatalf("MarshalText = %s", b)
	}
	if err := r.UnmarshalText([]byte("^0.x.y")); err == nil {
		t.Fatal("UnmarshalText accepted a bad range")
	}
}

// FuzzParse: Parse never panics, and what it accepts round-trips through
// String and compares equal to itself.
func FuzzParse(f *testing.F) {
	for _, s := range []string{"1.2.3", "0.1.0-rc.1+b", "1.0.0-alpha.beta.1", "01.2.3", "1.2.3-0"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		v, err := Parse(s)
		if err != nil {
			return
		}
		w, err := Parse(v.String())
		if err != nil {
			t.Fatalf("String %q of %q does not parse: %v", v.String(), s, err)
		}
		if v.Compare(w) != 0 || w.String() != v.String() {
			t.Fatalf("%q did not round-trip: %s vs %s", s, v, w)
		}
	})
}

// FuzzParseRange: ParseRange never panics, and Contains never panics on a
// few probe versions for any range it accepts. A range keeps its text.
func FuzzParseRange(f *testing.F) {
	for _, s := range []string{"^0.1", "~1.2", ">=1.0.0 <2.0.0 || 3.x", "*", "^1.2.3-beta.2", ">1", "<=1.2"} {
		f.Add(s)
	}
	probes := []Version{{}, MustParse("0.1.0"), MustParse("1.2.3-rc.1"), MustParse("2147483647.2147483647.2147483647")}
	f.Fuzz(func(t *testing.T, s string) {
		r, err := ParseRange(s)
		if err != nil {
			return
		}
		if r.String() != s {
			t.Fatalf("String = %q, want %q", r.String(), s)
		}
		for _, p := range probes {
			r.Contains(p)
		}
	})
}
