package derive

import (
	"encoding/binary"
	"testing"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/internal/coop/addr"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/gate"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/snap"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/tables"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/tables/keys"
)

// fuzzGame is a crude game the fuzzer drives: a save it pokes at, gates
// it opens and shuts, and a HUD that takes the counters.
type fuzzGame struct {
	s      snap.Snapshot
	hud    bool
	armed  bool
	stagOK bool
}

// The bytes the fuzzer may poke: what the derive reads.
var fuzzBytes = func() []int {
	var b []int
	for t := range addr.TableCount {
		for i := range addr.MemBitSize {
			b = append(b, addr.InfoOffSavedata+addr.OffMemBit(t)+i)
		}
	}
	for i := range addr.MemBitSize {
		b = append(b, addr.InfoOffMemory+i)
	}
	return append(b,
		addr.InfoOffSavedata+addr.OffMaxLife, addr.InfoOffSavedata+addr.OffMaxLife+1,
		addr.InfoOffSavedata+addr.OffMaxMagic, addr.InfoOffSavedata+addr.OffGetItem+slotDekuLeaf,
		addr.InfoOffSavedata+addr.OffEvent+0x31, addr.InfoOffSavedata+addr.OffEvent+0x13,
		addr.InfoOffSavedata+addr.OffEvent+0xAC)
}()

func (g *fuzzGame) save(off int) *byte { return &g.s.Save[off] }

// frame is the HUD: each nonzero counter lands (the key counter only with
// the key HUD on) and is zeroed.
func (g *fuzzGame) frame() {
	if n := g.s.MaxLifeCount; n != 0 {
		at := addr.InfoOffSavedata + addr.OffMaxLife
		v := min(max(int(binary.BigEndian.Uint16(g.s.Save[at:]))+int(n), 0), LifeCap)
		binary.BigEndian.PutUint16(g.s.Save[at:], uint16(v))
		g.s.MaxLifeCount = 0
	}
	if n := g.s.MaxMagicCount; n != 0 {
		p := g.save(addr.InfoOffSavedata + addr.OffMaxMagic)
		*p = byte(min(int(*p)+int(n), MagicCap))
		g.s.MaxMagicCount = 0
	}
	if n := g.s.KeyNumCount; n != 0 && g.hud {
		p := g.save(addr.InfoOffMemory + addr.MemBitOffKeyNum)
		*p = byte(min(max(int(*p)+int(n), 0), MaxKeys))
		g.s.KeyNumCount = 0
	}
	g.s.Heartbeat++
}

// apply makes w the way a router would: a compare-and-set.
func (g *fuzzGame) apply(w Write) {
	switch w.Kind {
	case KeyCounter, LifeCounter, MagicCounter:
		c := map[Kind]*int16{KeyCounter: &g.s.KeyNumCount, LifeCounter: &g.s.MaxLifeCount, MagicCounter: &g.s.MaxMagicCount}[w.Kind]
		if *c == 0 {
			*c = int16(w.Delta())
		}
	case KeyLive:
		if p := g.save(addr.InfoOffMemory + addr.MemBitOffKeyNum); *p == w.Expect[0] {
			*p = w.Data[0]
		}
	case KeySaved:
		if p := g.save(addr.InfoOffSavedata + addr.OffMemBit(w.Table) + addr.MemBitOffKeyNum); *p == w.Expect[0] {
			*p = w.Data[0]
		}
	}
}

// check is what every plan must satisfy, whatever the game did.
func check(t *testing.T, in Input, w Write, enabled map[int]bool) {
	t.Helper()
	s, st := in.Snap, in.Gates
	if !in.Armed || !st.CanWriteIdle() {
		t.Fatalf("%v planned with the gates shut (armed %v): %s", w, in.Armed, st.Why())
	}
	if len(w.Data) != len(w.Expect) || w.Reason == "" {
		t.Fatalf("%v: data % X, expect % X", w, w.Data, w.Expect)
	}
	if c, ok := w.Counter(); ok {
		if len(w.Data) != 2 || w.Expect[0] != 0 || w.Expect[1] != 0 || !st.CanWriteCounter(c) || st.Pending(c) != 0 {
			t.Fatalf("%v with the %v counter at %d", w, c, st.Pending(c))
		}
	} else if len(w.Data) != 1 || int(w.Data[0]) > MaxKeys {
		t.Fatalf("%v: a key byte of % X", w, w.Data)
	}
	table := int(s.Table)
	switch w.Kind {
	case KeyCounter:
		if w.Addr != addr.ItemKeyNumCount || !in.Stag.KeyHUD || w.Table != table || s.KeyNumCount != 0 {
			t.Fatalf("%v: HUD %v, table %d", w, in.Stag.KeyHUD, table)
		}
	case KeyLive:
		if w.Addr != addr.LiveKeyNum || in.Stag.KeyHUD || w.Table != table || s.KeyNumCount != 0 ||
			w.Expect[0] != s.Save[addr.InfoOffMemory+addr.MemBitOffKeyNum] {
			t.Fatalf("%v: HUD %v, table %d", w, in.Stag.KeyHUD, table)
		}
	case KeySaved:
		a, _ := addr.SavedMemBitAddr(w.Table)
		if w.Addr != a+addr.MemBitOffKeyNum || w.Table == table ||
			w.Expect[0] != s.Save[addr.InfoOffSavedata+addr.OffMemBit(w.Table)+addr.MemBitOffKeyNum] {
			t.Fatalf("%v with table %d loaded", w, table)
		}
	case LifeCounter:
		have := int(binary.BigEndian.Uint16(s.Save[addr.InfoOffSavedata+addr.OffMaxLife:]))
		if w.Addr != addr.ItemMaxLifeCount || w.Delta() <= 0 || have+w.Delta() > LifeCap {
			t.Fatalf("%v over max life %d", w, have)
		}
	case MagicCounter:
		have := int(s.Save[addr.InfoOffSavedata+addr.OffMaxMagic])
		if w.Addr != addr.ItemMaxMagicCount || w.Delta() <= 0 || have+w.Delta() > MagicCap {
			t.Fatalf("%v over max magic %d", w, have)
		}
	default:
		t.Fatalf("unknown write %v", w)
	}
	if w.Table >= 0 && !enabled[w.Table] {
		t.Fatalf("%v for a table that does not derive", w)
	}
}

// FuzzDeriver drives a Deriver through ticks the fuzzer makes up: bits and
// counts poked anywhere the derive reads, events, stage changes, the key
// HUD, ARMED, save-state loads. Every plan must keep to the derive's
// addresses, the gates, the counters at 0, add-only life and magic, and
// the enabled tables.
func FuzzDeriver(f *testing.F) {
	f.Add([]byte{0x00, 0x05, 0x10, 0x20, 0x31, 0x42})
	f.Add([]byte{0x81, 0x02, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08, 0x09, 0x0A, 0x0B, 0x0C})
	f.Add([]byte("keys, hearts and magic across many ticks of settle and IDLE"))
	failing := *drcTable()
	failing.SaveTable, failing.Pass = addr.TableFW, false
	tb := &tables.Tables{Keys: &keys.Result{Tables: []keys.Table{*drcTable(), failing}}, Hearts: heartTable()}
	enabled := map[int]bool{addr.TableDRC: true}
	f.Fuzz(func(t *testing.T, ops []byte) {
		d := New(Config{Tables: tb})
		g := &fuzzGame{armed: true, stagOK: true, hud: true}
		g.s.StageName, g.s.PlayerPtr, g.s.Table, g.s.StagInfo = "M_NewD2", 0x80A00000, addr.TableDRC, 0x80A01000
		var tr gate.Tracker
		now := time.Unix(0, 0)
		for i := 0; i < len(ops); i++ {
			op := ops[i]
			arg := byte(0)
			if i+1 < len(ops) {
				i++
				arg = ops[i]
			}
			switch op % 14 {
			case 12: // co-op's live write of a loaded memBit byte, a key door's switch among them
				if t := int(g.s.Table); addr.ValidTable(t) {
					rel := int(arg) % addr.MemBitSize
					if arg&0x80 != 0 {
						_, b, _ := addr.SwitchBit(0x10 + int(arg>>5&3))
						rel = b.Off
					}
					live := g.save(addr.InfoOffMemory + rel)
					set := (byte(1)<<(op>>5) | byte(arg)&0x0F) &^ *live
					*live |= set
					*g.save(addr.InfoOffSavedata + addr.OffMemBit(t) + rel) |= set
					d.WroteLive(addr.OffMemBit(t)+rel, set)
				}
			case 13: // a room change, or a bounce back
				g.s.StayNo = int8(arg % 4)
			case 0, 1, 2: // a bit set somewhere the derive reads
				*g.save(fuzzBytes[int(arg)*7%len(fuzzBytes)]) |= 1 << (op >> 5)
			case 3: // a key count, life count or magic count from the game
				switch arg % 3 {
				case 0:
					g.s.KeyNumCount += int16(int8(op)) % 3
				case 1:
					g.s.MaxLifeCount += int16(arg % 5)
				default:
					g.s.MaxMagicCount += 16
				}
			case 4:
				g.s.EvtMode = arg & 1
			case 5:
				g.hud = arg&1 != 0
			case 6:
				g.armed = arg%4 != 0
			case 7: // another stage, maybe another table
				g.s.Table = int8(arg % addr.TableCount)
				g.s.StageName = []string{"M_NewD2", "kindan", "sea", "M_DragB"}[arg%4]
				copy(g.s.Save[addr.InfoOffMemory:addr.InfoOffMemory+addr.MemBitSize],
					g.s.Save[addr.InfoOffSavedata+addr.OffMemBit(int(g.s.Table)):])
			case 8:
				g.s.Heartbeat -= uint32(arg) // a save state
			case 9:
				g.stagOK = arg%8 != 0
			case 10:
				g.s.NextEnable = int8(arg & 1)
			default: // time passes
				for range int(arg % 32) {
					g.frame()
					now = now.Add(tickEvery)
					s := g.s
					st := tr.Observe(&s)
					stag := Stag{OK: g.stagOK, Table: int(s.Table), KeyHUD: g.hud}
					in := Input{Now: now, Snap: &s, Gates: st, Stag: stag, Armed: g.armed}
					for _, w := range d.Tick(in).Writes {
						check(t, in, w, enabled)
						g.apply(w)
					}
				}
			}
			g.frame()
			now = now.Add(tickEvery)
			s := g.s
			st := tr.Observe(&s)
			in := Input{Now: now, Snap: &s, Gates: st, Stag: Stag{OK: g.stagOK, Table: int(s.Table), KeyHUD: g.hud}, Armed: g.armed}
			for _, w := range d.Tick(in).Writes {
				check(t, in, w, enabled)
				g.apply(w)
			}
			// A door held is an open door, and each is named.
			for _, k := range d.Status().Keys {
				if k.Facts.Held > k.Facts.Open || len(k.Held) != k.Facts.Held {
					t.Fatalf("table %d: %d of %d open doors held, %v", k.Table, k.Facts.Held, k.Facts.Open, k.Held)
				}
			}
		}
	})
}

const tickEvery = 100 * time.Millisecond
