package enemysync

import (
	"strings"
	"testing"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/fakegame"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mem"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/stage"
)

// newD2 stands in for M_NewD2 room 7 (any save table serves the fake).
var newD2 = fakegame.Stage{Name: "M_NewD2", Table: addr.TableDRC, Room: 7}

// keeseRoom is M_NewD2 room 7 as the release rig found it: two keyed
// Keese (setIDs 0 and 1, params 0x80FF8201, flying) and no Moblin,
// Bokoblin or ChuChu; plus one unkeyed fly-type Keese, which only
// "ki+u" takes.
func keeseRoom(t *testing.T) (*fakegame.FakeGame, *mem.FakeMem) {
	t.Helper()
	m := mem.NewFakeMem()
	g := fakegame.New(m)
	if err := g.LoadStage(newD2); err != nil {
		t.Fatal(err)
	}
	if err := g.EnterRoom(7); err != nil {
		t.Fatal(err)
	}
	es := fakegame.PlaceEnemies(
		fakegame.Enemy{Stage: newD2.Name, Room: 7, SetID: 0, Proc: stage.ProcKI, Params: 0x80FF8201},
		fakegame.Enemy{Stage: newD2.Name, Room: 7, SetID: 1, Proc: stage.ProcKI, Params: 0x80FF8201},
		fakegame.Enemy{Stage: newD2.Name, Room: 7, SetID: addr.ActorSetIDNone, Proc: stage.ProcKI, Params: 0xFFFF0101},
	)
	frames(t, g, es, 4)
	for i := range 3 {
		a, ok := es.Of(i)
		if !ok {
			t.Fatalf("Keese %d not created", i)
		}
		store(t, m, a.Addr+addr.KiOffAction, []byte{addr.KiActFly})
	}
	return g, m
}

// TestHostKeyedKeeseListed: with "ki" named, a host in a room whose only
// enemies are keyed Keese lists them (the release rig's M_NewD2 room 7),
// leaves the unkeyed one alone ("ki", not "ki+u"), and logs the count.
// Keese stay out of DefaultMirrorKinds until their live checks pass.
func TestHostKeyedKeeseListed(t *testing.T) {
	t.Setenv("WW_ENEMY_MIRROR_KINDS", "mo2,bk+u,cc+u,ki")
	_, m := keeseRoom(t)
	start := time.Unix(1000, 0)
	rep := &recReporter{}
	h := NewHost(m, rep, 7, start)
	st, err := h.Tick(start)
	if err != nil {
		t.Fatal(err)
	}
	if st == nil || st.Flags&StateFlagInScope == 0 || st.Stage != "M_NewD2" || st.Room != 7 {
		t.Fatalf("first tick: %+v", st)
	}
	if st.HostKindMask&(1<<KindKI) == 0 || h.Kinds.TakesUnkeyed(KindKI) {
		t.Errorf("kinds %s (mask %02X): want ki on, keyed only", h.Kinds, st.HostKindMask)
	}
	if !eq16(setIDs(st), []uint16{0, 1}) {
		t.Fatalf("listed setIDs %v, want the two keyed Keese [0 1]", setIDs(st))
	}
	for i, s := range st.Slots {
		if s.Kind != KindKI || s.Proc != addr.ProcKI {
			t.Errorf("slot %d: kind %s proc %04X", i, s.Kind, s.Proc)
		}
	}
	if want := "enemy host: 2 enemies (" + h.Kinds.String() + ") in M_NewD2 room 7|"; !strings.Contains(rep.String(), want) {
		t.Errorf("log %q lacks %q", rep.String(), want)
	}
}

// TestHostLogsEmptyRoom: a room with no enemy of the enabled kinds still
// logs its count once ("0 enemies"), so an empty STATE stream reads as
// such and not as a silent host.
func TestHostLogsEmptyRoom(t *testing.T) {
	t.Setenv("WW_ENEMY_MIRROR_KINDS", "mo2")
	_, m := keeseRoom(t)
	start := time.Unix(1000, 0)
	rep := &recReporter{}
	h := NewHost(m, rep, 7, start)
	for i := range 3 {
		if _, err := h.Tick(start.Add(time.Duration(i) * WalkEvery)); err != nil {
			t.Fatal(err)
		}
	}
	const want = "enemy host: 0 enemies (mo2) in M_NewD2 room 7|"
	if n := strings.Count(rep.String(), want); n != 1 {
		t.Errorf("%q logged %d times, want once: %q", want, n, rep.String())
	}
}

// TestMirrorLogsArm: the client logs once when the host's STATEs open
// its gates, after the startup "no STATE from the host yet", and not on
// each tick after.
func TestMirrorLogsArm(t *testing.T) {
	_, m, _, tick := clientRoom(t)
	start := time.Unix(2000, 0)
	rep := &recReporter{}
	c := NewMirror(m, rep, nil, false, start)
	if _, _, err := c.Tick(start); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(rep.String(), "enemy client: not mirroring: no STATE from the host yet|") {
		t.Fatalf("log before a STATE: %q", rep.String())
	}
	for i := range 5 {
		now := start.Add(time.Duration(i) * 16 * time.Millisecond)
		pushState(t, c, hostState(uint32(i)+1, uint32(i)*16), now)
		tick()
		if _, _, err := c.Tick(now); err != nil {
			t.Fatal(err)
		}
	}
	const want = "enemy client: mirroring: the host lists 3 enemies in M_Dai room 1|"
	if n := strings.Count(rep.String(), want); n != 1 {
		t.Errorf("%q logged %d times, want once: %q", want, n, rep.String())
	}
}
