package main

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/fakegame"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mem"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/stage"
)

func bdkOnce(ctx context.Context, w *bytes.Buffer, m *mem.FakeMem) error {
	return bdkProbe(ctx, w, m, 0)
}

// TestBdkProbe: bdk-probe shows Helmaroc King's words and decode and
// Link's y, and answers as bk_bdk would in each class: WAITING in the
// tower phase (health the create's zeroed 0), the intro, the mask fight,
// the mask demo and under a demo camera; APPLIED in the crest fight;
// ALREADY with a pending zero, in END and AFTER_FIGHT; NO_ACTOR without
// the morf or with no Bdk.
func TestBdkProbe(t *testing.T) {
	m, es := batchDRoom(t, fakegame.Enemy{SetID: addr.ActorSetIDNone, Proc: stage.ProcBDK})
	a, _ := es.Of(0)
	at := a.Addr
	store(t, m, fakegame.PlayerActor+addr.ActorOffCurPos+4, f32bits(9300))
	store(t, m, at+bdkOffHealth, u32b(0)) // the T_FLY create writes no health (zeroed at create)
	store(t, m, at+bdkOffMorf, u32b(0x80C20000))
	store(t, m, at+bdkOffM2B4, u32b(0x01000000))
	store(t, m, at+bdkOffAct, u32b(0x002A0064)) // m2C4 42, T_FLY
	s := runProbe(t, m, bdkOnce)
	for _, want := range []string{
		`stage "M_Dai" room 1; P0-BDK wants M2tower 30 0 3`,
		"1 proc 0xF0 actors\n",
		"  bdk at 0x",
		"morf 0x80C20000 | +0x284 00000000 (max_health 0 health 0) +0x2B4 01000000 (m2B4 1) +0x2C4 002A0064 (m2C4 42 mAction 0x64, the tower phase) +0x2C8 00000000 (mState 0)",
		"+0x8F8 00000000 (m8F8 0) +0x25A0 00000000 (m25A0 0x0 m25A2 0) pos (5000, 0, 0)",
		"  Link y 9300.0: above 9250 (T_LASTATTACK)\n",
		"bk_bdk would answer WAITING 0x02640000: mAction 0x64 not 0..8 (the tower phase)\n",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("output lacks %q:\n%s", want, s)
		}
	}
	// The awk in the design's hkforce.sh takes the third field.
	for _, l := range strings.Split(s, "\n") {
		if strings.HasPrefix(l, "  bdk ") {
			if f := strings.Fields(l); len(f) < 3 || f[2] != fmt.Sprintf("0x%08X", at) {
				t.Errorf("bdk line's third field: %q, want 0x%08X", f, at)
			}
		}
	}
	for _, c := range []struct {
		name            string
		w284, w2b4, act uint32
		mask, demo      uint32
		want            string
	}{
		{"the hammer hit", 0x00140000, 0x01000000, 0x00000067, 0, 0, "WAITING 0x02670014: mAction 0x67 not 0..8 (the tower phase)"},
		{"the intro", 0x14140000, 0x01000000, 0x0000000F, 0, 0x000A0000, "WAITING 0x020F0014: mAction 0xF not 0..8 (START, the intro)"},
		{"the mask fight", 0x14140000, 0, 0x00000003, 0x02000000, 0, "WAITING 0x02030214: m8F8 2 < 4 (the mask)"},
		{"the mask demo", 0x14140000, 0, 0x00000009, 0x04000000, 0x00020000, "WAITING 0x02090414: mAction 0x9 not 0..8 (KAMEN_DEMO)"},
		{"a demo camera", 0x14140000, 0, 0x00000005, 0x04000000, 0x00960000, "WAITING 0x02050414: m25A0 0x96 != 0 (a demo camera)"},
		{"the crest fight", 0x14140000, 0, 0x00000005, 0x04000000, 0, "APPLIED 0x01050414"},
		{"a pending zero", 0x14000000, 0, 0x00000001, 0x04000000, 0, "ALREADY 0x03010400: health <= 0 at a fight action: a pending zero, the next crest hit kills"},
		{"the death", 0x14000000, 0, 0x0000000A, 0x04000000, 0x00640000, "ALREADY 0x030A0400"},
		{"after the fight", 0, 0x01000000, 0x0000006E, 0, 0, "ALREADY 0x036E0000"},
	} {
		store(t, m, at+bdkOffHealth, u32b(c.w284))
		store(t, m, at+bdkOffM2B4, u32b(c.w2b4))
		store(t, m, at+bdkOffAct, u32b(c.act))
		store(t, m, at+bdkOffMask, u32b(c.mask))
		store(t, m, at+bdkOffDemo, u32b(c.demo))
		if s := runProbe(t, m, bdkOnce); !strings.Contains(s, "bk_bdk would answer "+c.want+"\n") {
			t.Errorf("%s: want %q:\n%s", c.name, c.want, s)
		}
	}
	// The crest fight with m2B4 still 1: the REL applies, the probe notes it.
	store(t, m, at+bdkOffHealth, u32b(0x14140000))
	store(t, m, at+bdkOffM2B4, u32b(0x01000000))
	store(t, m, at+bdkOffAct, u32b(0x00000005))
	store(t, m, at+bdkOffMask, u32b(0x04000000))
	store(t, m, at+bdkOffDemo, u32b(0))
	if s := runProbe(t, m, bdkOnce); !strings.Contains(s, "APPLIED 0x01050414\n  note: m2B4 1 with m8F8 4 (expected 0 in the arena)\n") {
		t.Errorf("m2B4 note:\n%s", s)
	}
	store(t, m, at+bdkOffMorf, u32b(0))
	if s := runProbe(t, m, bdkOnce); !strings.Contains(s, "bk_bdk would answer NO_ACTOR 0x04050414: the morf is NULL\n") {
		t.Errorf("no morf:\n%s", s)
	}
	if s := bdkReport(nil, 0, false); !strings.HasPrefix(s, "0 proc 0xF0 actors (want 1: Helmaroc King, M2tower layer 3)\n") ||
		!strings.Contains(s, "  Link: none\n") || !strings.Contains(s, "NO_ACTOR 0x04000000: no executing Bdk") {
		t.Errorf("no Bdk:\n%s", s)
	}
	for _, c := range []struct {
		y    float32
		want string
	}{{1700, "below 9250 (the climb)"}, {9800, "at or above 9800 (start() takes it)"}} {
		if s := bdkReport(nil, c.y, true); !strings.Contains(s, c.want) {
			t.Errorf("Link y %v: want %q:\n%s", c.y, c.want, s)
		}
	}
}
