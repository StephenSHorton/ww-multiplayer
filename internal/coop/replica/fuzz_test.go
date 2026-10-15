package replica

import (
	"testing"

	"github.com/StephenSHorton/ww-multiplayer/internal/coop/addr"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/bind"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/class"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/fakegame"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/hub"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/wire"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/world"
)

// Native fuzz targets in the style of internal/network/fuzz_test.go. The
// seed corpora run as ordinary tests under `go test`; to fuzz one, e.g.:
//
//	go test -run='^$' -fuzz=FuzzHandle -fuzztime=20s ./internal/coop/replica/
//
// FuzzPreflight is in preflight_test.go.

// record appends one fuzz record: [op][len][body].
func record(b []byte, op byte, body []byte) []byte {
	return append(append(b, op, byte(len(body))), body...)
}

func encoded(m wire.Msg) []byte {
	b, err := wire.Encode(m)
	if err != nil {
		panic(err)
	}
	return b
}

// FuzzHandle drives a replica with an arbitrary mix of hub payloads,
// ticks, game progress, transitions, reconnects and user answers. It must
// never panic, and the only game memory it may ever write is the binding
// tag, 0x30 bytes at 0x803C532C, after the user confirmed an offer.
//
// A record is [op][len][body]; op%6 is: 0 a payload for Handle, 1 a tick,
// 2 game progress (body picks a byte and bits), 3 a transition, 4 a
// reconnect, 5 the user (confirm or decline the offer, ask to bind).
func FuzzHandle(f *testing.F) {
	c1, m1 := chest(addr.TableSea, 1)
	ok := encoded(welcome(wire.StatusOK, uint32(hub.DefaultFeatures), false))
	unbound := encoded(welcome(wire.StatusUnbound, uint32(hub.FeatureImage|hub.FeatureMerge), true))
	granted := encoded(welcome(wire.StatusUnbound, uint32(hub.FeatureImage|hub.FeatureBind), true))
	w := encoded(worldOf(3, imageOf(c1, m1)))
	g := encoded(&wire.GainsDown{Seq: 4, Origin: 2, Gains: []wire.Gain{{Off: uint16(c1), Val: m1}}})
	stage, _ := wire.MakeStage(outset.Name)
	sc := encoded(&wire.Scoped{Kind: wire.ScopeZone, Stage: stage, Table: 0, Room: outset.Room, Words: []wire.ScopedWord{{Word: 0, Bits: 1}}})
	ctrl := encoded(&wire.Ctrl{Op: wire.CtrlTombstone, Arg: wire.TombstoneArg(uint16(c1), m1)})

	var s []byte
	s = record(s, 1, nil)
	s = record(s, 0, ok)
	s = record(s, 1, nil)
	s = record(s, 0, w)
	s = record(s, 0, g)
	s = record(s, 0, sc)
	s = record(s, 2, []byte{0x01, 0x23, 0x04})
	s = record(s, 1, nil)
	s = record(s, 0, ctrl)
	s = record(s, 3, []byte{1})
	s = record(s, 1, nil)
	f.Add(s, false)

	var b []byte
	b = record(b, 1, nil)
	b = record(b, 0, unbound)
	b = record(b, 0, w)
	for range 6 {
		b = record(b, 1, nil)
	}
	b = record(b, 5, []byte{2}) // ask to bind
	for range 6 {
		b = record(b, 1, nil)
	}
	b = record(b, 5, []byte{0}) // confirm
	b = record(b, 1, nil)
	b = record(b, 0, granted)
	b = record(b, 1, nil)
	b = record(b, 0, ok)
	b = record(b, 1, nil)
	f.Add(b, true)
	f.Add([]byte{0, 3, 0xEE, 0, 1, 1, 0, 4, 1, 2}, false)

	f.Fuzz(func(t *testing.T, stream []byte, fresh bool) {
		rr := newReplicaRig(t, Config{})
		if !fresh {
			rr.tag(rr.tagFor(testWorld, testNonce))
		}
		rr.m.Record(true)
		rr.r.Connect(rr.hub)
		stages := []fakegame.Stage{outset, drc, woods}
		for len(stream) >= 2 {
			op, n := stream[0]%6, int(stream[1])
			stream = stream[2:]
			body := stream[:min(n, len(stream))]
			stream = stream[len(body):]
			switch op {
			case 0:
				rr.r.Handle(body)
			case 1:
				rr.tick()
				rr.hub.take()
			case 2:
				if len(body) >= 3 {
					off := (int(body[0])<<8 | int(body[1])) % addr.SaveImageSize
					if m := world.SharedMask(off) & body[2]; m != 0 {
						rr.set(off, m)
					}
				}
			case 3:
				if len(body) > 0 && body[0]&1 == 1 {
					rr.load(stages[int(body[0]>>1)%len(stages)])
				} else if len(body) > 0 {
					rr.enter(int(body[0]>>1) % addr.RoomCount)
				}
			case 4:
				rr.r.Disconnect()
				rr.r.Connect(rr.hub)
			case 5:
				st := rr.r.Status()
				switch {
				case len(body) > 0 && body[0]%3 == 2:
					rr.r.RequestBind()
				case st.Offer == nil:
				case len(body) > 0 && body[0]%3 == 1:
					rr.r.Decline(st.Offer.Plan)
				default:
					rr.r.Confirm(st.Offer.Plan)
				}
			}
		}
		st := rr.r.Status()
		if st.State.String() == "" || st.Why == "" && st.State != StateArmed {
			t.Fatalf("status %+v", st)
		}
		for _, w := range rr.m.Writes() {
			if w.Addr != addr.Reserve || len(w.Data) != bind.TagSize {
				t.Fatalf("wrote %d bytes at 0x%08X: only the tag may be written", len(w.Data), w.Addr)
			}
		}
	})
}

// FuzzDiffer runs a game through an arbitrary sequence of progress, game
// clears, switches, kills and transitions, and checks after every tick
// that each edge the differ reports is a bit the game set since the last
// tick: no reset, clear or reload ever reads as a gain.
//
// Each op is one byte (op%8), most followed by an argument byte: 0-1 set
// an image bit, 2 clear one, 3 a switch, 4 a kill, 5 a room change, 6 a
// stage load or a trip through file select, 7 a tick.
func FuzzDiffer(f *testing.F) {
	f.Add([]byte{0, 1, 7, 2, 1, 7, 3, 0x40, 7, 4, 3, 7, 5, 9, 7, 6, 1, 7})
	f.Add([]byte{3, 0x00, 3, 0x45, 3, 0x6F, 7, 5, 1, 7, 5, 2, 7, 5, 3, 7, 5, 44, 7, 6, 0, 7, 6, 3, 7})
	f.Add([]byte{6, 2, 7, 3, 0x01, 4, 0x10, 7, 6, 4, 7, 6, 2, 7})
	var allOR []int // every OR and OR_MASKED byte of the image
	for off := range addr.SaveImageSize {
		if world.SharedMask(off) != 0 && class.Of(off).Class != class.Max {
			allOR = append(allOR, off)
		}
	}
	f.Fuzz(func(t *testing.T, ops []byte) {
		dr := newDifferRig(t, world.FullPolicy())
		dr.ticks(2)
		stages := []fakegame.Stage{outset, drc, drcBoss, woods}
		var sets world.Image // bits the game set since the last tick
		type sk struct {
			kind wire.ScopeKind
			room int8
			word uint8
		}
		scoped := map[sk]uint32{}
		arg := func(i int) int {
			if i < len(ops) {
				return int(ops[i])
			}
			return 0
		}
		for i := 0; i < len(ops); i++ {
			a := arg(i + 1)
			switch ops[i] % 8 {
			case 0, 1:
				off := allOR[(a<<8|arg(i+2))%len(allOR)]
				m := world.SharedMask(off)
				b := byte(1) << (arg(i+3) % 8)
				if m&b != 0 && dr.read(dr.byteAddr(off), 1)[0]&b == 0 {
					dr.set(off, b)
					sets[off] |= b
				}
				i += 3
			case 2:
				off := allOR[(a<<8|arg(i+2))%len(allOR)]
				dr.clear(off, byte(1)<<(arg(i+3)%8))
				i += 3
			case 3:
				no := 0x80 + a%0x70 // dan, zone and room switches
				room := int8(dr.read(addr.StayNo, 1)[0])
				_, bit, _ := addr.SwitchBit(no)
				if had := dr.read(switchAddr(dr, no, int(room)), 1); had != nil && had[0]&bit.Mask == 0 {
					if err := dr.g.OnSwitch(no, int(room)); err == nil {
						switch {
						case no < 0xC0:
							k := sk{wire.ScopeDan, -1, uint8((no - 0x80) / 32)}
							scoped[k] |= 1 << ((no - 0x80) % 32)
						case no < 0xE0:
							k := sk{wire.ScopeZone, room, uint8((no - 0xC0) / 16)}
							scoped[k] |= 1 << ((no - 0xC0) % 16)
						default:
							k := sk{wire.ScopeRoomEdge, room, 2}
							scoped[k] |= 1 << (no - 0xE0)
						}
					}
				}
				i++
			case 4:
				room := int8(dr.read(addr.StayNo, 1)[0])
				id := a % addr.ZoneActorCount
				if err := dr.g.OnActor(id, int(room)); err == nil {
					scoped[sk{wire.ScopeKill, room, uint8(id / 32)}] |= 1 << (id % 32)
				}
				i++
			case 5:
				dr.enter(a%addr.RoomCount, arg(i+2)%addr.RoomCount)
				i += 2
			case 6:
				if a%5 == 4 {
					if err := dr.g.Save(); err != nil {
						t.Fatal(err)
					}
					dr.g.EnterFileSelect()
					if err := dr.g.LoadFile(0); err != nil {
						t.Fatal(err)
					}
					sets = world.Image{} // the differ re-baselines on the reload
					clear(scoped)
				}
				dr.load(stages[a%len(stages)])
				i++
			case 7:
				e := dr.tick()
				for _, g := range e.Gains {
					if g.Val&^sets[g.Off] != 0 {
						t.Fatalf("op %d: gain %s=0x%02X, but the game set only 0x%02X there", i, DefaultLabel(int(g.Off), -1), g.Val, sets[g.Off])
					}
				}
				for _, s := range e.Scoped {
					for _, w := range s.Words {
						k := sk{s.Kind, s.Room, w.Word}
						if s.Kind == wire.ScopeDan {
							k.room = -1
						}
						if w.Bits&^scoped[k] != 0 {
							t.Fatalf("op %d: %s edge room %d word %d bits 0x%X, but the game set 0x%X", i, s.Kind, s.Room, w.Word, w.Bits, scoped[k])
						}
					}
				}
				sets = world.Image{}
				clear(scoped)
			}
		}
	})
}

// switchAddr is where switch no of room lives now, for reading whether it
// is already set.
func switchAddr(dr *differRig, no, room int) uint32 {
	_, bit, err := addr.SwitchBit(no)
	if err != nil {
		return addr.DanSwitch
	}
	switch {
	case no < 0xC0:
		return addr.Dan + uint32(bit.Off)
	default:
		zn, _ := addr.ZoneNoAddr(room)
		z := int8(dr.read(zn, 1)[0])
		if z < 0 {
			return addr.DanSwitch
		}
		base, _ := addr.ZoneAddr(int(z))
		return base + uint32(bit.Off)
	}
}
