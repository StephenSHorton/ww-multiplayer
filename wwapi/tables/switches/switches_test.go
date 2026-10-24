package switches

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/stage"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/stage/stagetest"
)

// realNames are the l_objectName names the tests place, with their procs
// (d_stage.cpp lines in actors.go and wwapi/stage).
var realNames = []struct {
	name string
	proc stage.Proc
}{
	{"SW_HIT0", stage.ProcSwhit0}, {"bonbori", stage.ProcEP}, {"Kbota_A", stage.ProcSwpush},
	{"Hhbot1", stage.ProcSwheavy}, {"ALLdie", stage.ProcAllDie}, {"AND_SW0", stage.ProcAndsw0},
	{"AND_SW2", stage.ProcAndsw2}, {"ObjTime", stage.ProcObjTimer}, {"SW_C00", stage.ProcSwc00},
	{"Evsw", stage.ProcTagEvsw}, {"door10", stage.ProcDoor10}, {"door12", stage.ProcDoor12},
	{"doorKD", procKddoor}, {"SMBdor", procMbdoor}, {"takara", stage.ProcTbox},
	{"osiBLK0", stage.ProcMovebox}, {"Bk", stage.ProcBK}, {"Tn", stage.ProcTN},
	{"TagEv", procTagEvent}, {"Hseki1", procTry}, {"Hfbot1A", procSwflat}, {"Htobi1", procShutter},
	{"Bdk", stage.ProcBDK}, {"Kui", procKui}, {"koisi1", procStone}, {"Warpt", procWarpt},
	{"MhmrSW0", procSwhammer}, {"Fganon", stage.ProcFganon}, {"Mn", procNpcMn},
	{"agbA", procAgbsw0}, {"agbMW", procAgbsw0}, {"agbCSW", procAgbsw0},
	{"Ss", procSs}, {"gmos", stage.ProcGM}, {"CrTrM1", procCorrect}, {"Itnak", procItnak},
	{"Hfbot1B", procSwflat}, {"Hfbot1C", procSwflat}, {"MsuSW", procSwlight},
	{"Sitem", stage.ProcItem}, // a proc the table doesn't read
}

// synthName is the name the tests give spec procs that realNames doesn't.
func synthName(p stage.Proc) string { return fmt.Sprintf("P%03X", uint16(p)) }

func testNames(t testing.TB) *stage.ObjectNames {
	t.Helper()
	var list []stagetest.ObjectName
	for _, n := range realNames {
		list = append(list, stagetest.ObjectName{Name: n.name, Proc: int16(n.proc), Subtype: -1})
	}
	for _, sp := range specs {
		list = append(list, stagetest.ObjectName{Name: synthName(sp.proc), Proc: int16(sp.proc), Subtype: -1})
	}
	names, err := stage.ParseObjectNames(stagetest.ObjectNameTable(list...))
	if err != nil {
		t.Fatal(err)
	}
	return names
}

// act is an ACTR entry; angle is x, y, z, each as its 16 bits.
func act(name string, params uint32, angle ...int) stage.Actor {
	a := stage.Actor{Tag: "ACTR", Layer: stage.AllLayers, Name: name, Params: params}
	for i, v := range angle[:min(len(angle), 3)] {
		a.Angle[i] = int16(uint16(v))
	}
	return a
}

// stage builds a stage of save table table: rooms[i] is room i's entries
// (nil for no room), dzs the stage's own.
func mkStage(name string, table int, dzs []stage.Actor, rooms ...[]stage.Actor) *stage.Stage {
	s := &stage.Stage{Name: name, SaveTable: table, DZS: &stage.DZx{Actors: index(dzs)}}
	for no, as := range rooms {
		if as != nil {
			s.Rooms = append(s.Rooms, stage.Room{No: no, DZR: &stage.DZx{Actors: index(as)}})
		}
	}
	return s
}

func index(as []stage.Actor) []stage.Actor {
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
		actors []stage.Actor
		want   []want
		count  int  // switches the case should produce; -1 not to check
		noUser bool // the entry isn't one the table reads
	}{
		{"crystal", []stage.Actor{act("SW_HIT0", 0xFF<<20|0x05)}, []want{{m(0x05), Latching, OpSet, true}}, 1, false},
		{"crystal timer 0", []stage.Actor{act("SW_HIT0", 0x05)}, []want{{m(0x05), Latching, OpSet, true}}, 1, false},
		{"timed crystal", []stage.Actor{act("SW_HIT0", 0x0A<<20|0x05, 0, 0, 0x06)},
			[]want{{m(0x05), Unsafe, OpSetClear, false}, {m(0x06), "", "", false}}, 2, false},
		{"torch", []stage.Actor{act("bonbori", 0x07<<24|0xFF<<16|0xFF<<8)}, []want{{m(0x07), Latching, OpSet, true}}, 1, false},
		{"timed torch", []stage.Actor{act("bonbori", 0x07<<24|0x08<<16|0x10<<8)},
			[]want{{m(0x07), Unsafe, OpSetClear, false}, {m(0x08), "", "", false}}, 2, false},
		{"floor switch 0", []stage.Actor{act("Kbota_A", 0<<24|0x10<<8)}, []want{{m(0x10), Latching, OpSet, true}}, 1, false},
		{"floor switch 1", []stage.Actor{act("Kbota_A", 1<<24|0x10<<8)}, []want{{m(0x10), Unsafe, OpSetClear, false}}, 1, false},
		{"floor switch 1 v1", []stage.Actor{act("Kbota_A", 1<<30|1<<24|0x10<<8, 0, 0, 0x11)},
			[]want{{m(0x10), Unsafe, OpSetClear, false}, {m(0x11), "", "", false}}, 2, false},
		{"floor switch 2", []stage.Actor{act("Kbota_A", 2<<24|0x10<<8)}, []want{{m(0x10), Unsafe, OpClear, false}}, 1, false},
		{"floor switch 3", []stage.Actor{act("Kbota_A", 3<<24|0x10<<8)}, []want{{m(0x10), Latching, OpSet, true}}, 1, false},
		{"floor switch 5", []stage.Actor{act("Kbota_A", 5<<24|0x10<<8)}, []want{{m(0x10), Unknown, OpSet, false}}, 1, false},
		{"iron boots 0", []stage.Actor{act("Hhbot1", 0<<24|0x10<<8)}, []want{{m(0x10), Unsafe, OpSetClear, false}}, 1, false},
		{"iron boots 1", []stage.Actor{act("Hhbot1", 1<<24|0x10<<8)}, []want{{m(0x10), Unsafe, OpSetClear, false}}, 1, false},
		{"iron boots 2", []stage.Actor{act("Hhbot1", 2<<24|0x10<<8)}, []want{{m(0x10), Unsafe, OpToggle, false}}, 1, false},
		{"iron boots 3", []stage.Actor{act("Hhbot1", 3<<24|0x10<<8)}, []want{{m(0x10), Latching, OpSet, true}}, 1, false},
		{"ALLdie", []stage.Actor{act("ALLdie", 0xC9<<8)}, []want{{z(1, 0xC9), Latching, OpSet, true}}, 1, false},
		{"ObjTime", []stage.Actor{act("ObjTime", 0x0A<<16|30)}, []want{{m(0x0A), Unsafe, OpClear, false}}, 1, false},
		{"SW_C00 0", []stage.Actor{act("SW_C00", 0<<16|0xFF<<8|0x20)}, []want{{m(0x20), Unsafe, OpSetClear, false}}, 1, false},
		{"SW_C00 1", []stage.Actor{act("SW_C00", 1<<16|0x21<<8|0x20)},
			[]want{{m(0x20), Latching, OpSet, true}, {m(0x21), "", "", false}}, 2, false},
		{"Evsw 0", []stage.Actor{act("Evsw", 0<<24|0x1234<<8|0x30)}, []want{{m(0x30), Unsafe, OpSetClear, false}}, 1, false},
		{"Evsw 1", []stage.Actor{act("Evsw", 1<<24|0x1234<<8|0x30)}, []want{{m(0x30), Event, OpSet, false}}, 1, false},
		{"TagEv", []stage.Actor{act("TagEv", 0xFF<<16|0x31<<8)}, []want{{m(0x31), Event, OpSet, false}}, 1, false},
		{"chest", []stage.Actor{act("takara", 0xFF<<12, 5, 0, 0x40)}, []want{{m(0x40), Latching, OpSet, true}}, 1, false},
		{"enemy chest", []stage.Actor{act("takara", 0xC2<<12|2, 5, 0, 0xFF)}, []want{{z(5, 0xC2), Latching, OpSet, true}}, 1, false},
		{"switch chest", []stage.Actor{act("takara", 0x41<<12|1, 5, 0, 0xFF)}, []want{{m(0x41), "", "", false}}, 1, false},
		{"key door10", []stage.Actor{act("door10", 0xFF<<20|4<<8|0x12, 2|3<<6)},
			[]want{{m(0x12), Latching, OpSet, true}}, 1, false},
		{"kill-all door10", []stage.Actor{act("door10", 0xFF<<20|2<<8|0xC5, 2|3<<6)},
			[]want{{z(2, 0xC5), Latching, OpSet, true}}, 1, false},
		{"kill-all back door10", []stage.Actor{act("door10", 0xC6<<20|5<<8|0x13, 2|3<<6)},
			[]want{{z(3, 0xC6), Latching, OpSet, true}, {m(0x13), Latching, OpSet, true}}, 2, false},
		{"high key door10", []stage.Actor{act("door10", 0xFF<<20|4<<8|0x85, 2|3<<6)},
			[]want{{dan(tb, 0x85), "", "", false}}, 1, false},
		{"key door12", []stage.Actor{act("door12", 1<<8|0x14, 0)}, []want{{m(0x14), Latching, OpSet, true}}, -1, false},
		{"boss door12", []stage.Actor{act("door12", 0<<8|0x15, 0, 0, 9<<8)}, []want{{m(0x15), Latching, OpSet, true}}, -1, false},
		{"key doorKD", []stage.Actor{act("doorKD", 0xC7<<20|2<<8|0x16, 1|4<<6)},
			[]want{{m(0x16), Latching, OpSet, true}, {z(4, 0xC7), Latching, OpSet, true}}, 2, false},
		{"mini-boss door", []stage.Actor{act("SMBdor", 0x17, 0, 0, 1)}, []want{{m(0x17), Latching, OpSet, true}}, 1, false},
		{"mini-boss door reader", []stage.Actor{act("SMBdor", 0x17, 0, 0, 0)}, []want{{m(0x17), "", "", false}}, 1, false},
		{"Bokoblin", []stage.Actor{act("Bk", 0, 0, 0, 0x22)}, []want{{m(0x22), Latching, OpSet, true}}, 1, false},
		{"Bokoblin none", []stage.Actor{act("Bk", 0, 0, 0, 0xFF)}, nil, 0, false},
		{"boss Bokoblin", []stage.Actor{act("Bk", 0xA)},
			[]want{{roomSw(tb, "D", 1, 0xE0), Latching, OpSet, true}, {roomSw(tb, "D", 1, 0xE1), Latching, OpSet, true}}, 2, false},
		// FW's door plants (kindan r5 #1: 0x13010000) and Mothulas (kinMB
		// #35 0x19000000 and r15 #141 0xA0010000 with angle.z 1; r9 #2
		// 0xFF010000).
		{"door plant", []stage.Actor{act("Ss", 0x13010000)}, []want{{m(0x13), Latching, OpSet, true}}, 1, false},
		{"chest plant", []stage.Actor{act("Ss", 0x01000000)}, []want{{m(0x01), Latching, OpSet, true}}, 1, false},
		{"door plant 0xFF", []stage.Actor{act("Ss", 0xFF010000)}, nil, 0, false},
		{"door plant 0", []stage.Actor{act("Ss", 0x00010000)}, nil, 0, false},
		{"Mothula", []stage.Actor{act("gmos", 0x19000000, 0, 0, 1)}, []want{{m(0x19), Latching, OpSet, true}}, 1, false},
		{"Mothula dan", []stage.Actor{act("gmos", 0xA0010000, 0, 0, 1)}, []want{{dan(tb, 0xA0), Latching, OpSet, true}}, 1, false},
		{"Mothula byte 1 0xFF", []stage.Actor{act("gmos", 0x1900FF00, 0, 0, 1)}, []want{{m(0x19), Latching, OpSet, true}}, 1, false},
		{"Mothula switch 0", []stage.Actor{act("gmos", 0x00000000, 0, 0, 0x0301)}, []want{{m(0x00), Latching, OpSet, true}}, 1, false},
		{"Mothula angle.z 0", []stage.Actor{act("gmos", 0x19000000, 0, 0, 0x0100)}, nil, 0, false},
		{"Mothula byte 1 set", []stage.Actor{act("gmos", 0x19000100, 0, 0, 1)}, nil, 0, false},
		{"Mothula none", []stage.Actor{act("gmos", 0xFF010000, 0, 0, 1)}, nil, 0, false},
		{"Mothula 0xFF", []stage.Actor{act("gmos", 0xFF000000, 0, 0, 1)}, nil, 0, false},
		{"Helmaroc", []stage.Actor{act("Bdk", 0)}, []want{{dan(tb, 0x80), Unsafe, OpSetClear, false}}, 1, false},
		{"Phantom Ganon", []stage.Actor{act("Fganon", 0x24<<24|0x23<<16)},
			[]want{{m(0x23), Latching, OpSet, true}, {m(0x24), Unsafe, OpSetClear, false}}, 2, false},
		{"post 0xFF", []stage.Actor{act("Kui", 0xFF<<24)}, []want{{m(0x00), Latching, OpSet, true}}, 1, false},
		{"no post", []stage.Actor{act("Kui", 0xFFFFFFFF)}, nil, 0, false},
		{"rock 2", []stage.Actor{act("koisi1", 2<<24|0x25<<8)}, []want{{m(0x25), Latching, OpSet, true}}, 1, false},
		{"rock 0", []stage.Actor{act("koisi1", 0<<24|0x25<<8)}, nil, 0, false},
		{"rock 6", []stage.Actor{act("koisi1", 6<<24|0x25<<8)}, []want{{m(0x25), Unknown, OpSet, false}}, 1, false},
		{"warp jar", []stage.Actor{act("Warpt", 3, 0x26)}, []want{{m(0x26), "", "", false}}, 1, false},
		{"warp pot", []stage.Actor{act("Warpt", 0, 0xFF26)}, []want{{m(0x26), Latching, OpSet, true}}, 1, false},
		{"hammer switch", []stage.Actor{act("MhmrSW0", 0x27<<8|0x28)},
			[]want{{m(0x27), Latching, OpSet, true}, {m(0x28), Unsafe, OpClear, false}}, 2, false},
		// Obj_Try only reads, and only types 5 and 6 (TotG r0's Hmon1d
		// 80000505, r4's Hmon1 00000505).
		{"Obj_Try 5", []stage.Actor{act("Hseki1", 0x29<<8|5)}, []want{{m(0x29), "", "", false}}, 1, false},
		{"Obj_Try 6 twin", []stage.Actor{act("Hseki1", 1<<31|0x29<<8|6)}, []want{{m(0x29), "", "", false}}, 1, false},
		{"Obj_Try 0", []stage.Actor{act("Hseki1", 0x29<<8)}, nil, 0, false},
		{"Obj_Try 12", []stage.Actor{act("Hseki1", 0x29<<8|12)}, nil, 0, false},
		{"Obj_Try 13", []stage.Actor{act("Hseki1", 0x29<<8|13)}, []want{{m(0x29), "", "", false}}, 1, false},
		// Obj_Swflat (TotG's Hfbot1B/C): type 1 and 2 clear when released,
		// 2 reads sw2, 0 and 3 stay down.
		{"swflat 1", []stage.Actor{act("Hfbot1A", 0x00001001)}, []want{{m(0x10), Unsafe, OpSetClear, false}}, 1, false},
		{"swflat 2", []stage.Actor{act("Hfbot1A", 0x000C3202)},
			[]want{{m(0x32), Unsafe, OpSetClear, false}, {m(0x0C), "", "", false}}, 2, false},
		{"swflat 2 without sw2", []stage.Actor{act("Hfbot1A", 0x00FF2502)}, []want{{m(0x25), Unsafe, OpSetClear, false}}, 1, false},
		{"swflat 1 ignores sw2", []stage.Actor{act("Hfbot1A", 0x000C1001)}, []want{{m(0x10), Unsafe, OpSetClear, false}}, 1, false},
		{"swflat 0", []stage.Actor{act("Hfbot1A", 0x000C1000)}, []want{{m(0x10), Latching, OpSet, true}}, 1, false},
		{"swflat 3", []stage.Actor{act("Hfbot1A", 0x000C1003)}, []want{{m(0x10), Latching, OpSet, true}}, 1, false},
		{"swflat room switch", []stage.Actor{act("Hfbot1A", 0x0020E002)},
			[]want{{roomSw(tb, "D", 1, 0xE0), Unsafe, OpSetClear, false}, {m(0x20), "", "", false}}, 2, false},
		{"shutter", []stage.Actor{act("Htobi1", 0x2A)}, []want{{m(0x2A), "", "", false}}, 1, false},
		{"Manny", []stage.Actor{act("Mn", 0x2C<<16|0x2B<<8)},
			[]want{{m(0x2C), Latching, OpSet, true}, {m(0x2B), "", "", false}}, 2, false},
		{"not a switch user", []stage.Actor{act("Sitem", 0x05)}, nil, 0, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			tbl := Build([]*stage.Stage{mkStage("D", tb, nil, nil, c.actors)}, testNames(t))
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
func andsw2(typ, out, num, first int) stage.Actor {
	return act("AND_SW2", uint32(first)<<24|uint32(out)<<16|uint32(typ)<<8|uint32(num))
}

// andsw0 is an AND_SW0 entry: behaviour beh, output out, num inputs from
// first (0: out+1).
func andsw0(beh, out, num, first int) stage.Actor {
	return act("AND_SW0", uint32(out)<<24|uint32(first)<<16|uint32(beh)<<8|uint32(num))
}

func crystal(no int) stage.Actor { return act("SW_HIT0", 0xFF<<20|uint32(no)) }

// plate is a momentary floor switch (swpush type 1), as DRC r16's Kbota_B.
func plate(no int) stage.Actor { return act("Kbota_A", 1<<24|uint32(no)<<8) }

// sun is a sun switch (Obj_Swlight type 1) with no hold switch.
func sun(no int) stage.Actor { return act("MsuSW", 1<<16|0xFF<<8|uint32(no)) }

// area is a per-frame area switch (SW_C00 type 0).
func area(no int) stage.Actor { return act("SW_C00", 0<<16|0xFF<<8|uint32(no)) }
func timedCrystal(no int) stage.Actor {
	return act("SW_HIT0", 0x10<<20|uint32(no), 0, 0, 0xFF) // no stop switch
}

// timedTorch is a torch with a timer and no stop switch: an unsafe writer
// that is no momentary input.
func timedTorch(no int) stage.Actor { return act("bonbori", uint32(no)<<24|0xFF<<16|0x10<<8) }

func TestANDPropagation(t *testing.T) {
	const tb = addr.TableFW
	m := func(no int) Key { return mem(tb, no) }
	for _, c := range []struct {
		name   string
		actors []stage.Actor
		want   []want
		fedBy  map[int]string // switch -> substring of its first writer's Why
	}{
		{"chain", []stage.Actor{crystal(0x11), crystal(0x12), andsw2(0, 0x10, 2, 0xFF), andsw0(0, 0x20, 1, 0x10)},
			[]want{{m(0x10), Latching, OpSet, true}, {m(0x20), Latching, OpSet, true}}, nil},
		{"unsafe input", []stage.Actor{crystal(0x11), timedTorch(0x12), andsw2(0, 0x10, 2, 0xFF), andsw0(0, 0x20, 1, 0x10)},
			[]want{{m(0x10), Unsafe, OpSet, false}, {m(0x20), Unsafe, OpSet, false}, {m(0x11), Latching, OpSet, true}},
			map[int]string{0x10: "fed by memory t4 0x12", 0x20: "fed by memory t4 0x10"}},
		{"input with no setter", []stage.Actor{andsw2(0, 0x50, 1, 0xFF)},
			[]want{{m(0x50), Unsafe, OpSet, false}, {m(0x51), "", "", false}}, map[int]string{0x50: "fed by memory t4 0x51"}},
		{"cycle", []stage.Actor{andsw0(0, 0x30, 1, 0x31), andsw0(0, 0x31, 1, 0x30), crystal(0x31)},
			[]want{{m(0x30), Latching, OpSet, true}, {m(0x31), Latching, OpSet, true}}, nil},
		{"cycle with unsafe input", []stage.Actor{andsw2(0, 0x40, 2, 0x41), andsw2(0, 0x41, 1, 0x40), act("ObjTime", 0x42<<16|5)},
			[]want{{m(0x40), Unsafe, OpSet, false}, {m(0x41), Unsafe, OpSet, false}, {m(0x42), Unsafe, OpClear, false}},
			map[int]string{0x40: "fed by memory t4 0x42", 0x41: "fed by memory t4 0x40"}},
		{"self-feeding", []stage.Actor{andsw0(0, 0x60, 1, 0x60)}, []want{{m(0x60), Latching, OpSet, true}}, nil},
		{"continuous andsw2", []stage.Actor{crystal(0x71), andsw2(1, 0x70, 1, 0xFF)},
			[]want{{m(0x70), Unsafe, OpSetClear, false}}, nil},
		{"continuous andsw0", []stage.Actor{crystal(0x71), andsw0(1, 0x70, 1, 0)},
			[]want{{m(0x70), Unsafe, OpSetClear, false}}, nil},
		{"delayed andsw0", []stage.Actor{crystal(0x71), andsw0(3, 0x70, 1, 0)}, []want{{m(0x70), Latching, OpSet, true}}, nil},
		{"timed andsw0", []stage.Actor{crystal(0x71), crystal(0x72), andsw0(2, 0x70, 2, 0)},
			[]want{{m(0x70), Unsafe, OpSet, false}, {m(0x71), Latching, OpSet, false}, {m(0x72), Latching, OpSet, false}},
			map[int]string{0x70: "fed by memory t4 0x71"}},
		{"andsw0 Forest Haven", []stage.Actor{andsw0(0, 0x70, 0xFF, 0)}, nil, nil},
		{"andsw2 no output", []stage.Actor{andsw2(0, 0xFF, 1, 0x10)}, nil, nil},
		{"andsw2 other type", []stage.Actor{andsw2(2, 0x70, 1, 0x71)}, nil, nil},
		{"too many inputs", []stage.Actor{andsw2(0, 0x70, MaxANDInputs+1, 0x00)}, []want{{m(0x70), Unknown, OpSet, false}}, nil},
		// A one-shot AND latches over momentary inputs (DRC sw41: the AND
		// of r16's two Kbota_B room switches), though they never sync.
		{"one-shot AND of floor switches", []stage.Actor{plate(0x11), plate(0x12), andsw0(0, 0x10, 2, 0x11)},
			[]want{{m(0x10), Latching, OpSet, true}, {m(0x11), Unsafe, OpSetClear, false}, {m(0x12), Unsafe, OpSetClear, false}}, nil},
		{"one-shot AND of room floor switches", []stage.Actor{plate(0xE0), plate(0xE1), andsw0(0, 0x29, 2, 0xE0)},
			[]want{{m(0x29), Latching, OpSet, true}}, nil},
		{"delayed andsw0 of a floor switch", []stage.Actor{plate(0x71), andsw0(3, 0x70, 1, 0)}, []want{{m(0x70), Latching, OpSet, true}}, nil},
		{"one-shot andsw2 of iron boots", []stage.Actor{act("Hhbot1", 0<<24|0x11<<8), andsw2(0, 0x10, 1, 0xFF)},
			[]want{{m(0x10), Latching, OpSet, true}}, nil},
		{"chain over a floor switch", []stage.Actor{plate(0x11), andsw2(0, 0x10, 1, 0xFF), andsw0(0, 0x20, 1, 0x10)},
			[]want{{m(0x10), Latching, OpSet, true}, {m(0x20), Latching, OpSet, true}}, nil},
		{"continuous AND of a floor switch", []stage.Actor{plate(0x71), andsw0(1, 0x70, 1, 0)},
			[]want{{m(0x70), Unsafe, OpSetClear, false}}, nil},
		// An inverted floor switch (swpush type 2) only clears, so it is no
		// momentary input and its unmodelled setter keeps the AND unsafe.
		{"one-shot AND of an inverted floor switch", []stage.Actor{act("Kbota_A", 2<<24|0x11<<8), andsw0(0, 0x10, 1, 0x11)},
			[]want{{m(0x10), Unsafe, OpSet, false}}, map[int]string{0x10: "fed by memory t4 0x11"}},
		// The timed AND clears its inputs, so they aren't momentary.
		{"timed andsw0 of a floor switch", []stage.Actor{plate(0x71), andsw0(2, 0x70, 1, 0)},
			[]want{{m(0x70), Unsafe, OpSet, false}}, map[int]string{0x70: "fed by memory t4 0x71"}},
		// An area switch is no momentary input: a partner walking in
		// mustn't spring this game's ambush.
		{"one-shot AND of an area switch", []stage.Actor{area(0x11), andsw0(0, 0x10, 1, 0x11)},
			[]want{{m(0x10), Unsafe, OpSet, false}}, map[int]string{0x10: "fed by memory t4 0x11"}},
		{"floor switch and timed torch", []stage.Actor{plate(0x11), timedTorch(0x12), andsw0(0, 0x10, 2, 0x11)},
			[]want{{m(0x10), Unsafe, OpSet, false}}, map[int]string{0x10: "fed by memory t4 0x12"}},
		{"floor switch and timed crystal", []stage.Actor{plate(0x11), timedCrystal(0x12), andsw0(0, 0x10, 2, 0x11)},
			[]want{{m(0x10), Latching, OpSet, true}}, nil},
		{"latching mixed with AND", []stage.Actor{crystal(0x70), timedTorch(0x71), andsw2(0, 0x70, 1, 0x71)},
			[]want{{m(0x70), Latching, OpSet, false}}, nil},
		// Timed crystals are momentary inputs too (FW r13 sw7, kinMB
		// sw226): a one-shot AND of them latches, a continuous or timed
		// one doesn't.
		{"one-shot AND of timed crystals", []stage.Actor{timedCrystal(0x11), timedCrystal(0x12), andsw0(0, 0x10, 2, 0x11)},
			[]want{{m(0x10), Latching, OpSet, true}, {m(0x11), Unsafe, OpSetClear, false}, {m(0x12), Unsafe, OpSetClear, false}}, nil},
		{"one-shot andsw2 of a timed crystal", []stage.Actor{timedCrystal(0x11), andsw2(0, 0x10, 1, 0xFF)},
			[]want{{m(0x10), Latching, OpSet, true}}, nil},
		{"continuous AND of timed crystals", []stage.Actor{timedCrystal(0x71), timedCrystal(0x72), andsw0(1, 0x70, 2, 0)},
			[]want{{m(0x70), Unsafe, OpSetClear, false}}, nil},
		{"timed andsw0 of timed crystals", []stage.Actor{timedCrystal(0x71), timedCrystal(0x72), andsw0(2, 0x70, 2, 0)},
			[]want{{m(0x70), Unsafe, OpSet, false}}, map[int]string{0x70: "fed by memory t4 0x71"}},
		{"timed crystal a switch timer clears", []stage.Actor{timedCrystal(0x11), act("ObjTime", 0x11<<16|5), andsw0(0, 0x10, 1, 0x11)},
			[]want{{m(0x10), Unsafe, OpSet, false}}, map[int]string{0x10: "fed by memory t4 0x11"}},
		{"timed crystal and its AND's clear", []stage.Actor{timedCrystal(0x11), andsw0(2, 0x20, 1, 0x11), andsw0(0, 0x10, 1, 0x11)},
			[]want{{m(0x10), Unsafe, OpSet, false}}, map[int]string{0x10: "fed by memory t4 0x11"}},
		// A type-1 TAG_EVSW next to a latching setter only pre-sets it from
		// a synced story bit (WT r15's Makar-tree tags), so the switch
		// syncs; next to a clearer it stays unsafe. A TagEv co-writer still
		// keeps it unsynced.
		{"Evsw 1 and a crystal", []stage.Actor{act("Evsw", 1<<24|0x4004<<8|0x32), crystal(0x32)}, []want{{m(0x32), Event, OpSet, true}}, nil},
		{"Evsw 1 and a timer", []stage.Actor{act("Evsw", 1<<24|0x4004<<8|0x33), act("ObjTime", 0x33<<16|5)}, []want{{m(0x33), Event, OpSet, false}}, nil},
		{"Evsw 0 and a crystal", []stage.Actor{act("Evsw", 0<<24|0x4004<<8|0x34), crystal(0x34)}, []want{{m(0x34), Unsafe, OpSetClear, false}}, nil},
		{"TagEv and a crystal", []stage.Actor{act("TagEv", 0xFF<<16|0x35<<8), crystal(0x35)}, []want{{m(0x35), Event, OpSet, false}}, nil},
		// ET's r3 and r16 sun-switch pairs (MsuSW type 1) are momentary
		// inputs: the light leaving clears them. A moon switch (type 0)
		// latches and syncs itself.
		{"one-shot AND of sun switches", []stage.Actor{sun(0x11), sun(0x12), andsw0(0, 0x10, 2, 0x11)},
			[]want{{m(0x10), Latching, OpSet, true}, {m(0x11), Unsafe, OpSetClear, false}, {m(0x12), Unsafe, OpSetClear, false}}, nil},
		{"continuous AND of sun switches", []stage.Actor{sun(0x71), sun(0x72), andsw0(1, 0x70, 2, 0)},
			[]want{{m(0x70), Unsafe, OpSetClear, false}}, nil},
		{"moon switch", []stage.Actor{act("MsuSW", 0xFF<<8|0x13)}, []want{{m(0x13), Latching, OpSet, true}}, nil},
	} {
		t.Run(c.name, func(t *testing.T) {
			tbl := Build([]*stage.Stage{mkStage("W", tb, nil, c.actors)}, testNames(t))
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
// latching setter, the rest latching, GBA or a type-1 TAG_EVSW", a
// latching AND's inputs are
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
				if is, _ := tbl.Lookup(in); !tbl.Synced(in) && !(w.Op == OpSet && is != nil && momentary(is)) {
					t.Errorf("%v: latching AND %v has unsynced input %v", s.Key, w.Ref, in)
				}
				if is, _ := tbl.Lookup(in); tbl.GBADependent(in) || is != nil && momentary(is) && slices.ContainsFunc(is.Writers, func(w Writer) bool { return w.Class == GBA }) {
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
func agb(name string, typ, sw0, sw1 int) stage.Actor {
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
		actors []stage.Actor
		key    Key
		synced bool
		gba    bool
		reason string
	}{
		// The owner's DRC key doors: a key door's switch the Tuner also
		// names.
		{"key door and agbA", []stage.Actor{act("door10", 0xFF<<20|4<<8|0x3D), agb("agbA", 0, 0x3D, 0xFF)},
			m(0x3D), true, true, "gba writer D r0 ACTR#1 agbA writes it only while a GBA is linked"},
		{"crystal and agbMW", []stage.Actor{crystal(0x10), agb("agbMW", 7, 0x10, 0xFF)},
			m(0x10), true, true, "so it syncs only while none is"},
		{"crystal and agbCSW's sw1", []stage.Actor{crystal(0x11), agb("agbCSW", 8, 0xFF, 0x11)},
			m(0x11), true, true, "every setter latches"},
		{"agbA alone", []stage.Actor{agb("agbA", 0, 0x12, 0xFF)},
			m(0x12), false, false, "no latching setter: only gba writers (D r0 ACTR#0 agbA"},
		{"agbA and a timed crystal", []stage.Actor{timedCrystal(0x13), agb("agbA", 0, 0x13, 0xFF)},
			m(0x13), false, false, "unsafe writer D r0 ACTR#0 SW_HIT0"},
		// A type past the table's 15 entries isn't known to need a link.
		{"crystal and a type past ExeSubTable", []stage.Actor{crystal(0x14), agb("agbA", agbsw0Types, 0x14, 0xFF)},
			m(0x14), false, false, "unknown writer D r0 ACTR#1 agbA: Tingle Tuner switch type 15 is past ExeSubTable's 15 entries"},
		{"the last type in the table", []stage.Actor{crystal(0x15), agb("agbA", agbsw0Types-1, 0x15, 0xFF)},
			m(0x15), true, true, "gba writer"},
		// An AND fed by a GBA-dependent input is GBA-dependent too.
		{"AND of a GBA input", []stage.Actor{crystal(0x21), agb("agbA", 0, 0x21, 0xFF), andsw2(0, 0x20, 1, 0xFF)},
			m(0x20), true, true, "an AND input syncs only while no GBA is linked"},
		{"AND of an AND of a GBA input", []stage.Actor{crystal(0x31), agb("agbA", 0, 0x31, 0xFF), andsw2(0, 0x30, 1, 0xFF), andsw0(0, 0x40, 1, 0x30)},
			m(0x40), true, true, "an AND input"},
		{"AND of plain inputs", []stage.Actor{crystal(0x51), andsw2(0, 0x50, 1, 0xFF)},
			m(0x50), true, false, "every setter latches"},
		// Dan, zone and room switches route as any other.
		{"dan", []stage.Actor{crystal(0x85), agb("agbA", 0, 0x85, 0xFF)}, dan(tb, 0x85), true, true, "gba writer"},
		{"zone", []stage.Actor{crystal(0xC5), agb("agbA", 0, 0xFF, 0xC5)}, zone(tb, "D", 0, 0xC5), true, true, "gba writer"},
	} {
		t.Run(c.name, func(t *testing.T) {
			tbl := Build([]*stage.Stage{mkStage("D", tb, nil, c.actors)}, testNames(t))
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
	tbl := Build([]*stage.Stage{mkStage("D", tb, nil, []stage.Actor{agb("agbA", 0, 0x60, 0xFF)})}, testNames(t))
	if len(tbl.Switches) != 1 {
		t.Errorf("switches %v, want only 0x60", keysOf(tbl))
	}
}

// TestMomentaryGBA covers momentary inputs a Tingle Tuner writer shares:
// the GBA writer leaves the input momentary, and the one-shot AND it feeds
// syncs only while no GBA is linked (GBA), as FW r13's sw7 does over sw9
// (an agbA2 in r14 shares it; the tests name every Tuner agbA).
func TestMomentaryGBA(t *testing.T) {
	const tb = addr.TableFW
	m := func(no int) Key { return mem(tb, no) }
	// stopped is a 90-frame crystal of switch no that stop holds on, as
	// kindan r13's (params 0x0060FF08..0C, angle.z 7).
	stopped := func(no, stop int) stage.Actor { return act("SW_HIT0", 0x06<<20|0xFF<<8|uint32(no), 0, 0, stop) }
	for _, c := range []struct {
		name        string
		actors      []stage.Actor
		key         Key
		synced, gba bool
	}{
		{"floor switch a GBA shares", []stage.Actor{plate(0x11), agb("agbA", 0, 0x11, 0xFF), andsw0(0, 0x10, 1, 0x11)}, m(0x10), true, true},
		{"timed crystal a GBA shares", []stage.Actor{timedCrystal(0x11), agb("agbA", 0, 0x11, 0xFF), andsw0(0, 0x10, 1, 0x11)}, m(0x10), true, true},
		{"timed crystals, no GBA", []stage.Actor{timedCrystal(0x11), timedCrystal(0x12), andsw0(0, 0x10, 2, 0x11)}, m(0x10), true, false},
		{"chain over a GBA-shared crystal", []stage.Actor{timedCrystal(0x11), agb("agbA", 0, 0x11, 0xFF), andsw0(0, 0x10, 1, 0x11), andsw0(0, 0x20, 1, 0x10)}, m(0x20), true, true},
		{"only a GBA writer", []stage.Actor{agb("agbA", 0, 0x11, 0xFF), andsw0(0, 0x10, 1, 0x11)}, m(0x10), false, false},
		{"FW r13 sw7", []stage.Actor{
			stopped(0x08, 0x07), stopped(0x09, 0x07), stopped(0x0A, 0x07), stopped(0x0B, 0x07), stopped(0x0C, 0x07),
			act("AND_SW0", 0x07FF0005, 0xFF, 0x8000, 0xFFFF), agb("agbA", 0, 0x07, 0xFF), agb("agbA", 0, 0x09, 0xFF),
		}, m(0x07), true, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			tbl := Build([]*stage.Stage{mkStage("W", tb, nil, c.actors)}, testNames(t))
			s, ok := tbl.Lookup(c.key)
			if !ok {
				t.Fatalf("%v missing; have %v", c.key, keysOf(tbl))
			}
			if s.Synced != c.synced || s.GBA != c.gba {
				t.Errorf("%v: synced %v GBA %v, want %v %v (%s)", c.key, s.Synced, s.GBA, c.synced, c.gba, s.Reason)
			}
			checkFixedPoint(t, tbl)
		})
	}
}

func TestMixedSetters(t *testing.T) {
	const tb = addr.TableET
	for _, c := range []struct {
		name   string
		stages []*stage.Stage
		synced bool
		n      int // writers
		reason string
	}{
		{"two latching in two stages", []*stage.Stage{
			mkStage("A", tb, nil, []stage.Actor{crystal(0x05)}),
			mkStage("B", tb, nil, []stage.Actor{act("ALLdie", 0x05<<8)}),
		}, true, 2, "every setter latches"},
		{"latching and timed", []*stage.Stage{
			mkStage("A", tb, nil, []stage.Actor{crystal(0x05), timedCrystal(0x05)}),
		}, false, 2, "unsafe writer A r0 ACTR#1 SW_HIT0: timed crystal"},
		{"latching and event", []*stage.Stage{
			mkStage("A", tb, nil, []stage.Actor{crystal(0x05)}, []stage.Actor{act("TagEv", 0x05<<8)}),
		}, false, 2, "event writer A r1"},
		{"latching and unknown", []*stage.Stage{
			mkStage("A", tb, []stage.Actor{act("koisi1", 6<<24|0x05<<8)}, []stage.Actor{crystal(0x05)}),
		}, false, 2, "unknown writer A stage ACTR#0 koisi1"},
		{"latching and push block", []*stage.Stage{
			mkStage("A", tb, nil, []stage.Actor{crystal(0x05), act("osiBLK0", 1<<24|0x05<<8, 0, 0, 0x02)}),
		}, false, 2, "push block path 2"},
		{"latching and Tingle Tuner in another stage", []*stage.Stage{
			mkStage("A", tb, nil, []stage.Actor{crystal(0x05)}),
			mkStage("B", tb, nil, []stage.Actor{agb("agbA", 0, 0x05, 0xFF)}),
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

// swflat is an Obj_Swflat (Hfbot1B/C) of type typ on switch sw, with sw2
// (type 2's latch).
func swflat(typ, sw, sw2 int) stage.Actor {
	return act("Hfbot1A", uint32(sw2)<<16|uint32(sw)<<8|uint32(typ))
}

// TestSwflat covers Obj_Swflat as TotG places it (params from the disc):
// its momentary switches feed one-shot ANDs that sync, and a bit one
// shares with a latching setter keeps syncing (sharedFloorSwitches).
func TestSwflat(t *testing.T) {
	const tb = addr.TableTotG
	m := func(no int) Key { return mem(tb, no) }
	// The stage's own key door r8-r10 (TGDR#11 keyshut 0FFFF40E), boss-key
	// chest (TRES#11 takara4, open switch sw16) and r22 Joy Pendant chest
	// (TRES#4 tkrAGc, sw17), here as ACTR entries; the rooms' floor
	// switches, ANDs and the other sw0 and sw28 setters.
	dzs := []stage.Actor{
		act("door10", 0x0FFFF40E, 8|10<<6),
		act("takara", 0xFF300000, 17, 0, 0x10),
		act("takara", 0xFF009302, 22, 0, 0x11),
	}
	rooms := make([][]stage.Actor, 13)
	rooms[0] = []stage.Actor{
		act("Hfbot1C", 0x001C0002), act("Hfbot1C", 0x000C3202), act("Hfbot1C", 0x000C3302),
		act("AND_SW0", 0x0C320002), act("Kui", 0xFF<<24),
	}
	rooms[4] = []stage.Actor{act("SW_C00", 1<<16|0xFF<<8|0x1C), act("Hfbot1B", 0x00002301)}
	rooms[6] = []stage.Actor{act("Hfbot1C", 0x0020E002), act("Hfbot1C", 0x0020E102), act("Hfbot1C", 0x0020E202), act("AND_SW0", 0x20E00003)}
	rooms[12] = []stage.Actor{act("Hfbot1B", 0x00000E01), act("Hfbot1B", 0x00001001), act("AND_SW0", 0x0FFF0102), act("Hfbot1B", 0x00001101)}
	tbl := Build([]*stage.Stage{mkStage("Siren", tb, dzs, rooms...)}, testNames(t))
	checkFixedPoint(t, tbl)
	check(t, tbl,
		// The r0 pair's one-shot AND opens the r0-r5 door, the r6 triple's
		// raises the lifts; the presses don't sync.
		want{m(0x0C), Latching, OpSet, true},
		want{m(0x32), Unsafe, OpSetClear, false},
		want{m(0x33), Unsafe, OpSetClear, false},
		want{m(0x20), Latching, OpSet, true},
		want{roomSw(tb, "Siren", 6, 0xE0), Unsafe, OpSetClear, false},
		// A floor switch alone on its bit stays local (the r4 bridge).
		want{m(0x23), Unsafe, OpSetClear, false},
		// sw28, r0 #18's latch, syncs, and the r12 continuous AND doesn't.
		want{m(0x1C), Latching, OpSet, true},
		want{m(0x0F), Unsafe, OpSetClear, false},
	)
	for _, c := range []struct {
		no, n  int    // switch, writers
		swflat string // the swflat's index in "Siren rN ACTR#i"
		by     string
	}{
		{0x00, 2, "Siren r0 ACTR#0", "Siren r0 ACTR#4 Kui"},
		{0x0E, 2, "Siren r12 ACTR#0", "Siren stage ACTR#0 door10"},
		{0x10, 2, "Siren r12 ACTR#1", "Siren stage ACTR#1 takara"},
		{0x11, 2, "Siren r12 ACTR#3", "Siren stage ACTR#2 takara"},
	} {
		s, _ := tbl.Lookup(m(c.no))
		if s == nil || !s.Synced || len(s.Writers) != c.n {
			t.Errorf("0x%02X: %+v, want synced with %d writers", c.no, s, c.n)
			continue
		}
		for _, w := range s.Writers {
			if w.Proc != procSwflat {
				continue
			}
			if !strings.HasPrefix(w.Ref.String(), c.swflat) || w.Class != Latching || w.Op != OpSetClear || !strings.Contains(w.Why, "latching setter "+c.by) {
				t.Errorf("0x%02X: swflat writer %v %s %s %q, want %s latching set+clear by %s", c.no, w.Ref, w.Class, w.Op, w.Why, c.swflat, c.by)
			}
		}
	}
}

// TestSwflatShared covers sharedFloorSwitches' guards: only a swflat is
// counted as latching, only beside a latching setter that is not an AND,
// and an unsafe writer beside them still keeps the switch unsynced.
func TestSwflatShared(t *testing.T) {
	const tb = addr.TableTotG
	m := func(no int) Key { return mem(tb, no) }
	for _, c := range []struct {
		name   string
		actors []stage.Actor
		synced bool
		class  Class // the floor switch's
	}{
		{"beside a crystal", []stage.Actor{swflat(1, 0x10, 0), crystal(0x10)}, true, Latching},
		{"type 2 beside a crystal", []stage.Actor{swflat(2, 0x10, 0x11), crystal(0x10)}, true, Latching},
		{"beside a Tuner switch only", []stage.Actor{swflat(1, 0x10, 0), agb("agbA", 0, 0x10, 0xFF)}, false, Unsafe},
		{"beside a one-shot AND", []stage.Actor{swflat(1, 0x10, 0), crystal(0x11), andsw0(0, 0x10, 1, 0x11)}, false, Unsafe},
		{"beside another swflat", []stage.Actor{swflat(1, 0x10, 0), swflat(0, 0x10, 0)}, false, Unsafe},
		{"beside a crystal and a timed torch", []stage.Actor{swflat(1, 0x10, 0), crystal(0x10), timedTorch(0x10)}, false, Latching},
		// The rule is swflat's: a swpush keeps its verdict.
		{"swpush beside a crystal", []stage.Actor{plate(0x10), crystal(0x10)}, false, Unsafe},
	} {
		t.Run(c.name, func(t *testing.T) {
			tbl := Build([]*stage.Stage{mkStage("Siren", tb, nil, c.actors)}, testNames(t))
			checkFixedPoint(t, tbl)
			s, ok := tbl.Lookup(m(0x10))
			if !ok {
				t.Fatalf("0x10 missing: %v", keysOf(tbl))
			}
			if s.Synced != c.synced {
				t.Errorf("synced %v, want %v (%s)", s.Synced, c.synced, s.Reason)
			}
			if w := s.Writers[0]; w.Op != OpSetClear || w.Class != c.class {
				t.Errorf("floor switch %s %s (%s), want %s set+clear", w.Class, w.Op, w.Why, c.class)
			}
		})
	}
}

// at places a at x, y, z.
func at(a stage.Actor, x, y, z float32) stage.Actor {
	a.Pos = [3]float32{x, y, z}
	return a
}

// TestStatueSlots covers statueSlots with TotG r0's slots (CrTrM1
// 00FF0504 and its twin Hmon1d 80000505 at (3115.6, -1637.1, -1311.3);
// CrTrM2 00FF0605 and Hmon2d 80000606), and what keeps a slot unsafe.
func TestStatueSlots(t *testing.T) {
	const tb = addr.TableTotG
	m := func(no int) Key { return mem(tb, no) }
	slot := func(typ, sw int) stage.Actor {
		return at(act("CrTrM1", 0xFF<<16|uint32(sw)<<8|uint32(typ)), 3115.6343, -1637.0668, -1311.3113)
	}
	statue := func(params uint32, dx float32) stage.Actor {
		return at(act("Hseki1", params), 3115.6343+dx, -1637.1115, -1311.3113)
	}
	for _, c := range []struct {
		name   string
		rooms  [][]stage.Actor
		synced bool
		twin   string // in the slot's Why
	}{
		{"r0 sw5", [][]stage.Actor{{slot(4, 5), statue(0x80000505, 0)}, nil, nil, nil, {at(act("Hseki1", 0x00000505), 7733, -1442, -2577)}},
			true, "twin statue Siren r0 ACTR#1 Hseki1"},
		{"r0 sw6", [][]stage.Actor{{slot(5, 6), statue(0x80000606, 0)}}, true, "twin statue Siren r0 ACTR#1"},
		{"twin just inside the slot", [][]stage.Actor{{slot(4, 5), statue(0x80000505, 2.99)}}, true, "twin statue"},
		{"twin out of the slot", [][]stage.Actor{{slot(4, 5), statue(0x80000505, 3)}}, false, ""},
		{"no twin", [][]stage.Actor{{slot(4, 5)}, {statue(0x00000505, 0)}}, false, ""},
		{"twin in another room", [][]stage.Actor{{slot(4, 5)}, {statue(0x80000505, 0)}}, false, ""},
		{"bit 31 clear", [][]stage.Actor{{slot(4, 5), statue(0x00000505, 0)}}, false, ""},
		{"the other slot's type", [][]stage.Actor{{slot(4, 5), statue(0x80000506, 0)}}, false, ""},
		{"a statue that doesn't read", [][]stage.Actor{{slot(1, 5), statue(0x80000502, 0)}}, false, ""},
		{"twin and a timed torch", [][]stage.Actor{{slot(4, 5), statue(0x80000505, 0), timedTorch(0x05)}}, false, "twin statue"},
	} {
		t.Run(c.name, func(t *testing.T) {
			tbl := Build([]*stage.Stage{mkStage("Siren", tb, nil, c.rooms...)}, testNames(t))
			checkFixedPoint(t, tbl)
			no := 5
			if c.name == "r0 sw6" {
				no = 6
			}
			s, ok := tbl.Lookup(m(no))
			if !ok {
				t.Fatalf("0x%02X missing: %v", no, keysOf(tbl))
			}
			if s.Synced != c.synced {
				t.Errorf("synced %v, want %v (%s)", s.Synced, c.synced, s.Reason)
			}
			w := s.Writers[0]
			if w.Proc != procCorrect || w.Op != OpSetClear {
				t.Fatalf("first writer %v %s %s, want the slot's set+clear", w.Ref, w.Class, w.Op)
			}
			if latch := c.twin != ""; (w.Class == Latching) != latch || latch && !strings.Contains(w.Why, c.twin) {
				t.Errorf("slot %s %q, want latching %v with %q", w.Class, w.Why, latch, c.twin)
			}
			for _, w := range s.Writers {
				if w.Proc == procTry {
					t.Errorf("Obj_Try writes: %+v", w)
				}
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
	stages := []*stage.Stage{
		mkStage("A", 3, []stage.Actor{crystal(0x05), crystal(0x85), crystal(0xC5), crystal(0xF5)},
			[]stage.Actor{crystal(0x05), crystal(0x85), crystal(0xC5), crystal(0xE5)},
			[]stage.Actor{crystal(0xC5), crystal(0xE5)}),
		mkStage("B", 3, nil, []stage.Actor{crystal(0x05), crystal(0x85), crystal(0xC5)}),
		mkStage("C", 4, nil, []stage.Actor{crystal(0x05), crystal(0x85)}),
		mkStage("X", 20, nil, []stage.Actor{crystal(0x05), crystal(0xC5)}),
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
	tbl := Build([]*stage.Stage{mkStage("D", 5, []stage.Actor{
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
	tbl := Build([]*stage.Stage{mkStage("D", 6, nil, []stage.Actor{
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
	st := mkStage("D", 7, nil, []stage.Actor{act("Itnak", 0x1234), act("Nope", 0x05), crystal(0x05)})
	tbl := Build([]*stage.Stage{st, nil}, testNames(t))
	if tbl.Stages != 1 || tbl.Users != 2 || tbl.Unnamed != 1 {
		t.Errorf("stages %d, users %d, unnamed %d; want 1, 2, 1", tbl.Stages, tbl.Users, tbl.Unnamed)
	}
	if len(tbl.Undecoded) != 1 || tbl.Undecoded[0].Name != "Itnak" || !strings.Contains(tbl.Undecoded[0].Why, "d_a_obj_Itnak.cpp is NonMatching") {
		t.Errorf("undecoded %+v", tbl.Undecoded)
	}
	if len(tbl.Switches) != 1 {
		t.Errorf("switches %v, want the crystal's", keysOf(tbl))
	}
	tbl = Build([]*stage.Stage{st}, nil)
	if tbl.Users != 0 || tbl.Unnamed != 3 || len(tbl.Switches) != 0 {
		t.Errorf("nil names: users %d, unnamed %d, %d switches", tbl.Users, tbl.Unnamed, len(tbl.Switches))
	}
	if tbl := Build(nil, nil); tbl.Stages != 0 || len(tbl.Switches) != 0 || tbl.Report() == "" {
		t.Errorf("empty build: %+v", tbl)
	}
}

// sample is a table with something of each kind in it.
func sample(t testing.TB) *Table {
	return Build([]*stage.Stage{
		mkStage("M_NewD2", addr.TableDRC, []stage.Actor{act("door10", 0xFF<<20|4<<8|0x12, 1|2<<6)},
			[]stage.Actor{crystal(0x05), timedTorch(0x06), andsw2(0, 0x07, 2, 0x05), act("Itnak", 0), agb("agbA", 0, 0x05, 0xFF)},
			[]stage.Actor{act("osiBLK0", 1<<24|0x20<<8, 0, 0, 0x21<<8|0x01), act("ALLdie", 0xC3<<8), act("Bk", 0xA)}),
		mkStage("M_DaiB", addr.TableDRC, []stage.Actor{crystal(0xC9)}, []stage.Actor{act("Bdk", 0), act("Htobi1", 0x30)}),
		mkStage("sea", addr.TableSea, nil, []stage.Actor{act("Evsw", 1<<24|0x30), act("Nope", 0)}),
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
		"Undecoded switch users: 1\n  Itnak    1",
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
	seen := make(map[stage.Proc]bool)
	for _, sp := range specs {
		if seen[sp.proc] {
			t.Errorf("proc 0x%03X listed twice", uint16(sp.proc))
		}
		seen[sp.proc] = true
		if sp.proc <= 0 || sp.proc >= stage.ProcMaxNum || sp.name == "" {
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
				for _, u := range sp.decode(entry{stage.Actor{Params: p, Angle: ang}}) {
					if u.write && (u.class == "" || u.op == "") || u.why == "" || len(u.inputs) > MaxANDInputs {
						t.Errorf("%s(0x%08X, %v): use %+v", sp.name, p, ang, u)
					}
				}
			}
		}
	}
	// The wwapi/stage procs the design names are all read.
	for _, p := range []stage.Proc{
		stage.ProcAllDie, stage.ProcTagEvsw, stage.ProcSwpush, stage.ProcSwheavy, stage.ProcMovebox,
		stage.ProcEP, stage.ProcTbox, stage.ProcSwc00, stage.ProcDoor10, stage.ProcDoor12,
		stage.ProcAndsw0, stage.ProcAndsw2, stage.ProcSwhit0, stage.ProcObjTimer,
	} {
		if !seen[p] {
			t.Errorf("%v has no spec", p)
		}
	}
}
