package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/StephenSHorton/ww-multiplayer/internal/vanillatest"
)

func root(t *testing.T) string {
	t.Helper()
	r, err := moduleRoot()
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func bloNames(t *testing.T) []string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root(t), filepath.FromSlash(NamesPath)))
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, l := range strings.Split(strings.ReplaceAll(string(b), "\r\n", "\n"), "\n") {
		if l != "" {
			out = append(out, l)
		}
	}
	return out
}

// The committed header is what the row list generates.
func TestHeaderFresh(t *testing.T) {
	want, err := Generate(tableRows())
	if err != nil {
		t.Fatal(err)
	}
	have, err := os.ReadFile(filepath.Join(root(t), filepath.FromSlash(HeaderPath)))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(bytes.ReplaceAll(have, []byte("\r\n"), []byte("\n")), want) {
		t.Fatalf("%s is stale: run go run ./tools/gen_wstable", HeaderPath)
	}
}

// No two .blo names of the vanilla DOL and RELs share a hash (names that
// differ only in case are one name to the hash and to JKRArchive), and
// every row names one of them exactly, so the hash matches at run time.
func TestNoCollisions(t *testing.T) {
	names := bloNames(t)
	if len(names) < 50 {
		t.Fatalf("%d names: %s looks truncated", len(names), NamesPath)
	}
	byHash := map[uint32]string{}
	for _, n := range names {
		h := NameHash(n)
		if prev, ok := byHash[h]; ok && !strings.EqualFold(prev, n) {
			t.Errorf("%s and %s share hash 0x%08X", prev, n, h)
		}
		byHash[h] = n
	}
	have := map[string]bool{}
	for _, n := range names {
		have[n] = true
	}
	for _, r := range tableRows() {
		if !have[r.name+".blo"] {
			t.Errorf("row %s.blo is not a .blo name string in the vanilla DOL or RELs", r.name)
		}
	}
	// The plan's anchors: ship_race2 (DOL only), the talk boxes (no row),
	// the title REL's own screen.
	for _, n := range []string{"ship_race2.blo", "hukidashi_00.blo", "hukidashi_08.blo", "title_logo_e.blo"} {
		if !have[n] {
			t.Errorf("%s missing from %s", n, NamesPath)
		}
	}
	rows := map[uint32]bool{}
	for _, r := range tableRows() {
		rows[NameHash(r.name+".blo")] = true
	}
	for _, n := range []string{"hukidashi_00.blo", "hukidashi_08.blo"} {
		if rows[NameHash(n)] {
			t.Errorf("%s has a row; the plan says it has none", n)
		}
	}
}

func rowByName(t *testing.T, name string) (row, int) {
	t.Helper()
	first := 0
	for _, r := range tableRows() {
		if r.name == name {
			return r, first
		}
		first += len(r.rules)
	}
	t.Fatalf("no row %s", name)
	return row{}, 0
}

// The plan's table (3.3): stage-2 and stage-3 rows, flags and rules.
func TestTable(t *testing.T) {
	rows := tableRows()
	nRules, nEdge := 0, 0
	for _, r := range rows {
		nRules += len(r.rules)
		for _, ru := range r.rules {
			if ru.act == actEdge {
				nEdge++
			}
		}
	}
	// 12 of the plan's rows plus wipe_01_01: the 22 GEOM-only rows went
	// to the global GEOM rule (section 6, lever 1; TestGeomGlobalRule).
	if len(rows) != 13 || nRules != 39 || nEdge != 12 {
		t.Errorf("rows %d rules %d EDGE %d, want 13, 39, 12", len(rows), nRules, nEdge)
	}
	if got := len(rows)*8 + nRules*5; got != 0x12B {
		t.Errorf("data 0x%X bytes, want 0x12B", got)
	}
	for _, r := range rows {
		if r.flags == flagGeom && r.kind == kindNone && len(r.rules) == 0 {
			t.Errorf("%s: a GEOM-only row; the global rule covers it", r.name)
		}
	}

	mp1, _ := rowByName(t, "main_parts1")
	if mp1.kind|mp1.flags != kindByCX|flagEpoch {
		t.Errorf("main_parts1 flags 0x%02X, want BY_CX|EPOCH 0x23", mp1.kind|mp1.flags)
	}
	wantNone := []string{"ben1", "ben2", "ben3", "ben4", "enem", "enm2", "bey1", "bey2", "enek", "ene1", "ene2", "ene3", "l", "r", "wiy1"}
	if len(mp1.rules) != len(wantNone) {
		t.Fatalf("main_parts1 has %d rules, want 15", len(mp1.rules))
	}
	for i, ru := range mp1.rules {
		if ru.tag != wantNone[i] || ru.act != actNone {
			t.Errorf("main_parts1 rule %d = %q %s, want %q NONE", i, ru.tag, actionNames[ru.act], wantNone[i])
		}
	}

	want := map[string]struct {
		flags uint8
		rules string
	}{
		"main_parts2":           {kindAllR, ""},
		"main_parts3":           {kindAllL, ""},
		"time_swim_1":           {flagScrR, ""},
		"time_swim_2":           {kindAllR, ""},
		"place_name":            {0, "pn:L"},
		"auction1":              {flagScrL, ""},
		"auction2":              {0, "icon:L ip00:L ip01:L ip02:L ip03:L gd00:L gd01:L gd02:L gd03:L gd04:L gd05:L"},
		"menu_save":             {flagGeom, "wp04:EDGE wp05:EDGE wp06:EDGE wp07:EDGE"},
		"wipe_00":               {0, "wp04:EDGE wp05:EDGE wp06:EDGE wp07:EDGE"},
		"wipe_00_2":             {0, "wp04:EDGE wp05:EDGE wp06:EDGE wp07:EDGE"},
		"main_parts_fileselect": {kindAllR | flagEpoch, ""},
		"wipe_01_01":            {0, ""},
	}
	for name, w := range want {
		r, _ := rowByName(t, name)
		var got []string
		for _, ru := range r.rules {
			got = append(got, ru.tag+":"+actionNames[ru.act])
		}
		if r.kind|r.flags != w.flags || strings.Join(got, " ") != w.rules {
			t.Errorf("%s: flags 0x%02X rules %q, want 0x%02X %q", name, r.kind|r.flags, strings.Join(got, " "), w.flags, w.rules)
		}
	}

	// The header carries main_parts1's row and its rules as the walker reads them.
	h, err := Generate(rows)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(h, []byte("{ 0x"+hex8(NameHash("main_parts1.blo"))+", 0x23, 15,  0, 0 }, // main_parts1.blo")) {
		t.Errorf("header lacks main_parts1's row {hash, 0x23, 15, 0, 0}")
	}
	_, pnFirst := rowByName(t, "place_name")
	if !bytes.Contains(h, []byte("    0x0000706E, // "+itoa2(pnFirst)+" 'pn' L (place_name)")) {
		t.Errorf("header lacks place_name's right-justified 'pn' rule")
	}
}

func hex8(v uint32) string {
	const d = "0123456789ABCDEF"
	b := make([]byte, 8)
	for i := 7; i >= 0; i-- {
		b[i] = d[v&15]
		v >>= 4
	}
	return string(b)
}

func itoa2(n int) string {
	s := []byte{' ', byte('0' + n%10)}
	if n >= 10 {
		s[0] = byte('0' + n/10)
	}
	return string(s)
}

// Tags are C multichar values, right-justified.
func TestTagValue(t *testing.T) {
	for tag, want := range map[string]uint32{
		"l": 0x6C, "r": 0x72, "pn": 0x706E, "wp04": 0x77703034, "ROOT": 0x524F4F54, "icon": 0x69636F6E,
	} {
		got, err := TagValue(tag)
		if err != nil || got != want {
			t.Errorf("TagValue(%q) = 0x%X, %v; want 0x%X", tag, got, err, want)
		}
	}
	for _, bad := range []string{"", "toolong", "a'b"} {
		if _, err := TagValue(bad); err == nil {
			t.Errorf("TagValue(%q) gave no error", bad)
		}
	}
}

// The hash, pinned by hand: 5381*33 ^ ('a'|0x20) = 0x0002B5C4, and case folds.
func TestNameHash(t *testing.T) {
	if got := NameHash("a"); got != 5381*33^0x61 {
		t.Errorf("NameHash(a) = 0x%X", got)
	}
	if NameHash("DOC_map_01.blo") != NameHash("doc_map_01.blo") {
		t.Error("hash does not fold case")
	}
	if NameHash("main_parts1.blo") == NameHash("main_parts1") {
		t.Error("the extension is not hashed")
	}
}

func TestTagsRange(t *testing.T) {
	got := tags(actL, "ip00-03", "ben1-4", "pn")
	var s []string
	for _, r := range got {
		s = append(s, r.tag)
	}
	if strings.Join(s, " ") != "ip00 ip01 ip02 ip03 ben1 ben2 ben3 ben4 pn" {
		t.Errorf("tags = %v", s)
	}
}

// With the user's vanilla disc (skipped without one): the committed name
// list is what the disc gives.
func TestBloNamesFromDisc(t *testing.T) {
	p := vanillatest.Path()
	if p == "" {
		t.Skipf("no vanilla disc: set %s", vanillatest.EnvVar)
	}
	got, err := BloNamesFromDisc(p)
	if err != nil {
		t.Skipf("vanilla disc %s: %v", p, err)
	}
	if strings.Join(got, "\n") != strings.Join(bloNames(t), "\n") {
		t.Errorf("%s differs from the disc's: run go run ./tools/gen_wstable -names <disc>", NamesPath)
	}
}

// geomAccepted: screens outside the plan's GEOM list whose only GEOM panes
// are 'BACK', the layout tool's reference picture, whose texture is in no
// archive on the disc (back.bti, baton3.bti, shiprace.bti, bg_proro.bti;
// checked 2026-11-09 against the vanilla disc), so it draws nothing and
// widening it changes nothing. geomKept: screens with a row that turns
// GEOM off, and why.
var (
	geomAccepted = []string{"baton_input", "file_error", "hukidashi_00", "hukidashi_14_1", "hukidashi_d00",
		"name_input", "opening", "ship_race1", "ship_race2", "title_logo_e", "wipe_01_02"}
	geomKept = map[string]string{
		"wipe_01_01": "the picto album's paper b1 and strip b2, drawn uncompressed",
		"auction1":   "BACK (back_auction_00.bti, not on the disc) moves with the screen (SCR_L)",
		"auction2":   "BACK (back_auction_00.bti, not on the disc)",
	}
)

// TestGeomGlobalRule: the global GEOM rule (a screen with no row gets
// GEOM; plan section 6, lever 1) widens exactly the panes the plan meant,
// over every .blo on the vanilla disc (testdata/geom_hits.txt): each of
// the plan's 22 GEOM screens and menu_save has a hit and gets GEOM; every
// other screen with a hit is either a BACK-only screen in geomAccepted or
// has a row that turns GEOM off (geomKept).
func TestGeomGlobalRule(t *testing.T) {
	b, err := os.ReadFile(filepath.Join(root(t), filepath.FromSlash(GeomHitsPath)))
	if err != nil {
		t.Fatal(err)
	}
	rowFlags := map[uint32]uint8{}
	for _, r := range tableRows() {
		rowFlags[NameHash(r.name+".blo")] = r.flags
	}
	geomOn := func(screen string) bool {
		f, ok := rowFlags[NameHash(screen+".blo")]
		return !ok || f&flagGeom != 0
	}
	tagsOf := map[string][]string{}
	for _, line := range strings.Split(strings.TrimSpace(strings.ReplaceAll(string(b), "\r\n", "\n")), "\n") {
		f := strings.Fields(line)
		if len(f) != 6 || !strings.HasSuffix(f[0], ".blo") {
			t.Fatalf("geom_hits.txt line %q", line)
		}
		s := strings.ToLower(strings.TrimSuffix(f[0], ".blo"))
		tagsOf[s] = append(tagsOf[s], f[1])
	}
	want := map[string]bool{"menu_save": true}
	for _, s := range geomScreens {
		want[strings.ToLower(s)] = true
	}
	for s := range want {
		if len(tagsOf[s]) == 0 {
			t.Errorf("%s.blo: a GEOM screen of the plan with no GEOM pane on the disc", s)
		}
		if !geomOn(s) {
			t.Errorf("%s.blo: a GEOM screen of the plan, but its row turns GEOM off", s)
		}
	}
	accepted := map[string]bool{}
	for _, s := range geomAccepted {
		accepted[s] = true
	}
	for s, tags := range tagsOf {
		switch {
		case want[s]:
		case geomKept[s] != "":
			if geomOn(s) {
				t.Errorf("%s.blo: GEOM must stay off (%s)", s, geomKept[s])
			}
		case accepted[s]:
			for _, tag := range tags {
				if tag != "BACK" {
					t.Errorf("%s.blo: GEOM pane %s is not the BACK reference picture", s, tag)
				}
			}
		default:
			t.Errorf("%s.blo: GEOM panes %v the plan does not list: add a row without GEOM or accept them", s, tags)
		}
	}
}

// TestGeomHitsFromDisc: testdata/geom_hits.txt is what the disc gives
// (skipped without a vanilla disc).
func TestGeomHitsFromDisc(t *testing.T) {
	p := vanillatest.Path()
	if p == "" {
		t.Skipf("no vanilla disc: set %s", vanillatest.EnvVar)
	}
	got, _, err := GeomHitsFromDisc(p)
	if err != nil {
		t.Skipf("vanilla disc %s: %v", p, err)
	}
	b, err := os.ReadFile(filepath.Join(root(t), filepath.FromSlash(GeomHitsPath)))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(got, "\n")+"\n" != strings.ReplaceAll(string(b), "\r\n", "\n") {
		t.Errorf("%s differs from the disc's: run go run ./tools/gen_wstable -geom <disc>", GeomHitsPath)
	}
}

// TestParseBlo: a hand-made screen with a full-screen childless PIC1
// under a PAN1 at (5, 0), a full-screen PIC1 with a child, and a PIC1
// short of the right edge: only the first is a GEOM hit, at absolute x.
func TestParseBlo(t *testing.T) {
	var b []byte
	u16 := func(v int) { b = append(b, byte(v>>8), byte(v)) }
	u32 := func(v uint32) { b = append(b, byte(v>>24), byte(v>>16), byte(v>>8), byte(v)) }
	block := func(magic string, body []byte) {
		b = append(b, magic...)
		u32(uint32(8 + len(body)))
		b = append(b, body...)
	}
	pane := func(tag string, x, y, w, h int) []byte {
		s := []byte{6, 1, 0, 0}
		v, _ := TagValue(tag)
		s = append(s, byte(v>>24), byte(v>>16), byte(v>>8), byte(v))
		for _, n := range []int{x, y, w, h} {
			s = append(s, byte(n>>8), byte(n))
		}
		return s
	}
	b = append(b, "SCRNblo1"...)
	b = append(b, make([]byte, 0x18)...)
	b = append(b, "INF1"...)
	u32(0x10)
	u16(640)
	u16(480)
	u32(0)
	block("PAN1", pane("ROOT", 5, 0, 640, 480))
	block("BGN1", nil)
	block("PIC1", pane("full", -20, -20, 680, 520))
	block("PIC1", pane("par", -20, -20, 680, 520))
	block("BGN1", nil)
	block("PIC1", pane("kid", 0, 0, 8, 8))
	block("END1", nil)
	block("PIC1", pane("shrt", -20, -20, 600, 520))
	block("END1", nil)
	block("EXT1", nil)
	root, err := ParseBlo(b)
	if err != nil {
		t.Fatal(err)
	}
	hits := GeomHits("t.blo", root)
	if len(hits) != 1 || hits[0].String() != "t.blo full -15 -20 665 500" {
		t.Errorf("hits %v, want [t.blo full -15 -20 665 500]", hits)
	}
}
