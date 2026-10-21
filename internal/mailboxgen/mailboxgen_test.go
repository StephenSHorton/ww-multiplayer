package mailboxgen

import (
	"fmt"
	"strings"
	"testing"
)

const sample = `#define MAILBOX_ADDR 0x80412F00
#define CAP 2

typedef struct {
    u8  a;    // +0x00
    u32 b;    // +0x04
} Inner;  // 8 bytes, aligned to 4

typedef struct {
    u32 spawn_trigger;   // +0x00: heartbeat
    // a comment line inside the struct
    u8  flag;            // +0x04
    Inner inner[CAP];    // +0x08
    u16 tail;            // +0x18
} Mailbox;
`

func TestParseLayout(t *testing.T) {
	l, err := Parse("mailbox_core.h", []byte(sample), nil)
	if err != nil {
		t.Fatal(err)
	}
	mb := l.Mailbox()
	if mb.Size != 0x1C {
		t.Errorf("Mailbox size 0x%X, want 0x1C (0x1A rounded up to 4)", mb.Size)
	}
	if l.Structs[0].Size != 8 || l.Structs[0].Align != 4 {
		t.Errorf("Inner = %+v", l.Structs[0])
	}
	if f := mb.Fields[2]; f.CName != "inner" || f.Offset != 8 || f.Len != 2 || f.Size != 8 {
		t.Errorf("inner = %+v", f)
	}
	out, err := Generate("mailbox_core.h", []byte(sample), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"Mailbox uint32 = 0x80412F00", "Cap = 2 // CAP", "InnerOffB = 0x04",
		"MailboxSize = 0x1C", "OffInner = 0x08 // Inner inner[CAP]", "LenInner = Cap", "OffTail = 0x18",
	} {
		if !strings.Contains(strings.Join(strings.Fields(string(out)), " "), want) {
			t.Errorf("generated source lacks %q:\n%s", want, out)
		}
	}
}

func TestParseRefuses(t *testing.T) {
	for name, c := range map[string]struct{ from, to, err string }{
		"stale comment":   {"u16 tail;            // +0x18", "u16 tail;            // +0x1A", "comment says tail"},
		"unknown type":    {"u16 tail;", "u64 tail;", `unknown type "u64"`},
		"unknown size":    {"inner[CAP]", "inner[NOPE]", `unknown array size "NOPE"`},
		"block comment":   {"// a comment line", "/* a comment */", "block comments"},
		"junk line":       {"    // a comment line inside the struct", "    int x = 1", "cannot parse"},
		"no mailbox":      {"} Mailbox;", "} Other;", "no Mailbox struct"},
		"no address":      {"#define MAILBOX_ADDR 0x80412F00", "#define OTHER 1", "no MAILBOX_ADDR"},
		"duplicate field": {"u16 tail;", "u16 flag;", "flag declared twice"},
	} {
		src := strings.Replace(sample, c.from, c.to, 1)
		if src == sample {
			t.Fatalf("%s: replacement did not apply", name)
		}
		if _, err := Generate("mailbox_core.h", []byte(src), nil); err == nil || !strings.Contains(err.Error(), c.err) {
			t.Errorf("%s: err = %v, want %q", name, err, c.err)
		}
	}
}

func TestGoName(t *testing.T) {
	for c, want := range map[string]string{
		"spawn_trigger": "SpawnTrigger", "MAX_PUPPETS": "MaxPuppets", "_reserved_hdr": "ReservedHdr",
		"mat14_tex0": "Mat14Tex0", "input_trigger_l": "InputTriggerL",
	} {
		if got := GoName(c); got != want {
			t.Errorf("GoName(%q) = %q, want %q", c, got, want)
		}
	}
}

// The Phase E3 split: the root header includes a mailbox_*.h in place.
// Moving Inner and CAP into the included header changes no offset and,
// apart from the generated file's first line, nothing in the output.
const splitCore = `#define MAILBOX_ADDR 0x80412F00
#include "wwapi.h"
#include "mailbox_mp.h"

typedef struct {
    u32 spawn_trigger;   // +0x00: heartbeat
    // a comment line inside the struct
    u8  flag;            // +0x04
    Inner inner[CAP];    // +0x08
    u16 tail;            // +0x18
} Mailbox;
`

const splitMp = `#ifndef MAILBOX_MP_H
#define MAILBOX_MP_H
#include "wwapi.h"
#define CAP 2

typedef struct {
    u8  a;    // +0x00
    u32 b;    // +0x04
} Inner;  // 8 bytes, aligned to 4
#endif
`

func reader(files map[string]string) Reader {
	return func(name string) ([]byte, error) {
		s, ok := files[name]
		if !ok {
			return nil, fmt.Errorf("no %s", name)
		}
		return []byte(s), nil
	}
}

func TestParseIncludes(t *testing.T) {
	one, err := Generate("mailbox_core.h", []byte(sample), nil)
	if err != nil {
		t.Fatal(err)
	}
	// An include seen twice is read once, as its guard would have it.
	core := strings.Replace(splitCore, `#include "mailbox_mp.h"`, "#include \"mailbox_mp.h\"\n#include \"mailbox_mp.h\"", 1)
	l, err := Parse("mailbox_core.h", []byte(core), reader(map[string]string{"mailbox_mp.h": splitMp}))
	if err != nil {
		t.Fatal(err)
	}
	if len(l.Sources) != 2 || l.Sources[0].Name != "mailbox_core.h" || l.Sources[1].Name != "mailbox_mp.h" {
		t.Errorf("Sources = %v", l.Sources)
	}
	two, err := l.Go()
	if err != nil {
		t.Fatal(err)
	}
	first := func(b []byte) (string, string) {
		s := string(b)
		i := strings.IndexByte(s, '\n')
		return s[:i], s[i:]
	}
	h1, rest1 := first(one)
	h2, rest2 := first(two)
	if rest1 != rest2 {
		t.Errorf("split headers generate other code:\n%s\nwant\n%s", two, one)
	}
	if !strings.Contains(h1, "from inject/include/mailbox_core.h.") || !strings.Contains(h2, "from inject/include/mailbox_core.h (with mailbox_mp.h).") {
		t.Errorf("generated headers %q, %q", h1, h2)
	}
}

func TestParseIncludeRefuses(t *testing.T) {
	files := map[string]string{"mailbox_mp.h": splitMp}
	for name, c := range map[string]struct {
		src  string
		read Reader
		err  string
	}{
		"no reader":      {splitCore, nil, "mailbox_core.h:3: cannot read included mailbox_mp.h"},
		"missing file":   {splitCore, reader(nil), "mailbox_core.h:3: no mailbox_mp.h"},
		"inside struct":  {strings.Replace(splitCore, "    u8  flag;", "#include \"mailbox_x.h\"\n    u8  flag;", 1), reader(files), "#include inside a struct"},
		"error in mp.h":  {splitCore, reader(map[string]string{"mailbox_mp.h": strings.Replace(splitMp, "u32 b;    // +0x04", "u32 b;    // +0x08", 1)}), "mailbox_mp.h:8: comment says b"},
		"type from core": {strings.Replace(splitCore, `#include "mailbox_mp.h"`, "", 1), reader(files), `unknown type "Inner"`},
	} {
		if _, err := Generate("mailbox_core.h", []byte(c.src), c.read); err == nil || !strings.Contains(err.Error(), c.err) {
			t.Errorf("%s: err = %v, want %q", name, err, c.err)
		}
	}
}

// FuzzParse: the parser refuses or accepts any header text without
// panicking, includes and all, and whatever it accepts generates Go or
// reports why not.
func FuzzParse(f *testing.F) {
	f.Add(sample, splitMp)
	f.Add(splitCore, splitMp)
	f.Add(splitCore, `#include "mailbox_core.h"`+"\n"+splitMp)
	f.Fuzz(func(t *testing.T, core, mp string) {
		l, err := Parse("mailbox_core.h", []byte(core), reader(map[string]string{"mailbox_mp.h": mp, "mailbox_core.h": core}))
		if err != nil {
			return
		}
		if l.Mailbox() == nil || len(l.Sources) == 0 || l.Sources[0].Name != "mailbox_core.h" {
			t.Fatalf("accepted a layout without a Mailbox or its root: %+v", l)
		}
		_, _ = l.Go()
	})
}

// TestParseDefines: the defines-only reader takes the numeric #defines
// Parse takes, skips string defines and every other line, and refuses a
// name defined twice.
func TestParseDefines(t *testing.T) {
	src := "#ifndef X_H\n#define X_H\n#define A 0x10   // a comment\r\n#define B 7\n#define ARC \"Mo2\"\n#define NEG (-1)\ntypedef struct {\n    u8 a; // +0x00\n} S;\n#endif\n"
	ds, err := ParseDefines("x.h", []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	if len(ds) != 2 || ds[0] != (Define{Name: "A", Value: 0x10, Text: "0x10"}) || ds[1] != (Define{Name: "B", Value: 7, Text: "7"}) {
		t.Errorf("defines %+v", ds)
	}
	if _, err := ParseDefines("x.h", []byte("#define A 1\n#define A 2\n")); err == nil || !strings.Contains(err.Error(), "x.h:2") {
		t.Errorf("a define twice: %v", err)
	}
}
