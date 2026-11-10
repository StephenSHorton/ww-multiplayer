package widescreen

import (
	"bytes"
	"errors"
	"fmt"
	"math"
	"strings"
	"testing"

	"github.com/StephenSHorton/ww-multiplayer/wwapi/mailbox"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mem"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/ops/opstest"
)

func TestParseSetting(t *testing.T) {
	near := func(a, b float64) bool { return math.Abs(a-b) < 1e-9 }
	for _, c := range []struct {
		in      string
		factor  float64
		auto    bool
		wantErr bool
	}{
		{"16:9", 4.0 / 3, false, false}, {"16:10", 1.2, false, false}, {"21:9", 1.75, false, false},
		{"32:9", 32.0 / 9 / (4.0 / 3), false, false}, {"64:27", 64.0 / 27 * 0.75, false, false},
		{"2.39", 2.39 * 0.75, false, false}, {"4:3", 1, false, false}, {"1.0", 1, false, false},
		{"off", 1, false, false}, {"", 1, false, false}, {"auto", 1, true, false},
		{"100:9", 3, false, false}, // clamped
		{"x", 0, false, true}, {"16:0", 0, false, true}, {"-2", 0, false, true}, {"nan", 0, false, true},
	} {
		s, err := ParseSetting(c.in)
		if (err != nil) != c.wantErr {
			t.Errorf("%q: %v", c.in, err)
			continue
		}
		if err == nil && (!near(s.Factor(), c.factor) || s.Auto != c.auto) {
			t.Errorf("%q: factor %v auto %v, want %v %v", c.in, s.Factor(), s.Auto, c.factor, c.auto)
		}
	}
}

func TestRequestFor(t *testing.T) {
	s, _ := ParseSetting("16:9")
	a, b, c := RequestFor(s, mailbox.WsModeClip, 0).Words()
	if a != 1 || b != math.Float32bits(float32(4.0/3.0)) || c != 0 {
		t.Errorf("16:9 clip: %d %08X %d", a, b, c)
	}
	if r := RequestFor(Setting{Off: true}, mailbox.WsModeNative, 0xFF); r.Mode != 0 || r.Factor != 1 {
		t.Errorf("off: %+v", r)
	}
	if r := RequestFor(Setting{Aspect: 1.3}, mailbox.WsModeClip, 0); r.Mode != 0 {
		t.Errorf("below 4:3 is off: %+v", r)
	}
}

func TestParseAppSetting(t *testing.T) {
	if s, err := ParseAppSetting("21:9"); err != nil || math.Abs(s.Factor()-1.75) > 1e-9 {
		t.Errorf("%+v %v", s, err)
	}
	if _, err := ParseAppSetting("auto"); err == nil {
		t.Error("auto is CLI-only")
	}
	if _, err := ParseAppSetting("2.39"); err == nil {
		t.Error("decimals are CLI-only")
	}
}

// sent is the words of each WS_SET the fake game ran.
type sent [][3]uint32

// rig is a FakeMem with the block as ws_init writes it, the ops runtime
// with WS_SET behaving as widescreen.c's handler, and no session.
func rig(t *testing.T) (*mem.FakeMem, *opstest.C) {
	m, c, _ := rigSent(t)
	return m, c
}

// rigSent is rig plus the WS_SET words the game got.
func rigSent(t *testing.T) (*mem.FakeMem, *opstest.C, *sent) {
	t.Helper()
	m := mem.NewFakeMem()
	c := opstest.New(m)
	c.Boot(nil)
	blk := make([]byte, mailbox.WsSize)
	put := func(o int, v uint32) {
		blk[o], blk[o+1], blk[o+2], blk[o+3] = byte(v>>24), byte(v>>16), byte(v>>8), byte(v)
	}
	put(mailbox.WsMagicOff, mailbox.WsMagic)
	blk[mailbox.WsVersionOff], blk[mailbox.WsModeOff] = 1, mailbox.WsModeClip
	put(mailbox.WsFactorOff, mailbox.WsFactorDefault)
	if err := m.Store(mailbox.WsAddr, blk); err != nil {
		t.Fatal(err)
	}
	got := &sent{}
	c.Register(mailbox.OpsOpWsSet, func(a, b, cc uint32) uint8 {
		*got = append(*got, [3]uint32{a, b, cc})
		f := math.Float32frombits(b)
		if a > 2 || !mailbox.WsFactorValid(f) {
			return mailbox.OpsRRefused
		}
		_ = m.Store(mailbox.WsAddr+mailbox.WsModeOff, []byte{byte(a)})
		_ = m.Store(mailbox.WsAddr+mailbox.WsFactorOff, []byte{byte(b >> 24), byte(b >> 16), byte(b >> 8), byte(b)})
		_ = m.Store(mailbox.WsAddr+mailbox.WsFlagsOff, []byte{byte(cc & mailbox.WsFlagsSupported)})
		return mailbox.OpsROK
	})
	old := checkContract
	checkContract = func(mem.Reader) error { return nil }
	t.Cleanup(func() { checkContract = old })
	return m, c, got
}

// drive runs frames of the fake C until the command's goroutine is done.
func drive(t *testing.T, m *mem.FakeMem, c *opstest.C, args []string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	done := make(chan error, 1)
	go func() {
		done <- apply(m, 1, args, &out, func(uint32) (float64, string, error) { return 1.5, "Widescreen Hack, test", nil })
	}()
	for {
		select {
		case err := <-done:
			return out.String(), err
		default:
			c.Frame()
		}
	}
}

func TestApply(t *testing.T) {
	m, c := rig(t)
	out, err := drive(t, m, c, nil)
	if err != nil || !strings.Contains(out, "mode 1 factor 1.3333") {
		t.Fatalf("status: %q %v", out, err)
	}
	out, err = drive(t, m, c, []string{"21:9"})
	if err != nil || !strings.Contains(out, "mode 1 factor 1.7500") {
		t.Fatalf("21:9: %q %v", out, err)
	}
	if out, err = drive(t, m, c, []string{"off"}); err != nil || !strings.Contains(out, "mode 0 factor 1.0000") {
		t.Fatalf("off: %q %v", out, err)
	}
	if out, err = drive(t, m, c, []string{"auto"}); err != nil || !strings.Contains(out, "mode 1 factor 1.5000") || !strings.Contains(out, "auto: Widescreen Hack, test") {
		t.Fatalf("auto: %q %v", out, err)
	}
	if out, err = drive(t, m, c, []string{"native", "16:9", "--flags", "3"}); err != nil || !strings.Contains(out, fmt.Sprintf("mode 2 factor 1.3333 flags 0x%02X", 3&mailbox.WsFlagsSupported)) {
		t.Fatalf("native: %q %v", out, err)
	}
	if _, err = drive(t, m, c, []string{"native", "off"}); err == nil {
		t.Error("native off must be refused")
	}
	if _, err = drive(t, m, c, []string{"bogus"}); err == nil {
		t.Error("bad setting must fail")
	}
	if _, err = drive(t, m, c, []string{"--flags", "999", "16:9"}); err == nil {
		t.Error("flags above a byte must fail")
	}
}

func TestApplyRefusals(t *testing.T) {
	m, c := rig(t)
	// A session holds the mailbox.
	_ = m.Store(mailbox.Mailbox+mailbox.OffShadowMode, []byte{5})
	if _, err := drive(t, m, c, []string{"16:9"}); !errors.Is(err, ErrClaimed) {
		t.Errorf("claimed: %v", err)
	}
	// ... but reading is fine.
	if _, err := drive(t, m, c, nil); err != nil {
		t.Errorf("status while claimed: %v", err)
	}
	_ = m.Store(mailbox.Mailbox+mailbox.OffShadowMode, []byte{0})
	// The contract fails.
	checkContract = func(mem.Reader) error { return errors.New("contract: other blob") }
	if _, err := drive(t, m, c, []string{"16:9"}); err == nil || !strings.Contains(err.Error(), "other blob") {
		t.Errorf("contract: %v", err)
	}
	checkContract = func(mem.Reader) error { return nil }
	// An old blob has no block.
	_ = m.Store(mailbox.WsAddr, make([]byte, mailbox.WsSize))
	if _, err := drive(t, m, c, []string{"16:9"}); err == nil || !strings.Contains(err.Error(), "no widescreen block") {
		t.Errorf("no block: %v", err)
	}
}

func TestApplyNative(t *testing.T) {
	m, c, got := rigSent(t)
	for _, tc := range []struct {
		args []string
		want [3]uint32
	}{
		{[]string{"native", "16:9"}, [3]uint32{mailbox.WsModeNative, 0x3FAAAAAB, 0xFF}},
		{[]string{"native", "16:9", "--flags", "0"}, [3]uint32{mailbox.WsModeNative, 0x3FAAAAAB, 0}},
		{[]string{"--flags", "0", "native", "16:9"}, [3]uint32{mailbox.WsModeNative, 0x3FAAAAAB, 0}},
		{[]string{"16:9"}, [3]uint32{mailbox.WsModeClip, 0x3FAAAAAB, 0xFF}},
	} {
		*got = nil
		if _, err := drive(t, m, c, tc.args); err != nil {
			t.Fatalf("%v: %v", tc.args, err)
		}
		if len(*got) != 1 || (*got)[0] != tc.want {
			t.Errorf("%v: sent %08X, want %08X", tc.args, *got, tc.want)
		}
	}
	*got = nil
	for _, args := range [][]string{
		{"native"}, {"native", "x"}, {"native", "16:0"}, {"native", "auto"},
		{"native", "16:9", "21:9"}, {"native", "16:9", "--flags", "256"},
	} {
		if _, err := drive(t, m, c, args); err == nil {
			t.Errorf("%v must fail", args)
		}
	}
	if len(*got) != 0 {
		t.Errorf("bad forms sent %08X", *got)
	}
}

// Every form that writes refuses while a session holds the mailbox, and
// when the contract check fails.
func TestApplyRefusesEveryForm(t *testing.T) {
	m, c, got := rigSent(t)
	forms := [][]string{{"auto"}, {"off"}, {"16:9"}, {"2.39"}, {"native", "16:9"}, {"native", "21:9", "--flags", "0"}}
	_ = m.Store(mailbox.Mailbox+mailbox.OffShadowMode, []byte{1})
	for _, args := range forms {
		if _, err := drive(t, m, c, args); !errors.Is(err, ErrClaimed) {
			t.Errorf("%v while claimed: %v", args, err)
		}
	}
	_ = m.Store(mailbox.Mailbox+mailbox.OffShadowMode, []byte{0})
	checkContract = func(mem.Reader) error { return errors.New("contract: other blob") }
	for _, args := range forms {
		if _, err := drive(t, m, c, args); err == nil || !strings.Contains(err.Error(), "other blob") {
			t.Errorf("%v, contract: %v", args, err)
		}
	}
	if len(*got) != 0 {
		t.Errorf("refused forms sent %08X", *got)
	}
}
