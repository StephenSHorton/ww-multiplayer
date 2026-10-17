package fakegame

import (
	"errors"
	"fmt"
	"sync"

	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
)

// ErrNoKey is KeyDoor.Open on a door that shows its lock while the loaded
// table holds no small key: the door offers no "Open" (daDoor10_c's event
// check returns before dEvtCnd_CANDOOR_e at mKeyNum 0, d_a_door10.cpp:
// 359-372).
var ErrNoKey = errors.New("fakegame: the door shows its lock and there is no key to open it")

// KeyDoor is a small-key door of a stage, between rooms Front and Back,
// whose lock stays until memory switch No is set: daDoor10_c of type 4 or
// 5, and daDoor12_c and daKddoor_c alike (§6.1). It is made with its
// stage (it exists while the stage that ChangeStage loaded is up), and
// starts in action 0, actionInit (daDoor10_c::CreateInit, d_a_door10.cpp:
// 461). Each execute is daDoor10_c::execute (d_a_door10.cpp:794-830):
//
//   - checkExecute (d_door.cpp:155-171) returns 0 on the first execute
//     after mStayNo moved (mRoomNo2, set at the end of each execute,
//     :829), and while Link is in neither of the door's rooms (frontCheck,
//     d_door.cpp:103-111, 2 for another room; a door whose rooms are the
//     same one is taken as faced from the front) or neither room is shown
//     (adjoinPlayer, d_door.cpp:63-75: room display, not distance; room
//     0x3F is always). Otherwise it returns 2;
//   - on 0 the action goes back to actionInit (:811-812); on 2 the action
//     runs, and actionInit calls setKey, which shows the lock unless the
//     switch is set (daDoor10_c::setKey, d_a_door10.cpp:27-50;
//     daDoor10_actionInit, :747-759), then waits (action 1), which never
//     reads the switch.
//
// So the lock is read again only at a room change of Link's, or once the
// door's rooms are hidden and shown again: a switch set while Link stays
// in one of its rooms leaves the lock up (the owner's F13; Appendix C item
// 150). Like a Shutter it does not execute while an event runs or the
// pause menu is up (actorsMove), which only delays the room change it
// sees. The door's own events (opening, the stop bars, checkExecute's 1)
// are not modelled: Open stands for Link going through it.
type KeyDoor struct {
	Stage       string
	Front, Back int
	No          int

	mu      sync.Mutex
	live    bool
	load    uint64 // the stage load it was made in
	init    bool   // action 0: actionInit runs on the next execute that returns 2
	seen    bool   // roomNo2 is set: an execute has run since it was made
	roomNo2 int8   // mRoomNo2
	locked  bool   // mKeyLock shown (keyOn)
	active  bool   // the last execute's checkExecute returned 2
}

// roomShown is dStage_roomControl_c::checkRoomDisp, as the fake keeps
// it: the room is loaded (it has a zone) and not hidden. g.mu is held.
func (g *FakeGame) roomShown(room int) bool {
	if room == 0x3F {
		return true
	}
	a, err := addr.RoomStatusAddr(room)
	if err != nil || g.zoneNo(room) < 0 {
		return false
	}
	return g.ld8(a+roomStatusOffFlags)&roomHidden == 0
}

func (d *KeyDoor) execute(g *FakeGame) error {
	g.mu.Lock()
	up := g.playing && g.curStage() == d.Stage
	load := g.loads
	stay := int8(g.ld8(addr.StayNo))
	shown := g.roomShown(d.Front) || g.roomShown(d.Back)
	g.mu.Unlock()
	d.mu.Lock()
	defer d.mu.Unlock()
	if !up {
		d.live, d.active = false, false
		return nil
	}
	if !d.live || d.load != load {
		d.live, d.load, d.init, d.seen, d.locked, d.active = true, load, true, false, false, false
	}
	if !g.actorsMove() {
		return nil
	}
	front := 2
	switch {
	case d.Front == d.Back, int(stay) == d.Front:
		front = 0
	case int(stay) == d.Back:
		front = 1
	}
	d.active = d.seen && d.roomNo2 == stay && front != 2 && shown
	if !d.active {
		d.init = true
	} else if d.init {
		on, err := g.IsSwitch(d.No, -1)
		if err != nil {
			return err
		}
		d.locked, d.init = !on, false
	}
	d.roomNo2, d.seen = stay, true
	return nil
}

// Locked reports whether d shows its lock.
func (d *KeyDoor) Locked() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.live && d.locked
}

// AtLink reports whether d is in its wait action next to Link: its last
// execute's checkExecute returned 2, and it has run actionInit since.
func (d *KeyDoor) AtLink() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.live && d.active && !d.init
}

// StaleLock reports whether d shows its lock next to Link though its
// switch is set: the lock it read before the switch was, which only a room
// change of Link's makes it read again.
func (d *KeyDoor) StaleLock(g *FakeGame) (bool, error) {
	if !d.AtLink() || !d.Locked() {
		return false, nil
	}
	return g.IsSwitch(d.No, -1)
}

// Open is Link going through d, which must be next to him (AtLink). A door
// that shows its lock takes a key: ErrNoKey at mKeyNum 0, the live copy's
// (dComIfGs_getKeyNum, d_com_inf_game.h:2106-2111), from either side, as
// the event check reads no side (d_a_door10.cpp:359-372). From the door's
// front room dDoor_key2_c::keyInit then sets the door's switch and counts
// the key out through mItemKeyNumCount (d_door.cpp:433-447), and the lock
// is gone. From its back room keyInit does neither (it acts only at
// mFrontCheck 0, d_door.cpp:435): the key stays, the switch stays clear,
// and the door locks again as it next runs actionInit. A door with no lock
// shown opens with no key and writes nothing. Link does not move: a room
// change through the door is the caller's EnterRoom.
func (d *KeyDoor) Open(g *FakeGame) error {
	if !d.AtLink() {
		return fmt.Errorf("fakegame: door 0x%02X is not next to Link", d.No)
	}
	if !d.Locked() {
		return nil
	}
	g.mu.Lock()
	keys := g.ld8(addr.LiveKeyNum)
	stay := int(int8(g.ld8(addr.StayNo)))
	g.mu.Unlock()
	if keys == 0 {
		return ErrNoKey
	}
	if d.Front != d.Back && stay != d.Front {
		return nil
	}
	if err := g.OnSwitch(d.No, -1); err != nil {
		return err
	}
	g.mu.Lock()
	g.addCount(addr.ItemKeyNumCount, -1)
	g.mu.Unlock()
	d.mu.Lock()
	d.locked = false
	d.mu.Unlock()
	return nil
}
