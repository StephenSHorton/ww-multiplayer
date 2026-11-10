// Command gen_wstable writes inject/include/wstable_gen.h, the widescreen
// J2D screen table (docs/design/native-widescreen.md 3.3; section 8 stage 2
// item 4, stage 3 item 3), from the Go row list in rows.go:
//
//	go run ./tools/gen_wstable                  # write the header
//	go run ./tools/gen_wstable -check           # exit 1 if it is stale
//	go run ./tools/gen_wstable -names <disc>    # rewrite testdata/blo_names.txt from a vanilla disc (read only)
//
// The header is C data only (no blob change until a C unit includes it).
// Its tests: the header is fresh, no two .blo name strings of the vanilla
// DOL and RELs (testdata/blo_names.txt, file NAME strings only) share a
// hash, every row's name is one of them, and the rows are the plan's.
package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// HeaderPath and NamesPath are relative to the module root.
const (
	HeaderPath = "inject/include/wstable_gen.h"
	NamesPath  = "tools/gen_wstable/testdata/blo_names.txt"
)

// Row flags: the kind in bits 0-1, then single bits (plan 3.3).
const (
	kindNone  = 0 // no shift
	kindAllL  = 1 // every anchor child shifts left
	kindAllR  = 2 // every anchor child shifts right
	kindByCX  = 3 // centre x < 320.5 -> left, else right
	kindMask  = 0x03
	flagGeom  = 0x04 // widen a childless full-screen PIC1
	flagScrL  = 0x08 // move the J2DScreen itself left (scissored screens)
	flagScrR  = 0x10 // move the J2DScreen itself right
	flagEpoch = 0x20 // re-latch the 2D mode before applying (ws_latch)
)

// Rule actions.
type action uint8

const (
	actNone action = 0 // leave the pane (and its subtree) where it is
	actL    action = 1 // shift left
	actR    action = 2 // shift right
	actEdge action = 3 // move an edge at abs x <= 0 or >= 640
)

var actionNames = [...]string{"NONE", "L", "R", "EDGE"}

type rule struct {
	tag string
	act action
}

type row struct {
	name  string // without ".blo"
	kind  uint8
	flags uint8 // GEOM, SCR_L, SCR_R, EPOCH
	rules []rule
	stage string // the plan's stage column, for the comment
}

// tags expands specs ("ben1-4", "ip00-03", "pn") into rules with act.
// A range keeps the start's digit width. It panics on a bad spec: the
// list is source, and the tests run it.
func tags(act action, specs ...string) []rule {
	var out []rule
	for _, s := range specs {
		lo, hi, ok := strings.Cut(s, "-")
		if !ok {
			out = append(out, rule{s, act})
			continue
		}
		i := len(lo)
		for i > 0 && lo[i-1] >= '0' && lo[i-1] <= '9' {
			i--
		}
		prefix, digits := lo[:i], lo[i:]
		a, err1 := strconv.Atoi(digits)
		b, err2 := strconv.Atoi(hi)
		if digits == "" || err1 != nil || err2 != nil || b < a {
			panic("gen_wstable: bad tag range " + s)
		}
		for n := a; n <= b; n++ {
			out = append(out, rule{fmt.Sprintf("%s%0*d", prefix, len(digits), n), act})
		}
	}
	return out
}

// NameHash is the table's hash (plan 3.3): h = 5381; for each byte c,
// h = h*33 ^ (c | 0x20), u32 wrap, over the name exactly as
// J2DScreen::set receives it (".blo" included). C reads c as unsigned.
func NameHash(name string) uint32 {
	h := uint32(5381)
	for i := 0; i < len(name); i++ {
		h = h*33 ^ uint32(name[i]|0x20)
	}
	return h
}

// TagValue is the C multichar constant for a tag of 1-4 characters,
// right-justified: 'l' = 0x6C, 'pn' = 0x706E, 'wp04' = 0x77703034.
func TagValue(tag string) (uint32, error) {
	if len(tag) < 1 || len(tag) > 4 {
		return 0, fmt.Errorf("tag %q: want 1-4 characters", tag)
	}
	var v uint32
	for i := 0; i < len(tag); i++ {
		c := tag[i]
		if c < 0x20 || c > 0x7E || c == '\'' || c == '\\' {
			return 0, fmt.Errorf("tag %q: character %q", tag, c)
		}
		v = v<<8 | uint32(c)
	}
	return v, nil
}

func flagNames(r row) string {
	var parts []string
	switch r.kind {
	case kindAllL:
		parts = append(parts, "ALL_L")
	case kindAllR:
		parts = append(parts, "ALL_R")
	case kindByCX:
		parts = append(parts, "BY_CX")
	}
	for _, f := range []struct {
		bit  uint8
		name string
	}{{flagGeom, "GEOM"}, {flagScrL, "SCR_L"}, {flagScrR, "SCR_R"}, {flagEpoch, "EPOCH"}} {
		if r.flags&f.bit != 0 {
			parts = append(parts, f.name)
		}
	}
	if len(parts) == 0 {
		return "rules only"
	}
	return strings.Join(parts, " ")
}

// Generate returns the header for rows.
func Generate(rows []row) ([]byte, error) {
	var b bytes.Buffer
	nRules := 0
	seen := map[uint32]string{}
	for _, r := range rows {
		if r.kind > kindMask || r.flags&kindMask != 0 || r.flags&^uint8(flagGeom|flagScrL|flagScrR|flagEpoch) != 0 {
			return nil, fmt.Errorf("%s: bad kind/flags", r.name)
		}
		shift := r.kind != kindNone || r.flags&(flagScrL|flagScrR) != 0
		if shift && r.flags&flagGeom != 0 {
			return nil, fmt.Errorf("%s: a shift and GEOM in one row (plan 3.3)", r.name)
		}
		for _, ru := range r.rules {
			if shift && ru.act == actEdge {
				return nil, fmt.Errorf("%s: a shift and EDGE in one row (plan 3.3)", r.name)
			}
		}
		h := NameHash(r.name + ".blo")
		if prev, dup := seen[h]; dup {
			return nil, fmt.Errorf("%s.blo and %s.blo share hash 0x%08X", r.name, prev, h)
		}
		seen[h] = r.name
		if len(r.rules) > 255 {
			return nil, fmt.Errorf("%s: more than 255 rules", r.name)
		}
		nRules += len(r.rules)
	}
	if nRules > 255 {
		return nil, fmt.Errorf("%d rules: first is a u8", nRules)
	}

	b.WriteString(`// GENERATED by tools/gen_wstable from tools/gen_wstable/rows.go. DO NOT EDIT:
// change the list and run go run ./tools/gen_wstable (-check compares; its
// tests fail on a stale header).
//
// The widescreen J2D screen table (docs/design/native-widescreen.md 3.3;
// section 8 stage 2 item 4, stage 3 item 3). Hook A (0x802D0968) stashes
// the name J2DScreen::set received; hook B (0x802D09DC) hashes it with
// ws_name_hash and, on a row, applies it (EPOCH first: ws_latch).
//
// Hash: h = 5381; for each byte c (unsigned): h = h*33 ^ (c | 0x20), u32
// wrap, over the name exactly as set receives it, ".blo" included
// ("main_parts1.blo"). No two .blo name strings in the vanilla DOL and
// RELs share a hash (tools/gen_wstable/testdata/blo_names.txt, tested).
//
// Row (WsRow, 8 bytes): hash; flags; n rules; first = index of its first
// rule in WST_RULE_TAG / WST_RULE_ACT; pad 0. flags: the kind in bits 0-1
// (WST_KIND_*), then WST_F_GEOM, WST_F_SCR_L, WST_F_SCR_R, WST_F_EPOCH.
// Rule (5 bytes, as two parallel arrays so no load is unaligned): a J2DPane
// tag, the C multichar value right-justified ('l' = 0x6C, 'pn' = 0x706E,
// 'wp04' = 0x77703034), and its action WST_ACT_*.
//
// Shift kinds, SCR_* and the NONE/L/R actions act only when the latched
// ui_shift is non-zero (stage 3); GEOM and EDGE act whenever 2D is
// compressed. A stage-2 walker can ignore them: ui_shift is 0 there.
//
// Defines static const data: include it from one C unit (the walker).
#ifndef WW_WSTABLE_GEN_H
#define WW_WSTABLE_GEN_H

#include "wwapi.h"

#define WST_KIND_MASK   0x03 // flags bits 0-1: the shift kind
#define WST_KIND_NONE   0    // no shift
#define WST_KIND_ALL_L  1    // every anchor child shifts left
#define WST_KIND_ALL_R  2    // every anchor child shifts right
#define WST_KIND_BY_CX  3    // centre x < 320.5 -> left, else right
#define WST_F_GEOM      0x04 // widen a childless full-screen PIC1
#define WST_F_SCR_L     0x08 // move the J2DScreen itself left
#define WST_F_SCR_R     0x10 // move the J2DScreen itself right
#define WST_F_EPOCH     0x20 // ws_latch() before applying
#define WST_ACT_NONE    0    // the pane stays (and is not walked into)
#define WST_ACT_L       1    // shift left
#define WST_ACT_R       2    // shift right
#define WST_ACT_EDGE    3    // move an edge at abs x <= 0 or >= 640
#define WST_ROOT_TAG    0x524F4F54 // 'ROOT': the anchor parent
`)
	fmt.Fprintf(&b, "#define WST_ROWS        %d\n", len(rows))
	fmt.Fprintf(&b, "#define WST_RULES       %d\n", nRules)
	fmt.Fprintf(&b, "#define WST_DATA_BYTES  0x%X // WST_ROWS*8 + WST_RULES*5\n", len(rows)*8+nRules*5)
	b.WriteString(`
typedef struct {
    u32 hash;
    u8 flags;
    u8 n;
    u8 first;
    u8 pad;
} WsRow;

_Static_assert(sizeof(WsRow) == 8, "WsRow is 8 bytes");

static inline u32 ws_name_hash(const char* name) {
    const unsigned char* p = (const unsigned char*)name;
    u32 h = 5381;
    while (*p) h = (h * 33) ^ (u32)(*p++ | 0x20);
    return h;
}

static const WsRow WST_ROW[WST_ROWS] = {
`)
	first := 0
	for _, r := range rows {
		fmt.Fprintf(&b, "    { 0x%08X, 0x%02X, %2d, %2d, 0 }, // %s.blo: %s (stage %s)\n",
			NameHash(r.name+".blo"), r.kind|r.flags, len(r.rules), first, r.name, flagNames(r), r.stage)
		first += len(r.rules)
	}
	b.WriteString("};\n\nstatic const u32 WST_RULE_TAG[WST_RULES] = {\n")
	var acts bytes.Buffer
	i := 0
	for _, r := range rows {
		for _, ru := range r.rules {
			v, err := TagValue(ru.tag)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", r.name, err)
			}
			fmt.Fprintf(&b, "    0x%08X, // %2d '%s' %s (%s)\n", v, i, ru.tag, actionNames[ru.act], r.name)
			fmt.Fprintf(&acts, "    WST_ACT_%s, // %2d '%s' (%s)\n", actionNames[ru.act], i, ru.tag, r.name)
			i++
		}
	}
	b.WriteString("};\n\nstatic const u8 WST_RULE_ACT[WST_RULES] = {\n")
	b.Write(acts.Bytes())
	b.WriteString("};\n\n#endif\n")
	return b.Bytes(), nil
}

func main() {
	check := flag.Bool("check", false, "compare instead of writing; exit 1 if stale")
	names := flag.String("names", "", "a vanilla disc image (ISO or CISO): rewrite "+NamesPath+" from it (read only)")
	flag.Parse()
	if err := run(*check, *names); err != nil {
		fmt.Fprintln(os.Stderr, "gen_wstable:", err)
		os.Exit(1)
	}
}

func run(check bool, disc string) error {
	root, err := moduleRoot()
	if err != nil {
		return err
	}
	if disc != "" {
		list, err := BloNamesFromDisc(disc)
		if err != nil {
			return err
		}
		dst := filepath.Join(root, filepath.FromSlash(NamesPath))
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		fmt.Printf("%d .blo names -> %s\n", len(list), NamesPath)
		return os.WriteFile(dst, []byte(strings.Join(list, "\n")+"\n"), 0o644)
	}
	out, err := Generate(tableRows())
	if err != nil {
		return err
	}
	dst := filepath.Join(root, filepath.FromSlash(HeaderPath))
	if check {
		have, err := os.ReadFile(dst)
		if err != nil {
			return err
		}
		if !bytes.Equal(bytes.ReplaceAll(have, []byte("\r\n"), []byte("\n")), out) {
			return fmt.Errorf("%s is stale: run go run ./tools/gen_wstable", HeaderPath)
		}
		return nil
	}
	return os.WriteFile(dst, out, 0o644)
}

// moduleRoot is the nearest directory at or above the working directory
// that holds go.mod.
func moduleRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", errors.New("no go.mod at or above the working directory")
		}
		dir = parent
	}
}
