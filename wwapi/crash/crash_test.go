package crash

import (
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mem"
)

func put32(t *testing.T, m *mem.FakeMem, a, v uint32) {
	t.Helper()
	var b [4]byte
	binary.BigEndian.PutUint32(b[:], v)
	if err := m.Store(a, b[:]); err != nil {
		t.Fatal(err)
	}
}

const (
	ctxAddr = 0x80500000
	sp0     = 0x80600000
	relInfo = 0x80700000
	relSecs = 0x80700100
	relText = 0x80710000
	rel2    = 0x80720000
)

// fakeCrash builds a DSI caught by JUTException: a context with PC in a
// DOL function, LR in T2, a three-frame stack whose last return lands in
// the mod's REL, and two linked modules.
func fakeCrash(t *testing.T) *mem.FakeMem {
	m := mem.NewFakeMem()
	var b [2]byte
	binary.BigEndian.PutUint16(b[:], 2) // DSI
	if err := m.Store(addr.ExCallbackObject+addr.ExCallbackOffError, b[:]); err != nil {
		t.Fatal(err)
	}
	put32(t, m, addr.ExCallbackObject+addr.ExCallbackOffContext, ctxAddr)
	put32(t, m, addr.ExCallbackObject+addr.ExCallbackOffDSISR, 0x40000000)
	put32(t, m, addr.ExCallbackObject+addr.ExCallbackOffDAR, 0x00000010)
	for i := uint32(0); i < 32; i++ {
		put32(t, m, ctxAddr+addr.OSContextOffGPR+4*i, 0x1000+i)
	}
	put32(t, m, ctxAddr+addr.OSContextOffGPR+4, sp0) // r1
	put32(t, m, ctxAddr+addr.OSContextOffCR, 0x22000000)
	put32(t, m, ctxAddr+addr.OSContextOffLR, 0x80410024)
	put32(t, m, ctxAddr+addr.OSContextOffCTR, 0x80003150)
	put32(t, m, ctxAddr+addr.OSContextOffXER, 0x20000000)
	put32(t, m, ctxAddr+addr.OSContextOffSRR0, 0x80003148)
	put32(t, m, ctxAddr+addr.OSContextOffSRR1, 0x00009032)

	// Stack: sp0 -> sp0+0x40 -> sp0+0x100 -> 0.
	put32(t, m, sp0, sp0+0x40)
	put32(t, m, sp0+4, 0xDEADBEEF) // stale
	put32(t, m, sp0+0x40, sp0+0x100)
	put32(t, m, sp0+0x44, 0x80003160)
	put32(t, m, sp0+0x100, 0)
	put32(t, m, sp0+0x104, relText+0x1A4)

	// Two modules: 0x58 (two sections, one bss-like empty) then 0x12.
	put32(t, m, addr.OSModuleList, relInfo)
	put32(t, m, relInfo+addr.OSModuleInfoOffID, 0x58)
	put32(t, m, relInfo+addr.OSModuleInfoOffNext, rel2)
	put32(t, m, relInfo+addr.OSModuleInfoOffNumSections, 3)
	put32(t, m, relInfo+addr.OSModuleInfoOffSectionInfo, relSecs)
	// sec 0 empty, sec 1 .text (exec bit), sec 2 size 0
	put32(t, m, relSecs+8, relText|addr.OSSectionInfoExec)
	put32(t, m, relSecs+12, 0x2000)
	put32(t, m, relSecs+16, 0x80730000)
	put32(t, m, rel2+addr.OSModuleInfoOffID, 0x12)
	return m
}

func TestReadExceptionNoCrash(t *testing.T) {
	rep, err := Read(mem.NewFakeMem())
	if err != nil {
		t.Fatal(err)
	}
	if rep.Exception.Caught {
		t.Fatal("caught on an empty RAM")
	}
	var out bytes.Buffer
	rep.Write(&out, Resolver{})
	if !strings.Contains(out.String(), "No crash context") {
		t.Errorf("output %q", out.String())
	}
}

func TestReadExceptionBadContext(t *testing.T) {
	m := mem.NewFakeMem()
	put32(t, m, addr.ExCallbackObject+addr.ExCallbackOffContext, 0x00001234)
	if _, err := ReadException(m); err == nil {
		t.Fatal("no error for a context outside MEM1")
	}
}

func TestReadCrash(t *testing.T) {
	m := fakeCrash(t)
	rep, err := Read(m)
	if err != nil {
		t.Fatal(err)
	}
	e := rep.Exception
	if !e.Caught || e.Error != 2 || e.ContextPtr != ctxAddr || e.DSISR != 0x40000000 || e.DAR != 0x10 {
		t.Fatalf("exception %+v", e)
	}
	if e.SRR0 != 0x80003148 || e.LR != 0x80410024 || e.CTR != 0x80003150 || e.CR != 0x22000000 || e.XER != 0x20000000 || e.SRR1 != 0x9032 {
		t.Fatalf("specials %+v", e)
	}
	if e.SP() != sp0 || e.GPR[31] != 0x1000+31 {
		t.Fatalf("gprs %x", e.GPR)
	}
	want := []Frame{{sp0, 0xDEADBEEF}, {sp0 + 0x40, 0x80003160}, {sp0 + 0x100, relText + 0x1A4}}
	if len(rep.Frames) != len(want) || rep.Stop != "" {
		t.Fatalf("frames %x stop %q", rep.Frames, rep.Stop)
	}
	for i := range want {
		if rep.Frames[i] != want[i] {
			t.Errorf("frame %d = %x, want %x", i, rep.Frames[i], want[i])
		}
	}
	if rep.ModErr != nil || len(rep.Modules) != 2 {
		t.Fatalf("modules %+v err %v", rep.Modules, rep.ModErr)
	}
	m58 := rep.Modules[0]
	if m58.ID != 0x58 || len(m58.Sections) != 1 || m58.Sections[0] != (Section{Index: 1, Start: relText, End: relText + 0x2000, Exec: true}) {
		t.Errorf("module 0x58 %+v", m58)
	}
	if rep.Modules[1].ID != 0x12 || len(rep.Modules[1].Sections) != 0 {
		t.Errorf("module 0x12 %+v", rep.Modules[1])
	}

	dol, _ := ParseDOLSymbols(strings.NewReader(sampleSymbols))
	t2, _ := ParseDolphinMap(strings.NewReader(sampleMap))
	var out bytes.Buffer
	rep.Write(&out, Resolver{DOL: dol, T2: t2, OwnModule: 0x58})
	s := out.String()
	for _, w := range []string{
		"DSI EXCEPTION (OSError 2)",
		"PC   0x80003148  __start+0x8",
		"LR   0x80410024  T2 frame_shim+0x4",
		"CTR  0x80003150  __start+0x10",
		"r31 0x0000101F",
		"ret 0x80003160  __start+0x20",
		"ret 0x807101A4  REL 0x58 sec 1 +0x1A4 (WW Multiplayer REL)",
		"Linked REL modules: 2 (0x12 0x58)",
	} {
		if !strings.Contains(s, w) {
			t.Errorf("output lacks %q:\n%s", w, s)
		}
	}
	if strings.Contains(s, "no DOL symbols") || strings.Contains(s, "no T2 map") {
		t.Errorf("missing-table notes with both tables loaded:\n%s", s)
	}
}

func TestWalkStops(t *testing.T) {
	m := mem.NewFakeMem()
	// A loop: sp -> sp-0x10.
	put32(t, m, sp0, sp0-0x10)
	f, stop := Walk(m, sp0, MaxFrames)
	if len(f) != 1 || !strings.Contains(stop, "does not move up") {
		t.Errorf("loop: %x %q", f, stop)
	}
	f, stop = Walk(m, sp0+2, MaxFrames)
	if len(f) != 0 || !strings.Contains(stop, "misaligned") {
		t.Errorf("misaligned: %x %q", f, stop)
	}
	f, stop = Walk(m, 0x00400000, MaxFrames)
	if len(f) != 0 || !strings.Contains(stop, "outside MEM1") {
		t.Errorf("outside: %x %q", f, stop)
	}
	// A long chain is cut at max with no stop reason.
	for i := uint32(0); i < 30; i++ {
		put32(t, m, sp0+0x1000+0x10*i, sp0+0x1000+0x10*(i+1))
	}
	f, stop = Walk(m, sp0+0x1000, MaxFrames)
	if len(f) != MaxFrames || stop != "" {
		t.Errorf("long chain: %d frames, %q", len(f), stop)
	}
	// 0xFFFFFFFF ends the chain cleanly.
	put32(t, m, sp0+0x2000, 0xFFFFFFFF)
	f, stop = Walk(m, sp0+0x2000, MaxFrames)
	if len(f) != 1 || stop != "" {
		t.Errorf("end marker: %x %q", f, stop)
	}
}

func TestReadModulesLoop(t *testing.T) {
	m := mem.NewFakeMem()
	put32(t, m, addr.OSModuleList, relInfo)
	put32(t, m, relInfo+addr.OSModuleInfoOffNext, relInfo)
	mods, err := ReadModules(m)
	if err == nil || len(mods) != 1 {
		t.Errorf("loop: %d modules, err %v", len(mods), err)
	}
	put32(t, m, relInfo+addr.OSModuleInfoOffNext, 0x1234)
	if _, err := ReadModules(m); err == nil || !strings.Contains(err.Error(), "outside MEM1") {
		t.Errorf("bad link: %v", err)
	}
}

func TestErrorName(t *testing.T) {
	for code, want := range map[uint16]string{0: "SYSTEM RESET", 3: "ISI", 15: "PROTECTION", 16: "FLOATING POINT", 17: "OSError 17"} {
		if got := ErrorName(code); got != want {
			t.Errorf("ErrorName(%d) = %q, want %q", code, got, want)
		}
	}
}

const sampleSymbols = `__check_pad3 = .init:0x80003100; // type:function size:0x40 scope:local
__start = .init:0x80003140; // type:function size:0x138 scope:weak
_rom_copy_info = .init:0x8000556C; // type:object size:0x84 scope:global data:4byte
gTRKInterruptVectorTable = .init:0x80003534; // type:label scope:global
exCallbackObject = .bss:0x803ED988; // type:object size:0x14 scope:global align:4 data:4byte
fopAcM_delete__FP10fopAc_ac_c = .text:0x80024478; // type:function size:0x6C scope:global
`

const sampleMap = `.text section layout
  Starting        Virtual
  address  Size   address
  -----------------------
  80410000 000020 80410000  0 main01_init_hook
  80410020 000040 80410020  0 frame_shim

.data section layout
  Starting        Virtual
  address  Size   address
  -----------------------
  80412000 000010 80412000  0 some_table
`

func TestParseDOLSymbols(t *testing.T) {
	s, err := ParseDOLSymbols(strings.NewReader(sampleSymbols))
	if err != nil {
		t.Fatal(err)
	}
	if s.Len() != 3 {
		t.Fatalf("%d symbols, want the 3 functions", s.Len())
	}
	if sym, off, ok := s.Lookup(0x80024478 + 0x10); !ok || sym.Name != "fopAcM_delete__FP10fopAc_ac_c" || off != 0x10 {
		t.Errorf("lookup: %+v %x %v", sym, off, ok)
	}
	if _, _, ok := s.Lookup(0x80024478 + 0x6C); ok {
		t.Error("an address past the function's size was named")
	}
	if _, _, ok := s.Lookup(0x80003000); ok {
		t.Error("an address below every symbol was named")
	}
	if _, _, ok := (*Symbols)(nil).Lookup(0x80003140); ok {
		t.Error("a nil table named an address")
	}
}

func TestParseDolphinMap(t *testing.T) {
	s, err := ParseDolphinMap(strings.NewReader(sampleMap))
	if err != nil {
		t.Fatal(err)
	}
	if s.Len() != 2 {
		t.Fatalf("%d symbols, want the 2 .text ones", s.Len())
	}
	if sym, off, ok := s.Lookup(0x8041005C); !ok || sym.Name != "frame_shim" || off != 0x3C {
		t.Errorf("lookup: %+v %x %v", sym, off, ok)
	}
	if _, _, ok := s.Lookup(0x80412004); ok {
		t.Error(".data symbol named")
	}
}

// TestDecompSymbols reads the real symbols.txt when the decomp is next to
// this checkout (WW_TWW_SYMBOLS, or ~/projects/tww), and skips otherwise.
func TestDecompSymbols(t *testing.T) {
	path := os.Getenv("WW_TWW_SYMBOLS")
	if path == "" {
		home, _ := os.UserHomeDir()
		path = filepath.Join(home, "projects", "tww", "config", "GZLE01", "symbols.txt")
	}
	f, err := os.Open(path)
	if err != nil {
		t.Skipf("no decomp symbols: %v", err)
	}
	defer f.Close()
	s, err := ParseDOLSymbols(f)
	if err != nil {
		t.Fatal(err)
	}
	// errorHandler__12JUTExceptionFUsP9OSContextUlUl, JUTException.cpp:126.
	if sym, _, ok := s.Lookup(0x802C4CEC); !ok || !strings.HasPrefix(sym.Name, "errorHandler__12JUTException") {
		t.Errorf("0x802C4CEC = %+v %v", sym, ok)
	}
}
