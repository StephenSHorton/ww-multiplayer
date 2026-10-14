package replica

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/internal/coop/addr"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/fakegame"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/gate"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/mem"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/snap"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/world"
)

// The stages the writer tests load (the names are only labels).
var (
	wOutset = fakegame.Stage{Name: "sea", Room: 44, Table: addr.TableSea}
	wDRC    = fakegame.Stage{Name: "M_NewD2", Table: addr.TableDRC, KeyHUD: true}
)

// testBeat is the rigs' heartbeat timeout: short, as a test's game only
// beats when told to.
const testBeat = 20 * time.Millisecond

// plannedOR is the planned OR of bits into image byte off while table cur
// is loaded, as the Planner places it: the saved byte, and the live copy
// when off is in cur's saved slot.
func plannedOR(off int, bits byte, cur int) Write {
	w := Write{Off: uint16(off), Bits: bits, Category: CategoryOf(off), Saved: imageAddr(off)}
	if t, rel, ok := memBitTable(off); ok && t == cur {
		w.Live = addr.LiveMemBit + uint32(rel)
	}
	return w
}

// liveCopy is the live memBit address of image byte off of the loaded
// table.
func liveCopy(off int) uint32 {
	_, rel, _ := memBitTable(off)
	return addr.LiveMemBit + uint32(rel)
}

// hookMem is a Mem that runs the game at chosen points of the writer's
// accesses, so a test can put a frame, or a game store, exactly between
// two of them.
type hookMem struct {
	mem.Mem
	mu        sync.Mutex
	polls     int
	beatAt    int    // the heartbeat read that runs beat first; 0: none
	beat      func() // the game's frame
	afterRead func(a uint32, n int)
	afterWrt  func(a uint32)
	drop      bool // writes report success and change nothing (a lost write)
}

func (h *hookMem) Read(a uint32, n int) ([]byte, error) {
	h.mu.Lock()
	var beat func()
	if a == addr.SpawnTrigger && h.beat != nil {
		if h.polls++; h.polls == h.beatAt {
			beat = h.beat
		}
	}
	after := h.afterRead
	h.mu.Unlock()
	if beat != nil {
		beat()
	}
	b, err := h.Mem.Read(a, n)
	if after != nil {
		after(a, n)
	}
	return b, err
}

func (h *hookMem) Write(a uint32, b []byte) error {
	h.mu.Lock()
	drop, after := h.drop, h.afterWrt
	h.mu.Unlock()
	if drop {
		return nil
	}
	err := h.Mem.Write(a, b)
	if after != nil {
		after(a)
	}
	return err
}

// beatOn makes the n-th heartbeat read from now run beat first.
func (h *hookMem) beatOn(n int, beat func()) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.polls, h.beatAt, h.beat = 0, n, beat
}

func (h *hookMem) hooks(afterRead func(a uint32, n int), afterWrite func(a uint32)) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.afterRead, h.afterWrt = afterRead, afterWrite
}

func (h *hookMem) dropWrites(on bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.drop = on
}

// wrig is a FrameWriter over a FakeGame with a file loaded in Outset.
// The game's own writes go through FakeMem.Store; the writer's through
// hookMem to FakeMem.Write, which the recorder sees.
type wrig struct {
	t     *testing.T
	m     *mem.FakeMem
	g     *fakegame.FakeGame
	hm    *hookMem
	gt    gate.Tracker
	st    gate.Status
	brk   *Breaker
	wg    *WriteGuard
	fw    *FrameWriter
	log   logLines
	armed bool
}

// newWriterRig settles the gates in Outset, armed. cfg's Policy defaults
// to switch5Policy; its Breaker and Logf are the rig's.
func newWriterRig(t *testing.T, cfg GuardConfig) *wrig {
	t.Helper()
	m := mem.NewFakeMem()
	r := &wrig{t: t, m: m, g: fakegame.New(m), hm: &hookMem{Mem: m}, armed: true}
	if err := r.g.NewGame(0, "Link"); err != nil {
		t.Fatal(err)
	}
	r.load(wOutset)
	if cfg.Breaker == nil {
		cfg.Breaker = NewBreaker(BreakerConfig{Logf: r.log.logf})
	}
	if cfg.Policy == nil {
		cfg.Policy = switch5Policy(t)
	}
	cfg.Logf = r.log.logf
	r.brk = cfg.Breaker
	r.wg = NewWriteGuard(cfg)
	fw, err := NewFrameWriter(WriterConfig{Mem: r.hm, Guard: r.wg, BeatTimeout: testBeat, Logf: r.log.logf})
	if err != nil {
		t.Fatal(err)
	}
	r.fw = fw
	r.settle()
	return r
}

func (r *wrig) load(s fakegame.Stage) {
	r.t.Helper()
	if err := r.g.LoadStage(s); err != nil {
		r.t.Fatalf("LoadStage(%s): %v", s.Name, err)
	}
}

// observe reads a snapshot (straight from FakeMem, past the hooks) and
// hands it to the gates and the writer.
func (r *wrig) observe() VerifyReport {
	r.t.Helper()
	s, err := snap.Read(r.m)
	if err != nil {
		r.t.Fatal(err)
	}
	r.st = r.gt.Observe(s)
	return r.fw.Observe(s, r.st, r.armed)
}

// tick runs three frames (a 10 Hz tick at 30 fps), then observes.
func (r *wrig) tick() VerifyReport {
	r.t.Helper()
	r.g.Frames(3)
	return r.observe()
}

func (r *wrig) settle() {
	r.t.Helper()
	for range gate.StableTicks + 2 {
		r.tick()
	}
	if !r.st.CanWrite() {
		r.t.Fatalf("the gates did not open: %s", r.st.Why())
	}
}

// apply runs a batch with the game beating on the writer's second
// heartbeat read.
func (r *wrig) apply(ws ...Write) (Result, error) {
	r.hm.beatOn(2, r.g.Frame)
	defer r.hm.beatOn(0, nil)
	return r.fw.ApplyContext(context.Background(), ws)
}

func (r *wrig) byteAt(a uint32) byte {
	r.t.Helper()
	b, err := r.m.Read(a, 1)
	if err != nil {
		r.t.Fatal(err)
	}
	return b[0]
}

// gameSet ORs bits into the byte at a as the game would.
func (r *wrig) gameSet(a uint32, bits byte) {
	r.t.Helper()
	if err := r.m.Store(a, []byte{r.byteAt(a) | bits}); err != nil {
		r.t.Fatal(err)
	}
}

// audit checks the write audit of §8.1 over what the recorder saw: every
// write one byte, inside M3's allowlist (so no PER_PLAYER, NEVER,
// DERIVED or TAG byte), and each result's bytes grow-only.
func (r *wrig) audit(results ...Result) {
	r.t.Helper()
	g := NewGuardFor(M3Targets)
	for _, w := range r.m.Writes() {
		if len(w.Data) != 1 {
			r.t.Fatalf("a %d-byte write at 0x%08X: writes are bytes", len(w.Data), w.Addr)
		}
		if g.Mask(w.Addr) == 0 {
			r.t.Fatalf("a write at 0x%08X, outside M3's allowlist", w.Addr)
		}
	}
	for _, res := range results {
		for _, b := range res.Wrote {
			if b.Old&^b.New != 0 {
				r.t.Fatalf("0x%08X went 0x%02X -> 0x%02X: a clear", b.Addr, b.Old, b.New)
			}
		}
	}
}

func TestFrameWriterWritesAndVerifies(t *testing.T) {
	r := newWriterRig(t, GuardConfig{})
	r.m.Record(true)
	sea, drc := addr.OffMemBit(addr.TableSea), addr.OffMemBit(addr.TableDRC)
	chest := sea + addr.MemBitOffTbox + 3
	sw5, m5 := switchImageByte(addr.TableSea, 0x05)
	r.gameSet(liveCopy(chest), 0x10) // a chest the player opened
	ws := []Write{
		plannedOR(chest, 0x05, addr.TableSea),
		plannedOR(sw5, m5, addr.TableSea),
		plannedOR(drc+addr.MemBitOffDungeonItem, 0x07, addr.TableSea),
		plannedOR(addr.OffOcean+4, 0x80, addr.TableSea),
		plannedOR(addr.OffMapGet+1, 0x02, addr.TableSea),
	}
	res, err := r.apply(ws...)
	if err != nil {
		t.Fatalf("Apply: %v (%+v)", err, res)
	}
	want := []uint32{liveCopy(chest), imageAddr(chest), liveCopy(sw5), imageAddr(sw5),
		imageAddr(drc + addr.MemBitOffDungeonItem), imageAddr(addr.OffOcean + 4), imageAddr(addr.OffMapGet + 1)}
	got := r.m.Writes()
	if len(got) != len(want) || len(res.Wrote) != len(want) {
		t.Fatalf("%d writes (%d in the result), want %d: %+v", len(got), len(res.Wrote), len(want), got)
	}
	for i, w := range got {
		if w.Addr != want[i] || res.Wrote[i].Addr != want[i] {
			t.Errorf("write %d at 0x%08X, want 0x%08X (live copy first)", i, w.Addr, want[i])
		}
	}
	if v := r.byteAt(liveCopy(chest)); v != 0x15 {
		t.Errorf("live chest byte 0x%02X, want the game's 0x10 kept and 0x05 added", v)
	}
	if res.Beat == 0 || res.Waited > testBeat {
		t.Errorf("beat %d after %v", res.Beat, res.Waited)
	}
	r.audit(res)

	rep := r.tick()
	if rep.Verified != len(want) || rep.Failed != 0 || rep.Unverified != 0 {
		t.Fatalf("verify %+v", rep)
	}

	// Planned again, all is there: nothing is written.
	r.m.ResetWrites()
	res, err = r.apply(ws...)
	if err != nil || res.Already != len(want) || len(r.m.Writes()) != 0 {
		t.Fatalf("second Apply: %v, %+v, %d writes", err, res, len(r.m.Writes()))
	}
	if st := r.fw.Stats(); st.Batches != 1 || st.Written != len(want) || st.Verified != len(want) || st.Already != len(want) {
		t.Errorf("stats %+v", st)
	}
}

// TestFrameWriterKeepsAGameWriteToAnotherByte puts the game's onSwitch,
// a word load, OR and store, between the writer's read and write of the
// next byte of the same word: a byte write leaves the game's bit alone.
func TestFrameWriterKeepsAGameWriteToAnotherByte(t *testing.T) {
	r := newWriterRig(t, GuardConfig{Policy: world.FullPolicy()})
	off, m0 := switchImageByte(addr.TableSea, 0x00) // mSwitch[0] bits 0-7: the word's last byte
	gOff, g8 := switchImageByte(addr.TableSea, 0x08)
	if liveCopy(off)&^3 != liveCopy(gOff)&^3 || off == gOff {
		t.Fatal("the switches are not neighbours in one word")
	}
	var once sync.Once
	r.hm.hooks(func(a uint32, n int) {
		if a == liveCopy(off) && n == 1 {
			once.Do(func() {
				if err := r.g.OnSwitch(0x08, -1); err != nil {
					t.Error(err)
				}
			})
		}
	}, nil)
	res, err := r.apply(plannedOR(off, m0, addr.TableSea))
	if err != nil {
		t.Fatal(err)
	}
	if v := r.byteAt(liveCopy(off)); v&m0 == 0 {
		t.Fatalf("the writer's switch 0x00 is not set: 0x%02X", v)
	}
	if v := r.byteAt(liveCopy(gOff)); v&g8 == 0 {
		t.Fatalf("the game's switch 0x08 was lost: 0x%02X", v)
	}
	if rep := r.tick(); rep.Verified != len(res.Wrote) || rep.Failed != 0 {
		t.Fatalf("verify %+v", rep)
	}
}

// TestFrameWriterDetectsALostWrite is the race the other way (§4.2.1):
// the game loads a word before the writer's write and stores it after, so
// the writer's bit is lost. The next tick's verify sees it, and the next
// batch writes it again.
func TestFrameWriterDetectsALostWrite(t *testing.T) {
	r := newWriterRig(t, GuardConfig{Policy: world.FullPolicy()})
	off, m0 := switchImageByte(addr.TableSea, 0x00)
	var store func()
	r.hm.hooks(func(a uint32, n int) {
		if a == liveCopy(off) && n == 1 && store == nil {
			var err error
			if store, err = r.g.BeginOnSwitch(0x08); err != nil {
				t.Error(err)
			}
		}
	}, func(a uint32) {
		if a == liveCopy(off) && store != nil {
			store()
		}
	})
	w := plannedOR(off, m0, addr.TableSea)
	if _, err := r.apply(w); err != nil {
		t.Fatal(err)
	}
	r.hm.hooks(nil, nil)
	if v := r.byteAt(liveCopy(off)); v&m0 != 0 {
		t.Fatalf("the game's store did not undo the write: 0x%02X", v)
	}
	rep := r.tick()
	if rep.Failed != 1 || rep.Verified != 1 || len(rep.Failures) != 1 || rep.Failures[0].Addr != liveCopy(off) {
		t.Fatalf("verify %+v, want the live byte failed and the saved one verified", rep)
	}
	if r.log.count("verify failed") != 1 || r.brk.Off() {
		t.Fatalf("off %v; log:\n%s", r.brk.Off(), &r.log)
	}
	if _, err := r.apply(w); err != nil {
		t.Fatal(err)
	}
	if rep := r.tick(); rep.Verified != 1 || rep.Failed != 0 {
		t.Fatalf("the rewrite: verify %+v", rep)
	}
}

func TestFrameWriterGatesClosedMeansNoWrite(t *testing.T) {
	cases := []struct {
		name  string
		setup func(r *wrig)
		open  bool
	}{
		{"not armed", func(r *wrig) { r.armed = false; r.tick() }, false},
		{"settling after a room change", func(r *wrig) {
			if err := r.g.EnterRoom(45); err != nil {
				t.Fatal(err)
			}
			r.tick()
		}, false},
		{"a stage change pending", func(r *wrig) {
			if err := r.g.SetNextStage(wDRC); err != nil {
				t.Fatal(err)
			}
			r.tick()
		}, false},
		{"the heartbeat stalled", func(r *wrig) { r.observe() }, false},
		{"the snapshot failed", func(r *wrig) { r.fw.Observe(nil, r.gt.Observe(nil), true) }, false},
		{"on file select", func(r *wrig) { r.g.EnterFileSelect(); r.tick() }, false},
		{"the pause menu is open: stage bits need no IDLE", func(r *wrig) { r.g.SetPauseMenu(true); r.tick() }, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := newWriterRig(t, GuardConfig{})
			c.setup(r)
			r.m.Record(true)
			chest := addr.OffMemBit(addr.TableDRC) + addr.MemBitOffTbox
			start := time.Now()
			res, err := r.apply(plannedOR(chest, 1, addr.TableSea))
			if c.open {
				if err != nil || len(res.Wrote) != 1 {
					t.Fatalf("Apply: %v, %+v", err, res)
				}
				return
			}
			if !errors.Is(err, ErrClosed) || res.Closed != 1 {
				t.Fatalf("Apply = %v, %+v; want ErrClosed", err, res)
			}
			if n := len(r.m.Writes()); n != 0 {
				t.Fatalf("%d writes with a gate closed", n)
			}
			if d := time.Since(start); d >= testBeat {
				t.Errorf("a closed batch waited %v for the beat", d)
			}
			if r.brk.Off() || r.wg.Stats().Refused != 0 {
				t.Errorf("a closed gate counted as a fault: %+v", r.wg.Stats())
			}
		})
	}
}

// TestFrameWriterRefusesForbiddenBytes hands the writer a buggy plan: the
// guard refuses every PER_PLAYER, NEVER, DERIVED and TAG byte, and the
// good write in the batch still lands.
func TestFrameWriterRefusesForbiddenBytes(t *testing.T) {
	r := newWriterRig(t, GuardConfig{Breaker: NewBreaker(BreakerConfig{MaxFaults: 100})})
	r.m.Record(true)
	raw := func(off int) Write { return Write{Off: uint16(off), Bits: 1, Saved: imageAddr(off)} }
	drc := addr.OffMemBit(addr.TableDRC)
	bad := []Write{
		raw(addr.OffLife + 1),                               // PER_PLAYER
		raw(addr.OffRupee + 1),                              // PER_PLAYER
		raw(addr.OffStatusAPad),                             // NEVER
		raw(drc + addr.MemBitOffPad),                        // NEVER
		raw(drc + addr.MemBitOffKeyNum),                     // DERIVED
		raw(addr.OffSelectEquip),                            // DERIVED
		raw(addr.OffReserve),                                // TAG
		raw(addr.OffGetItem),                                // M4's
		plannedOR(addr.OffEvent+2, 1, 0),                    // M4's
		plannedOR(drc+addr.MemBitOffTbox, 1, addr.TableDRC), // planned for a table not loaded
	}
	good := plannedOR(drc+addr.MemBitOffTbox, 1, addr.TableSea)
	res, err := r.apply(append(bad, good)...)
	if !errors.Is(err, ErrGuard) {
		t.Fatalf("Apply = %v, want ErrGuard", err)
	}
	if res.Refused != len(bad) || len(res.Wrote) != 1 || res.Wrote[0].Addr != good.Saved {
		t.Fatalf("result %+v", res)
	}
	r.audit(res)
	if st := r.wg.Stats(); st.Refused != len(bad) {
		t.Errorf("guard stats %+v", st)
	}
	if st := r.fw.Stats(); st.Refused != len(bad) {
		t.Errorf("writer stats %+v", st)
	}
	if n := r.log.count("(refusal "); n != len(bad) {
		t.Errorf("%d refusals logged, want %d:\n%s", n, len(bad), &r.log)
	}
}

func TestFrameWriterPolicy(t *testing.T) {
	r := newWriterRig(t, GuardConfig{})
	r.m.Record(true)
	sw5, m5 := switchImageByte(addr.TableDRC, 0x05)
	swD, mD := switchImageByte(addr.TableDRC, 0x0D)
	sw40, m40 := switchImageByte(addr.TableDRC, 0x40)
	res, err := r.apply(plannedOR(sw5, m5, addr.TableSea), plannedOR(swD, mD, addr.TableSea), plannedOR(sw40, m40, addr.TableSea))
	if !errors.Is(err, ErrGuard) || res.Refused != 2 || len(res.Wrote) != 1 || res.Wrote[0].Addr != imageAddr(sw5) {
		t.Fatalf("Apply = %v, %+v: want only the synced switch written", err, res)
	}
	if r.byteAt(imageAddr(swD))&mD != 0 || r.byteAt(imageAddr(sw40))&m40 != 0 {
		t.Fatal("an unsynced switch was written")
	}

	// A synced and an unsynced switch in one byte: the whole byte is
	// refused, so the plan fails closed.
	sw6, m6 := switchImageByte(addr.TableDRC, 0x06)
	if sw6 != sw5 {
		t.Fatal("switches 5 and 6 are not in one byte")
	}
	sw5s, _ := switchImageByte(addr.TableSea, 0x05)
	res, err = r.apply(plannedOR(sw5s, m5|m6, addr.TableSea))
	if !errors.Is(err, ErrGuard) || len(res.Wrote) != 0 {
		t.Fatalf("Apply = %v, %+v: want the byte refused", err, res)
	}
}

// TestFrameWriterAborts moves the game on between the tick's snapshot and
// the beat: nothing is written, and the guard stays shut for the tick.
func TestFrameWriterAborts(t *testing.T) {
	cases := []struct {
		name  string
		setup func(r *wrig)
		beat  func(r *wrig) func()
	}{
		{"a stage change was asked for", func(r *wrig) {
			if err := r.g.SetNextStage(wDRC); err != nil {
				t.Fatal(err)
			}
		}, nil},
		{"another stage loaded", func(r *wrig) { r.load(wDRC) }, nil},
		{"the same stage reloaded with another table", func(r *wrig) {
			s := wOutset
			s.Table = addr.TableSea2
			r.load(s)
		}, nil},
		{"a save state was loaded during the wait", nil, func(r *wrig) func() {
			return func() {
				if err := r.m.Store(addr.SpawnTrigger, []byte{0, 0, 0, 1}); err != nil {
					t.Error(err)
				}
			}
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := newWriterRig(t, GuardConfig{})
			chest := addr.OffMemBit(addr.TableSea) + addr.MemBitOffTbox
			if c.setup != nil {
				c.setup(r)
			}
			r.m.Record(true)
			beat := r.g.Frame
			if c.beat != nil {
				beat = c.beat(r)
			}

			r.hm.beatOn(2, beat)
			res, err := r.fw.ApplyContext(context.Background(), []Write{plannedOR(chest, 1, addr.TableSea)})
			if !errors.Is(err, ErrAborted) || len(res.Wrote) != 0 {
				t.Fatalf("Apply = %v, %+v; want ErrAborted", err, res)
			}
			if n := len(r.m.Writes()); n != 0 {
				t.Fatalf("%d writes", n)
			}
			if st := r.fw.Stats(); st.Aborted != 1 {
				t.Errorf("stats %+v", st)
			}
			if err := r.wg.Check(imageAddr(chest), 0, 1); !errors.Is(err, ErrGuard) {
				t.Errorf("the guard is open after the abort: %v", err)
			}
			r.hm.beatOn(0, nil)
			if r.byteAt(addr.NextStageEnable) != 0 {
				if err := r.g.ChangeStage(); err != nil {
					t.Fatal(err)
				}
			}
			r.settle()
			if err := r.wg.Check(imageAddr(chest), 0, 1); err != nil {
				t.Errorf("the guard did not reopen on settled ticks: %v", err)
			}
		})
	}
}

func TestFrameWriterStall(t *testing.T) {
	r := newWriterRig(t, GuardConfig{})
	r.m.Record(true)
	start := time.Now()
	_, err := r.fw.ApplyContext(context.Background(), []Write{plannedOR(addr.OffOcean, 1, addr.TableSea)})
	if !errors.Is(err, ErrStall) {
		t.Fatalf("Apply = %v, want ErrStall", err)
	}
	if d := time.Since(start); d < testBeat {
		t.Errorf("gave up after %v, before the %v timeout", d, testBeat)
	}
	if len(r.m.Writes()) != 0 || r.fw.Stats().Stalls != 1 || r.log.count("did not move") != 1 {
		t.Fatalf("writes %d, stats %+v, log:\n%s", len(r.m.Writes()), r.fw.Stats(), &r.log)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := r.fw.ApplyContext(ctx, []Write{plannedOR(addr.OffOcean, 1, addr.TableSea)}); !errors.Is(err, context.Canceled) {
		t.Fatalf("Apply with a done context = %v", err)
	}
}

// TestFrameWriterAutoDisablesOnVerifyFailures loses every write, as
// Dolphin's dual mapping can (CLAUDE.md gotcha 2): the ninth verify
// failure in a minute turns co-op off, and nothing is written after.
func TestFrameWriterAutoDisablesOnVerifyFailures(t *testing.T) {
	r := newWriterRig(t, GuardConfig{})
	r.hm.dropWrites(true)
	w := plannedOR(addr.OffOcean+1, 1, addr.TableSea)
	for i := range MaxFaultsPerMinute + 1 {
		if _, err := r.apply(w); err != nil {
			t.Fatalf("batch %d: %v", i, err)
		}
		rep := r.tick()
		if rep.Failed != 1 {
			t.Fatalf("batch %d: verify %+v", i, rep)
		}
		if off := r.brk.Off(); off != (i == MaxFaultsPerMinute) {
			t.Fatalf("after %d failures: off %v (%s)", i+1, off, r.brk.Why())
		}
	}
	off, why := r.fw.Disabled()
	if !off || !strings.Contains(why, "9 write faults") || !strings.Contains(why, "9 verify failures") {
		t.Fatalf("Disabled = %v, %q", off, why)
	}
	r.hm.dropWrites(false)
	r.m.Record(true)
	_, err := r.apply(w)
	var de *DisabledError
	if !errors.Is(err, ErrOff) || !errors.As(err, &de) || de.Why != why {
		t.Fatalf("Apply after the trip = %v", err)
	}
	if len(r.m.Writes()) != 0 {
		t.Fatal("wrote after the trip")
	}
}

// TestFrameWriterAutoDisablesOnSaveStateStorm loads a "save state" (the
// heartbeat goes back) five times in a minute.
func TestFrameWriterAutoDisablesOnSaveStateStorm(t *testing.T) {
	r := newWriterRig(t, GuardConfig{})
	for i := range MaxRegressionsPerMinute + 1 {
		if err := r.m.Store(addr.SpawnTrigger, []byte{0, 0, 0, byte(i)}); err != nil {
			t.Fatal(err)
		}
		r.observe()
		if !r.st.Regressed {
			t.Fatalf("load %d: no regression seen", i)
		}
		r.g.Frames(1000)
		r.tick()
		if off := r.brk.Off(); off != (i == MaxRegressionsPerMinute) {
			t.Fatalf("after %d loads: off %v (%s)", i+1, off, r.brk.Why())
		}
	}
	if _, why := r.fw.Disabled(); !strings.Contains(why, "save-state loads") {
		t.Fatalf("why %q", why)
	}
}

func TestFrameWriterMismatchTurnsOff(t *testing.T) {
	r := newWriterRig(t, GuardConfig{})
	r.brk.Mismatch("the host's switch tables differ from this build's")
	r.m.Record(true)
	if _, err := r.apply(plannedOR(addr.OffOcean, 1, addr.TableSea)); !errors.Is(err, ErrOff) {
		t.Fatalf("Apply = %v, want ErrOff", err)
	}
	if off, why := r.fw.Disabled(); !off || !strings.Contains(why, "switch tables differ") || len(r.m.Writes()) != 0 {
		t.Fatalf("Disabled = %v, %q; %d writes", off, why, len(r.m.Writes()))
	}
}

// TestFrameWriterStopWithinOneTick runs the game and a 10 Hz-style tick
// loop that always has something to write, then turns co-op off: Stop
// returns before a tick has passed, and nothing is written after it.
func TestFrameWriterStopWithinOneTick(t *testing.T) {
	r := newWriterRig(t, GuardConfig{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); r.g.Run(ctx, time.Millisecond) }()

	var wrote, offErrs atomic.Int64
	ocean := addr.OffOcean + 8
	go func() {
		defer wg.Done()
		var gt gate.Tracker
		for i := 0; ctx.Err() == nil; i++ {
			// The game clears the byte (a save state, say), so there is
			// always a write to make.
			r.m.Store(imageAddr(ocean), []byte{0})
			s, err := snap.Read(r.m)
			if err != nil {
				t.Error(err)
				return
			}
			r.fw.Observe(s, gt.Observe(s), true)
			res, err := r.fw.ApplyContext(ctx, []Write{plannedOR(ocean, byte(1)<<(i%8), addr.TableSea)})
			wrote.Add(int64(len(res.Wrote)))
			if errors.Is(err, ErrOff) {
				offErrs.Add(1)
			}
			time.Sleep(2 * time.Millisecond)
		}
	}()
	deadline := time.Now().Add(5 * time.Second)
	for wrote.Load() < 3 {
		if time.Now().After(deadline) {
			t.Fatalf("the loop wrote %d bytes in 5 s; log:\n%s", wrote.Load(), &r.log)
		}
		time.Sleep(time.Millisecond)
	}
	r.m.Record(true)
	start := time.Now()
	r.fw.Stop("co-op turned off by the user")
	if d := time.Since(start); d >= TickEvery {
		t.Errorf("Stop took %v, more than a tick", d)
	}
	n := len(r.m.Writes())
	deadline = time.Now().Add(5 * time.Second)
	for offErrs.Load() < 3 {
		if time.Now().After(deadline) {
			t.Fatal("the loop never saw co-op off")
		}
		time.Sleep(time.Millisecond)
	}
	cancel()
	wg.Wait()
	if got := len(r.m.Writes()); got != n {
		t.Fatalf("%d writes after Stop returned", got-n)
	}
	if off, why := r.fw.Disabled(); !off || why != "co-op turned off by the user" {
		t.Fatalf("Disabled = %v, %q", off, why)
	}
}

func TestFrameWriterDryRun(t *testing.T) {
	m := mem.NewFakeMem()
	m.Record(true)
	g := fakegame.New(m)
	g.Frames(10)
	ws := []Write{plannedOR(addr.OffOcean, 1, addr.TableSea)}
	dry, err := NewFrameWriter(WriterConfig{Mem: m, DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	for name, w := range map[string]*FrameWriter{"DryRun": dry, "M2's literal": {Mem: m, Guard: NewGuard()}} {
		t.Run(name, func(t *testing.T) {
			if !w.DryRun() || w.WriteGuard() != nil {
				t.Fatal("not a dry run")
			}
			s, err := snap.Read(m)
			if err != nil {
				t.Fatal(err)
			}
			w.Observe(s, gatesOpen, true)
			if _, err := w.ApplyContext(context.Background(), ws); !errors.Is(err, ErrDryRun) {
				t.Fatalf("Apply = %v, want ErrDryRun", err)
			}
			w.Stop("off")
			if off, _ := w.Disabled(); off {
				t.Fatal("a dry run turned off")
			}
		})
	}
	if n := len(m.Writes()); n != 0 {
		t.Fatalf("a dry run wrote %d times", n)
	}
	if _, err := NewFrameWriter(WriterConfig{Mem: m}); err == nil {
		t.Error("a writing FrameWriter without a WriteGuard")
	}
	if _, err := NewFrameWriter(WriterConfig{Guard: NewWriteGuard(GuardConfig{})}); err == nil {
		t.Error("a FrameWriter without memory")
	}
}

// TestFrameWriterTransitionStorm walks in and out of stages and rooms
// while the planner's view keeps asking for bits, re-planned every tick
// for the table loaded, as the reconciler does. Every write is a byte
// inside M3's allowlist, none clears a bit, every gained bit stays, and
// nothing is written on a tick a gate is closed.
func TestFrameWriterTransitionStorm(t *testing.T) {
	r := newWriterRig(t, GuardConfig{Policy: world.FullPolicy()})
	r.m.Record(true)
	view := world.Image{}
	for _, tb := range []int{addr.TableSea, addr.TableSea2, addr.TableDRC} {
		m := addr.OffMemBit(tb)
		view[m+addr.MemBitOffTbox+3] = 0x0F
		view[m+addr.MemBitOffSwitch+3] = 0x21
		view[m+addr.MemBitOffVisited+7] = 0x03
	}
	view[addr.OffOcean+3] = 0x40
	stages := []fakegame.Stage{wDRC, wOutset, {Name: "Asoko", Table: addr.TableSea2}, wDRC}
	var results []Result
	writesWhileClosed, closedTicks, written := 0, 0, 0
	for i := range 60 {
		switch i % 10 {
		case 3:
			if err := r.g.SetNextStage(stages[(i/10)%len(stages)]); err != nil {
				t.Fatal(err)
			}
		case 5:
			if err := r.g.ChangeStage(); err != nil {
				t.Fatal(err)
			}
		case 7:
			if err := r.g.EnterRoom(i % 3); err != nil {
				t.Fatal(err)
			}
		}
		r.tick()
		s, err := snap.Read(r.m)
		if err != nil {
			t.Fatal(err)
		}
		cur := int(s.Table)
		var ws []Write
		img := s.Image()
		for off := range view {
			if miss := view[off] &^ img[off]; miss != 0 {
				ws = append(ws, plannedOR(off, miss, cur))
			}
		}
		before := len(r.m.Writes())
		res, err := r.apply(ws...)
		if err != nil && !errors.Is(err, ErrClosed) {
			t.Fatalf("tick %d: %v", i, err)
		}
		if !r.st.CanWrite() {
			closedTicks++
			if len(r.m.Writes()) != before {
				writesWhileClosed++
			}
		}
		written += len(res.Wrote)
		results = append(results, res)
	}
	r.audit(results...)
	if writesWhileClosed != 0 {
		t.Fatalf("%d ticks wrote with a gate closed", writesWhileClosed)
	}
	if closedTicks < 20 || written == 0 {
		t.Fatalf("a storm with %d closed ticks of 60 and %d bytes written proves little", closedTicks, written)
	}
	for range gate.StableTicks + 2 {
		r.tick()
	}
	s, _ := snap.Read(r.m)
	img := s.Image()
	for off, b := range view {
		if t3, _, ok := memBitTable(off); ok && t3 != addr.TableSea && t3 != addr.TableSea2 && t3 != addr.TableDRC {
			continue
		}
		if img[off]&b != b {
			t.Errorf("%s = 0x%02X lacks 0x%02X after the storm", DefaultLabel(off, -1), img[off], b)
		}
	}
	if r.brk.Off() || r.fw.Stats().Failed != 0 {
		t.Fatalf("off %v (%s), stats %+v", r.brk.Off(), r.brk.Why(), r.fw.Stats())
	}
}

// loadState copies the game state of src's RAM over dst's, as loading a
// Dolphin save state does: the room table, the save, the play struct,
// mStayNo, dMenu_pause and the mailbox (0x803B0000-0x80420000), and the
// fake's Link actor and STAG chunk.
func loadState(t *testing.T, dst, src *mem.FakeMem) {
	t.Helper()
	for _, r := range [][2]uint32{{0x803B0000, 0x80420000}, {fakegame.PlayerActor, fakegame.StagChunk + 0x1000}} {
		b, err := src.Read(r[0], int(r[1]-r[0]))
		if err != nil {
			t.Fatal(err)
		}
		if err := dst.Store(r[0], b); err != nil {
			t.Fatal(err)
		}
	}
}

// soloState is another file, never bound, standing in Outset as the rig's
// does: what a save state of the user's solo play holds.
func soloState(t *testing.T, frames int) *mem.FakeMem {
	t.Helper()
	m := mem.NewFakeMem()
	g := fakegame.New(m)
	if err := g.NewGame(1, "Solo"); err != nil {
		t.Fatal(err)
	}
	if err := g.LoadStage(wOutset); err != nil {
		t.Fatal(err)
	}
	g.Frames(frames)
	return m
}

// TestFrameWriterCatchesASaveStateOfAnotherFile loads a save state of
// another file in the same stage and table between the tick's snapshot
// and the beat: before the writer's first heartbeat read, or while it
// polls, with the heartbeat below the snapshot's, far ahead of it, or
// right where the game would be. Whichever check catches it (the
// heartbeat followed from the snapshot's, or the file read again after
// the beat), nothing is written into the other file.
func TestFrameWriterCatchesASaveStateOfAnotherFile(t *testing.T) {
	const (
		before = 1 // the writer's first heartbeat read
		during = 2 // its first poll
	)
	cases := []struct {
		name   string
		at     int
		frames int // the other game's frames: its heartbeat
		near   int // >= 0: the heartbeat put this far past the snapshot's instead
		why    string
	}{
		{"before the first read, the heartbeat behind", before, 1, -1, "back to"},
		{"before the first read, the heartbeat far ahead", before, 100000, -1, "frames past the snapshot"},
		{"before the first read, the heartbeat where the game is", before, 1, 0, "another file is loaded"},
		{"while polling, the heartbeat behind", during, 1, -1, "went backwards"},
		{"while polling, the heartbeat far ahead", during, 100000, -1, "jumped"},
		{"while polling, the heartbeat a frame on", during, 1, 1, "another file is loaded"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := newWriterRig(t, GuardConfig{})
			solo := soloState(t, c.frames)
			chest := addr.OffMemBit(addr.TableSea) + addr.MemBitOffTbox + 3
			drc := addr.OffMemBit(addr.TableDRC) + addr.MemBitOffTbox
			ws := []Write{plannedOR(chest, 0x08, addr.TableSea), plannedOR(drc, 0x01, addr.TableSea)}
			r.tick()
			hb, err := mem.ReadU32(r.m, addr.SpawnTrigger)
			if err != nil {
				t.Fatal(err)
			}
			r.m.Record(true)
			r.hm.beatOn(c.at, func() {
				loadState(t, r.m, solo)
				if c.near >= 0 {
					r.store32(addr.SpawnTrigger, hb+uint32(c.near))
				}
				if c.at == before {
					r.hm.beatOn(1, r.g.Frame) // the loaded game beats on the poll
				}
			})
			res, err := r.fw.ApplyContext(context.Background(), ws)
			r.hm.beatOn(0, nil)
			if !errors.Is(err, ErrAborted) || !strings.Contains(err.Error(), c.why) {
				t.Fatalf("Apply = %v, want ErrAborted saying %q", err, c.why)
			}
			if n := len(r.m.Writes()); n != 0 || len(res.Wrote) != 0 {
				t.Fatalf("%d writes into the solo file: %+v", n, r.m.Writes())
			}
			if v := r.byteAt(liveCopy(chest)); v != 0 {
				t.Fatalf("the solo file's live chest byte is 0x%02X", v)
			}
			if st := r.fw.Stats(); st.Aborted != 1 {
				t.Errorf("stats %+v", st)
			}
		})
	}
}

func (r *wrig) store32(a, v uint32) {
	r.t.Helper()
	if err := r.m.Store(a, []byte{byte(v >> 24), byte(v >> 16), byte(v >> 8), byte(v)}); err != nil {
		r.t.Fatal(err)
	}
}

// TestFrameWriterVerifySkipsAStageChange writes a batch, then the game
// changes stage (and table) before the next snapshot, or keeps the stage
// and loads another table (a stage of another table under the same name):
// getSave rewrote the live copy, so the batch is not judged, and no
// verify failure reaches the Breaker. Another file loaded since (a save
// state) is not judged either.
func TestFrameWriterVerifySkipsAStageChange(t *testing.T) {
	cases := []struct {
		name string
		move func(r *wrig)
	}{
		{"another stage and table", func(r *wrig) { r.load(wDRC) }},
		{"the same stage, another table", func(r *wrig) {
			s := wOutset
			s.Table = addr.TableSea2
			r.load(s)
		}},
		{"another file in the same stage", func(r *wrig) {
			solo := soloState(t, 0)
			hb, err := mem.ReadU32(r.m, addr.SpawnTrigger)
			if err != nil {
				t.Fatal(err)
			}
			loadState(t, r.m, solo)
			r.store32(addr.SpawnTrigger, hb) // no regression to excuse it
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := newWriterRig(t, GuardConfig{})
			chest := addr.OffMemBit(addr.TableSea) + addr.MemBitOffTbox + 3
			res, err := r.apply(plannedOR(chest, 0x05, addr.TableSea))
			if err != nil || len(res.Wrote) != 2 {
				t.Fatalf("Apply = %v, %+v: want the live and saved bytes", err, res)
			}
			c.move(r)
			rep := r.tick()
			if rep.Unverified != len(res.Wrote) || rep.Failed != 0 || rep.Verified != 0 {
				t.Fatalf("verify %+v: want the %d bytes unjudged", rep, len(res.Wrote))
			}
			if st := r.fw.Stats(); st.Failed != 0 || st.Unverified != len(res.Wrote) {
				t.Errorf("stats %+v", st)
			}
			if n := faultCount(r.brk); r.brk.Off() || n != 0 {
				t.Errorf("the Breaker saw a fault: off %v, %d faults (%s)", r.brk.Off(), n, r.brk.Why())
			}
		})
	}
}

// faultCount is the faults b holds in its window.
func faultCount(b *Breaker) int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.faults)
}
