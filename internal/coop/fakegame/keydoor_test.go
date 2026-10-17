package fakegame

import (
	"errors"
	"testing"

	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
)

// TestKeyDoor: a key door reads its switch only in actionInit, which runs
// after Link's room changes (or as the door is made), never while he stays
// in one of its rooms: a switch set then leaves the lock up, with no Open
// at no key, until he changes room. A door of a room he is not in reads
// the switch as he comes in. A key opens a locked door the way keyInit
// does from its front room, and spends nothing from its back. A new load
// of the stage makes the door anew.
func TestKeyDoor(t *testing.T) {
	setup := func(t *testing.T) (*FakeGame, *KeyDoor, func(n int)) {
		g, _ := newGame(t)
		load(t, g, drc)
		enter(t, g, 1, 0)
		d := &KeyDoor{Stage: drc.Name, Front: 0, Back: 1, No: 0x10}
		frames := func(n int) {
			t.Helper()
			for range n {
				if err := g.FrameWith(d); err != nil {
					t.Fatal(err)
				}
			}
		}
		frames(2)
		if !d.AtLink() || !d.Locked() {
			t.Fatalf("at Link %v, locked %v: want a locked door next to Link", d.AtLink(), d.Locked())
		}
		return g, d, frames
	}
	stale := func(t *testing.T, g *FakeGame, d *KeyDoor) bool {
		t.Helper()
		s, err := d.StaleLock(g)
		if err != nil {
			t.Fatal(err)
		}
		return s
	}

	t.Run("a switch set while Link stays", func(t *testing.T) {
		g, d, frames := setup(t)
		onSwitch(t, g, 0x10, -1)
		frames(100)
		if !d.Locked() || !stale(t, g, d) {
			t.Fatalf("locked %v: the door read its switch with Link in its room", d.Locked())
		}
		if err := d.Open(g); !errors.Is(err, ErrNoKey) {
			t.Fatalf("Open at no key: %v, want ErrNoKey", err)
		}
		// One room change, even into the door's other room, and the door
		// runs actionInit again.
		enter(t, g, 0, 1)
		frames(1)
		if !d.Locked() {
			t.Fatal("the lock went on the room change's own frame: checkExecute returns 0 then")
		}
		frames(1)
		if d.Locked() || stale(t, g, d) {
			t.Fatal("the lock stayed after the room change")
		}
	})
	t.Run("an event or the menu delays the look", func(t *testing.T) {
		g, d, frames := setup(t)
		onSwitch(t, g, 0x10, -1)
		g.SetPauseMenu(true)
		enter(t, g, 0, 1)
		frames(10)
		if !d.Locked() {
			t.Fatal("the door executed under the pause menu")
		}
		g.SetPauseMenu(false)
		frames(2)
		if d.Locked() {
			t.Fatal("the lock stayed after the menu closed")
		}
	})
	t.Run("a door of another room", func(t *testing.T) {
		g, d, frames := setup(t)
		enter(t, g, 2, 1)
		frames(2)
		if d.AtLink() {
			t.Fatal("the door is next to Link from another room")
		}
		onSwitch(t, g, 0x10, -1)
		frames(10)
		enter(t, g, 1, 2)
		frames(2)
		if !d.AtLink() || d.Locked() {
			t.Fatalf("at Link %v, locked %v: want it open as Link comes in", d.AtLink(), d.Locked())
		}
	})
	t.Run("a key opens it from the front", func(t *testing.T) {
		g, d, frames := setup(t)
		g.mu.Lock()
		g.st8(addr.LiveKeyNum, 1)
		g.mu.Unlock()
		enter(t, g, 0, 1)
		frames(2)
		if err := d.Open(g); err != nil {
			t.Fatal(err)
		}
		if on, _ := g.IsSwitch(0x10, -1); !on || d.Locked() {
			t.Fatalf("switch %v, locked %v after the key", on, d.Locked())
		}
		g.mu.Lock()
		count := int16(g.ld16(addr.ItemKeyNumCount))
		g.mu.Unlock()
		if count != -1 {
			t.Fatalf("mItemKeyNumCount = %d, want -1", count)
		}
		frames(1)
	})
	t.Run("from the back no key is spent", func(t *testing.T) {
		// Link is in room 1, the door's back room: keyInit acts only at
		// mFrontCheck 0 (d_door.cpp:435).
		g, d, frames := setup(t)
		g.mu.Lock()
		g.st8(addr.LiveKeyNum, 1)
		g.mu.Unlock()
		if err := d.Open(g); err != nil {
			t.Fatal(err)
		}
		g.mu.Lock()
		count := int16(g.ld16(addr.ItemKeyNumCount))
		g.mu.Unlock()
		if on, _ := g.IsSwitch(0x10, -1); on || count != 0 {
			t.Fatalf("switch %v, mItemKeyNumCount %d: want neither from the back", on, count)
		}
		enter(t, g, 0, 1)
		frames(2)
		if !d.Locked() {
			t.Fatal("the door dropped its lock with its switch clear")
		}
		g.mu.Lock()
		g.st8(addr.LiveKeyNum, 0)
		g.mu.Unlock()
		if err := d.Open(g); !errors.Is(err, ErrNoKey) {
			t.Fatalf("Open at no key: %v, want ErrNoKey", err)
		}
	})
	t.Run("a new load of the stage", func(t *testing.T) {
		g, d, frames := setup(t)
		onSwitch(t, g, 0x10, -1)
		frames(5)
		load(t, g, Stage{Name: drc.Name, Room: 1, Table: drc.Table, KeyHUD: true})
		frames(2)
		if d.Locked() {
			t.Fatal("the door made again kept its old lock")
		}
	})
}
