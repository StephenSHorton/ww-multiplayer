package replica

import (
	"fmt"

	"github.com/StephenSHorton/ww-multiplayer/internal/coop/addr"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/class"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/world"
)

// Timing is when the receiver applies a planned write: the "sync rule"
// column of §3, with §6.3's apply policy for memory switches and §6.5's
// for story bits.
type Timing uint8

const (
	// Live: as soon as the receiver is IDLE. The current table's stage
	// progress bits (read only when actors are created, d_a_tbox.cpp:
	// 447-455, d_a_item.cpp:227-237, so a write fires no event),
	// inventory, collection and capacities.
	Live Timing = iota + 1
	// OnLoad: saved data the game reads when it loads it: another
	// table's memBit (getSave, d_save.cpp:1506-1509), the ocean, the
	// charts and the event registers. Written to the saved copy only.
	OnLoad
	// OnRoomChange: the current table's memory switches, deferred to the
	// receiver's next room change (§6.3) so a partner's switch does not
	// start a camera event in the room the receiver is in.
	OnRoomChange
	// OnTransition: story event bits, written on the first tick of the
	// receiver's next stage change (§6.5) so the next stage's layers and
	// actors read them at creation.
	OnTransition
)

func (t Timing) String() string {
	switch t {
	case Live:
		return "live, when idle"
	case OnLoad:
		return "on load"
	case OnRoomChange:
		return "at the next room change"
	case OnTransition:
		return "at the next stage transition"
	}
	return fmt.Sprintf("Timing(%d)", uint8(t))
}

// timingOf is image byte off's apply rule while table cur is loaded.
func timingOf(off, cur int) Timing {
	switch CategoryOf(off) {
	case CatStage, CatSwitch:
		t, _, _ := memBitTable(off)
		switch {
		case t != cur:
			return OnLoad
		case CategoryOf(off) == CatSwitch:
			return OnRoomChange
		}
		return Live
	case CatOcean, CatRegister:
		return OnLoad
	case CatStory:
		return OnTransition
	}
	return Live
}

// Write is one planned write: OR Bits into image byte Off or, for a MAX
// byte, raise it to Bits.
type Write struct {
	Off      uint16
	Bits     byte
	Max      bool
	Category Category
	When     Timing
	// Saved is the image byte's address, addr.SaveImage + Off.
	Saved uint32
	// Live is the live memBit's copy of the byte when Off is in the
	// loaded table's saved slot, 0 otherwise. Such a write goes to both,
	// the way dComIfGs_onStageTbox does (d_com_inf_game.cpp:694-702),
	// so a putSave cannot lose it (§3.1).
	Live uint32
}

// Addrs returns where w writes: Saved, then Live if set.
func (w Write) Addrs() []uint32 {
	if w.Live != 0 {
		return []uint32{w.Saved, w.Live}
	}
	return []uint32{w.Saved}
}

// newWrite places a write of bits at off while table cur is loaded.
func newWrite(off int, bits byte, cur int) Write {
	w := Write{
		Off:      uint16(off),
		Bits:     bits,
		Max:      class.Of(off).Class == class.Max,
		Category: CategoryOf(off),
		When:     timingOf(off, cur),
		Saved:    addr.SaveImage + uint32(off),
	}
	if t, rel, ok := memBitTable(off); ok && t == cur {
		w.Live = addr.LiveMemBit + uint32(rel)
	}
	return w
}

// Hold is a story bit the receiver keeps out of its game until a
// condition holds locally, although the world has it (§6.5): applying it
// early would move the receiver's resume stage or lock its boat.
type Hold struct {
	ID  uint16 // event bit 0xHHLL
	Why string
	// released reports whether the hold is over, from the receiver's
	// canonical image (raw, local bytes included) and that image with
	// the plan's other story bits in it (after).
	released func(raw, after *world.Image) bool
}

func eventSet(img *world.Image, id uint16) bool {
	b := addr.EventByte(id)
	return img[addr.OffEvent+b.Off]&b.Mask == b.Mask
}

// Story bits the holds name (d_save_event_flag.inc).
const (
	evMetKorl         = 0x0F80 // MET_KORL, :111
	ev0801            = 0x0801 // UNK_0801, :66
	ev0808            = 0x0808 // UNK_0808, :63
	ev2401            = 0x2401 // UNK_2401, :279
	ev2D10            = 0x2D10 // UNK_2D10, :342
	evZeldaAwakened   = 0x2D02 // ZELDA_AWAKENED, :345
	evHyruleCourtyard = 0x3804 // HYRULE_COURTYARD_CUTSCENE, :430
	ev3E10            = 0x3E10 // UNK_3E10, :476
	ev3F80            = 0x3F80 // UNK_3F80, :480
)

// masterSwords are the items daPy_lk_c::checkMasterSwordEquip accepts in
// mSelectEquip[0] (d_a_player.h:599-603; dItemNo_MASTER_SWORD_1-3,
// d_item_data.h:64-69).
var masterSwords = [...]byte{0x39, 0x3A, 0x3E}

func masterSwordEquipped(raw *world.Image) bool {
	for _, it := range masterSwords {
		if raw[addr.OffSelectEquip] == it {
			return true
		}
	}
	return false
}

// Holds are §6.5's receiver-side holds. The four resume-stage bits pick
// where a loaded file starts (l_checkData in dComIfGs_setGameStartStage,
// d_com_inf_game.cpp:1304-1311): each but MET_KORL is held until the
// receiver has MET_KORL itself, and MET_KORL until it gets it in its own
// game. The other three lock boarding the boat while their condition is
// unmet (daShip_c, d_a_ship.cpp:4224-4229). §6.5 names two of them; the
// decomp's third clause, UNK_3E10 without UNK_3F80, is held the same way.
var Holds = []Hold{
	{evMetKorl, "MET_KORL moves the resume stage to Windfall", func(raw, _ *world.Image) bool {
		return eventSet(raw, evMetKorl)
	}},
	{ev0801, "0x0801 moves the resume stage to MajyuE", func(_, after *world.Image) bool {
		return eventSet(after, evMetKorl)
	}},
	{ev0808, "0x0808 moves the resume stage to MajyuE", func(_, after *world.Image) bool {
		return eventSet(after, evMetKorl)
	}},
	{ev2401, "0x2401 moves the resume stage to A_umikz", func(_, after *world.Image) bool {
		return eventSet(after, evMetKorl)
	}},
	{ev2D10, "0x2D10 locks the boat until a Master Sword is equipped", func(raw, _ *world.Image) bool {
		return masterSwordEquipped(raw)
	}},
	{evHyruleCourtyard, "0x3804 locks the boat until ZELDA_AWAKENED", func(_, after *world.Image) bool {
		return eventSet(after, evZeldaAwakened)
	}},
	{ev3E10, "0x3E10 locks the boat until 0x3F80", func(_, after *world.Image) bool {
		return eventSet(after, ev3F80)
	}},
}

// HeldBit is a bit of the world a hold keeps out of the receiver's game.
type HeldBit struct {
	Off  uint16
	Mask byte
	Hold Hold
}

// Planner computes what the receiver's game lacks of the world (§4.2):
//
//	missing = desired &^ local &^ suppressed
//
// where desired is the world's image within the policy, local the
// receiver's canonical image, and suppressed the bits the receiver's game
// ever had (so a bit the game cleared itself, a timer or a game-cleared
// story bit, is not set again) plus the holds. A MAX byte is missing when
// the world's value is above every value the receiver ever had. It is
// level-triggered: Plan works from the current state alone, so a write
// lost to the game is planned again next tick.
//
// In M2 nothing is applied: the replica logs the plan (dry run).
// A Planner is not safe for concurrent use.
type Planner struct {
	policy *world.Policy

	have    bool
	file    fileKey
	ever    world.BitSet
	everMax world.Image
}

// NewPlanner returns a Planner for policy (nil: world.DefaultPolicy).
func NewPlanner(policy *world.Policy) *Planner {
	if policy == nil {
		policy = world.DefaultPolicy()
	}
	return &Planner{policy: policy}
}

// observe records a consistent tick's local bits in the ever-had set,
// starting over when another file is loaded.
func (p *Planner) observe(o *obs) {
	if !o.consistent() {
		return
	}
	if !p.have || p.file != o.key {
		p.have, p.file = true, o.key
		p.ever, p.everMax = world.BitSet{}, world.Image{}
	}
	for off, b := range o.local {
		if b == 0 {
			continue
		}
		if class.Of(off).Class == class.Max {
			p.everMax[off] = max(p.everMax[off], b)
		} else {
			p.ever.Add(off, b)
		}
	}
}

// EverHad returns a copy of the ever-had set of OR bits.
func (p *Planner) EverHad() world.BitSet { return p.ever }

// Plan returns the writes that would bring the receiver (raw, its
// canonical image; local, its shared bits) up to view while table cur is
// loaded, in ascending offset order, and the bits holds keep back.
func (p *Planner) Plan(view, raw, local *world.Image, cur int) ([]Write, []HeldBit) {
	var miss world.Image
	for off := range view {
		m := world.SharedMask(off) & p.policy[off]
		if m == 0 {
			continue
		}
		if class.Of(off).Class == class.Max {
			v := view[off] & world.SharedMask(off)
			if v&^m == 0 && v > local[off] && v > p.everMax[off] {
				miss[off] = v
			}
			continue
		}
		miss[off] = view[off] & m &^ local[off] &^ p.ever.Mask(off)
	}

	// Holds, to a fixed point: a hold is judged on the receiver's state
	// with the plan's unheld story bits in it, and holding a bit can
	// keep back what another hold waits for (MET_KORL for the other
	// resume-stage bits).
	var held []HeldBit
	holding := make([]bool, len(Holds))
	for changed := true; changed; {
		changed = false
		after := *raw
		for i := addr.OffEvent; i < addr.OffEvent+addr.EventBitBytes; i++ {
			after[i] |= miss[i]
		}
		for i, h := range Holds {
			if holding[i] || h.released(raw, &after) {
				continue
			}
			holding[i], changed = true, true
			b := addr.EventByte(h.ID)
			off := addr.OffEvent + b.Off
			if c := miss[off] & b.Mask; c != 0 {
				miss[off] &^= c
				held = append(held, HeldBit{Off: uint16(off), Mask: c, Hold: h})
			}
		}
	}

	var ws []Write
	for off, b := range miss {
		if b != 0 {
			ws = append(ws, newWrite(off, b, cur))
		}
	}
	return ws, held
}

// Planned returns local with ws applied: the image the receiver would
// reach if every planned write landed.
func Planned(local *world.Image, ws []Write) world.Image {
	out := *local
	for _, w := range ws {
		if w.Max {
			m := world.SharedMask(int(w.Off))
			out[w.Off] = out[w.Off]&^m | w.Bits
		} else {
			out[w.Off] |= w.Bits
		}
	}
	return out
}
