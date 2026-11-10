package main

import (
	"encoding/binary"
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/StephenSHorton/ww-multiplayer/internal/inject"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/stage"
)

// GeomHitsPath is the committed list of every pane on the vanilla disc
// the walker's GEOM test matches (docs/design/native-widescreen.md 3.3):
// a childless PIC1 whose absolute box covers x <= 0..>= 640 and
// y <= 0..>= 480. One line per pane: "<file>.blo <tag> x0 y0 x1 y1",
// absolute. It decides whether GEOM can be one global rule (section 6,
// lever 1) instead of 22 rows: TestGeomGlobalRule checks that every
// screen in it is a GEOM screen of the plan or listed in geomAccepted.
// Written by `go run ./tools/gen_wstable -geom <disc>`; tests never read
// the disc.
const GeomHitsPath = "tools/gen_wstable/testdata/geom_hits.txt"

// GeomHit is one matching pane.
type GeomHit struct {
	File           string // the .blo file name, no folder
	Tag            uint32
	X0, Y0, X1, Y1 float64
}

func (h GeomHit) String() string {
	return fmt.Sprintf("%s %s %g %g %g %g", h.File, tagString(h.Tag), h.X0, h.Y0, h.X1, h.Y1)
}

// tagString is a tag as the .blo spells it (leading zero bytes dropped).
func tagString(t uint32) string {
	var b []byte
	for s := 24; s >= 0; s -= 8 {
		c := byte(t >> uint(s))
		if c == 0 && len(b) == 0 {
			continue
		}
		if c < 0x21 || c > 0x7E {
			return fmt.Sprintf("0x%08X", t)
		}
		b = append(b, c)
	}
	if len(b) == 0 {
		return "0x00000000"
	}
	return string(b)
}

// bloPane is one pane of a parsed screen.
type bloPane struct {
	magic    uint32
	tag      uint32
	x0, y0   float64 // mBounds, relative to the parent
	x1, y1   float64
	children []*bloPane
}

// ParseBlo reads a SCRN/blo1 screen the way J2DScreen::set does
// (J2DScreen.cpp makeHierarchyPanes, J2DPane.cpp makePaneStream): INF1,
// then panes, BGN1/END1 around each pane's children, EXT1 at the end.
// Every pane block starts with the J2DPane stream at +8.
func ParseBlo(b []byte) (*bloPane, error) {
	if len(b) < 0x20 || string(b[0:8]) != "SCRNblo1" {
		return nil, fmt.Errorf("not a SCRN blo1")
	}
	be := binary.BigEndian
	pos := 0x20
	root := &bloPane{}
	if pos+0x10 > len(b) || string(b[pos:pos+4]) != "INF1" {
		return nil, fmt.Errorf("no INF1 at 0x20")
	}
	root.x1 = float64(be.Uint16(b[pos+8:]))
	root.y1 = float64(be.Uint16(b[pos+10:]))
	pos += int(be.Uint32(b[pos+4:]))
	var walk func(parent *bloPane) (bool, error)
	walk = func(parent *bloPane) (bool, error) {
		last := parent
		for {
			if pos+8 > len(b) {
				return false, fmt.Errorf("truncated at 0x%X", pos)
			}
			magic := string(b[pos : pos+4])
			size := int(be.Uint32(b[pos+4:]))
			if size < 8 || pos+size > len(b) {
				return false, fmt.Errorf("block %q at 0x%X: bad size 0x%X", magic, pos, size)
			}
			switch magic {
			case "EXT1":
				return true, nil
			case "BGN1":
				pos += size
				end, err := walk(last)
				if err != nil || end {
					return end, err
				}
			case "END1":
				pos += size
				return false, nil
			default:
				if size < 8+16 {
					return false, fmt.Errorf("pane %q at 0x%X too short", magic, pos)
				}
				s := b[pos+8:]
				x0 := float64(int16(be.Uint16(s[8:])))
				y0 := float64(int16(be.Uint16(s[10:])))
				p := &bloPane{
					magic: be.Uint32(b[pos:]), tag: be.Uint32(s[4:]),
					x0: x0, y0: y0,
					x1: x0 + float64(int16(be.Uint16(s[12:]))),
					y1: y0 + float64(int16(be.Uint16(s[14:]))),
				}
				parent.children = append(parent.children, p)
				last = p
				pos += size
			}
		}
	}
	if _, err := walk(root); err != nil {
		return nil, err
	}
	return root, nil
}

// GeomHits lists a screen's panes that match the GEOM test, with
// absolute coordinates (each ancestor's mBounds.i added, as the walker
// does; the screen itself sits at 0,0).
func GeomHits(file string, root *bloPane) []GeomHit {
	const pic1 = 0x50494331 // 'PIC1'
	var out []GeomHit
	var walk func(p *bloPane, ax, ay float64)
	walk = func(p *bloPane, ax, ay float64) {
		for _, c := range p.children {
			x0, y0, x1, y1 := ax+c.x0, ay+c.y0, ax+c.x1, ay+c.y1
			if c.magic == pic1 && len(c.children) == 0 && x0 <= 0 && x1 >= 640 && y0 <= 0 && y1 >= 480 {
				out = append(out, GeomHit{File: file, Tag: c.tag, X0: x0, Y0: y0, X1: x1, Y1: y1})
			}
			walk(c, x0, y0)
		}
	}
	walk(root, 0, 0)
	return out
}

// GeomHitsFromDisc parses every .blo in every archive (and loose) on the
// disc and lists the GEOM matches, sorted and unique. It reads the disc
// and writes nothing; only names, tags and coordinates come out.
func GeomHitsFromDisc(discPath string) ([]string, int, error) {
	d, err := inject.OpenDisc(discPath)
	if err != nil {
		return nil, 0, err
	}
	defer d.Close()
	set := map[string]bool{}
	screens := 0
	add := func(name string, blo []byte) error {
		if stage.IsYaz0(blo) {
			var err error
			if blo, err = stage.DecodeYaz0(blo); err != nil {
				return fmt.Errorf("%s: %w", name, err)
			}
		}
		root, err := ParseBlo(blo)
		if err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
		screens++
		for _, h := range GeomHits(path.Base(name), root) {
			set[h.String()] = true
		}
		return nil
	}
	for _, f := range d.Files() {
		lower := strings.ToLower(f.Path)
		switch {
		case strings.HasSuffix(lower, ".blo"):
			b, err := d.ReadFile(f.Path)
			if err != nil {
				return nil, 0, err
			}
			if err := add(f.Path, b); err != nil {
				return nil, 0, err
			}
		case strings.HasSuffix(lower, ".arc"):
			b, err := d.ReadFile(f.Path)
			if err != nil {
				return nil, 0, err
			}
			if err := walkArc(f.Path, b, add); err != nil {
				return nil, 0, err
			}
		}
	}
	out := make([]string, 0, len(set))
	for s := range set {
		out = append(out, s)
	}
	sort.Strings(out)
	return out, screens, nil
}

// walkArc hands every .blo in a RARC archive to add, and walks the
// archives inside it (the file select's dat/file_select.arc sits in
// res/Stage/Name/Stage.arc). A file that is no RARC is skipped.
func walkArc(name string, b []byte, add func(string, []byte) error) error {
	arc, err := stage.ParseArchive(b)
	if err != nil {
		return nil
	}
	for _, af := range arc.Files() {
		lower := strings.ToLower(af.Path)
		if !strings.HasSuffix(lower, ".blo") && !strings.HasSuffix(lower, ".arc") {
			continue
		}
		ab, err := arc.Read(af)
		if err != nil {
			return fmt.Errorf("%s/%s: %w", name, af.Path, err)
		}
		if strings.HasSuffix(lower, ".arc") {
			err = walkArc(name+"/"+af.Path, ab, add)
		} else {
			err = add(af.Path, ab)
		}
		if err != nil {
			return err
		}
	}
	return nil
}
