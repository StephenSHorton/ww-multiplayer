package player

import (
	"time"

	"github.com/StephenSHorton/ww-multiplayer/wwapi/mailbox"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mem"
)

// Pad is one synthetic controller state for port 0. The substick,
// triggers and analog A/B are always neutral (0).
type Pad struct {
	Buttons uint16 // PADStatus button bits, e.g. 0x0100 = A, 0x1000 = Start
	StickX  int8   // -128..127, 0 = centred
	StickY  int8
}

// Override makes the game read p as port 0's controller until Release.
// The pad_read_shim (hooked over bl PADRead in JUTGamePad::read at
// 0x802C39A0) checks mailbox.input_enable each frame; when it is set, it
// overrides mPadStatus[0] with the input_* fields before JUTGamePad's
// .update() derivations run, so it works whatever the frame phase, with
// no race against Dolphin's SI poll.
//
// The input_* fields are written first and input_enable last, so the shim
// never sees a half-written state. The substick, triggers and analogs are
// zeroed so an earlier override cannot leak through.
func Override(m mem.Mem, p Pad) error {
	if err := m.Write(mailbox.Mailbox+mailbox.OffInputButtons, []byte{byte(p.Buttons >> 8), byte(p.Buttons & 0xFF)}); err != nil {
		return err
	}
	if err := m.Write(mailbox.Mailbox+mailbox.OffInputStickX, []byte{byte(p.StickX)}); err != nil {
		return err
	}
	if err := m.Write(mailbox.Mailbox+mailbox.OffInputStickY, []byte{byte(p.StickY)}); err != nil {
		return err
	}
	if err := m.Write(mailbox.Mailbox+mailbox.OffInputSubstickX, []byte{0, 0, 0, 0, 0, 0}); err != nil { // input_substick_x..input_analog_b
		return err
	}
	return m.Write(mailbox.Mailbox+mailbox.OffInputEnable, []byte{1})
}

// Release ends an Override: input_enable goes to 0 first, so the shim is
// a pass-through again, and then the input_* fields are zeroed.
func Release(m mem.Mem) error {
	if err := m.Write(mailbox.Mailbox+mailbox.OffInputEnable, []byte{0}); err != nil {
		return err
	}
	return m.Write(mailbox.Mailbox+mailbox.OffInputButtons, []byte{0, 0, 0, 0, 0, 0, 0, 0, 0, 0}) // input_buttons..input_analog_b
}

// Press holds p for hold, then releases it. A hold of 0 leaves the
// override in place, as Override does.
func Press(m mem.Mem, p Pad, hold time.Duration) error {
	if err := Override(m, p); err != nil {
		return err
	}
	if hold > 0 {
		time.Sleep(hold)
		return Release(m)
	}
	return nil
}

// Overrides reads input_overrides, the count of frames the shim has
// overridden so far.
func Overrides(m mem.Reader) (uint32, error) {
	return mem.ReadU32(m, mailbox.Mailbox+mailbox.OffInputOverrides)
}
