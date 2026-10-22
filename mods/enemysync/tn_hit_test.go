package enemysync

import (
	"bytes"
	"encoding/binary"
	"strings"
	"testing"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/log"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mailbox"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mem"
)

// The TN batch's B1 Go side beyond the row (enemy-mirror-tn.json
// go_changes, hit_path, am2_fix; CONTRACT.md section 4): the Darknut's
// hit forwarding, dormant and play-skip rules, its ATTRIB exclusion
// (D12), the AM2 direct apply's verdict through HIT byte 54 (R-14,
// R-15), and the tn:tg_hit_up annex knob (R-13).

// TestForwardHitTN: a Darknut's head and Tg hits go to the host for every
// At type but the Hookshot (go_changes client.go); the other kinds'
// filters are unchanged (TestForwardHit).
func TestForwardHitTN(t *testing.T) {
	const sword, bomb = uint32(1 << 1), uint32(1 << 5)
	for _, c := range []struct {
		collider uint8
		atType   uint32
		want     bool
	}{
		{HitHead, sword, true},
		{HitHead, AtTypeSkullHammer, true},
		{HitHead, AtTypeHookshot, false},
		{HitTg, sword, true},
		{HitTg, bomb, true},
		{HitTg, AtTypeGrapplingHook, true},
		{HitTg, AtTypeHookshot, false},
		{HitTg, 0, true},
	} {
		if got := ForwardHit(KindTN, c.collider, c.atType); got != c.want {
			t.Errorf("ForwardHit(tn, %d, 0x%08X) = %v, want %v", c.collider, c.atType, got, c.want)
		}
	}
	// An AM2 front verdict still goes (the host logs HDFront): ForwardHit
	// does not look at ClientFlags.
	if !ForwardHit(KindAM2, HitHead, sword) {
		t.Error("an AM2 weak-spot sword hit is not forwarded")
	}
}

// TestDormantPlaySkipTN: a Darknut is never dormant (go_changes host.go),
// and its PLAY_SKIP is TnF2PlaySkip (smooth.go).
func TestDormantPlaySkipTN(t *testing.T) {
	h := NewHost(mem.NewFakeMem(), log.Discard{}, 7, time.Unix(1000, 0))
	e := &Enemy{Kind: KindTN, Ident: Ident{Proc: addr.ProcTN, SetID: 1, PKey: 0x1234}}
	for _, set := range []func(*KindSample){
		func(s *KindSample) {},
		func(s *KindSample) { s.St.Dormant = true },
		func(s *KindSample) { s.Attn = 0 },
	} {
		ks := KindSample{Kind: KindTN, Ident: e.Ident}
		set(&ks)
		if s := h.wireSlot(e, &ks, time.Unix(1000, 0)); s.WFlags&WFlagDormant != 0 || dormant(KindTN, &ks, 0) {
			t.Errorf("tn dormant: %+v", ks)
		}
	}
	if !playSkip(&Slot{Kind: KindTN, Flags2: TnF2PlaySkip}) || playSkip(&Slot{Kind: KindTN, Flags2: TnF2HasWeapon | TnF2Attn}) {
		t.Error("tn playSkip")
	}
	sl := &Slot{Kind: KindTN, Flags2: TnF2PlaySkip, MorfFrame: 7, MorfRate: 1}
	if f := advanceFrame(sl, 100); f != 7 {
		t.Errorf("a play-skipped tn frame advanced to %v", f)
	}
}

// hitRigKind is hitRig with slot 0 (setID 0, handle 1, no flags) of kind
// id, and the ATTRIB hook set to return attrib.
func hitRigKind(t *testing.T, id KindID, attrib uint8) (*mem.FakeMem, *HitApplier, *State, *Listing) {
	t.Helper()
	m, a, st, l := hitRig(t)
	st.Slots[0].Kind = id
	l.Entries[0].Slot.Kind = id
	a.Attrib = func(*Hit) uint8 { return attrib }
	return m, a, st, l
}

// TestHitApplierTN: a Darknut's hit never carries the remote-Link ATTRIB
// bits (D12); a Bokoblin's does, and so does an Armos's (the REL's direct
// branch runs before ATTRIB). The kind is the host slot's, never the
// client's: ClientFlags on a non-AM2 hit set nothing.
func TestHitApplierTN(t *testing.T) {
	const attrib = mailbox.RemoteHAFAttribBody | mailbox.RemoteHAFSlot1
	for _, c := range []struct {
		kind  KindID
		cf    uint8
		col   uint8
		want  uint8
		label string
	}{
		{KindTN, 0, HitHead, 0, "tn head: no ATTRIB"},
		{KindTN, 0, HitTg, 0, "tn tg: no ATTRIB"},
		{KindTN, HitCFVerdict | HitCFBack, HitHead, 0, "tn with stray ClientFlags"},
		{KindBK, 0, HitHead, attrib, "bk keeps ATTRIB"},
		{KindBK, HitCFVerdict | HitCFBack, HitHead, attrib, "bk with stray ClientFlags"},
		{KindAM2, 0, HitHead, attrib, "am2 without a verdict"},
		{KindAM2, HitCFVerdict, HitTg, attrib, "am2 body: no verdict bits"},
		{KindAM2, HitCFVerdict, HitHead, attrib | mailbox.EnemyHAFVerdict, "am2 front verdict"},
		{KindAM2, HitCFVerdict | HitCFBack, HitHead, attrib | mailbox.EnemyHAFVerdict | mailbox.EnemyHAFBack, "am2 back verdict"},
		{KindAM2, HitCFBack, HitHead, attrib, "am2 back without verdict"},
	} {
		m, a, st, l := hitRigKind(t, c.kind, attrib)
		h := hostHit(1, 0, 1)
		h.Collider, h.ClientFlags = c.col, c.cf
		if ack := a.Offer(h, st, l, time.Unix(1000, 0)); ack != nil {
			t.Fatalf("%s: %+v", c.label, ack)
		}
		if blks := hostBlocks(t, m); len(blks) != 1 || blks[0].HitApply.Flags != c.want {
			t.Errorf("%s: flags 0x%02X, want 0x%02X", c.label, blks[len(blks)-1].HitApply.Flags, c.want)
		}
	}
}

// TestHitApplierAM2Crit: an AM2 verdict hit carries EnemyHAFCrit when its
// At type and cut type make week_atari_check's m2CE 1 (a crit sword cut)
// or 8 (the hammer's side swing; d_a_am2.cpp:236-280).
func TestHitApplierAM2Crit(t *testing.T) {
	const sword, machete, dnSword, moblinSpear, unk800 = uint32(0x2), uint32(0x400), uint32(0x04000000), uint32(0x10000000), uint32(0x800)
	const bokoStick, arrow, bomb = uint32(0x80), uint32(0x4000), uint32(0x20)
	for _, c := range []struct {
		at   uint32
		cut  uint8
		want bool
	}{
		{sword, 0x06, true}, {sword, 0x07, true}, {sword, 0x08, true}, {sword, 0x09, true}, {sword, 0x0A, true},
		{sword, 0x0C, true}, {sword, 0x0E, true}, {sword, 0x0F, true}, {sword, 0x10, true}, {sword, 0x15, true},
		{sword, 0x19, true}, {sword, 0x1A, true}, {sword, 0x1B, true}, {sword, 0x1E, true}, {sword, 0x1F, true},
		{sword, 0x00, false}, {sword, 0x01, false}, {sword, 0x02, false}, {sword, 0x05, false}, {sword, 0x0B, false},
		{sword, 0x0D, false}, {sword, 0x11, false}, {sword, 0x16, false}, {sword, 0x1C, false}, {sword, 0x1D, false},
		{machete, 0x0E, true}, {dnSword, 0x08, true}, {moblinSpear, 0x19, true}, {unk800, 0x06, true},
		{0, 0x08, true}, // an unread hit object: the REL's em_am2_m2ce takes it as the sword
		{AtTypeSkullHammer, CutTypeHammerSide, true}, {AtTypeSkullHammer, 0x12, false}, {AtTypeSkullHammer, 0x08, false},
		{bokoStick, 0x08, false}, {arrow, 0x08, false}, {bomb, 0x08, false},
	} {
		if got := am2Crit(c.at, c.cut); got != c.want {
			t.Errorf("am2Crit(0x%08X, 0x%02X) = %v, want %v", c.at, c.cut, got, c.want)
		}
		m, a, st, l := hitRigKind(t, KindAM2, 0)
		a.Attrib = nil
		h := hostHit(1, 0, 1)
		h.AtType, h.CutType, h.ClientFlags = c.at, c.cut, HitCFVerdict|HitCFBack
		if ack := a.Offer(h, st, l, time.Unix(1000, 0)); ack != nil {
			t.Fatalf("0x%08X/0x%02X: %+v", c.at, c.cut, ack)
		}
		want := mailbox.EnemyHAFVerdict | mailbox.EnemyHAFBack
		if c.want {
			want |= mailbox.EnemyHAFCrit
		}
		if blks := hostBlocks(t, m); len(blks) != 1 || blks[0].HitApply.Flags != want {
			t.Errorf("0x%08X/0x%02X: flags 0x%02X, want 0x%02X", c.at, c.cut, blks[0].HitApply.Flags, want)
		}
	}
	// No verdict, no crit bit: the old path's flags are unchanged.
	m, a, st, l := hitRigKind(t, KindAM2, 0)
	a.Attrib = nil
	h := hostHit(1, 0, 1)
	h.AtType, h.CutType = 0x2, 0x08
	a.Offer(h, st, l, time.Unix(1000, 0))
	if blks := hostBlocks(t, m); len(blks) != 1 || blks[0].HitApply.Flags != 0 {
		t.Errorf("a crit cut without a verdict: %+v", blks)
	}
}

// TestAm2CritPins: Go's crit decision reads the same At classes the REL's
// em_am2_m2ce does (wwenemy_kinds.h), so the two cannot drift.
func TestAm2CritPins(t *testing.T) {
	_, defs := kindsHeader(t)
	for name, want := range map[string]int64{
		"EMK_AT_SWORD_CLASS": int64(AtTypeSwordClass), "EMK_AT_SKULL_HAMMER": int64(AtTypeSkullHammer),
		"EMK_AM2_M2CE_CRIT": 1, "EMK_AM2_M2CE_HAMMER": 7, "EMK_AM2_M2CE_HAMMER_SIDE": 8,
	} {
		if got, ok := defs[name]; !ok || got != want {
			t.Errorf("%s = 0x%X (defined %v), Go 0x%X", name, got, ok, want)
		}
	}
	if defs["EMK_AT_OLD_PATH"]&(defs["EMK_AT_SWORD_CLASS"]|defs["EMK_AT_SKULL_HAMMER"]) != 0 {
		t.Error("an old-path At type overlaps the crit classes")
	}
}

// TestHitClientFlags: the client's ClientFlags from the REL's pending
// byte (R-14): AM2 collider 1 only, "pending" any nonzero value.
func TestHitClientFlags(t *testing.T) {
	const p, v, b = mailbox.EnemyHPPending, mailbox.EnemyHPVerdict, mailbox.EnemyHPBack
	for _, c := range []struct {
		kind     KindID
		collider uint8
		pending  uint8
		want     uint8
	}{
		{KindAM2, HitHead, p, 0},
		{KindAM2, HitHead, p | v, HitCFVerdict},
		{KindAM2, HitHead, p | v | b, HitCFVerdict | HitCFBack},
		{KindAM2, HitHead, p | b, 0}, // back without a verdict means nothing
		{KindAM2, HitTg, p | v | b, 0},
		{KindTN, HitHead, p | v | b, 0},
		{KindAM, HitHead, p | v | b, 0},
	} {
		if got := hitClientFlags(c.kind, c.collider, c.pending); got != c.want {
			t.Errorf("hitClientFlags(%s, %d, 0x%02X) = 0x%02X, want 0x%02X", c.kind, c.collider, c.pending, got, c.want)
		}
	}
}

// TestMirrorReadHitVerdict: readHit carries the AM2 verdict into the HIT
// (byte 54 after EncodeHit) and the host turns it into HAF bits.
func TestMirrorReadHitVerdict(t *testing.T) {
	fm := mem.NewFakeMem()
	c := NewMirror(fm, log.Discard{}, nil, false, time.Unix(2000, 0))
	const actor, pid, obj = uint32(0x80C00000), uint32(0x1234), uint32(0x80D00000)
	c.Stats.LastStatus.Status[0] = mailbox.EnemySlotStatus{Actor: actor, PID: pid, State: mailbox.EnemyStateMirrored}
	store(t, fm, actor+addr.Am2OffWeakSph+addr.Mo2CcTgHitObjOff, binary.BigEndian.AppendUint32(nil, obj))
	store(t, fm, obj+0x10, binary.BigEndian.AppendUint32(nil, 1<<1)) // AT_TYPE_SWORD
	st := hostState(1, 0)
	for _, x := range []struct {
		pending uint8
		want    uint8
	}{
		{mailbox.EnemyHPPending, 0},
		{mailbox.EnemyHPPending | mailbox.EnemyHPVerdict, HitCFVerdict},
		{mailbox.EnemyHPPending | mailbox.EnemyHPVerdict | mailbox.EnemyHPBack, HitCFVerdict | HitCFBack},
	} {
		h, err := c.readHit(st, &st.Slots[0], pid, KindAM2, 0, mailbox.EnemyHitEntry{HitSeq: 5, Collider: HitHead, Pending: x.pending})
		if err != nil || h == nil || h.ClientFlags != x.want {
			t.Fatalf("pending 0x%02X: %+v %v, want ClientFlags 0x%02X", x.pending, h, err, x.want)
		}
		b, err := EncodeHit(h)
		if err != nil || b[54] != x.want {
			t.Fatalf("byte 54 0x%02X %v", b[54], err)
		}
		wh, err := DecodeHit(b)
		if err != nil || wh.ClientFlags != x.want {
			t.Fatalf("decoded %+v %v", wh, err)
		}
	}
}

// TestHitClientFlagsWire: ClientFlags is HIT byte 54; 0 leaves the tail
// as wire v4 had it (TestHitV3Tail).
func TestHitClientFlagsWire(t *testing.T) {
	h := &Hit{Stage: "Cave09", Room: 7, SetID: 1, Handle: 0x0102, CutType: 0x08, ClientFlags: HitCFVerdict | HitCFBack}
	b, err := EncodeHit(h)
	if err != nil {
		t.Fatal(err)
	}
	if len(b) != HitLen || b[54] != 0x03 || b[55] != 0 || b[52] != 0x01 || b[53] != 0x02 {
		t.Fatalf("tail % x", b[48:])
	}
	got, err := DecodeHit(b)
	if err != nil || got.ClientFlags != h.ClientFlags || got.Handle != h.Handle || got.CutType != 0x08 {
		t.Errorf("round trip %+v %v", got, err)
	}
	h.ClientFlags = 0
	b0, _ := EncodeHit(h)
	if b0[54] != 0 || !bytes.Equal(b0[:54], b[:54]) {
		t.Errorf("ClientFlags 0 changed other bytes: % x", b0)
	}
}

// TestParseAnnexTN: tn takes hooks= (into HookDisableX[3], by the generic
// names and its own aliases) and tg_hit_up=1..255 (the u8 at 0x1FB, R-13);
// quiet and invuln go to the knob table (layout 4), and the aliases are
// tn's only.
func TestParseAnnexTN(t *testing.T) {
	a, err := ParseAnnex("tn:hooks=shield+coll,tn:tg_hit_up=72,am2:hooks=special")
	if err != nil {
		t.Fatal(err)
	}
	var want mailbox.EnemyAnnex
	want.HookDisableX[KindTN-mailbox.EnemyAnnexKinds] = mailbox.EnemyHKDModelEq | mailbox.EnemyHKDLegacyDeath
	want.HookDisableX[KindAM2-mailbox.EnemyAnnexKinds] = mailbox.EnemyHKDSpecial
	want.TnTgHitUp = 72
	if a != want {
		t.Errorf("parsed %+v, want %+v", a, want)
	}
	for v, bits := range map[string]uint8{
		"tn:hooks=model_eq+attach+eye+legacy_death+attn+mtx_plain+special": 0x7F,
		"tn:hooks=sword+adopt_anm": mailbox.EnemyHKDEye | mailbox.EnemyHKDMtxPlain,
		"tn:hooks=0x80":            0x80,
	} {
		if a, err := ParseAnnex(v); err != nil || a.HookDisableX[3] != bits {
			t.Errorf("%q: 0x%02X %v, want 0x%02X", v, a.HookDisableX[3], err, bits)
		}
	}
	for _, v := range []string{"tn:tg_hit_up=0", "tn:tg_hit_up=256", "tn:tg_hit_up=-1",
		"bk:hooks=shield", "am2:hooks=sword", "tn:hooks=wings"} {
		if _, err := ParseAnnex(v); err == nil {
			t.Errorf("%q parsed", v)
		}
	}
	// Layout 4 (batch C): tn's quiet and invuln go to the knob table
	// (TestParseAnnexL4); its tg_hit_up stays the u8 at 0x1FB. A refused
	// hook names tn's own hook names.
	if o, err := ParseOverrides("tn:quiet=30,tn:invuln=5"); err != nil || o.Knobs.Quiet[KindTN] != 30 || o.Knobs.Invuln[KindTN] != 5 {
		t.Errorf("tn:quiet=30,tn:invuln=5: %+v %v", o.Knobs, err)
	}
	if _, err := ParseAnnex("tn:hooks=sheild"); err == nil || !strings.Contains(err.Error(), "shield, sword, coll, adopt_anm") {
		t.Errorf("tn:hooks=sheild: %v, want the tn hook names", err)
	}
	if _, err := ParseAnnex("am:hooks=sheild"); err == nil || strings.Contains(err.Error(), "shield") {
		t.Errorf("am:hooks=sheild: %v, want the generic hook names only", err)
	}
	if a, err := ParseAnnex("bk:tg_hit_up=60"); err != nil || a.TnTgHitUp != 0 || a.Override[KindBK].TgHitUp != 60 {
		t.Errorf("bk:tg_hit_up: %+v %v", a, err)
	}
}

// TestAnnexTnTgHitUpWritten: the knob reaches byte 0x1FB of both sides'
// blocks (the host's hit applier and the client mirror copy the annex
// field by field).
func TestAnnexTnTgHitUpWritten(t *testing.T) {
	m, a, st, l := hitRig(t)
	a.Annex.TnTgHitUp = 68
	if ack := a.Offer(hostHit(1, 0, 1), st, l, time.Unix(1000, 0)); ack != nil {
		t.Fatal(ack)
	}
	blks := hostBlocks(t, m)
	if len(blks) != 1 || blks[0].Annex.TnTgHitUp != 68 {
		t.Errorf("host block annex %+v", blks)
	}
	for _, w := range blockSideWrites(m) {
		if w.Data[mailbox.EnemyAnnexTnTgUpOff-mailbox.EnemyAnnexOff] != 68 {
			t.Error("byte 0x1FB of the host write is not the knob")
		}
	}

	g, cm, _ := kindRoom(t)
	tick := booted(t, g, cm)
	start := time.Unix(2000, 0)
	c := NewMirror(cm, log.Discard{}, nil, false, start)
	c.Annex.TnTgHitUp = 68
	cm.Record(true)
	now := start.Add(time.Second)
	cst := &State{Flags: StateFlagInScope, Epoch: 7, Seq: 1, HostBeat: 9, HostMS: 1000, Stage: "M_Dai", Room: 1, KindHash: KindHash()}
	pushState(t, c, cst, now)
	tick()
	if _, _, err := c.Tick(now); err != nil {
		t.Fatal(err)
	}
	if b := blockWrites(t, cm); len(b) == 0 || b[len(b)-1].Annex.TnTgHitUp != 68 {
		t.Errorf("client block annex %+v", b)
	}
}
