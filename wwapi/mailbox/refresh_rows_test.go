package mailbox

import (
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// cRefreshRows parses inject/rel/refresh.c's rf_rows initialiser (every
// entry, the #if'd door rows too).
func cRefreshRows(t *testing.T) []RefreshRow {
	t.Helper()
	src, err := os.ReadFile("../../inject/rel/refresh.c")
	if err != nil {
		t.Fatal(err)
	}
	s := string(src)
	i := strings.Index(s, "rf_rows[] = {")
	if i < 0 {
		t.Fatal("refresh.c: no rf_rows[] = {")
	}
	s = s[i+len("rf_rows[] = {"):]
	j := strings.Index(s, "\n};")
	if j < 0 {
		t.Fatal("refresh.c: rf_rows has no end")
	}
	var b strings.Builder
	for _, ln := range strings.Split(s[:j], "\n") {
		if k := strings.Index(ln, "//"); k >= 0 {
			ln = ln[:k]
		}
		if strings.HasPrefix(strings.TrimSpace(ln), "#") {
			continue
		}
		b.WriteString(ln + " ")
	}
	names := map[string]uint32{
		"WWRF_PROC_WALL": uint32(RefreshProcWall), "WWRF_PROC_FLOOR": uint32(RefreshProcFloor),
		"WWRF_PROC_ICE": uint32(RefreshProcIce), "WWRF_PROC_MJDOOR": uint32(RefreshProcMjdoor),
		"WWRF_PROC_MKIE": uint32(RefreshProcMkie), "WWRF_PROC_MKIEK": uint32(RefreshProcMkiek),
		"WWRF_PROC_MKNJD": uint32(RefreshProcMknjd), "WWRF_PROC_VFAN": uint32(RefreshProcVfan),
		"WWRF_PROC_SWHIT0": uint32(RefreshProcSwhit0), "WWRF_PROC_WARPT": uint32(RefreshProcWarpt),
		"WWRF_PROC_MFLFT": uint32(RefreshProcMflft), "WWRF_PROC_KOKIIE": uint32(RefreshProcKokiie),
		"WWRF_PROC_VMC": uint32(RefreshProcVmc), "WWRF_PROC_DOOR10": uint32(RefreshProcDoor10),
		"WWRF_PROC_DOOR12":   uint32(RefreshProcDoor12),
		"WWRF_MKIE_SW_SHIFT": RefreshMkieSwShift, "WWRF_VMC_SW_SHIFT": RefreshVmcSwShift,
		"WWRF_MFLFT_SW_SHIFT": RefreshMflftSwShift, "WWRF_KOKIIE_SW_SHIFT": RefreshKokiieSwShift,
		"RF_ROW_RECREATE": uint32(RefreshRowRecreate), "RF_ROW_ANGLE_X": uint32(RefreshRowAngleX),
		"RF_ROW_CUR_ROOM": uint32(RefreshRowCurRoom), "RF_ROW_NO_ZONE": uint32(RefreshRowNoZone),
		"RF_ROW_MEM_ONLY": uint32(RefreshRowMemOnly), "RF_ROW_NEED32": uint32(RefreshRowNeed32),
		"RF_ROW_KIDS": uint32(RefreshRowKids),
	}
	rv := regexp.MustCompile(`^RF_V\((\d+)\)$`)
	eval := func(e string) uint32 {
		var v uint32
		for _, term := range strings.Split(e, "|") {
			term = strings.TrimSpace(term)
			if m := rv.FindStringSubmatch(term); m != nil {
				n, _ := strconv.Atoi(m[1])
				v |= 1 << n
				continue
			}
			if n, ok := names[term]; ok {
				v |= n
				continue
			}
			n, err := strconv.ParseUint(term, 0, 32)
			if err != nil {
				t.Fatalf("refresh.c rf_rows: unknown term %q", term)
			}
			v |= uint32(n)
		}
		return v
	}
	var rows []RefreshRow
	for _, m := range regexp.MustCompile(`\{([^{}]*)\}`).FindAllStringSubmatch(b.String(), -1) {
		f := strings.Split(m[1], ",")
		if len(f) != 9 {
			t.Fatalf("refresh.c rf_rows: entry %q has %d fields, want 9", m[1], len(f))
		}
		rows = append(rows, RefreshRow{
			Proc: uint16(eval(f[0])), StOff: uint16(eval(f[1])), NeedOff: uint16(eval(f[2])),
			Shift: uint8(eval(f[3])), Flags: uint8(eval(f[4])), StW: uint8(eval(f[5])),
			Far10: uint8(eval(f[6])), Idle: eval(f[7]), Done: eval(f[8]),
		})
	}
	return rows
}

// TestRefreshRowsMatchC: RefreshRows is refresh.c's rf_rows, row for row.
func TestRefreshRowsMatchC(t *testing.T) {
	c := cRefreshRows(t)
	if len(c) != len(RefreshRows) {
		t.Fatalf("refresh.c has %d rows, RefreshRows %d", len(c), len(RefreshRows))
	}
	for i := range c {
		if c[i] != RefreshRows[i] {
			t.Errorf("row %d (%s): C %+v, Go %+v", i, RefreshProcName(c[i].Proc), c[i], RefreshRows[i])
		}
		if w := c[i].StW; w != 1 && w != 2 && w != 4 {
			t.Errorf("row %d (%s): width %d", i, RefreshProcName(c[i].Proc), w)
		}
		if c[i].Idle&c[i].Done != 0 {
			t.Errorf("row %d (%s): idle and done overlap", i, RefreshProcName(c[i].Proc))
		}
	}
}

func TestRefreshRowStateClass(t *testing.T) {
	r := RefreshRowOf(RefreshProcVmc)
	for v, want := range map[uint32]string{0: "idle", 1: "busy", 2: "busy", 3: "done", 0xFFFFFFFF: "busy"} {
		if got := r.StateClass(v); got != want {
			t.Errorf("vmc state %d: %s, want %s", v, got, want)
		}
	}
	if got := RefreshRowOf(RefreshProcMknjd).StateClass(8); got != "idle" {
		t.Errorf("mknjd 8: %s, want idle", got)
	}
	if RefreshRowOf(RefreshProcTbox) != nil {
		t.Error("tbox has no table row")
	}
}
