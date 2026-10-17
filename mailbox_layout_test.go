package main

import (
	"bufio"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/StephenSHorton/ww-multiplayer/wwapi/mailbox"
)

// mailboxLayout computes the Mailbox struct's field offsets from
// inject/include/mailbox.h with PowerPC EABI natural alignment, and the
// struct's size. It knows the scalar types the header uses plus the two
// struct types declared above Mailbox (Puppet 0x20 B, FaceState 0x10 B).
func mailboxLayout(t *testing.T) (map[string]int, int, map[string]int) {
	t.Helper()
	f, err := os.Open("inject/include/mailbox.h")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	defines := map[string]int{}
	defRE := regexp.MustCompile(`^#define\s+(\w+)\s+(0x[0-9A-Fa-f]+|\d+)\s*$`)
	fieldRE := regexp.MustCompile(`^\s*(u8|s8|u16|s16|u32|f32|Puppet|FaceState)\s+(\w+)(?:\[(\w+)\])?;\s*(?://\s*\+(0x[0-9A-Fa-f]+))?`)
	type typ struct{ size, align int }
	types := map[string]typ{
		"u8": {1, 1}, "s8": {1, 1}, "u16": {2, 2}, "s16": {2, 2},
		"u32": {4, 4}, "f32": {4, 4}, "Puppet": {0x20, 4}, "FaceState": {0x10, 2},
	}
	offsets := map[string]int{}
	commented := map[string]int{}
	in := false
	off := 0
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		if m := defRE.FindStringSubmatch(line); m != nil {
			v, _ := strconv.ParseInt(m[2], 0, 64)
			defines[m[1]] = int(v)
			continue
		}
		if strings.HasPrefix(line, "typedef struct {") {
			in, off = true, 0
			offsets = map[string]int{}
			commented = map[string]int{}
			continue
		}
		if !in {
			continue
		}
		if strings.HasPrefix(line, "} Mailbox;") {
			return offsets, off, commented
		}
		if strings.HasPrefix(line, "}") {
			in = false
			continue
		}
		m := fieldRE.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		ty := types[m[1]]
		n := 1
		if m[3] != "" {
			if v, err := strconv.Atoi(m[3]); err == nil {
				n = v
			} else if v, ok := defines[m[3]]; ok {
				n = v
			} else {
				t.Fatalf("mailbox.h: unknown array size %q", m[3])
			}
		}
		off = (off + ty.align - 1) &^ (ty.align - 1)
		offsets[m[2]] = off
		if m[4] != "" {
			v, _ := strconv.ParseInt(m[4], 0, 64)
			commented[m[2]] = int(v)
		}
		off += ty.size * n
	}
	t.Fatal("mailbox.h: no Mailbox struct found")
	return nil, 0, nil
}

// TestMailboxLayout checks the Go mailbox offsets against the C struct,
// and the header's own "+0xNN" comments against the computed layout.
func TestMailboxLayout(t *testing.T) {
	offs, size, commented := mailboxLayout(t)
	for name, c := range commented {
		if offs[name] != c {
			t.Errorf("mailbox.h comment says %s is at +0x%X, the struct puts it at +0x%X", name, c, offs[name])
		}
	}
	for name, want := range map[string]int{
		"face_state":       mailboxFaceStateBase,
		"face_seqs":        mailboxFaceSeqsBase,
		"face_state_local": mailboxFaceStateLocal,
		"input_enable":     mailboxInputEnable,
		"input_overrides":  mailboxInputOverrides,
		"tunic_bufs":       mailboxTunicBufs,
		"tunic_buf_size":   mailboxTunicBufSize,
		"tunic_enable":     mailboxTunicEnable,
		"tunic_state":      mailboxTunicState,
		"tunic_swaps":      mailboxTunicSwaps,
	} {
		got, ok := offs[name]
		if !ok {
			t.Errorf("mailbox.h has no field %s", name)
			continue
		}
		if got != want {
			t.Errorf("%s: C offset +0x%X, Go constant +0x%X", name, got, want)
		}
	}
	if size != mailboxTunicEnd {
		t.Errorf("Mailbox is 0x%X bytes, Go's mailboxTunicEnd is 0x%X", size, mailboxTunicEnd)
	}
	// region-probe's canaries start at mailbox.Mailbox+MailboxSize: a stale
	// size would arm canary words over the mailbox's tail.
	if size != mailbox.MailboxSize || mailbox.Mailbox != mailboxBase {
		t.Errorf("Mailbox is 0x%X bytes at 0x%08X; wwapi/mailbox says 0x%X at 0x%08X", size, uint32(mailboxBase), mailbox.MailboxSize, mailbox.Mailbox)
	}
	if len(commented) < 40 {
		t.Errorf("only %d fields carry a +0xNN comment; the parser is missing fields", len(commented))
	}
	// TUNIC_SLOT_LOCAL indexes tunic_bufs / tunic_enable in C; Go's local
	// slot must be the same, and inside the three-slot arrays.
	src, _ := os.ReadFile("inject/include/mailbox.h")
	m := regexp.MustCompile(`#define\s+TUNIC_SLOT_LOCAL\s+(\d+)`).FindSubmatch(src)
	if m == nil || string(m[1]) != strconv.Itoa(tunicSlotLocal) {
		t.Errorf("TUNIC_SLOT_LOCAL = %q, Go tunicSlotLocal = %d", m, tunicSlotLocal)
	}
	if mailboxTunicBuf(tunicSlots) != mailboxTunicBufSize {
		t.Errorf("tunic_bufs holds %d slots, Go uses %d", (mailboxTunicBufSize-mailboxTunicBufs)/4, tunicSlots)
	}
	// The mailbox (0x80412F00) must end below the docs/08 co-op region
	// at +0x200 and below __OSArenaLo (0x80414000).
	if size > 0x200 {
		t.Errorf("Mailbox is 0x%X bytes, past +0x200", size)
	}
}
