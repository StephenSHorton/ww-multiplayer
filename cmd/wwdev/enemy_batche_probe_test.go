package main

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/fakegame"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mailbox"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mem"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/stage"
)

// runProbe runs probe once over m.
func runProbe(t *testing.T, m *mem.FakeMem, probe func(context.Context, *bytes.Buffer, *mem.FakeMem) error) string {
	t.Helper()
	var out bytes.Buffer
	if err := probe(context.Background(), &out, m); err != nil {
		t.Fatal(err)
	}
	return out.String()
}

func bwdOnce(ctx context.Context, w *bytes.Buffer, m *mem.FakeMem) error {
	return bwdProbe(ctx, w, m, 0)
}
func bpwOnce(ctx context.Context, w *bytes.Buffer, m *mem.FakeMem) error {
	return bpwProbe(ctx, w, m, 0)
}

// TestBwdProbe: bwd-probe shows Molgera's words and decode, and answers as
// bk_bwd would: APPLIED with the tongue looped and its timer running,
// WAITING (with why) once the timer runs out or a demo camera runs,
// ALREADY in end() and the inert post-kill state, NO_ACTOR without the
// morf or with no Bwd.
func TestBwdProbe(t *testing.T) {
	m, es := batchDRoom(t, fakegame.Enemy{SetID: addr.ActorSetIDNone, Proc: stage.ProcBWD})
	a, _ := es.Of(0)
	at := a.Addr
	store(t, m, at+bwdOffMorf, u32b(0x80C10000))
	store(t, m, at+bwdOffAct, u32b(0x00000003))
	store(t, m, at+bwdOffStep, u32b(0x00020000))
	store(t, m, at+bwdOffM1BB, u32b(0x00500000)) // m1BB1 80
	store(t, m, at+bwdOffHits, u32b(0x01020000)) // m1BB4 1, m1BB5 2
	s := runProbe(t, m, bwdOnce)
	for _, want := range []string{
		`stage "M_Dai" room 1; bwd-probe wants kazeB (0 0 or 1 0)`,
		"1 proc 0xDA actors\n",
		"morf 0x80C10000 | +0x18AC 00000003 (m18AE 3) +0x18B0 00020000 (m18B0 2) +0x1BB0 00500000 (m1BB1 80) +0x1BB4 01020000 (m1BB4 1 m1BB5 2) +0x3C1C 00000000 (m3C1E 0)",
		"bk_bwd would answer APPLIED 0x01030202\n",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("output lacks %q:\n%s", want, s)
		}
	}
	store(t, m, at+bwdOffM1BB, u32b(0))
	if s := runProbe(t, m, bwdOnce); !strings.Contains(s, "bk_bwd would answer WAITING 0x02030202: m1BB1 0\n") {
		t.Errorf("the timer out:\n%s", s)
	}
	store(t, m, at+bwdOffM1BB, u32b(0x00500000))
	store(t, m, at+bwdOffDemo, u32b(0x00000096))
	if s := runProbe(t, m, bwdOnce); !strings.Contains(s, "bk_bwd would answer WAITING 0x02030202: m3C1E 150 != 0\n") {
		t.Errorf("a demo camera:\n%s", s)
	}
	store(t, m, at+bwdOffAct, u32b(0x0000000C))
	if s := runProbe(t, m, bwdOnce); !strings.Contains(s, "bk_bwd would answer ALREADY 0x030C0202\n") {
		t.Errorf("end():\n%s", s)
	}
	store(t, m, at+bwdOffAct, u32b(0x0000000D))
	if s := runProbe(t, m, bwdOnce); !strings.Contains(s, "bk_bwd would answer ALREADY 0x030D0202\n") {
		t.Errorf("inert:\n%s", s)
	}
	store(t, m, at+bwdOffMorf, u32b(0))
	if s := runProbe(t, m, bwdOnce); !strings.Contains(s, "bk_bwd would answer NO_ACTOR 0x040D0202: the head morf is NULL\n") {
		t.Errorf("no morf:\n%s", s)
	}
	if s := bwdReport(nil); !strings.HasPrefix(s, "0 proc 0xDA actors (want 1: Molgera)\n") || !strings.Contains(s, "NO_ACTOR 0x04000000: no executing Bwd") {
		t.Errorf("no Bwd:\n%s", s)
	}
}

// TestBpwProbe: bpw-probe lists the proc 0xD4 actors with their type and
// the Poes with their parent, and answers as bk_bpw would for the first
// executing body in queue order: APPLIED frozen with health, WAITING (with
// why) in the split, ALREADY at health 0 or in the death demo, NO_ACTOR
// with only a kantera.
func TestBpwProbe(t *testing.T) {
	m, es := batchDRoom(t,
		fakegame.Enemy{SetID: addr.ActorSetIDNone, Proc: stage.ProcBPW, Params: 0x00E0FF01}, // the kantera first in the queue
		fakegame.Enemy{SetID: addr.ActorSetIDNone, Proc: stage.ProcBPW, Params: 0x00E0FFFF},
		fakegame.Enemy{SetID: addr.ActorSetIDNone, Proc: stage.ProcPW},
	)
	kan, _ := es.Of(0)
	body, _ := es.Of(1)
	poe, _ := es.Of(2)
	store(t, m, kan.Addr+mailbox.BossBpwTypeOff, []byte{1})
	store(t, m, kan.Addr+mailbox.BossBpwActionOff, u32b(0x00000000))
	store(t, m, body.Addr+mailbox.BossBpwTypeOff, []byte{0})
	store(t, m, body.Addr+mailbox.BossBpwActionOff, u32b(0x00030053))
	store(t, m, body.Addr+addr.ActorOffMaxHealth, u32b(0x0F0F0000))
	store(t, m, poe.Addr+addr.ActorOffMaxHealth, u32b(0x04040000))
	store(t, m, poe.Addr+pwOffM344, u32b(0x00010000))
	store(t, m, poe.Addr+pwOffParent, u32b(body.PID))
	s := runProbe(t, m, bpwOnce)
	for _, want := range []string{
		`stage "M_Dai" room 1; P0-BPW wants M_DaiB 0 0`,
		"2 proc 0xD4 actors\n",
		"  kantera  pid ", "  body     pid ",
		"+0x3DC 0 | +0x444 00030053 (mBodyAction 3 mActionState 0x53) +0x284 0F0F0000 (max_health 15 health 15)",
		"1 proc 0xD5 Poes\n",
		"health 4 m344 0 m345 1 +0x36C ",
		"bk_bpw would answer APPLIED 0x0103530F\n",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("output lacks %q:\n%s", want, s)
		}
	}
	if k, b := strings.Index(s, "kantera "), strings.Index(s, "body "); k < 0 || b < k {
		t.Errorf("not in queue order:\n%s", s)
	}
	store(t, m, body.Addr+mailbox.BossBpwActionOff, u32b(0x0004006F))
	if s := runProbe(t, m, bpwOnce); !strings.Contains(s, "bk_bpw would answer WAITING 0x02046F0F: mBodyAction 4 != 3, mActionState 0x6F not 0x52..0x55, 0x64 or 0x65\n") {
		t.Errorf("the split:\n%s", s)
	}
	store(t, m, body.Addr+mailbox.BossBpwActionOff, u32b(0x00040079))
	if s := runProbe(t, m, bpwOnce); !strings.Contains(s, "bk_bpw would answer ALREADY 0x0304790F\n") {
		t.Errorf("the death demo:\n%s", s)
	}
	store(t, m, body.Addr+mailbox.BossBpwActionOff, u32b(0x00000000))
	store(t, m, body.Addr+addr.ActorOffMaxHealth, u32b(0x0F000000))
	if s := runProbe(t, m, bpwOnce); !strings.Contains(s, "bk_bpw would answer ALREADY 0x03000000\n") {
		t.Errorf("health 0:\n%s", s)
	}
	if r, _, why := bpwVerdict([]bpwActor{{Type: 1, Init: 2}, {Type: 0, Init: 1}}); r != mailbox.BossRNoActor || why != "no executing body (mType 0, init 2)" {
		t.Errorf("a kantera and a creating body: %d %q", r, why)
	}
}
