package mailboxgen

import (
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
	l, err := Parse([]byte(sample))
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
	out, err := Generate([]byte(sample))
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
		if _, err := Generate([]byte(src)); err == nil || !strings.Contains(err.Error(), c.err) {
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
