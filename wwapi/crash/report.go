package crash

import (
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/StephenSHorton/ww-multiplayer/wwapi/mem"
)

// Report is everything Read gathers for one crash.
type Report struct {
	Exception Exception
	Frames    []Frame
	Stop      string // why the walk ended early, "" if it did not
	Modules   []Module
	ModErr    error // a module-list problem; the modules read so far are kept
}

// Read reads the exception, the stack (MaxFrames deep) and the module
// list. With no exception caught yet it returns a Report whose
// Exception.Caught is false and reads nothing else.
func Read(m mem.Reader) (Report, error) {
	e, err := ReadException(m)
	if err != nil || !e.Caught {
		return Report{Exception: e}, err
	}
	rep := Report{Exception: e}
	rep.Frames, rep.Stop = Walk(m, e.SP(), MaxFrames)
	rep.Modules, rep.ModErr = ReadModules(m)
	return rep, nil
}

// writeHeader prints exCallbackObject's fields: the error, the context
// pointer, DSISR and DAR.
func writeHeader(w io.Writer, e Exception) {
	fmt.Fprintf(w, "%s EXCEPTION (OSError %d)  context 0x%08X  dsisr 0x%08X  dar 0x%08X\n",
		ErrorName(e.Error), e.Error, e.ContextPtr, e.DSISR, e.DAR)
}

// WritePartial prints what Read got from exCallbackObject before it
// failed (a context pointer outside MEM1, or a context it could not
// read): the error code, DSISR and DAR are still worth having. Nothing
// when the callback object held no context.
func (rep Report) WritePartial(w io.Writer) {
	if rep.Exception.ContextPtr == 0 {
		return
	}
	fmt.Fprintln(w, "exCallbackObject (the context it points at could not be read):")
	writeHeader(w, rep.Exception)
}

// Write prints rep, naming addresses through r (whose Modules, when
// empty, are taken from rep).
func (rep Report) Write(w io.Writer, r Resolver) {
	e := rep.Exception
	if !e.Caught {
		fmt.Fprintln(w, "No crash context: exCallbackObject's context is 0 (JUTException has caught nothing since boot).")
		return
	}
	if len(r.Modules) == 0 {
		r.Modules = rep.Modules
	}
	writeHeader(w, e)
	fmt.Fprintf(w, "PC   0x%08X  %s\n", e.SRR0, r.Name(e.SRR0))
	fmt.Fprintf(w, "LR   0x%08X  %s\n", e.LR, r.Name(e.LR))
	fmt.Fprintf(w, "CTR  0x%08X  %s\n", e.CTR, r.Name(e.CTR))
	fmt.Fprintf(w, "CR 0x%08X  XER 0x%08X  MSR 0x%08X\n", e.CR, e.XER, e.SRR1)
	for i := 0; i < 32; i += 4 {
		var cols []string
		for j := i; j < i+4; j++ {
			cols = append(cols, fmt.Sprintf("r%-2d 0x%08X", j, e.GPR[j]))
		}
		fmt.Fprintln(w, strings.Join(cols, "  "))
	}
	fmt.Fprintf(w, "Stack from r1 (frame 0's ret is stale unless the faulting function called out; LR is the call site):\n")
	for i, f := range rep.Frames {
		fmt.Fprintf(w, "  #%-2d sp 0x%08X  ret 0x%08X  %s\n", i, f.SP, f.Ret, r.Name(f.Ret))
	}
	if rep.Stop != "" {
		fmt.Fprintf(w, "  (walk stopped: %s)\n", rep.Stop)
	}
	ids := make([]string, 0, len(rep.Modules))
	for _, m := range rep.Modules {
		ids = append(ids, fmt.Sprintf("0x%X", m.ID))
	}
	sort.Strings(ids)
	fmt.Fprintf(w, "Linked REL modules: %d", len(rep.Modules))
	if len(ids) > 0 {
		fmt.Fprintf(w, " (%s)", strings.Join(ids, " "))
	}
	fmt.Fprintln(w)
	if rep.ModErr != nil {
		fmt.Fprintf(w, "  module list: %v\n", rep.ModErr)
	}
	if r.DOL.Len() == 0 {
		fmt.Fprintln(w, "(no DOL symbols loaded: set WW_TWW_SYMBOLS to the decomp's config/GZLE01/symbols.txt)")
	}
	if r.T2.Len() == 0 {
		fmt.Fprintln(w, "(no T2 map loaded: build inject/ for inject/build/WWMultiplayer.map, or set WW_T2_MAP)")
	}
}
