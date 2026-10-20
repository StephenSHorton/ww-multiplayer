package enemysync

import (
	"encoding/binary"
	"strings"
	"testing"
	"time"

	posefeed "github.com/StephenSHorton/ww-multiplayer/mods/multiplayer/feed"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/heap"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/heap/heaptest"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/log"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mailbox"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mem"
)

// The fake heaps, well away from fakegame's actors.
const (
	gateGameHeap    uint32 = 0x82000000
	gateZeldaHeap   uint32 = 0x82400000
	gateArchiveHeap uint32 = 0x82600000
	gateShanm       uint32 = 0x82800000
)

// gateRig is a host with step6+full_bodies and the heap gate on, over
// heaps that pass every rule, one remote in slot 0 (no body yet).
type gateRig struct {
	t     *testing.T
	m     *mem.FakeMem
	scope Scope
	r     *RemoteHost
	now   time.Time
	views []posefeed.RemoteView
	st    mailbox.RemoteStatus
	// stages is each remote's PSTATE stage by player id (absent: the
	// host's); "-" sends none (the slot's PSTATE goes stale).
	stages    map[uint8]string
	lastStage map[uint8]string // each view's stage at the last tick
	flags     map[uint8]uint8  // each remote's PSTATE flags by player id
	noMove    bool             // a stage change does not move the views
}

func newGateRig(t *testing.T, mut func(c *RemoteConfig)) *gateRig {
	t.Helper()
	m, scope := pstateGame(t)
	hostRELFrame(t, m, 100)
	cfg := DefaultRemoteConfig()
	cfg.On, cfg.Flags, cfg.HeapGate = true, remoteSteps[1]|remoteSteps[6]|mailbox.RemoteFlagFullBodies, true
	if mut != nil {
		mut(&cfg)
	}
	g := &gateRig{t: t, m: m, scope: scope, r: NewRemoteHost(m, log.Discard{}, cfg, 9), now: time.Unix(100, 0),
		views: []posefeed.RemoteView{{ID: 4, LinkSlot: 0, Live: true}}, stages: map[uint8]string{}, lastStage: map[uint8]string{}, flags: map[uint8]uint8{}}
	g.heaps(0x100000, 0x20000, 0x30000)
	return g
}

// heaps lays out the GameHeap (a block of gameLargest and one of 0x40000),
// the ZeldaHeap and the ArchiveHeap (one block each).
func (g *gateRig) heaps(gameLargest, zeldaLargest, archiveLargest uint32) {
	heaptest.FakeAt(g.t, g.m, addr.GameHeapPtr, gateGameHeap, gateGameHeap+0x100, gateGameHeap+0x300100,
		[][2]uint32{{0, gameLargest}, {0x200000, 0x40000}})
	heaptest.FakeAt(g.t, g.m, addr.ZeldaHeapPtr, gateZeldaHeap, gateZeldaHeap+0x100, gateZeldaHeap+0x100100, [][2]uint32{{0, zeldaLargest}})
	heaptest.FakeAt(g.t, g.m, addr.ArchiveHeapPtr, gateArchiveHeap, gateArchiveHeap+0x100, gateArchiveHeap+0x100100, [][2]uint32{{0, archiveLargest}})
}

func (g *gateRig) counter(a, v uint32) { store(g.t, g.m, a, binary.BigEndian.AppendUint32(nil, v)) }

// body sets body i's state and kind in the REL's status.
func (g *gateRig) body(i int, state, kind uint8) {
	g.st.Bodies[i].State, g.st.Bodies[i].Kind = state, kind
	storeStatus(g.t, g.m, &g.st)
}

// tick advances by d, runs the host and returns the block it wrote.
func (g *gateRig) tick(d time.Duration) mailbox.RemoteBlock {
	g.t.Helper()
	g.now = g.now.Add(d)
	for k := range g.views {
		v := &g.views[k]
		stage, ok := g.stages[v.ID]
		if !ok {
			stage = g.scope.Stage
		}
		if was, ok := g.lastStage[v.ID]; ok && was != stage && stage != "-" && !g.noMove {
			// A new stage spawns the remote somewhere new (players.go
			// arrival): its position moves with the stage.
			v.Pos[0] += 1000
			v.Target[0] += 1000
		}
		g.lastStage[v.ID] = stage
		if stage != "-" {
			g.r.OnPState(&PState{Client: 2, PlayerID: v.ID, Stage: stage, Room: g.scope.Room, Flags: g.flags[v.ID]}, g.now)
		}
	}
	if _, err := g.r.Tick(g.now, g.scope, true, g.views, g.now); err != nil {
		g.t.Fatal(err)
	}
	return readBlock(g.t, g.m)
}

func (g *gateRig) want(step string, b mailbox.RemoteBlock, allow, reason uint8) {
	g.t.Helper()
	if b.FullAllow != allow || b.HeapReason != reason {
		g.t.Errorf("%s: allow 0x%02X reason %s, want 0x%02X %s (%s)", step, b.FullAllow, hgName(b.HeapReason), allow, hgName(reason), g.r.gate.line())
	}
}

// settle runs the new stage's two samples and expects the gate open for
// slot 0.
func (g *gateRig) settle(step string) {
	g.t.Helper()
	g.settleTo(step, mailbox.RemoteHGOpen)
}

// settleTo is settle with the open gate's reason (QUEUED while slot 1
// also waits for a body).
func (g *gateRig) settleTo(step string, reason uint8) {
	g.t.Helper()
	g.want(step+" first sample", g.tick(16*time.Millisecond), 0, mailbox.RemoteHGStageSettle)
	g.want(step+" between samples", g.tick(100*time.Millisecond), 0, mailbox.RemoteHGStageSettle)
	g.want(step+" second sample", g.tick(150*time.Millisecond), 0x01, reason)
}

// TestHeapGateOpen: a new stage settles on two samples, then the lowest
// active slot without a body gets the one bit; slots with a body or no
// remote never do; FLAGS2 rides along; the client's block carries none of
// it.
func TestHeapGateOpen(t *testing.T) {
	g := newGateRig(t, func(c *RemoteConfig) { c.Flags2 = mailbox.RemoteFlag2FullCo })
	g.settle("stage")
	if b := g.tick(16 * time.Millisecond); b.Flags2 != mailbox.RemoteFlag2FullCo || b.FullAllow != 1 {
		t.Errorf("flags2 0x%04X allow 0x%02X", b.Flags2, b.FullAllow)
	}
	// A lite body in slot 0 is never upgraded: slot 1 gets the bit.
	g.views = append(g.views, posefeed.RemoteView{ID: 5, LinkSlot: 1, Live: true})
	g.body(0, mailbox.RemoteBodyLive, mailbox.RemoteKindLite)
	g.tick(16 * time.Millisecond) // the status is read after the write
	g.want("lite in 0", g.tick(16*time.Millisecond), 0x02, mailbox.RemoteHGOpen)
	// No slot wants one: open, no bit.
	g.body(1, mailbox.RemoteBodyParked, mailbox.RemoteKindLite)
	g.tick(16 * time.Millisecond)
	g.want("all bodied", g.tick(16*time.Millisecond), 0, mailbox.RemoteHGOpen)

	// The client never writes the fields.
	m, scope := pstateGame(t)
	hostRELFrame(t, m, 100)
	cfg := g.r.Config()
	c := NewRemoteClient(m, log.Discard{}, nil, cfg, time.Unix(100, 0))
	if _, err := c.Tick(time.Unix(100, 0), scope, ClientGate{RELOK: true}); err != nil {
		t.Fatal(err)
	}
	if b := readBlock(t, m); b.FullAllow != 0 || b.HeapReason != 0 || b.Flags2 != 0 {
		t.Errorf("client block allow %d reason %d flags2 %d", b.FullAllow, b.HeapReason, b.Flags2)
	}
	raw, _ := m.Read(mailbox.RemoteBlockAddr+mailbox.RemoteFullAllowOff, 4)
	if binary.BigEndian.Uint32(raw) != 0 {
		t.Errorf("client +0x1C word % X", raw)
	}
}

// TestHeapGateEligible: the bit goes only to a slot the REL can create
// in (active, its PSTATE fresh and in the host's stage); while another
// such slot waits for a body the open gate says QUEUED (the REL's wait),
// not OPEN (its lite fallback).
func TestHeapGateEligible(t *testing.T) {
	// Slot 0's remote in another stage, slot 1's in the host's: 0x02.
	g := newGateRig(t, nil)
	g.views = append(g.views, posefeed.RemoteView{ID: 5, LinkSlot: 1, Live: true})
	g.stages[4] = "sea"
	g.want("first sample", g.tick(16*time.Millisecond), 0, mailbox.RemoteHGStageSettle)
	g.want("between samples", g.tick(100*time.Millisecond), 0, mailbox.RemoteHGStageSettle)
	g.want("other stage in 0", g.tick(150*time.Millisecond), 0x02, mailbox.RemoteHGOpen) // was 0x01 OPEN: slot 1 fell back to lite
	// No PSTATE for slot 0 (stale, no stage): still 0x02.
	g.stages[4] = "-"
	g.r.mu.Lock()
	delete(g.r.pstates, 4)
	g.r.mu.Unlock()
	g.want("stale 0", g.tick(16*time.Millisecond), 0x02, mailbox.RemoteHGOpen)
	// Neither eligible: open, no bit.
	g.stages[5] = "sea"
	g.want("none eligible", g.tick(16*time.Millisecond), 0, mailbox.RemoteHGOpen)

	// Both in the host's stage: slot 0 gets the bit, slot 1 holds.
	g = newGateRig(t, nil)
	g.views = append(g.views, posefeed.RemoteView{ID: 5, LinkSlot: 1, Live: true})
	g.settleTo("both", mailbox.RemoteHGQueued)
	// Slot 0 tried a FULL body and is EMPTY again (its attempt spent):
	// slot 1 goes first.
	g.st.Bodies[0].Tries = 1
	g.body(0, mailbox.RemoteBodyEmpty, mailbox.RemoteKindFull)
	g.tick(16 * time.Millisecond)
	g.want("0 spent", g.tick(16*time.Millisecond), 0x02, mailbox.RemoteHGQueued)
	// Slot 0's lite body: slot 1 alone, OPEN.
	g.body(0, mailbox.RemoteBodyQueued, mailbox.RemoteKindLite)
	g.tick(16 * time.Millisecond)
	g.want("0 lite", g.tick(16*time.Millisecond), 0x02, mailbox.RemoteHGOpen)
	// Slot 1 out of tries: nothing to give.
	g.st.Bodies[1].Err = mailbox.RemoteBErrTries
	g.body(1, mailbox.RemoteBodyEmpty, mailbox.RemoteKindLite)
	g.tick(16 * time.Millisecond)
	g.want("1 tries", g.tick(16*time.Millisecond), 0, mailbox.RemoteHGOpen)
}

// TestHeapGateOff: without FULL_BODIES, or with the gate off, the three
// fields stay 0 (Batch A's block); no heap is read.
func TestHeapGateOff(t *testing.T) {
	for name, mut := range map[string]func(c *RemoteConfig){
		"no full_bodies": func(c *RemoteConfig) { c.Flags &^= mailbox.RemoteFlagFullBodies },
		"gate off":       func(c *RemoteConfig) { c.HeapGate = false },
	} {
		g := newGateRig(t, mut)
		g.m.Store(addr.GameHeapPtr, make([]byte, 4)) // would be a WALK_PROBLEM if read
		for range 5 {
			if b := g.tick(300 * time.Millisecond); b.FullAllow != 0 || b.HeapReason != mailbox.RemoteHGNone || b.Flags2 != 0 {
				t.Errorf("%s: allow %d reason %d flags2 %d", name, b.FullAllow, b.HeapReason, b.Flags2)
			}
		}
		if g.r.gate.Samples != 0 {
			t.Errorf("%s: %d samples", name, g.r.gate.Samples)
		}
	}
}

// TestHeapGateSizes: each heap-size rule shuts the gate with its reason;
// the archive rule only while the shanm table is not allocated; "off"
// skips the size rules.
func TestHeapGateSizes(t *testing.T) {
	for _, c := range []struct {
		name                 string
		game, zelda, archive uint32
		shanm                bool
		th                   *heap.Thresholds
		want                 uint8
	}{
		{"game largest", heap.GateGameLargest - 0x20, 0x20000, 0x30000, false, nil, mailbox.RemoteHGGameLargest},
		{"game free", heap.GateGameLargest, 0x20000, 0x30000, false, &heap.Thresholds{GameFree: 0x200000}, mailbox.RemoteHGGameFree},
		{"zelda largest", 0x100000, heap.GateZeldaLargest - 0x10, 0x30000, false, nil, mailbox.RemoteHGZeldaLargest},
		{"archive largest", 0x100000, 0x20000, heap.GateArchiveLargest - 0x10, false, nil, mailbox.RemoteHGArchiveLargest},
		{"archive allocated", 0x100000, 0x20000, 0x100, true, nil, mailbox.RemoteHGOpen},
		{"off", 0x100, 0x100, 0x100, false, &heap.Thresholds{}, mailbox.RemoteHGOpen},
	} {
		t.Run(c.name, func(t *testing.T) {
			g := newGateRig(t, func(cfg *RemoteConfig) {
				if c.th != nil {
					cfg.HeapSizes = *c.th
				}
			})
			g.heaps(c.game, c.zelda, c.archive)
			if c.shanm {
				hdr := make([]byte, 12)
				binary.BigEndian.PutUint32(hdr, mailbox.RemoteShanmMagic)
				binary.BigEndian.PutUint32(hdr[4:], mailbox.RemoteShanmBytes)
				binary.BigEndian.PutUint32(hdr[8:], gateShanm)
				store(t, g.m, gateShanm, hdr)
				store(t, g.m, mailbox.Mailbox+mailbox.OffRELShanm, binary.BigEndian.AppendUint32(nil, gateShanm))
			}
			g.tick(16 * time.Millisecond)
			b := g.tick(300 * time.Millisecond)
			allow := uint8(0)
			if c.want == mailbox.RemoteHGOpen {
				allow = 1
			}
			g.want(c.name, b, allow, c.want)
			if c.shanm == g.r.gate.sample.ArchiveWalked {
				t.Errorf("archive walked %v with the table allocated %v", g.r.gate.sample.ArchiveWalked, c.shanm)
			}
		})
	}
}

// TestHeapGateFreshness: out of play, a pending stage change or, while a
// slot waits, a sample older than HeapSampleFresh shut the gate with a
// transient reason.
func TestHeapGateFreshness(t *testing.T) {
	g := newGateRig(t, nil)
	g.settle("stage")
	// No slot wants a body: walks slow down to HeapWalkSlow.
	g.views = nil
	g.want("walked, nobody waits", g.tick(300*time.Millisecond), 0, mailbox.RemoteHGOpen)
	// Nobody waits: the stale sample still names the reason (no flapping).
	g.want("stale, nobody waits", g.tick(HeapSampleFresh+100*time.Millisecond), 0, mailbox.RemoteHGOpen)
	g.views = []posefeed.RemoteView{{ID: 4, LinkSlot: 0, Live: true}}
	// A slot starts waiting: the slow walk is pulled in, so the gate
	// opens on a fresh sample at once instead of NO_SAMPLE until it.
	g.want("a slot waits, walked at once", g.tick(16*time.Millisecond), 0x01, mailbox.RemoteHGOpen)
	// A failed walk leaves the stale sample: NO_SAMPLE while a slot waits.
	g.counter(addr.GameHeapPtr, 0)
	g.want("walk fails, a slot waits", g.tick(HeapWalkFast), 0, mailbox.RemoteHGNoSample)
	g.heaps(0x100000, 0x20000, 0x30000)
	g.want("walked again", g.tick(HeapWalkFast), 0x01, mailbox.RemoteHGOpen)

	g.scope.Pending, g.scope.InPlay = true, false
	g.want("pending", g.tick(16*time.Millisecond), 0, mailbox.RemoteHGStageSettle)
	g.scope.Pending, g.scope.InPlay = false, true
	g.settle("after the reload")
}

// TestHeapGateInflight: a FULL body QUEUED clears every bit (INFLIGHT);
// once it is LIVE, FullMax holds the gate; with room for another, the gate
// reopens only on a sample taken after the status that showed LIVE.
func TestHeapGateInflight(t *testing.T) {
	g := newGateRig(t, func(c *RemoteConfig) { c.FullMax = 2 })
	g.views = append(g.views, posefeed.RemoteView{ID: 5, LinkSlot: 1, Live: true})
	g.settleTo("stage", mailbox.RemoteHGQueued)
	g.body(0, mailbox.RemoteBodyQueued, mailbox.RemoteKindFull)
	g.tick(16 * time.Millisecond)
	g.want("queued", g.tick(16*time.Millisecond), 0, mailbox.RemoteHGInflight)
	g.want("still queued", g.tick(16*time.Millisecond), 0, mailbox.RemoteHGInflight)
	samples := g.r.gate.Samples
	g.body(0, mailbox.RemoteBodyLive, mailbox.RemoteKindFull)
	g.tick(16 * time.Millisecond)
	g.want("live", g.tick(16*time.Millisecond), 0x02, mailbox.RemoteHGOpen)
	if g.r.gate.Samples == samples || !g.r.gate.sampleAt.After(g.r.gate.reopenAt) {
		t.Errorf("reopened without a sample after LIVE (samples %d -> %d)", samples, g.r.gate.Samples)
	}
	g.body(1, mailbox.RemoteBodyLive, mailbox.RemoteKindFull)
	g.tick(16 * time.Millisecond)
	g.want("full max", g.tick(16*time.Millisecond), 0, mailbox.RemoteHGFullMax)

	// FullMax 1 (the default): one LIVE FULL body holds the gate.
	g = newGateRig(t, nil)
	g.views = append(g.views, posefeed.RemoteView{ID: 5, LinkSlot: 1, Live: true})
	g.settleTo("default", mailbox.RemoteHGQueued)
	g.body(0, mailbox.RemoteBodyParked, mailbox.RemoteKindFull)
	g.tick(16 * time.Millisecond)
	g.want("default full max", g.tick(16*time.Millisecond), 0, mailbox.RemoteHGFullMax)
}

// TestHeapGateCounters: a Game or Zelda error counter rising closes the
// gate for the rest of the stage; solid only while a FULL body is QUEUED
// (vanilla bumps it per stage load); two closed stages in a row are
// STICKY for the session.
func TestHeapGateCounters(t *testing.T) {
	g := newGateRig(t, nil)
	g.settle("stage 1")
	g.counter(addr.SolidHeapErrors, 3)
	g.want("solid, nothing queued", g.tick(16*time.Millisecond), 0x01, mailbox.RemoteHGOpen)
	g.counter(addr.ZeldaHeapErrors, 1)
	g.want("zelda rose", g.tick(16*time.Millisecond), 0, mailbox.RemoteHGCounterRose)
	g.want("rest of the stage", g.tick(time.Second), 0, mailbox.RemoteHGCounterRose)

	// A clean stage between closed ones resets the run.
	g.scope.Stage = "sea"
	g.settle("stage 2 (baseline absorbs zelda 1)")
	g.scope.Stage = "M_Dai"
	g.settle("stage 3")
	g.body(0, mailbox.RemoteBodyQueued, mailbox.RemoteKindFull)
	g.tick(16 * time.Millisecond)
	g.counter(addr.SolidHeapErrors, 4)
	g.want("solid while queued", g.tick(16*time.Millisecond), 0, mailbox.RemoteHGCounterRose)
	g.body(0, mailbox.RemoteBodyEmpty, mailbox.RemoteKindFull)
	g.tick(16 * time.Millisecond)

	// Stage 3 closed, so stage 4 closing makes two in a row: STICKY.
	g.scope.Stage = "sea"
	g.settle("stage 4")
	g.counter(addr.GameHeapErrors, 1)
	g.want("two in a row", g.tick(16*time.Millisecond), 0, mailbox.RemoteHGSticky)
	g.scope.Stage = "M_Dai"
	g.tick(16 * time.Millisecond)
	g.want("sticky for the session", g.tick(time.Second), 0, mailbox.RemoteHGSticky)
}

// TestHeapGateWalkProblem: a walk Problem reads NO_SAMPLE and walks again
// on the next Tick; heapProblemStreak in a row is WALK_PROBLEM; a clean
// walk clears it.
func TestHeapGateWalkProblem(t *testing.T) {
	g := newGateRig(t, nil)
	g.settle("stage")
	store(t, g.m, gateGameHeap, binary.BigEndian.AppendUint32(nil, 0x8039CCC0)) // not an ExpHeap
	g.tick(300 * time.Millisecond)
	for i := 1; i < heapProblemStreak; i++ {
		g.want("problem", g.r.blockAfter(), 0, mailbox.RemoteHGNoSample)
		g.tick(16 * time.Millisecond)
	}
	g.want("streak", g.r.blockAfter(), 0, mailbox.RemoteHGWalkProblem)
	g.heaps(0x100000, 0x20000, 0x30000)
	g.want("clean again", g.tick(300*time.Millisecond), 0x01, mailbox.RemoteHGOpen)
}

// blockAfter is the last block written (for steps that tick first).
func (r *RemoteHost) blockAfter() mailbox.RemoteBlock {
	blk, _, _, _ := mailbox.ReadRemoteBlock(r.m)
	return blk
}

type recReporter struct{ strings.Builder }

func (r *recReporter) Log(_ log.Level, msg string) { r.WriteString(msg + "|") }

// TestHeapGateLog: the status line and the change log name the reason.
func TestHeapGateLog(t *testing.T) {
	g := newGateRig(t, nil)
	rec := &recReporter{}
	g.r.rep = rec
	g.settle("stage")
	got := rec.String()
	for _, want := range []string{"heap gate stage_settle allow 0x00", "heap gate open allow 0x01", "game largest 0x100000", "k0]"} {
		if !strings.Contains(got, want) {
			t.Errorf("log lacks %q:\n%s", want, got)
		}
	}
}

// TestHeapGateSettling: a slot whose position still trails its remote's
// by more than SettleDist (the smoother crossing a stage change) gets no
// bit and holds with QUEUED, a transient reason the REL waits on; once it
// arrives the bit is its.
func TestHeapGateSettling(t *testing.T) {
	g := newGateRig(t, nil)
	g.views[0].Pos = [3]float32{1398.9, 3648.4, -2063.9} // the stage before
	g.views[0].Target = [3]float32{-7003.8, -300, 3090.5}
	g.want("first sample", g.tick(16*time.Millisecond), 0, mailbox.RemoteHGStageSettle)
	g.want("between samples", g.tick(100*time.Millisecond), 0, mailbox.RemoteHGStageSettle)
	g.want("sliding", g.tick(150*time.Millisecond), 0, mailbox.RemoteHGQueued)
	g.want("still sliding", g.tick(250*time.Millisecond), 0, mailbox.RemoteHGQueued)
	// Within SettleDist (running lag): the bit.
	g.views[0].Pos = [3]float32{-6990, -295, 3080}
	g.tick(16 * time.Millisecond)
	g.want("arrived", g.tick(250*time.Millisecond), 0x01, mailbox.RemoteHGOpen)

	// Two slots, 0 sliding: 1 gets the bit, QUEUED (0 still waits).
	g = newGateRig(t, nil)
	g.views[0].Target = [3]float32{5000, 0, 0}
	g.views = append(g.views, posefeed.RemoteView{ID: 5, LinkSlot: 1, Live: true})
	g.want("first sample", g.tick(16*time.Millisecond), 0, mailbox.RemoteHGStageSettle)
	g.want("between samples", g.tick(100*time.Millisecond), 0, mailbox.RemoteHGStageSettle)
	g.want("1 first", g.tick(150*time.Millisecond), 0x02, mailbox.RemoteHGQueued)
}

// TestHeapGateArrival: a remote whose PSTATE names the host's stage
// while it still loads gets no stage in its slot (the REL neither creates
// nor runs down its FULL wait); once loaded it holds QUEUED until a new
// position comes in and the smoothed one has caught up.
func TestHeapGateArrival(t *testing.T) {
	g := newGateRig(t, nil)
	g.noMove = true
	old := [3]float32{1398.9, 3648.4, -2063.9}
	g.views[0].Pos, g.views[0].Target = old, old
	g.stages[4] = "sea"
	g.want("first sample", g.tick(16*time.Millisecond), 0, mailbox.RemoteHGStageSettle)
	g.want("between samples", g.tick(100*time.Millisecond), 0, mailbox.RemoteHGStageSettle)
	g.want("other stage", g.tick(2100*time.Millisecond), 0, mailbox.RemoteHGOpen)

	// Loading into the host's stage: no stage in the slot, no bit.
	delete(g.stages, 4)
	g.flags[4] = PSLoading
	b := g.tick(2100 * time.Millisecond)
	g.want("loading", b, 0, mailbox.RemoteHGOpen)
	if b.Slots[0].Stage != "" {
		t.Errorf("loading: slot stage %q, want none", b.Slots[0].Stage)
	}
	// Loaded, but no new position yet: QUEUED.
	g.flags[4] = 0
	b = g.tick(250 * time.Millisecond)
	g.want("no new position", b, 0, mailbox.RemoteHGQueued)
	if b.Slots[0].Stage != g.scope.Stage {
		t.Errorf("loaded: slot stage %q, want %q", b.Slots[0].Stage, g.scope.Stage)
	}
	// The new position arrives; the smoother is still sliding: QUEUED.
	g.views[0].Target = [3]float32{-7003.8, -300, 3090.5}
	g.want("sliding", g.tick(250*time.Millisecond), 0, mailbox.RemoteHGQueued)
	// Caught up: the bit.
	g.views[0].Pos = g.views[0].Target
	g.tick(16 * time.Millisecond)
	g.want("arrived", g.tick(250*time.Millisecond), 0x01, mailbox.RemoteHGOpen)
}

// TestHeapGateArrivalEarly: the new stage's position can come in while
// PSTATE still says loading (the Link spawns first); the slot counts it
// as arrived, so a remote standing still at its spawn gets the bit. With
// no new position at all, ArriveWait after the load ends the wait.
func TestHeapGateArrivalEarly(t *testing.T) {
	for _, early := range []bool{true, false} {
		g := newGateRig(t, nil)
		g.noMove = true
		old := [3]float32{1398.9, 3648.4, -2063.9}
		g.views[0].Pos, g.views[0].Target = old, old
		g.stages[4] = "sea"
		g.want("first sample", g.tick(16*time.Millisecond), 0, mailbox.RemoteHGStageSettle)
		g.want("between samples", g.tick(100*time.Millisecond), 0, mailbox.RemoteHGStageSettle)
		g.want("other stage", g.tick(2100*time.Millisecond), 0, mailbox.RemoteHGOpen)
		delete(g.stages, 4)
		g.flags[4] = PSLoading
		g.tick(2100 * time.Millisecond)
		if early {
			spawn := [3]float32{-7003.8, -300, 3090.5}
			g.views[0].Pos, g.views[0].Target = spawn, spawn
			g.tick(250 * time.Millisecond)
		}
		g.flags[4] = 0
		if early {
			g.want("loaded, early position", g.tick(250*time.Millisecond), 0x01, mailbox.RemoteHGOpen)
			continue
		}
		g.want("loaded, no position", g.tick(250*time.Millisecond), 0, mailbox.RemoteHGQueued)
		g.tick(ArriveWait)
		g.want("arrive wait over", g.tick(250*time.Millisecond), 0x01, mailbox.RemoteHGOpen)
	}
}
