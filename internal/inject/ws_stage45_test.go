package inject

// Native widescreen stages 4 and 5 (docs/design/native-widescreen.md
// section 8): the hook sites, the WS module's frame_any slot and the
// projected-point variant.

import (
	"encoding/binary"
	"os"
	"regexp"
	"testing"
)

// wsStage45Sites: ws_project_live's sites (3.4's "optional" rows), each a
// bl to mDoLib_project in the vanilla DOL (md5 757ce8cb...).
var wsStage45Sites = []wsSite{
	{"ws_project_live", 0x8008C6C0, 0x089600, 0x4BF8B089, 0x80017748}, // dKyr_lenzflare_move: the flare's angle
	{"ws_project_live", 0x800994AC, 0x0963EC, 0x4BF7E29D, 0x80017748}, // dKyr_drawStar: the moon
	{"ws_project_live", 0x80099A6C, 0x0969AC, 0x4BF7DCDD, 0x80017748}, // dKyr_drawStar: each star
	{"ws_project_live", 0x8019744C, 0x19438C, 0x4BE802FD, 0x80017748}, // dKy_telescope_lookin_chk
}

// TestWidescreenStage45HookSites: as TestWidescreenStage2HookSites, for
// the stage 4-5 sites.
func TestWidescreenStage45HookSites(t *testing.T) {
	for _, s := range wsStage45Sites {
		if got := blTarget(t, s.vanilla, s.site); got != s.callee {
			t.Errorf("%s 0x%08X: vanilla 0x%08X calls 0x%08X, want 0x%08X", s.fn, s.site, s.vanilla, got, s.callee)
		}
		if s.site-0x800030C0 != s.fileOff {
			t.Errorf("%s 0x%08X: file offset 0x%X, want 0x%X", s.fn, s.site, s.fileOff, s.site-0x800030C0)
		}
		checkHookSite(t, s.fn, s.site, s.fileOff, s.vanilla)
	}
}

// TestWidescreenStage45VanillaWords: each pinned vanilla word is the one
// on the vanilla disc's DOL. Skips without a vanilla disc.
func TestWidescreenStage45VanillaWords(t *testing.T) {
	p := vanillaDiscPath()
	if p == "" {
		t.Skip("no vanilla disc: set WW_VANILLA_DISC to the ISO or CISO")
	}
	d, err := OpenDisc(p)
	if err != nil {
		t.Skipf("vanilla disc %s: %v", p, err)
	}
	defer d.Close()
	dol, err := d.ReadDOL()
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range wsStage45Sites {
		if got := binary.BigEndian.Uint32(dol[s.fileOff:]); got != s.vanilla {
			t.Errorf("%s 0x%08X: the vanilla DOL holds 0x%08X, the pin says 0x%08X", s.fn, s.site, got, s.vanilla)
		}
	}
}

// TestWidescreenStage45Source: the WS module runs ws_frame_any in its
// frame_any slot (the title movie plays before Link exists, so never the
// frame slot), and ws_project_live widens about 320 by the live 3D factor
// (ws_live_factor, not the latched 2D one), behind F != 1.
func TestWidescreenStage45Source(t *testing.T) {
	b, err := os.ReadFile("../../inject/src/core/hooks.c")
	if err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`\{\s*MOD_ID_WS,\s*ws_init,\s*ws_frame_any,\s*0,\s*0,\s*0\s*\}`).Match(b) {
		t.Error("hooks.c: the WS module's entry is not { MOD_ID_WS, ws_init, ws_frame_any, 0, 0, 0 }")
	}
	body := regexp.MustCompile(`(?s)void ws_project_live\(cXyz\* src, cXyz\* dst\) \{(.*?)\n\}`).FindSubmatch(b)
	if body == nil {
		t.Fatal("hooks.c has no ws_project_live")
	}
	for _, want := range []string{"mDoLib_project(src, dst);", "F = ws_live_factor();", "if (F != 1.0f)", "dst->x = 320.0f + (dst->x - 320.0f) * F;"} {
		if !regexp.MustCompile(regexp.QuoteMeta(want)).Match(body[1]) {
			t.Errorf("ws_project_live does not hold %q", want)
		}
	}
}
