// Package crash reads what the game leaves in RAM when it stops on a CPU
// exception that JUTException caught: the faulting thread's OSContext
// (registers, PC, LR), a back-chain walk of its stack, and the linked REL
// modules, and names each address from the decomp's DOL symbols, the T2
// map inject/build.py writes, or a REL module's section and offset. wwdev
// crash-context prints it (cmd/wwdev).
//
// The idea comes from datstarkey/ww-online's
// scripts/dolphin-crash-context.py (MIT); this is a clean-room Go
// version over mem.Reader, with every address checked against the tww
// decomp (wwapi/addr crash.go).
//
// It only sees faults JUTException's errorHandler caught (the "CONTEXT:
// ... EXCEPTION" screen). An emulator-side stop, such as Dolphin's
// "Invalid read" panic, leaves exCallbackObject as it was; Dolphin's own
// log (launch.DevLogArgs) is the place to look for those.
//
// # v0 UNSTABLE
//
// This package is part of wwapi v0. Its API may change in any 0.x minor
// version.
package crash

import (
	"fmt"

	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mem"
)

// Exception is exCallbackObject and the OSContext it points at.
type Exception struct {
	// Caught is false while exCallbackObject's context is still 0: no
	// exception has reached JUTException since boot. The fields below are
	// then zero.
	Caught     bool
	Error      uint16 // OSError (OSError.h:15-31)
	ContextPtr uint32
	DSISR, DAR uint32 // the handler's param_3 and param_4
	GPR        [32]uint32
	CR, LR     uint32
	CTR, XER   uint32
	SRR0, SRR1 uint32 // SRR0 is the faulting PC
}

// SP is r1, the faulting thread's stack pointer.
func (e Exception) SP() uint32 { return e.GPR[1] }

// cpuExpName is JUTException::sCpuExpName (JUTException.cpp:34-52;
// sym:16920, 17 pointers), indexed by OSError.
var cpuExpName = [...]string{
	"SYSTEM RESET", "MACHINE CHECK", "DSI", "ISI", "EXTERNAL INTERRUPT",
	"ALIGNMENT", "PROGRAM", "FLOATING POINT", "DECREMENTER", "SYSTEM CALL",
	"TRACE", "PERFORMACE MONITOR", "BREAK POINT", "SYSTEM INTERRUPT",
	"THERMAL INTERRUPT", "PROTECTION", "FLOATING POINT",
}

// ErrorName is the name JUTException prints for err, or "OSError <n>"
// past its table.
func ErrorName(err uint16) string {
	if int(err) < len(cpuExpName) {
		return cpuExpName[err]
	}
	return fmt.Sprintf("OSError %d", err)
}

// ReadException reads exCallbackObject and, when it holds a context, the
// context's integer state. A context pointer outside MEM1 is an error.
func ReadException(m mem.Reader) (Exception, error) {
	cb, err := m.Read(addr.ExCallbackObject, addr.ExCallbackObjectSize)
	if err != nil {
		return Exception{}, fmt.Errorf("crash: exCallbackObject: %w", err)
	}
	e := Exception{
		Error:      mem.U16BE(cb[addr.ExCallbackOffError:]),
		ContextPtr: mem.U32BE(cb[addr.ExCallbackOffContext:]),
		DSISR:      mem.U32BE(cb[addr.ExCallbackOffDSISR:]),
		DAR:        mem.U32BE(cb[addr.ExCallbackOffDAR:]),
	}
	if e.ContextPtr == 0 {
		return Exception{}, nil
	}
	if !addr.InMEM1(e.ContextPtr) || !addr.InMEM1(e.ContextPtr+addr.OSContextIntSize-1) {
		return Exception{}, fmt.Errorf("crash: context pointer 0x%08X is outside MEM1", e.ContextPtr)
	}
	c, err := m.Read(e.ContextPtr, addr.OSContextIntSize)
	if err != nil {
		return Exception{}, fmt.Errorf("crash: OSContext at 0x%08X: %w", e.ContextPtr, err)
	}
	for i := range e.GPR {
		e.GPR[i] = mem.U32BE(c[addr.OSContextOffGPR+4*i:])
	}
	e.CR = mem.U32BE(c[addr.OSContextOffCR:])
	e.LR = mem.U32BE(c[addr.OSContextOffLR:])
	e.CTR = mem.U32BE(c[addr.OSContextOffCTR:])
	e.XER = mem.U32BE(c[addr.OSContextOffXER:])
	e.SRR0 = mem.U32BE(c[addr.OSContextOffSRR0:])
	e.SRR1 = mem.U32BE(c[addr.OSContextOffSRR1:])
	e.Caught = true
	return e, nil
}

// Frame is one stack frame of the back chain: its address and the return
// address saved in it ([sp+4], the LR save word of the PowerPC EABI).
type Frame struct {
	SP, Ret uint32
}

// MaxFrames is how deep Walk goes by default, as the Python reader did.
const MaxFrames = 20

// Walk follows the back chain from sp ([sp] is the caller's frame) for at
// most max frames. It stops at a null or 0xFFFFFFFF link, a frame
// outside MEM1, a misaligned frame, or a link that does not move up the
// stack (a loop), and says why in stop ("" when max was reached).
//
// Frame 0's Ret is only meaningful when the faulting function had called
// out before the fault (it writes its caller's LR save word, not its
// own): the faulting call site is Exception.LR, and the chain is reliable
// from frame 1 on.
func Walk(m mem.Reader, sp uint32, max int) (frames []Frame, stop string) {
	for len(frames) < max {
		if sp == 0 || sp == 0xFFFFFFFF {
			return frames, ""
		}
		if sp&3 != 0 {
			return frames, fmt.Sprintf("frame 0x%08X is misaligned", sp)
		}
		if !addr.InMEM1(sp) || !addr.InMEM1(sp+7) {
			return frames, fmt.Sprintf("frame 0x%08X is outside MEM1", sp)
		}
		b, err := m.Read(sp, 8)
		if err != nil {
			return frames, fmt.Sprintf("frame 0x%08X: %v", sp, err)
		}
		next := mem.U32BE(b)
		frames = append(frames, Frame{SP: sp, Ret: mem.U32BE(b[4:])})
		if next != 0 && next != 0xFFFFFFFF && next <= sp {
			return frames, fmt.Sprintf("back chain 0x%08X -> 0x%08X does not move up the stack", sp, next)
		}
		sp = next
	}
	return frames, ""
}

// Section is one REL section loaded in RAM.
type Section struct {
	Index      int
	Start, End uint32 // [Start, End)
	Exec       bool   // OS_SECTIONINFO_EXEC
}

// Module is one linked REL module.
type Module struct {
	ID       uint32
	Info     uint32 // its OSModuleInfo's address
	Sections []Section
}

// maxModules and maxSections bound the walk over a corrupt list.
const (
	maxModules  = 400
	maxSections = 64
)

// ReadModules walks __OSModuleList and returns every linked module with
// its loaded sections (offset and size both non-zero: bss and absent
// sections are left out). A link that leaves MEM1, or a list longer than
// maxModules, ends the walk with the modules read so far and an error.
func ReadModules(m mem.Reader) ([]Module, error) {
	head, err := mem.ReadU32(m, addr.OSModuleList)
	if err != nil {
		return nil, fmt.Errorf("crash: __OSModuleList: %w", err)
	}
	var mods []Module
	seen := map[uint32]bool{}
	for p := head; p != 0; {
		if len(mods) >= maxModules || seen[p] {
			return mods, fmt.Errorf("crash: module list loops or runs past %d entries at 0x%08X", maxModules, p)
		}
		seen[p] = true
		if !addr.InMEM1(p) || !addr.InMEM1(p+addr.OSModuleInfoOffSectionInfo+3) {
			return mods, fmt.Errorf("crash: module link 0x%08X is outside MEM1", p)
		}
		b, err := m.Read(p, addr.OSModuleInfoOffSectionInfo+4)
		if err != nil {
			return mods, fmt.Errorf("crash: module at 0x%08X: %w", p, err)
		}
		mod := Module{ID: mem.U32BE(b[addr.OSModuleInfoOffID:]), Info: p}
		n := mem.U32BE(b[addr.OSModuleInfoOffNumSections:])
		si := mem.U32BE(b[addr.OSModuleInfoOffSectionInfo:])
		if n > maxSections {
			return mods, fmt.Errorf("crash: module 0x%X at 0x%08X claims %d sections", mod.ID, p, n)
		}
		if n > 0 && addr.InMEM1(si) && addr.InMEM1(si+n*addr.OSSectionInfoSize-1) {
			t, err := m.Read(si, int(n*addr.OSSectionInfoSize))
			if err != nil {
				return mods, fmt.Errorf("crash: module 0x%X sections: %w", mod.ID, err)
			}
			for i := 0; i < int(n); i++ {
				off := mem.U32BE(t[i*addr.OSSectionInfoSize:])
				size := mem.U32BE(t[i*addr.OSSectionInfoSize+4:])
				start := off &^ addr.OSSectionInfoExec
				if start == 0 || size == 0 {
					continue
				}
				mod.Sections = append(mod.Sections, Section{
					Index: i, Start: start, End: start + size, Exec: off&addr.OSSectionInfoExec != 0,
				})
			}
		}
		mods = append(mods, mod)
		p = mem.U32BE(b[addr.OSModuleInfoOffNext:])
	}
	return mods, nil
}
