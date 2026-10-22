package enemysync

import (
	"encoding/binary"
	"math"
	"slices"
	"testing"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/internal/inject"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/fakegame"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/log"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mailbox"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mem"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/stage"
)

// booted makes a room rig a client: this build's patch (the contract
// gate) with the REL module linked; the returned tick advances the REL's
// frame counter and the heartbeat, as the game does.
func booted(t *testing.T, g *fakegame.FakeGame, m *mem.FakeMem) func() {
	t.Helper()
	if err := fakegame.Boot(m); err != nil {
		t.Fatal(err)
	}
	rel := mailbox.REL{Linked: true, BuildID: inject.RELBuildID, Frames: 100}
	tick := func() {
		rel.Frames++
		if err := mailbox.WriteREL(gameWriter{m}, rel); err != nil {
			t.Fatal(err)
		}
		g.Frame()
	}
	tick()
	return tick
}

// localIdent is the Ident Candidates gives the live actor of pid.
func localIdent(t *testing.T, m mem.Reader, set string, pid uint32) Ident {
	t.Helper()
	s, err := ParseKinds(set)
	if err != nil {
		t.Fatal(err)
	}
	es, err := Candidates(m, "", 1, s)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range es {
		if e.PID == pid {
			return e.Ident
		}
	}
	t.Fatalf("pid %d is no %s candidate", pid, set)
	return Ident{}
}

// slotByPID finds a block slot by its pid.
func slotByPID(b *mailbox.EnemyBlock, pid uint32) (int, *mailbox.EnemySlot) {
	for j := range b.Slots {
		if b.Slots[j].PID == pid {
			return j, &b.Slots[j]
		}
	}
	return -1, nil
}

// TestAnnexSpan: a Bokoblin and a ChuChu mirrored: one write per tick from
// the annex (MAILBOX+0x1C8) through seq_tail, the annex before seq_head and
// its annex_seq the block's seq; each slot's aux in its block slot's annex
// entry (BK m48C.z and the jaw, CC shape_angle.x and m310), the ChuChu's
// m316.z in the attack bytes, the local actors' pkeys and procs, SF2_QUIET
// (WW_ENEMY_QUIET_DEATH's default bk,cc), and the block's kind mask = the
// kinds both sides enable.
func TestAnnexSpan(t *testing.T) {
	g, m, es := kindRoom(t)
	tick := booted(t, g, m)
	bk, _ := es.Of(0) // BK setID 0, the torch type
	cc, _ := es.Of(3) // CC setID 4, red
	start := time.Unix(2000, 0)
	c := NewMirror(m, log.Discard{}, nil, false, start)
	c.Kinds, _ = ParseKinds("mo2,bk,cc")
	m.Record(true)
	now := start.Add(time.Second)
	st := &State{Flags: StateFlagInScope, Epoch: 7, Seq: 1, HostBeat: 9, HostMS: 1000, Stage: "M_Dai", Room: 1,
		HostKindMask: 1<<KindBK | 1<<KindCC, KindHash: KindHash()}
	bs := Slot{SetID: 0, Proc: addr.ProcBK, Kind: KindBK, Handle: 1, Health: 5, Flags2: BkF2HasWeapon, Aux: [4]int16{11, 22, 0, 0}, MorfRate: 1}
	cs := Slot{SetID: 4, Proc: addr.ProcCC, Kind: KindCC, Handle: 2, Health: 3, Flags2: CcF2Tg | CcF2Squash, Aux: [4]int16{1, 2, 3, 4}, Pos: [3]float32{50, 0, 0}}
	cs.SetCcM316Z(-5)
	st.Slots = []Slot{bs, cs}
	pushState(t, c, st, now)
	tick()
	if _, _, err := c.Tick(now); err != nil {
		t.Fatal(err)
	}
	ws := blockSideWrites(m)
	if len(ws) != 1 || ws[0].Addr != mailbox.EnemyAnnexAddr || len(ws[0].Data) != mailbox.EnemyWriteSpan {
		t.Fatalf("writes: %d, first at 0x%08X", len(ws), ws[0].Addr)
	}
	if seqHead := mailbox.EnemyAnnexSize + mailbox.EnemySeqHeadOff; mailbox.EnemyAnnexSeqOff-mailbox.EnemyAnnexOff >= seqHead {
		t.Errorf("annex_seq at +0x%X is not before seq_head at +0x%X", mailbox.EnemyAnnexSeqOff-mailbox.EnemyAnnexOff, seqHead)
	}
	blk, tail, annexSeq, err := mailbox.DecodeEnemyWrite(ws[0].Data)
	if err != nil || tail != blk.Seq || annexSeq != blk.Seq {
		t.Fatalf("write: %v tail %d annex_seq %d seq %d", err, tail, annexSeq, blk.Seq)
	}
	if blk.KindMask != 1<<KindBK|1<<KindCC {
		t.Errorf("kind mask %03b", blk.KindMask)
	}
	jb, sb := slotByPID(&blk, bk.PID)
	jc, sc := slotByPID(&blk, cc.PID)
	if sb == nil || sc == nil {
		t.Fatalf("slots: %+v", blk.Slots)
	}
	if want := localIdent(t, m, "bk", bk.PID); sb.PKey != want.PKey || sb.Proc != addr.ProcBK || sb.Flags2 != BkF2HasWeapon|mailbox.EnemySF2Quiet {
		t.Errorf("BK slot %+v, want pkey 0x%08X", *sb, want.PKey)
	}
	if want := localIdent(t, m, "cc", cc.PID); sc.PKey != want.PKey || sc.Proc != addr.ProcCC || sc.CcM316Z() != -5 || sc.Flags2 != CcF2Tg|CcF2Squash|mailbox.EnemySF2Quiet {
		t.Errorf("CC slot %+v, want pkey 0x%08X", *sc, want.PKey)
	}
	if blk.Annex.Aux[jb] != [4]int16{11, 22, 0, 0} || blk.Annex.Aux[jc] != [4]int16{1, 2, 3, 4} {
		t.Errorf("annex aux %v", blk.Annex.Aux)
	}
	// The client enables Moblins only: neither is mirrored, the mask is 0.
	c.Kinds, _ = ParseKinds("mo2")
	m.ResetWrites()
	now = now.Add(16 * time.Millisecond)
	st.Seq, st.HostMS = 2, 1016
	pushState(t, c, st, now)
	tick()
	if _, _, err := c.Tick(now); err != nil {
		t.Fatal(err)
	}
	if b := blockWrites(t, m); len(b) != 1 || len(b[0].Slots) != 0 || b[0].KindMask != 0 {
		t.Errorf("mo2 only: %+v", b)
	}
}

// deadRoom is M_Dai room 1 with keyed Moblins setID 0 and 81 (the highest
// keyed ALLOW setID measured is 81: past DeadMask's eight bits).
func deadRoom(t *testing.T) (*fakegame.FakeGame, *mem.FakeMem, *fakegame.Enemies) {
	t.Helper()
	m := mem.NewFakeMem()
	g := fakegame.New(m)
	if err := g.LoadStage(mDai); err != nil {
		t.Fatal(err)
	}
	if err := g.EnterRoom(1, 2); err != nil {
		t.Fatal(err)
	}
	es := fakegame.PlaceEnemies(
		fakegame.Enemy{Stage: mDai.Name, Room: 1, SetID: 0, Proc: stage.ProcMO2},
		fakegame.Enemy{Stage: mDai.Name, Room: 1, SetID: 81, Proc: stage.ProcMO2},
	)
	frames(t, g, es, 4)
	return g, m, es
}

// TestDeadWords: ReadDeadWords reads the room's zone words 0..3 (setID 81
// is word 2 bit 17), the STATE carries them, and a client whose setID 81
// still lives, not listed by the host, takes a quiet catch-up slot for it
// (DYING|RELEASE, SF2_QUIET: a Moblin too) and claims it.
func TestDeadWords(t *testing.T) {
	g, m, es := deadRoom(t)
	if err := g.OnActor(81, 1); err != nil {
		t.Fatal(err)
	}
	w, err := ReadDeadWords(m, 1)
	if err != nil || w != [DeadWords]uint32{2: 1 << 17} {
		t.Fatalf("dead words %08X %v", w, err)
	}
	h := NewHost(m, log.Discard{}, 7, time.Unix(1000, 0))
	hs, err := h.Tick(time.Unix(1000, 0))
	if err != nil || hs.Dead != w || !hs.IsDead(81) || hs.IsDead(80) || hs.DeadMask != 0 {
		t.Fatalf("host STATE dead %08X %v", hs.Dead, err)
	}

	tick := booted(t, g, m)
	a81, _ := es.Of(1)
	start := time.Unix(2000, 0)
	c := NewMirror(m, log.Discard{}, nil, false, start)
	c.Claims = NewClaimSet()
	m.Record(true)
	now := start.Add(time.Second)
	st := &State{Flags: StateFlagInScope, Epoch: 7, Seq: 1, HostMS: 1000, Stage: "M_Dai", Room: 1, HostKindMask: 1 << KindMO2, KindHash: KindHash()}
	st.SetDead(w)
	pushState(t, c, st, now)
	tick()
	if _, _, err := c.Tick(now); err != nil {
		t.Fatal(err)
	}
	b := blockWrites(t, m)
	if len(b) != 1 {
		t.Fatalf("%d blocks", len(b))
	}
	_, s := slotByPID(&b[0], a81.PID)
	if s == nil || s.Flags != mailbox.EnemySFDying|mailbox.EnemySFRelease || s.Flags2 != mailbox.EnemySF2Quiet || s.Proc != ProcMO2 ||
		s.PKey != localIdent(t, m, "mo2", a81.PID).PKey {
		t.Errorf("setID 81 catch-up: %+v", b[0].Slots)
	}
	if !c.Claims.Claimed(a81.PID) {
		t.Error("the catch-up's pid is not claimed")
	}
}

// TestMirrorQuietDeath (WW_ENEMY_QUIET_DEATH=mo2): a dying copy's slot is
// pinned and quiet; it stays (DYING|RELEASE from its last pose) after the
// host stops listing it, until the REL reports STATE_DEAD; then the slot
// frees, the death switch the copy carried is set (no co-op switch sync),
// and the keyed zone bit waits until the host's dead words carry it.
func TestMirrorQuietDeath(t *testing.T) {
	_, m, es, tick := clientRoom(t)
	a0, _ := es.Of(1) // setID 0
	store(t, m, a0.Addr+addr.Mo2OffDeathSwitch, []byte{0x05})
	start := time.Unix(2000, 0)
	c := NewMirror(m, log.Discard{}, nil, false, start)
	c.Quiet, _ = ParseQuietKinds("mo2,bk,cc")
	c.Claims = NewClaimSet()
	m.Record(true)
	now := start.Add(time.Second)
	step := func(st *State) mailbox.EnemyBlock {
		t.Helper()
		now = now.Add(16 * time.Millisecond)
		st.HostMS = uint32(now.Sub(start).Milliseconds())
		pushState(t, c, st, now)
		tick()
		m.ResetWrites()
		if _, _, err := c.Tick(now); err != nil {
			t.Fatal(err)
		}
		b := blockWrites(t, m)
		if len(b) != 1 {
			t.Fatalf("%d blocks", len(b))
		}
		return b[0]
	}
	st := hostState(1, 0)
	st.Slots[0].Flags |= SlotDying
	b := step(st)
	if j, s := slotByPID(&b, a0.PID); j != 0 || s.Flags&mailbox.EnemySFDying == 0 || s.Flags2&mailbox.EnemySF2Quiet == 0 || !c.blk[0].Pinned || c.blk[0].sw != 5 {
		t.Fatalf("dying: slot %d %+v blk %+v", j, s, c.blk[0])
	}
	// The host no longer lists it (its tombstone gone): pinned, released.
	st = hostState(2, 0)
	st.Slots = st.Slots[1:]
	b = step(st)
	if j, s := slotByPID(&b, a0.PID); j != 0 || s.Flags != mailbox.EnemySFDying|mailbox.EnemySFRelease || s.Flags2 != mailbox.EnemySF2Quiet {
		t.Fatalf("unlisted: slot %d %+v", j, s)
	}
	// STATE_DEAD: the slot frees, the switch is set, the zone bit waits.
	stOff := mailbox.EnemyBlockAddr + mailbox.EnemyStatusOff
	store(t, m, stOff+mailbox.EnemySTPIDOff, binary.BigEndian.AppendUint32(nil, a0.PID))
	store(t, m, stOff+mailbox.EnemySTStateOff, []byte{mailbox.EnemyStateDead})
	st = hostState(3, 0)
	st.Slots = st.Slots[1:]
	b = step(st)
	if j, _ := slotByPID(&b, a0.PID); j >= 0 || c.Stats.Deaths != 1 || c.Stats.Switches != 1 || c.Stats.ZoneBits != 0 {
		t.Fatalf("after STATE_DEAD: slot %d stats %+v", j, c.Stats)
	}
	_, sw, _ := addr.SwitchBit(5)
	if v, _ := mem.ReadU8(m, addr.LiveMemBit+uint32(sw.Off)); v&sw.Mask == 0 {
		t.Error("the death switch was not set")
	}
	if mask, _ := DeadMask(m, 1); mask != 0 {
		t.Errorf("a zone bit the host's dead words lack: %08b", mask)
	}
	if c.Claims.Claimed(a0.PID) {
		t.Error("a copy dead here is still claimed")
	}
	// The host's dead words carry it: the bit is ORed, once.
	st = hostState(4, 0)
	st.Slots = st.Slots[1:]
	st.SetDead([DeadWords]uint32{1})
	b = step(st)
	if mask, _ := DeadMask(m, 1); mask != 1 || c.Stats.ZoneBits != 1 {
		t.Errorf("zone bit %08b stats %+v", mask, c.Stats)
	}
	if j, _ := slotByPID(&b, a0.PID); j >= 0 {
		t.Error("a copy dead here was caught up again")
	}
}

// TestMirrorKindGates: a STATE from another build's kind table is refused
// (the mirror disarms); a slot whose kind is not its proc's is refused; a
// host that does not enable Moblins gets none mirrored; the claimed pids
// are the matched ones, and nothing once disarmed.
func TestMirrorKindGates(t *testing.T) {
	_, m, es, tick := clientRoom(t)
	start := time.Unix(2000, 0)
	c := NewMirror(m, log.Discard{}, nil, false, start)
	c.Kinds, _ = ParseKinds("mo2") // this game mirrors Moblins only
	c.Claims = NewClaimSet()
	m.Record(true)
	now := start
	st := hostState(1, 0)
	st.Slots[1].Kind = KindBK // setID 1 sent as a Bokoblin
	pushState(t, c, st, now)
	tick()
	if _, sleep, err := c.Tick(now); err != nil || sleep != 0 {
		t.Fatalf("tick: %v %v", sleep, err)
	}
	a0, _ := es.Of(1)
	a1, _ := es.Of(2)
	a2, _ := es.Of(0)
	b := blockWrites(t, m)
	if _, s := slotByPID(&b[0], a1.PID); s != nil || len(b[0].Slots) != 2 {
		t.Errorf("a kind mismatch mirrored: %+v", b[0].Slots)
	}
	if !c.Claims.Claimed(a0.PID) || !c.Claims.Claimed(a2.PID) || c.Claims.Claimed(a1.PID) {
		t.Error("claims")
	}
	// The host does not enable Moblins.
	now = now.Add(16 * time.Millisecond)
	st = hostState(2, 16)
	st.HostKindMask = 1 << KindBK
	pushState(t, c, st, now)
	tick()
	m.ResetWrites()
	c.Tick(now)
	if b = blockWrites(t, m); len(b) != 1 || len(b[0].Slots) != 0 || b[0].KindMask != 0 || c.Claims.Claimed(a0.PID) {
		t.Errorf("host without Moblins: %+v", b)
	}
	// Another build's table: disarmed (one count-0 block), nothing claimed.
	now = now.Add(16 * time.Millisecond)
	st = hostState(3, 32)
	st.KindHash ^= 1
	pushState(t, c, st, now)
	tick()
	m.ResetWrites()
	if _, sleep, _ := c.Tick(now); sleep != GateSleep {
		t.Errorf("a foreign kind table was mirrored (sleep %v)", sleep)
	}
	if b = blockWrites(t, m); len(b) != 1 || len(b[0].Slots) != 0 || c.Claims.Claimed(a2.PID) {
		t.Errorf("disarm: %+v", b)
	}
}

// TestHostOrder: the host lists by the least 3D distance to any player
// (its own Link, then the remote players SetPlayers gives), ties by
// Ident; WW_ENEMY_SELECT=legacy keeps the lowest setIDs first.
func TestHostOrder(t *testing.T) {
	_, m, es := moblinRoom(t)
	for i, x := range map[int]float32{1: 3000, 2: 500, 0: 100} { // placed 1 setID 0, 2 setID 1, 0 setID 2
		a, _ := es.Of(i)
		store(t, m, a.Addr+addr.ActorOffCurPos, f32s(x, 999, 0))
	}
	store(t, m, fakegame.PlayerActor+addr.ActorOffCurPos, f32s(0, 0, 0))
	h := NewHost(m, log.Discard{}, 7, time.Unix(1000, 0))
	st, err := h.Tick(time.Unix(1000, 0))
	if err != nil || !eq16(setIDs(st), []uint16{2, 1, 0}) {
		t.Fatalf("by distance to Link: %v %v", setIDs(st), err)
	}
	h.SetPlayers([][3]float32{{2900, 0, 10}})
	if st, _ = h.Tick(time.Unix(1000, 16e6)); !eq16(setIDs(st), []uint16{2, 0, 1}) {
		t.Errorf("with a remote player near setID 0: %v", setIDs(st))
	}
	h.Select = SelectLegacy
	if st, _ = h.Tick(time.Unix(1000, 32e6)); !eq16(setIDs(st), []uint16{0, 1, 2}) {
		t.Errorf("legacy: %v", setIDs(st))
	}
	// Handles follow the Ident, not the order.
	for _, s := range st.Slots {
		if s.Handle != h.HandleOf(Ident{Proc: s.Proc, SetID: s.SetID, PKey: s.PKey}) {
			t.Errorf("setID %d handle %d", s.SetID, s.Handle)
		}
	}
}

// TestHostEngaged: a client HIT or a host PHIT naming a handle
// (NoteEngaged) sets WFlagEngaged on that enemy for EngagedFor.
func TestHostEngaged(t *testing.T) {
	_, m, _ := moblinRoom(t)
	t0 := time.Unix(1000, 0)
	h := NewHost(m, log.Discard{}, 7, t0)
	h.Select = SelectLegacy
	st, _ := h.Tick(t0)
	h.NoteEngaged(st.Slots[1].Handle, t0)
	h.NoteEngaged(999, t0) // unknown: ignored
	st, _ = h.Tick(t0.Add(EngagedFor - time.Millisecond))
	if st.Slots[0].WFlags != 0 || st.Slots[1].WFlags != WFlagEngaged || st.Slots[2].WFlags != 0 {
		t.Errorf("engaged: %02X %02X %02X", st.Slots[0].WFlags, st.Slots[1].WFlags, st.Slots[2].WFlags)
	}
	if st, _ = h.Tick(t0.Add(EngagedFor)); st.Slots[1].WFlags != 0 {
		t.Errorf("still engaged after EngagedFor")
	}
}

// TestHostState30Hz: under WW_ENEMY_STATE_30HZ the host sends a STATE only
// when its REL's frame counter moved, and at least every KeepaliveEvery.
func TestHostState30Hz(t *testing.T) {
	_, m, _ := moblinRoom(t)
	t0 := time.Unix(1000, 0)
	h := NewHost(m, log.Discard{}, 7, t0)
	h.State30Hz = true
	hostRELFrame(t, m, 100)
	st, _ := h.Tick(t0)
	if st == nil || st.Seq != 1 || len(st.Slots) == 0 {
		t.Fatalf("first: %+v", st)
	}
	// A snap marked (a re-created actor) on a tick whose STATE is held back:
	// the one-shot flag waits for the next STATE that goes out (review 2).
	id := h.listing.Entries[0].Ident
	h.snapNext[id] = true
	if st, _ := h.Tick(t0.Add(16 * time.Millisecond)); st != nil {
		t.Errorf("the same REL frame sent again: seq %d", st.Seq)
	}
	hostRELFrame(t, m, 101)
	st, _ = h.Tick(t0.Add(32 * time.Millisecond))
	if st == nil || st.Seq != 2 {
		t.Fatalf("a new REL frame: %+v", st)
	}
	if st.Slots[0].Flags&SlotSnap == 0 || h.snapNext[id] {
		t.Errorf("the snap held back with its STATE was lost: flags %02X, still marked %v", st.Slots[0].Flags, h.snapNext[id])
	}
	if st, _ := h.Tick(t0.Add(32*time.Millisecond + KeepaliveEvery)); st == nil || st.Seq != 3 {
		t.Errorf("no STATE after KeepaliveEvery with the REL still: %+v", st)
	}
}

// TestHostKinds: under WW_ENEMY_MIRROR_KINDS=bk,cc the host lists the
// room's Bokoblins and ChuChus with their kind, aux and handles: a
// Bokoblin's m48C.z and jaw in Aux, SF2 bit 6 (KindF2Light) when it dies
// with its ice timer running, a ChuChu's m316.z in the attack bytes and
// WFlagEngaged while its At Set bit is on, WFlagDormant while its
// attention flags lack LOCKON_BATTLE and it is not carried (a Bokoblin
// never); the host block carries the enabled set as its kind mask.
func TestHostKinds(t *testing.T) {
	_, m, es := kindRoom(t)
	bk, _ := es.Of(0)
	cc, _ := es.Of(3)
	store(t, m, bk.Addr+addr.BkOffTiltZ, []byte{0, 11})
	store(t, m, bk.Addr+addr.BkOffJaw, []byte{0, 22})
	store(t, m, bk.Addr+addr.BkOffDRAction, []byte{0, 20})
	store(t, m, bk.Addr+addr.BkOffIceShrink, []byte{1})
	store(t, m, cc.Addr+addr.CcOffGroundTiltZ, []byte{0xFF, 0xFB})
	store(t, m, cc.Addr+addr.CcOffCyl+addr.CcCylOffAtSPrm, []byte{0, 0, 0, 1})
	store(t, m, cc.Addr+addr.ActorOffAttnFlags, []byte{0, 0, 0, addr.AttnLockonBattle})
	h := NewHost(m, log.Discard{}, 7, time.Unix(1000, 0))
	h.Kinds, _ = ParseKinds("bk,cc")
	h.Select = SelectLegacy
	st, err := h.Tick(time.Unix(1000, 0))
	if err != nil {
		t.Fatal(err)
	}
	if st.HostKindMask != 1<<KindBK|1<<KindCC {
		t.Errorf("host kind mask %03b", st.HostKindMask)
	}
	var sb, sc *Slot
	for i := range st.Slots {
		s := &st.Slots[i]
		switch {
		case s.Proc == addr.ProcBK && s.SetID == 0:
			sb = s
		case s.Proc == addr.ProcCC && s.SetID == 4:
			sc = s
		case s.Proc == addr.ProcMo2:
			t.Errorf("a Moblin listed: %+v", s)
		}
	}
	if sb == nil || sc == nil {
		t.Fatalf("slots: %+v", st.Slots)
	}
	if sb.Kind != KindBK || sb.Aux != [4]int16{11, 22, 0, 0} || sb.Flags&SlotDying == 0 || sb.Flags2&KindF2Light == 0 || sb.Handle == 0 {
		t.Errorf("BK slot %+v", *sb)
	}
	if sc.Kind != KindCC || sc.CcM316Z() != -5 || sc.WFlags != WFlagEngaged || sc.Handle == 0 || sc.Handle == sb.Handle {
		t.Errorf("CC slot %+v", *sc)
	}
	if sb.WFlags&WFlagDormant != 0 { // the fixture Bokoblin's attention flags are 0
		t.Errorf("BK slot dormant: %+v", *sb)
	}
	// The ChuChu without LOCKON_BATTLE: dormant, unless carried.
	store(t, m, cc.Addr+addr.ActorOffAttnFlags, []byte{0, 0, 0, 0})
	store(t, m, cc.Addr+addr.CcOffCyl+addr.CcCylOffAtSPrm, []byte{0, 0, 0, 0})
	h.engaged = map[Ident]time.Time{}
	if st, _ = h.Tick(time.Unix(1000, 16e6)); slotOf2(st, addr.ProcCC, 4).WFlags != WFlagDormant {
		t.Errorf("CC without LOCKON_BATTLE: %+v", slotOf2(st, addr.ProcCC, 4))
	}
	st0 := binary.BigEndian.AppendUint32(nil, addr.ActorStatusCarry)
	store(t, m, cc.Addr+addr.ActorOffStatus, st0)
	if st, _ = h.Tick(time.Unix(1000, 32e6)); slotOf2(st, addr.ProcCC, 4).WFlags != 0 {
		t.Errorf("carried CC: %+v", slotOf2(st, addr.ProcCC, 4))
	}
	a := newHitApplier(m, log.Discard{}, 7)
	a.Kinds = h.Kinds
	a.SetREL(true)
	m.Record(true)
	if err := a.Keepalive(h.Scope(), time.Unix(1000, 0)); err != nil {
		t.Fatal(err)
	}
	if b := hostBlocks(t, m); len(b) != 1 || b[0].KindMask != 1<<KindBK|1<<KindCC {
		t.Errorf("host block: %+v", b)
	}
	// WW_ENEMY_ANNEX's overrides go out in the host block's annex (the REL's
	// em_hit_center and em_hit_tick read them from it).
	if a.Annex, err = ParseAnnex("bk:tg_hit_up=60,cc:invuln=10,am:hooks=special"); err != nil {
		t.Fatal(err)
	}
	m.ResetWrites()
	if err := a.Keepalive(h.Scope(), time.Unix(1001, 0)); err != nil {
		t.Fatal(err)
	}
	if b := hostBlocks(t, m); len(b) != 1 || b[0].Annex.Override[KindBK].TgHitUp != 60 || b[0].Annex.InvulnWait[KindCC] != 10 ||
		b[0].Annex.HookDisableX[KindAM-mailbox.EnemyAnnexKinds] != mailbox.EnemyHKDSpecial {
		t.Errorf("host block annex: %+v", b)
	}
}

// TestParseAnnex: WW_ENEMY_ANNEX's entries land in their kind's override
// row (hook names and numbers ORed), "" and "none" are none, and a bad
// kind, field or value is refused.
func TestParseAnnex(t *testing.T) {
	a, err := ParseAnnex(" BK:tg_hit_up=56, cc:invuln=8 ,cc:quiet=30,bk:hooks=attach+0x20,mo2:hooks=legacy_death")
	if err != nil {
		t.Fatal(err)
	}
	want := mailbox.EnemyAnnex{}
	want.Override[KindBK] = mailbox.EnemyKindOverride{TgHitUp: 56, HookDisable: mailbox.EnemyHKDAttach | mailbox.EnemyHKDMtxPlain}
	want.Override[KindCC] = mailbox.EnemyKindOverride{QuietFrames: 30}
	want.Override[KindMO2] = mailbox.EnemyKindOverride{HookDisable: mailbox.EnemyHKDLegacyDeath}
	want.InvulnWait[KindCC] = 8
	if a != want {
		t.Errorf("parsed %+v, want %+v", a, want)
	}
	for _, v := range []string{"", "none", "None"} {
		if a, err := ParseAnnex(v); err != nil || a != (mailbox.EnemyAnnex{}) {
			t.Errorf("%q: %+v %v", v, a, err)
		}
	}
	for _, v := range []string{"bk", "bk:tg_hit_up", "xx:quiet=1", "bk:speed=1", "bk:quiet=256", "bk:tg_hit_up=40000", "bk:hooks=wings", "cc:invuln=-1"} {
		if _, err := ParseAnnex(v); err == nil {
			t.Errorf("%q parsed", v)
		}
	}
	// The allow kinds take hooks= only, into HookDisableX (allow design
	// D3); their quiet, tg_hit_up and invuln are compiled in the REL.
	a, err = ParseAnnex("ki:hooks=extra,am:hooks=special+eye,am2:hooks=mtx_plain+0x80,bk:hooks=attn")
	if err != nil {
		t.Fatal(err)
	}
	want = mailbox.EnemyAnnex{}
	want.Override[KindBK].HookDisable = mailbox.EnemyHKDAttn
	want.HookDisableX[KindKI-mailbox.EnemyAnnexKinds] = mailbox.EnemyHKDExtra
	want.HookDisableX[KindAM-mailbox.EnemyAnnexKinds] = mailbox.EnemyHKDSpecial | mailbox.EnemyHKDEye
	want.HookDisableX[KindAM2-mailbox.EnemyAnnexKinds] = mailbox.EnemyHKDMtxPlain | mailbox.EnemyHKDExtra
	if a != want {
		t.Errorf("allow hooks: parsed %+v, want %+v", a, want)
	}
	for _, v := range []string{"ki:speed=1", "am:hooks=wings"} {
		if _, err := ParseAnnex(v); err == nil {
			t.Errorf("%q parsed", v)
		}
	}
	// Layout 4 (batch C): their quiet, tg_hit_up and invuln go to the knob
	// table (TestParseAnnexL4), none of it to the annex.
	for _, v := range []string{"ki:quiet=5", "am:tg_hit_up=150", "am2:invuln=40"} {
		if a, err := ParseAnnex(v); err != nil || a != (mailbox.EnemyAnnex{}) {
			t.Errorf("%q: %+v %v, want accepted into the knob table", v, a, err)
		}
	}
}

// TestMirrorCatchUpPin: a fresh catch-up slot starts where its copy
// stands (never a zero Pos: the world origin), carries WW_ENEMY_ANNEX's
// overrides, and its pin survives REFUSED/ERR_GATE (em_validate's runtime
// gates pass again a moment later) but ends at REFUSED/ERR_ICE (the copy's
// own ice timers ran: this game owns the death), with no quiet death
// counted and the copy never caught up again.
func TestMirrorCatchUpPin(t *testing.T) {
	g, m, es := deadRoom(t)
	if err := g.OnActor(81, 1); err != nil {
		t.Fatal(err)
	}
	w, err := ReadDeadWords(m, 1)
	if err != nil {
		t.Fatal(err)
	}
	tick := booted(t, g, m)
	a81, _ := es.Of(1)
	at := [3]float32{120, 5, -340}
	var pos []byte
	for _, v := range at {
		pos = binary.BigEndian.AppendUint32(pos, math.Float32bits(v))
	}
	store(t, m, a81.Addr+addr.ActorOffCurPos, pos)
	store(t, m, a81.Addr+addr.ActorOffCurAngle+2, []byte{0x12, 0x34})
	start := time.Unix(2000, 0)
	c := NewMirror(m, log.Discard{}, nil, false, start)
	if c.Annex, err = ParseAnnex("mo2:quiet=40,ki:hooks=extra"); err != nil {
		t.Fatal(err)
	}
	m.Record(true)
	now := start.Add(time.Second)
	step := func(seq uint32) *mailbox.EnemySlot {
		t.Helper()
		now = now.Add(16 * time.Millisecond)
		st := &State{Flags: StateFlagInScope, Epoch: 7, Seq: seq, HostMS: uint32(now.Sub(start).Milliseconds()), Stage: "M_Dai", Room: 1,
			HostKindMask: 1 << KindMO2, KindHash: KindHash()}
		st.SetDead(w)
		pushState(t, c, st, now)
		tick()
		m.ResetWrites()
		if _, _, err := c.Tick(now); err != nil {
			t.Fatal(err)
		}
		b := blockWrites(t, m)
		if len(b) != 1 {
			t.Fatalf("%d blocks", len(b))
		}
		if b[0].Annex.Override[KindMO2].QuietFrames != 40 || b[0].Annex.HookDisableX[KindKI-mailbox.EnemyAnnexKinds] != mailbox.EnemyHKDExtra {
			t.Errorf("block annex %+v", b[0].Annex)
		}
		_, s := slotByPID(&b[0], a81.PID)
		return s
	}
	s := step(1)
	if s == nil || s.Pos != at || s.AngleY != 0x1234 || s.Flags != mailbox.EnemySFDying|mailbox.EnemySFRelease {
		t.Fatalf("catch-up: %+v, want Pos %v", s, at)
	}
	refuse := func(e uint8) {
		stOff := mailbox.EnemyBlockAddr + mailbox.EnemyStatusOff
		store(t, m, stOff+mailbox.EnemySTPIDOff, binary.BigEndian.AppendUint32(nil, a81.PID))
		store(t, m, stOff+mailbox.EnemySTStateOff, []byte{mailbox.EnemyStateRefused})
		store(t, m, stOff+mailbox.EnemySTLastErrOff, []byte{e})
	}
	refuse(mailbox.EnemyErrGate)
	if s := step(2); s == nil || s.Pos != at || !c.blk[0].Pinned {
		t.Fatalf("after ERR_GATE: %+v blk %+v", s, c.blk[0])
	}
	refuse(mailbox.EnemyErrIce)
	if s := step(3); s != nil || c.Stats.Deaths != 0 {
		t.Fatalf("after ERR_ICE: %+v stats %+v", s, c.Stats)
	}
	if s := step(4); s != nil {
		t.Errorf("caught up again: %+v", s)
	}
}

// mirrorPIDs ticks c once with st at now (the REL's frame and the
// heartbeat advanced) and returns the block's slot pids.
func mirrorPIDs(t *testing.T, c *Mirror, m *mem.FakeMem, tick func(), st *State, now time.Time) []uint32 {
	t.Helper()
	pushState(t, c, st, now)
	tick()
	m.ResetWrites()
	if _, _, err := c.Tick(now); err != nil {
		t.Fatal(err)
	}
	b := blockWrites(t, m)
	if len(b) != 1 {
		t.Fatalf("%d blocks", len(b))
	}
	var pids []uint32
	for _, s := range b[0].Slots {
		pids = append(pids, s.PID)
	}
	return pids
}

// livePIDs is the pids of setIDs 0, 1, 2 (es.Of 1, 2, 0) as the fake game
// has them now.
func livePIDs(es *fakegame.Enemies) []uint32 {
	a0, _ := es.Of(1)
	a1, _ := es.Of(2)
	a2, _ := es.Of(0)
	return []uint32{a0.PID, a1.PID, a2.PID}
}

// TestMirrorReCreated (review 2): the local Moblins re-created under the
// same keys in the same stage and room (here a reload this mirror never
// saw out of play, as a save state is) get new pids; the next walk moves
// every block slot to its key's new actor (the prototype re-read the pid
// every tick), not the dead pid forever.
func TestMirrorReCreated(t *testing.T) {
	g, m, es, tick := clientRoom(t)
	start := time.Unix(2000, 0)
	c := NewMirror(m, log.Discard{}, nil, false, start)
	m.Record(true)
	now := start.Add(time.Second)
	old := livePIDs(es)
	if got := mirrorPIDs(t, c, m, tick, hostState(1, 0), now); !slices.Equal(got, old) {
		t.Fatalf("first: %v, want %v", got, old)
	}
	if err := g.LoadStage(mDai); err != nil {
		t.Fatal(err)
	}
	if err := g.EnterRoom(1, 2); err != nil {
		t.Fatal(err)
	}
	frames(t, g, es, 4)
	fresh := livePIDs(es)
	if slices.Equal(fresh, old) {
		t.Fatal("the reload kept the pids: the test proves nothing")
	}
	now = now.Add(MatchEvery + time.Second)
	if got := mirrorPIDs(t, c, m, tick, hostState(2, 16), now); !slices.Equal(got, fresh) {
		t.Errorf("after the re-create: %v, want %v (stale %v)", got, fresh, old)
	}
}

// TestMirrorRoomReEntry (review 2): this game leaves the host's room and
// comes back while the host stays (the host-room gate disarms before any
// walk): the room's state starts over on return, and the first tick back
// mirrors the room's new actors.
func TestMirrorRoomReEntry(t *testing.T) {
	g, m, es, tick := clientRoom(t)
	start := time.Unix(2000, 0)
	c := NewMirror(m, log.Discard{}, nil, false, start)
	m.Record(true)
	now := start.Add(time.Second)
	old := livePIDs(es)
	if got := mirrorPIDs(t, c, m, tick, hostState(1, 0), now); !slices.Equal(got, old) {
		t.Fatalf("first: %v, want %v", got, old)
	}
	c.deadHere[mo2Key(7)] = true
	if err := g.EnterRoom(2); err != nil {
		t.Fatal(err)
	}
	frames(t, g, es, 2)
	now = now.Add(16 * time.Millisecond)
	pushState(t, c, hostState(2, 16), now)
	tick()
	if _, sleep, _ := c.Tick(now); sleep != GateSleep {
		t.Fatal("room mismatch not gated")
	}
	if len(c.deadHere) != 0 || c.blk[0].Used {
		t.Errorf("room 2: dead-here %v, slot 0 %+v", c.deadHere, c.blk[0])
	}
	// On through room 3 (room 1's zone lives two room changes), and back.
	if err := g.EnterRoom(3); err != nil {
		t.Fatal(err)
	}
	frames(t, g, es, 2)
	if err := g.EnterRoom(1, 2); err != nil {
		t.Fatal(err)
	}
	frames(t, g, es, 4)
	fresh := livePIDs(es)
	if slices.Equal(fresh, old) {
		t.Fatal("re-entry kept the pids: the test proves nothing")
	}
	now = now.Add(16 * time.Millisecond)
	if got := mirrorPIDs(t, c, m, tick, hostState(3, 32), now); !slices.Equal(got, fresh) {
		t.Errorf("back in room 1: %v, want %v (stale %v)", got, fresh, old)
	}
}

// TestMirrorQuietGone (review 2): a pinned quiet copy the walk no longer
// finds is dead (its STATE_DEAD missed: the delete fell between the status
// read and the walk, or a disarm outlasted the REL's latch): the death
// switch is set; with the REL's last word REFUSED/ERR_ICE for it, this
// game's own death owns it and nothing is set.
func TestMirrorQuietGone(t *testing.T) {
	for _, ice := range []bool{false, true} {
		g, m, es, tick := clientRoom(t)
		a0, _ := es.Of(1) // setID 0
		store(t, m, a0.Addr+addr.Mo2OffDeathSwitch, []byte{0x05})
		start := time.Unix(2000, 0)
		c := NewMirror(m, log.Discard{}, nil, false, start)
		c.Quiet, _ = ParseQuietKinds("mo2")
		m.Record(true)
		now := start.Add(time.Second)
		st := hostState(1, 0)
		st.Slots[0].Flags |= SlotDying
		if got := mirrorPIDs(t, c, m, tick, st, now); got[0] != a0.PID || !c.blk[0].Pinned || !c.blk[0].quiet {
			t.Fatalf("dying: %v blk %+v", got, c.blk[0])
		}
		stOff := mailbox.EnemyBlockAddr + mailbox.EnemyStatusOff
		store(t, m, stOff+mailbox.EnemySTPIDOff, binary.BigEndian.AppendUint32(nil, a0.PID))
		if ice {
			store(t, m, stOff+mailbox.EnemySTStateOff, []byte{mailbox.EnemyStateRefused})
			store(t, m, stOff+mailbox.EnemySTLastErrOff, []byte{mailbox.EnemyErrIce})
		} else {
			store(t, m, stOff+mailbox.EnemySTStateOff, []byte{mailbox.EnemyStateDying})
		}
		// Gone (deleted), its status still the last one read.
		if err := es.Kill(g, a0.PID); err != nil {
			t.Fatal(err)
		}
		now = now.Add(MatchEvery + time.Millisecond)
		st = hostState(2, 16)
		st.Slots = st.Slots[1:]
		mirrorPIDs(t, c, m, tick, st, now)
		_, sw, _ := addr.SwitchBit(5)
		v, _ := mem.ReadU8(m, addr.LiveMemBit+uint32(sw.Off))
		if c.blk[0].Used && c.blk[0].Key == mo2Key(0) || !c.deadHere[mo2Key(0)] {
			t.Errorf("ice %v: still pinned %+v, dead-here %v", ice, c.blk[0], c.deadHere)
		}
		if want := !ice; (c.Stats.Deaths == 1) != want || (v&sw.Mask != 0) != want {
			t.Errorf("ice %v: deaths %d, switch set %v", ice, c.Stats.Deaths, v&sw.Mask != 0)
		}
	}
}

// TestMirrorEpochForgets (review 2): a save state loaded in the same room
// (the heartbeat goes back: a new epoch) brings back an enemy the mirror
// saw die here; the dead-here set and the pins are dropped, so the host's
// dead words catch it up again (DYING|RELEASE).
func TestMirrorEpochForgets(t *testing.T) {
	_, m, es, tick := clientRoom(t)
	a0, _ := es.Of(1) // setID 0
	start := time.Unix(2000, 0)
	c := NewMirror(m, log.Discard{}, nil, false, start)
	c.Quiet, _ = ParseQuietKinds("mo2")
	m.Record(true)
	now := start.Add(time.Second)
	st := hostState(1, 0)
	st.Slots[0].Flags |= SlotDying
	mirrorPIDs(t, c, m, tick, st, now)
	stOff := mailbox.EnemyBlockAddr + mailbox.EnemyStatusOff
	store(t, m, stOff+mailbox.EnemySTPIDOff, binary.BigEndian.AppendUint32(nil, a0.PID))
	store(t, m, stOff+mailbox.EnemySTStateOff, []byte{mailbox.EnemyStateDead})
	dead := func(seq uint32) *State {
		st := hostState(seq, 16*seq)
		st.Slots = st.Slots[1:]
		st.SetDead([DeadWords]uint32{1})
		return st
	}
	now = now.Add(16 * time.Millisecond)
	if got := mirrorPIDs(t, c, m, tick, dead(2), now); slices.Contains(got, a0.PID) || !c.deadHere[mo2Key(0)] {
		t.Fatalf("after STATE_DEAD: %v dead-here %v", got, c.deadHere)
	}
	// The save state: the heartbeat goes back, the REL forgets its latch.
	hb, _ := mem.ReadU32(m, mailbox.SpawnTrigger)
	store(t, m, mailbox.SpawnTrigger, binary.BigEndian.AppendUint32(nil, hb-50))
	store(t, m, stOff+mailbox.EnemySTStateOff, []byte{mailbox.EnemyStateUnresolved})
	now = now.Add(16 * time.Millisecond)
	pushState(t, c, dead(3), now)
	m.ResetWrites()
	if _, _, err := c.Tick(now); err != nil {
		t.Fatal(err)
	}
	b := blockWrites(t, m)
	if len(b) != 1 {
		t.Fatalf("%d blocks", len(b))
	}
	if _, s := slotByPID(&b[0], a0.PID); s == nil || s.Flags != mailbox.EnemySFDying|mailbox.EnemySFRelease {
		t.Errorf("after the save state: setID 0 not caught up again: %+v", b[0].Slots)
	}
}

// slotOf2 is st's slot for proc and setID (a zero Slot if none).
func slotOf2(st *State, proc, setID uint16) Slot {
	if st != nil {
		for _, s := range st.Slots {
			if s.Proc == proc && s.SetID == setID {
				return s
			}
		}
	}
	return Slot{}
}

// TestMirrorAmbusher: a pot ambusher (kindRoom's BK setID 1, type 2)
// matched to the host's is offered with its own mType: batch C's REL takes
// a live 2/3 (ambush_cleanup), so no tick ever writes an mType, the
// ambusher's or the torch Bokoblin's beside it. Nothing is written while
// the REL does not report the copy MIRRORED (refused: its own AI runs).
// Once it does, and while the host's slot is HIDDEN, a copy that came out
// here (m02BA 0) is put back in its pot (0xFF) and its actor and model
// scale go to 1; once the host's reveals, m02BA is the REL's. A
// re-adoption with the host's already out sets the scale only, and an
// adopted ambusher the host stops listing keeps its type 2.
func TestMirrorAmbusher(t *testing.T) {
	g, m, es := kindRoom(t)
	tick := booted(t, g, m)
	torch, _ := es.Of(0)
	amb, _ := es.Of(1)
	const morf, model = uint32(0x817E0000), uint32(0x817E1000)
	store(t, m, amb.Addr+addr.BkOffMorf, binary.BigEndian.AppendUint32(nil, morf))
	store(t, m, morf+addr.McaMorfOffModel, binary.BigEndian.AppendUint32(nil, model))
	half := binary.BigEndian.AppendUint32(nil, math.Float32bits(0.5))
	halves := func() {
		t.Helper()
		store(t, m, amb.Addr+addr.ActorOffScale, slices.Concat(half, half, half)) // its pot gone (d_a_bk.cpp:4248)
		store(t, m, model+addr.J3DModelOffBaseScale, slices.Concat(half, half, half))
	}
	store(t, m, amb.Addr+addr.BkOffDrawGate, []byte{0}) // revealed here first
	halves()
	start := time.Unix(2000, 0)
	c := NewMirror(m, log.Discard{}, nil, false, start)
	c.Kinds, _ = ParseKinds("bk")
	m.Record(true)
	now := start.Add(time.Second)
	st := &State{Flags: StateFlagInScope, Epoch: 7, Seq: 1, HostBeat: 9, HostMS: 1000, Stage: "M_Dai", Room: 1,
		HostKindMask: 1 << KindBK, KindHash: KindHash()}
	st.Slots = []Slot{
		{SetID: 0, Proc: addr.ProcBK, Kind: KindBK, Handle: 1, Health: 5, MorfRate: 1},
		{SetID: 1, Proc: addr.ProcBK, Kind: KindBK, Handle: 2, Health: 5, MorfRate: 1, Flags: SlotHidden},
	}
	step := func() *mailbox.EnemyBlock {
		t.Helper()
		now = now.Add(16 * time.Millisecond)
		st.Seq++
		st.HostMS = uint32(now.Sub(start).Milliseconds())
		pushState(t, c, st, now)
		tick()
		m.ResetWrites()
		if _, _, err := c.Tick(now); err != nil {
			t.Fatal(err)
		}
		b := blockWrites(t, m)
		if len(b) != 1 {
			t.Fatalf("%d blocks", len(b))
		}
		return &b[0]
	}
	u8 := func(a uint32) uint8 {
		t.Helper()
		v, err := mem.ReadU8(m, a)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	scales := func(want float32) {
		t.Helper()
		for _, a := range []uint32{amb.Addr + addr.ActorOffScale, model + addr.J3DModelOffBaseScale} {
			for i := range uint32(3) {
				if v, _ := mem.ReadF32(m, a+4*i); v != want {
					t.Errorf("scale at 0x%08X[%d] = %v, want %v", a, i, v, want)
				}
			}
		}
	}
	// wrote reports whether this tick wrote m02BA or a scale.
	wrote := func() bool {
		for _, w := range m.Writes() {
			switch w.Addr {
			case amb.Addr + addr.BkOffDrawGate, amb.Addr + addr.ActorOffScale, model + addr.J3DModelOffBaseScale:
				return true
			}
		}
		return false
	}
	status := func(j int, state, lastErr uint8) {
		t.Helper()
		at := mailbox.EnemyBlockAddr + mailbox.EnemyStatusOff + uint32(j)*mailbox.EnemyStatusSize
		store(t, m, at+mailbox.EnemySTActorOff, binary.BigEndian.AppendUint32(nil, amb.Addr))
		store(t, m, at+mailbox.EnemySTPIDOff, binary.BigEndian.AppendUint32(nil, amb.PID))
		store(t, m, at+mailbox.EnemySTStateOff, []byte{state})
		store(t, m, at+mailbox.EnemySTLastErrOff, []byte{lastErr})
	}

	// noType fails the test on any mType write this tick (batch C: never).
	noType := func() {
		t.Helper()
		for _, w := range m.Writes() {
			if w.Addr == amb.Addr+addr.BkOffType || w.Addr == torch.Addr+addr.BkOffType {
				t.Errorf("an mType write at 0x%08X", w.Addr)
			}
		}
	}

	// Offered, not yet adopted: its own mType 2 in the block's slot, no
	// write but the block.
	b := step()
	noType()
	j, s := slotByPID(b, amb.PID)
	if s == nil || s.Flags&mailbox.EnemySFHidden == 0 {
		t.Fatalf("the ambusher has no hidden slot: %+v", b.Slots)
	}
	if u8(amb.Addr+addr.BkOffType) != BkTypeAmbushJump || u8(amb.Addr+addr.BkOffDrawGate) != 0 || u8(torch.Addr+addr.BkOffType) != 4 {
		t.Errorf("mType %d m02BA 0x%02X, torch mType %d", u8(amb.Addr+addr.BkOffType), u8(amb.Addr+addr.BkOffDrawGate), u8(torch.Addr+addr.BkOffType))
	}
	if n := len(blockSideWrites(m)); n != 1 {
		t.Errorf("%d writes besides the knob table, want the block only", n)
	}
	if wrote() || c.Stats.Ambushers != 1 || c.Stats.AmbushHides != 0 {
		t.Errorf("before the adoption: wrote %v stats %+v", wrote(), c.Stats)
	}
	scales(0.5)

	// Refused (its own AI runs): left alone.
	status(j, mailbox.EnemyStateRefused, mailbox.EnemyErrGate)
	step()
	noType()
	if wrote() || c.Stats.AmbushHides != 0 {
		t.Errorf("refused: wrote %v stats %+v", wrote(), c.Stats)
	}

	// Adopted: back in its pot, full size; then nothing more to write.
	status(j, mailbox.EnemyStateMirrored, mailbox.EnemyErrNone)
	step()
	noType()
	if u8(amb.Addr+addr.BkOffDrawGate) != 0xFF || c.Stats.Ambushers != 1 || c.Stats.AmbushHides != 1 {
		t.Errorf("adopted: m02BA 0x%02X stats %+v", u8(amb.Addr+addr.BkOffDrawGate), c.Stats)
	}
	scales(1)
	step()
	if wrote() || c.Stats.AmbushHides != 1 {
		t.Errorf("adopted, second tick: wrote %v stats %+v", wrote(), c.Stats)
	}

	// The host's came out: once the block's slot (the smoother's render
	// time) is no longer HIDDEN, m02BA is left to the REL.
	// (Until then each tick's m02BA 0 below is put back: still HIDDEN.)
	st.Slots[1].Flags &^= SlotHidden
	revealed := false
	for range 30 {
		store(t, m, amb.Addr+addr.BkOffDrawGate, []byte{0})
		b := step()
		_, s := slotByPID(b, amb.PID)
		if s == nil || s.Flags&mailbox.EnemySFHidden != 0 {
			continue
		}
		revealed = true
		for _, w := range m.Writes() {
			if w.Addr == amb.Addr+addr.BkOffDrawGate || w.Addr == amb.Addr+addr.BkOffType {
				t.Errorf("a write at 0x%08X after the reveal", w.Addr)
			}
		}
		if u8(amb.Addr+addr.BkOffDrawGate) != 0 {
			t.Error("m02BA rewritten after the reveal")
		}
	}
	if !revealed || c.Stats.Ambushers != 1 {
		t.Errorf("revealed %v stats %+v", revealed, c.Stats)
	}

	// Handed back, out at half size, adopted again with the host's out: the
	// scale goes to 1, m02BA stays the REL's.
	hides := c.Stats.AmbushHides
	status(j, mailbox.EnemyStateReleased, mailbox.EnemyErrNone)
	step()
	halves()
	status(j, mailbox.EnemyStateMirrored, mailbox.EnemyErrNone)
	step()
	scales(1)
	if u8(amb.Addr+addr.BkOffDrawGate) != 0 || c.Stats.AmbushHides != hides {
		t.Errorf("re-adopted: m02BA 0x%02X stats %+v", u8(amb.Addr+addr.BkOffDrawGate), c.Stats)
	}

	// Adopted, then no longer listed: its mType stays its own.
	st.Slots = st.Slots[:1]
	for range 3 {
		step()
		noType()
	}
	if u8(amb.Addr+addr.BkOffType) != BkTypeAmbushJump {
		t.Errorf("an adopted ambusher's mType is %d", u8(amb.Addr+addr.BkOffType))
	}
}

// TestMirrorAmbusherReoffer: an ambusher whose slot goes before the REL
// ever took it, and comes back, is offered each time with its own mType
// and never written (batch C retired the mType-0 write and its restore);
// each offer counts in Stats.Ambushers.
func TestMirrorAmbusherReoffer(t *testing.T) {
	g, m, es := kindRoom(t)
	tick := booted(t, g, m)
	amb, _ := es.Of(1)
	start := time.Unix(2000, 0)
	c := NewMirror(m, log.Discard{}, nil, false, start)
	c.Kinds, _ = ParseKinds("bk")
	now := start.Add(time.Second)
	st := &State{Flags: StateFlagInScope, Epoch: 7, Seq: 1, HostBeat: 9, HostMS: 1000, Stage: "M_Dai", Room: 1,
		HostKindMask: 1 << KindBK, KindHash: KindHash()}
	listed := []Slot{
		{SetID: 0, Proc: addr.ProcBK, Kind: KindBK, Handle: 1, Health: 5, MorfRate: 1},
		{SetID: 1, Proc: addr.ProcBK, Kind: KindBK, Handle: 2, Health: 5, MorfRate: 1, Flags: SlotHidden},
	}
	step := func(slots []Slot) {
		t.Helper()
		st.Slots = slots
		now = now.Add(16 * time.Millisecond)
		st.Seq++
		st.HostMS = uint32(now.Sub(start).Milliseconds())
		pushState(t, c, st, now)
		tick()
		if _, _, err := c.Tick(now); err != nil {
			t.Fatal(err)
		}
	}
	mType := func() uint8 {
		t.Helper()
		v, err := mem.ReadU8(m, amb.Addr+addr.BkOffType)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	m.Record(true)
	step(listed)
	if mType() != BkTypeAmbushJump || c.Stats.Ambushers != 1 {
		t.Fatalf("offered: mType %d stats %+v", mType(), c.Stats)
	}
	step(listed[:1])
	if mType() != BkTypeAmbushJump {
		t.Errorf("dropped unadopted: mType %d, want %d", mType(), BkTypeAmbushJump)
	}
	step(listed)
	if mType() != BkTypeAmbushJump || c.Stats.Ambushers != 2 {
		t.Errorf("offered again: mType %d stats %+v", mType(), c.Stats)
	}
	for _, w := range m.Writes() {
		if w.Addr == amb.Addr+addr.BkOffType {
			t.Errorf("an mType write: % X", w.Data)
		}
	}
}
