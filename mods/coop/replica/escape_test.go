package replica

import (
	"context"
	"encoding/binary"
	"errors"
	"math"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/mods/coop/hub"
	"github.com/StephenSHorton/ww-multiplayer/mods/coop/wire"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/fakegame"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/gate"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mailbox"
)

// nsAddr is the address of mNextStage's byte off.
func nsAddr(off int) uint32 { return addr.NextStage + uint32(off) }

// The WriteGuard holds each escape byte to the values the game takes
// there, and opens them only when ARMED and IDLE.
func TestWriteGuardEscapeRules(t *testing.T) {
	busy := gate.Status{Stable: true, Heartbeat: true, IdleWhy: "an event is running"}
	cases := []struct {
		name     string
		targets  Target // 0: a replica's, M3Targets | TargetEscape
		gates    gate.Status
		unarmed  bool
		a        uint32
		old, val byte
		want     Refusal
	}{
		{"a name letter", 0, gatesOpen, false, nsAddr(0), 's', 'M', 0},
		{"a name digit", 0, gatesOpen, false, nsAddr(6), 0, '2', 0},
		{"an underscore", 0, gatesOpen, false, nsAddr(1), 0, '_', 0},
		{"a NUL", 0, gatesOpen, false, nsAddr(3), 'a', 0, 0},
		{"a dash", 0, gatesOpen, false, nsAddr(0), 0, '-', RefuseValue},
		{"a space", 0, gatesOpen, false, nsAddr(2), 0, ' ', RefuseValue},
		{"a high byte", 0, gatesOpen, false, nsAddr(2), 0, 0xC3, RefuseValue},
		{"the name's last byte", 0, gatesOpen, false, nsAddr(7), 0, 'x', RefuseValue},
		{"the name's last NUL", 0, gatesOpen, false, nsAddr(7), 'x', 0, 0},
		{"a point 0..255", 0, gatesOpen, false, nsAddr(addr.StageOffPoint), 0, 0, 0},
		{"the point's low byte", 0, gatesOpen, false, nsAddr(addr.StageOffPoint + 1), 0, 0xFF, 0},
		{"point 256", 0, gatesOpen, false, nsAddr(addr.StageOffPoint), 0, 1, RefuseValue},
		{"a restart point", 0, gatesOpen, false, nsAddr(addr.StageOffPoint), 0, 0xFF, RefuseValue},
		{"room 63", 0, gatesOpen, false, nsAddr(addr.StageOffRoomNo), 0, 63, 0},
		{"room 64", 0, gatesOpen, false, nsAddr(addr.StageOffRoomNo), 0, 64, RefuseValue},
		{"room -1", 0, gatesOpen, false, nsAddr(addr.StageOffRoomNo), 0, 0xFF, RefuseValue},
		{"layer -1", 0, gatesOpen, false, nsAddr(addr.StageOffLayer), 0, 0xFF, 0},
		{"layer 11", 0, gatesOpen, false, nsAddr(addr.StageOffLayer), 0, 11, 0},
		{"layer 12", 0, gatesOpen, false, nsAddr(addr.StageOffLayer), 0, 12, RefuseValue},
		{"layer -2", 0, gatesOpen, false, nsAddr(addr.StageOffLayer), 0, 0xFE, RefuseValue},
		{"mEnable 0 -> 1", 0, gatesOpen, false, addr.NextStageEnable, 0, 1, 0},
		{"mEnable already pending", 0, gatesOpen, false, addr.NextStageEnable, 1, 1, RefuseValue},
		{"mEnable cleared", 0, gatesOpen, false, addr.NextStageEnable, 1, 0, RefuseValue},
		{"mEnable 0 -> 2", 0, gatesOpen, false, addr.NextStageEnable, 0, 2, RefuseValue},
		{"mWipe 0", 0, gatesOpen, false, nsAddr(addr.NextOffWipe), 5, 0, 0},
		{"mWipe 11", 0, gatesOpen, false, nsAddr(addr.NextOffWipe), 0, 11, 0},
		{"mWipe 12", 0, gatesOpen, false, nsAddr(addr.NextOffWipe), 0, 12, RefuseValue},
		{"mWipe -1", 0, gatesOpen, false, nsAddr(addr.NextOffWipe), 0, 0xFF, RefuseValue},
		{"past mNextStage", 0, gatesOpen, false, nsAddr(addr.NextStageSize), 0, 1, RefuseTarget},
		{"mCurStage", 0, gatesOpen, false, addr.CurStageName, 0, 's', RefuseTarget},
		{"warp_seq", 0, gatesOpen, false, mailbox.WarpSeq + 3, 6, 7, 0},
		{"warp_x", 0, gatesOpen, false, WarpPos, 0, 0x44, 0},
		{"warp_z's last byte", 0, gatesOpen, false, WarpPos + 11, 0, 1, 0},
		{"warp_ack", 0, gatesOpen, false, WarpAck, 6, 7, RefuseTarget},
		{"warp_force", 0, gatesOpen, false, mailbox.Mailbox + 0xE4, 0, 1, RefuseTarget},
		{"the heartbeat", 0, gatesOpen, false, mailbox.SpawnTrigger, 0, 1, RefuseTarget},
		{"during an event", 0, busy, false, addr.NextStageEnable, 0, 1, RefuseGate},
		{"a warp during an event", 0, busy, false, mailbox.WarpSeq, 0, 1, RefuseGate},
		{"unarmed", 0, gatesOpen, true, addr.NextStageEnable, 0, 1, RefuseGate},
		{"a reconciler's guard", M3Targets, gatesOpen, false, addr.NextStageEnable, 0, 1, RefuseTarget},
		{"a warp on a reconciler's guard", M3Targets, gatesOpen, false, mailbox.WarpSeq, 0, 1, RefuseTarget},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			tg := c.targets
			if tg == 0 {
				tg = M3Targets | TargetEscape
			}
			wg := NewWriteGuard(GuardConfig{Targets: tg})
			wg.SetTick(Tick{Gates: c.gates, Armed: !c.unarmed, Table: addr.TableSea})
			err := wg.Check(c.a, c.old, c.val)
			if c.want == 0 {
				if err != nil {
					t.Fatalf("Check = %v, want nil", err)
				}
				return
			}
			var re *RefusalError
			if !errors.As(err, &re) || re.Reason != c.want {
				t.Fatalf("Check = %v, want a %s refusal", err, c.want)
			}
		})
	}
}

// Every escape byte is whole (mask 0xFF) and the escape spans hold
// exactly mNextStage and the warp request.
func TestEscapeAllowlist(t *testing.T) {
	g := NewGuardFor(TargetEscape)
	var n int
	for _, s := range g.list.Spans() {
		n += len(s.Mask)
	}
	if want := addr.NextStageSize + 4 + 12; n != want {
		t.Errorf("%d escape bytes, want %d", n, want)
	}
	for _, a := range []uint32{addr.NextStage, addr.NextStageEnable, mailbox.WarpSeq, WarpPos} {
		if g.Mask(a) != 0xFF || g.TargetOf(a) != TargetEscape {
			t.Errorf("0x%08X: mask 0x%02X, target %s", a, g.Mask(a), g.TargetOf(a))
		}
	}
	if NewGuard().Targets()&TargetEscape == 0 || !strings.Contains(AllTargets.String(), "escape") {
		t.Errorf("AllTargets = %s", AllTargets)
	}
}

func TestStageRequest(t *testing.T) {
	cases := []struct {
		h  StageRequest
		ok bool
	}{
		{StageRequest{Name: "sea", Point: 0, Room: 44, Layer: -1}, true},
		{StageRequest{Name: "M_NewD2", Point: 255, Room: 63, Layer: 11}, true},
		{StageRequest{Name: "", Room: 0}, false},
		{StageRequest{Name: "TooLong1", Room: 0}, false},
		{StageRequest{Name: "a-b", Room: 0}, false},
		{StageRequest{Name: "a\x00b", Room: 0}, false},
		{StageRequest{Name: "sea", Point: -1, Room: 0}, false},
		{StageRequest{Name: "sea", Point: 256, Room: 0}, false},
		{StageRequest{Name: "sea", Room: 64}, false},
		{StageRequest{Name: "sea", Room: -1}, false},
		{StageRequest{Name: "sea", Layer: 12}, false},
		{StageRequest{Name: "sea", Layer: -2}, false},
	}
	for _, c := range cases {
		if err := c.h.Check(); (err == nil) != c.ok {
			t.Errorf("%+v: Check = %v, want ok %v", c.h, err, c.ok)
		}
		if !c.ok {
			continue
		}
		// What Check takes, the guard takes byte by byte.
		wg := NewWriteGuard(GuardConfig{Targets: TargetEscape})
		wg.SetTick(Tick{Gates: gatesOpen, Armed: true, Table: addr.TableSea})
		for _, f := range c.h.Fields() {
			for i, v := range f.Data {
				if err := wg.Check(f.Addr+uint32(i), 0, v); err != nil {
					t.Errorf("%+v: %v", c.h, err)
				}
			}
		}
	}
	fs := StageRequest{Name: "sea", Point: 0x0102, Room: 44, Layer: -1}.Fields()
	b := fs[0].Data
	if string(b[:4]) != "sea\x00" || b[7] != 0 || b[8] != 1 || b[9] != 2 || b[10] != 44 || b[11] != 0xFF {
		t.Errorf("mNextStage bytes % X", b)
	}
	if len(fs) != 3 || fs[1].Addr != addr.NextStage+addr.NextOffWipe || fs[2].Addr != addr.NextStageEnable || fs[2].Data[0] != 1 {
		t.Errorf("fields %+v: want the 12 bytes, mWipe, then mEnable last", fs)
	}
}

func TestWarpFields(t *testing.T) {
	fs := WarpFields(Pos{X: 1.5, Y: -2, Z: 300}, 41)
	if len(fs) != 2 || fs[0].Addr != WarpPos || fs[1].Addr != mailbox.WarpSeq {
		t.Fatalf("fields %+v: want the coordinates, then warp_seq last", fs)
	}
	for i, want := range []float32{1.5, -2, 300} {
		if got := math.Float32frombits(binary.BigEndian.Uint32(fs[0].Data[4*i:])); got != want {
			t.Errorf("coordinate %d = %v, want %v", i, got, want)
		}
	}
	if seq := binary.BigEndian.Uint32(fs[1].Data); seq != 42 {
		t.Errorf("warp_seq %d, want 42", seq)
	}
	for _, p := range []Pos{{X: float32(math.NaN())}, {Y: float32(math.Inf(1))}, {Z: -2e6}} {
		if p.valid() {
			t.Errorf("%+v is valid", p)
		}
	}
}

// escapeRig is a writer rig whose guard opens the escape bytes.
func escapeRig(t *testing.T) *wrig {
	t.Helper()
	return newWriterRig(t, GuardConfig{Targets: M3Targets | TargetEscape})
}

// applyFields runs ApplyFields with the game beating on the writer's
// second heartbeat read.
func (r *wrig) applyFields(fs []Field) (Result, error) {
	r.hm.beatOn(2, r.g.Frame)
	defer r.hm.beatOn(0, nil)
	return r.fw.ApplyFields(context.Background(), fs)
}

func TestApplyFieldsRefreshes(t *testing.T) {
	r := escapeRig(t)
	r.m.Record(true)
	h := StageRequest{Name: "sea", Point: 0, Room: 44, Layer: 0} // what LoadStage(wOutset) asked
	res, err := r.applyFields(h.Fields())
	if err != nil {
		t.Fatalf("ApplyFields = %v\n%s", err, r.log.String())
	}
	ws := r.m.Writes()
	if len(ws) == 0 || ws[len(ws)-1].Addr != addr.NextStageEnable || len(ws[len(ws)-1].Data) != 1 || ws[len(ws)-1].Data[0] != 1 {
		t.Fatalf("writes %+v: mEnable must be written last", ws)
	}
	if res.Beat == 0 || len(res.Wrote) != 1 || res.Already != addr.StageSize+1 {
		t.Errorf("result %+v: want only mEnable written, the rest already there", res)
	}
	if st := r.fw.Stats(); st.Escapes != 1 || st.Written != 0 {
		t.Errorf("stats %+v", st)
	}
	// The game carries the request out: the same stage again.
	if err := r.g.ChangeStage(); err != nil {
		t.Fatal(err)
	}
	if b := r.byteAt(addr.NextStageEnable); b != 0 {
		t.Fatalf("mEnable %d after the stage change", b)
	}
}

func TestApplyFieldsIsAllOrNothing(t *testing.T) {
	bad := StageRequest{Name: "M_NewD2", Point: 0, Room: 0, Layer: 12}
	busy := func(r *wrig) { r.g.SetEvent(1, 0); r.tick() }
	cases := []struct {
		name  string
		setup func(r *wrig)
		fs    []Field
		want  error
	}{
		{"a layer the game has no table for", nil, bad.Fields(), ErrGuard},
		{"a pending stage change is never re-armed", func(r *wrig) { r.store(addr.NextStageEnable, 1) }, StageRequest{Name: "sea", Room: 44}.Fields(), ErrGuard},
		{"an event runs", busy, StageRequest{Name: "sea", Room: 44}.Fields(), ErrClosed},
		{"co-op is off", func(r *wrig) { r.brk.TurnOff("coop off") }, StageRequest{Name: "sea", Room: 44}.Fields(), ErrOff},
		{"warp_ack is no target", nil, []Field{{Addr: WarpAck, Data: []byte{0, 0, 0, 1}}}, ErrGuard},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := escapeRig(t)
			if c.setup != nil {
				c.setup(r)
			}
			r.m.Record(true)
			if _, err := r.applyFields(c.fs); !errors.Is(err, c.want) {
				t.Fatalf("ApplyFields = %v, want %v", err, c.want)
			}
			if ws := r.m.Writes(); len(ws) != 0 {
				t.Fatalf("%d writes: an escape refused before the wait writes nothing", len(ws))
			}
		})
	}
}

func (r *wrig) store(a uint32, v byte) {
	r.t.Helper()
	if err := r.m.Store(a, []byte{v}); err != nil {
		r.t.Fatal(err)
	}
}

// A stage change the game asks for between the snapshot and the beat
// wins: the escape is dropped.
func TestApplyFieldsAbortsWhenTheGameMovesOn(t *testing.T) {
	r := escapeRig(t)
	r.m.Record(true)
	r.hm.beatOn(2, func() {
		r.g.Frame()
		if err := r.g.SetNextStage(wDRC); err != nil {
			t.Error(err)
		}
	})
	_, err := r.fw.ApplyFields(context.Background(), StageRequest{Name: "sea", Room: 44}.Fields())
	if !errors.Is(err, ErrAborted) {
		t.Fatalf("ApplyFields = %v, want ErrAborted", err)
	}
	if ws := r.m.Writes(); len(ws) != 0 {
		t.Fatalf("%d writes after the game moved on", len(ws))
	}
}

// IDLE closing between the snapshot and the beat drops the escape: an
// event, the pause menu, a minigame or a refight starting as the writer
// waits is read again after the beat (recheckIdle), for both escapes.
func TestApplyFieldsAbortsWhenIdleCloses(t *testing.T) {
	closes := []struct {
		name string
		act  func(g *fakegame.FakeGame)
		want string
	}{
		{"an event starts", func(g *fakegame.FakeGame) { g.SetEvent(1, 0) }, "event"},
		{"an event is ordered", func(g *fakegame.FakeGame) { g.SetEvent(0, 1) }, "event"},
		{"the pause menu opens", func(g *fakegame.FakeGame) { g.SetPauseMenu(true) }, "pause"},
		{"a minigame starts", func(g *fakegame.FakeGame) { g.SetMiniGame(1) }, "minigame"},
		{"a refight starts", func(g *fakegame.FakeGame) { g.SetRefight(1) }, "refight"},
	}
	escapes := []struct {
		name string
		fs   []Field
	}{
		{"refresh", StageRequest{Name: "sea", Room: 44}.Fields()},
		{"warp", WarpFields(Pos{X: 10, Y: 20, Z: 30}, 0)},
	}
	for _, e := range escapes {
		for _, c := range closes {
			t.Run(e.name+", "+c.name, func(t *testing.T) {
				r := escapeRig(t)
				r.m.Record(true)
				r.hm.beatOn(2, func() {
					r.g.Frame()
					c.act(r.g)
				})
				res, err := r.fw.ApplyFields(context.Background(), e.fs)
				if !errors.Is(err, ErrAborted) || !strings.Contains(err.Error(), c.want) {
					t.Fatalf("ApplyFields = %v, want ErrAborted naming the %s", err, c.want)
				}
				if ws := r.m.Writes(); len(ws) != 0 || len(res.Wrote) != 0 {
					t.Fatalf("%d writes after IDLE closed: %+v", len(ws), ws)
				}
				if st := r.fw.Stats(); st.Aborted != 1 || st.Escapes != 0 {
					t.Errorf("stats %+v", st)
				}
			})
		}
	}
}

func TestApplyFieldsDryRun(t *testing.T) {
	r := escapeRig(t)
	dry, err := NewFrameWriter(WriterConfig{Mem: r.m, DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := dry.ApplyFields(context.Background(), StageRequest{Name: "sea", Room: 44}.Fields()); !errors.Is(err, ErrDryRun) {
		t.Fatalf("ApplyFields = %v, want ErrDryRun", err)
	}
}

// partnerPos is a PartnerPos the test sets.
type partnerPos struct {
	mu sync.Mutex
	p  Pos
	ok bool
}

func (pp *partnerPos) set(p Pos) {
	pp.mu.Lock()
	defer pp.mu.Unlock()
	pp.p, pp.ok = p, true
}

func (pp *partnerPos) get() (Pos, bool) {
	pp.mu.Lock()
	defer pp.mu.Unlock()
	return pp.p, pp.ok
}

// escRig is an armed, writing replica in Outset with a partner, on a
// clock the test moves.
type escRig struct {
	*recRig
	pos *partnerPos
	clk *breakerClock
}

func newEscRig(t *testing.T) *escRig {
	t.Helper()
	pp := &partnerPos{}
	clk := &breakerClock{t: time.Unix(1_800_000_000, 0)}
	rr := newRecRig(t, Config{Apply: true, PartnerPos: pp.get, Now: clk.Now}, hub.FeatureImage)
	return &escRig{recRig: rr, pos: pp, clk: clk}
}

// presence is the PRESENCE the hub relays from the partner.
func (er *escRig) presence(stage string, point int16, table, room int8, f wire.PresenceFlags) {
	er.t.Helper()
	st, err := wire.MakeStage(stage)
	if err != nil {
		er.t.Fatal(err)
	}
	say(er.t, er.r, &wire.Presence{Stage: st, Table: table, Room: room, Layer: -1, Point: point, Flags: f})
}

// settled lets PartnerSettle pass, as the partner stays where its last
// PRESENCE put it.
func (er *escRig) settled() { er.clk.advance(PartnerSettle) }

// loadRoom gives room its collision in the game (mStatus[room].mpBgW).
func (er *escRig) loadRoom(room int8, on bool) {
	var v [4]byte
	if on {
		v = [4]byte{0x81, 0x23, 0x40, 0x00}
	}
	er.store(roomBgW(room), v[:])
}

// escape asks for k and ticks until it answers.
func (er *escRig) escape(k EscapeKind) EscapeResult {
	er.t.Helper()
	ch := er.r.Escape(k)
	for range 5 {
		select {
		case res := <-ch:
			return res
		default:
		}
		er.tick()
	}
	select {
	case res := <-ch:
		return res
	default:
	}
	er.t.Fatalf("no answer to %s", k)
	return EscapeResult{}
}

func (er *escRig) warpSeq() (seq, ack uint32) {
	b := er.read(mailbox.WarpSeq, 8)
	return binary.BigEndian.Uint32(b), binary.BigEndian.Uint32(b[4:])
}

const inGame = wire.PresenceInGame | wire.PresenceArmed

func TestEscapeRefresh(t *testing.T) {
	er := newEscRig(t)
	res := er.escape(EscapeRefresh)
	if res.Err != nil {
		t.Fatalf("refresh: %v\n%s", res.Err, er.log)
	}
	if !strings.Contains(res.Did, "reloading sea by point 0 into room 44") {
		t.Errorf("did %q", res.Did)
	}
	if b := er.read(addr.NextStageEnable, 1)[0]; b != 1 {
		t.Fatalf("mEnable %d: no stage change asked for", b)
	}
	rep := er.reps[len(er.reps)-1]
	if rep.Escape != EscapeRefresh || rep.EscapeErr != nil || !rep.Gates.CanWriteIdle() {
		t.Errorf("the tick's report: %+v", rep)
	}
	if err := er.g.ChangeStage(); err != nil {
		t.Fatal(err)
	}
	for range gate.StableTicks + 2 {
		er.tick()
	}
	if st := er.r.Status(); st.State != StateArmed || st.Writes.Escapes != 1 {
		t.Fatalf("after the reload: %+v", st)
	}
	if !er.log.has("co-op: refresh: reloading sea") {
		t.Errorf("log:\n%s", er.log)
	}
}

// After a death and Continue the game asks for the file's return place,
// an entrance (dComIfGs_setGameStartStage then dComIfGs_gameStart,
// d_a_player_dproc.inc:1091-1094, d_com_inf_game.cpp:1397-1406, with
// dComIfGp_setNextStage's layer -1): /refresh reloads by it.
func TestEscapeRefreshAfterDeathAndContinue(t *testing.T) {
	er := newEscRig(t)
	cont := outset
	cont.Layer = -1
	er.load(cont)
	er.ticks(gate.StableTicks + 2)
	res := er.escape(EscapeRefresh)
	if res.Err != nil {
		t.Fatalf("refresh: %v\n%s", res.Err, er.log)
	}
	if h := nextStage(er); h != (StageRequest{Name: "sea", Point: 0, Room: 44, Layer: -1}) || er.read(addr.NextStageEnable, 1)[0] != 1 {
		t.Fatalf("mNextStage %+v", h)
	}
}

func TestEscapeRefusals(t *testing.T) {
	cases := []struct {
		name    string
		setup   func(er *escRig)
		kind    EscapeKind
		want    string
		notNow  bool
		writeOK bool
	}{
		{"an event runs", func(er *escRig) { er.g.SetEvent(1, 0); er.tick() }, EscapeRefresh, "", true, false},
		{"co-op writes are off", func(er *escRig) { er.r.TurnOff("you typed /coop off") }, EscapeRefresh, "you typed /coop off", false, false},
		{"Link came in by a void-out's restart", func(er *escRig) {
			// dStage_restartRoom (d_stage.cpp:2368-2372)
			er.store(addr.NextStage+addr.StageOffPoint, []byte{0xFF, 0xFF})
			er.store(addr.CurStagePoint, []byte{0xFF, 0xFF})
			er.ticks(gate.StableTicks + 2)
		}, EscapeRefresh, "by a restart (point -1)", false, false},
		{"Link came in by a turn restart", func(er *escRig) {
			// dStage_turnRestart, dStage_escapeRestart (d_stage.cpp:2375-2404)
			er.store(addr.NextStage+addr.StageOffPoint, []byte{0xFF, 0xFD})
			er.store(addr.CurStagePoint, []byte{0xFF, 0xFD})
			er.ticks(gate.StableTicks + 2)
		}, EscapeRefresh, "by a restart (point -3)", false, false},
		{"no word of the partner", nil, EscapeWarp, "no word of the partner", false, false},
		{"the partner changes stage", func(er *escRig) {
			er.presence("sea", 0, 0, 44, inGame|wire.PresenceTransitioning)
		}, EscapeWarp, "changing stage", true, false},
		{"the partner is on the title screen", func(er *escRig) {
			er.presence("sea_T", 0, -1, -1, 0)
		}, EscapeWarp, "not in a stage", true, false},
		{"no position from pose sync", func(er *escRig) {
			er.presence("sea", 0, 0, 44, inGame)
			er.loadRoom(44, true)
		}, EscapeWarp, "has not come in over pose sync", true, false},
		{"another stage, its entrance unknown", func(er *escRig) {
			er.presence("M_NewD2", 3, 3, 0, inGame)
		}, EscapeWarp, "is not known yet", false, false},
		{"the partner came in by a restart", func(er *escRig) {
			er.presence("M_NewD2", -1, 3, 0, inGame)
		}, EscapeWarp, "by a restart", false, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			er := newEscRig(t)
			if c.setup != nil {
				c.setup(er)
			}
			er.bm.writes = nil
			res := er.escape(c.kind)
			if res.Err == nil {
				t.Fatalf("%s did %q", c.kind, res.Did)
			}
			if !strings.Contains(res.Err.Error(), c.want) || errors.Is(res.Err, ErrNotNow) != c.notNow {
				t.Fatalf("err %v: want %q, not-now %v", res.Err, c.want, c.notNow)
			}
			for _, w := range er.bm.written() {
				if NewGuardFor(TargetEscape).Mask(w.a) != 0 {
					t.Fatalf("a refused escape wrote 0x%08X", w.a)
				}
			}
		})
	}
}

func TestEscapeDryRun(t *testing.T) {
	rr := newRecRig(t, Config{}, hub.FeatureImage)
	select {
	case res := <-rr.r.Escape(EscapeRefresh):
		if res.Err == nil || !strings.Contains(res.Err.Error(), "--coop-dry-run") {
			t.Fatalf("a dry run's refresh: %+v", res)
		}
	default:
		t.Fatal("a dry run's escape is not refused at once")
	}
}

func TestEscapeWarpSameStage(t *testing.T) {
	er := newEscRig(t)
	er.presence("sea", 0, 0, 44, inGame)
	er.loadRoom(44, true)
	er.pos.set(Pos{X: 100, Y: 5, Z: -200})
	er.settled()
	res := er.escape(EscapeWarp)
	if res.Err != nil {
		t.Fatalf("warp: %v\n%s", res.Err, er.log)
	}
	if seq, _ := er.warpSeq(); seq != 1 {
		t.Fatalf("warp_seq %d, want 1", seq)
	}
	b := er.read(WarpPos, 12)
	if x := math.Float32frombits(binary.BigEndian.Uint32(b)); x != 100 {
		t.Errorf("warp_x %v", x)
	}
	// Not acknowledged yet: a second warp waits.
	if res := er.escape(EscapeWarp); !errors.Is(res.Err, ErrNotNow) || !strings.Contains(res.Err.Error(), "still under way") {
		t.Fatalf("a warp over a pending one: %+v", res)
	}
	// multiplayer_update moved Link and acknowledged it.
	er.store(WarpAck, []byte{0, 0, 0, 1})
	if res := er.escape(EscapeWarp); res.Err != nil {
		t.Fatal(res.Err)
	}
	if seq, _ := er.warpSeq(); seq != 2 {
		t.Fatalf("warp_seq %d, want 2", seq)
	}
}

// A partner in a room this game has not loaded: reload at the partner's
// entrance, then warp once the room is there.
func TestEscapeWarpRoomNotLoaded(t *testing.T) {
	er := newEscRig(t)
	er.presence("sea", 0, 0, 12, inGame)
	er.pos.set(Pos{X: 1, Y: 2, Z: 3})
	er.settled()
	res := er.escape(EscapeWarp)
	if res.Err != nil {
		t.Fatalf("warp: %v\n%s", res.Err, er.log)
	}
	if !strings.Contains(res.Did, "room 12, where the partner is, is not loaded here") || !strings.Contains(res.Did, "as you came in by it") {
		t.Errorf("did %q", res.Did)
	}
	if h := nextStage(er); h != (StageRequest{Name: "sea", Point: 0, Room: 44, Layer: -1}) {
		t.Fatalf("mNextStage %+v", h)
	}
	if err := er.g.ChangeStage(); err != nil {
		t.Fatal(err)
	}
	er.ticks(gate.StableTicks + 4)
	if seq, _ := er.warpSeq(); seq != 0 {
		t.Fatalf("warped into an unloaded room (warp_seq %d)", seq)
	}
	er.loadRoom(12, true)
	er.ticks(2)
	if seq, _ := er.warpSeq(); seq != 1 {
		t.Fatalf("warp_seq %d after the room loaded, want 1\n%s", seq, er.log)
	}
}

// nextStage is the request mNextStage holds now.
func nextStage(er *escRig) StageRequest {
	b := er.read(addr.NextStage, addr.StageSize)
	name, _, _ := strings.Cut(string(b[:addr.StageNameLen]), "\x00")
	return StageRequest{Name: name, Point: int16(binary.BigEndian.Uint16(b[8:])), Room: int8(b[10]), Layer: int8(b[11])}
}

func TestEscapeWarpOtherStage(t *testing.T) {
	er := newEscRig(t)
	// The partner, met mid-stage, then through a stage change: a
	// PRESENCE torn across the change (the new name, the old table and
	// room; inferred, the game has no such frame), then one without Link,
	// then the new stage's start room.
	er.presence("sea", 0, 0, 44, inGame)
	er.presence("sea", 0, 0, 44, inGame|wire.PresenceTransitioning)
	er.presence("M_NewD2", 2, 0, 44, inGame)
	er.presence("M_NewD2", 2, 0, 44, 0)
	er.presence("M_NewD2", 2, 3, 5, inGame)
	er.presence("M_NewD2", 2, 3, 6, inGame) // walked on
	er.pos.set(Pos{X: 7, Y: 8, Z: 9})
	res := er.escape(EscapeWarp)
	if res.Err != nil {
		t.Fatalf("warp: %v\n%s", res.Err, er.log)
	}
	if h := nextStage(er); h != (StageRequest{Name: "M_NewD2", Point: 2, Room: 5, Layer: -1}) {
		t.Fatalf("mNextStage %+v", h)
	}
	if !strings.Contains(res.Did, "as the partner came in by it") {
		t.Errorf("did %q", res.Did)
	}
	// The game takes it; the second step warps to the partner once
	// there.
	if err := er.g.SetNextStage(drc); err != nil { // names M_NewD2's STAG; the pending request stays
		t.Fatal(err)
	}
	if err := er.g.ChangeStage(); err != nil {
		t.Fatal(err)
	}
	if got := er.read(addr.CurStageRoomNo, 1)[0]; got != 5 {
		t.Fatalf("came in to room %d", got)
	}
	er.loadRoom(6, true)
	er.settled()
	er.ticks(gate.StableTicks + 4)
	if seq, _ := er.warpSeq(); seq != 1 {
		t.Fatalf("warp_seq %d after the stage change, want 1\n%s", seq, er.log)
	}
	if !er.log.has("co-op: warp: warping to the partner at (7, 8, 9) in room 6") {
		t.Errorf("log:\n%s", er.log)
	}
}

// A partner just arrived on Link's stage: its PRESENCE is fresh, but the
// position pose sync has may still be the one it left on its last stage.
// No warp is asked, the user's own or a warp's second step, until it has
// been in the stage for PartnerSettle.
func TestEscapeWarpWaitsForThePartnersArrival(t *testing.T) {
	stale, fresh := Pos{X: -900, Y: 10, Z: 900}, Pos{X: 120, Y: 5, Z: -40}
	noWarp := func(er *escRig, when string) {
		er.t.Helper()
		if seq, _ := er.warpSeq(); seq != 0 {
			er.t.Fatalf("%s: warp_seq %d\n%s", when, seq, er.log)
		}
		for _, w := range er.bm.written() {
			if w.a >= WarpPos && w.a < WarpPos+12 || w.a >= mailbox.WarpSeq && w.a < mailbox.WarpSeq+4 {
				er.t.Fatalf("%s: a warp byte written: %+v", when, w)
			}
		}
	}
	warpedTo := func(er *escRig, seq uint32, p Pos) {
		er.t.Helper()
		if got, _ := er.warpSeq(); got != seq {
			er.t.Fatalf("warp_seq %d, want %d\n%s", got, seq, er.log)
		}
		b := er.read(WarpPos, 12)
		if x := math.Float32frombits(binary.BigEndian.Uint32(b)); x != p.X {
			er.t.Fatalf("warp_x %v, want %v", x, p.X)
		}
	}

	t.Run("the user's warp", func(t *testing.T) {
		for _, between := range []bool{false, true} {
			er := newEscRig(t)
			er.presence("M_NewD2", 2, 3, 5, inGame) // Y, room 5, long since
			er.pos.set(stale)
			er.settled()
			er.presence("M_NewD2", 2, 3, 5, inGame|wire.PresenceTransitioning)
			if between {
				er.presence("M_NewD2", 2, 3, 5, 0) // its Link gone as the next stage builds
				if res := er.escape(EscapeWarp); !errors.Is(res.Err, ErrNotNow) || !strings.Contains(res.Err.Error(), "not in a stage") {
					t.Fatalf("warp with the partner between stages: %+v", res)
				}
			}
			er.presence("sea", 0, 0, 5, inGame) // X: Link's stage, room 5 again
			er.loadRoom(5, true)
			res := er.escape(EscapeWarp)
			if !errors.Is(res.Err, ErrNotNow) || !strings.Contains(res.Err.Error(), "only just come into sea") {
				t.Fatalf("warp onto the arriving partner: %+v", res)
			}
			er.ticks(10)
			noWarp(er, "as the partner arrives")

			er.pos.set(fresh) // pose sync catches up
			er.settled()
			if res := er.escape(EscapeWarp); res.Err != nil {
				t.Fatalf("warp once the partner settled: %v\n%s", res.Err, er.log)
			}
			warpedTo(er, 1, fresh)
		}
	})

	t.Run("a room change in the stage keeps it settled", func(t *testing.T) {
		er := newEscRig(t)
		er.presence("sea", 0, 0, 5, inGame)
		er.settled()
		er.presence("sea", 0, 0, 6, inGame)
		er.loadRoom(6, true)
		er.pos.set(fresh)
		if res := er.escape(EscapeWarp); res.Err != nil {
			t.Fatalf("warp: %v", res.Err)
		}
		warpedTo(er, 1, fresh)
	})

	t.Run("a warp's second step", func(t *testing.T) {
		er := newEscRig(t)
		er.presence("sea", 0, 0, 44, inGame)
		er.presence("sea", 0, 0, 44, inGame|wire.PresenceTransitioning)
		er.presence("M_NewD2", 2, 3, 5, 0)
		er.presence("M_NewD2", 2, 3, 5, inGame)
		er.pos.set(fresh)
		er.settled()
		if res := er.escape(EscapeWarp); res.Err != nil {
			t.Fatalf("warp: %v\n%s", res.Err, er.log)
		}
		if err := er.g.SetNextStage(drc); err != nil {
			t.Fatal(err)
		}
		if err := er.g.ChangeStage(); err != nil {
			t.Fatal(err)
		}
		// As Link comes in, the partner reloads the same stage and
		// comes back to room 6, while pose sync still has its old place.
		er.presence("M_NewD2", 2, 3, 5, inGame|wire.PresenceTransitioning)
		er.presence("M_NewD2", 2, 3, 6, inGame)
		er.pos.set(stale)
		er.loadRoom(6, true)
		er.ticks(gate.StableTicks + 4)
		noWarp(er, "the second step, as the partner arrives")
		if st := er.r.Status(); st.Writes.Escapes != 1 {
			t.Fatalf("escapes %d", st.Writes.Escapes)
		}

		er.pos.set(fresh)
		er.settled()
		er.ticks(2)
		warpedTo(er, 1, fresh)
	})
}

// Once Run returns no tick comes: the escape waiting for one is answered,
// and one asked for later at once, with ErrStopped, not ErrNotNow.
func TestEscapeAfterRunStops(t *testing.T) {
	er := newEscRig(t)
	ch := er.r.Escape(EscapeRefresh)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		er.r.Run(ctx)
	}()
	cancel()
	<-done
	for i, ch := range []<-chan EscapeResult{ch, er.r.Escape(EscapeWarp)} {
		select {
		case res := <-ch:
			// The waiting one may have had Run's first tick: that tick
			// made it, or refused it for now.
			if i == 0 && (res.Err == nil || errors.Is(res.Err, ErrNotNow)) {
				continue
			}
			if !errors.Is(res.Err, ErrStopped) || errors.Is(res.Err, ErrNotNow) {
				t.Fatalf("escape %d after Run stopped: %+v", i, res)
			}
		case <-time.After(time.Second):
			t.Fatalf("escape %d not answered after Run stopped", i)
		}
	}

	// Asked with no tick to come: stop answers it.
	er = newEscRig(t)
	ch = er.r.Escape(EscapeRefresh)
	er.r.stop()
	select {
	case res := <-ch:
		if !errors.Is(res.Err, ErrStopped) {
			t.Fatalf("the waiting escape: %+v", res)
		}
	default:
		t.Fatal("the waiting escape is not answered")
	}
}

// What the partner's PRESENCE teaches of its entrances' rooms.
func TestLearnPartner(t *testing.T) {
	type pres struct {
		stage       string
		point       int16
		table, room int8
		flags       wire.PresenceFlags
	}
	sea := func(room int8) pres { return pres{"sea", 0, 0, room, inGame} }
	cases := []struct {
		name  string
		seq   []pres
		stage string
		point int16
		room  int8 // -1: not learned
	}{
		{"mid-stage at the first word", []pres{{"M_NewD2", 2, 3, 5, inGame}}, "M_NewD2", 2, -1},
		{"a clean arrival", []pres{sea(44), {"M_NewD2", 2, 3, 5, inGame}, {"M_NewD2", 2, 3, 7, inGame}}, "M_NewD2", 2, 5},
		{"the old table and room beside the new name (a torn read; the game has no such frame) are skipped", []pres{sea(44), {"M_NewD2", 2, 0, 44, inGame}, {"M_NewD2", 2, 3, 5, inGame}}, "M_NewD2", 2, 5},
		{"same table and room number, with a PRESENCE without Link", []pres{sea(0), {"LinkRM", 1, 0, 0, inGame}, {"LinkRM", 1, 0, 0, 0}, {"LinkRM", 1, 0, 0, inGame}}, "LinkRM", 1, 0},
		{"same table and room number, nothing between: not learned", []pres{sea(0), {"LinkRM", 1, 0, 0, inGame}, {"LinkRM", 1, 0, 0, inGame}}, "LinkRM", 1, -1},
		{"paused and transitioning words teach nothing", []pres{sea(44), {"M_NewD2", 2, 3, 5, inGame | wire.PresencePaused}, {"M_NewD2", 2, 3, 6, inGame}}, "M_NewD2", 2, 6},
		{"a restart point is no entrance", []pres{sea(44), {"M_NewD2", -1, 3, 5, inGame}}, "M_NewD2", -1, -1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			er := newEscRig(t)
			for _, p := range c.seq {
				er.presence(p.stage, p.point, p.table, p.room, p.flags)
			}
			er.r.mu.Lock()
			room, ok := er.r.heard[entryKey{c.stage, c.point}]
			er.r.mu.Unlock()
			switch {
			case c.room < 0 && ok:
				t.Fatalf("learned room %d", room)
			case c.room >= 0 && (!ok || room != c.room):
				t.Fatalf("learned %d, %v; want %d", room, ok, c.room)
			}
		})
	}
}

// The partner's PRESENCE grows the map of entrances only so far.
func TestLearnPartnerIsBounded(t *testing.T) {
	er := newEscRig(t)
	er.presence("sea", 0, 0, 44, inGame)
	for i := range maxEntries + 50 {
		er.presence("sea", int16(i%256), 0, int8(i%64), inGame|wire.PresenceTransitioning)
		er.presence("st"+string(rune('A'+i/256)), int16(i%256), 3, int8(1+i%60), inGame)
	}
	er.r.mu.Lock()
	n := len(er.r.heard)
	er.r.mu.Unlock()
	if n > maxEntries {
		t.Fatalf("%d entrances heard", n)
	}
}
