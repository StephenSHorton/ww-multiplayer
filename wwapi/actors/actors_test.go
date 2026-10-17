package actors

import (
	"encoding/binary"
	"errors"
	"math"
	"strings"
	"testing"

	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mem"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/stage"
)

// fakeActorType is what the hand-laid queues' g_fopAc_type and actor_type
// hold (any nonzero value; the game's counter starts at 0x9130001,
// f_pc_base.cpp:28-33).
const fakeActorType = 0x09130001

type fakeActor struct {
	at     uint32
	proc   int16
	setID  uint16
	group  uint8
	room   int8
	health int8
	pos    [3]float32
}

func store(t testing.TB, m *mem.FakeMem, a uint32, b []byte) {
	t.Helper()
	if err := m.Store(a, b); err != nil {
		t.Fatal(err)
	}
}

func u32b(v uint32) []byte { return binary.BigEndian.AppendUint32(nil, v) }
func u16b(v uint16) []byte { return binary.BigEndian.AppendUint16(nil, v) }

// layActors links actors into g_fopAcTg_Queue in order, the way
// fopAc_Create and cTg_Addition leave them, every one executing (mInitState
// 2), and returns their nodes.
func layActors(t testing.TB, m *mem.FakeMem, actors ...fakeActor) []uint32 {
	t.Helper()
	store(t, m, addr.FopAcType, u32b(fakeActorType))
	nodes := make([]uint32, len(actors))
	for i, a := range actors {
		nodes[i] = a.at + addr.ActorOffActorTag
	}
	for i, a := range actors {
		var prev, next uint32
		if i > 0 {
			prev = nodes[i-1]
		}
		if i+1 < len(nodes) {
			next = nodes[i+1]
		}
		n := nodes[i]
		store(t, m, n+addr.TagOffPrev, u32b(prev))
		store(t, m, n+addr.TagOffData, u32b(addr.ActorQueue))
		store(t, m, n+addr.TagOffNext, u32b(next))
		store(t, m, n+addr.TagOffTagData, u32b(a.at))
		store(t, m, n+addr.TagOffUse, []byte{1})
		store(t, m, a.at+addr.ActorOffActorType, u32b(fakeActorType))
		store(t, m, a.at+addr.ActorOffBsPcID, u32b(uint32(100+i)))
		store(t, m, a.at+addr.ActorOffProcName, u16b(uint16(a.proc)))
		store(t, m, a.at+addr.ActorOffInitState, []byte{2})
		store(t, m, a.at+addr.ActorOffSetID, u16b(a.setID))
		store(t, m, a.at+addr.ActorOffGroup, []byte{a.group})
		store(t, m, a.at+addr.ActorOffHomeRoomNo, []byte{byte(a.room)})
		store(t, m, a.at+addr.ActorOffHealth, []byte{byte(a.health)})
		store(t, m, a.at+addr.ActorOffMaxHealth, []byte{byte(a.health)})
		for j, f := range a.pos {
			store(t, m, a.at+addr.ActorOffCurPos+uint32(4*j), u32b(math.Float32bits(f)))
		}
	}
	var head, tail uint32
	if len(nodes) > 0 {
		head, tail = nodes[0], nodes[len(nodes)-1]
	}
	store(t, m, addr.ActorQueue+addr.ActorQueueOffHead, u32b(head))
	store(t, m, addr.ActorQueue+addr.ActorQueueOffTail, u32b(tail))
	store(t, m, addr.ActorQueue+addr.ActorQueueOffSize, u32b(uint32(len(nodes))))
	store(t, m, addr.ProcessIDCounter, u32b(uint32(100+len(nodes))))
	return nodes
}

// testLayouts are the layouts the hand-laid walks read: a Bokoblin's
// (internal/coop/combat's, with its s32 weapon record) and a BOKO's u8
// mode; and testStalfos, a Stalfos's s8 weapon record.
var testLayouts = Layouts{
	int16(stage.ProcBK): {Ice: 0x1264, Weapon: 0x1200, Hold: 0xB30},
	addr.ProcBoko:       {Mode: addr.BokoOffNowMode, ModeU8: true},
	testStalfos:         {Ice: 0x1FA8, Mode: 0x2AC, Weapon: 0x1DCC, Hold: 0x1DD0, HoldS8: true},
}

const testStalfos = int16(stage.ProcST)

// iceUp gives a's enemyice its self-pointer and mode 1, as its create and
// enemy_ice's first call leave it.
func iceUp(t testing.TB, m *mem.FakeMem, a fakeActor) {
	t.Helper()
	l := testLayouts[a.proc]
	store(t, m, a.at+l.Ice, u32b(a.at))
	store(t, m, a.at+l.Ice+addr.EnemyIceOffMode, []byte{1})
}

var (
	link = fakeActor{at: 0x80A00000, proc: 0xA9, setID: addr.ActorSetIDNone, group: 1, room: -1, pos: [3]float32{10, 20, 30}}
	boko = fakeActor{at: 0x80A10000, proc: 0xBE, setID: 5, group: 2, room: 3, health: 4}
	pot  = fakeActor{at: 0x80A20000, proc: 0x1CB, setID: 9, group: 0, room: 3}
)

func TestWalkActors(t *testing.T) {
	m := mem.NewFakeMem()
	layActors(t, m, link, boko, pot)
	iceUp(t, m, boko)
	w, err := ReadWith(m, 0, testLayouts)
	if err != nil {
		t.Fatal(err)
	}
	if !w.Complete() || len(w.Actors) != 3 || w.Size != 3 || w.Type != fakeActorType || w.ProcCounter != 103 {
		t.Fatalf("walk: stop %q notes %q actors %d size %d type 0x%X counter %d", w.Stop, w.Notes, len(w.Actors), w.Size, w.Type, w.ProcCounter)
	}
	l, b := w.Actors[0], w.Actors[1]
	if l.Addr != link.at || l.Proc != 0xA9 || l.SetID != addr.ActorSetIDNone || l.Group != 1 || l.HomeRoom != -1 ||
		l.Pos != link.pos || l.Ice.Off != 0 || l.InitState != 2 {
		t.Errorf("Link: %+v", l)
	}
	if b.PID != 101 || b.SetID != 5 || b.Health != 4 || !b.Ice.Read || !b.Ice.SelfOK || b.Ice.Mode != 1 || b.Ice.Timer != 0 ||
		b.TimerAddr() != boko.at+0x126A || !b.Keyed() || !b.HoldOK || b.Hold != 0 {
		t.Errorf("Bokoblin: %+v", b)
	}
	if a, ok := w.ByPID(101); !ok || a.Addr != boko.at {
		t.Error("ByPID")
	}
	if a, ok := w.ByAddr(pot.at); !ok || a.PID != 102 {
		t.Error("ByAddr")
	}

	// The plain walk reads no layout.
	if w, err := Read(m, 0); err != nil || len(w.Actors) != 3 || w.Actors[1].Ice != (IceState{}) || w.Actors[1].HoldOK {
		t.Errorf("plain walk: %+v %v", w.Actors, err)
	}

	// An empty queue, and one before any actor was created, are clean
	// empty walks.
	empty := mem.NewFakeMem()
	layActors(t, empty)
	if w, err := Read(empty, 0); err != nil || !w.Complete() || len(w.Actors) != 0 {
		t.Errorf("empty queue: %+v, %v", w, err)
	}
	if w, err := Read(mem.NewFakeMem(), 0); err != nil || !w.Complete() || len(w.Actors) != 0 {
		t.Errorf("no actor type yet: %+v, %v", w, err)
	}
}

// TestWalkActorsStops breaks the list in each way the walk guards against
// and checks it stops there, having followed no pointer it did not check.
func TestWalkActorsStops(t *testing.T) {
	cases := []struct {
		name  string
		limit int
		mut   func(m *mem.FakeMem, nodes []uint32)
		rows  int
		stop  string
	}{
		{"cycle", 0, func(m *mem.FakeMem, n []uint32) { m.Store(n[2]+addr.TagOffNext, u32b(n[0])) }, 3, "mSize is 3"},
		{"cycle within mSize", 0, func(m *mem.FakeMem, n []uint32) {
			m.Store(n[2]+addr.TagOffNext, u32b(n[0]))
			m.Store(addr.ActorQueue+addr.ActorQueueOffSize, u32b(9))
		}, 3, "has a cycle"},
		{"self loop", 0, func(m *mem.FakeMem, n []uint32) { m.Store(n[0]+addr.TagOffNext, u32b(n[0])) }, 1, "has a cycle"},
		{"next outside MEM1", 0, func(m *mem.FakeMem, n []uint32) { m.Store(n[1]+addr.TagOffNext, u32b(0x90000000)) }, 2, "is outside MEM1"},
		{"next at the end of MEM1", 0, func(m *mem.FakeMem, n []uint32) { m.Store(n[1]+addr.TagOffNext, u32b(addr.MEM1End-8)) }, 2, "is outside MEM1"},
		{"head below its actor", 0, func(m *mem.FakeMem, n []uint32) { m.Store(addr.ActorQueue, u32b(addr.MEM1Base+0x10)) }, 0, "no room in MEM1"},
		{"misaligned", 0, func(m *mem.FakeMem, n []uint32) { m.Store(n[0]+addr.TagOffNext, u32b(n[1]+2)) }, 1, "not word-aligned"},
		{"prev changed", 0, func(m *mem.FakeMem, n []uint32) { m.Store(n[1]+addr.TagOffPrev, u32b(0x80123450)) }, 1, "changed during the walk"},
		{"tag data", 0, func(m *mem.FakeMem, n []uint32) { m.Store(n[1]+addr.TagOffTagData, u32b(0x80123450)) }, 1, "not its actor"},
		{"actor type", 0, func(m *mem.FakeMem, n []uint32) { m.Store(boko.at+addr.ActorOffActorType, u32b(0)) }, 1, "not g_fopAc_type"},
		{"limit", 2, func(*mem.FakeMem, []uint32) {}, 2, "limit of 2"},
		{"type 0 with a head", 0, func(m *mem.FakeMem, n []uint32) { m.Store(addr.FopAcType, u32b(0)) }, 0, "g_fopAc_type is 0"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m := mem.NewFakeMem()
			c.mut(m, layActors(t, m, link, boko, pot))
			w, err := Read(m, c.limit)
			if err != nil {
				t.Fatal(err)
			}
			if len(w.Actors) != c.rows || !strings.Contains(w.Stop, c.stop) || w.Complete() {
				t.Errorf("actors %d stop %q; want %d actors, stop with %q", len(w.Actors), w.Stop, c.rows, c.stop)
			}
		})
	}

	// mpTail disagreeing with a clean walk is a note, not a stop, but the
	// walk is not complete.
	m := mem.NewFakeMem()
	layActors(t, m, link, boko)
	store(t, m, addr.ActorQueue+addr.ActorQueueOffTail, u32b(0x80123450))
	if w, _ := Read(m, 0); w.Stop != "" || len(w.Actors) != 2 || len(w.Notes) != 1 || w.Complete() {
		t.Errorf("notes: stop %q actors %d notes %q", w.Stop, len(w.Actors), w.Notes)
	}

	// An enemyice past the end of MEM1 is not read.
	edge := boko
	edge.at = addr.MEM1End - addr.ActorSize
	m = mem.NewFakeMem()
	layActors(t, m, edge)
	if w, err := ReadWith(m, 0, testLayouts); err != nil || len(w.Actors) != 1 || w.Actors[0].Ice.Read || w.Actors[0].Ice.Off != 0x1264 {
		t.Errorf("ice past MEM1: %+v %v", w.Actors, err)
	}
}

// TestWalkRequiresQueueData: a node whose mpData is not the queue is no
// queued actor (cLs_Addition sets it, c_list.cpp:40): the walk stops.
func TestWalkRequiresQueueData(t *testing.T) {
	m := mem.NewFakeMem()
	layActors(t, m, link, boko, pot)
	store(t, m, boko.at+addr.ActorOffActorTag+addr.TagOffData, u32b(0))
	w, _ := Read(m, 0)
	if len(w.Actors) != 1 || !strings.Contains(w.Stop, "mpData") {
		t.Errorf("actors %d stop %q", len(w.Actors), w.Stop)
	}
}

// TestWalkMbIsUse: a node whose mbIsUse is 0 has left its list
// (cTg_SingleCut; cTg_Create clears it, c_tag.cpp:89-92): the walk stops.
func TestWalkMbIsUse(t *testing.T) {
	m := mem.NewFakeMem()
	layActors(t, m, link, boko, pot)
	store(t, m, pot.at+addr.ActorOffActorTag+addr.TagOffUse, []byte{0})
	w, _ := Read(m, 0)
	if len(w.Actors) != 2 || !strings.Contains(w.Stop, "mbIsUse") {
		t.Errorf("actors %d stop %q", len(w.Actors), w.Stop)
	}
}

// TestLayoutReads: a u8 mode, an s8 record sign-extended, and a part past
// the end of MEM1 left unread.
func TestLayoutReads(t *testing.T) {
	m := mem.NewFakeMem()
	st := fakeActor{at: 0x80A30000, proc: testStalfos, setID: 7, group: 2, room: 3, health: 4}
	wp := fakeActor{at: 0x80A40000, proc: addr.ProcBoko, setID: addr.ActorSetIDNone, group: 0, room: 3}
	layActors(t, m, st, wp)
	store(t, m, st.at+0x2AC, u32b(0xFFFFFFFE))
	store(t, m, st.at+0x1DCC, u32b(101))
	store(t, m, st.at+0x1DD0, []byte{0xFE})
	store(t, m, wp.at+addr.BokoOffNowMode, []byte{0xF3})
	w, err := ReadWith(m, 0, testLayouts)
	if err != nil || len(w.Actors) != 2 {
		t.Fatalf("%+v %v", w, err)
	}
	s, b := w.Actors[0], w.Actors[1]
	if !s.ModeOK || s.Mode != -2 || s.Weapon != 101 || !s.HoldOK || s.Hold != -2 || !s.Ice.Read || s.Ice.Off != 0x1FA8 {
		t.Errorf("Stalfos: %+v", s)
	}
	if !b.ModeOK || b.Mode != 0xF3 || b.Ice.Off != 0 || b.HoldOK {
		t.Errorf("BOKO: %+v", b)
	}

	edge := st
	edge.at = addr.MEM1End - addr.ActorSize
	m = mem.NewFakeMem()
	layActors(t, m, edge)
	if w, err := ReadWith(m, 0, testLayouts); err != nil || len(w.Actors) != 1 ||
		w.Actors[0].ModeOK || w.Actors[0].Weapon != 0 || w.Actors[0].HoldOK || w.Actors[0].Ice.Read {
		t.Errorf("past MEM1: %+v %v", w.Actors, err)
	}
}

// TestString: without Names an actor prints no proc name; with it, the
// name Names gives.
func TestString(t *testing.T) {
	a := Actor{Addr: 0x80A10000, PID: 7, Proc: 0xBE, SetID: addr.ActorSetIDNone, HomeRoom: -1}
	old := Names
	t.Cleanup(func() { Names = old })
	Names = nil
	if got, want := a.String(), "actor 0x80A10000 (pid 7, proc 0x00BE , setID none, room -1)"; got != want {
		t.Errorf("%q, want %q", got, want)
	}
	Names = func(p int16) string { return "bk" }
	if got, want := a.String(), "actor 0x80A10000 (pid 7, proc 0x00BE bk, setID none, room -1)"; got != want {
		t.Errorf("%q, want %q", got, want)
	}
}

// TestReread: the walk's actor passes; a reused block and a node off the
// queue are refused with ErrChanged; ZoneHolds without a zone is false.
func TestReread(t *testing.T) {
	m := mem.NewFakeMem()
	layActors(t, m, link, boko)
	iceUp(t, m, boko)
	w, err := ReadWith(m, 0, testLayouts)
	if err != nil || len(w.Actors) != 2 {
		t.Fatalf("%+v %v", w, err)
	}
	b := w.Actors[1]
	if now, err := Reread(m, &b, w.Type, testLayouts); err != nil || now != b {
		t.Fatalf("Reread: %+v %v", now, err)
	}
	if _, err := Reread(m, &b, 0, testLayouts); !errors.Is(err, ErrChanged) {
		t.Errorf("g_fopAc_type 0: %v", err)
	}
	store(t, m, boko.at+addr.ActorOffBsPcID, u32b(999))
	if _, err := Reread(m, &b, w.Type, testLayouts); !errors.Is(err, ErrChanged) || !strings.Contains(err.Error(), "reused") {
		t.Errorf("a reused block: %v", err)
	}
	store(t, m, b.Node+addr.TagOffUse, []byte{0})
	if _, err := Reread(m, &b, w.Type, testLayouts); !errors.Is(err, ErrChanged) || !strings.Contains(err.Error(), "mbIsUse") {
		t.Errorf("a node off the queue: %v", err)
	}
	if ok, _ := ZoneHolds(m, 3); ok {
		t.Error("ZoneHolds with no zone")
	}
}
