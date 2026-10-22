package enemysync

import (
	"encoding/binary"
	"strings"
	"testing"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/wwapi/log"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mailbox"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mem"
)

// blockSideWrites is m's recorded writes less the knob tables (layout 4
// writes the table as its own write before a block naming a new gen).
func blockSideWrites(m *mem.FakeMem) []mem.WriteRecord {
	var out []mem.WriteRecord
	for _, w := range m.Writes() {
		if w.Addr != mailbox.EnemyKnobAddr {
			out = append(out, w)
		}
	}
	return out
}

// knobWrites decodes the knob tables m recorded (each must be a whole
// table, valid for its own gen).
func knobWrites(t *testing.T, m *mem.FakeMem) []mailbox.EnemyKnobs {
	t.Helper()
	var out []mailbox.EnemyKnobs
	for _, w := range m.Writes() {
		if w.Addr != mailbox.EnemyKnobAddr {
			continue
		}
		if len(w.Data) != int(mailbox.EnemyKnobSize) {
			t.Fatalf("knob table write of %d bytes", len(w.Data))
		}
		gen := uint16(binary.BigEndian.Uint32(w.Data))
		k, ok, err := mailbox.DecodeEnemyKnobs(w.Data, gen, int(len(Kinds)))
		if err != nil || !ok {
			t.Fatalf("knob table write: ok %v err %v", ok, err)
		}
		out = append(out, k)
	}
	return out
}

// relEcho stores what the REL writes after a consume: ack_seq and the
// knob echo (the game's write, never recorded).
func relEcho(t *testing.T, m *mem.FakeMem, ack uint32, seen uint16) {
	t.Helper()
	store(t, m, mailbox.EnemyBlockAddr+mailbox.EnemyAckSeqOff, binary.BigEndian.AppendUint32(nil, ack))
	store(t, m, mailbox.EnemyBlockAddr+mailbox.EnemyKnobSeenOff, binary.BigEndian.AppendUint16(nil, seen))
}

// TestKnobWriterGens: the first block writes the table before it with a
// gen that is never 0; the same values under the same epoch write
// nothing more; a value change and an epoch change each write a new gen;
// a reset (disarm) writes a new one at the next block.
func TestKnobWriterGens(t *testing.T) {
	m := mem.NewFakeMem()
	m.Record(true)
	var k knobWriter
	k.seed(0xFFFF) // the next gen wraps through 0, which it skips
	var want mailbox.EnemyKnobs
	want.Quiet[KindKI] = 5
	g1, err := k.prepare(m, &want, 7, 1)
	if err != nil || g1 == 0 {
		t.Fatalf("first gen %d %v", g1, err)
	}
	if g, _ := k.prepare(m, &want, 7, 2); g != g1 {
		t.Errorf("same values: gen %d, want %d", g, g1)
	}
	if n := len(knobWrites(t, m)); n != 1 {
		t.Fatalf("%d tables for one gen", n)
	}
	got, ok, err := mailbox.ReadEnemyKnobs(m, g1, int(len(Kinds)))
	if err != nil || !ok || got.Quiet[KindKI] != 5 || got.Gen != g1 {
		t.Errorf("table in RAM: %+v ok %v %v", got, ok, err)
	}
	want.Invuln[KindBB] = 3
	g2, _ := k.prepare(m, &want, 7, 3)
	g3, _ := k.prepare(m, &want, 8, 4)
	k.reset()
	g4, _ := k.prepare(m, &want, 8, 5)
	if g2 == g1 || g3 == g2 || g4 == g3 || g2 == 0 || g3 == 0 || g4 == 0 {
		t.Errorf("gens %d %d %d %d: each change wants a new nonzero one", g1, g2, g3, g4)
	}
	if n := len(knobWrites(t, m)); n != 4 {
		t.Errorf("%d tables, want 4", n)
	}
}

// TestKnobRewriteOnEcho (batch C go_changes, test_plan L4-4): once the
// REL acked a block naming the gen, an echo of another gen (a clobbered
// table: the REL found it invalid) rewrites the table exactly once and
// logs it; the rewrite waits for the REL to ack a block written after it
// before it can fire again, and an echo that matches never rewrites. An
// ack before the first block naming the gen is no verdict.
func TestKnobRewriteOnEcho(t *testing.T) {
	m := mem.NewFakeMem()
	m.Record(true)
	rec := &levelLog{}
	var k knobWriter
	k.seed(41)
	var want mailbox.EnemyKnobs
	want.TgUp[KindBB] = 30
	gen, err := k.prepare(m, &want, 1, 10)
	if err != nil {
		t.Fatal(err)
	}
	check := func(ack uint32, seen uint16) {
		t.Helper()
		st := mailbox.EnemyStatus{AckSeq: ack, KnobGenSeen: seen}
		if err := k.check(m, rec, "enemy test", &st); err != nil {
			t.Fatal(err)
		}
	}
	check(9, 0) // an ack of an older block: no verdict on gen
	check(10, gen)
	if k.Rewrites != 0 {
		t.Fatalf("%d rewrites with the echo matching", k.Rewrites)
	}
	// The table is clobbered: the REL refuses it and echoes 0.
	store(t, m, mailbox.EnemyKnobAddr, []byte{0xDE, 0xAD, 0xBE, 0xEF})
	check(10, 0)
	check(10, 0)
	check(10, 0)
	if k.Rewrites != 1 || len(knobWrites(t, m)) != 2 {
		t.Fatalf("%d rewrites, %d tables: want exactly one rewrite", k.Rewrites, len(knobWrites(t, m)))
	}
	if got, ok, _ := mailbox.ReadEnemyKnobs(m, gen, int(len(Kinds))); !ok || got.TgUp[KindBB] != 30 {
		t.Errorf("rewritten table: %+v ok %v", got, ok)
	}
	n := 0
	for _, l := range rec.lines {
		if strings.Contains(l.msg, "knob table rewritten: seen G0 want G") {
			n++
		}
	}
	if n != 1 {
		t.Errorf("%d rewrite lines: %+v", n, rec.lines)
	}
	// The next block names the same gen and becomes the new since; the
	// REL acks it with the table valid again.
	if g, _ := k.prepare(m, &want, 1, 11); g != gen {
		t.Fatalf("gen %d after the rewrite, want %d", g, gen)
	}
	check(10, 0) // still the old ack
	check(11, gen)
	if k.Rewrites != 1 || len(knobWrites(t, m)) != 2 {
		t.Errorf("%d rewrites, %d tables after the echo recovered", k.Rewrites, len(knobWrites(t, m)))
	}
}

// TestMirrorKnobTable: the client writes the table before its first
// block, the block names its gen and carries WW_ENEMY_ANNEX's hook bytes
// for ids 7..14; a clobbered table with the REL's ack on that block and a
// mismatching echo is rewritten once at the next tick, not again while
// the REL has not acked a newer block.
func TestMirrorKnobTable(t *testing.T) {
	_, m, _, tick := clientRoom(t)
	start := time.Unix(2000, 0)
	c := NewMirror(m, log.Discard{}, nil, false, start)
	o, err := ParseOverrides("bb:hooks=tail,ki:quiet=5,bb:invuln=3")
	if err != nil {
		t.Fatal(err)
	}
	c.HkdHi, c.Knobs = o.HkdHi, o.Knobs
	m.Record(true)
	now := start.Add(time.Second)
	pushState(t, c, hostState(1, 1000), now)
	tick()
	if _, _, err := c.Tick(now); err != nil {
		t.Fatal(err)
	}
	ks := knobWrites(t, m)
	bs := blockWrites(t, m)
	if len(ks) != 1 || len(bs) != 1 || m.Writes()[0].Addr != mailbox.EnemyKnobAddr {
		t.Fatalf("%d tables, %d blocks: want the table first", len(ks), len(bs))
	}
	gen := ks[0].Gen
	if gen == 0 || bs[0].KnobGen != gen || bs[0].HkdHi[KindBB-7] != mailbox.EnemyHKDAttach ||
		ks[0].Quiet[KindKI] != 5 || ks[0].Invuln[KindBB] != 3 {
		t.Fatalf("block gen %d hkd %v, table %+v", bs[0].KnobGen, bs[0].HkdHi, ks[0])
	}
	store(t, m, mailbox.EnemyKnobAddr+mailbox.EnemyKnobGenTailOff, []byte{0xDE, 0xAD, 0xBE, 0xEF})
	relEcho(t, m, bs[0].Seq, 0)
	for i := range 3 {
		now = now.Add(50 * time.Millisecond)
		pushState(t, c, hostState(uint32(2+i), 1050+50*uint32(i)), now)
		tick()
		if _, _, err := c.Tick(now); err != nil {
			t.Fatal(err)
		}
	}
	if c.knob.Rewrites != 1 || len(knobWrites(t, m)) != 2 {
		t.Errorf("%d rewrites, %d tables: want one", c.knob.Rewrites, len(knobWrites(t, m)))
	}
	if _, ok, _ := mailbox.ReadEnemyKnobs(m, gen, int(len(Kinds))); !ok {
		t.Error("the table in RAM is still not valid for the gen")
	}
	for _, b := range blockWrites(t, m) {
		if b.KnobGen != gen {
			t.Errorf("block seq %d names gen %d, want %d", b.Seq, b.KnobGen, gen)
		}
	}
}

// TestHitApplierKnobTable: the host writes its table before its first
// block (keepalive), names the gen in every block, and rewrites once on a
// mismatching echo of an acked block.
func TestHitApplierKnobTable(t *testing.T) {
	m, a, _, _ := hitRig(t)
	o, err := ParseOverrides("tn:tg_hit_up=70,am:tg_hit_up=90,bb:tg_hit_up=40,bb:hooks=handback")
	if err != nil {
		t.Fatal(err)
	}
	a.Annex, a.HkdHi, a.Knobs = o.Annex, o.HkdHi, o.Knobs
	t0 := time.Unix(1000, 0)
	sea := Scope{Stage: "sea", Room: 5, InPlay: true}
	if err := a.Keepalive(sea, t0); err != nil {
		t.Fatal(err)
	}
	ks, bs := knobWrites(t, m), hostBlocks(t, m)
	if len(ks) != 1 || len(bs) != 1 || m.Writes()[0].Addr != mailbox.EnemyKnobAddr {
		t.Fatalf("%d tables, %d blocks", len(ks), len(bs))
	}
	if ks[0].TgUp[KindAM] != 90 || ks[0].TgUp[KindBB] != 40 || ks[0].TgUp[KindTN] != 0 || bs[0].Annex.TnTgHitUp != 70 ||
		bs[0].KnobGen != ks[0].Gen || bs[0].HkdHi[KindBB-7] != mailbox.EnemyHKDSpecial {
		t.Errorf("table %+v block gen %d hkd %v tn %d", ks[0], bs[0].KnobGen, bs[0].HkdHi, bs[0].Annex.TnTgHitUp)
	}
	relEcho(t, m, bs[0].Seq, ks[0].Gen^1)
	for i := 1; i <= 3; i++ {
		if err := a.Keepalive(sea, t0.Add(time.Duration(i)*KeepaliveEvery)); err != nil {
			t.Fatal(err)
		}
	}
	if a.knob.Rewrites != 1 || len(knobWrites(t, m)) != 2 || len(hostBlocks(t, m)) != 4 {
		t.Errorf("%d rewrites, %d tables, %d blocks", a.knob.Rewrites, len(knobWrites(t, m)), len(hostBlocks(t, m)))
	}
}

// TestParseAnnexL4 (batch C go_changes): ids 7..14's hooks= land in the
// block's hook bytes (bb:hooks=tail at +0x28 bit 0x02), ids 3..15's
// quiet/invuln/tg_hit_up in the knob table (ki:quiet=5 is accepted now),
// tn:tg_hit_up stays the annex's u8, ids 0..2 stay in the annex, and the
// per-kind names are each kind's only. mt (the reserved id 8, cut after
// P0-MT) is refused, so its hook byte +0x29 is never written.
func TestParseAnnexL4(t *testing.T) {
	o, err := ParseOverrides("bb:hooks=tail,ki:quiet=5,am:tg_hit_up=150,am2:invuln=40,tn:tg_hit_up=72,tn:quiet=30," +
		"bb:hooks=beak+handback,bb:quiet=9,bk:hooks=ambush,cc:quiet=12")
	if err != nil {
		t.Fatal(err)
	}
	var want Overrides
	want.HkdHi[KindBB-7] = mailbox.EnemyHKDEye | mailbox.EnemyHKDSpecial // the later entry wins
	want.Knobs.Quiet[KindKI], want.Knobs.Quiet[KindTN], want.Knobs.Quiet[KindBB] = 5, 30, 9
	want.Knobs.TgUp[KindAM] = 150
	want.Knobs.Invuln[KindAM2] = 40
	want.Annex.TnTgHitUp = 72
	want.Annex.Override[KindBK].HookDisable = mailbox.EnemyHKDSpecial
	want.Annex.Override[KindCC].QuietFrames = 12
	if o != want {
		t.Errorf("parsed %+v\nwant %+v", o, want)
	}
	if o, err := ParseOverrides("bb:hooks=tail"); err != nil || o.HkdHi[0] != 0x02 ||
		mailbox.EnemyHkdHiOff+int(KindBB-7) != 0x28 {
		t.Errorf("bb:hooks=tail: %v %v (want +0x28 bit 0x02)", o.HkdHi, err)
	}
	if a, err := ParseAnnex("bb:hooks=tail,ki:quiet=5"); err != nil || a != (mailbox.EnemyAnnex{}) {
		t.Errorf("ParseAnnex drops the layout-4 fields: %+v %v", a, err)
	}
	for _, v := range []string{"bb:hooks=shield", "tn:hooks=tail", "bk:hooks=custody", "mt:hooks=beak", "bb:speed=1", "tn:tg_hit_up=0"} {
		if _, err := ParseOverrides(v); err == nil {
			t.Errorf("%q parsed", v)
		}
	}
	if _, err := ParseOverrides("bb:hooks=tial"); err == nil || !strings.Contains(err.Error(), "tail, beak, mtx, handback") {
		t.Errorf("bb:hooks=tial: %v, want bb's hook names", err)
	}
	// The MT cut (R-20): no row, so mt:hooks is refused with the reason.
	if _, ok := KindByName("mt"); ok {
		t.Error("an MT row after the cut")
	}
	if _, err := ParseOverrides("mt:hooks=chain"); err == nil || !strings.Contains(err.Error(), "P0-MT") {
		t.Errorf("mt:hooks: %v, want the MT cut's refusal", err)
	}
}
