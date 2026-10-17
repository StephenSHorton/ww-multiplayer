package story

import (
	"slices"
	"testing"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/internal/coop/class"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/world"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
)

// Bits and bytes the tests use (d_save_event_flag.inc).
const (
	evMetKorl       = 0x0F80 // MET_KORL, :111
	ev0520          = 0x0520 // UNK_0520, read by getLayerNo (d_com_inf_game.cpp:194)
	ev1820          = 0x1820 // UNK_1820, read by getLayerNo
	evZeldaAwakened = 0x2D02 // ZELDA_AWAKENED, :345
	evRodeKorl      = 0x2A08 // RODE_KORL: LocalOnly
	ev3804          = 0x3804 // HYRULE_COURTYARD_CUTSCENE
	ev2D10          = 0x2D10
	ev0801          = 0x0801
	ev0808          = 0x0808
	ev2401          = 0x2401
	ev3E10          = 0x3E10
	ev3F80          = 0x3F80

	regLetter = 0x7A // LETTER_ROCK_SPIRE_SHOP_AD 0x7A03: MAX, field 0x03
	regBeedle = 0x86 // Beedle points 0x86FF: MAX, field 0xFF
	regFigure = 0x80 // figurine bitfield 0x80FF: OR
	regLocal  = 0x79 // a register outside the allowlist: per player
)

// ev returns event bit id as a gain.
func ev(id uint16) world.Gain {
	b := addr.EventByte(id)
	return world.Gain{Off: uint16(addr.OffEvent + b.Off), Val: b.Mask}
}

// reg returns event register byte i with value v as a gain.
func reg(i int, v byte) world.Gain { return world.Gain{Off: uint16(addr.OffEvent + i), Val: v} }

// sw returns memory switch no of save table t as a gain (onSwitch,
// d_save.cpp:1083-1086: switch no is bit no%32 of big-endian word no/32).
func sw(t, no int) world.Gain {
	off := addr.OffMemBit(t) + addr.MemBitOffSwitch + 4*(no/32) + 3 - (no%32)/8
	return world.Gain{Off: uint16(off), Val: 1 << (no % 8)}
}

// orByte is the first OR byte of the image outside the story bytes,
// registers and switches: something the Batcher passes straight on.
var orByte = func() uint16 {
	for off := range world.ImageSize {
		if class.Of(off).Class == class.OR && !IsStoryByte(off) && !IsRegisterByte(off) && !isSwitchByte(off) {
			return uint16(off)
		}
	}
	panic("no plain OR byte")
}()

// TestTestBits: the bits these tests sync or don't, as they assume.
func TestTestBits(t *testing.T) {
	for _, id := range []uint16{evMetKorl, ev0520, ev1820, evZeldaAwakened, ev3804, ev2D10,
		ev0801, ev0808, ev2401, ev3E10, ev3F80} {
		if !class.EventSyncs(id) {
			t.Errorf("0x%04X does not sync", id)
		}
	}
	if class.EventSyncs(evRodeKorl) {
		t.Error("RODE_KORL syncs")
	}
	for i, c := range map[int]class.Class{regLetter: class.Max, regBeedle: class.Max, regFigure: class.OR, regLocal: class.PerPlayer} {
		if got := class.Of(addr.OffEvent + i).Class; got != c {
			t.Errorf("register 0x%02X is %s, want %s", i, got, c)
		}
	}
	for tb := range addr.TableCount {
		first := addr.OffMemBit(tb) + addr.MemBitOffSwitch
		if isSwitchByte(first-1) || !isSwitchByte(first) || !isSwitchByte(first+15) || isSwitchByte(first+16) {
			t.Errorf("table %d: the switch bytes are not 0x%X-0x%X", tb, first, first+15)
		}
		if g := sw(tb, 24); int(g.Off) != first || g.Val != 0x01 {
			t.Errorf("table %d: switch 24 at 0x%X/0x%02X", tb, g.Off, g.Val)
		}
		if g := sw(tb, 103); int(g.Off) != first+15 || g.Val != 0x80 {
			t.Errorf("table %d: switch 103 at 0x%X/0x%02X", tb, g.Off, g.Val)
		}
	}
}

// tick is one Batcher.Tick and what it should send.
type tick struct {
	ms    int // since the start
	idle  bool
	gains []world.Gain
	want  []world.Gain
}

func runTicks(t *testing.T, b *Batcher, ticks []tick) {
	t.Helper()
	t0 := time.Unix(1_700_000_000, 0)
	for _, k := range ticks {
		got := b.Tick(t0.Add(time.Duration(k.ms)*time.Millisecond), k.idle, k.gains)
		if !slices.Equal(got, k.want) {
			t.Errorf("tick at %d ms (idle %v): sent %v, want %v", k.ms, k.idle, got, k.want)
		}
	}
}

// merged is gs merged into one image and listed again, ascending.
func merged(gs ...world.Gain) []world.Gain {
	var img world.Image
	world.Apply(&img, gs)
	return gainsOf(&img)
}

// TestBatchTiming: story bits wait until the game has been IDLE for
// BatchIdle and the batch has not grown for as long, then go together;
// what is not story goes at once.
func TestBatchTiming(t *testing.T) {
	a, z, l := ev(ev0520), ev(evZeldaAwakened), ev(ev1820)
	item := world.Gain{Off: orByte, Val: 0x01}
	cases := []struct {
		name  string
		ticks []tick
	}{
		{"a bit set while idle waits one second", []tick{
			{0, true, []world.Gain{a}, nil},
			{900, true, nil, nil},
			{1000, true, nil, []world.Gain{a}},
			{1100, true, nil, nil},
		}},
		{"a cutscene's bits go together a second after it ends", []tick{
			{0, false, []world.Gain{a}, nil},
			{300, false, []world.Gain{z}, nil},
			{500, true, nil, nil},
			{1400, true, nil, nil},
			{1500, true, nil, merged(a, z)},
		}},
		{"a busy blip starts the second again", []tick{
			{0, true, []world.Gain{a}, nil},
			{600, false, nil, nil},
			{700, true, nil, nil},
			{1600, true, nil, nil},
			{1700, true, nil, []world.Gain{a}},
		}},
		{"a new bit postpones the batch", []tick{
			{0, true, []world.Gain{a}, nil},
			{800, true, []world.Gain{l}, nil},
			{1000, true, nil, nil},
			{1700, true, nil, nil},
			{1800, true, nil, merged(a, l)},
		}},
		{"a bit already waiting does not postpone it", []tick{
			{0, true, []world.Gain{a}, nil},
			{800, true, []world.Gain{a}, nil},
			{1000, true, nil, []world.Gain{a}},
		}},
		{"a LocalOnly bit is never sent", []tick{
			{0, true, []world.Gain{ev(evRodeKorl)}, nil},
			{2000, true, nil, nil},
		}},
		{"only a byte's SyncMask bits are sent", []tick{
			{0, true, []world.Gain{{Off: uint16(addr.OffEvent + 0x2A), Val: 0xFF}}, nil},
			{1000, true, nil, []world.Gain{{Off: uint16(addr.OffEvent + 0x2A), Val: 0xFF &^ 0x08 & class.SyncMaskOf(0x2A)}}},
		}},
		{"a plain OR byte goes at once, even in an event", []tick{
			{0, false, []world.Gain{item, a}, []world.Gain{item}},
			{100, true, nil, nil},
			{1100, true, nil, []world.Gain{a}},
		}},
		{"a switch rising while idle with no batch goes at once", []tick{
			{0, true, []world.Gain{sw(addr.TableSea, 5)}, []world.Gain{sw(addr.TableSea, 5)}},
		}},
		{"a switch rising in an event waits for that event's bits", []tick{
			{0, false, []world.Gain{sw(addr.TableSea, 5)}, nil},
			{2000, false, []world.Gain{z}, nil},
			{2100, true, nil, nil},
			{3100, true, nil, merged(z, sw(addr.TableSea, 5))},
		}},
		{"a switch rising with a story bit waits with it", []tick{
			{0, true, []world.Gain{sw(addr.TableSea, 5), a}, nil},
			{1000, true, nil, merged(a, sw(addr.TableSea, 5))},
		}},
		{"a switch rising while a batch waits waits too, without postponing it", []tick{
			{0, true, []world.Gain{a}, nil},
			{900, true, []world.Gain{sw(addr.TableSea, 6)}, nil},
			{1000, true, nil, merged(a, sw(addr.TableSea, 6))},
		}},
		{"an event that sets only a switch releases it a second after it ends", []tick{
			{0, false, []world.Gain{sw(addr.TableSea, 5)}, nil},
			{500, true, nil, nil},
			{1500, true, nil, []world.Gain{sw(addr.TableSea, 5)}},
		}},
		{"a MAX register waits, and goes at its highest value", []tick{
			{0, true, []world.Gain{reg(regLetter, 1)}, nil},
			{100, true, []world.Gain{reg(regLetter, 2), reg(regFigure, 0x10)}, nil},
			{1100, true, nil, []world.Gain{reg(regLetter, 2), reg(regFigure, 0x10)}},
		}},
		{"a byte that does not merge is dropped", []tick{
			{0, true, []world.Gain{reg(regLocal, 1)}, nil},
			{1000, true, nil, nil},
		}},
		{"a bit rising on the tick the batch is due joins it", []tick{
			{0, true, []world.Gain{a}, nil},
			{1000, true, []world.Gain{z}, nil},
			{2000, true, nil, merged(a, z)},
		}},
		{"what rises after a release starts a new batch", []tick{
			{0, true, []world.Gain{a}, nil},
			{1000, true, nil, []world.Gain{a}},
			{1100, true, []world.Gain{z}, nil},
			{2000, true, nil, nil},
			{2100, true, nil, []world.Gain{z}},
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var b Batcher
			runTicks(t, &b, c.ticks)
		})
	}
}

// TestBatchWaiting: Waiting lists the batch until it goes.
func TestBatchWaiting(t *testing.T) {
	var b Batcher
	t0 := time.Unix(0, 0)
	if b.Waiting() != nil {
		t.Fatal("a zero Batcher waits")
	}
	b.Tick(t0, false, []world.Gain{ev(ev1820), sw(addr.TableSea, 1), ev(ev0520)})
	want := merged(ev(ev0520), ev(ev1820), sw(addr.TableSea, 1))
	if got := b.Waiting(); !slices.Equal(got, want) {
		t.Errorf("Waiting %v, want %v", got, want)
	}
	if got := b.Tick(t0.Add(BatchIdle), true, nil); got != nil {
		t.Errorf("sent %v on the first IDLE tick", got)
	}
	if got := b.Tick(t0.Add(2*BatchIdle), true, nil); !slices.Equal(got, want) {
		t.Errorf("sent %v, want %v", got, want)
	}
	if b.Waiting() != nil {
		t.Error("Waiting after the release")
	}
}

// TestBatchWithhold: a whole image sent while a batch waits loses the
// batch's bits, a MAX register's whole field, and its coupled switches;
// nothing else.
func TestBatchWithhold(t *testing.T) {
	var b Batcher
	b.Tick(time.Unix(0, 0), false, []world.Gain{ev(ev0520), reg(regLetter, 2), sw(addr.TableSea, 3)})

	var img world.Image
	for _, g := range []world.Gain{ev(ev0520), ev(ev1820), reg(regLetter, 1), reg(regFigure, 0x81),
		sw(addr.TableSea, 3), sw(addr.TableSea, 4), {Off: orByte, Val: 0xFF}} {
		img[g.Off] |= g.Val
	}
	img[addr.OffEvent+regLetter] |= 0xF0 // bits outside the field are not the world's anyway
	want := img
	want[ev(ev0520).Off] &^= ev(ev0520).Val
	want[addr.OffEvent+regLetter] &^= 0x03
	want[sw(addr.TableSea, 3).Off] &^= sw(addr.TableSea, 3).Val

	got := img
	b.Withhold(&got)
	if got != want {
		for off := range got {
			if got[off] != want[off] {
				t.Errorf("byte 0x%X: 0x%02X, want 0x%02X", off, got[off], want[off])
			}
		}
	}

	var idle Batcher
	got = img
	idle.Withhold(&got)
	if got != img {
		t.Error("Withhold with no batch changed the image")
	}
}

// TestBatchReset: Reset drops the batch, not the IDLE run.
func TestBatchReset(t *testing.T) {
	var b Batcher
	runTicks(t, &b, []tick{
		{0, true, []world.Gain{ev(ev0520), sw(addr.TableSea, 1)}, nil},
	})
	b.Reset()
	if b.Waiting() != nil {
		t.Fatal("Waiting after Reset")
	}
	runTicks(t, &b, []tick{
		{1000, true, nil, nil},
		// The IDLE run began at 0: a bit rising now waits for its own
		// quiet second, and a switch with no batch goes at once.
		{1100, true, []world.Gain{sw(addr.TableSea, 2)}, []world.Gain{sw(addr.TableSea, 2)}},
		{1200, true, []world.Gain{ev(ev1820)}, nil},
		{2200, true, nil, []world.Gain{ev(ev1820)}},
	})
}
