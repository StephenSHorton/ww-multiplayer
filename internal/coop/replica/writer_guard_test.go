package replica

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/StephenSHorton/ww-multiplayer/internal/coop/addr"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/bind"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/class"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/gate"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/snap"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/world"
)

// gatesOpen is a tick with every gate open.
var gatesOpen = gate.Status{Stable: true, Idle: true, Heartbeat: true}

// logLines collects log lines; safe for concurrent use.
type logLines struct {
	mu sync.Mutex
	l  []string
}

func (l *logLines) logf(format string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.l = append(l.l, fmt.Sprintf(format, args...))
}

func (l *logLines) count(sub string) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	n := 0
	for _, s := range l.l {
		if strings.Contains(s, sub) {
			n++
		}
	}
	return n
}

func (l *logLines) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return strings.Join(l.l, "\n")
}

// switchImageByte is the image byte and mask of memory switch no of table t.
func switchImageByte(t, no int) (int, byte) {
	_, b, err := addr.SwitchBit(no)
	if err != nil {
		panic(err)
	}
	return addr.OffMemBit(t) + b.Off, b.Mask
}

// switch5Policy is the default policy with switch 0x05 of the sea and DRC
// tables granted, as the M1 table would mark a latching one.
func switch5Policy(t *testing.T) *world.Policy {
	t.Helper()
	p := world.DefaultPolicy()
	for _, tb := range []int{addr.TableSea, addr.TableDRC} {
		if err := p.GrantSwitch(tb, 0x05); err != nil {
			t.Fatal(err)
		}
	}
	return p
}

func imageAddr(off int) uint32 { return addr.SaveImage + uint32(off) }

func zoneByteAddr(z int, off uint32) uint32 {
	a, err := addr.ZoneAddr(z)
	if err != nil {
		panic(err)
	}
	return a + off
}

func TestWriteGuardM3Check(t *testing.T) {
	sea := addr.OffMemBit(addr.TableSea)
	drc := addr.OffMemBit(addr.TableDRC)
	sw5, m5 := switchImageByte(addr.TableSea, 0x05)
	sw10, m10 := switchImageByte(addr.TableSea, 0x10)
	liveSw5 := addr.LiveMemBit + uint32(sw5-sea)

	cases := []struct {
		name     string
		a        uint32
		old, val byte
		want     Refusal // 0: allowed
	}{
		{"a chest in the loaded table's saved slot", imageAddr(sea + addr.MemBitOffTbox + 3), 0, 1, 0},
		{"a chest in the live memBit", addr.LiveMemBit + addr.MemBitOffTbox + 3, 0x10, 0x11, 0},
		{"a chest in a cold table", imageAddr(drc + addr.MemBitOffTbox), 0, 0x80, 0},
		{"a pickup", imageAddr(drc + addr.MemBitOffItem + 2), 0, 4, 0},
		{"a visited room", imageAddr(drc + addr.MemBitOffVisited + 7), 0, 1, 0},
		{"the live visited rooms", addr.LiveMemBit + addr.MemBitOffVisited, 0, 0xFF, 0},
		{"the map, compass and boss key", imageAddr(drc + addr.MemBitOffDungeonItem), 0, 7, 0},
		{"a synced memory switch, saved", imageAddr(sw5), 0, m5, 0},
		{"a synced memory switch, live", liveSw5, 0, m5, 0},
		{"a synced switch in a cold table", imageAddr(addr.OffMemBit(addr.TableDRC) + sw5 - sea), 0, m5, 0},
		{"a salvage bit", imageAddr(addr.OffOcean + 0x10), 0, 2, 0},
		{"a chart owned", imageAddr(addr.OffMapGet + 3), 1, 3, 0},
		{"a chart salvaged", imageAddr(addr.OffMapComplete), 0, 0x40, 0},
		{"a sea-map bit", imageAddr(addr.OffFmapBits + 9), 0, 1, 0},
		{"a Triforce chart", imageAddr(addr.OffMapTriforce), 0, 0x80, 0},

		{"a clear", imageAddr(sea + addr.MemBitOffTbox + 3), 0x03, 0x01, RefuseClear},
		{"a clear in the live memBit", addr.LiveMemBit + addr.MemBitOffVisited, 0xFF, 0xFE, RefuseClear},
		{"a clear and a set", imageAddr(addr.OffOcean), 0x01, 0x02, RefuseClear},
		{"dungeon item bit 6", imageAddr(drc + addr.MemBitOffDungeonItem), 0, 0x40, RefuseMask},
		{"an unsynced switch beside a synced one", imageAddr(sw5), 0, m5 << 1, RefuseMask},
		{"an unsynced switch", imageAddr(sw10), 0, m10, RefusePolicy},
		{"an unsynced live switch", addr.LiveMemBit + uint32(sw10-sea), 0, m10, RefusePolicy},

		{"PER_PLAYER mLife", imageAddr(addr.OffLife + 1), 0x0C, 0x0D, RefuseTarget},
		{"PER_PLAYER mRupee", imageAddr(addr.OffRupee), 0, 1, RefuseTarget},
		{"PER_PLAYER mItemRecord", imageAddr(addr.OffItemRecord), 0, 1, RefuseTarget},
		{"PER_PLAYER mPlayerName", addr.PlayerName, 0, 'L', RefuseTarget},
		{"NEVER status_a padding", imageAddr(addr.OffStatusAPad), 0, 1, RefuseTarget},
		{"NEVER memBit padding", imageAddr(drc + addr.MemBitOffPad), 0, 1, RefuseTarget},
		{"NEVER live memBit padding", addr.LiveMemBit + addr.MemBitOffPad, 0, 1, RefuseTarget},
		{"NEVER mMap row 0", imageAddr(addr.OffMapRow0), 0, 1, RefuseTarget},
		{"DERIVED mKeyNum", imageAddr(drc + addr.MemBitOffKeyNum), 0, 1, RefuseTarget},
		{"DERIVED live mKeyNum", addr.LiveKeyNum, 0, 1, RefuseTarget},
		{"DERIVED mSelectEquip", imageAddr(addr.OffSelectEquip), 0, 0x38, RefuseTarget},
		{"DERIVED mItems", imageAddr(addr.OffItems), 0xFF, 0xFF &^ 1, RefuseTarget},
		{"DERIVED mMaxLife", imageAddr(addr.OffMaxLife + 1), 0x0C, 0x10, RefuseTarget},
		{"TAG", addr.Reserve, 0, 'W', RefuseTarget},
		{"TAG past the tag", addr.Reserve + bind.TagSize, 0, 1, RefuseTarget},
		{"M4 inventory", imageAddr(addr.OffGetItem), 0, 1, RefuseTarget},
		{"M4 story bit", imageAddr(addr.OffEvent + 3), 0, 1, RefuseTarget},
		{"M4 capacity", imageAddr(addr.OffWalletSize), 0, 1, RefuseTarget},
		{"M4 pending counter", addr.ItemKeyNumCount + 1, 0, 1, RefuseTarget},
		{"M4 play.mSelectItem", addr.PlaySelectItem, 0, 1, RefuseTarget},
		{"M5 dan switches", addr.DanSwitch, 0, 1, RefuseTarget},
		{"M5 zone switches", zoneByteAddr(2, addr.ZoneOffSwitch), 0, 1, RefuseTarget},
		{"M6 dead actors", zoneByteAddr(2, addr.ZoneOffActor), 0, 1, RefuseTarget},
		{"mDan.mStageNo", addr.DanStageNo, 0, 1, RefuseTarget},
		{"the heartbeat", addr.SpawnTrigger, 0, 1, RefuseTarget},
		{"below MEM1", 0x7FFFFFFF, 0, 1, RefuseTarget},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var log logLines
			wg := NewWriteGuard(GuardConfig{Policy: switch5Policy(t), Logf: log.logf})
			wg.SetTick(Tick{Gates: gatesOpen, Armed: true, Table: addr.TableSea})
			err := wg.Check(c.a, c.old, c.val)
			if c.want == 0 {
				if err != nil {
					t.Fatalf("Check = %v, want nil", err)
				}
				return
			}
			var re *RefusalError
			if !errors.As(err, &re) || re.Reason != c.want || !errors.Is(err, ErrGuard) {
				t.Fatalf("Check = %v, want a %s refusal", err, c.want)
			}
			if st := wg.Stats(); st.Refused != 1 || st.ByReason[c.want] != 1 || st.Checked != 1 {
				t.Errorf("stats %+v", st)
			}
			if log.count("(refusal ") != 1 {
				t.Errorf("log:\n%s", &log)
			}
		})
	}
}

func TestWriteGuardGates(t *testing.T) {
	chest := imageAddr(addr.OffMemBit(addr.TableDRC) + addr.MemBitOffTbox)
	live := addr.LiveMemBit + addr.MemBitOffTbox
	settling := gate.Status{Heartbeat: true, Idle: true, StableWhy: "settling"}
	stalled := gate.Status{Stable: true, Idle: true, HeartbeatWhy: "heartbeat stalled"}
	busy := gate.Status{Stable: true, Heartbeat: true, IdleWhy: "an event is running"}
	cases := []struct {
		name  string
		tick  *Tick // nil: no SetTick
		close string
		a     uint32
		want  Refusal
	}{
		{"before any tick", nil, "", chest, RefuseGate},
		{"STABLE closed", &Tick{Gates: settling, Armed: true}, "", chest, RefuseGate},
		{"HEARTBEAT closed", &Tick{Gates: stalled, Armed: true}, "", chest, RefuseGate},
		{"gates closed without a reason", &Tick{Gates: gate.Status{Idle: true}, Armed: true}, "", chest, RefuseGate},
		{"not armed", &Tick{Gates: gatesOpen}, "", chest, RefuseGate},
		{"closed after the snapshot", &Tick{Gates: gatesOpen, Armed: true}, "a stage change started", chest, RefuseGate},
		{"IDLE is not needed for stage bits", &Tick{Gates: busy, Armed: true}, "", chest, 0},
		{"the ocean needs IDLE", &Tick{Gates: busy, Armed: true}, "", imageAddr(addr.OffOcean + 4), RefuseGate},
		{"the ocean when IDLE", &Tick{Gates: gatesOpen, Armed: true}, "", imageAddr(addr.OffOcean + 4), 0},
		{"the live memBit with no table loaded", &Tick{Gates: gatesOpen, Armed: true, Table: -1}, "", live, RefuseTarget},
		{"the live memBit of table 3", &Tick{Gates: gatesOpen, Armed: true, Table: addr.TableDRC}, "", live, 0},
		{"a non-target before any tick is a target refusal", nil, "", imageAddr(addr.OffLife), RefuseTarget},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			wg := NewWriteGuard(GuardConfig{})
			if c.tick != nil {
				wg.SetTick(*c.tick)
			}
			if c.close != "" {
				wg.Close(c.close)
			}
			err := wg.Check(c.a, 0, 1)
			var re *RefusalError
			switch {
			case c.want == 0 && err != nil:
				t.Fatalf("Check = %v, want nil", err)
			case c.want != 0 && (!errors.As(err, &re) || re.Reason != c.want):
				t.Fatalf("Check = %v, want a %s refusal", err, c.want)
			case c.close != "" && !strings.Contains(err.Error(), c.close):
				t.Fatalf("Check = %v, want the Close reason", err)
			}
		})
	}
}

// settledGates runs the gates over a settled history of s, the heartbeat
// moving each tick.
func settledGates(s snap.Snapshot) gate.Status {
	var hist []*snap.Snapshot
	for i := range gate.StableTicks + 2 {
		c := s
		c.Heartbeat = uint32(i + 1)
		hist = append(hist, &c)
	}
	return gate.Evaluate(hist)
}

// TestWriteGuardLaterTargets checks the gates of the targets later
// milestones open, through a guard of every target.
func TestWriteGuardLaterTargets(t *testing.T) {
	inGame := snap.Snapshot{PlayerPtr: 0x80A00000, StageName: "sea", Table: addr.TableSea}
	idle := settledGates(inGame)
	busy := inGame
	busy.EvtMode = 1
	keyPending := inGame
	keyPending.KeyNumCount = 2
	transition := idle
	transition.OnTransition = true
	transition.Stable = false
	if !idle.CanWriteIdle() || settledGates(busy).Idle || settledGates(keyPending).CanWriteCounter(gate.KeyNum) {
		t.Fatal("bad gate fixtures")
	}
	metKorl := addr.EventByte(0x0F80) // MET_KORL (d_save_event_flag.inc:111)
	story := imageAddr(addr.OffEvent + metKorl.Off)
	cases := []struct {
		name     string
		gates    gate.Status
		armed    bool
		a        uint32
		old, val byte
		ok       bool
	}{
		{"inventory when idle", idle, true, imageAddr(addr.OffGetItem), 0, 1, true},
		{"inventory during an event", settledGates(busy), true, imageAddr(addr.OffGetItem), 0, 1, false},
		{"inventory unarmed", idle, false, imageAddr(addr.OffGetItem), 0, 1, false},
		{"a capacity rises", idle, true, imageAddr(addr.OffArrowMax), 30, 60, true},
		{"a capacity falls", idle, true, imageAddr(addr.OffArrowMax), 60, 30, false},
		{"a derived slot fills", idle, true, imageAddr(addr.OffItems), 0xFF, 0x20, true},
		{"the key counter at 0", idle, true, addr.ItemKeyNumCount + 1, 0, 1, true},
		{"the key counter still pending", settledGates(keyPending), true, addr.ItemKeyNumCount + 1, 0, 1, false},
		{"the max-life counter while the key one is pending", settledGates(keyPending), true, addr.ItemMaxLifeCount + 1, 0, 4, true},
		{"a story bit on a settled tick", idle, true, story, 0, metKorl.Mask, false},
		{"a story bit as a stage change starts", transition, true, story, 0, metKorl.Mask, true},
		{"dan switches when idle", idle, true, addr.DanSwitch + 7, 0, 1, true},
		{"a dan switch clear", idle, true, addr.DanSwitch + 7, 1, 0, false},
		{"dead actors when idle", idle, true, zoneByteAddr(31, addr.ZoneOffActor+0x3F), 0, 0x80, true},
		{"the tag, unarmed, when idle", idle, false, addr.Reserve, 0, 'W', true},
		{"the tag during an event", settledGates(busy), false, addr.Reserve, 0, 'W', false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			wg := NewWriteGuard(GuardConfig{Targets: AllTargets, Policy: world.FullPolicy()})
			wg.SetTick(Tick{Gates: c.gates, Armed: c.armed, Table: addr.TableSea})
			err := wg.Check(c.a, c.old, c.val)
			if (err == nil) != c.ok {
				t.Fatalf("Check = %v, want ok %v", err, c.ok)
			}
			if !c.ok && !errors.Is(err, ErrGuard) {
				t.Fatalf("Check = %v, not ErrGuard", err)
			}
		})
	}
}

// TestM3GuardHasNoForbiddenByte walks the whole image and live memBit:
// M3's allowlist holds only stage and ocean bits, within the class masks.
func TestM3GuardHasNoForbiddenByte(t *testing.T) {
	g := NewGuardFor(M3Targets)
	n := 0
	for off := range addr.SaveImageSize {
		m := g.Mask(imageAddr(off))
		r := class.Of(off)
		switch {
		case m == 0:
			continue
		case r.Class != class.OR && r.Class != class.ORMasked:
			t.Fatalf("%s (%s) is in M3's allowlist", DefaultLabel(off, -1), r.Class)
		case m&^r.Mask != 0:
			t.Fatalf("%s: mask 0x%02X is wider than its class mask 0x%02X", DefaultLabel(off, -1), m, r.Mask)
		}
		if c := CategoryOf(off); c != CatStage && c != CatSwitch && c != CatOcean {
			t.Fatalf("%s is %s, not an M3 category", DefaultLabel(off, -1), c)
		}
		n++
	}
	// 16 tables x (tbox 4 + switches 16 + item 4 + visited 8 + dungeon
	// item 1), mOcean, the three chart rows, the sea map, the Triforce
	// charts.
	want := addr.TableCount*(4+16+4+8+1) + addr.OceanSize + 3*16 + (addr.OffMap71 - addr.OffFmapBits) + 1
	if n != want {
		t.Errorf("%d image bytes allowed, want %d", n, want)
	}
	for rel := range addr.MemBitSize {
		m := g.Mask(addr.LiveMemBit + uint32(rel))
		if r := class.Of(addr.OffMemBit(0) + rel); m != 0 && r.Class != class.OR && r.Class != class.ORMasked {
			t.Fatalf("live memBit+0x%02X (%s) is in M3's allowlist", rel, r.Class)
		}
	}
	if g.Targets() != M3Targets || NewGuard().Targets() != AllTargets {
		t.Errorf("targets %s and %s", g.Targets(), NewGuard().Targets())
	}
	if got := M3Targets.String(); got != "stage|ocean" {
		t.Errorf("M3Targets = %q", got)
	}
}

func TestWriteGuardOffAndBreaker(t *testing.T) {
	var log logLines
	brk := NewBreaker(BreakerConfig{Logf: log.logf})
	wg := NewWriteGuard(GuardConfig{Breaker: brk, Logf: log.logf})
	wg.SetTick(Tick{Gates: gatesOpen, Armed: true, Table: addr.TableSea})
	if wg.Breaker() != brk {
		t.Fatal("the guard does not use the given breaker")
	}
	life := imageAddr(addr.OffLife)
	for i := range MaxFaultsPerMinute {
		if err := wg.Check(life, 0, 1); err == nil {
			t.Fatal("mLife allowed")
		}
		if brk.Off() {
			t.Fatalf("off after %d refusals: %s", i+1, brk.Why())
		}
	}
	if err := wg.Check(life, 0, 1); errors.Is(err, ErrOff) || !brk.Off() {
		t.Fatalf("refusal %d: %v; off %v", MaxFaultsPerMinute+1, err, brk.Off())
	}
	if !strings.Contains(brk.Why(), "WriteGuard refusals") {
		t.Errorf("why: %s", brk.Why())
	}
	// Off: even a good write is refused, as co-op off, and it is no new
	// fault.
	chest := imageAddr(addr.OffMemBit(addr.TableDRC))
	err := wg.Check(chest, 0, 1)
	var re *RefusalError
	if !errors.Is(err, ErrOff) || !errors.As(err, &re) || re.Reason != RefuseOff {
		t.Fatalf("Check while off = %v", err)
	}
	if st := wg.Stats(); st.ByReason[RefuseOff] != 1 || st.ByReason[RefuseTarget] != MaxFaultsPerMinute+1 {
		t.Errorf("stats %+v", st)
	}
	if log.count("(refusal ") != MaxFaultsPerMinute+1 || log.count("turned itself off") != 1 {
		t.Errorf("log:\n%s", &log)
	}
	brk.Reset()
	if err := wg.Check(chest, 0, 1); err != nil {
		t.Fatalf("after Reset: %v", err)
	}
}

func TestWriteGuardLogIsRateLimited(t *testing.T) {
	var log logLines
	brk := NewBreaker(BreakerConfig{MaxFaults: 1 << 30})
	wg := NewWriteGuard(GuardConfig{Breaker: brk, Logf: log.logf})
	for range 250 {
		wg.Check(imageAddr(addr.OffLife), 0, 1)
	}
	if n := log.count("(refusal "); n != 16+2 {
		t.Errorf("%d logLines logged for 250 refusals, want 18", n)
	}
	if wg.Stats().Refused != 250 {
		t.Errorf("stats %+v", wg.Stats())
	}
}

func TestWriteGuardCheckWrite(t *testing.T) {
	chest := addr.OffMemBit(addr.TableSea) + addr.MemBitOffTbox + 3
	cold := addr.OffMemBit(addr.TableDRC) + addr.MemBitOffTbox
	good := plannedOR(chest, 1, addr.TableSea)
	cases := []struct {
		name string
		w    Write
		ok   bool
	}{
		{"the loaded table, both copies", good, true},
		{"a cold table, saved only", plannedOR(cold, 1, addr.TableSea), true},
		{"the ocean", plannedOR(addr.OffOcean, 1, addr.TableSea), true},
		{"planned with DRC loaded", plannedOR(cold, 1, addr.TableDRC), false},
		{"the loaded table without its live copy", plannedOR(chest, 1, addr.TableDRC), false},
		{"a wrong saved address", func() Write { w := good; w.Saved++; return w }(), false},
		{"a wrong live address", func() Write { w := good; w.Live++; return w }(), false},
		{"a live address outside the memBit", func() Write { w := plannedOR(addr.OffOcean, 1, 0); w.Live = addr.LiveMemBit; return w }(), false},
		{"an offset outside the image", Write{Off: addr.SaveImageSize, Bits: 1, Saved: imageAddr(addr.SaveImageSize)}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			wg := NewWriteGuard(GuardConfig{})
			wg.SetTick(Tick{Gates: gatesOpen, Armed: true, Table: addr.TableSea})
			err := wg.CheckWrite(c.w)
			if (err == nil) != c.ok {
				t.Fatalf("CheckWrite = %v, want ok %v", err, c.ok)
			}
			if !c.ok {
				var re *RefusalError
				if !errors.As(err, &re) || re.Reason != RefuseTarget || wg.Stats().Refused != 1 {
					t.Fatalf("CheckWrite = %v; stats %+v", err, wg.Stats())
				}
			}
		})
	}
}

func TestGuardStaticCheckRefusesClears(t *testing.T) {
	g := NewGuard()
	wallet := imageAddr(addr.OffWalletSize)
	chest := imageAddr(addr.OffMemBit(addr.TableDRC))
	cases := []struct {
		name     string
		a        uint32
		old, val []byte
		want     Refusal
	}{
		{"an OR byte gains", chest, []byte{1}, []byte{3}, 0},
		{"an OR byte loses", chest, []byte{3}, []byte{1}, RefuseClear},
		{"a MAX byte rises", wallet, []byte{1}, []byte{2}, 0},
		{"a MAX byte falls", wallet, []byte{2}, []byte{1}, RefuseClear},
		{"a DERIVED byte may fall (keys spent)", imageAddr(addr.OffMemBit(addr.TableDRC) + addr.MemBitOffKeyNum), []byte{2}, []byte{1}, 0},
		{"the tag may be zeroed (a scrub)", addr.Reserve, []byte{'W'}, []byte{0}, 0},
		{"two bytes, the second a clear", chest, []byte{0, 1}, []byte{1, 0}, RefuseClear},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := g.Check(c.a, c.old, c.val)
			var re *RefusalError
			switch {
			case c.want == 0 && err != nil:
				t.Fatalf("Check = %v", err)
			case c.want != 0 && (!errors.As(err, &re) || re.Reason != c.want || !errors.Is(err, ErrGuard)):
				t.Fatalf("Check = %v, want %s", err, c.want)
			}
		})
	}
}

// FuzzWriteGuardCheck checks Check's contract on any byte write: a write
// it allows is an M3 bit gain inside the policy, with the gates open.
func FuzzWriteGuardCheck(f *testing.F) {
	f.Add(uint32(0x803C4F88), byte(0), byte(1), int8(0), byte(7))
	f.Add(uint32(0x803C5380), byte(1), byte(3), int8(3), byte(7))
	f.Add(uint32(0x803C4C0A), byte(0), byte(0), int8(0), byte(7))
	f.Add(uint32(0x803C5387), byte(0), byte(0x20), int8(0), byte(7))
	f.Add(uint32(0x803C4CE0), byte(2), byte(0), int8(-1), byte(2))
	f.Add(uint32(0x803C4CE0), byte(0), byte(0), int8(-1), byte(0x4C)) // closed gates without a reason
	f.Fuzz(func(t *testing.T, a uint32, old, val byte, table int8, flags byte) {
		pol := world.DefaultPolicy()
		pol.GrantSwitch(addr.TableSea, 0x05)
		wg := NewWriteGuard(GuardConfig{Policy: pol, Breaker: NewBreaker(BreakerConfig{MaxFaults: 1 << 30})})
		st := gate.Status{Stable: flags&1 != 0, Heartbeat: flags&2 != 0, Idle: true}
		wg.SetTick(Tick{Gates: st, Armed: flags&4 != 0, Table: table})
		err := wg.Check(a, old, val)
		if err == nil {
			if !st.CanWrite() || flags&4 == 0 {
				t.Fatalf("allowed with the gates closed: %+v", st)
			}
			if old&^val != 0 {
				t.Fatalf("allowed a clear 0x%02X -> 0x%02X", old, val)
			}
			off, ok := -1, false
			switch {
			case a >= addr.SaveImage && a < addr.SaveImage+addr.SaveImageSize:
				off, ok = int(a-addr.SaveImage), true
			case a >= addr.LiveMemBit && a < addr.LiveMemBit+addr.MemBitSize:
				ok = addr.ValidTable(int(table))
				if ok {
					off = addr.OffMemBit(int(table)) + int(a-addr.LiveMemBit)
				}
			}
			if !ok {
				t.Fatalf("allowed 0x%08X, outside the image and the live memBit", a)
			}
			if c := CategoryOf(off); c != CatStage && c != CatSwitch && c != CatOcean {
				t.Fatalf("allowed %s (%s)", DefaultLabel(off, -1), c)
			}
			if gain := val &^ old; gain&^(world.SharedMask(off)&pol[off]) != 0 {
				t.Fatalf("allowed bits 0x%02X of %s outside the policy", gain, DefaultLabel(off, -1))
			}
			return
		}
		if !errors.Is(err, ErrGuard) {
			t.Fatalf("refusal %v is not ErrGuard", err)
		}
	})
}

// TestWriteGuardBossBits: while a boss stage is loaded, the live memBit's
// boss bits may not change; its other dungeon items, the saved slots and
// the live byte outside a boss stage may.
func TestWriteGuardBossBits(t *testing.T) {
	live := addr.LiveMemBit + addr.MemBitOffDungeonItem
	saved := imageAddr(addr.OffMemBit(addr.TableDRC) + addr.MemBitOffDungeonItem)
	cases := []struct {
		name     string
		boss     bool
		a        uint32
		old, val byte
		want     Refusal
	}{
		{"the boss defeated, live, in a boss stage", true, live, 0x03, 0x0B, RefuseGate},
		{"its demo seen, live, in a boss stage", true, live, 0, 0x20, RefuseGate},
		{"the map, live, in a boss stage", true, live, 0, 0x01, 0},
		{"the boss bits already set", true, live, 0x08, 0x09, 0},
		{"the saved slot in a boss stage", true, saved, 0, 0x08, 0},
		{"the boss defeated, live, elsewhere", false, live, 0, 0x08, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			wg := NewWriteGuard(GuardConfig{})
			wg.SetTick(Tick{Gates: gatesOpen, Armed: true, Table: addr.TableDRC, Boss: c.boss})
			err := wg.Check(c.a, c.old, c.val)
			var re *RefusalError
			switch {
			case c.want == 0 && err != nil:
				t.Fatalf("Check = %v, want nil", err)
			case c.want != 0 && (!errors.As(err, &re) || re.Reason != c.want || !strings.Contains(err.Error(), "boss")):
				t.Fatalf("Check = %v, want a %s refusal naming the boss bits", err, c.want)
			}
		})
	}
}
