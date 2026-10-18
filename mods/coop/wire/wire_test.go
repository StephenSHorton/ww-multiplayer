package wire

import (
	"bytes"
	"encoding/hex"
	"errors"
	"math/rand"
	"reflect"
	"strings"
	"testing"

	"github.com/StephenSHorton/ww-multiplayer/mods/coop/class"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/save"
)

// unhex decodes a hex string, ignoring spaces and '|' separators.
func unhex(t testing.TB, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(strings.NewReplacer(" ", "", "|", "").Replace(s))
	if err != nil {
		t.Fatalf("bad hex %q: %v", s, err)
	}
	return b
}

func mustStage(t testing.TB, name string) Stage {
	t.Helper()
	s, err := MakeStage(name)
	if err != nil {
		t.Fatalf("MakeStage(%q): %v", name, err)
	}
	return s
}

var (
	idA = ID{0xA0, 0xA1, 0xA2, 0xA3, 0xA4, 0xA5, 0xA6, 0xA7}
	idB = ID{0xB0, 0xB1, 0xB2, 0xB3, 0xB4, 0xB5, 0xB6, 0xB7}
)

// Image offsets the tests use, one per class (mods/coop/class).
var (
	offGetItem   = uint16(addr.OffGetItem + 3)                                      // OR, mask 0xFF (mGetItem[3]; [0..2] sync bit 0 only)
	offBottles   = uint16(addr.OffGetItem)                                          // OR, mask 0x01 (bits 1-7 are bottle history)
	offWallet    = uint16(addr.OffWalletSize)                                       // MAX, mask 0xFF
	offSwitch3   = uint16(addr.OffMemBit(addr.TableDRC) + addr.MemBitOffSwitch)     // OR_MASKED
	offDungeon4  = uint16(addr.OffMemBit(addr.TableFW) + addr.MemBitOffDungeonItem) // OR, mask 0x3F
	offLetterReg = uint16(addr.OffEvent + 0x7A)                                     // MAX, mask 0x03 (LETTER_ROCK_SPIRE_SHOP_AD)
	offLife      = uint16(addr.OffLife)                                             // PER_PLAYER
	offMaxLife   = uint16(addr.OffMaxLife)                                          // DERIVED
	offReserve   = uint16(addr.OffReserve)                                          // TAG
	offImagePad  = uint16(addr.OffImagePad)                                         // NEVER
)

// TestClassesOfTestOffsets pins the classes the tests below rely on.
func TestClassesOfTestOffsets(t *testing.T) {
	for _, c := range []struct {
		off   uint16
		class class.Class
		mask  byte
	}{
		{offGetItem, class.OR, 0xFF}, {offBottles, class.OR, 0x01}, {offWallet, class.Max, 0xFF}, {offSwitch3, class.ORMasked, 0xFF},
		{offDungeon4, class.OR, addr.DungeonItemMask}, {offLetterReg, class.Max, 0x03},
		{offLife, class.PerPlayer, 0}, {offMaxLife, class.Derived, 0}, {offReserve, class.Tag, 0}, {offImagePad, class.Never, 0},
	} {
		if r := class.Of(int(c.off)); r.Class != c.class || r.Mask != c.mask {
			t.Errorf("offset 0x%03X is %s mask 0x%02X, want %s mask 0x%02X", c.off, r.Class, r.Mask, c.class, c.mask)
		}
	}
}

// goldenWorld is the image TestGolden's WORLD carries.
func goldenWorld() *World {
	w := &World{WorldID: idA, Seq: 7}
	w.Image[offGetItem] = 0x81
	w.Image[offWallet] = 2
	w.Image[offMaxLife+1] = 12
	return w
}

// golden is one message per sub-message (and per direction for GAINS),
// with its exact payload.
var golden = []struct {
	name string
	dir  Dir
	msg  Msg
	hex  string
}{
	{"CO_HELLO", Up, &CoHello{
		Schema: 1, ClassHash: 0x0102030405060708, StageTblHash: 0x1112131415161718,
		WorldID: idA, BindNonce: idB, Slot: 2, ClearCount: 0, NameHash: 0xDEADBEEF,
		FileState: FileBound, Want: 0xF,
	}, "01 | 0001 | 0102030405060708 | 1112131415161718 | a0a1a2a3a4a5a6a7 | b0b1b2b3b4b5b6b7 | 02 | 00 | deadbeef | 01 | 0000000f"},
	{"CO_WELCOME", Down, &CoWelcome{
		Status: StatusOK, Features: 0xF, WorldID: idA, Seq: 0x102, FreshBind: true,
	}, "02 | 01 | 00 | 0000000f | a0a1a2a3a4a5a6a7 | 0000000000000102 | 01"},
	{"CO_WELCOME refused", Down, &CoWelcome{
		Status: StatusRefused, Reason: NoticeClearCount, WorldID: idA,
	}, "02 | 03 | 04 | 00000000 | a0a1a2a3a4a5a6a7 | 0000000000000000 | 00"},
	{"GAINS c->s", Up, &GainsUp{
		Batch: 0x01020304, Gains: []Gain{{offWallet, 0x02}, {offGetItem, 0x81}, {offSwitch3, 0x10}},
	}, "04 | 01020304 | 0003 | 0012 02 | 0054 81 | 03f0 10"},
	{"GAINS c->s empty", Up, &GainsUp{Batch: 9}, "04 | 00000009 | 0000"},
	{"GAINS s->c", Down, &GainsDown{
		Seq: 0xA, Origin: 2, Gains: []Gain{{offDungeon4, 0x05}},
	}, "04 | 000000000000000a | 02 | 0001 | 0431 05"},
	{"PRESENCE", Up, &Presence{
		Stage: Stage{'M', '_', 'N', 'e', 'w', 'D', '2'}, Table: 3, Room: 5, Layer: -1, Point: -2,
		Flags: PresenceInGame | PresenceArmed,
	}, "05 | 4d5f4e6577443200 | 03 | 05 | ff | fffe | 11"},
	{"PRESENCE relayed, title screen", Down, &Presence{
		Stage: Stage{'s', 'e', 'a', '_', 'T'}, Table: -1, Room: -1, Layer: -1,
	}, "05 | 7365615f54000000 | ff | ff | ff | 0000 | 00"},
	{"SCOPED KILL", Up, &Scoped{
		Kind: ScopeKill, Stage: Stage{'M', '_', 'N', 'e', 'w', 'D', '2'}, Table: 3, Room: 5,
		Words: []ScopedWord{{0, 0x80000001}, {15, 0x100}},
	}, "06 | 04 | 4d5f4e6577443200 | 03 | 05 | 02 | 00 80000001 | 0f 00000100"},
	{"SCOPED DAN", Down, &Scoped{
		Kind: ScopeDan, Stage: Stage{'M', '_', 'N', 'e', 'w', 'D', '2'}, Table: 3, Room: -1,
		Words: []ScopedWord{{1, 0x4}},
	}, "06 | 01 | 4d5f4e6577443200 | 03 | ff | 01 | 01 00000004"},
	{"SCOPED ROOMEDGE", Up, &Scoped{
		Kind: ScopeRoomEdge, Stage: Stage{'s', 'e', 'a'}, Table: 0, Room: 44,
		Words: []ScopedWord{{2, 0x8000}},
	}, "06 | 03 | 7365610000000000 | 00 | 2c | 01 | 02 00008000"},
	{"NOTICE", Down, &Notice{Code: NoticeWorldMismatch, Text: "wrong world"},
		"07 | 01 | 0b | 77726f6e6720776f726c64"},
	{"NOTICE empty", Down, &Notice{Code: NoticeRejected}, "07 | 06 | 00"},
	{"CTRL TOMBSTONE", Up, &Ctrl{Op: CtrlTombstone, Arg: TombstoneArg(offGetItem, 0x80)}, "08 | 03 | 00005480"},
	{"CTRL RESYNC", Down, &Ctrl{Op: CtrlResync}, "08 | 01 | 00000000"},
	{"CTRL COOP_OFF", Down, &Ctrl{Op: CtrlCoopOff, Arg: 0xFFFFFFFF}, "08 | 02 | ffffffff"},
}

// TestGolden: each sub-message encodes to exactly its documented bytes
// and decodes back to itself.
func TestGolden(t *testing.T) {
	for _, g := range golden {
		want := unhex(t, g.hex)
		got, err := Encode(g.msg)
		if err != nil {
			t.Errorf("%s: Encode: %v", g.name, err)
			continue
		}
		if !bytes.Equal(got, want) {
			t.Errorf("%s: Encode = %x, want %x", g.name, got, want)
		}
		back, err := Decode(want, g.dir)
		if err != nil {
			t.Errorf("%s: Decode: %v", g.name, err)
			continue
		}
		if !reflect.DeepEqual(back, g.msg) {
			t.Errorf("%s: Decode = %+v, want %+v", g.name, back, g.msg)
		}
	}
}

// TestGoldenWorld: WORLD's header is worldID, seq and the 0x778 length,
// then the image verbatim.
func TestGoldenWorld(t *testing.T) {
	w := goldenWorld()
	got, err := Encode(w)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	header := unhex(t, "03 | a0a1a2a3a4a5a6a7 | 0000000000000007 | 0778")
	if !bytes.HasPrefix(got, header) || len(got) != len(header)+addr.SaveImageSize {
		t.Fatalf("WORLD = %x... (%d bytes), want header %x and %d bytes", got[:min(len(got), 20)], len(got), header, len(header)+addr.SaveImageSize)
	}
	img := got[len(header):]
	if img[offGetItem] != 0x81 || img[offWallet] != 2 || img[offMaxLife+1] != 12 {
		t.Errorf("image bytes not at their offsets")
	}
	back, err := Decode(got, Down)
	if err != nil || !reflect.DeepEqual(back, w) {
		t.Fatalf("Decode = %v, %v", back, err)
	}
}

// TestBodySizes pins the body sizes of §4.4.
func TestBodySizes(t *testing.T) {
	gains := []Gain{{offWallet, 1}, {offGetItem, 1}}
	words := []ScopedWord{{0, 1}, {1, 1}}
	for _, c := range []struct {
		msg  Msg
		body int
	}{
		{&CoHello{Schema: Schema}, 45},
		{&CoWelcome{Status: StatusOK}, 23},
		{&World{}, 18 + 0x778},
		{&GainsUp{Gains: gains}, 6 + 3*len(gains)},
		{&GainsDown{Gains: gains}, 11 + 3*len(gains)},
		{&Presence{Table: -1, Room: -1, Layer: -1}, 14},
		{&Scoped{Kind: ScopeDan, Stage: Stage{'s'}, Room: -1, Words: words}, 12 + 5*len(words)},
		{&Notice{Code: NoticeSchema, Text: "abc"}, 2 + 3},
		{&Ctrl{Op: CtrlResync}, 5},
	} {
		b, err := Encode(c.msg)
		if err != nil {
			t.Errorf("%s: %v", c.msg.Sub(), err)
			continue
		}
		if len(b)-1 != c.body {
			t.Errorf("%s body is %d bytes, want %d", c.msg.Sub(), len(b)-1, c.body)
		}
	}
}

// mergeOffsets is every image offset whose class merges.
var mergeOffsets = func() (offs []uint16) {
	for off := range addr.SaveImageSize {
		if class.Of(off).Class.Merges() {
			offs = append(offs, uint16(off))
		}
	}
	return offs
}()

// randGains returns up to n valid gains in ascending order.
func randGains(rng *rand.Rand, n int) []Gain {
	var gs []Gain
	for _, off := range mergeOffsets {
		if len(gs) == n {
			break
		}
		if rng.Intn(len(mergeOffsets)/max(n, 1)+1) != 0 {
			continue
		}
		gs = append(gs, Gain{Off: off, Val: byte(rng.Intn(256)) & class.Of(int(off)).Mask})
	}
	return gs
}

func randID(rng *rand.Rand) (id ID) {
	rng.Read(id[:])
	return id
}

var stageNames = []string{"", "sea", "sea_T", "Name", "M_NewD2", "kindan", "GanonK", "Xboss0"}

// randMsg returns a random valid message that travels in direction dir.
func randMsg(rng *rand.Rand, dir Dir) Msg {
	stage := func() Stage { s, _ := MakeStage(stageNames[rng.Intn(len(stageNames))]); return s }
	nonEmptyStage := func() Stage { s, _ := MakeStage(stageNames[1+rng.Intn(len(stageNames)-1)]); return s }
	room := func() int8 { return int8(rng.Intn(addr.RoomCount)) }
	table := func() int8 { return int8(rng.Intn(addr.TableCount)) }
	for {
		switch Sub(1 + rng.Intn(8)) {
		case SubCoHello:
			if dir != Up {
				continue
			}
			return &CoHello{Schema: Schema, ClassHash: rng.Uint64(), StageTblHash: rng.Uint64(), WorldID: randID(rng),
				BindNonce: randID(rng), Slot: uint8(rng.Intn(CardSlots)), ClearCount: uint8(rng.Intn(256)),
				NameHash: rng.Uint32(), FileState: FileState(rng.Intn(4)), Want: rng.Uint32()}
		case SubCoWelcome:
			if dir != Down {
				continue
			}
			m := &CoWelcome{Status: Status(1 + rng.Intn(3)), WorldID: randID(rng), Seq: rng.Uint64(), FreshBind: rng.Intn(2) == 1}
			if m.Status == StatusRefused {
				m.Reason = NoticeCode(1 + rng.Intn(noticeCodes))
			} else {
				m.Reason = NoticeCode(rng.Intn(noticeCodes + 1))
				m.Features = rng.Uint32()
			}
			return m
		case SubWorld:
			w := &World{WorldID: randID(rng), Seq: rng.Uint64()}
			rng.Read(w.Image[:])
			StripLocal(&w.Image)
			return w
		case SubGains:
			if dir == Up {
				return &GainsUp{Batch: rng.Uint32(), Gains: randGains(rng, rng.Intn(40))}
			}
			return &GainsDown{Seq: rng.Uint64(), Origin: uint8(rng.Intn(256)), Gains: randGains(rng, rng.Intn(40))}
		case SubPresence:
			return &Presence{Stage: stage(), Table: int8(rng.Intn(addr.TableCount+1)) - 1, Room: int8(rng.Intn(addr.RoomCount+1)) - 1,
				Layer: int8(rng.Intn(MaxLayer+2)) - 1, Point: int16(rng.Intn(65536)), Flags: PresenceFlags(rng.Intn(presenceFlagsKnown + 1))}
		case SubScoped:
			m := &Scoped{Kind: ScopeKind(1 + rng.Intn(4)), Stage: nonEmptyStage(), Table: table(), Room: room()}
			if m.Kind == ScopeDan && rng.Intn(2) == 0 {
				m.Room = -1
			}
			first, last, bits, _ := m.Kind.words()
			for w := first; w <= last; w++ {
				if rng.Intn(2) == 0 {
					m.Words = append(m.Words, ScopedWord{Word: uint8(w), Bits: rng.Uint32() & bits})
				}
			}
			return m
		case SubNotice:
			if dir != Down {
				continue
			}
			texts := []string{"", "world mismatch", "ゼルダ: not bound", strings.Repeat("x", MaxNoticeLen)}
			return &Notice{Code: NoticeCode(1 + rng.Intn(noticeCodes)), Text: texts[rng.Intn(len(texts))]}
		case SubCtrl:
			m := &Ctrl{Op: CtrlOp(1 + rng.Intn(3)), Arg: rng.Uint32()}
			if m.Op == CtrlTombstone {
				off := mergeOffsets[rng.Intn(len(mergeOffsets))]
				mask := class.Of(int(off)).Mask
				bit := byte(1) << rng.Intn(8)
				for bit&mask == 0 {
					bit = byte(1) << rng.Intn(8)
				}
				m.Arg = TombstoneArg(off, bit)
			}
			return m
		}
	}
}

// TestRoundTripRandom: random valid messages of every kind survive
// Encode then Decode unchanged, and re-encode to the same bytes.
func TestRoundTripRandom(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	for i := 0; i < 5000; i++ {
		dir := Dir(1 + i%2)
		m := randMsg(rng, dir)
		b, err := Encode(m)
		if err != nil {
			t.Fatalf("Encode(%+v): %v", m, err)
		}
		back, err := Decode(b, dir)
		if err != nil {
			t.Fatalf("Decode(%x) %s: %v", b, dir, err)
		}
		if !reflect.DeepEqual(back, m) {
			t.Fatalf("round trip: got %+v, want %+v", back, m)
		}
		if again, _ := Encode(back); !bytes.Equal(again, b) {
			t.Fatalf("re-encoding changed %x to %x", b, again)
		}
	}
}

// worldWith returns a WORLD payload whose image has byte off set to v.
func worldWith(t *testing.T, off uint16, v byte) []byte {
	t.Helper()
	b, err := Encode(&World{})
	if err != nil {
		t.Fatal(err)
	}
	b[1+worldHeaderLen+int(off)] = v
	return b
}

// gainsUp returns a GAINS c->s payload with the given raw entries.
func gainsUp(entries ...Gain) []byte {
	b := []byte{byte(SubGains), 0, 0, 0, 1}
	return appendGains(b, entries)
}

func gainsDown(entries ...Gain) []byte {
	b := []byte{byte(SubGains), 0, 0, 0, 0, 0, 0, 0, 1, 3}
	return appendGains(b, entries)
}

// scoped returns a SCOPED payload: kind, stage, table, room, then words.
func scoped(kind byte, stage string, table, room int8, words ...ScopedWord) []byte {
	var st Stage
	copy(st[:], stage)
	b := append([]byte{byte(SubScoped), kind}, st[:]...)
	b = append(b, byte(table), byte(room), byte(len(words)))
	for _, w := range words {
		b = append(b, w.Word, byte(w.Bits>>24), byte(w.Bits>>16), byte(w.Bits>>8), byte(w.Bits))
	}
	return b
}

func presence(stage []byte, table, room, layer int8, flags byte) []byte {
	b := append([]byte{byte(SubPresence)}, stage...)
	return append(b, byte(table), byte(room), byte(layer), 0, 0, flags)
}

// TestDecodeRejects: every malformed or out-of-range payload is refused
// with an ErrMalformed error that says what is wrong.
func TestDecodeRejects(t *testing.T) {
	hello, _ := Encode(&CoHello{Schema: Schema})
	welcome := func(status, reason byte, features uint32, fresh byte) []byte {
		b, _ := Encode(&CoWelcome{Status: StatusOK})
		b[1], b[2] = status, reason
		b[3], b[4], b[5], b[6] = byte(features>>24), byte(features>>16), byte(features>>8), byte(features)
		b[23] = fresh
		return b
	}
	stage8 := []byte("ABCDEFGH")
	world, _ := Encode(&World{})

	cases := []struct {
		name string
		dir  Dir
		data []byte
		want string
	}{
		{"empty payload", Up, nil, "empty payload"},
		{"unknown sub 0", Up, []byte{0}, "unknown sub-message 0x00"},
		{"unknown sub 9", Down, []byte{9, 1, 2}, "unknown sub-message 0x09"},
		{"bad direction", Dir(0), []byte{byte(SubCtrl), 1, 0, 0, 0, 0}, "unknown direction"},
		{"CO_HELLO going down", Down, hello, "CO_HELLO does not travel s->c"},
		{"CO_WELCOME going up", Up, welcome(1, 0, 0, 0), "CO_WELCOME does not travel c->s"},
		{"NOTICE going up", Up, []byte{byte(SubNotice), 1, 0}, "NOTICE does not travel c->s"},

		{"CO_HELLO short", Up, hello[:20], "CO_HELLO body is 19 bytes"},
		{"CO_HELLO long", Up, append(append([]byte(nil), hello...), 0), "CO_HELLO body is 46 bytes"},
		{"CO_HELLO slot 3", Up, func() []byte { b := append([]byte(nil), hello...); b[35] = 3; return b }(), "slot 3"},
		{"CO_HELLO file state 4", Up, func() []byte { b := append([]byte(nil), hello...); b[41] = 4; return b }(), "file state 4"},

		{"CO_WELCOME short", Down, welcome(1, 0, 0, 0)[:23], "CO_WELCOME body is 22 bytes"},
		{"CO_WELCOME status 0", Down, welcome(0, 0, 0, 0), "status 0"},
		{"CO_WELCOME status 4", Down, welcome(4, 0, 0, 0), "status 4"},
		{"CO_WELCOME reason 8", Down, welcome(1, 8, 0, 0), "reason 8"},
		{"CO_WELCOME refused without reason", Down, welcome(3, 0, 0, 0), "refusal"},
		{"CO_WELCOME refused with features", Down, welcome(3, 1, 1, 0), "refusal"},
		{"CO_WELCOME freshBind 2", Down, welcome(1, 0, 0, 2), "freshBind is 2"},

		{"WORLD short header", Up, world[:10], "shorter than its 18-byte header"},
		{"WORLD length field", Up, func() []byte { b := append([]byte(nil), world...); b[18] = 0x77; return b }(), "image length 0x777"},
		{"WORLD cut off", Down, world[:len(world)-1], "WORLD body is"},
		{"WORLD trailing byte", Down, append(append([]byte(nil), world...), 0), "WORLD body is"},
		{"WORLD per-player byte", Up, worldWith(t, offLife, 1), "PER_PLAYER"},
		{"WORLD tag byte", Up, worldWith(t, offReserve, 'W'), "TAG"},
		{"WORLD padding byte", Down, worldWith(t, offImagePad, 1), "NEVER"},
		{"WORLD MAX bit outside mask", Up, worldWith(t, offLetterReg, 0x04), "local bits 0x04"},
		{"WORLD dungeon-item bit 6", Up, worldWith(t, offDungeon4, 0x40), "local bits 0x40"},
		{"WORLD bottle history", Up, worldWith(t, offBottles, 0x05), "local bits 0x04"},

		{"GAINS c->s no header", Up, []byte{byte(SubGains), 0, 0}, "shorter than its header"},
		{"GAINS c->s no count", Up, []byte{byte(SubGains), 0, 0, 0, 0, 0}, "cut off before its count"},
		{"GAINS c->s count too high", Up, gainsUp(Gain{offGetItem, 1})[:9], "count 1 needs 3 bytes, 2 follow"},
		{"GAINS c->s trailing", Up, append(gainsUp(Gain{offGetItem, 1}), 0), "count 1 needs 3 bytes, 4 follow"},
		{"GAINS c->s offset past image", Up, gainsUp(Gain{addr.SaveImageSize, 1}), "outside the 0x778-byte image"},
		{"GAINS c->s offset 0xFFFF", Up, gainsUp(Gain{0xFFFF, 1}), "outside the 0x778-byte image"},
		{"GAINS c->s per-player", Up, gainsUp(Gain{offLife, 1}), "PER_PLAYER, which never travels"},
		{"GAINS c->s derived", Up, gainsUp(Gain{offMaxLife, 1}), "DERIVED, which never travels"},
		{"GAINS c->s tag", Up, gainsUp(Gain{offReserve, 1}), "TAG, which never travels"},
		{"GAINS c->s never", Up, gainsUp(Gain{offImagePad, 1}), "NEVER, which never travels"},
		{"GAINS c->s outside mask", Up, gainsUp(Gain{offLetterReg, 0x04}), "bits 0x04 are outside its MAX mask 0x03"},
		{"GAINS c->s bottle history", Up, gainsUp(Gain{offBottles, 0x04}), "bits 0x04 are outside its OR mask 0x01"},
		{"GAINS c->s duplicate", Up, gainsUp(Gain{offGetItem, 1}, Gain{offGetItem, 2}), "not strictly ascending"},
		{"GAINS c->s descending", Up, gainsUp(Gain{offGetItem, 1}, Gain{offWallet, 2}), "not strictly ascending"},
		{"GAINS s->c no header", Down, []byte{byte(SubGains), 0, 0, 0, 0, 0, 0, 0, 0}, "shorter than its header"},
		{"GAINS s->c per-player", Down, gainsDown(Gain{offLife, 1}), "PER_PLAYER"},
		{"GAINS s->c descending", Down, gainsDown(Gain{offSwitch3, 1}, Gain{offGetItem, 2}), "not strictly ascending"},
		{"GAINS s->c count", Down, gainsDown(Gain{offGetItem, 1})[:13], "count 1 needs 3 bytes"},

		{"PRESENCE short", Up, presence([]byte("sea\x00\x00\x00\x00\x00"), 0, 0, 0, 0)[:14], "PRESENCE body is 13 bytes"},
		{"PRESENCE stage without NUL", Up, presence(stage8, 0, 0, 0, 0), "no NUL"},
		{"PRESENCE stage tail", Up, presence([]byte("sea\x00wD2\x00"), 0, 0, 0, 0), "bytes after its NUL"},
		{"PRESENCE stage space", Up, presence([]byte("se a\x00\x00\x00\x00"), 0, 0, 0, 0), "byte 0x20"},
		{"PRESENCE stage control", Up, presence([]byte("se\x1b\x00\x00\x00\x00\x00"), 0, 0, 0, 0), "byte 0x1B"},
		{"PRESENCE table 16", Up, presence([]byte("sea\x00\x00\x00\x00\x00"), 16, 0, 0, 0), "table 16"},
		{"PRESENCE table -2", Down, presence([]byte("sea\x00\x00\x00\x00\x00"), -2, 0, 0, 0), "table -2"},
		{"PRESENCE room 64", Up, presence([]byte("sea\x00\x00\x00\x00\x00"), 0, 64, 0, 0), "room 64"},
		{"PRESENCE room -2", Up, presence([]byte("sea\x00\x00\x00\x00\x00"), 0, -2, 0, 0), "room -2"},
		{"PRESENCE layer 12", Up, presence([]byte("sea\x00\x00\x00\x00\x00"), 0, 0, 12, 0), "layer 12"},
		{"PRESENCE layer -2", Up, presence([]byte("sea\x00\x00\x00\x00\x00"), 0, 0, -2, 0), "layer -2"},
		{"PRESENCE flags", Up, presence([]byte("sea\x00\x00\x00\x00\x00"), 0, 0, 0, 0x20), "unknown bits"},

		{"SCOPED short", Up, scoped(1, "sea", 0, 0)[:12], "shorter than its 12-byte header"},
		{"SCOPED count too high", Up, scoped(1, "sea", 0, 0, ScopedWord{0, 1})[:15], "count 1 needs 5 bytes, 2 follow"},
		{"SCOPED trailing", Up, append(scoped(1, "sea", 0, 0), 0), "count 0 needs 0 bytes, 1 follow"},
		{"SCOPED kind 0", Up, scoped(0, "sea", 0, 0), "kind 0"},
		{"SCOPED kind 5", Down, scoped(5, "sea", 0, 0), "kind 5"},
		{"SCOPED no stage", Up, scoped(1, "", 0, 0), "has no stage"},
		{"SCOPED bad stage", Up, scoped(1, "sea\x00wD", 0, 0), "bytes after its NUL"},
		{"SCOPED table -1", Up, scoped(2, "sea", -1, 0), "table -1"},
		{"SCOPED table 16", Up, scoped(4, "sea", 16, 0), "table 16"},
		{"SCOPED zone room -1", Up, scoped(2, "sea", 0, -1), "ZONE room -1"},
		{"SCOPED kill room 64", Up, scoped(4, "sea", 0, 64), "KILL room 64"},
		{"SCOPED dan room -2", Up, scoped(1, "sea", 0, -2), "DAN room -2"},
		{"SCOPED dan word 2", Up, scoped(1, "sea", 0, 0, ScopedWord{2, 1}), "DAN word 2 (want 0..1)"},
		{"SCOPED zone word 2", Up, scoped(2, "sea", 0, 0, ScopedWord{2, 1}), "ZONE word 2 (want 0..1)"},
		{"SCOPED zone bits above u16", Up, scoped(2, "sea", 0, 0, ScopedWord{1, 0x10000}), "exceed the word's 0xFFFF"},
		{"SCOPED roomedge word 0", Up, scoped(3, "sea", 0, 0, ScopedWord{0, 1}), "ROOMEDGE word 0 (want 2..2)"},
		{"SCOPED kill word 16", Up, scoped(4, "sea", 0, 0, ScopedWord{16, 1}), "KILL word 16 (want 0..15)"},
		{"SCOPED duplicate word", Up, scoped(4, "sea", 0, 0, ScopedWord{3, 1}, ScopedWord{3, 2}), "not strictly ascending"},
		{"SCOPED descending", Up, scoped(4, "sea", 0, 0, ScopedWord{4, 1}, ScopedWord{3, 2}), "not strictly ascending"},
		{"SCOPED too many words", Up, scoped(1, "sea", 0, 0, ScopedWord{0, 1}, ScopedWord{1, 1}, ScopedWord{1, 1}), "3 words (max 2)"},

		{"NOTICE short", Down, []byte{byte(SubNotice), 1}, "shorter than its 2-byte header"},
		{"NOTICE length mismatch", Down, []byte{byte(SubNotice), 1, 3, 'a', 'b'}, "length 3, but 2 bytes follow"},
		{"NOTICE code 0", Down, []byte{byte(SubNotice), 0, 0}, "code 0"},
		{"NOTICE code 8", Down, []byte{byte(SubNotice), 8, 0}, "code 8"},
		{"NOTICE bad UTF-8", Down, []byte{byte(SubNotice), 1, 2, 0xC3, 0x28}, "not valid UTF-8"},
		{"NOTICE escape", Down, append([]byte{byte(SubNotice), 1, 4}, "\x1b[2J"...), "control character U+001B"},
		{"NOTICE newline", Down, append([]byte{byte(SubNotice), 1, 2}, "a\n"...), "control character U+000A"},

		{"CTRL short", Up, []byte{byte(SubCtrl), 1, 0, 0, 0}, "CTRL body is 4 bytes"},
		{"CTRL long", Up, []byte{byte(SubCtrl), 1, 0, 0, 0, 0, 0}, "CTRL body is 6 bytes"},
		{"CTRL op 0", Up, []byte{byte(SubCtrl), 0, 0, 0, 0, 0}, "op 0"},
		{"CTRL op 4", Down, []byte{byte(SubCtrl), 4, 0, 0, 0, 0}, "op 4"},
		{"TOMBSTONE high bits", Up, []byte{byte(SubCtrl), 3, 1, 0, byte(offGetItem), 0x80}, "bits above its offset"},
		{"TOMBSTONE no bit", Up, []byte{byte(SubCtrl), 3, 0, 0, byte(offGetItem), 0}, "names no bit"},
		{"TOMBSTONE past image", Up, []byte{byte(SubCtrl), 3, 0, 0x07, 0x78, 1}, "outside the 0x778-byte image"},
		{"TOMBSTONE per-player", Up, []byte{byte(SubCtrl), 3, 0, 0, byte(offLife), 1}, "PER_PLAYER"},
		{"TOMBSTONE outside mask", Up, []byte{byte(SubCtrl), 3, 0, byte(offLetterReg >> 8), byte(offLetterReg), 0x80}, "outside its MAX mask"},
	}
	for _, c := range cases {
		m, err := Decode(c.data, c.dir)
		if err == nil {
			t.Errorf("%s: Decode(%x) = %+v, want an error", c.name, c.data, m)
			continue
		}
		if m != nil {
			t.Errorf("%s: Decode returned %+v with its error", c.name, m)
		}
		if !errors.Is(err, ErrMalformed) || errors.Is(err, ErrSchema) {
			t.Errorf("%s: error %v does not wrap ErrMalformed alone", c.name, err)
		}
		if !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: error %q, want it to say %q", c.name, err, c.want)
		}
	}
}

// TestSchemaMismatch: a CO_HELLO of another schema is ErrSchema (not
// ErrMalformed), whatever its length, so the hub can answer NOTICE SCHEMA.
func TestSchemaMismatch(t *testing.T) {
	for _, body := range [][]byte{{0, 2}, append([]byte{0, 2}, make([]byte, 43)...), append([]byte{0xFF, 0xFF}, make([]byte, 60)...)} {
		_, err := Decode(append([]byte{byte(SubCoHello)}, body...), Up)
		if !errors.Is(err, ErrSchema) || errors.Is(err, ErrMalformed) {
			t.Errorf("CO_HELLO body %x: err = %v, want ErrSchema alone", body, err)
		}
	}
	if _, err := Encode(&CoHello{Schema: Schema + 1}); !errors.Is(err, ErrSchema) {
		t.Errorf("Encode with schema %d: err = %v, want ErrSchema", Schema+1, err)
	}
	if _, err := Decode([]byte{byte(SubCoHello), 0}, Up); !errors.Is(err, ErrMalformed) {
		t.Errorf("a 1-byte CO_HELLO body: err = %v, want ErrMalformed", err)
	}
}

// TestEncodeRefuses: Encode refuses what Decode would, and writes nothing.
func TestEncodeRefuses(t *testing.T) {
	var local World
	local.Image[offLife] = 12
	for _, m := range []Msg{
		&CoHello{Schema: Schema, Slot: 3},
		&CoWelcome{},
		&CoWelcome{Status: StatusRefused},
		&local,
		&GainsUp{Gains: []Gain{{offGetItem, 1}, {offWallet, 1}}},
		&GainsUp{Gains: []Gain{{offMaxLife, 1}}},
		&GainsDown{Gains: make([]Gain, MaxGains+1)},
		&Presence{Stage: Stage{'s', 'e', 'a', ' '}},
		&Scoped{Kind: ScopeZone, Stage: Stage{'s'}, Room: -1},
		&Notice{Code: NoticeSchema, Text: strings.Repeat("x", MaxNoticeLen+1)},
		&Notice{Code: NoticeSchema, Text: "two\nlines"},
		&Ctrl{Op: CtrlTombstone, Arg: TombstoneArg(offLife, 1)},
	} {
		if b, err := Encode(m); err == nil || b != nil {
			t.Errorf("Encode(%+v) = %x, %v; want nothing and an error", m, b, err)
		}
	}
}

// TestStripLocal: what StripLocal leaves is exactly the shared bits, and a
// stripped image always encodes.
func TestStripLocal(t *testing.T) {
	var img save.Image
	for i := range img {
		img[i] = 0xFF
	}
	StripLocal(&img)
	for off, b := range img {
		r := class.Of(off)
		want := byte(0)
		switch {
		case r.Class.Merges():
			want = r.Mask
		case r.Class == class.Derived:
			want = 0xFF
		}
		if b != want {
			t.Fatalf("offset 0x%03X (%s %s): %02X after StripLocal, want %02X", off, r.Field, r.Class, b, want)
		}
	}
	if _, err := Encode(&World{Image: img}); err != nil {
		t.Fatalf("a stripped all-ones image does not encode: %v", err)
	}
	for _, off := range []uint16{offLife, offReserve, offImagePad} {
		if img[off] != 0 {
			t.Errorf("offset 0x%03X kept %02X", off, img[off])
		}
	}
}

func TestCanonicalGains(t *testing.T) {
	in := []Gain{{offSwitch3, 0x01}, {offWallet, 1}, {offGetItem, 0x10}, {offWallet, 2}, {offSwitch3, 0x80}, {offWallet, 0}}
	want := []Gain{{offWallet, 2}, {offGetItem, 0x10}, {offSwitch3, 0x81}}
	got := CanonicalGains(in)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("CanonicalGains = %v, want %v", got, want)
	}
	if in[0] != (Gain{offSwitch3, 0x01}) {
		t.Fatal("CanonicalGains modified its input")
	}
	if _, err := Encode(&GainsUp{Gains: got}); err != nil {
		t.Fatalf("canonical gains don't encode: %v", err)
	}
	if got := CanonicalGains(nil); len(got) != 0 {
		t.Fatalf("CanonicalGains(nil) = %v", got)
	}
}

// TestEveryGainFits: a GAINS with every shared byte at once stays far
// below a frame and round-trips.
func TestEveryGainFits(t *testing.T) {
	var gs []Gain
	for _, off := range mergeOffsets {
		gs = append(gs, Gain{Off: off, Val: class.Of(int(off)).Mask})
	}
	b, err := Encode(&GainsDown{Seq: 1, Gains: gs})
	if err != nil || len(b) > MaxPayload {
		t.Fatalf("Encode of %d gains: %d bytes, %v", len(gs), len(b), err)
	}
	m, err := Decode(b, Down)
	if err != nil || len(m.(*GainsDown).Gains) != len(gs) {
		t.Fatalf("Decode: %v", err)
	}
}

func TestMakeStage(t *testing.T) {
	for _, ok := range []string{"", "sea", "M_NewD2", "sea_T", "Name"} {
		s, err := MakeStage(ok)
		if err != nil || s.String() != ok || s.IsEmpty() != (ok == "") {
			t.Errorf("MakeStage(%q) = %q, %v", ok, s, err)
		}
	}
	for _, bad := range []string{"M_NewD22", "sea ", "s\x00a", "é", "\x7f"} {
		if _, err := MakeStage(bad); !errors.Is(err, ErrMalformed) {
			t.Errorf("MakeStage(%q) = %v, want ErrMalformed", bad, err)
		}
	}
}

func TestTombstoneArg(t *testing.T) {
	c := &Ctrl{Op: CtrlTombstone, Arg: TombstoneArg(offSwitch3, 0x20)}
	if off, mask, ok := c.Tombstone(); !ok || off != offSwitch3 || mask != 0x20 {
		t.Fatalf("Tombstone = (0x%X, 0x%X, %v)", off, mask, ok)
	}
	if _, _, ok := (&Ctrl{Op: CtrlResync}).Tombstone(); ok {
		t.Fatal("a RESYNC reported a tombstone")
	}
}

func TestClassHash(t *testing.T) {
	if ClassHash() == 0 || ClassHash() != classHash {
		t.Fatalf("ClassHash = %#x", ClassHash())
	}
}

// TestDecodeCopies: a decoded message holds no reference to the payload.
func TestDecodeCopies(t *testing.T) {
	for _, g := range golden {
		b := unhex(t, g.hex)
		m, err := Decode(b, g.dir)
		if err != nil {
			t.Fatalf("%s: %v", g.name, err)
		}
		before, _ := Encode(m)
		for i := range b {
			b[i] ^= 0xFF
		}
		if after, _ := Encode(m); !bytes.Equal(before, after) {
			t.Errorf("%s: changing the payload changed the decoded message", g.name)
		}
	}
}
