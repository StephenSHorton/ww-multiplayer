package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/internal/inject"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/fakegame"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mailbox"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mem"
)

// The fake keeper and body actors (free MEM1 the fake game does not use).
const (
	fakeKeeper = uint32(0x80C80000)
	fakeBody   = uint32(0x80C90000)
	fakeOther  = uint32(0x80001234)
	fakeHook   = uint32(0x80500000)
)

// TestRemoteFlagsParse: hex with and without 0x, names joined by , | +,
// unknown names refused; the text spells the inverted gates.
func TestRemoteFlagsParse(t *testing.T) {
	for in, want := range map[string]uint16{
		"0x3": 0x3, "13": 0x13, "0": 0, "bodies,swap": 0x3, "hittable|detect+iframe_stamp": 0x200C,
		"FF_GATE_OFF,rouya_gate_off": 0x300, "0X2000": 0x2000,
	} {
		if got, err := parseRemoteFlags(in); err != nil || got != want {
			t.Errorf("parseRemoteFlags(%q) = 0x%X, %v; want 0x%X", in, got, err, want)
		}
	}
	for _, in := range []string{"bodies,nope", "0x10000", ""} {
		if f, err := parseRemoteFlags(in); err == nil && in != "" {
			t.Errorf("parseRemoteFlags(%q) = 0x%X, want an error", in, f)
		}
	}
	if got := remoteFlagsText(0x0003); got != "0x0003 bodies|swap (ff gate on, rouya gate on)" {
		t.Errorf("text: %q", got)
	}
	if got := remoteFlagsText(0x8200); got != "0x8200 rouya_gate_off|unknown 0x8000 (ff gate on, rouya gate OFF)" {
		t.Errorf("text: %q", got)
	}
	if got := remoteFlagsText(0); got != "0x0000 none (ff gate on, rouya gate on)" {
		t.Errorf("text: %q", got)
	}
}

// remoteRoom is moblinRoom with the contract booted, the REL linked, the
// hook words set (the keeper a 0xB5 actor), mPlayerInfo[0] == Link, and
// one LIVE body in slot 0 ('WWRB', own vt).
func remoteRoom(t *testing.T) (*mem.FakeMem, *fakegame.Enemies) {
	t.Helper()
	_, m, es := moblinRoom(t)
	if err := fakegame.Boot(m); err != nil {
		t.Fatal(err)
	}
	if err := mailbox.WriteREL(m, mailbox.REL{Linked: true, BuildID: inject.RELBuildID, Frames: 1000}); err != nil {
		t.Fatal(err)
	}
	store(t, m, mailbox.Mailbox+mailbox.OffRELExecHook, append(u32b(fakeHook), u32b(fakeKeeper+mailbox.RemoteKeeperSwapOff)...))
	store(t, m, fakeKeeper+addr.ActorOffProcName, u16b(inject.RELProcID))
	store(t, m, addr.PlayerInfo0Ptr, u32b(fakegame.PlayerActor))
	store(t, m, fakeBody+addr.ActorOffProcName, u16b(inject.RELProcID))
	store(t, m, fakeBody+mailbox.RemoteBodyMagicOff, append(u32b(mailbox.RemoteBodyMagic), 0, 1, 5, mailbox.RemoteBodyLive))
	store(t, m, fakeBody+mailbox.RemoteBodyVPtrOff, u32b(fakeBody+mailbox.RemoteBodyVTOff))
	store(t, m, fakeBody+mailbox.RemoteBodyVTOff+0x1C, u32b(0x80510000))
	store(t, m, mailbox.Mailbox+mailbox.OffPoseBufStates, []byte{1, 0})
	store(t, m, mailbox.Mailbox+mailbox.OffPoseSeqs, []byte{7, 0})
	return m, es
}

// onNthREL is a reader that runs f just before the nth read of the REL
// words (the first read of each snapshot), so a change lands whole
// between two snapshots.
type onNthREL struct {
	*mem.FakeMem
	seen, n int
	f       func()
}

func (r *onNthREL) Read(a uint32, n int) ([]byte, error) {
	if a == mailbox.Mailbox+mailbox.OffRELMagic {
		if r.seen++; r.seen == r.n {
			r.f()
		}
	}
	return r.FakeMem.Read(a, n)
}

// remoteStatus is a REL span with one LIVE body in slot 0.
func remoteStatus(hookCalls uint32) mailbox.RemoteStatus {
	st := mailbox.RemoteStatus{AckSeq: 9, AckFrame: 995}
	st.Bodies[0] = mailbox.RemoteBodyStatus{Actor: fakeBody, PID: 4242, State: mailbox.RemoteBodyLive, FramesLive: 60, Tries: 1}
	st.Counters[mailbox.RemoteCtrHookCalls] = hookCalls
	st.Counters[mailbox.RemoteCtrRouyaSkips] = 3
	return st
}

func storeStatus(t *testing.T, m *mem.FakeMem, st *mailbox.RemoteStatus) {
	t.Helper()
	store(t, m, mailbox.RemoteBlockAddr+mailbox.RemoteAckSeqOff, st.Encode())
}

// TestRemoteWatch: the watch prints the hook words with the keeper
// check, the block valid with its flags and gates, the jail byte, the
// body read back, the provisional latch, a hit pushed mid-run at once,
// hook_calls per REL frame, and each Moblin's m714 resolved to host,
// body0 and other, with a change printed at once; the CSV gets a header
// and a row per tick.
func TestRemoteWatch(t *testing.T) {
	m, es := remoteRoom(t)
	blk := mailbox.RemoteBlock{Magic: mailbox.RemoteMagic, Seq: 9, Layout: mailbox.RemoteLayout, Flags: 0x3, Role: mailbox.RemoteRoleHost,
		MarginQ8: mailbox.RemoteDefMarginQ8, DYMax: mailbox.RemoteDefDYMax, Policy: []mailbox.RemotePolicy{{Proc: int16(mailbox.RemoteProcMo2), Flags: mailbox.RemotePFAllow}}}
	blk.Slots[0] = mailbox.RemoteSlot{Active: true, SFlags: mailbox.RemoteSFTargetable, Stage: "M_Dai", Room: 1, Pos: [3]float32{10, 20, 30}, EyeDY: 110, CylR: 30, CylH: 120, Gen: 1}
	if err := mailbox.WriteRemoteBlock(m, &blk); err != nil {
		t.Fatal(err)
	}
	st := remoteStatus(100)
	storeStatus(t, m, &st)
	// The swap state: valid host, SWAP, body 0 a candidate; latch entry 2
	// has Moblin setID 1 on the body.
	mo0, _ := es.Of(1)
	mo1, _ := es.Of(2)
	mo2, _ := es.Of(0)
	sw := make([]byte, swSize)
	binary.BigEndian.PutUint32(sw[swLastSeq:], 9)
	binary.BigEndian.PutUint16(sw[swFlags:], 0x3)
	sw[swValid], sw[swRole], sw[swSwapReady], sw[swLiveMask] = 1, 1, 1, 1
	sw[swBodies+swBodyGen] = 1
	binary.BigEndian.PutUint16(sw[swBodies+swBodySize+swBodyCool:], 30)
	l := sw[swLatch+2*latchEntry:]
	binary.BigEndian.PutUint32(l[latchEnemyPID:], mo1.PID)
	binary.BigEndian.PutUint32(l[latchTarget:], 4242)
	l[latchHold], l[latchIsMo2] = 31, 1
	store(t, m, fakeKeeper+mailbox.RemoteKeeperSwapOff, sw)
	store(t, m, mo0.Addr+mo2OffM714, u32b(fakegame.PlayerActor))
	store(t, m, mo1.Addr+mo2OffM714, u32b(fakeBody))
	store(t, m, mo2.Addr+mo2OffM714, u32b(fakeOther))

	defer func(e time.Duration) { remoteWatchEvery = e }(remoteWatchEvery)
	remoteWatchEvery = 40 * time.Millisecond
	// Mid-run (the 5th snapshot): 3 REL frames, 300 hook calls, one hit,
	// and setID 2's m714 back on the host, all between two snapshots.
	r := &onNthREL{FakeMem: m, n: 5, f: func() {
		st := remoteStatus(400)
		st.HitRingHead = 1
		st.Hits[0] = mailbox.RemoteHit{Seq: 1, Slot: 0, HFlags: mailbox.RemoteHFShield, PID: mo1.PID, SetID: 1, Proc: int16(mailbox.RemoteProcMo2), AtType: 0x800, Atp: 2, Se: 3, Spl: 6, HitMark: 0x0D, Frame: 1003}
		m.Store(mailbox.RemoteBlockAddr+mailbox.RemoteAckSeqOff, st.Encode())
		m.Store(mailbox.Mailbox+mailbox.OffRELFrame, u32b(1003))
		m.Store(mo2.Addr+mo2OffM714, u32b(fakegame.PlayerActor))
	}}
	var out, csv bytes.Buffer
	if err := remoteWatch(context.Background(), &out, &csv, r, 180*time.Millisecond, true); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	s := out.String()
	for _, want := range []string{
		"remote-Link block at 0x80413420 (0x330 bytes), hook words at 0x804130BC/0x804130C0",
		"m714 at mo2+0xD28",
		"policy (1): 0x00BD ALLOW",
		"exec_hook 0x80500000 swap_state 0x80C80458 (keeper 0x80C80000 proc 0x00B5 ok)",
		"block valid: seq 9 ack_seq 9 ack_frame 995 flags 0x0003 bodies|swap (ff gate on, rouya gate on) role 1",
		"collect 0x00 & 0x0F = 0x0 (no pearls: the rouya gate skips every mo2 swap)", "create_cooldown 0; full_allow 0x00 heap_reason none flags2 0x0000 (Co off)",
		"full: full_allow 0x00 heap_reason none flags2 0x0000 (Co off)", "profile: DMC[0x27] 0x00000000 DMC[0xB5] 0x00000000 empty (not booted?); no profile list",
		"shanm: not allocated (no FULL create this boot)", "body: lite LIVE err none", "read: proc 0x00B5 prof 0x0000 magic 'WWRB'",
		"mPlayerInfo[0] 0x", " equal;",
		"hook_calls 400(+300)", "hook_calls/frame 100.0 over 3 REL frames", "rouya_skips 3(+0)",
		"slot 0: active true sflags 0x01 stage \"M_Dai\" room 1 pos 10.0 20.0 30.0", "pose_buf_state 1 (ready) pose_seq 7",
		"body: lite LIVE err none actor 0x80C90000 pid 4242", "magic 'WWRB' slot 0 gen 1 state LIVE iframes_left 5 vptr 0x80C90320 own vt vt+0x1C 0x80510000",
		"swap: valid 1 role 1 flags 0x0003 swap_ready 1 live_mask 01 last_seq 9 stale_frames 0 full_allow 00 flags2 0x0000 full_denied 00; body0 parked 0 gen 1 cooldown 0 del_sent 0 kind lite full_wait 0; body1 parked 0 gen 0 cooldown 30 del_sent 0 kind lite full_wait 0",
		"latch: [2] enemy pid ", " -> body0 (pid 4242) hold 31 force pid 0/0 mo2 1", "+0x000: 00000009 00000000",
		"HIT seq 1 slot 0 hflags shield attacker pid", "proc 0x00BD attype 0x00000800 atp 2 se 3 hitmark 13 spl 6", "frame 1003",
		"-> host", "-> body0", "-> other 0x80001234",
		"m714 setID 2 pid", ": other 0x80001234 -> host",
		"apply: seq 0", "result NONE detail none",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("output lacks %q:\n%s", want, s)
		}
	}
	if n := strings.Count(s, "HIT seq 1"); n != 1 {
		t.Errorf("the hit printed %d times", n)
	}
	lines := strings.Split(strings.TrimSpace(csv.String()), "\n")
	if len(lines) < 5 || lines[0] != remoteCSVHeader {
		t.Fatalf("csv: %d lines, header %q", len(lines), lines[0])
	}
	want := len(strings.Split(remoteCSVHeader, ","))
	for _, l := range lines[1:] {
		if n := len(strings.Split(l, ",")); n != want {
			t.Fatalf("csv row has %d columns, want %d: %s", n, want, l)
		}
	}
	if !strings.Contains(csv.String(), ",0:host;1:body0;2:other 0x80001234;3:null;65535:null\n") || !strings.HasSuffix(lines[len(lines)-1], ",0:host;1:body0;2:host;3:null;65535:null") {
		t.Errorf("csv m714 column:\n%s", csv.String())
	}
}

// TestRemoteSnapValidity: magic 0 says off, a torn seq and a stale ack
// are named, the swap state of a non-0xB5 keeper is flagged, and the
// gates read inverted.
func TestRemoteSnapValidity(t *testing.T) {
	m, _ := remoteRoom(t)
	s, err := readRemoteSnap(m)
	if err != nil {
		t.Fatal(err)
	}
	if v := s.valid(); v != "magic 0 (off: vanilla-equivalent)" {
		t.Errorf("magic 0: %q", v)
	}
	blk := mailbox.RemoteBlock{Magic: mailbox.RemoteMagic, Seq: 5, Layout: mailbox.RemoteLayout}
	if err := mailbox.WriteRemoteBlock(m, &blk); err != nil {
		t.Fatal(err)
	}
	store(t, m, mailbox.RemoteBlockAddr+mailbox.RemoteSeqTailOff, u32b(4))
	store(t, m, mailbox.RemoteBlockAddr+mailbox.RemoteFullAllowOff, []byte{3, 0x20, 0, 2})
	st := mailbox.RemoteStatus{AckFrame: 800}
	storeStatus(t, m, &st)
	store(t, m, fakeKeeper+addr.ActorOffProcName, u16b(0x00BD))
	if s, err = readRemoteSnap(m); err != nil {
		t.Fatal(err)
	}
	if v := s.valid(); v != "TORN head 5 tail 4, never acked (the REL has not consumed a write), full_allow 0x03 without full_bodies, heap_reason 32 unknown, flags2 0x0002 has unknown bits, STALE: last ack 200 REL frames ago" {
		t.Errorf("torn/stale: %q", v)
	}
	if s.KeeperOK {
		t.Error("a 0xBD keeper passed")
	}
	if got := s.resolveTarget(0); got != "null" {
		t.Errorf("null: %q", got)
	}

	// Entries already in the ring are printed once at start, and not
	// again as new hits.
	st = mailbox.RemoteStatus{AckSeq: 5, AckFrame: 999, HitRingHead: 1}
	st.Hits[0] = mailbox.RemoteHit{Seq: 1, PID: 77, AtType: 0x800}
	storeStatus(t, m, &st)
	var out bytes.Buffer
	if err := remoteWatch(context.Background(), &out, nil, m, 20*time.Millisecond, false); err != nil {
		t.Fatal(err)
	}
	if o := out.String(); !strings.Contains(o, "hit ring at start (head 1): seq 1 slot 0 hflags - attacker pid 77") || strings.Contains(o, "HIT seq") {
		t.Errorf("ring at start:\n%s", o)
	}
}

// TestRemoteFlags: without a block it warns, writes exactly the flags
// word (2 bytes at +0x00A) and says nothing holds the block.
func TestRemoteFlags(t *testing.T) {
	m, _ := remoteRoom(t)
	defer func(d time.Duration) { remoteFlagsSettle = d }(remoteFlagsSettle)
	remoteFlagsSettle = 0
	m.Record(true)
	var out bytes.Buffer
	if err := remoteFlags(&out, m, 0x0007); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	ws := m.Writes()
	if len(ws) != 1 || ws[0].Addr != mailbox.RemoteBlockAddr+mailbox.RemoteFlagsOff || !bytes.Equal(ws[0].Data, []byte{0, 7}) {
		t.Fatalf("writes: %+v", ws)
	}
	s := out.String()
	for _, want := range []string{"warning: the block's magic is 0x00000000", "flags were 0x0000 none", "flags now 0x0007 bodies|swap|hittable", "no writer bumped the seq"} {
		if !strings.Contains(s, want) {
			t.Errorf("output lacks %q:\n%s", want, s)
		}
	}
}

// TestRemoteSlot: the holder seeds the block (role host, the defaults,
// ALLOW mo2), writes slot 1 at Link + offset with seq + 1 per write and
// head == tail, carries a flags word written under it (remote-flags), and
// writes magic 0 on stop; nothing else is written. It refuses while
// another writer bumps the seq and without the REL linked.
func TestRemoteSlot(t *testing.T) {
	m, _ := remoteRoom(t)
	pos := make([]byte, 12)
	for i, v := range []float32{100, 200, 300} {
		binary.BigEndian.PutUint32(pos[4*i:], math.Float32bits(v))
	}
	store(t, m, fakegame.PlayerActor+addr.ActorOffCurPos, pos)
	store(t, m, fakegame.PlayerActor+addr.ActorOffShapeAngle+2, u16b(0x1234))
	defer func(a, b, c time.Duration) { remoteSlotProbe, remoteSlotEvery, remoteSlotAfter = a, b, c }(remoteSlotProbe, remoteSlotEvery, remoteSlotAfter)
	remoteSlotProbe, remoteSlotEvery, remoteSlotAfter = 30*time.Millisecond, 40*time.Millisecond, 0
	o, secs, err := parseRemoteSlotArgs([]string{"1", "5"}, map[string]string{"flags": "bodies", "offset": "0,0,-50", "gen": "3"})
	if err != nil || secs != 5 || o.Slot != 1 || o.PoseSlot != 1 || o.Gen != 3 || o.Flags == nil || *o.Flags != mailbox.RemoteFlagBodies || o.Offset != [3]float32{0, 0, -50} {
		t.Fatalf("args: %+v secs %d %v", o, secs, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m.Record(true)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		time.Sleep(60 * time.Millisecond)
		// remote-flags under the holder.
		m.Store(mailbox.RemoteBlockAddr+mailbox.RemoteFlagsOff, u16b(0x000F))
	}()
	var out bytes.Buffer
	if err := remoteSlot(ctx, &out, m, o, 150*time.Millisecond); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	wg.Wait()
	var blocks []mailbox.RemoteBlock
	lastMagic := uint32(1)
	for _, w := range m.Writes() {
		switch {
		case w.Addr == mailbox.RemoteBlockAddr && len(w.Data) == mailbox.RemoteGoSpan:
			b, tail, err := mailbox.DecodeRemoteBlock(w.Data)
			if err != nil || tail != b.Seq {
				t.Fatalf("block %+v tail %d %v", b, tail, err)
			}
			blocks = append(blocks, b)
			lastMagic = b.Magic
		case w.Addr == mailbox.RemoteBlockAddr && len(w.Data) == 4:
			lastMagic = binary.BigEndian.Uint32(w.Data)
		default:
			t.Errorf("stray write 0x%08X+%d", w.Addr, len(w.Data))
		}
	}
	if len(blocks) < 4 || lastMagic != 0 {
		t.Fatalf("%d blocks, last magic 0x%08X\n%s", len(blocks), lastMagic, out.String())
	}
	for i, b := range blocks {
		sl := b.Slots[1]
		if b.Magic != mailbox.RemoteMagic || b.Layout != mailbox.RemoteLayout || b.Role != mailbox.RemoteRoleHost || b.Seq != uint32(i+1) ||
			b.MarginQ8 != mailbox.RemoteDefMarginQ8 || b.MinHold != mailbox.RemoteDefMinHold || b.BodyIFrames != mailbox.RemoteDefBodyIFrames ||
			b.DYMax != mailbox.RemoteDefDYMax || b.CreateCooldown != mailbox.RemoteDefCreateCooldown ||
			len(b.Policy) != 1 || b.Policy[0] != (mailbox.RemotePolicy{Proc: int16(mailbox.RemoteProcMo2), Flags: mailbox.RemotePFAllow}) ||
			b.Slots[0].Active || !sl.Active || sl.SFlags != mailbox.RemoteSFTargetable || sl.Stage != "M_Dai" || sl.Room != 1 ||
			sl.Pos != [3]float32{100, 200, 250} || sl.AngleY != 0x1234 || sl.Gen != 3 || sl.PoseSlot != 1 ||
			sl.EyeDY != mailbox.RemoteDefEyeDY || sl.CylR != mailbox.RemoteDefCylR || sl.CylH != mailbox.RemoteDefCylH {
			t.Fatalf("block %d: %+v", i, b)
		}
	}
	if blocks[0].Flags != mailbox.RemoteFlagBodies || blocks[len(blocks)-1].Flags != 0x000F {
		t.Errorf("flags: first 0x%04X last 0x%04X (want the remote-flags write carried)", blocks[0].Flags, blocks[len(blocks)-1].Flags)
	}
	s := out.String()
	for _, want := range []string{"block seeded (defaults, PROC_POLICY ALLOW mo2 0x00BD); role 1; flags 0x0001 bodies",
		"note: pose_buf_states[1] is 0, not 1", "slot 1 \"M_Dai\" room 1 pos 100.0 200.0 250.0", "stopped after", "magic 0 written; body 1 now EMPTY"} {
		if !strings.Contains(s, want) {
			t.Errorf("output lacks %q:\n%s", want, s)
		}
	}

	// Another writer bumping the seq: refused before any write.
	live := blocks[len(blocks)-1]
	if err := mailbox.WriteRemoteBlock(m, &live); err != nil {
		t.Fatal(err)
	}
	m.ResetWrites()
	stop := make(chan struct{})
	go func() {
		for seq := live.Seq + 1; ; seq++ {
			select {
			case <-stop:
				return
			case <-time.After(5 * time.Millisecond):
				live.Seq = seq
				_ = mailbox.WriteRemoteBlock(m, &live)
			}
		}
	}()
	err = remoteSlot(ctx, &out, m, o, 50*time.Millisecond)
	close(stop)
	if err == nil || !strings.Contains(err.Error(), "another writer holds the block") {
		t.Errorf("busy: %v", err)
	}

	// Without the REL linked it refuses.
	if err := mailbox.WriteREL(m, mailbox.REL{}); err != nil {
		t.Fatal(err)
	}
	if err := remoteSlot(ctx, &out, m, o, time.Millisecond); err == nil || !strings.Contains(err.Error(), "REL module is not linked") {
		t.Errorf("unlinked: %v", err)
	}
}

// TestRemoteSlotArgs: bad values are refused.
func TestRemoteSlotArgs(t *testing.T) {
	for _, c := range []struct {
		pos    []string
		values map[string]string
	}{
		{nil, nil},
		{[]string{"4"}, nil},
		{[]string{"0", "x"}, nil},
		{[]string{"0"}, map[string]string{"offset": "1,2"}},
		{[]string{"0"}, map[string]string{"at": "1,nan,3"}},
		{[]string{"0"}, map[string]string{"stage": "TooLongName"}},
		{[]string{"0"}, map[string]string{"role": "both"}},
		{[]string{"0"}, map[string]string{"pose-slot": "2"}},
		{[]string{"0"}, map[string]string{"sflags": "1FF"}},
		{[]string{"0"}, map[string]string{"cyl-r": "-1"}},
		{[]string{"0"}, map[string]string{"flags": "warp"}},
	} {
		if o, _, err := parseRemoteSlotArgs(c.pos, c.values); err == nil {
			t.Errorf("%q %v: accepted %+v", c.pos, c.values, o)
		}
	}
	o, secs, err := parseRemoteSlotArgs([]string{"2"}, map[string]string{"at": "1,2,3", "room": "-1", "stage": "sea", "role": "client", "sflags": "0x05", "iframes": "-3"})
	if err != nil || secs != 60 || o.At == nil || *o.At != [3]float32{1, 2, 3} || o.Room == nil || *o.Room != -1 || o.Stage != "sea" ||
		o.Role != mailbox.RemoteRoleClient || o.SFlags != 5 || o.IFrames != -3 || o.Flags != nil || o.Gen != 1 || o.PoseSlot != 2 {
		t.Errorf("args: %+v secs %d %v", o, secs, err)
	}
}

// TestSwapStatePins: the probe's swap-state offsets are the "// +0xNN"
// field comments and struct sizes of inject/rel/remote.h (remote_swap_state,
// rl_body, rl_latch), so a REL layout change fails here instead of
// printing a wrong decode.
func TestSwapStatePins(t *testing.T) {
	src, err := os.ReadFile(filepath.Join("..", "..", "inject", "rel", "remote.h"))
	if err != nil {
		t.Fatal(err)
	}
	field := regexp.MustCompile(`^\s*[A-Za-z_][\w ]*?\s(\w+)(?:\[[^\]]*\])?;\s*//\s*\+0x([0-9A-Fa-f]+)`)
	end := regexp.MustCompile(`^\}\s*(\w+);\s*//\s*0x([0-9A-Fa-f]+)`)
	offs := map[string]map[string]uint64{}
	sizes := map[string]uint64{}
	cur := map[string]uint64{}
	for _, line := range strings.Split(string(src), "\n") {
		if mm := field.FindStringSubmatch(line); mm != nil {
			v, _ := strconv.ParseUint(mm[2], 16, 32)
			cur[mm[1]] = v
			continue
		}
		if mm := end.FindStringSubmatch(line); mm != nil {
			v, _ := strconv.ParseUint(mm[2], 16, 32)
			offs[mm[1]], sizes[mm[1]] = cur, v
			cur = map[string]uint64{}
		}
	}
	for _, c := range []struct {
		strct, name string
		got         int
	}{
		{"remote_swap_state", "last_seq", swLastSeq},
		{"remote_swap_state", "stale_frames", swStaleFrames},
		{"remote_swap_state", "flags", swFlags},
		{"remote_swap_state", "valid", swValid},
		{"remote_swap_state", "role", swRole},
		{"remote_swap_state", "swap_ready", swSwapReady},
		{"remote_swap_state", "live_mask", swLiveMask},
		{"remote_swap_state", "bodies", swBodies},
		{"remote_swap_state", "latch", swLatch},
		{"remote_swap_state", "full_allow", swFullAllow},
		{"remote_swap_state", "flags2", swFlags2},
		{"remote_swap_state", "full_denied", swFullDenied},
		{"remote_swap_state", "full_wait", swFullWait},
		{"rl_body", "kind", swBodyKind},
		{"rl_body", "gen", swBodyGen},
		{"rl_body", "cooldown", swBodyCool},
		{"rl_body", "del_sent", swBodyDelSent},
		{"rl_body", "parked", swBodyParked},
		{"rl_latch", "enemy_pid", latchEnemyPID},
		{"rl_latch", "target_pid", latchTarget},
		{"rl_latch", "force_pid", latchForcePID},
		{"rl_latch", "hold", latchHold},
		{"rl_latch", "force_frames", latchForceFr},
		{"rl_latch", "is_mo2", latchIsMo2},
	} {
		want, ok := offs[c.strct][c.name]
		if !ok {
			t.Errorf("remote.h: no %s.%s", c.strct, c.name)
			continue
		}
		if uint64(c.got) != want {
			t.Errorf("%s.%s: probe +0x%X, remote.h +0x%X", c.strct, c.name, c.got, want)
		}
	}
	for name, got := range map[string]int{"remote_swap_state": swSize, "rl_body": swBodySize, "rl_latch": latchEntry} {
		if sizes[name] != uint64(got) {
			t.Errorf("sizeof %s: probe 0x%X, remote.h 0x%X", name, got, sizes[name])
		}
	}
	if !strings.Contains(string(src), "#define RL_LATCH_MAX 16") {
		t.Errorf("RL_LATCH_MAX is not %d", latchCount)
	}
	if swSize > swapStateLen {
		t.Errorf("swap state 0x%X > the keeper's 0x%X", swSize, swapStateLen)
	}
}
