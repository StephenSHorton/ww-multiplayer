package enemysync

import (
	"strings"
	"testing"

	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/stage"
)

// TestKindMO2Legacy: the Moblin row is the addr.Mo2* constants the
// Moblin pipelines run on, and its walk layout is Layouts' MO2 entry.
func TestKindMO2Legacy(t *testing.T) {
	k := Kinds[KindMO2]
	if k.ID != KindMO2 || k.Name != "mo2" || k.Proc != ProcMO2 || ProcMO2 != addr.ProcMo2 {
		t.Errorf("MO2 row id/name/proc = %d/%q/0x%X", k.ID, k.Name, k.Proc)
	}
	if k.Arc != addr.Mo2Arc || k.BckFirst != addr.Mo2BckFirst || k.BckLast != addr.Mo2BckLast || k.BckCount() != addr.Mo2BckCount || k.BckWait != addr.Mo2BckWait {
		t.Errorf("MO2 row arc %q bck 0x%X..0x%X (%d) wait 0x%X", k.Arc, k.BckFirst, k.BckLast, k.BckCount(), k.BckWait)
	}
	if k.MorfOff != addr.Mo2OffMorf || k.IceOff != addr.Mo2OffIce || k.GateOff != addr.Mo2OffMode {
		t.Errorf("MO2 row morf/ice/gate = 0x%X/0x%X/0x%X", k.MorfOff, k.IceOff, k.GateOff)
	}
	legacy := Layouts[int16(stage.ProcMO2)]
	if got := KindLayouts[int16(stage.ProcMO2)]; got != legacy {
		t.Errorf("KindLayouts MO2 = %+v, want Layouts' %+v", got, legacy)
	}
	if len(Layouts) != 1 {
		t.Errorf("Layouts gained entries: %v", Layouts)
	}
	// The live gate is MirrorsMode's, and no params gate.
	for mode := range 256 {
		if got, want := k.LiveOK(LiveGate{Mode: uint8(mode)}), MirrorsMode(int32(mode)); got != want {
			t.Errorf("MO2 LiveOK(mode %d) = %v, MirrorsMode %v", mode, got, want)
		}
	}
	for _, p := range []uint32{0, 0xFF00FF01, 0xFF01FF01, 0x05, 0x0F, 0xFFFFFFFF} {
		if !k.ParamsOK(p) {
			t.Errorf("MO2 ParamsOK(0x%08X) = false, want no params gate", p)
		}
	}
}

// TestKindRows pins the BK and CC rows to the addr constants and the
// design's type_table.
func TestKindRows(t *testing.T) {
	for _, c := range []struct {
		id                 KindID
		name               string
		proc               uint16
		arc                string
		first, last, count int
		wait               int
		morf, ice, gate    uint32
	}{
		{KindBK, "bk", 0xBE, "Bk", 0x29, 0x53, 43, 0x51, 0x2BC, 0x1264, 0x2B4},
		{KindCC, "cc", 0xCF, "CC", 0x14, 0x22, 15, 0, 0x2B4, 0x81C, 0x2F0},
	} {
		k := Kinds[c.id]
		if k.ID != c.id || k.Name != c.name || k.Proc != c.proc || k.Arc != c.arc || k.BckFirst != c.first || k.BckLast != c.last ||
			k.BckCount() != c.count || k.BckWait != c.wait || k.MorfOff != c.morf || k.IceOff != c.ice || k.GateOff != c.gate {
			t.Errorf("%s row = %+v", c.name, k)
		}
		if KindOf(c.proc) != c.id || c.id.String() != c.name {
			t.Errorf("KindOf(0x%X) = %v", c.proc, KindOf(c.proc))
		}
	}
	if KindOf(addr.ProcMo2) != KindMO2 || KindOf(addr.ProcBoko) != KindNone || KindOf(0) != KindNone || KindNone.Row() != nil {
		t.Error("KindOf of a proc outside the table")
	}
	if uint16(stage.ProcBK) != addr.ProcBK || uint16(stage.ProcCC) != addr.ProcCC {
		t.Error("stage and addr procs differ")
	}
}

func TestParseKinds(t *testing.T) {
	for _, c := range []struct {
		in, want string
		mask     uint32
	}{
		{"", "mo2,bk+u,cc+u", 7},
		{"mo2", "mo2", 1},
		{"none", "none", 0},
		{"mo2,bk", "mo2,bk", 3},
		{" CC+u , mo2 ", "mo2,cc+u", 5},
		{"bk+u,cc", "bk+u,cc", 6},
	} {
		s, err := ParseKinds(c.in)
		if err != nil {
			t.Errorf("ParseKinds(%q): %v", c.in, err)
			continue
		}
		if s.String() != c.want || s.Mask() != c.mask {
			t.Errorf("ParseKinds(%q) = %s mask %d, want %s mask %d", c.in, s, s.Mask(), c.want, c.mask)
		}
	}
	for _, bad := range []string{"ki", "mo2,mo2", "bk+x", "mo2,,bk"} {
		if _, err := ParseKinds(bad); err == nil {
			t.Errorf("ParseKinds(%q) accepted", bad)
		}
	}
	s, _ := ParseKinds("bk+u")
	if !s.Has(KindBK) || !s.TakesUnkeyed(KindBK) || s.Has(KindMO2) || s.TakesUnkeyed(KindCC) || s.Has(KindNone) {
		t.Errorf("bk+u = %+v", s)
	}
	if a := AllKinds(); a.String() != "mo2+u,bk+u,cc+u" {
		t.Errorf("AllKinds = %s", a)
	}
	if !strings.Contains(func() string { _, err := ParseKinds("zz"); return err.Error() }(), "mo2, bk, cc") {
		t.Error("the unknown-kind error does not list the kinds")
	}
}

// TestStaticGates runs the gates on the params the enemy audit found
// live (design test_plan.unit): M_NewD2 room 0's torch Bokoblins
// 0xFFFF8784 (type 4), room 6's sleepers 0xFFFF0002 (type 2) and machete
// 0xFFFF9644 (type 4), A_nami's 0xFFFF1460 (type 0), a runner 0xFF00FF19
// (type 9), a Kargaroc rider 0xFFFF05 (type 5); M_Dai room 2's black
// ChuChus 0xFF460300 and red 0xFF460100.
func TestStaticGates(t *testing.T) {
	bk, cc := &Kinds[KindBK], &Kinds[KindCC]
	for _, c := range []struct {
		name             string
		k                *Kind
		params           uint32
		live             LiveGate
		paramsOK, static bool
	}{
		{"BK torch 0xFFFF8784", bk, 0xFFFF8784, LiveGate{Mode: 4}, true, true},
		{"BK sleeper 0xFFFF0002", bk, 0xFFFF0002, LiveGate{Mode: 2}, false, false},
		// A sleeper that woke: d_a_bk.cpp:3162 sets mType 0, the nibble
		// still refuses it.
		{"BK sleeper woke, mType 0", bk, 0xFFFF0002, LiveGate{Mode: 0}, false, false},
		{"BK machete 0xFFFF9644", bk, 0xFFFF9644, LiveGate{Mode: 4}, true, true},
		{"BK A_nami 0xFFFF1460", bk, 0xFFFF1460, LiveGate{Mode: 0}, true, true},
		{"BK runner 0xFF00FF19", bk, 0xFF00FF19, LiveGate{Mode: 9}, false, false},
		{"BK rider 0xFFFF05", bk, 0xFFFF05, LiveGate{Mode: 5}, false, false},
		{"BK type 1", bk, 0xFFFFFF01, LiveGate{Mode: 1}, true, true},
		// 0xB: create makes it type 4 with the bouen model.
		{"BK torch shield 0xB", bk, 0xFFFFFF0B, LiveGate{Mode: 4}, true, true},
		{"BK 0xB still 0xB live", bk, 0xFFFFFF0B, LiveGate{Mode: 0xB}, true, false},
		{"BK type 6 (home writer)", bk, 0xFFFFFF06, LiveGate{Mode: 6}, false, false},
		{"CC black 0xFF460300", cc, 0xFF460300, LiveGate{Mode: 0, Color: 3}, false, false},
		{"CC red 0xFF460100", cc, 0xFF460100, LiveGate{Mode: 0, Color: 1}, true, true},
		{"CC Homen green 0xFF000000", cc, 0xFF000000, LiveGate{}, true, true},
		{"CC Homen blue 0xFF000400", cc, 0xFF000400, LiveGate{Color: 4}, true, true},
		{"CC behaviour 0xFF reads 0", cc, 0xFF0002FF, LiveGate{Color: 2}, true, true},
		{"CC behaviour 2 refused", cc, 0xFF000002, LiveGate{Mode: 2}, false, false},
		{"CC behaviour 3 (ceiling)", cc, 0xFF000103, LiveGate{Mode: 3, Color: 1}, true, true},
		{"CC behaviour 4 refused", cc, 0xFF000004, LiveGate{Mode: 4}, false, false},
		{"CC colour 11 reads 1", cc, 0xFF000B00, LiveGate{Color: 1}, true, true},
		{"CC colour 13 reads 3 (black)", cc, 0xFF000D00, LiveGate{Color: 3}, false, false},
		{"CC colour 15 is the insta-kill", cc, 0xFF000F00, LiveGate{Color: 1, InstaKill: 1}, false, false},
		{"CC live m304 set", cc, 0xFF000100, LiveGate{Color: 1, InstaKill: 1}, true, false},
	} {
		if got := c.k.ParamsOK(c.params); got != c.paramsOK {
			t.Errorf("%s: ParamsOK = %v, want %v", c.name, got, c.paramsOK)
		}
		if got := c.k.StaticOK(c.params, c.live); got != c.static {
			t.Errorf("%s: StaticOK = %v, want %v", c.name, got, c.static)
		}
	}
	for _, c := range []struct {
		params uint32
		b, col uint8
		insta  bool
	}{
		{0xFF460300, 0, 3, false},
		{0xFF0002FF, 0, 2, false},
		{0xFF00FFFF, 0, 0, false},
		{0xFF000F00, 0, 1, true},
		{0xFF000E04, 4, 14, false}, // behaviour 4 skips the >= 10 rule
	} {
		b, col, insta := CcDecodeParams(c.params)
		if b != c.b || col != c.col || insta != c.insta {
			t.Errorf("CcDecodeParams(0x%08X) = %d %d %v, want %d %d %v", c.params, b, col, insta, c.b, c.col, c.insta)
		}
	}
}

// TestBckName: the generated name tables cover each kind's range.
func TestBckName(t *testing.T) {
	for _, c := range []struct {
		id   KindID
		idx  uint8
		want string
	}{
		{KindMO2, addr.Mo2BckFirst, "ADOTSUKI"}, {KindMO2, addr.Mo2BckWait, "WAIT"}, {KindMO2, addr.Mo2BckLast, "WALK"},
		{KindBK, addr.BkBckFirst, "BK_AOMUKE"}, {KindBK, addr.BkBckWait, "BK_WAIT"}, {KindBK, addr.BkBckLast, "BK_WALK2"},
		{KindCC, addr.CcBckFirst, "ATACK01"}, {KindCC, 0x1C, "HUSE_WALK"}, {KindCC, addr.CcBckLast, "TSTART02"},
		{KindBK, addr.BkBckFirst - 1, "-"}, {KindCC, addr.CcBckLast + 1, "-"}, {KindBK, 0xFF, "-"}, {KindNone, 0x30, "-"},
	} {
		if got := BckName(c.id, c.idx); got != c.want {
			t.Errorf("BckName(%s, 0x%02X) = %q, want %q", c.id, c.idx, got, c.want)
		}
	}
	for _, k := range Kinds {
		for i := k.BckFirst; i <= k.BckLast; i++ {
			if BckName(k.ID, uint8(i)) == "-" {
				t.Errorf("%s BCK 0x%02X has no name", k.Name, i)
			}
		}
	}
}
