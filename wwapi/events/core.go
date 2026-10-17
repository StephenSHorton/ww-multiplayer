package events

// The core game events, emitted on the core bus by the watcher a
// wwapi.Game owns (design §5.3). They are polled snapshot edges, not
// in-game callbacks: the watcher compares one snapshot with the last, at
// 10 Hz by default, so a flag set and cleared again between two polls is
// never seen, and the order of the events of one tick is not defined.
//
// More of §5.3's table (HeartbeatResumed, RoomChanged, ItemLost, ...)
// comes as consumers need it.

// Place is where the game is: the loaded stage, the room Link is in, the
// save table the live memBit holds, and the file slot loaded.
type Place struct {
	Stage string // mCurStage's name; "" before the first stage
	Room  int8   // mStayNo; -1 at a stage start
	Table int8   // mDan.mStageNo; -1 until the first stage load
	File  uint8  // mDataNum
}

// StageChanged is a change of stage, save table or file (a room change
// alone is not one). The watcher's first snapshot emits one with a zero
// From and First set, so a subscriber learns where the game starts; a
// Game attached before the first stage load gets To.Stage "" there, and
// the first stage then comes as an ordinary StageChanged.
type StageChanged struct {
	Base
	From, To Place
	First    bool
}

// EventName implements Event.
func (StageChanged) EventName() string { return "core.stage_changed" }

// ItemGained is an inventory slot (dSv_player_item_c mItems[Slot])
// taking a new item: empty to an item, or one item to another (an
// upgrade, such as the Master Sword's power). Prev is the item before,
// 0xFF for an empty slot. A bottle slot (addr.SlotBottle0 on) counts only
// when it was empty: a bottle's contents changing (drinking a potion,
// catching a fairy) is not a gain.
type ItemGained struct {
	Base
	Slot       int
	Item, Prev byte
}

// EventName implements Event.
func (ItemGained) EventName() string { return "core.item_gained" }

// SwitchKind says which switch store a SwitchSet comes from.
type SwitchKind uint8

const (
	// SwitchMemory is a memory switch (0x00-0x7F) of the live memBit, the
	// table the stage loaded (Table).
	SwitchMemory SwitchKind = iota + 1
	// SwitchSaved is a memory switch of a table's saved memBit
	// (mMemory[Table]): putSave copying the live memBit back, or a
	// write to another table.
	SwitchSaved
	// SwitchDan is a dungeon switch (0x80-0xBF) of mDan.
	SwitchDan
	// SwitchZone is a zone switch (0xC0-0xEF) of mZone[Zone], which
	// belongs to room Room.
	SwitchZone
)

func (k SwitchKind) String() string {
	switch k {
	case SwitchMemory:
		return "memory"
	case SwitchSaved:
		return "saved"
	case SwitchDan:
		return "dan"
	case SwitchZone:
		return "zone"
	}
	return "SwitchKind(?)"
}

// SwitchSet is a stage switch bit rising.
type SwitchSet struct {
	Base
	Kind   SwitchKind
	Switch int  // the switch number the game's onSwitch takes
	Table  int8 // SwitchMemory, SwitchSaved: the save table; else -1
	Zone   int  // SwitchZone: the zone index; else -1
	Room   int8 // SwitchZone: the zone's room; else -1
}

// EventName implements Event.
func (SwitchSet) EventName() string { return "core.switch_set" }

// EventBitSet is a story event bit (dSv_event_c mEvent) rising. ID is
// the id onEventBit takes, 0xHHLL: byte HH, mask LL.
type EventBitSet struct {
	Base
	ID uint16
}

// EventName implements Event.
func (EventBitSet) EventName() string { return "core.event_bit_set" }

// HeartbeatLost is the mod's per-frame heartbeat stopping after it had
// been moving: emulation paused, the game reset, or a save state loaded.
// Why is the HEARTBEAT gate's reason.
type HeartbeatLost struct {
	Base
	Why string
}

// EventName implements Event.
func (HeartbeatLost) EventName() string { return "core.heartbeat_lost" }
