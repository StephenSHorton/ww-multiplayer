package enemysync

import (
	"encoding/binary"
	"fmt"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	posefeed "github.com/StephenSHorton/ww-multiplayer/mods/multiplayer/feed"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/fakegame"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/heap"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/log"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mailbox"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mem"
)

// --- config -----------------------------------------------------------------

func TestParseRemoteFlags(t *testing.T) {
	for _, c := range []struct {
		in   string
		want uint16
	}{
		{"0", 0},
		{"0x2F", 0x2F},
		{"47", 47},
		{"bodies", mailbox.RemoteFlagBodies},
		{"bodies,swap", mailbox.RemoteFlagBodies | mailbox.RemoteFlagSwap},
		{"Bodies+SWAP + detect", mailbox.RemoteFlagBodies | mailbox.RemoteFlagSwap | mailbox.RemoteFlagDetect},
		{"step0", 0},
		{"step1", mailbox.RemoteFlagBodies},
		{"step3", mailbox.RemoteFlagBodies | mailbox.RemoteFlagSwap | mailbox.RemoteFlagHittable | mailbox.RemoteFlagDetect | mailbox.RemoteFlagIFrameStamp},
		{"step6", 0x00FF&^(mailbox.RemoteFlagFFGateOff|mailbox.RemoteFlagRouyaGateOff) | mailbox.RemoteFlagAttacker | mailbox.RemoteFlagIFrameStamp},
		{"step7", 0x00FF&^(mailbox.RemoteFlagFFGateOff|mailbox.RemoteFlagRouyaGateOff) | mailbox.RemoteFlagAttacker | mailbox.RemoteFlagIFrameStamp |
			mailbox.RemoteFlagFullBodies},
		{"step3+full_bodies", mailbox.RemoteFlagBodies | mailbox.RemoteFlagSwap | mailbox.RemoteFlagHittable | mailbox.RemoteFlagDetect |
			mailbox.RemoteFlagIFrameStamp | mailbox.RemoteFlagFullBodies},
		{"step2,rouya_gate_off,ff_gate_off", mailbox.RemoteFlagBodies | mailbox.RemoteFlagSwap | mailbox.RemoteFlagRouyaGateOff | mailbox.RemoteFlagFFGateOff},
		{"group_wide,both_words,name_a9,guard,attrib,apply,hittable,attacker,iframe_stamp", mailbox.RemoteFlagGroupWide | mailbox.RemoteFlagBothWordsEnable |
			mailbox.RemoteFlagNameA9 | mailbox.RemoteFlagGuard | mailbox.RemoteFlagAttrib | mailbox.RemoteFlagApply | mailbox.RemoteFlagHittable |
			mailbox.RemoteFlagAttacker | mailbox.RemoteFlagIFrameStamp},
	} {
		got, err := ParseRemoteFlags(c.in)
		if err != nil || got != c.want {
			t.Errorf("ParseRemoteFlags(%q) = 0x%04X %v, want 0x%04X", c.in, got, err, c.want)
		}
	}
	// No step preset turns a safety gate off.
	all, _ := ParseRemoteFlags("step7")
	if all&(mailbox.RemoteFlagFFGateOff|mailbox.RemoteFlagRouyaGateOff|mailbox.RemoteFlagGroupWide|mailbox.RemoteFlagBothWordsEnable) != 0 {
		t.Errorf("step7 = 0x%04X carries an opt-out or a widening", all)
	}
	for _, bad := range []string{"bodys", "step8", "stepx", "0x10000", "full_body"} {
		if _, err := ParseRemoteFlags(bad); err == nil {
			t.Errorf("ParseRemoteFlags(%q) parsed", bad)
		}
	}
}

func TestSeedPolicy(t *testing.T) {
	p := SeedPolicy()
	if p[0] != (mailbox.RemotePolicy{Proc: addr.ProcMo2, Flags: mailbox.RemotePFAllow}) {
		t.Errorf("seed[0] = %+v", p[0])
	}
	flags := map[int16]uint8{}
	for _, e := range p {
		if _, ok := flags[e.Proc]; ok {
			t.Errorf("proc 0x%X twice", e.Proc)
		}
		flags[e.Proc] = e.Flags
	}
	// The enemy audit's verdicts (players.go SeedPolicy).
	for proc, want := range map[int16]uint8{
		addr.ProcMo2: mailbox.RemotePFAllow, addr.ProcBK: mailbox.RemotePFAllow, addr.ProcTN: mailbox.RemotePFAllow,
		addr.ProcCC: mailbox.RemotePFAllow, addr.ProcOship: mailbox.RemotePFAllow, addr.ProcKI: mailbox.RemotePFAllow,
		addr.ProcST: mailbox.RemotePFAllow | mailbox.RemotePFNoLatch,
		addr.ProcFM: mailbox.RemotePFDeny, addr.ProcRD: mailbox.RemotePFDeny, addr.ProcPW: mailbox.RemotePFDeny,
		addr.ProcSSS: mailbox.RemotePFDeny, addr.ProcNpcJi1: mailbox.RemotePFDeny, addr.ProcFGMahou: mailbox.RemotePFDeny,
		addr.ProcBGN: mailbox.RemotePFDeny, addr.ProcFganon: mailbox.RemotePFDeny,
	} {
		if flags[proc] != want {
			t.Errorf("proc 0x%X flags %d, want %d", proc, flags[proc], want)
		}
	}
	for proc, f := range flags {
		if f&mailbox.RemotePFAllow != 0 && f&mailbox.RemotePFDeny != 0 || f&mailbox.RemotePFBothWords != 0 {
			t.Errorf("proc 0x%X flags %d", proc, f)
		}
	}
	if _, ok := flags[addr.ProcBoko]; ok || len(p) > mailbox.RemoteProcPolicyMax {
		t.Errorf("seed = %+v", p)
	}
	if ff := SeedFFDeny(); len(ff) != mailbox.RemoteFFDenyMax {
		t.Errorf("FF seed = %v", ff)
	}
}

func TestParseRemotePolicy(t *testing.T) {
	base := []mailbox.RemotePolicy{{Proc: 0xBD, Flags: mailbox.RemotePFAllow}, {Proc: 0x79, Flags: mailbox.RemotePFDeny}}
	got, err := ParseRemotePolicy("0xBD=allow+no_latch, 0x1D5=deny+both_words, 0x79=off", base)
	want := []mailbox.RemotePolicy{{Proc: 0xBD, Flags: mailbox.RemotePFAllow | mailbox.RemotePFNoLatch}, {Proc: 0x1D5, Flags: mailbox.RemotePFDeny | mailbox.RemotePFBothWords}}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Errorf("policy = %+v %v, want %+v", got, err, want)
	}
	if base[0].Flags != mailbox.RemotePFAllow {
		t.Error("ParseRemotePolicy changed its base")
	}
	if got, err := ParseRemotePolicy("", base); err != nil || !reflect.DeepEqual(got, base) {
		t.Errorf("empty = %+v %v", got, err)
	}
	if got, err := ParseRemotePolicy("none", base); err != nil || got != nil {
		t.Errorf("none = %+v %v", got, err)
	}
	for _, bad := range []string{"0xBD", "zz=allow", "0xBD=maybe", "0x8000=allow"} {
		if _, err := ParseRemotePolicy(bad, base); err == nil {
			t.Errorf("%q parsed", bad)
		}
	}
}

// TestRemoteConfigFromEnv: unset is the default bodies (step6, no
// full_bodies, DefaultPolicy of the default kinds) with the contract
// defaults; off, none, 0 are the block off without a warning; every knob
// lands; a bad one keeps its default.
func TestRemoteConfigFromEnv(t *testing.T) {
	for _, k := range []string{"WW_ENEMY_MIRROR_KINDS", "WW_ENEMY_REMOTE", "WW_ENEMY_REMOTE_MARGIN", "WW_ENEMY_REMOTE_MIN_HOLD", "WW_ENEMY_REMOTE_IFRAMES",
		"WW_ENEMY_REMOTE_DY_MAX", "WW_ENEMY_REMOTE_EYE_DY", "WW_ENEMY_REMOTE_CYL_R", "WW_ENEMY_REMOTE_CYL_H",
		"WW_ENEMY_REMOTE_COOLDOWN", "WW_ENEMY_REMOTE_POLICY", "WW_ENEMY_REMOTE_FF",
		"WW_ENEMY_REMOTE_HEAP", "WW_ENEMY_REMOTE_FULL_MAX", "WW_ENEMY_REMOTE_FLAGS2"} {
		t.Setenv(k, "")
	}
	os.Unsetenv("WW_ENEMY_REMOTE") // unset, not only blank
	var warns remoteWarnRep
	c := RemoteConfigFromEnv(&warns)
	step6 := mailbox.RemoteFlagBodies | mailbox.RemoteFlagSwap | mailbox.RemoteFlagHittable | mailbox.RemoteFlagDetect |
		mailbox.RemoteFlagIFrameStamp | mailbox.RemoteFlagApply | mailbox.RemoteFlagAttrib | mailbox.RemoteFlagAttacker |
		mailbox.RemoteFlagNameA9 | mailbox.RemoteFlagGuard
	kinds, _ := ParseKinds(DefaultMirrorKinds)
	if !c.On || c.Flags != step6 || c.Flags&(mailbox.RemoteFlagFullBodies|mailbox.RemoteFlagGroupWide) != 0 || c.HeapGate ||
		!reflect.DeepEqual(c.Policy, DefaultPolicy(kinds)) || warns.n != 0 {
		t.Errorf("defaults = %+v (%d warnings)", c, warns.n)
	}
	off := DefaultRemoteConfig()
	off.On, off.Flags, off.Policy = c.On, c.Flags, c.Policy
	if !reflect.DeepEqual(c, off) {
		t.Errorf("default tunables = %+v", c)
	}
	for _, v := range []string{"off", "OFF", " none ", "0", "false"} {
		t.Setenv("WW_ENEMY_REMOTE", v)
		var w remoteWarnRep
		if c := RemoteConfigFromEnv(&w); c.On || c.Flags != 0 || w.n != 0 {
			t.Errorf("WW_ENEMY_REMOTE=%q: on %v flags 0x%X, %d warnings", v, c.On, c.Flags, w.n)
		}
	}
	t.Setenv("WW_ENEMY_REMOTE", "")
	c = RemoteConfigFromEnv(log.Discard{})
	if c.MarginQ8 != 164 || c.MinHold != 30 || c.BodyIFrames != 30 || c.DYMax != 400 || c.EyeDY != 110 || c.CylR != 30 || c.CylH != 120 || c.CreateCooldown != 30 {
		t.Errorf("tunables = %+v", c)
	}
	if c.HeapGate || c.FullMax != 1 || c.Flags2 != 0 || c.HeapSizes != heap.DefaultThresholds() {
		t.Errorf("full-body defaults = %+v", c)
	}
	t.Setenv("WW_ENEMY_REMOTE", "step2")
	t.Setenv("WW_ENEMY_REMOTE_MARGIN", "200")
	t.Setenv("WW_ENEMY_REMOTE_MIN_HOLD", "0x10")
	t.Setenv("WW_ENEMY_REMOTE_IFRAMES", "300") // bad: keeps 30
	t.Setenv("WW_ENEMY_REMOTE_DY_MAX", "250.5")
	t.Setenv("WW_ENEMY_REMOTE_EYE_DY", "NaN") // bad: keeps 110
	t.Setenv("WW_ENEMY_REMOTE_CYL_R", "40")
	t.Setenv("WW_ENEMY_REMOTE_CYL_H", "150")
	t.Setenv("WW_ENEMY_REMOTE_COOLDOWN", "60")
	t.Setenv("WW_ENEMY_REMOTE_POLICY", "0x1D5=allow")
	t.Setenv("WW_ENEMY_REMOTE_FF", "sea, MajyuE")
	c = RemoteConfigFromEnv(log.Discard{})
	if !c.On || c.Flags != mailbox.RemoteFlagBodies|mailbox.RemoteFlagSwap || c.MarginQ8 != 200 || c.MinHold != 16 || c.BodyIFrames != 30 ||
		c.DYMax != 250.5 || c.EyeDY != 110 || c.CylR != 40 || c.CylH != 150 || c.CreateCooldown != 60 {
		t.Errorf("knobs = %+v", c)
	}
	if last := c.Policy[len(c.Policy)-1]; last.Proc != 0x1D5 || last.Flags != mailbox.RemotePFAllow || len(c.Policy) != len(SeedPolicy())+1 {
		t.Errorf("policy = %+v", c.Policy)
	}
	if !reflect.DeepEqual(c.FFDeny, []string{"sea", "MajyuE"}) {
		t.Errorf("FF = %v", c.FFDeny)
	}
	if c.HeapGate {
		t.Error("heap gate on without full_bodies")
	}

	// The full-body knobs: the gate follows FULL_BODIES.
	t.Setenv("WW_ENEMY_REMOTE", "step7")
	t.Setenv("WW_ENEMY_REMOTE_HEAP", "0xFFFFFFF, 0, 0x10")
	t.Setenv("WW_ENEMY_REMOTE_FULL_MAX", "2")
	t.Setenv("WW_ENEMY_REMOTE_FLAGS2", "full_co")
	c = RemoteConfigFromEnv(log.Discard{})
	if !c.HeapGate || c.Flags&mailbox.RemoteFlagFullBodies == 0 || c.FullMax != 2 || c.Flags2 != mailbox.RemoteFlag2FullCo ||
		c.HeapSizes != (heap.Thresholds{GameLargest: 0xFFFFFFF, ZeldaLargest: 0x10, ArchiveLargest: heap.GateArchiveLargest}) {
		t.Errorf("full knobs = %+v", c)
	}
	t.Setenv("WW_ENEMY_REMOTE_HEAP", "off")
	t.Setenv("WW_ENEMY_REMOTE_FLAGS2", "0x1")
	if c = RemoteConfigFromEnv(log.Discard{}); c.HeapSizes != (heap.Thresholds{}) || c.Flags2 != 1 {
		t.Errorf("heap off: %+v", c)
	}
	for _, bad := range [][2]string{{"WW_ENEMY_REMOTE_HEAP", "1,2"}, {"WW_ENEMY_REMOTE_HEAP", "a,b,c"}, {"WW_ENEMY_REMOTE_FLAGS2", "co"},
		{"WW_ENEMY_REMOTE_FULL_MAX", "300"}} {
		t.Setenv("WW_ENEMY_REMOTE_HEAP", "")
		t.Setenv("WW_ENEMY_REMOTE_FLAGS2", "")
		t.Setenv("WW_ENEMY_REMOTE_FULL_MAX", "")
		t.Setenv(bad[0], bad[1])
		if c = RemoteConfigFromEnv(log.Discard{}); c.HeapSizes != heap.DefaultThresholds() || c.Flags2 != 0 || c.FullMax != 1 {
			t.Errorf("%s=%q kept no default: %+v", bad[0], bad[1], c)
		}
	}

	t.Setenv("WW_ENEMY_REMOTE", "warp")
	t.Setenv("WW_ENEMY_REMOTE_FF", "none")
	var bad remoteWarnRep
	if c = RemoteConfigFromEnv(&bad); c.On || c.FFDeny != nil || !strings.Contains(bad.all, "stays off") {
		t.Errorf("bad flags: %+v (warnings %q)", c, bad.all)
	}
	t.Setenv("WW_ENEMY_REMOTE_FF", "a,b,c,d,e")
	if c = RemoteConfigFromEnv(log.Discard{}); !reflect.DeepEqual(c.FFDeny, SeedFFDeny()) {
		t.Errorf("5 FF names: %v", c.FFDeny)
	}
}

// remoteWarnRep counts the Warns logged through it and keeps their text.
type remoteWarnRep struct {
	n   int
	all string
}

func (r *remoteWarnRep) Log(level log.Level, msg string) {
	if level == log.Warn {
		r.n++
		r.all += msg + "|"
	}
}

// TestDefaultPolicyPinned: the default bodies' PROC_POLICY ALLOWs exactly
// the mirrored kinds' procs (the Moblin, Bokoblin and ChuChu by default;
// a Keese named in the kinds too), never the Stalfos or any other seed
// ALLOW, and keeps the seed's DENY list.
func TestDefaultPolicyPinned(t *testing.T) {
	split := func(p []mailbox.RemotePolicy) (allow, deny []int16) {
		for _, e := range p {
			switch e.Flags {
			case mailbox.RemotePFAllow:
				allow = append(allow, e.Proc)
			case mailbox.RemotePFDeny:
				deny = append(deny, e.Proc)
			default:
				t.Errorf("proc 0x%X flags 0x%X", e.Proc, e.Flags)
			}
		}
		return allow, deny
	}
	var seedDeny []int16
	for _, e := range SeedPolicy() {
		if e.Flags == mailbox.RemotePFDeny {
			seedDeny = append(seedDeny, e.Proc)
		}
	}
	for _, c := range []struct {
		kinds string
		allow []int16
	}{
		{DefaultMirrorKinds, []int16{addr.ProcMo2, addr.ProcBK, addr.ProcCC}},
		{"mo2,ki", []int16{addr.ProcMo2, addr.ProcKI}},
		{"none", nil},
	} {
		kinds, err := ParseKinds(c.kinds)
		if err != nil {
			t.Fatal(err)
		}
		allow, deny := split(DefaultPolicy(kinds))
		if !reflect.DeepEqual(allow, c.allow) || !reflect.DeepEqual(deny, seedDeny) || len(deny) != 15 {
			t.Errorf("kinds %q: allow %X deny %X, want allow %X deny %X", c.kinds, allow, deny, c.allow, seedDeny)
		}
		if slices.Contains(allow, addr.ProcST) {
			t.Errorf("kinds %q: the Stalfos is ALLOW", c.kinds)
		}
	}
	// The env-less config takes WW_ENEMY_MIRROR_KINDS's set.
	t.Setenv("WW_ENEMY_REMOTE", "")
	t.Setenv("WW_ENEMY_MIRROR_KINDS", "mo2,ki")
	t.Setenv("WW_ENEMY_REMOTE_POLICY", "")
	if allow, _ := split(RemoteConfigFromEnv(log.Discard{}).Policy); !reflect.DeepEqual(allow, []int16{addr.ProcMo2, addr.ProcKI}) {
		t.Errorf("WW_ENEMY_MIRROR_KINDS=mo2,ki: allow %X", allow)
	}
}

// TestRemoteHostPHitDefaultPolicy: under the default policy and kinds, a
// Darknut (proc TN: SeedPolicy's ALLOW, not mirrored by default) hitting
// a remote body is no PHIT (DropNotAllowed), while a Bokoblin passes that
// filter.
func TestRemoteHostPHitDefaultPolicy(t *testing.T) {
	_, _, r := remoteHostGame(t, mailbox.RemoteFlagDetect)
	r.Kinds, _ = ParseKinds(DefaultMirrorKinds)
	r.cfg.Policy = DefaultPolicy(r.Kinds)
	tn := ringHit(0, 0, 1, 200)
	tn.Proc = addr.ProcTN
	if p := r.phit(&tn, &mailbox.RemoteBlock{}, Scope{}, &Listing{}); p != nil || r.Stats.DropNotAllowed != 1 {
		t.Errorf("a Darknut's hit: %+v, drops %d", p, r.Stats.DropNotAllowed)
	}
	bk := ringHit(0, 0, 2, 200)
	bk.Proc = addr.ProcBK
	r.phit(&bk, &mailbox.RemoteBlock{}, Scope{}, &Listing{})
	if r.Stats.DropNotAllowed != 1 {
		t.Errorf("a Bokoblin's hit was not allowed: drops %d", r.Stats.DropNotAllowed)
	}
}

// --- PSTATE ------------------------------------------------------------------

// pstateGame is M_Dai room 1 with Link at fakegame.PlayerActor, full life,
// no event.
func pstateGame(t *testing.T) (*mem.FakeMem, Scope) {
	t.Helper()
	m := mem.NewFakeMem()
	g := fakegame.New(m)
	if err := g.LoadStage(mDai); err != nil {
		t.Fatal(err)
	}
	if err := g.EnterRoom(1); err != nil {
		t.Fatal(err)
	}
	store(t, m, addr.Life, []byte{0, 12})
	scope, err := ReadScope(m)
	if err != nil || !scope.InPlay {
		t.Fatalf("scope %+v %v", scope, err)
	}
	return m, scope
}

func TestReadPState(t *testing.T) {
	m, scope := pstateGame(t)
	link := fakegame.PlayerActor
	u32 := func(v uint32) []byte { return binary.BigEndian.AppendUint32(nil, v) }
	u16 := func(v uint16) []byte { return binary.BigEndian.AppendUint16(nil, v) }
	store(t, m, link+addr.PlayerOffCutType, []byte{8, 3, 0, 0, 0x00, 0x1E})
	store(t, m, link+addr.PlayerOffShieldAngleY, u16(0xC000))
	store(t, m, link+addr.PlayerOffCurProc, u32(addr.PlayerProcCrouchDefense))
	store(t, m, link+addr.PlayerOffModeFlg, u32(addr.PlayerModeDamage|addr.PlayerModeSwim))
	p, err := ReadPState(m, scope, 7)
	if err != nil {
		t.Fatal(err)
	}
	want := &PState{Flags: PSGuard | PSCrouchDef | PSDamageMode | PSSwim, Epoch: 7, Stage: "M_Dai", Room: 1, CutType: 8, CutCount: 3, IFrames: 30, ShieldAngleY: -0x4000}
	if !reflect.DeepEqual(p, want) {
		t.Errorf("pstate\n got %+v\nwant %+v", p, want)
	}
	for _, c := range []struct {
		name  string
		setup func()
		flags uint8
	}{
		{"walking", func() {
			store(t, m, link+addr.PlayerOffCurProc, u32(6))
			store(t, m, link+addr.PlayerOffModeFlg, u32(0))
		}, 0},
		{"upper ATNG", func() { store(t, m, link+addr.PlayerOffUpperAnm, u16(addr.PlayerAnmAtng)) }, PSGuard},
		{"upper ATNGHAM", func() { store(t, m, link+addr.PlayerOffUpperAnm, u16(addr.PlayerAnmAtngHam)) }, PSGuard},
		{"guard slip", func() {
			store(t, m, link+addr.PlayerOffUpperAnm, u16(0xFFFF))
			store(t, m, link+addr.PlayerOffCurProc, u32(addr.PlayerProcGuardSlip))
		}, PSGuard},
		{"crouch defense slip", func() { store(t, m, link+addr.PlayerOffCurProc, u32(addr.PlayerProcCrouchDefenseSlip)) }, PSGuard | PSCrouchDef},
		{"on ship", func() {
			store(t, m, link+addr.PlayerOffCurProc, u32(6))
			store(t, m, link+addr.PlayerOffModeFlg, u32(addr.PlayerModeInShip))
		}, PSOnShip},
		{"death demo", func() {
			store(t, m, link+addr.PlayerOffModeFlg, u32(0))
			store(t, m, link+addr.PlayerOffCurProc, u32(addr.PlayerProcDemoDead))
		}, PSDead},
		{"life 0", func() {
			store(t, m, link+addr.PlayerOffCurProc, u32(6))
			store(t, m, addr.Life, u16(0))
		}, PSDead},
		{"event", func() {
			store(t, m, addr.Life, u16(12))
			store(t, m, addr.EvtMode, []byte{1})
		}, PSInEvent},
		{"player demo", func() {
			store(t, m, addr.EvtMode, []byte{0})
			store(t, m, link+addr.PlayerOffDemoType, u16(3))
		}, PSInEvent},
	} {
		c.setup()
		p, err := ReadPState(m, scope, 0)
		if err != nil || p.Flags != c.flags {
			t.Errorf("%s: flags 0x%02X %v, want 0x%02X", c.name, p.Flags, err, c.flags)
		}
	}
	// Out of play: LOADING alone, with the scope's stage and room.
	out := scope
	out.InPlay = false
	if p, err := ReadPState(m, out, 0); err != nil || p.Flags != PSLoading || p.Stage != "M_Dai" || p.Room != 1 || p.CutType != 0 {
		t.Errorf("out of play: %+v %v", p, err)
	}
}

// --- host --------------------------------------------------------------------

// remoteHostGame is M_Dai room 1 with the REL linked at frame 100 and a
// host side with the block on (flags), epoch 9.
func remoteHostGame(t *testing.T, flags uint16) (*mem.FakeMem, Scope, *RemoteHost) {
	t.Helper()
	m, scope := pstateGame(t)
	hostRELFrame(t, m, 100)
	cfg := DefaultRemoteConfig()
	cfg.On, cfg.Flags = true, flags
	return m, scope, NewRemoteHost(m, log.Discard{}, cfg, 9)
}

func readBlock(t *testing.T, m *mem.FakeMem) mailbox.RemoteBlock {
	t.Helper()
	blk, tail, _, err := mailbox.ReadRemoteBlock(m)
	if err != nil || tail != blk.Seq || blk.Seq == 0 {
		t.Fatalf("block: %v tail %d head %d", err, tail, blk.Seq)
	}
	return blk
}

// storeStatus stores st as the REL's span (the game's write).
func storeStatus(t *testing.T, m *mem.FakeMem, st *mailbox.RemoteStatus) {
	t.Helper()
	store(t, m, mailbox.RemoteBlockAddr+mailbox.RemoteAckSeqOff, st.Encode())
}

// TestRemoteHostSlots: slot i is pose-feed slot i, with the roster's
// position and angle and the owner's PSTATE (stage, room, cut, i-frames,
// shield angle, sflags translated); an owner change bumps gen; a stale or
// missing PSTATE leaves no stage and NO_HIT; the block carries the role,
// flags, tunables, policy and FF denylist.
func TestRemoteHostSlots(t *testing.T) {
	m, scope, r := remoteHostGame(t, mailbox.RemoteFlagBodies|mailbox.RemoteFlagSwap)
	now := time.Unix(100, 0)
	views := []posefeed.RemoteView{
		{ID: 4, Name: "bob", LinkSlot: 1, Pos: [3]float32{10, 20, 30}, RotY: 0x1000, Live: true},
		{ID: 5, Name: "carol", LinkSlot: -1, Pos: [3]float32{1, 2, 3}, Live: false},
		{ID: 6, Name: "dave", LinkSlot: 0, Pos: [3]float32{4, 5, 6}, RotY: -2, Live: true},
	}
	r.OnPState(&PState{Client: 2, PlayerID: 4, Flags: PSGuard, Stage: "M_Dai", Room: 1, CutType: 1, CutCount: 2, IFrames: 5, ShieldAngleY: 77}, now)
	r.OnPState(&PState{Client: 3, PlayerID: 0, Stage: "M_Dai", Room: 1}, now) // unmapped: dropped
	if _, err := r.Tick(now, scope, true, views, now, nil); err != nil {
		t.Fatal(err)
	}
	blk := readBlock(t, m)
	if blk.Magic != mailbox.RemoteMagic || blk.Role != mailbox.RemoteRoleHost || blk.Layout != mailbox.RemoteLayout ||
		blk.Flags != mailbox.RemoteFlagBodies|mailbox.RemoteFlagSwap || blk.MarginQ8 != 164 || blk.DYMax != 400 ||
		!reflect.DeepEqual(blk.Policy, SeedPolicy()) || !reflect.DeepEqual(blk.FFDeny, SeedFFDeny()) || blk.Apply != (mailbox.RemoteApply{}) {
		t.Errorf("block %+v", blk)
	}
	s1 := blk.Slots[1]
	want1 := mailbox.RemoteSlot{Active: true, SFlags: mailbox.RemoteSFTargetable | mailbox.RemoteSFGuard, Room: 1, CutType: 1, CutCount: 2,
		PlayerID: 4, IFrames: 5, Stage: "M_Dai", Pos: [3]float32{10, 20, 30}, AngleY: 0x1000, ShieldAngleY: 77,
		EyeDY: 110, CylR: 30, CylH: 120, PoseSlot: 1, Gen: 1}
	if s1 != want1 {
		t.Errorf("slot 1\n got %+v\nwant %+v", s1, want1)
	}
	// Dave has no PSTATE: active, no stage, NO_HIT.
	if s0 := blk.Slots[0]; !s0.Active || s0.PlayerID != 6 || s0.Stage != "" || s0.SFlags != mailbox.RemoteSFNoHit || s0.PoseSlot != 0 || s0.Pos != [3]float32{4, 5, 6} {
		t.Errorf("slot 0 = %+v", s0)
	}
	if blk.Slots[2].Active || blk.Slots[3].Active {
		t.Error("slots past MaxRemoteLinks written")
	}
	if r.Stats.PStatesUnmapped != 1 {
		t.Errorf("unmapped %d", r.Stats.PStatesUnmapped)
	}

	// Slot 1 changes owner: gen 2. Slot 0 freed: inactive.
	later := now.Add(time.Second)
	views = []posefeed.RemoteView{{ID: 5, LinkSlot: 1, Live: true}}
	if _, err := r.Tick(later, scope, true, views, later, nil); err != nil {
		t.Fatal(err)
	}
	blk = readBlock(t, m)
	if blk.Slots[1].PlayerID != 5 || blk.Slots[1].Gen != 2 || blk.Slots[0].Active {
		t.Errorf("after change: %+v / %+v", blk.Slots[0], blk.Slots[1])
	}
	// Back to bob, with his PSTATE now stale.
	stale := now.Add(PStateStale + time.Second)
	views = []posefeed.RemoteView{{ID: 4, LinkSlot: 1, Live: true}}
	if _, err := r.Tick(stale, scope, true, views, stale, nil); err != nil {
		t.Fatal(err)
	}
	blk = readBlock(t, m)
	if s := blk.Slots[1]; s.Gen != 3 || s.Stage != "" || s.SFlags != mailbox.RemoteSFNoHit {
		t.Errorf("stale PSTATE: %+v", s)
	}
	// A stale roster empties every slot.
	if _, err := r.Tick(stale.Add(time.Second), scope, true, views, stale.Add(-RosterStale), nil); err != nil {
		t.Fatal(err)
	}
	if blk = readBlock(t, m); blk.Slots[1].Active {
		t.Errorf("stale roster: %+v", blk.Slots[1])
	}
}

// TestRemoteSlotFlags: TARGETABLE only in the host's stage and room,
// alive, in play, not in an event and drawn live; NO_HIT while loading,
// in an event or stale; the rest copied bit by bit.
func TestRemoteSlotFlags(t *testing.T) {
	scope := Scope{Stage: "M_Dai", Room: 1, InPlay: true}
	here := PState{Stage: "M_Dai", Room: 1}
	for _, c := range []struct {
		name string
		p    PState
		live bool
		want uint8
	}{
		{"here", here, true, mailbox.RemoteSFTargetable},
		{"other room", PState{Stage: "M_Dai", Room: 2}, true, 0},
		{"other stage", PState{Stage: "sea", Room: 1}, true, 0},
		{"stale pose", here, false, mailbox.RemoteSFNoHit},
		{"loading", PState{Stage: "M_Dai", Room: 1, Flags: PSLoading}, true, mailbox.RemoteSFNoHit},
		{"event", PState{Stage: "M_Dai", Room: 1, Flags: PSInEvent}, true, mailbox.RemoteSFNoHit | mailbox.RemoteSFInEvent},
		{"dead", PState{Stage: "M_Dai", Room: 1, Flags: PSDead}, true, mailbox.RemoteSFDead},
		{"guard crouch swim ship", PState{Stage: "M_Dai", Room: 1, Flags: PSGuard | PSCrouchDef | PSSwim | PSOnShip | PSDamageMode}, true,
			mailbox.RemoteSFTargetable | mailbox.RemoteSFGuard | mailbox.RemoteSFCrouchDef | mailbox.RemoteSFSwim | mailbox.RemoteSFOnShip},
	} {
		if got := slotFlags(&c.p, scope, c.live); got != c.want {
			t.Errorf("%s: 0x%02X, want 0x%02X", c.name, got, c.want)
		}
	}
	if got := slotFlags(&here, Scope{Stage: "M_Dai", Room: 1}, true); got&mailbox.RemoteSFTargetable != 0 {
		t.Error("targetable with the host out of play")
	}
}

// TestRemoteHostGates: off (no WW_ENEMY_REMOTE) or without the REL, the
// host side writes nothing; Shutdown writes magic 0 only after a write.
func TestRemoteHostGates(t *testing.T) {
	m, scope, r := remoteHostGame(t, 0)
	now := time.Unix(100, 0)
	m.Record(true)
	off := NewRemoteHost(m, log.Discard{}, DefaultRemoteConfig(), 9)
	if _, err := off.Tick(now, scope, true, nil, now, nil); err != nil {
		t.Fatal(err)
	}
	if err := off.Shutdown(); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Tick(now, scope, false, nil, now, nil); err != nil {
		t.Fatal(err)
	}
	if w := m.Writes(); len(w) != 0 {
		t.Fatalf("writes with the block off or no REL: %+v", w)
	}
	// Step 0: on with every flag clear writes the block (magic, no flags).
	if _, err := r.Tick(now, scope, true, nil, now, nil); err != nil {
		t.Fatal(err)
	}
	if blk := readBlock(t, m); blk.Flags != 0 || blk.Magic != mailbox.RemoteMagic {
		t.Errorf("step 0 block %+v", blk)
	}
	if err := r.Shutdown(); err != nil {
		t.Fatal(err)
	}
	if blk, _, _, _ := mailbox.ReadRemoteBlock(m); blk.Magic != 0 {
		t.Error("Shutdown left the magic")
	}
}

// TestRemoteHostEpoch: the WWRP epoch moves on a stage change (in play
// throughout, it does not move between ticks), and seq never repeats.
func TestRemoteHostEpoch(t *testing.T) {
	m, scope, r := remoteHostGame(t, 0)
	now := time.Unix(100, 0)
	r.Tick(now, scope, true, nil, now, nil)
	a := readBlock(t, m)
	r.Tick(now, scope, true, nil, now, nil)
	b := readBlock(t, m)
	other := scope
	other.Stage = "sea"
	r.Tick(now, other, true, nil, now, nil)
	c := readBlock(t, m)
	if a.Epoch != b.Epoch || c.Epoch == b.Epoch || b.Seq != a.Seq+1 || c.Seq != b.Seq+1 {
		t.Errorf("epochs %d %d %d seqs %d %d %d", a.Epoch, b.Epoch, c.Epoch, a.Seq, b.Seq, c.Seq)
	}
}

// TestRemoteHostEpochReload (coop-slice-drc.md A1): the epoch, which
// makes remote.c reset every slot's create tries (rl_intake), also moves
// when the host comes back into play in the same stage (a void-out or
// Game Over reload) and when a remote's load ends in the host's stage;
// not while the host is out of play, not at a remote's load start, not
// for a remote that arrives in another stage, and once for a stage
// change that is also the return to play.
func TestRemoteHostEpochReload(t *testing.T) {
	m, scope, r := remoteHostGame(t, mailbox.RemoteFlagBodies)
	now := time.Unix(100, 0)
	views := []posefeed.RemoteView{{ID: 4, LinkSlot: 0, Pos: [3]float32{1, 2, 3}, Live: true}}
	epoch := func(sc Scope, p *PState, pos [3]float32) uint32 {
		t.Helper()
		now = now.Add(100 * time.Millisecond)
		if p != nil {
			p.PlayerID = 4
			r.OnPState(p, now)
		}
		views[0].Pos, views[0].Target = pos, pos
		if _, err := r.Tick(now, sc, true, views, now, nil); err != nil {
			t.Fatal(err)
		}
		return readBlock(t, m).Epoch
	}
	here := &PState{Stage: "M_Dai", Room: 1}
	e0 := epoch(scope, here, [3]float32{1, 2, 3})
	if e := epoch(scope, here, [3]float32{1, 2, 3}); e != e0 {
		t.Fatalf("in play: epoch %d -> %d", e0, e)
	}
	// The host's void-out: out of play (no move), back in the same stage (one).
	out := scope
	out.InPlay, out.Pending = false, true
	if e := epoch(out, here, [3]float32{1, 2, 3}); e != e0 {
		t.Errorf("out of play: epoch %d -> %d", e0, e)
	}
	e1 := epoch(scope, here, [3]float32{1, 2, 3})
	if e1 != e0+1 {
		t.Errorf("back in play: epoch %d, want %d", e1, e0+1)
	}
	// The remote's void-out: loading (no move), its load ends in the host's
	// stage (one), then in play (no move).
	if e := epoch(scope, &PState{Stage: "M_Dai", Room: 1, Flags: PSLoading}, [3]float32{1, 2, 3}); e != e1 {
		t.Errorf("remote loading: epoch %d -> %d", e1, e)
	}
	e2 := epoch(scope, here, [3]float32{7, 8, 9})
	if e2 != e1+1 {
		t.Errorf("remote arrived: epoch %d, want %d", e2, e1+1)
	}
	if e := epoch(scope, here, [3]float32{7, 8, 9}); e != e2 {
		t.Errorf("remote in play: epoch %d -> %d", e2, e)
	}
	// A remote load that ends in another stage: no move.
	epoch(scope, &PState{Stage: "M_Dai", Room: 1, Flags: PSLoading}, [3]float32{7, 8, 9})
	if e := epoch(scope, &PState{Stage: "sea", Room: 1}, [3]float32{5, 5, 5}); e != e2 {
		t.Errorf("remote arrived elsewhere: epoch %d -> %d", e2, e)
	}
	// A new stage on the return to play: one.
	epoch(out, nil, [3]float32{5, 5, 5})
	sea := scope
	sea.Stage = "sea"
	if e := epoch(sea, nil, [3]float32{5, 5, 5}); e != e2+1 {
		t.Errorf("new stage on return: epoch %d, want %d", e, e2+1)
	}
}

// TestRemoteHostOverlap: the scope as ReadScope reads it through a
// void-out reload. The request (mEnable and the overlap set) and the new
// play scene's create phases before phase_4 (mEnable cleared, Link's
// pointer still the deleted Link's, the overlap live until the fade-in
// ends) are both out of play: the PSTATE is LOADING alone and the epoch
// does not move; the fade-in's end is the one return to play, one move.
// Without Scope.Loading the create phases read as in play: a PSTATE of
// the deleted Link and a second move when phase_4 nulls the pointer.
func TestRemoteHostOverlap(t *testing.T) {
	m, scope, r := remoteHostGame(t, mailbox.RemoteFlagBodies)
	now := time.Unix(100, 0)
	views := []posefeed.RemoteView{{ID: 4, LinkSlot: 0, Pos: [3]float32{1, 2, 3}, Live: true}}
	tick := func(name string, inPlay, pending, loading bool) uint32 {
		t.Helper()
		sc, err := ReadScope(m)
		if err != nil || sc.InPlay != inPlay || sc.Pending != pending || sc.Loading != loading || sc.Stage != scope.Stage || sc.Room != scope.Room {
			t.Fatalf("%s: scope %+v %v", name, sc, err)
		}
		p, err := ReadPState(m, sc, 0)
		if err != nil || (p.Flags == PSLoading) == inPlay {
			t.Errorf("%s: pstate %+v %v", name, p, err)
		}
		now = now.Add(100 * time.Millisecond)
		r.OnPState(&PState{PlayerID: 4, Stage: "M_Dai", Room: 1}, now)
		if _, err := r.Tick(now, sc, true, views, now, nil); err != nil {
			t.Fatal(err)
		}
		return readBlock(t, m).Epoch
	}
	e0 := tick("in play", true, false, false)
	store(t, m, addr.NextStageEnable, []byte{1})
	store(t, m, addr.OverlapReq, binary.BigEndian.AppendUint32(nil, addr.OverlapRequest))
	if e := tick("requested", false, true, true); e != e0 {
		t.Errorf("requested: epoch %d -> %d", e0, e)
	}
	store(t, m, addr.NextStageEnable, []byte{0})
	for i := range 3 {
		if e := tick(fmt.Sprintf("create phase %d", i), false, false, true); e != e0 {
			t.Errorf("create phase %d: epoch %d -> %d", i, e0, e)
		}
	}
	store(t, m, addr.OverlapReq, []byte{0, 0, 0, 0})
	if e := tick("fade-in done", true, false, false); e != e0+1 {
		t.Errorf("fade-in done: epoch %d, want %d", e, e0+1)
	}
	if e := tick("in play again", true, false, false); e != e0+1 {
		t.Errorf("in play again: epoch %d, want %d", e, e0+1)
	}
}

// ringHit is a Moblin spear hit on body slot.
func ringHit(n uint32, slot uint8, setID uint16, frame uint32) mailbox.RemoteHit {
	return mailbox.RemoteHit{Seq: uint16(n), Slot: slot, PID: 0x900 + uint32(setID), SetID: setID, Proc: addr.ProcMo2, AtType: 0x800, Atp: 2, Se: 3,
		HitMark: 0x0D, Spl: 6, RelHit: [3]float32{1, 50, -1}, RVec: [3]float32{0, 0, 9}, Frame: frame}
}

// push stores hits as the REL pushes them: head counts, entry (n-1)%4.
func push(t *testing.T, m *mem.FakeMem, st *mailbox.RemoteStatus, hits ...mailbox.RemoteHit) {
	t.Helper()
	for _, h := range hits {
		st.HitRingHead++
		h.Seq = uint16(st.HitRingHead)
		st.Hits[(st.HitRingHead-1)%mailbox.RemoteHitRingLen] = h
	}
	storeStatus(t, m, st)
}

// TestRemoteHostPHit: the ring entries pushed before the host's first read
// are not sent; a new Moblin hit on a slot becomes a PHIT to the client
// of the PSTATE whose player owns the slot, with the host's epoch, scope
// and the entry's attacker fields, the attacker named by its handle in
// the host's listing (looked up by the ring entry's attacker pid: an
// unkeyed listed one, HF_NOT_KEYED in the ring, gets its handle too); an
// attacker the host does not list goes with NoMirrorHandle (the client's
// stand-in owner); unowned and clientless hits are dropped and counted;
// one per (client, attacker) per PHitRateFrames.
func TestRemoteHostPHit(t *testing.T) {
	m, scope, r := remoteHostGame(t, mailbox.RemoteFlagDetect)
	r.Kinds, _ = ParseKinds("mo2")
	now := time.Unix(100, 0)
	// ringHit's attacker pid is 0x900+setID: setIDs 1, 3 and 5 are listed
	// (handles 7, 9, 11), and the unkeyed Moblin of pid 0xC00 (handle 12).
	l := &Listing{Entries: []Listed{{PID: 0x901, Handle: 7}, {PID: 0x903, Handle: 9}, {PID: 0x905, Handle: 11}, {PID: 0xC00, Handle: 12}}}
	views := []posefeed.RemoteView{{ID: 4, LinkSlot: 0, Live: true}, {ID: 6, LinkSlot: 1, Live: true}}
	r.OnPState(&PState{Client: 2, PlayerID: 4, Flags: PSGuard, Stage: "M_Dai", Room: 1}, now)
	var st mailbox.RemoteStatus
	push(t, m, &st, ringHit(0, 0, 1, 50)) // before this Go: never sent
	if out, err := r.Tick(now, scope, true, views, now, l); err != nil || len(out) != 0 {
		t.Fatalf("first tick: %v %v", out, err)
	}
	shield := ringHit(0, 0, 1, 200)
	shield.HFlags = mailbox.RemoteHFShield
	notMo2 := ringHit(0, 0, 2, 200)
	notMo2.HFlags, notMo2.Proc = mailbox.RemoteHFNotMo2, addr.ProcBK
	notKeyed := ringHit(0, 0, 0x300, 200)
	notKeyed.HFlags = mailbox.RemoteHFNotKeyed
	push(t, m, &st, shield, notMo2, notKeyed, ringHit(0, 1, 1, 200)) // the last: slot 1's owner has no PSTATE
	out, err := r.Tick(now, scope, true, views, now, l)
	if err != nil || len(out) != 3 {
		t.Fatalf("PHITs %+v %v", out, err)
	}
	want := &PHit{Client: 2, Flags: PHitHostShield | PHitHostGuard, Epoch: 9, Stage: "M_Dai", Room: 1, Atp: 2, SetID: 7, Seq: 1,
		Se: 3, HitMark: 0x0D, AtType: 0x800, Spl: 6, RelHit: [3]float32{1, 50, -1}, RVec: [3]float32{0, 0, 9}, HostFrame: 200}
	if !reflect.DeepEqual(out[0], want) {
		t.Errorf("PHIT\n got %+v\nwant %+v", out[0], want)
	}
	if out[1].SetID != NoMirrorHandle || out[1].Seq != 2 {
		t.Errorf("stand-in PHIT (an unlisted Bokoblin): %+v", out[1])
	}
	if out[2].SetID != 12 || out[2].Seq != 3 {
		t.Errorf("listed unkeyed PHIT: %+v", out[2])
	}
	if s := r.Stats; s.StandIn != 1 || s.DropNoClient != 1 || s.PHits != 3 {
		t.Errorf("stats %+v", s)
	}
	// The rate limit: same (client, attacker) within 15 frames dropped,
	// another attacker and a later frame sent; seq counts per client.
	push(t, m, &st, ringHit(0, 0, 1, 210), ringHit(0, 0, 3, 211), ringHit(0, 0, 1, 215))
	out, _ = r.Tick(now, scope, true, views, now, l)
	if len(out) != 2 || out[0].SetID != 9 || out[0].Seq != 4 || out[1].SetID != 7 || out[1].Seq != 5 || r.Stats.DropRate != 1 {
		t.Errorf("rate: %+v drops %d", out, r.Stats.DropRate)
	}
	// Not an enabled kind and not ALLOW (a host bomb, proc 0x14C here), or no
	// attacker actor at all: dropped, never sent to the client.
	bomb := ringHit(0, 0, 5, 400)
	bomb.HFlags, bomb.Proc = mailbox.RemoteHFNotMo2, 0x14C
	noActor := ringHit(0, 0, 6, 400)
	noActor.HFlags, noActor.PID = mailbox.RemoteHFNotKeyed, 0
	push(t, m, &st, bomb, noActor)
	if out, _ = r.Tick(now, scope, true, views, now, l); len(out) != 0 || r.Stats.DropNotAllowed != 2 {
		t.Errorf("not allowed: %+v %+v", out, r.Stats)
	}
	// An unowned slot (the remote left).
	push(t, m, &st, ringHit(0, 0, 4, 300))
	if out, _ = r.Tick(now, scope, true, nil, now, l); len(out) != 0 || r.Stats.DropNoOwner != 1 {
		t.Errorf("unowned: %+v %+v", out, r.Stats)
	}
	// The REL relinked: the head starts over and the new hit is sent.
	st = mailbox.RemoteStatus{}
	push(t, m, &st, ringHit(0, 0, 5, 10))
	if out, _ = r.Tick(now, scope, true, views, now, l); len(out) != 1 || out[0].SetID != 11 {
		t.Errorf("relink: %+v", out)
	}
}

// TestRemoteAttrib: with ATTRIB on, a HIT from a slot's owner carries
// HAF_ATTRIB_BODY (plus HAF_SLOT1 for slot 1) in its hit_apply; player 0,
// a player without a slot, or ATTRIB off carry none. The HIT's cut type
// and count override the PSTATE's in that slot for HitCutHold.
func TestRemoteAttrib(t *testing.T) {
	m, scope, r := remoteHostGame(t, mailbox.RemoteFlagAttrib)
	now := time.Unix(100, 0)
	views := []posefeed.RemoteView{{ID: 4, LinkSlot: 0, Live: true}, {ID: 6, LinkSlot: 1, Live: true}}
	r.OnPState(&PState{Client: 2, PlayerID: 6, Stage: "M_Dai", Room: 1, CutType: 1, CutCount: 1}, now)
	r.Tick(now, scope, true, views, now, nil)
	for _, c := range []struct {
		player uint8
		want   uint8
	}{
		{4, mailbox.RemoteHAFAttribBody},
		{6, mailbox.RemoteHAFAttribBody | mailbox.RemoteHAFSlot1},
		{0, 0},
		{9, 0},
	} {
		if got := r.AttribFlags(&Hit{PlayerID: c.player}); got != c.want {
			t.Errorf("player %d: 0x%02X, want 0x%02X", c.player, got, c.want)
		}
	}
	a := newHitApplier(m, log.Discard{}, 9)
	a.Attrib = r.AttribFlags
	if ha := a.hitApply(&pendingHit{hit: Hit{PlayerID: 6, SetID: 1}, seq: 1}); ha.Flags != mailbox.RemoteHAFAttribBody|mailbox.RemoteHAFSlot1 {
		t.Errorf("hit_apply flags 0x%02X", ha.Flags)
	}
	r.NoteHit(&Hit{PlayerID: 6, CutType: 0x0B, CutCount: 4}, now)
	r.Tick(now, scope, true, views, now, nil)
	if s := readBlock(t, m).Slots[1]; s.CutType != 0x0B || s.CutCount != 4 {
		t.Errorf("HIT cut not staged: %+v", s)
	}
	later := now.Add(HitCutHold + time.Millisecond)
	r.OnPState(&PState{Client: 2, PlayerID: 6, Stage: "M_Dai", Room: 1, CutType: 1, CutCount: 1}, later)
	r.Tick(later, scope, true, views, later, nil)
	if s := readBlock(t, m).Slots[1]; s.CutType != 1 || s.CutCount != 1 {
		t.Errorf("HIT cut held too long: %+v", s)
	}
	off := NewRemoteHost(m, log.Discard{}, DefaultRemoteConfig(), 9)
	if off.AttribFlags(&Hit{PlayerID: 4}) != 0 {
		t.Error("ATTRIB with the block off")
	}
}

// --- client ------------------------------------------------------------------

// remoteClientGame is pstateGame with the REL linked at frame 100, a
// client side with flags, and a gate open on host epoch 7.
func remoteClientGame(t *testing.T, on bool, flags uint16) (*mem.FakeMem, Scope, *RemoteClient, ClientGate) {
	t.Helper()
	m, scope := pstateGame(t)
	hostRELFrame(t, m, 100)
	cfg := DefaultRemoteConfig()
	cfg.On, cfg.Flags = on, flags
	return m, scope, NewRemoteClient(m, log.Discard{}, nil, cfg, time.Unix(3, 0)), ClientGate{RELOK: true, HostEpoch: 7, HaveHost: true, Frame: 1234, Epoch: 3}
}

func phitFor(seq uint16) *PHit {
	return &PHit{Client: 2, Flags: PHitHostShield, Epoch: 7, Stage: "M_Dai", Room: 1, Atp: 2, SetID: 1, Seq: seq, Se: 3, HitMark: 0x0D,
		AtType: 0x800, Spl: 6, RelHit: [3]float32{1, 2, 3}, RVec: [3]float32{0, 0, 9}, HostFrame: 77}
}

// splitOut decodes a Tick's payloads into PSTATEs and PACKs.
func splitOut(t *testing.T, out [][]byte) (ps []*PState, packs []*PAck) {
	t.Helper()
	for _, b := range out {
		switch b[0] {
		case SubPState:
			p, err := DecodePState(b)
			if err != nil {
				t.Fatal(err)
			}
			ps = append(ps, p)
		case SubPAck:
			a, err := DecodePAck(b)
			if err != nil {
				t.Fatal(err)
			}
			packs = append(packs, a)
		default:
			t.Fatalf("sub %d", b[0])
		}
	}
	return ps, packs
}

// TestRemoteClientPState: a PSTATE goes out at once, then only after
// PStateEvery or on a change; it carries the host epoch it was read
// against.
func TestRemoteClientPState(t *testing.T) {
	m, scope, r, g := remoteClientGame(t, false, 0)
	now := time.Unix(100, 0)
	out, err := r.Tick(now, scope, g)
	ps, _ := splitOut(t, out)
	if err != nil || len(ps) != 1 || ps[0].Epoch != 7 || ps[0].Stage != "M_Dai" || ps[0].Room != 1 {
		t.Fatalf("first: %+v %v", ps, err)
	}
	if out, _ = r.Tick(now.Add(10*time.Millisecond), scope, g); len(out) != 0 {
		t.Errorf("unchanged within PStateEvery: %d payloads", len(out))
	}
	store(t, m, fakegame.PlayerActor+addr.PlayerOffCurProc, binary.BigEndian.AppendUint32(nil, addr.PlayerProcCrouchDefense))
	out, _ = r.Tick(now.Add(20*time.Millisecond), scope, g)
	if ps, _ = splitOut(t, out); len(ps) != 1 || ps[0].Flags != PSGuard|PSCrouchDef {
		t.Errorf("on change: %+v", ps)
	}
	if out, _ = r.Tick(now.Add(20*time.Millisecond+PStateEvery), scope, g); len(out) != 1 {
		t.Errorf("after PStateEvery: %d payloads", len(out))
	}
	// The block is never written while the config is off.
	if blk, _, _, _ := mailbox.ReadRemoteBlock(m); blk.Magic != 0 || r.Shutdown() != nil {
		t.Errorf("block off: %+v", blk)
	}
}

// TestRemoteClientGates: each Go gate answers a PACK dropped at once with
// its detail, and nothing is written into the apply record.
func TestRemoteClientGates(t *testing.T) {
	for _, c := range []struct {
		name   string
		on     bool
		flags  uint16
		edit   func(*PHit, *Scope, *ClientGate)
		detail uint8
	}{
		{"block off", false, mailbox.RemoteFlagApply, nil, PADGoGate},
		{"apply off", true, mailbox.RemoteFlagBodies, nil, PADGoGate},
		{"no REL", true, mailbox.RemoteFlagApply, func(_ *PHit, _ *Scope, g *ClientGate) { g.RELOK = false }, PADGoGate},
		{"not in play", true, mailbox.RemoteFlagApply, func(_ *PHit, s *Scope, _ *ClientGate) { s.InPlay = false }, PADNotInPlay},
		{"old epoch", true, mailbox.RemoteFlagApply, func(p *PHit, _ *Scope, _ *ClientGate) { p.Epoch = 6 }, PADStaleEpoch},
		{"no host", true, mailbox.RemoteFlagApply, func(_ *PHit, _ *Scope, g *ClientGate) { g.HaveHost = false }, PADStaleEpoch},
		{"other room", true, mailbox.RemoteFlagApply, func(p *PHit, _ *Scope, _ *ClientGate) { p.Room = 2 }, PADGoGate},
		{"other stage", true, mailbox.RemoteFlagApply, func(p *PHit, _ *Scope, _ *ClientGate) { p.Stage = "sea" }, PADGoGate},
	} {
		m, scope, r, g := remoteClientGame(t, c.on, c.flags)
		p := phitFor(5)
		if c.edit != nil {
			c.edit(p, &scope, &g)
		}
		r.OnPHit(p)
		out, err := r.Tick(time.Unix(100, 0), scope, g)
		_, packs := splitOut(t, out)
		if err != nil || len(packs) != 1 || packs[0].Result != PAckDropped || packs[0].Detail != c.detail || packs[0].Seq != 5 || packs[0].ClientFrame != g.Frame {
			t.Errorf("%s: %+v %v", c.name, packs, err)
		}
		if blk, _, _, _ := mailbox.ReadRemoteBlock(m); blk.Apply.Seq != 0 {
			t.Errorf("%s: apply record written %+v", c.name, blk.Apply)
		}
	}
}

// TestRemoteClientApply: an accepted PHIT is written as the apply record
// (role client, a fresh seq, At SPrm 0x05, the PHIT's flags) and kept in
// every keepalive; once the REL acks it with a result the client waits
// applySettleFrames and sends the PACK with the result, detail and life
// delta; the next queued PHIT then goes in; one the REL never takes is
// dropped aborted.
func TestRemoteClientApply(t *testing.T) {
	m, scope, r, g := remoteClientGame(t, true, mailbox.RemoteFlagApply)
	now := time.Unix(100, 0)
	// The PHIT names handle 1, which the mirror has in block slot 2 (an
	// unkeyed copy): the record carries OwnerSlotBase|2, never the handle,
	// and slot 2 stays locked while the REL has not acked it.
	r.Owner = func(h uint16) uint16 {
		if h == 1 {
			return OwnerSlotBase | 2
		}
		return NoMirrorSetID
	}
	r.OnPHit(phitFor(5))
	r.OnPHit(phitFor(6))
	out, err := r.Tick(now, scope, g)
	if _, packs := splitOut(t, out); err != nil || len(packs) != 0 {
		t.Fatalf("first tick: %+v %v", packs, err)
	}
	if l := r.LockedSlots(); l != [MaxSlots]bool{2: true} {
		t.Errorf("locked %v while the apply naming slot 2 waits", l)
	}
	blk := readBlock(t, m)
	seq := uint32(3) << 16
	wantAp := mailbox.RemoteApply{Seq: seq + 1, SetID: OwnerSlotBase | 2, Atp: 2, Se: 3, HitMark: 0x0D, Spl: 6, AtSPrm: mailbox.RemoteApplyAtSPrm,
		Flags: mailbox.RemoteAFHostShield, AtType: 0x800, RelHit: [3]float32{1, 2, 3}, RVec: [3]float32{0, 0, 9}}
	if blk.Role != mailbox.RemoteRoleClient || blk.Epoch != 3 || blk.Flags != mailbox.RemoteFlagApply || blk.Apply != wantAp || blk.Policy != nil || blk.FFDeny != nil {
		t.Fatalf("block %+v\nwant apply %+v", blk, wantAp)
	}
	// Acked without a result yet: nothing.
	st := mailbox.RemoteStatus{ApplyAck: seq + 1}
	storeStatus(t, m, &st)
	if out, _ = r.Tick(now, scope, g); len(out) != 0 {
		t.Errorf("no result yet: %d payloads", len(out))
	}
	// The result: APPLIED, life 12 -> 8. It settles over two REL frames.
	st.ApplyResult = mailbox.RemoteApplyResult{Result: PAckApplied, Frames: 1, Sets: 1}
	st.LifeBefore, st.LifeAfter = 12, 8
	storeStatus(t, m, &st)
	if out, _ = r.Tick(now, scope, g); len(out) != 0 {
		t.Errorf("before settling: %d payloads", len(out))
	}
	hostRELFrame(t, m, 102)
	out, _ = r.Tick(now, scope, g)
	_, packs := splitOut(t, out)
	if len(packs) != 1 || *packs[0] != (PAck{Result: PAckApplied, Seq: 5, LifeBefore: 12, LifeDelta: -4, ClientFrame: 1234}) {
		t.Fatalf("PACK %+v", packs)
	}
	if blk = readBlock(t, m); blk.Apply.Seq != seq+2 {
		t.Errorf("next request seq %d", blk.Apply.Seq)
	}
	// The REL never takes the second: aborted after hitAckFrames.
	hostRELFrame(t, m, 102+hitAckFrames)
	out, _ = r.Tick(now, scope, g)
	if _, packs = splitOut(t, out); len(packs) != 1 || packs[0].Result != PAckDropped || packs[0].Detail != PADAborted || packs[0].Seq != 6 {
		t.Errorf("abort PACK %+v", packs)
	}
	// With the queue empty a keepalive carries the last record, never a
	// new seq.
	r.Tick(now, scope, g)
	if blk = readBlock(t, m); blk.Apply.Seq != seq+2 {
		t.Errorf("keepalive apply seq %d", blk.Apply.Seq)
	}
	if r.Stats.Applied != 1 {
		t.Errorf("stats %+v", r.Stats)
	}
	// More than applyQueueDepth at once: the overflow is dropped aborted.
	for i := range applyQueueDepth + 1 {
		r.OnPHit(phitFor(uint16(10 + i)))
	}
	out, _ = r.Tick(now, scope, g)
	if _, packs = splitOut(t, out); len(packs) != 1 || packs[0].Seq != uint16(10+applyQueueDepth) || packs[0].Detail != PADAborted {
		t.Errorf("overflow PACKs %+v", packs)
	}
	// Losing the REL drops what is queued.
	g.RELOK = false
	out, _ = r.Tick(now, scope, g)
	if _, packs = splitOut(t, out); len(packs) != applyQueueDepth {
		t.Errorf("REL gone: %d PACKs", len(packs))
	}
	if l := r.LockedSlots(); l != [MaxSlots]bool{} {
		t.Errorf("locked %v with nothing queued", l)
	}
	if err := r.Shutdown(); err != nil {
		t.Fatal(err)
	}
	if blk, _, _, _ := mailbox.ReadRemoteBlock(m); blk.Magic != 0 {
		t.Error("Shutdown left the magic")
	}
}

// TestMirrorRoutesPHit: the mirror hands a PHIT to its RemoteClient and
// drops one when it has none; RemoteGate carries the latest STATE's epoch.
func TestMirrorRoutesPHit(t *testing.T) {
	m := mem.NewFakeMem()
	c := NewMirror(m, log.Discard{}, nil, false, time.Unix(1, 0))
	b, _ := EncodePHit(phitFor(1))
	c.OnEnemy(b, time.Unix(2, 0)) // no Remote: ignored
	c.Remote = NewRemoteClient(m, log.Discard{}, nil, DefaultRemoteConfig(), time.Unix(1, 0))
	c.OnEnemy(b, time.Unix(2, 0))
	if len(c.Remote.inbox) != 1 || c.Remote.inbox[0].Seq != 1 {
		t.Errorf("inbox %+v", c.Remote.inbox)
	}
	if g := c.RemoteGate(); g.HaveHost || g.RELOK {
		t.Errorf("gate before a STATE %+v", g)
	}
	pushState(t, c, hostState(1, 0), time.Unix(2, 0))
	if g := c.RemoteGate(); !g.HaveHost || g.HostEpoch != 7 || g.Epoch != c.Epoch() {
		t.Errorf("gate %+v", g)
	}
}
