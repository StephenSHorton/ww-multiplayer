package enemysync

import (
	"encoding/binary"
	"hash/fnv"
	"slices"
	"testing"

	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/fakegame"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mem"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/stage"
)

// The PKey test vector (design identity.pkey: the C copy's
// EMK_PKEY_TV_* in wwenemy_kinds.h must carry these numbers): M_NewD2
// room 0's torch Bokoblin, setID 0, params 0xFFFF8784, home (1280.0,
// -200.0, 6000.0). pkeyTVOut was computed outside Go (a Python FNV-1a
// over the 21 bytes 00BE 0000 FFFF8784 00 44A00000 C3480000 45BB8000).
const (
	pkeyTVProc   = uint16(0x00BE)
	pkeyTVSetID  = uint16(0x0000)
	pkeyTVParams = uint32(0xFFFF8784)
	pkeyTVRoom   = int8(0)
	pkeyTVOut    = uint32(0xD0264F5D)
)

var pkeyTVHome = [3]uint32{0x44A00000, 0xC3480000, 0x45BB8000}

func TestPKey(t *testing.T) {
	if PKeyLen != 21 {
		t.Errorf("PKeyLen = %d, want 21 (2+2+4+1+12)", PKeyLen)
	}
	if got := PKey(pkeyTVProc, pkeyTVSetID, pkeyTVParams, pkeyTVRoom, pkeyTVHome); got != pkeyTVOut {
		t.Errorf("PKey(test vector) = 0x%08X, want 0x%08X", got, pkeyTVOut)
	}
	// The standard library's FNV-1a over the same bytes, as a second
	// oracle for other inputs (negative room, unkeyed setID).
	ref := func(proc, setID uint16, params uint32, room int8, home [3]uint32) uint32 {
		h := fnv.New32a()
		b := binary.BigEndian.AppendUint16(nil, proc)
		b = binary.BigEndian.AppendUint16(b, setID)
		b = binary.BigEndian.AppendUint32(b, params)
		b = append(b, byte(room))
		for _, v := range home {
			b = binary.BigEndian.AppendUint32(b, v)
		}
		h.Write(b)
		return pkeyFinish(h.Sum32())
	}
	for _, c := range []struct {
		proc, setID uint16
		params      uint32
		room        int8
		home        [3]uint32
	}{
		{0xCF, 0xFFFF, 0xFF000100, 3, [3]uint32{0xC4BB8000, 0, 0x447A0000}},
		{0xBD, 2, 0xFF00FF01, -1, [3]uint32{0x7FC00000, 0x80000000, 1}},
		{0xBE, 0x1FF, 0, 63, [3]uint32{}},
	} {
		if got, want := PKey(c.proc, c.setID, c.params, c.room, c.home), ref(c.proc, c.setID, c.params, c.room, c.home); got != want {
			t.Errorf("PKey(%+v) = 0x%08X, hash/fnv 0x%08X", c, got, want)
		}
	}
	// Every field is in the key.
	base := PKey(pkeyTVProc, pkeyTVSetID, pkeyTVParams, pkeyTVRoom, pkeyTVHome)
	h2 := pkeyTVHome
	h2[2] ^= 1
	for name, k := range map[string]uint32{
		"proc":   PKey(pkeyTVProc+1, pkeyTVSetID, pkeyTVParams, pkeyTVRoom, pkeyTVHome),
		"setID":  PKey(pkeyTVProc, pkeyTVSetID+1, pkeyTVParams, pkeyTVRoom, pkeyTVHome),
		"params": PKey(pkeyTVProc, pkeyTVSetID, pkeyTVParams^0x100, pkeyTVRoom, pkeyTVHome),
		"room":   PKey(pkeyTVProc, pkeyTVSetID, pkeyTVParams, pkeyTVRoom+1, pkeyTVHome),
		"home z": PKey(pkeyTVProc, pkeyTVSetID, pkeyTVParams, pkeyTVRoom, h2),
	} {
		if k == base {
			t.Errorf("changing %s leaves the pkey 0x%08X", name, k)
		}
	}
	// 0 -> 1, everything else unchanged.
	if pkeyFinish(0) != 1 || pkeyFinish(1) != 1 || pkeyFinish(0xDEADBEEF) != 0xDEADBEEF {
		t.Error("pkeyFinish does not map 0 to 1 alone")
	}
	if fnv1a(nil) != fnvOffset {
		t.Errorf("fnv1a(nil) = 0x%08X, want the offset basis", fnv1a(nil))
	}
}

// kindRoom is M_Dai with Link in room 1 and a mix of kinds placed:
// placed indices 0 BK setID 0 torch, 1 BK setID 1 sleeper (type 2), 2 CC
// setID 3 black, 3 CC setID 4 red, 4 CC unkeyed green, 5 MO2 setID 2,
// 6 BK setID 7 in room 2, 7 a BOKO-less BK setID 8 of type 0.
func kindRoom(t *testing.T) (*fakegame.FakeGame, *mem.FakeMem, *fakegame.Enemies) {
	t.Helper()
	m := mem.NewFakeMem()
	g := fakegame.New(m)
	if err := g.LoadStage(mDai); err != nil {
		t.Fatal(err)
	}
	if err := g.EnterRoom(1, 2); err != nil {
		t.Fatal(err)
	}
	es := fakegame.PlaceEnemies(
		fakegame.Enemy{Stage: mDai.Name, Room: 1, SetID: 0, Proc: stage.ProcBK, Params: 0xFFFF8784},
		fakegame.Enemy{Stage: mDai.Name, Room: 1, SetID: 1, Proc: stage.ProcBK, Params: 0xFFFF0002},
		fakegame.Enemy{Stage: mDai.Name, Room: 1, SetID: 3, Proc: stage.ProcCC, Params: 0xFF460300},
		fakegame.Enemy{Stage: mDai.Name, Room: 1, SetID: 4, Proc: stage.ProcCC, Params: 0xFF460100},
		fakegame.Enemy{Stage: mDai.Name, Room: 1, SetID: addr.ActorSetIDNone, Proc: stage.ProcCC, Params: 0xFF000000},
		fakegame.Enemy{Stage: mDai.Name, Room: 1, SetID: 2, Proc: stage.ProcMO2},
		fakegame.Enemy{Stage: mDai.Name, Room: 2, SetID: 7, Proc: stage.ProcBK, Params: 0xFFFF8784},
		fakegame.Enemy{Stage: mDai.Name, Room: 1, SetID: 8, Proc: stage.ProcBK, Params: 0xFFFFFF00},
	)
	frames(t, g, es, 4)
	// What each create would have written to the gate bytes.
	live := func(i int, off uint32, b ...byte) {
		t.Helper()
		a, ok := es.Of(i)
		if !ok {
			t.Fatalf("placed %d is not live", i)
		}
		store(t, m, a.Addr+off, b)
	}
	live(0, addr.BkOffType, 4)
	live(1, addr.BkOffType, 2)
	live(2, addr.CcOffBehavior, 0, 3)
	live(3, addr.CcOffBehavior, 0, 1)
	return g, m, es
}

func identsOf(es []Enemy) []Ident {
	var out []Ident
	for _, e := range es {
		out = append(out, e.Ident)
	}
	return out
}

func TestClassify(t *testing.T) {
	_, m, es := kindRoom(t)
	all, err := Classify(m, 1, AllKinds())
	if err != nil {
		t.Fatal(err)
	}
	why := map[uint16]string{}
	byKind := map[uint16]KindID{}
	for _, e := range all {
		why[e.Ident.SetID] = e.Refused
		byKind[e.Ident.SetID] = e.Kind
		if e.Ident.PKey != PKey(e.Ident.Proc, e.Ident.SetID, e.Params, e.HomeRoom, e.HomePos) || e.HomeRoom != 1 {
			t.Errorf("%s: pkey or home room wrong", e.Ident)
		}
	}
	want := map[uint16]string{0: "", 1: RefuseParams, 3: RefuseParams, 4: "", addr.ActorSetIDNone: "", 2: "", 8: ""}
	for id, w := range want {
		got, ok := why[id]
		if !ok {
			t.Errorf("setID 0x%X not listed", id)
			continue
		}
		if got != w {
			t.Errorf("setID 0x%X refused %q, want %q", id, got, w)
		}
	}
	if _, ok := why[7]; ok {
		t.Error("setID 7 (room 2) listed in room 1")
	}
	if byKind[0] != KindBK || byKind[4] != KindCC || byKind[2] != KindMO2 {
		t.Errorf("kinds %v", byKind)
	}
	if !slices.IsSortedFunc(identsOf(all), compareIdent) {
		t.Error("Classify is not sorted by Ident")
	}

	// Without +u the unkeyed ChuChu is refused; with only mo2 enabled
	// only the Moblin is listed.
	s, _ := ParseKinds("bk,cc")
	all, _ = Classify(m, 1, s)
	for _, e := range all {
		if e.Ident.SetID == addr.ActorSetIDNone && e.Refused != RefuseUnkeyedOff {
			t.Errorf("unkeyed without +u: %q", e.Refused)
		}
		if e.Kind == KindMO2 {
			t.Error("mo2 listed while disabled")
		}
	}
	mo, _ := ParseKinds("mo2")
	cs, _ := Candidates(m, 1, mo)
	if len(cs) != 1 || cs[0].Kind != KindMO2 || cs[0].Ident.SetID != 2 {
		t.Errorf("mo2 Candidates = %+v", cs)
	}

	// The live gate: the red ChuChu turned insta-kill (m304) is refused.
	a, _ := es.Of(3)
	store(t, m, a.Addr+addr.CcOffInstaKill, []byte{1})
	all, _ = Classify(m, 1, AllKinds())
	for _, e := range all {
		if e.Ident.SetID == 4 && e.Refused != RefuseLive {
			t.Errorf("CC with m304: %q", e.Refused)
		}
	}
}

// TestPKeyIndependentOfPos: moving an actor (current.pos) leaves its
// Ident alone; moving its home does not.
func TestPKeyIndependentOfPos(t *testing.T) {
	_, m, es := kindRoom(t)
	before, _ := Candidates(m, 1, AllKinds())
	a, _ := es.Of(0)
	store(t, m, a.Addr+addr.ActorOffCurPos, f32s(100, 200, 300))
	after, _ := Candidates(m, 1, AllKinds())
	if !slices.Equal(identsOf(before), identsOf(after)) {
		t.Errorf("current.pos moved an Ident:\n%v\n%v", identsOf(before), identsOf(after))
	}
	store(t, m, a.Addr+addr.ActorOffHomePos, f32s(100, 200, 300))
	moved, _ := Candidates(m, 1, AllKinds())
	if slices.Equal(identsOf(before), identsOf(moved)) {
		t.Error("home.pos did not move the BK's pkey")
	}
}

// TestDuplicateIdent: two live actors with one Ident are both refused;
// a refused one does not make a taken one a duplicate.
func TestDuplicateIdent(t *testing.T) {
	id := Ident{Proc: addr.ProcCC, SetID: 0xFFFF, PKey: 0x1234}
	es := []Enemy{{Ident: id}, {Ident: id}, {Ident: Ident{Proc: addr.ProcCC, SetID: 1, PKey: 9}},
		{Ident: Ident{Proc: addr.ProcCC, SetID: 2, PKey: 7}}, {Ident: Ident{Proc: addr.ProcCC, SetID: 2, PKey: 7}, Refused: RefuseLive}}
	RefuseDuplicates(es)
	for i, w := range []string{RefuseDuplicate, RefuseDuplicate, "", "", RefuseLive} {
		if es[i].Refused != w {
			t.Errorf("es[%d] refused %q, want %q", i, es[i].Refused, w)
		}
	}

	// Live: two unkeyed green ChuChus spawned at the same place with the
	// same params (a Homen pair placed twice) pair to one Ident.
	m := mem.NewFakeMem()
	g := fakegame.New(m)
	if err := g.LoadStage(mDai); err != nil {
		t.Fatal(err)
	}
	if err := g.EnterRoom(1); err != nil {
		t.Fatal(err)
	}
	two := fakegame.PlaceEnemies(
		fakegame.Enemy{Stage: mDai.Name, Room: 1, SetID: addr.ActorSetIDNone, Proc: stage.ProcCC, Params: 0xFF000000},
		fakegame.Enemy{Stage: mDai.Name, Room: 1, SetID: addr.ActorSetIDNone, Proc: stage.ProcCC, Params: 0xFF000000},
		fakegame.Enemy{Stage: mDai.Name, Room: 1, SetID: addr.ActorSetIDNone, Proc: stage.ProcCC, Params: 0xFF000100},
	)
	frames(t, g, two, 4)
	all, err := Classify(m, 1, AllKinds())
	if err != nil {
		t.Fatal(err)
	}
	var dup, taken int
	for _, e := range all {
		switch e.Refused {
		case RefuseDuplicate:
			dup++
		case "":
			taken++
		}
	}
	if dup != 2 || taken != 1 {
		t.Errorf("duplicates %d taken %d, want 2 and 1: %+v", dup, taken, all)
	}
}

// TestCandidatesMO2Legacy: with mo2 alone, Candidates takes exactly the
// Moblins KeyedMoblins takes (the fixture has 3, under MaxSlots).
func TestCandidatesMO2Legacy(t *testing.T) {
	_, m, _ := moblinRoom(t)
	mo, _ := ParseKinds("mo2")
	cs, err := Candidates(m, 1, mo)
	if err != nil {
		t.Fatal(err)
	}
	ks, err := KeyedMoblins(m, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(cs) != len(ks) {
		t.Fatalf("Candidates %d, KeyedMoblins %d", len(cs), len(ks))
	}
	for i := range cs {
		if cs[i].PID != ks[i].PID || cs[i].Ident.SetID != ks[i].SetID || cs[i].Addr != ks[i].Addr || cs[i].Live.Mode != ks[i].Mode {
			t.Errorf("%d: Candidates %+v, KeyedMoblins %+v", i, cs[i], ks[i])
		}
	}
}
