package main

import (
	"fmt"
	"sort"
	"strings"

	"github.com/StephenSHorton/ww-multiplayer/internal/inject"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/stage"
)

// BloNamesFromDisc lists every .blo name string in the disc's main.dol,
// in every REL of its RELS.arc and in every loose .rel: sorted, unique.
// It reads the disc and writes nothing. Only the file NAME strings come
// out, no game data.
func BloNamesFromDisc(path string) ([]string, error) {
	d, err := inject.OpenDisc(path)
	if err != nil {
		return nil, err
	}
	defer d.Close()
	set := map[string]bool{}
	dol, err := d.ReadDOL()
	if err != nil {
		return nil, err
	}
	for _, n := range BloStrings(dol) {
		set[n] = true
	}
	for _, f := range d.Files() {
		lower := strings.ToLower(f.Path)
		switch {
		case strings.HasSuffix(lower, ".rel"):
			b, err := d.ReadFile(f.Path)
			if err != nil {
				return nil, err
			}
			if stage.IsYaz0(b) {
				if b, err = stage.DecodeYaz0(b); err != nil {
					return nil, fmt.Errorf("%s: %w", f.Path, err)
				}
			}
			for _, n := range BloStrings(b) {
				set[n] = true
			}
		case lower == "rels.arc" || strings.HasSuffix(lower, "/rels.arc"):
			b, err := d.ReadFile(f.Path)
			if err != nil {
				return nil, err
			}
			arc, err := stage.ParseArchive(b)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", f.Path, err)
			}
			for _, af := range arc.Files() {
				rb, err := arc.Read(af)
				if err != nil {
					return nil, fmt.Errorf("%s/%s: %w", f.Path, af.Path, err)
				}
				for _, n := range BloStrings(rb) {
					set[n] = true
				}
			}
		}
	}
	out := make([]string, 0, len(set))
	for n := range set {
		out = append(out, n)
	}
	sort.Strings(out)
	return out, nil
}

// BloStrings returns each NUL-terminated run of printable ASCII in b that
// ends in ".blo" (any case): the whole run back to the previous NUL or
// non-printable byte, so a path prefix stays part of the name.
func BloStrings(b []byte) []string {
	var out []string
	start := 0
	for i, c := range b {
		if c >= 0x20 && c <= 0x7E {
			continue
		}
		if c == 0 && i-start > 4 && strings.EqualFold(string(b[i-4:i]), ".blo") {
			out = append(out, string(b[start:i]))
		}
		start = i + 1
	}
	return out
}
