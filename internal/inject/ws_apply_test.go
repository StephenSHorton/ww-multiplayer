package inject

import (
	"math"
	"os"
	"regexp"
	"strconv"
	"testing"
)

const widescreenC = "../../inject/src/ws/widescreen.c"

// wsApplyRow is one row of widescreen.c's ws_apply_tbl.
type wsApplyRow struct {
	addr  uint32
	s16   bool
	shift bool // b multiplies ui_shift, else ui_factor
	a, b  float32
}

func wsApplyRows(t *testing.T) []wsApplyRow {
	t.Helper()
	src, err := os.ReadFile(widescreenC)
	if err != nil {
		t.Fatal(err)
	}
	tbl := regexp.MustCompile(`(?s)ws_apply_tbl\[\] = \{(.*?)\n\};`).FindSubmatch(src)
	if tbl == nil {
		t.Fatal("widescreen.c has no ws_apply_tbl")
	}
	re := regexp.MustCompile(`\{ ((?:WSA_\w+ \| )*)0x([0-9A-F]{6}), (-?[0-9.]+)f, (-?[0-9.]+)f \}`)
	var rows []wsApplyRow
	for _, m := range re.FindAllSubmatch(tbl[1], -1) {
		addr, _ := strconv.ParseUint(string(m[2]), 16, 32)
		a, _ := strconv.ParseFloat(string(m[3]), 32)
		b, _ := strconv.ParseFloat(string(m[4]), 32)
		flags := string(m[1])
		rows = append(rows, wsApplyRow{
			addr: 0x80000000 | uint32(addr), a: float32(a), b: float32(b),
			s16:   regexp.MustCompile(`WSA_S16`).MatchString(flags),
			shift: regexp.MustCompile(`WSA_S \|`).MatchString(flags),
		})
	}
	return rows
}

// wsApplyValue is what ws_apply writes: a + b*X in f32 (the C may fuse
// the multiply-add; the tests below hold either way), s16 rows rounded
// half away from zero.
func wsApplyValue(r wsApplyRow, F, S float32) (f float32, s int16) {
	x := F
	if r.shift {
		x = S
	}
	v := r.a + r.b*x
	if v < 0 {
		return v, int16(v - 0.5)
	}
	return v, int16(v + 0.5)
}

// TestWsApplyVanillaAtOne: at F = 1 and S = 0 (modes 0 and 1, and boot)
// every ws_apply row writes back the vanilla word (docs/design/
// native-widescreen.md section 8 stage 2 item 1, stage 3 item 4; vanilla
// words from the DOL and the HIO constructors), so no restore is needed.
func TestWsApplyVanillaAtOne(t *testing.T) {
	vanillaF32 := map[uint32]uint32{
		0x803F89B8: 0xC1100000, // -9
		0x803F89BC: 0xC1A80000, // -21
		0x803F89C0: 0x4424C000, // 659
		0x803F89C4: 0x44030000, // 524
		0x803FB78C: 0xC1100000, // -9
		0x803FB77C: 0x3F83CCCD, // 1.0296875
	}
	vanillaS16 := map[uint32]int16{
		0x803E68E8: -180, // g_meter_mapHIO +0xC, the minimap hide target
		0x803E68E4: 35,   // +0x8, the minimap corner x
		0x803E68F0: 590,  // +0x14, the free-camera icon x
		0x803E6B06: 0,    // g_menuHIO +0x9A, the minigame timer icon x
	}
	rows := wsApplyRows(t)
	if len(rows) != len(vanillaF32)+len(vanillaS16) {
		t.Fatalf("ws_apply_tbl has %d rows, want %d", len(rows), len(vanillaF32)+len(vanillaS16))
	}
	for _, r := range rows {
		f, s := wsApplyValue(r, 1, 0)
		if r.s16 {
			want, ok := vanillaS16[r.addr]
			if !ok || s != want {
				t.Errorf("0x%08X s16: %d at F = 1, want %d (known %v)", r.addr, s, want, ok)
			}
			continue
		}
		want, ok := vanillaF32[r.addr]
		if !ok || math.Float32bits(f) != want {
			t.Errorf("0x%08X f32: 0x%08X at F = 1, want 0x%08X (known %v)", r.addr, math.Float32bits(f), want, ok)
		}
	}
}

// TestWsApplyAt16x9: at 16:9 (F = 4/3 from the factor word 0x3FAAAAAB,
// S = 329.5 * (F - 1) as ws_latch computes it) the s16 rows give the
// plan's -290 (hide target), -75, 700, -110 (stage 3), and the f32 rows
// stay within an ulp or two of the exact values.
func TestWsApplyAt16x9(t *testing.T) {
	F := math.Float32frombits(0x3FAAAAAB)
	S := float32(329.5) * (F - 1)
	wantS16 := map[uint32]int16{0x803E68E8: -290, 0x803E68E4: -75, 0x803E68F0: 700, 0x803E6B06: -110}
	exact := map[uint32]float64{
		0x803F89B8: 320.5 - 329.5*4/3, 0x803F89BC: 241 - 262*4.0/3, 0x803F89C0: 659 * 4.0 / 3,
		0x803F89C4: 524 * 4.0 / 3, 0x803FB78C: 320.5 - 329.5*4/3, 0x803FB77C: 1.0296875 * 4 / 3,
	}
	for _, r := range wsApplyRows(t) {
		f, s := wsApplyValue(r, F, S)
		if r.s16 {
			if s != wantS16[r.addr] {
				t.Errorf("0x%08X: %d at 16:9, want %d", r.addr, s, wantS16[r.addr])
			}
			continue
		}
		if d := math.Abs(float64(f)-exact[r.addr]) / math.Abs(exact[r.addr]); d > 3e-7 {
			t.Errorf("0x%08X: %v at 16:9, exact %v", r.addr, f, exact[r.addr])
		}
	}
}

// TestRelWsVtableOrder: the REL takes the cannon game's 2D widget vtable
// slots (rel/ws.c, plan H-12) in _prolog before it publishes the exec
// hook word, and gives them back first thing in _epilog, so no slot ever
// points into an unlinked module.
func TestRelWsVtableOrder(t *testing.T) {
	src, err := os.ReadFile("../../inject/rel/rel_main.c")
	if err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`(?s)void _prolog\(void\) \{[^}]*relws_link\(\);\s*WWREL_WORD\(WWREL_EXEC_HOOK_OFF\) = `).Match(src) {
		t.Error("rel_main.c _prolog does not call relws_link() right before publishing the exec hook word")
	}
	if !regexp.MustCompile(`void _epilog\(void\) \{\s*relws_unlink\(\);`).Match(src) {
		t.Error("rel_main.c _epilog does not call relws_unlink() first")
	}
}
