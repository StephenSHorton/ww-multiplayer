package main

import (
	"slices"
	"testing"

	"github.com/StephenSHorton/ww-multiplayer/internal/playercolor"
)

func TestExtractColorFlag(t *testing.T) {
	red := playercolor.Palette()[1].Color
	orange := playercolor.Color{R: 0xFF, G: 0x80}
	cases := []struct {
		args []string
		want *playercolor.Color
		rest []string
	}{
		{[]string{"host", "Link"}, nil, []string{"host", "Link"}},
		{[]string{"host", "--color", "red", "Link"}, &red, []string{"host", "Link"}},
		{[]string{"host", "--color=#FF8000", "Link"}, &orange, []string{"host", "Link"}},
		{[]string{"-color", "red", "join", "1.2.3.4"}, &red, []string{"join", "1.2.3.4"}},
		{[]string{"host", "--color", "blue", "--color", "red"}, &red, []string{"host"}},
		// A bare "--" ends the options: a name after it stays a name.
		{[]string{"host", "--", "--color"}, nil, []string{"host", "--", "--color"}},
		{[]string{"host", "--port", "25566"}, nil, []string{"host", "--port", "25566"}},
	}
	for _, c := range cases {
		got, rest, err := extractColorFlag(c.args)
		if err != nil {
			t.Errorf("%v: %v", c.args, err)
			continue
		}
		if (got == nil) != (c.want == nil) || (got != nil && *got != *c.want) {
			t.Errorf("%v: color %v, want %v", c.args, got, c.want)
		}
		if !slices.Equal(rest, c.rest) {
			t.Errorf("%v: rest %q, want %q", c.args, rest, c.rest)
		}
	}
	for _, bad := range [][]string{{"host", "--color"}, {"--color=mauve"}, {"--color", ""}} {
		if _, _, err := extractColorFlag(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestInitColorPrefEnvAndFlag(t *testing.T) {
	blue := playercolor.Palette()[2].Color
	red := playercolor.Palette()[1].Color
	if p, _, err := initColorPref([]string{"host"}, "blue"); err != nil || p == nil || *p != blue {
		t.Errorf("WW_COLOR=blue -> %v, %v", p, err)
	}
	if p, _, err := initColorPref([]string{"host", "--color", "red"}, "blue"); err != nil || p == nil || *p != red {
		t.Errorf("--color should override WW_COLOR: %v, %v", p, err)
	}
	if _, _, err := initColorPref(nil, "nope"); err == nil {
		t.Error("a bad WW_COLOR was accepted")
	}
	if p, rest, err := initColorPref(nil, ""); err != nil || p != nil || len(rest) != 0 {
		t.Errorf("no preference -> %v %v %v", p, rest, err)
	}
}
