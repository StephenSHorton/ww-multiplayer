package inject_test

// Native widescreen stage 4, the THP movies (docs/design/native-widescreen.md
// T-9): the module-415 .bss offsets src/ws/widescreen.c writes, proven from
// the vanilla disc's loose REL (not only from the decomp's symbols.txt).
// An external test package: wwapi/rel imports wwapi/stage, which imports
// this package.

import (
	"encoding/binary"
	"os"
	"regexp"
	"strconv"
	"testing"

	"github.com/StephenSHorton/ww-multiplayer/internal/vanillatest"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/rel"
)

const wsSource = "../../inject/src/ws/widescreen.c"

// wsDefines reads widescreen.c's `#define NAME 0x... | decimal` lines.
func wsDefines(t *testing.T) map[string]uint32 {
	t.Helper()
	b, err := os.ReadFile(wsSource)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]uint32{}
	for _, m := range regexp.MustCompile(`(?m)^#define\s+(\w+)\s+(0x[0-9A-Fa-f]+|\d+)\b`).FindAllSubmatch(b, -1) {
		v, err := strconv.ParseUint(string(m[2]), 0, 32)
		if err == nil {
			out[string(m[1])] = uint32(v)
		}
	}
	return out
}

// The THP step's constants (the disc test below proves them).
var wsTHPWant = map[string]uint32{
	"OS_MODULE_LIST": 0x800030C8,
	"MP_MODULE_ID":   0x19F,
	"MP_BSS_SIZE":    0xCB5C8,
	"MP_VIDEO_X":     0xCB580,
	"MP_DRAW_X":      0xCB59C,
	"THP_W":          640,
	"THP_H":          480,
}

// TestWsTHPDefines pins the THP step's constants in widescreen.c (no disc
// needed).
func TestWsTHPDefines(t *testing.T) {
	got := wsDefines(t)
	for n, v := range wsTHPWant {
		if g, ok := got[n]; !ok || g != v {
			t.Errorf("widescreen.c #define %s = 0x%X (present %v), want 0x%X", n, g, ok, v)
		}
	}
}

// TestWsTHPVanillaREL: on the vanilla disc, rels/d_a_movie_player.rel is
// module 0x19F; its .bss is the only section of size MP_BSS_SIZE (so the C
// can find it by that size through OSLink's bssSection); the draw
// (daMP_ActivePlayer_Draw, text 0x61DC..0x6230) loads DrawPosX from .bss +
// MP_DRAW_X and the polygon's width and height from .bss + MP_VIDEO_X and
// +4; the init (daMP_ActivePlayer_Init) stores DrawPosX from that xSize
// off r30 = .bss + 0x20 + 0xD0000. Both movies are 640x480. Skips without
// a vanilla disc.
func TestWsTHPVanillaREL(t *testing.T) {
	f, err := rel.Parse(vanillatest.ReadFile(t, "rels/d_a_movie_player.rel"))
	if err != nil {
		t.Fatal(err)
	}
	want := wsTHPWant
	if f.ID != want["MP_MODULE_ID"] {
		t.Fatalf("module id 0x%X, want 0x%X", f.ID, want["MP_MODULE_ID"])
	}
	bss := -1
	for i, s := range f.Sections {
		if s.Size == want["MP_BSS_SIZE"] {
			if bss >= 0 {
				t.Errorf("sections %d and %d are both 0x%X bytes", bss, i, s.Size)
			}
			bss = i
		}
	}
	if bss < 0 || !f.Sections[bss].BSS() || f.BSSSize != want["MP_BSS_SIZE"] {
		t.Fatalf("no .bss of 0x%X (header bss size 0x%X, section %d)", want["MP_BSS_SIZE"], f.BSSSize, bss)
	}
	if want["MP_VIDEO_X"]+8 > f.BSSSize || want["MP_DRAW_X"]+4 > f.BSSSize {
		t.Fatal("the offsets run past the .bss")
	}

	// The self-import's relocations into .bss, by patched text offset.
	relocs := map[uint32]rel.Reloc{}
	for _, imp := range f.Imports {
		if imp.Module != f.ID {
			continue
		}
		for _, r := range imp.Relocs {
			if r.Section == 1 && int(r.TargetSection) == bss {
				relocs[r.Offset] = r
			}
		}
	}
	text := f.Sections[1].Data
	word := func(off uint32) uint32 { return binary.BigEndian.Uint32(text[off:]) }
	for _, c := range []struct {
		lis, addi uint32 // the instructions the HA/LO pair patches
		addend    uint32
		loads     map[uint32]uint32 // the loads through the register that follow
	}{
		{0x61F0, 0x61F4, want["MP_DRAW_X"], map[uint32]uint32{0x61F8: 0x80840000}},                      // lwz r4,0(r4): x
		{0x6208, 0x620C, want["MP_VIDEO_X"], map[uint32]uint32{0x6210: 0x80C70000, 0x6214: 0x80E70004}}, // lwz r6,0(r7), lwz r7,4(r7): w, h
	} {
		ha, okH := relocs[c.lis+2]
		lo, okL := relocs[c.addi+2]
		if !okH || !okL || ha.Type != rel.RelocAddr16Ha || lo.Type != rel.RelocAddr16Lo || ha.Addend != c.addend || lo.Addend != c.addend {
			t.Errorf("0x%X/0x%X: no ADDR16_HA/LO pair to .bss+0x%X (%+v %+v)", c.lis, c.addi, c.addend, ha, lo)
		}
		for off, w := range c.loads {
			if got := word(off); got != w {
				t.Errorf("draw 0x%X: 0x%08X, want 0x%08X", off, got, w)
			}
		}
	}
	if r, ok := relocs[0x5FB6]; !ok || r.Addend != 0x20 || word(0x5FBC) != 0x3FC3000D {
		t.Errorf("init: r30 is not .bss+0x20+0xD0000 (%+v, 0x%08X)", r, word(0x5FBC))
	}
	r30 := uint32(0x20 + 0xD0000)
	for off, w := range map[uint32]uint32{
		0x6014: 0x801E0000 | (want["MP_VIDEO_X"]-r30)&0xFFFF, // lwz r0,xSize(r30)
		0x6020: 0x901E0000 | (want["MP_DRAW_X"]-r30)&0xFFFF,  // stw r0,DrawPosX(r30)
	} {
		if got := word(off); got != w {
			t.Errorf("init 0x%X: 0x%08X, want 0x%08X", off, got, w)
		}
	}

	d := vanillatest.Open(t)
	for _, p := range []string{"thpdemo/title_loop.thp", "thpdemo/end_st_epilogue.thp"} {
		r, err := d.Open(p)
		if err != nil {
			t.Fatal(err)
		}
		h := make([]byte, 0x24)
		if _, err := r.ReadAt(h, 0); err != nil {
			t.Fatal(err)
		}
		c := make([]byte, 0x1C)
		if _, err := r.ReadAt(c, int64(binary.BigEndian.Uint32(h[0x20:]))); err != nil {
			t.Fatal(err)
		}
		// THPFrameCompInfo: numComponents, frameComp[16] (0 = video), then
		// THPVideoInfo xSize, ySize.
		if string(h[:4]) != "THP\x00" || c[4] != 0 {
			t.Errorf("%s: not a THP with video first", p)
			continue
		}
		if x, y := binary.BigEndian.Uint32(c[0x14:]), binary.BigEndian.Uint32(c[0x18:]); x != want["THP_W"] || y != want["THP_H"] {
			t.Errorf("%s: %dx%d, want %dx%d", p, x, y, want["THP_W"], want["THP_H"])
		}
	}
}
