package crash

import (
	"bufio"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
)

// Sym is one function symbol.
type Sym struct {
	Addr, Size uint32 // Size 0: unknown
	Name       string
}

// Symbols is a sorted function table.
type Symbols struct {
	syms []Sym
}

// Len is how many symbols s holds; a nil s holds none.
func (s *Symbols) Len() int {
	if s == nil {
		return 0
	}
	return len(s.syms)
}

func newSymbols(syms []Sym) *Symbols {
	sort.Slice(syms, func(i, j int) bool { return syms[i].Addr < syms[j].Addr })
	return &Symbols{syms: syms}
}

// unsizedReach is how far past an unsized symbol Lookup still names it.
const unsizedReach = 0x1000

// Lookup returns the symbol a falls in and a's offset from it: the
// closest symbol at or below a, as long as a is inside its size (or
// within unsizedReach of an unsized one).
func (s *Symbols) Lookup(a uint32) (Sym, uint32, bool) {
	if s.Len() == 0 {
		return Sym{}, 0, false
	}
	i := sort.Search(len(s.syms), func(i int) bool { return s.syms[i].Addr > a }) - 1
	if i < 0 {
		return Sym{}, 0, false
	}
	sym := s.syms[i]
	off := a - sym.Addr
	if (sym.Size != 0 && off >= sym.Size) || (sym.Size == 0 && off >= unsizedReach) {
		return Sym{}, 0, false
	}
	return sym, off, true
}

// ParseDOLSymbols reads a decomp-toolkit symbols.txt (the tww decomp's
// config/GZLE01/symbols.txt) and keeps the functions in .text and .init:
//
//	__start = .init:0x80003140; // type:function size:0x138 scope:weak
func ParseDOLSymbols(r io.Reader) (*Symbols, error) {
	var syms []Sym
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for sc.Scan() {
		line := sc.Text()
		name, rest, ok := strings.Cut(line, " = ")
		if !ok {
			continue
		}
		loc, attrs, _ := strings.Cut(rest, ";")
		sec, hex, ok := strings.Cut(loc, ":")
		if !ok || (sec != ".text" && sec != ".init") || !strings.Contains(attrs, "type:function") {
			continue
		}
		a, err := strconv.ParseUint(strings.TrimPrefix(hex, "0x"), 16, 32)
		if err != nil {
			continue
		}
		sym := Sym{Addr: uint32(a), Name: strings.TrimSpace(name)}
		for _, f := range strings.Fields(attrs) {
			if v, ok := strings.CutPrefix(f, "size:"); ok {
				if n, err := strconv.ParseUint(strings.TrimPrefix(v, "0x"), 16, 32); err == nil {
					sym.Size = uint32(n)
				}
			}
		}
		syms = append(syms, sym)
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("crash: symbols.txt: %w", err)
	}
	return newSymbols(syms), nil
}

// ParseDolphinMap reads the .text section of a CodeWarrior-style Dolphin
// map, the layout inject/build.py's write_symbol_map writes for T2
// (inject/build/WWMultiplayer.map):
//
//	.text section layout
//	  Starting        Virtual
//	  address  Size   address
//	  -----------------------
//	  80410000 000120 80410000  0 main01_init_hook
func ParseDolphinMap(r io.Reader) (*Symbols, error) {
	var syms []Sym
	inText := false
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		line := sc.Text()
		if strings.HasSuffix(strings.TrimSpace(line), "section layout") {
			inText = strings.HasPrefix(strings.TrimSpace(line), ".text ")
			continue
		}
		if !inText {
			continue
		}
		f := strings.Fields(line)
		if len(f) < 5 {
			continue
		}
		size, err1 := strconv.ParseUint(f[1], 16, 32)
		virt, err2 := strconv.ParseUint(f[2], 16, 32)
		if err1 != nil || err2 != nil {
			continue
		}
		syms = append(syms, Sym{Addr: uint32(virt), Size: uint32(size), Name: f[4]})
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("crash: map: %w", err)
	}
	return newSymbols(syms), nil
}

// Resolver names addresses: a linked REL module's section first (REL
// code sits on the game's heaps), then T2's map, then the DOL's symbols.
// Any of them may be empty.
type Resolver struct {
	DOL, T2 *Symbols
	Modules []Module
	// OwnModule is this mod's REL module id (inject.RELModuleID), named
	// in the output; 0 names none.
	OwnModule uint32
}

// Name describes a: "name+0xoff", "REL 0x58 sec 1 +0x1A4", or "?".
func (r Resolver) Name(a uint32) string {
	for _, m := range r.Modules {
		for _, s := range m.Sections {
			if a >= s.Start && a < s.End {
				own := ""
				if r.OwnModule != 0 && m.ID == r.OwnModule {
					own = " (WW Multiplayer REL)"
				}
				return fmt.Sprintf("REL 0x%X sec %d +0x%X%s", m.ID, s.Index, a-s.Start, own)
			}
		}
	}
	if sym, off, ok := r.T2.Lookup(a); ok {
		return fmt.Sprintf("T2 %s+0x%X", sym.Name, off)
	}
	if sym, off, ok := r.DOL.Lookup(a); ok {
		return fmt.Sprintf("%s+0x%X", sym.Name, off)
	}
	return "?"
}
