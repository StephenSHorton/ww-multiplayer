package inject

import (
	"encoding/binary"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"testing"

	mbox "github.com/StephenSHorton/ww-multiplayer/wwapi/mailbox"
)

// The C side's addresses, read from the committed sources rather than
// mirrored here: inject/include/mailbox_core.h (MAILBOX_ADDR), inject/build.py
// (inject_address, the OSInit arena-lo patch) and inject/src/core/hooks.c
// (CALLBACK_PTR_ADDR). CI cannot build the C side; these tests tie the
// committed blob.go to what those sources say it should look like.
const (
	mailboxH      = "../../inject/include/mailbox_core.h"
	buildPy       = "../../inject/build.py"
	hooksC        = "../../inject/src/core/hooks.c"
	osInitT1Load  = 0x800056E0 // build.py T1_LOAD
	osInitT1File  = 0x2620     // build.py T1_FILE
	lisR3Mask     = 0xFFFF0000
	lisR3         = 0x3C600000 // lis r3, imm
	addiR3R3      = 0x38630000 // addi r3, r3, imm
	lisR12_8041   = 0x3D808041 // lis r12, 0x8041
	lwzR12R12Base = 0x818C0000 // lwz r12, imm(r12)
)

// define reads `#define name value` from a C source.
func define(t *testing.T, file, name string) uint32 {
	t.Helper()
	src, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`(?m)^#define\s+` + name + `\s+(0x[0-9A-Fa-f]+|\d+)\s*(?://.*)?$`).FindSubmatch(src)
	if m == nil {
		t.Fatalf("%s: no #define %s", file, name)
	}
	v, err := strconv.ParseUint(string(m[1]), 0, 32)
	if err != nil {
		t.Fatal(err)
	}
	return uint32(v)
}

type buildPyPatch struct {
	addr  uint32
	bytes []byte
}

// buildPyFacts reads inject_address and the OSInit patch list from build.py.
func buildPyFacts(t *testing.T) (injectAddr uint32, patches []buildPyPatch) {
	t.Helper()
	src, err := os.ReadFile(buildPy)
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`inject_address\s*=\s*(0x[0-9A-Fa-f]+)`).FindSubmatch(src)
	if m == nil {
		t.Fatal("build.py: no inject_address=")
	}
	v, _ := strconv.ParseUint(string(m[1]), 0, 32)
	injectAddr = uint32(v)
	for _, name := range []string{"T1_LOAD", "T1_FILE"} {
		want := map[string]uint32{"T1_LOAD": osInitT1Load, "T1_FILE": osInitT1File}[name]
		m := regexp.MustCompile(name + `\s*=\s*(0x[0-9A-Fa-f]+)`).FindSubmatch(src)
		if m == nil {
			t.Fatalf("build.py: no %s", name)
		}
		if v, _ := strconv.ParseUint(string(m[1]), 0, 32); uint32(v) != want {
			t.Fatalf("build.py %s = %s, this test assumes 0x%X", name, m[1], want)
		}
	}
	re := regexp.MustCompile(`\((0x[0-9A-Fa-f]{8}),\s*b"((?:\\x[0-9A-Fa-f]{2})+)"\)`)
	for _, m := range re.FindAllSubmatch(src, -1) {
		a, _ := strconv.ParseUint(string(m[1]), 0, 32)
		hex := regexp.MustCompile(`\\x([0-9A-Fa-f]{2})`).FindAllSubmatch(m[2], -1)
		b := make([]byte, len(hex))
		for i, h := range hex {
			x, _ := strconv.ParseUint(string(h[1]), 16, 8)
			b[i] = byte(x)
		}
		patches = append(patches, buildPyPatch{uint32(a), b})
	}
	if len(patches) == 0 {
		t.Fatal("build.py: no OSInit patch records found")
	}
	return injectAddr, patches
}

// patchedArenaLo decodes `lis r3, hi` + `addi r3, r3, lo` from the
// OSInit patch list: the __OSArenaLo the patched game starts its heap at.
func patchedArenaLo(t *testing.T, patches []buildPyPatch) uint32 {
	t.Helper()
	var hi, lo uint32
	var haveHi, haveLo bool
	for _, p := range patches {
		if len(p.bytes) != 4 {
			continue
		}
		w := binary.BigEndian.Uint32(p.bytes)
		switch w & lisR3Mask {
		case lisR3:
			hi, haveHi = w&0xFFFF, true
		case addiR3R3:
			lo, haveLo = w&0xFFFF, true
		}
	}
	if !haveHi || !haveLo {
		t.Fatal("build.py: the OSInit patch has no lis r3 / addi r3,r3 pair")
	}
	return hi<<16 + uint32(int32(int16(lo)))
}

// checkBudget is the code-space rule: T2 starts at t2Addr, must end at or
// below the mailbox, and the mailbox must sit below the patched arena.
func checkBudget(t2Addr uint32, t2Len int, mailbox, arenaLo uint32) error {
	end := uint64(t2Addr) + uint64(t2Len)
	if end > uint64(mailbox) {
		return fmt.Errorf("T2 0x%08X..0x%08X runs past the mailbox at 0x%08X by 0x%X bytes", t2Addr, end, mailbox, end-uint64(mailbox))
	}
	if mailbox >= arenaLo {
		return fmt.Errorf("mailbox 0x%08X is not below __OSArenaLo 0x%08X", mailbox, arenaLo)
	}
	return nil
}

// TestT2EndsBelowMailbox guards the code-space budget (docs/08 §4.5,
// docs/09 §6): a C change that grows T2 into the mailbox fails here, at
// blob regen time, instead of corrupting the mailbox on a live Dolphin.
// The mailbox's own end against the arena is TestModRegions'
// (wwapi/mailbox), whose MailboxSize TestMailboxLayout pins to
// mailbox_core.h.
func TestT2EndsBelowMailbox(t *testing.T) {
	mailbox := define(t, mailboxH, "MAILBOX_ADDR")
	_, patches := buildPyFacts(t)
	arenaLo := patchedArenaLo(t, patches)
	if err := checkBudget(T2Address, len(T2Bytes), mailbox, arenaLo); err != nil {
		t.Fatal(err)
	}
	// Go's copies (wwapi/mailbox, the canaries and region-probe use
	// them) must agree with the C side.
	if mbox.Mailbox != mailbox || mbox.PatchedArenaLo != arenaLo || mbox.T2Base != T2Address {
		t.Errorf("wwapi/mailbox says T2 0x%08X, mailbox 0x%08X, arena 0x%08X; the C side says 0x%08X, 0x%08X, 0x%08X",
			mbox.T2Base, mbox.Mailbox, mbox.PatchedArenaLo, T2Address, mailbox, arenaLo)
	}
	end := T2Address + uint32(len(T2Bytes))
	t.Logf("T2 0x%08X..0x%08X, 0x%X bytes free below the mailbox (0x%08X); arena at 0x%08X", T2Address, end, mailbox-end, mailbox, arenaLo)
}

// TestCheckBudget: the rule rejects an over-budget blob and a mailbox at
// or past the arena, and accepts a blob that ends exactly at the mailbox.
func TestCheckBudget(t *testing.T) {
	for _, c := range []struct {
		len     int
		mailbox uint32
		arena   uint32
		ok      bool
	}{
		{0x2E08, 0x80412F00, 0x80414000, true},
		{0x2F00, 0x80412F00, 0x80414000, true},
		{0x2F01, 0x80412F00, 0x80414000, false},
		{0x3000, 0x80412F00, 0x80414000, false},
		{0x2E08, 0x80414000, 0x80414000, false},
	} {
		if err := checkBudget(0x80410000, c.len, c.mailbox, c.arena); (err == nil) != c.ok {
			t.Errorf("checkBudget(len 0x%X, mailbox 0x%08X, arena 0x%08X) = %v, want ok=%v", c.len, c.mailbox, c.arena, err, c.ok)
		}
	}
}

// TestBuildPyMatchesBlob: blob.go was produced by the build.py on disk.
// T2Address is build.py's inject_address, and every byte of every patch
// build.py lists (the OSInit arena patch and the 48 MB heap plan) is
// either in DOLPatches with the same value or left out of the diff because
// the vanilla byte already had it; no other DOLPatches byte falls inside
// any build.py patch's window (the hook sites Freighter writes lie
// between those windows, not inside them).
func TestBuildPyMatchesBlob(t *testing.T) {
	injectAddr, patches := buildPyFacts(t)
	if injectAddr != T2Address {
		t.Errorf("build.py inject_address 0x%08X, blob.go T2Address 0x%08X", injectAddr, T2Address)
	}
	inBlob := map[uint32]byte{}
	for _, p := range DOLPatches {
		for i, b := range p.Bytes {
			inBlob[p.FileOff+uint32(i)] = b
		}
	}
	type window struct{ lo, hi uint32 }
	var windows []window
	want := map[uint32]byte{}
	for _, p := range patches {
		off := osInitT1File + (p.addr - osInitT1Load)
		windows = append(windows, window{off, off + uint32(len(p.bytes))})
		for i, b := range p.bytes {
			want[off+uint32(i)] = b
			if got, ok := inBlob[off+uint32(i)]; ok && got != b {
				t.Errorf("file 0x%X (0x%08X): build.py writes 0x%02X, blob.go DOLPatches has 0x%02X", off+uint32(i), p.addr+uint32(i), b, got)
			}
		}
	}
	inWindow := func(off uint32) bool {
		for _, w := range windows {
			if off >= w.lo && off < w.hi {
				return true
			}
		}
		return false
	}
	n := 0
	for off, b := range inBlob {
		if !inWindow(off) {
			continue
		}
		n++
		if w, ok := want[off]; !ok || w != b {
			t.Errorf("blob.go DOLPatches writes 0x%02X at file 0x%X inside a build.py patch window, build.py does not", b, off)
		}
	}
	if n == 0 {
		t.Error("blob.go has no DOLPatches bytes in the OSInit window: the arena-lo patch is missing")
	}
}

// TestFrameShimLoadsCallbackSlot: frame_shim's hand-written
// `lis 12, 0x8041; lwz 12, 0x2F08(12)` is the one place the mailbox
// address is baked into PPC code. It must load CALLBACK_PTR_ADDR, which
// must be MAILBOX_ADDR+8, and the committed blob must contain that pair.
func TestFrameShimLoadsCallbackSlot(t *testing.T) {
	mailbox := define(t, mailboxH, "MAILBOX_ADDR")
	cb := define(t, hooksC, "CALLBACK_PTR_ADDR")
	if cb != mailbox+8 {
		t.Fatalf("CALLBACK_PTR_ADDR 0x%08X, want MAILBOX_ADDR+8 = 0x%08X", cb, mailbox+8)
	}
	if cb&0x8000 != 0 || cb>>16 != lisR12_8041&0xFFFF {
		t.Fatalf("CALLBACK_PTR_ADDR 0x%08X is not reachable as lis 12,0x8041 + a positive lwz offset; frame_shim needs rewriting", cb)
	}
	src, err := os.ReadFile(hooksC)
	if err != nil {
		t.Fatal(err)
	}
	asm := fmt.Sprintf(`"lis\s+12,\s*0x8041\s*\\n"\s*"lwz\s+12,\s*0x%X\(12\)`, cb&0xFFFF)
	if !regexp.MustCompile(asm).Match(src) {
		t.Errorf("hooks.c frame_shim does not load 0x%X(12) after lis 12,0x8041", cb&0xFFFF)
	}
	lwz := uint32(lwzR12R12Base | cb&0xFFFF)
	found := false
	for i := 0; i+8 <= len(T2Bytes); i += 4 {
		if binary.BigEndian.Uint32(T2Bytes[i:]) == lisR12_8041 && binary.BigEndian.Uint32(T2Bytes[i+4:]) == lwz {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("blob.go T2Bytes has no lis r12,0x8041; lwz r12,0x%X(r12) pair: the blob is stale against hooks.c", cb&0xFFFF)
	}
}

// TestExecShimLoadsHookSlot: exec_shim (hooks.c, the remote-Link design
// §4) loads the REL's exec-hook word, mailbox+WWREL_EXEC_HOOK_OFF
// (inject/include/wwrel.h), and falls back to fpcMtd_Execute 0x8003EF38,
// and the committed blob carries those ten instructions as one run.
func TestExecShimLoadsHookSlot(t *testing.T) {
	mailbox := define(t, mailboxH, "MAILBOX_ADDR")
	hookOff := define(t, "../../inject/include/wwrel.h", "WWREL_EXEC_HOOK_OFF")
	hook := define(t, hooksC, "EXEC_HOOK_PTR_ADDR")
	if hook != mailbox+hookOff {
		t.Fatalf("EXEC_HOOK_PTR_ADDR 0x%08X, want MAILBOX_ADDR+WWREL_EXEC_HOOK_OFF = 0x%08X", hook, mailbox+hookOff)
	}
	if hook&0x8000 != 0 || hook>>16 != lisR12_8041&0xFFFF {
		t.Fatalf("EXEC_HOOK_PTR_ADDR 0x%08X is not reachable as lis 12,0x8041 + a positive lwz offset; exec_shim needs rewriting", hook)
	}
	src, err := os.ReadFile(hooksC)
	if err != nil {
		t.Fatal(err)
	}
	asm := fmt.Sprintf(`"lis\s+12,\s*0x8041\s*\\n"\s*"lwz\s+12,\s*0x%X\(12\)`, hook&0xFFFF)
	if !regexp.MustCompile(asm).Match(src) {
		t.Errorf("hooks.c exec_shim does not load 0x%X(12) after lis 12,0x8041", hook&0xFFFF)
	}
	want := []uint32{
		lisR12_8041, lwzR12R12Base | hook&0xFFFF,
		0x2C0C0000, // cmpwi r12, 0
		0x4182000C, // beq- +0xC
		0x7D8903A6, // mtctr r12
		0x4E800420, // bctr: wwrel_exec_hook
		0x3D808003, // lis r12, 0x8003
		0x618CEF38, // ori r12, r12, 0xEF38: fpcMtd_Execute
		0x7D8903A6, // mtctr r12
		0x4E800420, // bctr
	}
	found := false
	for i := 0; i+4*len(want) <= len(T2Bytes) && !found; i += 4 {
		found = true
		for k, w := range want {
			if binary.BigEndian.Uint32(T2Bytes[i+4*k:]) != w {
				found = false
				break
			}
		}
	}
	if !found {
		t.Error("blob.go T2Bytes has no exec_shim run (lis/lwz 0x30BC, cmpwi, beq, bctr, fpcMtd_Execute fallback): the blob is stale against hooks.c")
	}
}

// TestSeachartShimLoadsVisWord: seachart_shim (hooks.c, the ww-online
// review's idea 14) calls J2DScreen::draw 0x802D0F2C, then loads the
// visuals block's sea chart word, mailbox+WWVIS_OFF+WWVIS_SEACHART_FN_OFF
// (inject/include/wwvis.h), and calls it with r30/r31; build.py hooks it
// at 0x801BB06C; and the committed blob carries the run.
func TestSeachartShimLoadsVisWord(t *testing.T) {
	const wwvisH = "../../inject/include/wwvis.h"
	mailbox := define(t, mailboxH, "MAILBOX_ADDR")
	word := mailbox + define(t, wwvisH, "WWVIS_OFF") + define(t, wwvisH, "WWVIS_SEACHART_FN_OFF")
	if got := define(t, hooksC, "SEACHART_FN_PTR_ADDR"); got != word {
		t.Fatalf("SEACHART_FN_PTR_ADDR 0x%08X, want MAILBOX_ADDR+WWVIS_OFF+WWVIS_SEACHART_FN_OFF = 0x%08X", got, word)
	}
	if word&0x8000 != 0 || word>>16 != lisR12_8041&0xFFFF {
		t.Fatalf("the sea chart word 0x%08X is not reachable as lis 12,0x8041 + a positive lwz offset; seachart_shim needs rewriting", word)
	}
	py, err := os.ReadFile(buildPy)
	if err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`(?m)^\s*proj\.hook_branchlink\("seachart_shim",\s*0x801BB06C\)`).Match(py) {
		t.Error(`build.py does not hook seachart_shim at 0x801BB06C (dDlst_FMAP_c::draw's bl J2DScreen::draw)`)
	}
	want := []uint32{
		0x3D80802D, // lis r12, 0x802D
		0x618C0F2C, // ori r12, r12, 0x0F2C: J2DScreen::draw
		0x7D8903A6, // mtctr r12
		0x4E800421, // bctrl
		lisR12_8041, lwzR12R12Base | word&0xFFFF,
		0x2C0C0000, // cmpwi r12, 0
		0x41820014, // beq- +0x14
		0x7FC3F378, // mr r3, r30
		0x7FE4FB78, // mr r4, r31
		0x7D8903A6, // mtctr r12
		0x4E800421, // bctrl: wwvis_seachart_draw
	}
	found := false
	for i := 0; i+4*len(want) <= len(T2Bytes) && !found; i += 4 {
		found = true
		for k, w := range want {
			if binary.BigEndian.Uint32(T2Bytes[i+4*k:]) != w {
				found = false
				break
			}
		}
	}
	if !found {
		t.Error("blob.go T2Bytes has no seachart_shim run (J2DScreen::draw, lis/lwz of the sea chart word, cmpwi, beq, r30/r31, bctrl): the blob is stale against hooks.c")
	}
}
