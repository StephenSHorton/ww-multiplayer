package switches

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/StephenSHorton/ww-multiplayer/internal/coop/addr"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/stagedata"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/stagedata/stagetest"
)

// realNames are the l_objectName names the tests place, with their procs
// (d_stage.cpp lines in actors.go and stagedata).
var realNames = []struct {
	name string
	proc stagedata.Proc
}{
	{"SW_HIT0", stagedata.ProcSwhit0}, {"bonbori", stagedata.ProcEP}, {"Kbota_A", stagedata.ProcSwpush},
	{"Hhbot1", stagedata.ProcSwheavy}, {"ALLdie", stagedata.ProcAllDie}, {"AND_SW0", stagedata.ProcAndsw0},
	{"AND_SW2", stagedata.ProcAndsw2}, {"ObjTime", stagedata.ProcObjTimer}, {"SW_C00", stagedata.ProcSwc00},
	{"Evsw", stagedata.ProcTagEvsw}, {"door10", stagedata.ProcDoor10}, {"door12", stagedata.ProcDoor12},
	{"doorKD", procKddoor}, {"SMBdor", procMbdoor}, {"takara", stagedata.ProcTbox},
	{"osiBLK0", stagedata.ProcMovebox}, {"Bk", stagedata.ProcBK}, {"Tn", stagedata.ProcTN},
	{"TagEv", procTagEvent}, {"Hseki1", procTry}, {"Hfbot1A", procSwflat}, {"Htobi1", procShutter},
	{"Bdk", stagedata.ProcBDK}, {"Kui", procKui}, {"koisi1", procStone}, {"Warpt", procWarpt},
	{"MhmrSW0", procSwhammer}, {"Fganon", stagedata.ProcFganon}, {"Mn", procNpcMn},
	{"agbA", procAgbsw0}, {"agbMW", procAgbsw0}, {"agbCSW", procAgbsw0},
	{"Sitem", stagedata.ProcItem}, // a proc the table doesn't read
}

// synthName is the name the tests give spec procs that realNames doesn't.
func synthName(p stagedata.Proc) string { return fmt.Sprintf("P%03X", uint16(p)) }

func testNames(t testing.TB) *stagedata.ObjectNames {
	t.Helper()
	var list []stagetest.ObjectName
	for _, n := range realNames {
		list = append(list, stagetest.ObjectName{Name: n.name, Proc: int16(n.proc), Subtype: -1})
	}
	for _, sp := range specs {
		list = append(list, stagetest.ObjectName{Name: synthName(sp.proc), Proc: int16(sp.proc), Subtype: -1})
	}
	names, err := stagedata.ParseObjectNames(stagetest.ObjectNameTable(list...))
	if err != nil {
		t.Fatal(err)
	}
	return names
}

// act is an ACTR entry; angle is x, y, z, each as its 16 bits.
func act(name string, params uint32, angle ...int) stagedata.Actor {
	a := stagedata.Actor{Tag: "ACTR", Layer: stagedata.AllLayers, Name: name, Params: params}
	for i, v := range angle[:min(len(angle), 3)] {
		a.Angle[i] = int16(uint16(v))
	}
	return a
}

// stage builds a stage of save table table: rooms[i] is room i's entries
// (nil for no room), dzs the stage's own.
func stage(name string, table int, dzs []stagedata.Actor, rooms ...[]stagedata.Actor) *stagedata.Stage {
	s := &stagedata.Stage{Name: name, SaveTable: table, DZS: &stagedata.DZx{Actors: index(dzs)}}
	for no, as := range rooms {
		if as != nil {
			s.Rooms = append(s.Rooms, stagedata.Room{No: no, DZR: &stagedata.DZx{Actors: index(as)}})
		}
	}
	return s
}

func index(as []stagedata.Actor) []stagedata.Actor {
	out := slices.Clone(as)
	for i := range out {
		out[i].Index = i
	}
	return out
}

func mem(table, no int) Key { return Key{Scope{Store: StoreMemory, Table: table, Room: -1}, no} }
func dan(table, no int) Key { return Key{Scope{Store: StoreDan, Table: table, Room: -1}, no} }
func zone(table int, stage string, room, no int) Key {
	return Key{Scope{Store: StoreZone, Table: table, Stage: stage, Room: room}, no}
}
func roomSw(table int, stage string, room, no int) Key {
	return Key{Scope{Store: StoreRoom, Table: table, Stage: stage, Room: room}, no}
}

// want is the expected verdict on one switch: the class and op of its
// first writer ("" class for no writer) and whether it is synced.
type want struct {
	key    Key
	class  Class
	op     Op
	synced bool
}

func check(t *testing.T, tb *Table, ws ...want) {
	t.Helper()
	for _, w := range ws {
		s, ok := tb.Lookup(w.key)
		if !ok {
			t.Errorf("%v: missing; have %v", w.key, keysOf(tb))
			continue
		}
		if s.Synced != w.synced {
			t.Errorf("%v: synced %v, want %v (%s)", w.key, s.Synced, w.synced, s.Reason)
		}
		if w.class == "" {
			if len(s.Writers) != 0 {
				t.Errorf("%v: %d writers, want none", w.key, len(s.Writers))
			}
			continue
		}
		if len(s.Writers) == 0 {
			t.Errorf("%v: no writer, want %s %s", w.key, w.class, w.op)
			continue
		}
		if got := s.Writers[0]; got.Class != w.class || got.Op != w.op {
			t.Errorf("%v: writer %s %s (%s), want %s %s", w.key, got.Class, got.Op, got.Why, w.class, w.op)
		}
	}
}

func keysOf(tb *Table) []string {
	var s []string
	for _, sw := range tb.Switches {
		s = append(s, sw.Key.String())
	}
	return s
}

// TestClasses places one kind of switch user per case in room 1 of a
// table-3 stage and checks the verdict on each switch it uses.
func TestClasses(t *testing.T) {
	const tb = addr.TableDRC
	z := func(room, no int) Key { return zone(tb, "D", room, no) }
	m := func(no int) Key { return mem(tb, no) }
	for _, c := range []struct {
		name   string
		actors []stagedata.Actor
		want   []want
		count  int  // switches the case should produce; -1 not to check
		noUser bool // the entry isn't one the table reads
	}{
		{"crystal", []stagedata.Actor{act("SW_HIT0", 0xFF<<20|0x05)}, []want{{m(0x05), Latching, OpSet, true}}, 1, false},
		{"crystal timer 0", []stagedata.Actor{act("SW_HIT0", 0x05)}, []want{{m(0x05), Latching, OpSet, true}}, 1, false},
		{"timed crystal", []stagedata.Actor{act("SW_HIT0", 0x0A<<20|0x05, 0, 0, 0x06)},
			[]want{{m(0x05), Unsafe, OpSetClear, false}, {m(0x06), "", "", false}}, 2, false},
		{"torch", []stagedata.Actor{act("bonbori", 0x07<<24|0xFF<<16|0xFF<<8)}, []want{{m(0x07), Latching, OpSet, true}}, 1, false},
		{"timed torch", []stagedata.Actor{act("bonbori", 0x07<<24|0x08<<16|0x10<<8)},
			[]want{{m(0x07), Unsafe, OpSetClear, false}, {m(0x08), "", "", false}}, 2, false},
		{"floor switch 0", []stagedata.Actor{act("Kbota_A", 0<<24|0x10<<8)}, []want{{m(0x10), Latching, OpSet, true}}, 1, false},
		{"floor switch 1", []stagedata.Actor{act("Kbota_A", 1<<24|0x10<<8)}, []want{{m(0x10), Unsafe, OpSetClear, false}}, 1, false},
		{"floor switch 1 v1", []stagedata.Actor{act("Kbota_A", 1<<30|1<<24|0x10<<8, 0, 0, 0x11)},
			[]want{{m(0x10), Unsafe, OpSetClear, false}, {m(0x11), "", "", false}}, 2, false},
		{"floor switch 2", []stagedata.Actor{act("Kbota_A", 2<<24|0x10<<8)}, []want{{m(0x10), Unsafe, OpSetClear, false}}, 1, false},
		{"floor switch 3", []stagedata.Actor{act("Kbota_A", 3<<24|0x10<<8)}, []want{{m(0x10), Latching, OpSet, true}}, 1, false},
		{"floor switch 5", []stagedata.Actor{act("Kbota_A", 5<<24|0x10<<8)}, []want{{m(0x10), Unknown, OpSet, false}}, 1, false},
		{"iron boots 0", []stagedata.Actor{act("Hhbot1", 0<<24|0x10<<8)}, []want{{m(0x10), Unsafe, OpSetClear, false}}, 1, false},
		{"iron boots 1", []stagedata.Actor{act("Hhbot1", 1<<24|0x10<<8)}, []want{{m(0x10), Unsafe, OpSetClear, false}}, 1, false},
		{"iron boots 2", []stagedata.Actor{act("Hhbot1", 2<<24|0x10<<8)}, []want{{m(0x10), Unsafe, OpToggle, false}}, 1, false},
		{"iron boots 3", []stagedata.Actor{act("Hhbot1", 3<<24|0x10<<8)}, []want{{m(0x10), Latching, OpSet, true}}, 1, false},
		{"ALLdie", []stagedata.Actor{act("ALLdie", 0xC9<<8)}, []want{{z(1, 0xC9), Latching, OpSet, true}}, 1, false},
		{"ObjTime", []stagedata.Actor{act("ObjTime", 0x0A<<16|30)}, []want{{m(0x0A), Unsafe, OpClear, false}}, 1, false},
		{"SW_C00 0", []stagedata.Actor{act("SW_C00", 0<<16|0xFF<<8|0x20)}, []want{{m(0x20), Unsafe, OpSetClear, false}}, 1, false},
		{"SW_C00 1", []stagedata.Actor{act("SW_C00", 1<<16|0x21<<8|0x20)},
			[]want{{m(0x20), Latching, OpSet, true}, {m(0x21), "", "", false}}, 2, false},
		{"Evsw 0", []stagedata.Actor{act("Evsw", 0<<24|0x1234<<8|0x30)}, []want{{m(0x30), Unsafe, OpSetClear, false}}, 1, false},
		{"Evsw 1", []stagedata.Actor{act("Evsw", 1<<24|0x1234<<8|0x30)}, []want{{m(0x30), Event, OpSet, false}}, 1, false},
		{"TagEv", []stagedata.Actor{act("TagEv", 0xFF<<16|0x31<<8)}, []want{{m(0x31), Event, OpSet, false}}, 1, false},
		{"chest", []stagedata.Actor{act("takara", 0xFF<<12, 5, 0, 0x40)}, []want{{m(0x40), Latching, OpSet, true}}, 1, false},
		{"enemy chest", []stagedata.Actor{act("takara", 0xC2<<12|2, 5, 0, 0xFF)}, []want{{z(5, 0xC2), Latching, OpSet, true}}, 1, false},
		{"switch chest", []stagedata.Actor{act("takara", 0x41<<12|1, 5, 0, 0xFF)}, []want{{m(0x41), "", "", false}}, 1, false},
		{"key door10", []stagedata.Actor{act("door10", 0xFF<<20|4<<8|0x12, 2|3<<6)},
			[]want{{m(0x12), Latching, OpSet, true}}, 1, false},
		{"kill-all door10", []stagedata.Actor{act("door10", 0xFF<<20|2<<8|0xC5, 2|3<<6)},
			[]want{{z(2, 0xC5), Latching, OpSet, true}}, 1, false},
		{"kill-all back door10", []stagedata.Actor{act("door10", 0xC6<<20|5<<8|0x13, 2|3<<6)},
			[]want{{z(3, 0xC6), Latching, OpSet, true}, {m(0x13), Latching, OpSet, true}}, 2, false},
		{"high key door10", []stagedata.Actor{act("door10", 0xFF<<20|4<<8|0x85, 2|3<<6)},
			[]want{{dan(tb, 0x85), "", "", false}}, 1, false},
		{"key door12", []stagedata.Actor{act("door12", 1<<8|0x14, 0)}, []want{{m(0x14), Latching, OpSet, true}}, -1, false},
		{"boss door12", []stagedata.Actor{act("door12", 0<<8|0x15, 0, 0, 9<<8)}, []want{{m(0x15), Latching, OpSet, true}}, -1, false},
		{"key doorKD", []stagedata.Actor{act("doorKD", 0xC7<<20|2<<8|0x16, 1|4<<6)},
			[]want{{m(0x16), Latching, OpSet, true}, {z(4, 0xC7), Latching, OpSet, true}}, 2, false},
		{"mini-boss door", []stagedata.Actor{act("SMBdor", 0x17, 0, 0, 1)}, []want{{m(0x17), Latching, OpSet, true}}, 1, false},
		{"mini-boss door reader", []stagedata.Actor{act("SMBdor", 0x17, 0, 0, 0)}, []want{{m(0x17), "", "", false}}, 1, false},
		{"Bokoblin", []stagedata.Actor{act("Bk", 0, 0, 0, 0x22)}, []want{{m(0x22), Latching, OpSet, true}}, 1, false},
		{"Bokoblin none", []stagedata.Actor{act("Bk", 0, 0, 0, 0xFF)}, nil, 0, false},
		{"boss Bokoblin", []stagedata.Actor{act("Bk", 0xA)},
			[]want{{roomSw(tb, "D", 1, 0xE0), Latching, OpSet, true}, {roomSw(tb, "D", 1, 0xE1), Latching, OpSet, true}}, 2, false},
		{"Helmaroc", []stagedata.Actor{act("Bdk", 0)}, []want{{dan(tb, 0x80), Unsafe, OpSetClear, false}}, 1, false},
		{"Phantom Ganon", []stagedata.Actor{act("Fganon", 0x24<<24|0x23<<16)},
			[]want{{m(0x23), Latching, OpSet, true}, {m(0x24), Unsafe, OpSetClear, false}}, 2, false},
		{"post 0xFF", []stagedata.Actor{act("Kui", 0xFF<<24)}, []want{{m(0x00), Latching, OpSet, true}}, 1, false},
		{"no post", []stagedata.Actor{act("Kui", 0xFFFFFFFF)}, nil, 0, false},
		{"rock 2", []stagedata.Actor{act("koisi1", 2<<24|0x25<<8)}, []want{{m(0x25), Latching, OpSet, true}}, 1, false},
		{"rock 0", []stagedata.Actor{act("koisi1", 0<<24|0x25<<8)}, nil, 0, false},
		{"rock 6", []stagedata.Actor{act("koisi1", 6<<24|0x25<<8)}, []want{{m(0x25), Unknown, OpSet, false}}, 1, false},
		{"warp jar", []stagedata.Actor{act("Warpt", 3, 0x26)}, []want{{m(0x26), "", "", false}}, 1, false},
		{"warp pot", []stagedata.Actor{act("Warpt", 0, 0xFF26)}, []want{{m(0x26), Latching, OpSet, true}}, 1, false},
		{"hammer switch", []stagedata.Actor{act("MhmrSW0", 0x27<<8|0x28)},
			[]want{{m(0x27), Latching, OpSet, true}, {m(0x28), Unsafe, OpClear, false}}, 2, false},
		{"Obj_Try", []stagedata.Actor{act("Hseki1", 0x29<<8)}, []want{{m(0x29), Unknown, OpSet, false}}, 1, false},
		{"shutter", []stagedata.Actor{act("Htobi1", 0x2A)}, []want{{m(0x2A), "", "", false}}, 1, false},
		{"Manny", []stagedata.Actor{act("Mn", 0x2C<<16|0x2B<<8)},
			[]want{{m(0x2C), Latching, OpSet, true}, {m(0x2B), "", "", false}}, 2, false},
		{"not a switch user", []stagedata.Actor{act("Sitem", 0x05)}, nil, 0, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			tbl := Build([]*stagedata.Stage{stage("D", tb, nil, nil, c.actors)}, testNames(t))
			check(t, tbl, c.want...)
			if c.count >= 0 && len(tbl.Switches) != c.count {
				t.Errorf("%d switches %v, want %d", len(tbl.Switches), keysOf(tbl), c.count)
			}
			if wantUsers := 1; c.noUser && tbl.Users != 0 || !c.noUser && tbl.Users != wantUsers {
				t.Errorf("%d users, want noUser %v", tbl.Users, c.noUser)
			}
			if len(tbl.Invalid) != 0 || len(tbl.Undecoded) != 0 {
				t.Errorf("invalid %v, undecoded %v", tbl.Invalid, tbl.Undecoded)
			}
		})
	}
}

// andsw2 is an AND_SW2 entry: type typ, output out, num inputs from first
// (0xFF: out+1).
func andsw2(typ, out, num, first int) stagedata.Actor {
	return act("AND_SW2", uint32(first)<<24|uint32(out)<<16|uint32(typ)<<8|uint32(num))
}

// andsw0 is an AND_SW0 entry: behaviour beh, output out, num inputs from
// first (0: out+1).
func andsw0(beh, out, num, first int) stagedata.Actor {
	return act("AND_SW0", uint32(out)<<24|uint32(first)<<16|uint32(beh)<<8|uint32(num))
}

func crystal(no int) stagedata.Actor { return act("SW_HIT0", 0xFF<<20|uint32(no)) }
func timedCrystal(no int) stagedata.Actor {
	return act("SW_HIT0", 0x10<<20|uint32(no), 0, 0, 0xFF) // no stop switch
}

func TestANDPropagation(t *testing.T) {
	const tb = addr.TableFW
	m := func(no int) Key { return mem(tb, no) }
	for _, c := range []struct {
		name   string
		actors []stagedata.Actor
		want   []want
		fedBy  map[int]string // switch -> substring of its first writer's Why
	}{
		{"chain", []stagedata.Actor{crystal(0x11), crystal(0x12), andsw2(0, 0x10, 2, 0xFF), andsw0(0, 0x20, 1, 0x10)},
			[]want{{m(0x10), Latching, OpSet, true}, {m(0x20), Latching, OpSet, true}}, nil},
		{"unsafe input", []stagedata.Actor{crystal(0x11), timedCrystal(0x12), andsw2(0, 0x10, 2, 0xFF), andsw0(0, 0x20, 1, 0x10)},
			[]want{{m(0x10), Unsafe, OpSet, false}, {m(0x20), Unsafe, OpSet, false}, {m(0x11), Latching, OpSet, true}},
			map[int]string{0x10: "fed by memory t4 0x12", 0x20: "fed by memory t4 0x10"}},
		{"input with no setter", []stagedata.Actor{andsw2(0, 0x50, 1, 0xFF)},
			[]want{{m(0x50), Unsafe, OpSet, false}, {m(0x51), "", "", false}}, map[int]string{0x50: "fed by memory t4 0x51"}},
		{"cycle", []stagedata.Actor{andsw0(0, 0x30, 1, 0x31), andsw0(0, 0x31, 1, 0x30), crystal(0x31)},
			[]want{{m(0x30), Latching, OpSet, true}, {m(0x31), Latching, OpSet, true}}, nil},
		{"cycle with unsafe input", []stagedata.Actor{andsw2(0, 0x40, 2, 0x41), andsw2(0, 0x41, 1, 0x40), act("ObjTime", 0x42<<16|5)},
			[]want{{m(0x40), Unsafe, OpSet, false}, {m(0x41), Unsafe, OpSet, false}, {m(0x42), Unsafe, OpClear, false}},
			map[int]string{0x40: "fed by memory t4 0x42", 0x41: "fed by memory t4 0x40"}},
		{"self-feeding", []stagedata.Actor{andsw0(0, 0x60, 1, 0x60)}, []want{{m(0x60), Latching, OpSet, true}}, nil},
		{"continuous andsw2", []stagedata.Actor{crystal(0x71), andsw2(1, 0x70, 1, 0xFF)},
			[]want{{m(0x70), Unsafe, OpSetClear, false}}, nil},
		{"continuous andsw0", []stagedata.Actor{crystal(0x71), andsw0(1, 0x70, 1, 0)},
			[]want{{m(0x70), Unsafe, OpSetClear, false}}, nil},
		{"delayed andsw0", []stagedata.Actor{crystal(0x71), andsw0(3, 0x70, 1, 0)}, []want{{m(0x70), Latching, OpSet, true}}, nil},
		{"timed andsw0", []stagedata.Actor{crystal(0x71), crystal(0x72), andsw0(2, 0x70, 2, 0)},
			[]want{{m(0x70), Unsafe, OpSet, false}, {m(0x71), Latching, OpSet, false}, {m(0x72), Latching, OpSet, false}},
			map[int]string{0x70: "fed by memory t4 0x71"}},
		{"andsw0 Forest Haven", []stagedata.Actor{andsw0(0, 0x70, 0xFF, 0)}, nil, nil},
		{"andsw2 no output", []stagedata.Actor{andsw2(0, 0xFF, 1, 0x10)}, nil, nil},
		{"andsw2 other type", []stagedata.Actor{andsw2(2, 0x70, 1, 0x71)}, nil, nil},
		{"too many inputs", []stagedata.Actor{andsw2(0, 0x70, MaxANDInputs+1, 0x00)}, []want{{m(0x70), Unknown, OpSet, false}}, nil},
		{"latching mixed with AND", []stagedata.Actor{crystal(0x70), timedCrystal(0x71), andsw2(0, 0x70, 1, 0x71)},
			[]want{{m(0x70), Latching, OpSet, false}}, nil},
	} {
		t.Run(c.name, func(t *testing.T) {
			tbl := Build([]*stagedata.Stage{stage("W", tb, nil, c.actors)}, testNames(t))
			check(t, tbl, c.want...)
			for no, sub := range c.fedBy {
				s, _ := tbl.Lookup(m(no))
				if s == nil || len(s.Writers) == 0 || !strings.Contains(s.Writers[0].Why, sub) {
					t.Errorf("0x%02X: writers %+v, want %q in the first's Why", no, s, sub)
				}
			}
			if len(c.want) == 0 && len(tbl.Switches) != 0 {
				t.Errorf("switches %v, want none", keysOf(tbl))
			}
			checkFixedPoint(t, tbl)
		})
	}
}

// checkFixedPoint checks the verdicts are consistent: Synced is "a
// latching setter, the rest latching or GBA", a latching AND's inputs are
// all synced, and GBA is set exactly on the synced switches with a GBA
// writer or a latching AND writer with a GBA input.
func checkFixedPoint(t testing.TB, tbl *Table) {
	t.Helper()
	for i := range tbl.Switches {
		s := &tbl.Switches[i]
		if s.Synced != synced(s) {
			t.Errorf("%v: Synced %v disagrees with its writers", s.Key, s.Synced)
		}
		gba := false
		for _, w := range s.Writers {
			if w.Class == GBA {
				gba = true
			}
			if w.Class != Latching {
				continue
			}
			for _, in := range w.Inputs {
				if !tbl.Synced(in) {
					t.Errorf("%v: latching AND %v has unsynced input %v", s.Key, w.Ref, in)
				}
				if tbl.GBADependent(in) {
					gba = true
				}
			}
		}
		if want := s.Synced && gba; s.GBA != want {
			t.Errorf("%v: GBA %v, want %v (synced %v)", s.Key, s.GBA, want, s.Synced)
		}
		if tbl.GBADependent(s.Key) != (s.Synced && s.GBA) {
			t.Errorf("%v: GBADependent disagrees with Synced and GBA", s.Key)
		}
	}
}

// agb is a Tingle Tuner switch (agbsw0) of type typ writing sw0 and sw1.
func agb(name string, typ, sw0, sw1 int) stagedata.Actor {
	return act(name, uint32(sw1)<<24|uint32(sw0)<<16|0xFFFF, 0, 0, typ)
}

// TestTingleTuner covers the agbsw0 writers (actors.go): every type the
// game has writes only while a GBA is linked, so a switch it shares with
// latching setters syncs, recorded as GBA-dependent, while one only it
// writes does not, and a type past ExeSubTable stays Unknown.
func TestTingleTuner(t *testing.T) {
	const tb = addr.TableDRC
	m := func(no int) Key { return mem(tb, no) }
	for _, c := range []struct {
		name   string
		actors []stagedata.Actor
		key    Key
		synced bool
		gba    bool
		reason string
	}{
		// The owner's DRC key doors: a key door's switch the Tuner also
		// names.
		{"key door and agbA", []stagedata.Actor{act("door10", 0xFF<<20|4<<8|0x3D), agb("agbA", 0, 0x3D, 0xFF)},
			m(0x3D), true, true, "gba writer D r0 ACTR#1 agbA writes it only while a GBA is linked"},
		{"crystal and agbMW", []stagedata.Actor{crystal(0x10), agb("agbMW", 7, 0x10, 0xFF)},
			m(0x10), true, true, "so it syncs only while none is"},
		{"crystal and agbCSW's sw1", []stagedata.Actor{crystal(0x11), agb("agbCSW", 8, 0xFF, 0x11)},
			m(0x11), true, true, "every setter latches"},
		{"agbA alone", []stagedata.Actor{agb("agbA", 0, 0x12, 0xFF)},
			m(0x12), false, false, "no latching setter: only gba writers (D r0 ACTR#0 agbA"},
		{"agbA and a timed crystal", []stagedata.Actor{timedCrystal(0x13), agb("agbA", 0, 0x13, 0xFF)},
			m(0x13), false, false, "unsafe writer D r0 ACTR#0 SW_HIT0"},
		// A type past the table's 15 entries isn't known to need a link.
		{"crystal and a type past ExeSubTable", []stagedata.Actor{crystal(0x14), agb("agbA", agbsw0Types, 0x14, 0xFF)},
			m(0x14), false, false, "unknown writer D r0 ACTR#1 agbA: Tingle Tuner switch type 15 is past ExeSubTable's 15 entries"},
		{"the last type in the table", []stagedata.Actor{crystal(0x15), agb("agbA", agbsw0Types-1, 0x15, 0xFF)},
			m(0x15), true, true, "gba writer"},
		// An AND fed by a GBA-dependent input is GBA-dependent too.
		{"AND of a GBA input", []stagedata.Actor{crystal(0x21), agb("agbA", 0, 0x21, 0xFF), andsw2(0, 0x20, 1, 0xFF)},
			m(0x20), true, true, "an AND input syncs only while no GBA is linked"},
		{"AND of an AND of a GBA input", []stagedata.Actor{crystal(0x31), agb("agbA", 0, 0x31, 0xFF), andsw2(0, 0x30, 1, 0xFF), andsw0(0, 0x40, 1, 0x30)},
			m(0x40), true, true, "an AND input"},
		{"AND of plain inputs", []stagedata.Actor{crystal(0x51), andsw2(0, 0x50, 1, 0xFF)},
			m(0x50), true, false, "every setter latches"},
		// Dan, zone and room switches route as any other.
		{"dan", []stagedata.Actor{crystal(0x85), agb("agbA", 0, 0x85, 0xFF)}, dan(tb, 0x85), true, true, "gba writer"},
		{"zone", []stagedata.Actor{crystal(0xC5), agb("agbA", 0, 0xFF, 0xC5)}, zone(tb, "D", 0, 0xC5), true, true, "gba writer"},
	} {
		t.Run(c.name, func(t *testing.T) {
			tbl := Build([]*stagedata.Stage{stage("D", tb, nil, c.actors)}, testNames(t))
			checkFixedPoint(t, tbl)
			s, ok := tbl.Lookup(c.key)
			if !ok {
				t.Fatalf("no %v: %v", c.key, keysOf(tbl))
			}
			if s.Synced != c.synced || s.GBA != c.gba || tbl.GBADependent(c.key) != (c.synced && c.gba) || !strings.Contains(s.Reason, c.reason) {
				t.Errorf("synced %v, gba %v, reason %q; want %v, %v, %q", s.Synced, s.GBA, s.Reason, c.synced, c.gba, c.reason)
			}
			for _, w := range s.Writers {
				if w.Name == "agbA" || w.Name == "agbMW" || w.Name == "agbCSW" {
					if w.Op != OpToggle || (w.Class != GBA && w.Class != Unknown) {
						t.Errorf("Tuner writer %s %s, want a toggle of class gba or unknown", w.Class, w.Op)
					}
				}
			}
		})
	}
	// 0xFF is no switch: an agbsw0 with one switch writes one.
	tbl := Build([]*stagedata.Stage{stage("D", tb, nil, []stagedata.Actor{agb("agbA", 0, 0x60, 0xFF)})}, testNames(t))
	if len(tbl.Switches) != 1 {
		t.Errorf("switches %v, want only 0x60", keysOf(tbl))
	}
}

func TestMixedSetters(t *testing.T) {
	const tb = addr.TableET
	for _, c := range []struct {
		name   string
		stages []*stagedata.Stage
		synced bool
		n      int // writers
		reason string
	}{
		{"two latching in two stages", []*stagedata.Stage{
			stage("A", tb, nil, []stagedata.Actor{crystal(0x05)}),
			stage("B", tb, nil, []stagedata.Actor{act("ALLdie", 0x05<<8)}),
		}, true, 2, "every setter latches"},
		{"latching and timed", []*stagedata.Stage{
			stage("A", tb, nil, []stagedata.Actor{crystal(0x05), timedCrystal(0x05)}),
		}, false, 2, "unsafe writer A r0 ACTR#1 SW_HIT0: timed crystal"},
		{"latching and event", []*stagedata.Stage{
			stage("A", tb, nil, []stagedata.Actor{crystal(0x05)}, []stagedata.Actor{act("TagEv", 0x05<<8)}),
		}, false, 2, "event writer A r1"},
		{"latching and unknown", []*stagedata.Stage{
			stage("A", tb, []stagedata.Actor{act("Hseki1", 0x05<<8)}, []stagedata.Actor{crystal(0x05)}),
		}, false, 2, "unknown writer A stage ACTR#0 Hseki1"},
		{"latching and push block", []*stagedata.Stage{
			stage("A", tb, nil, []stagedata.Actor{crystal(0x05), act("osiBLK0", 1<<24|0x05<<8, 0, 0, 0x02)}),
		}, false, 2, "push block path 2"},
		{"latching and Tingle Tuner in another stage", []*stagedata.Stage{
			stage("A", tb, nil, []stagedata.Actor{crystal(0x05)}),
			stage("B", tb, nil, []stagedata.Actor{agb("agbA", 0, 0x05, 0xFF)}),
		}, true, 2, "gba writer B r0 ACTR#0 agbA"},
	} {
		t.Run(c.name, func(t *testing.T) {
			tbl := Build(c.stages, testNames(t))
			s, ok := tbl.Lookup(mem(tb, 0x05))
			if !ok {
				t.Fatalf("no switch 0x05: %v", keysOf(tbl))
			}
			if s.Synced != c.synced || len(s.Writers) != c.n || !strings.Contains(s.Reason, c.reason) {
				t.Errorf("synced %v, %d writers, reason %q; want %v, %d, %q", s.Synced, len(s.Writers), s.Reason, c.synced, c.n, c.reason)
			}
		})
	}
}

func TestKeyFor(t *testing.T) {
	for _, c := range []struct {
		table int
		stage string
		room  int
		no    int
		want  Key
		err   string
	}{
		{3, "D", 2, 0x00, mem(3, 0x00), ""},
		{3, "D", -1, 0x7F, mem(3, 0x7F), ""},
		{3, "D", 2, 0x80, dan(3, 0x80), ""},
		{3, "D", -1, 0xBF, dan(3, 0xBF), ""},
		{3, "D", 2, 0xC0, zone(3, "D", 2, 0xC0), ""},
		{3, "D", 63, 0xDF, zone(3, "D", 63, 0xDF), ""},
		{3, "D", 2, 0xE0, roomSw(3, "D", 2, 0xE0), ""},
		{3, "D", 2, 0xEF, roomSw(3, "D", 2, 0xEF), ""},
		{20, "D", 2, 0xC1, zone(20, "D", 2, 0xC1), ""},
		{3, "D", 2, 0xFF, Key{}, "no switch"},
		{3, "D", 2, -1, Key{}, "no switch"},
		{3, "D", 2, 0xF0, Key{}, "past the stores"},
		{3, "D", 2, 0x100, Key{}, "past the stores"},
		{3, "D", 2, -2, Key{}, "past the stores"},
		{3, "D", -1, 0xC0, Key{}, "has no zone"},
		{3, "D", 64, 0xE0, Key{}, "has no zone"},
		{16, "D", 2, 0x10, Key{}, "past the 16 tables"},
		{-1, "D", 2, 0x90, Key{}, "past the 16 tables"},
	} {
		got, err := KeyFor(c.table, c.stage, c.room, c.no)
		if c.err != "" {
			if err == nil || !strings.Contains(err.Error(), c.err) {
				t.Errorf("KeyFor(%d, %q, %d, 0x%X) = %v, %v; want error %q", c.table, c.stage, c.room, c.no, got, err, c.err)
			}
			continue
		}
		if err != nil || got != c.want {
			t.Errorf("KeyFor(%d, %q, %d, 0x%X) = %v, %v; want %v", c.table, c.stage, c.room, c.no, got, err, c.want)
		}
	}
	if _, err := KeyFor(0, "", 0, 0xFF); !errors.Is(err, ErrNoSwitch) {
		t.Errorf("0xFF: %v, want ErrNoSwitch", err)
	}
}

// TestScopeRouting checks which writers land on one switch.
func TestScopeRouting(t *testing.T) {
	stages := []*stagedata.Stage{
		stage("A", 3, []stagedata.Actor{crystal(0x05), crystal(0x85), crystal(0xC5), crystal(0xF5)},
			[]stagedata.Actor{crystal(0x05), crystal(0x85), crystal(0xC5), crystal(0xE5)},
			[]stagedata.Actor{crystal(0xC5), crystal(0xE5)}),
		stage("B", 3, nil, []stagedata.Actor{crystal(0x05), crystal(0x85), crystal(0xC5)}),
		stage("C", 4, nil, []stagedata.Actor{crystal(0x05), crystal(0x85)}),
		stage("X", 20, nil, []stagedata.Actor{crystal(0x05), crystal(0xC5)}),
	}
	tbl := Build(stages, testNames(t))
	for _, c := range []struct {
		key     Key
		writers []string // the writers' "stage room"
	}{
		{mem(3, 0x05), []string{"A -1", "A 0", "B 0"}},
		{dan(3, 0x85), []string{"A -1", "A 0", "B 0"}},
		{mem(4, 0x05), []string{"C 0"}},
		{dan(4, 0x85), []string{"C 0"}},
		{zone(3, "A", 0, 0xC5), []string{"A 0"}},
		{zone(3, "A", 1, 0xC5), []string{"A 1"}},
		{zone(3, "B", 0, 0xC5), []string{"B 0"}},
		{roomSw(3, "A", 0, 0xE5), []string{"A 0"}},
		{roomSw(3, "A", 1, 0xE5), []string{"A 1"}},
		{zone(20, "X", 0, 0xC5), []string{"X 0"}},
	} {
		s, ok := tbl.Lookup(c.key)
		if !ok {
			t.Errorf("%v: missing", c.key)
			continue
		}
		var got []string
		for _, w := range s.Writers {
			got = append(got, fmt.Sprintf("%s %d", w.Stage, w.Room))
		}
		if !slices.Equal(got, c.writers) {
			t.Errorf("%v: writers %v, want %v", c.key, got, c.writers)
		}
	}
	if n := len(tbl.Switches); n != 10 {
		t.Errorf("%d switches %v, want 10", n, keysOf(tbl))
	}
	// The stage's own 0xC5 has no zone, 0xF5 is past the stores, and
	// table 20's memory switch has no table.
	var invalid []string
	for _, iv := range tbl.Invalid {
		invalid = append(invalid, fmt.Sprintf("%s %d 0x%02X", iv.Stage, iv.Room, iv.No))
	}
	if want := []string{"A -1 0xC5", "A -1 0xF5", "X 0 0x05"}; !slices.Equal(invalid, want) {
		t.Errorf("invalid %v, want %v", invalid, want)
	}
	// Switches come sorted.
	if !slices.IsSortedFunc(tbl.Switches, func(a, b Switch) int { return compareKey(a.Key, b.Key) }) {
		t.Errorf("switches not sorted: %v", keysOf(tbl))
	}
}

// TestRoomOverrides checks the uses that route with another room than the
// actor's: doors in stage.dzs with their front and back rooms, chests
// with home.angle.x, and an AND in stage.dzs whose zone input has no
// room.
func TestRoomOverrides(t *testing.T) {
	tbl := Build([]*stagedata.Stage{stage("D", 5, []stagedata.Actor{
		act("door10", 0xC6<<20|5<<8|0xC5, 2|3<<6),
		act("takara", 0xC7<<12|2, 4, 0, 0xC8),
		andsw2(0, 0x10, 1, 0xC1),
	})}, testNames(t))
	check(t, tbl,
		want{zone(5, "D", 3, 0xC6), Latching, OpSet, true},
		want{zone(5, "D", 2, 0xC5), "", "", false}, // the front bars read it
		want{zone(5, "D", 4, 0xC7), Latching, OpSet, true},
		want{zone(5, "D", 4, 0xC8), Latching, OpSet, true},
		want{mem(5, 0x10), Unsafe, OpSet, false},
	)
	s, _ := tbl.Lookup(mem(5, 0x10))
	if s == nil || !strings.Contains(s.Writers[0].Why, "input 0xC1") || len(s.Writers[0].Inputs) != 0 {
		t.Errorf("AND with an unroutable input: %+v", s)
	}
}

func TestPushBlocks(t *testing.T) {
	tbl := Build([]*stagedata.Stage{stage("D", 6, nil, []stagedata.Actor{
		act("osiBLK0", 1<<24|0x10<<8, 0, 0, 0x11<<8|0x03),         // path 3, two bits
		act("osiBLK0", 1<<24|0x12<<8, 0, 0, 0xFF<<8|0x04),         // path 4, one bit
		act("osiBLK0", 1<<30|1<<24|0x13<<8, 0, 0, 0x14<<8|0x05),   // dmy: no path, an appear switch
		act("osiBLK0", 1<<24|0x15<<8, 0, 0, 0x16<<8|0xFF),         // no path: an appear switch
		act("osiBLK0", 0xB<<24|0x17<<8, 0, 0, 0x18<<8|0x06),       // the Mkie box
		act("osiBLK0", 0xB<<24|0x19<<8|1<<30, 0, 0, 0x1A<<8|0x06), // the Mkie box with dmy: no Mkie switch
	})}, testNames(t))
	pushes := []PushBlock{
		{Path: 3, Switches: []Key{mem(6, 0x10), mem(6, 0x11)}},
		{Path: 4, Switches: []Key{mem(6, 0x12)}},
	}
	if len(tbl.PushBlocks) != len(pushes) {
		t.Fatalf("push blocks %+v, want %d", tbl.PushBlocks, len(pushes))
	}
	for i, w := range pushes {
		if p := tbl.PushBlocks[i]; p.Path != w.Path || !slices.Equal(p.Switches, w.Switches) || p.Name != "osiBLK0" || p.Index != i {
			t.Errorf("push block %d: %+v, want path %d %v", i, p, w.Path, w.Switches)
		}
	}
	check(t, tbl,
		want{mem(6, 0x10), Unsafe, OpSetClear, false},
		want{mem(6, 0x11), Unsafe, OpSetClear, false},
		want{mem(6, 0x12), Unsafe, OpSetClear, false},
		want{mem(6, 0x13), "", "", false},
		want{mem(6, 0x15), "", "", false},
		want{mem(6, 0x17), "", "", false},
		want{mem(6, 0x18), Latching, OpSet, true},
		want{mem(6, 0x19), "", "", false},
	)
	for _, no := range []int{0x14, 0x16, 0x1A} {
		if _, ok := tbl.Lookup(mem(6, no)); ok {
			t.Errorf("0x%02X is in the table", no)
		}
	}
}

func TestUndecodedAndUnnamed(t *testing.T) {
	st := stage("D", 7, nil, []stagedata.Actor{act("Hfbot1A", 0x1234), act("Nope", 0x05), crystal(0x05)})
	tbl := Build([]*stagedata.Stage{st, nil}, testNames(t))
	if tbl.Stages != 1 || tbl.Users != 2 || tbl.Unnamed != 1 {
		t.Errorf("stages %d, users %d, unnamed %d; want 1, 2, 1", tbl.Stages, tbl.Users, tbl.Unnamed)
	}
	if len(tbl.Undecoded) != 1 || tbl.Undecoded[0].Name != "Hfbot1A" || !strings.Contains(tbl.Undecoded[0].Why, "d_a_obj_swflat.cpp is NonMatching") {
		t.Errorf("undecoded %+v", tbl.Undecoded)
	}
	if len(tbl.Switches) != 1 {
		t.Errorf("switches %v, want the crystal's", keysOf(tbl))
	}
	tbl = Build([]*stagedata.Stage{st}, nil)
	if tbl.Users != 0 || tbl.Unnamed != 3 || len(tbl.Switches) != 0 {
		t.Errorf("nil names: users %d, unnamed %d, %d switches", tbl.Users, tbl.Unnamed, len(tbl.Switches))
	}
	if tbl := Build(nil, nil); tbl.Stages != 0 || len(tbl.Switches) != 0 || tbl.Report() == "" {
		t.Errorf("empty build: %+v", tbl)
	}
}

// sample is a table with something of each kind in it.
func sample(t testing.TB) *Table {
	return Build([]*stagedata.Stage{
		stage("M_NewD2", addr.TableDRC, []stagedata.Actor{act("door10", 0xFF<<20|4<<8|0x12, 1|2<<6)},
			[]stagedata.Actor{crystal(0x05), timedCrystal(0x06), andsw2(0, 0x07, 2, 0x05), act("Hfbot1A", 0), agb("agbA", 0, 0x05, 0xFF)},
			[]stagedata.Actor{act("osiBLK0", 1<<24|0x20<<8, 0, 0, 0x21<<8|0x01), act("ALLdie", 0xC3<<8), act("Bk", 0xA)}),
		stage("M_DaiB", addr.TableDRC, []stagedata.Actor{crystal(0xC9)}, []stagedata.Actor{act("Bdk", 0), act("Htobi1", 0x30)}),
		stage("sea", addr.TableSea, nil, []stagedata.Actor{act("Evsw", 1<<24|0x30), act("Nope", 0)}),
	}, testNames(t))
}

func TestJSONRoundTrip(t *testing.T) {
	tbl := sample(t)
	b, err := json.Marshal(tbl)
	if err != nil {
		t.Fatal(err)
	}
	var back Table
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(&back, tbl) {
		t.Errorf("round trip differs:\n%s", b)
	}
	if !back.Synced(mem(addr.TableDRC, 0x05)) || back.Synced(mem(addr.TableDRC, 0x07)) {
		t.Errorf("verdicts after the round trip: 0x05 %v, 0x07 %v", back.Synced(mem(addr.TableDRC, 0x05)), back.Synced(mem(addr.TableDRC, 0x07)))
	}
	if !strings.Contains(string(b), `"store":"memory"`) || !strings.Contains(string(b), `"class":"latching"`) {
		t.Errorf("JSON doesn't spell stores and classes: %s", b)
	}
	var s Store
	if err := s.UnmarshalText([]byte("nowhere")); err == nil {
		t.Error("unknown store accepted")
	}
	// Building is deterministic, so the cache is stable.
	if again := sample(t); !reflect.DeepEqual(again, tbl) {
		t.Error("two builds differ")
	}
}

func TestReport(t *testing.T) {
	r := sample(t).Report()
	for _, s := range []string{
		"Switch table: 3 stages, 13 switch users (1 entries not in l_objectName)",
		"Writers: 12, latching 5, unsafe 5, event 1, unknown 0, gba 1",
		"  memory      8  synced     2  no setter     1",
		"Synced only while no GBA is linked (shared with the Tingle Tuner): 1 (memory 1, dan 0, zone 0, room 0)",
		"Push blocks storing a path in switches (never synced): 1",
		"  M_NewD2 r1 ACTR#0 osiBLK0 path 1: memory t3 0x20, memory t3 0x21",
		"Undecoded switch users: 1\n  Hfbot1A  1",
		"Switch uses the game can't route: 1\n  M_DaiB stage ACTR#0 SW_HIT0: zone switch 0xC9 from room -1, which has no zone",
		"Dragon Roost Cavern (table 3): 11 switches, 5 synced (ok*: while no GBA is linked)",
		"  ok* memory t3 0x05  set by SW_HIT0; held while a GBA is linked (Tingle Tuner: agbA)",
		"     memory t3 0x07  unsafe writer M_NewD2 r0 ACTR#2 AND_SW2: one-shot AND: set once its inputs are all on; fed by memory t3 0x06, which is not synced",
		"  ok memory t3 0x12  set by door10",
		"  ok zone M_NewD2 r1 0xC3  set by ALLdie",
		"  ok room M_NewD2 r1 0xE0  set by Bk",
		"     dan t3 0x80  unsafe writer M_DaiB r0 ACTR#0 Bdk",
		"     memory t3 0x30  no known setter (read by Htobi1)",
		"Forsaken Fortress (table 2): 0 switches, 0 synced (ok*: while no GBA is linked)",
	} {
		if !strings.Contains(r, s) {
			t.Errorf("report lacks %q:\n%s", s, r)
		}
	}
}

// TestSpecs checks the spec list itself.
func TestSpecs(t *testing.T) {
	seen := make(map[stagedata.Proc]bool)
	for _, sp := range specs {
		if seen[sp.proc] {
			t.Errorf("proc 0x%03X listed twice", uint16(sp.proc))
		}
		seen[sp.proc] = true
		if sp.proc <= 0 || sp.proc >= stagedata.ProcMaxNum || sp.name == "" {
			t.Errorf("spec %+v", sp)
		}
		if (sp.decode == nil) == (sp.undecoded == "") {
			t.Errorf("%s: wants exactly one of decode and undecoded", sp.name)
		}
		if sp.decode == nil {
			continue
		}
		for _, p := range []uint32{0, 0xFFFFFFFF, 0x12345678, 0x80808080} {
			for _, ang := range [][3]int16{{}, {-1, -1, -1}, {0x1234, 0, 0x5678}} {
				for _, u := range sp.decode(entry{stagedata.Actor{Params: p, Angle: ang}}) {
					if u.write && (u.class == "" || u.op == "") || u.why == "" || len(u.inputs) > MaxANDInputs {
						t.Errorf("%s(0x%08X, %v): use %+v", sp.name, p, ang, u)
					}
				}
			}
		}
	}
	// The stagedata procs the design names are all read.
	for _, p := range []stagedata.Proc{
		stagedata.ProcAllDie, stagedata.ProcTagEvsw, stagedata.ProcSwpush, stagedata.ProcSwheavy, stagedata.ProcMovebox,
		stagedata.ProcEP, stagedata.ProcTbox, stagedata.ProcSwc00, stagedata.ProcDoor10, stagedata.ProcDoor12,
		stagedata.ProcAndsw0, stagedata.ProcAndsw2, stagedata.ProcSwhit0, stagedata.ProcObjTimer,
	} {
		if !seen[p] {
			t.Errorf("%v has no spec", p)
		}
	}
}
