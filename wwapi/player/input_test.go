package player

import (
	"bytes"
	"testing"

	"github.com/StephenSHorton/ww-multiplayer/wwapi/mailbox"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mem"
)

// TestOverrideOrder: the input_* fields go first and input_enable last,
// and Release clears input_enable before it zeroes the fields.
func TestOverrideOrder(t *testing.T) {
	m := mem.NewFakeMem()
	store(t, m, mailbox.Mailbox+mailbox.OffInputSubstickX, []byte{9, 9, 9, 9, 9, 9})
	m.Record(true)
	if err := Press(m, Pad{Buttons: 0x0108, StickX: -128, StickY: 127}, 0); err != nil {
		t.Fatal(err)
	}
	mb := mailbox.Mailbox
	want := []mem.WriteRecord{
		{Addr: mb + mailbox.OffInputButtons, Data: []byte{0x01, 0x08}},
		{Addr: mb + mailbox.OffInputStickX, Data: []byte{0x80}},
		{Addr: mb + mailbox.OffInputStickY, Data: []byte{0x7F}},
		{Addr: mb + mailbox.OffInputSubstickX, Data: make([]byte, 6)},
		{Addr: mb + mailbox.OffInputEnable, Data: []byte{1}},
	}
	checkWrites(t, m.Writes(), want)

	m.ResetWrites()
	if err := Release(m); err != nil {
		t.Fatal(err)
	}
	checkWrites(t, m.Writes(), []mem.WriteRecord{
		{Addr: mb + mailbox.OffInputEnable, Data: []byte{0}},
		{Addr: mb + mailbox.OffInputButtons, Data: make([]byte, 10)},
	})
}

// TestPressReleases: a Press with a hold ends released.
func TestPressReleases(t *testing.T) {
	m := mem.NewFakeMem()
	if err := Press(m, Pad{Buttons: 0x0100}, 1); err != nil {
		t.Fatal(err)
	}
	b, err := m.Read(mailbox.Mailbox+mailbox.OffInputEnable, 12)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(b, make([]byte, 12)) {
		t.Errorf("input_enable..input_analog_b = % X after Press, want zeros", b)
	}
	store(t, m, mailbox.Mailbox+mailbox.OffInputOverrides, u32b(17))
	if n, err := Overrides(m); err != nil || n != 17 {
		t.Errorf("Overrides = %d, %v; want 17", n, err)
	}
}

func checkWrites(t *testing.T, got, want []mem.WriteRecord) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("writes %+v, want %+v", got, want)
	}
	for i := range got {
		if got[i].Addr != want[i].Addr || !bytes.Equal(got[i].Data, want[i].Data) {
			t.Errorf("write %d = %#x % X, want %#x % X", i, got[i].Addr, got[i].Data, want[i].Addr, want[i].Data)
		}
	}
}
