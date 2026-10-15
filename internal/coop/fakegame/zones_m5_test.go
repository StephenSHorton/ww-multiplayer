package fakegame

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/internal/coop/addr"
)

// TestIsSwitch: isSwitch reads the store onSwitch writes, for each range,
// and asserts where the game does.
func TestIsSwitch(t *testing.T) {
	g, _ := newGame(t)
	load(t, g, drc)
	enter(t, g, 1, 2)
	for _, c := range []struct {
		name   string
		no     int
		room   int // OnSwitch's
		ask    int // IsSwitch's
		want   bool
		assert bool
	}{
		{"memory", 0x21, -1, -1, true, false},
		{"dan, any room", 0x85, 1, 7, true, false},
		{"zone, its room", 0xC3, 1, 1, true, false},
		{"zone, another room's zone", 0xC3, 1, 2, false, false},
		{"room switch", 0xE1, 1, 1, true, false},
		{"zone, a room with no zone", 0xC3, 1, 3, false, true},
		{"zone, room -1", 0xC3, 1, -1, false, true},
		{"zone, room 64", 0xC3, 1, addr.RoomCount, false, true},
		{"0xF0", 0xF0, -2, 1, false, true},
		{"-2", -2, -2, 1, false, true},
		{"-1 is no switch", -1, -2, 1, false, false},
		{"255 is no switch", 0xFF, -2, 1, false, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			if c.room != -2 {
				onSwitch(t, g, c.no, c.room)
			}
			got, err := g.IsSwitch(c.no, c.ask)
			if c.assert != errors.Is(err, ErrAssert) || (!c.assert && err != nil) {
				t.Fatalf("IsSwitch(0x%X, %d): error %v, want an assert: %v", c.no, c.ask, err, c.assert)
			}
			if got != c.want {
				t.Fatalf("IsSwitch(0x%X, %d) = %v, want %v", c.no, c.ask, got, c.want)
			}
		})
	}
	if err := g.OffSwitch(0xC3, 1); err != nil {
		t.Fatal(err)
	}
	if on, _ := g.IsSwitch(0xC3, 1); on {
		t.Fatal("0xC3 still set after offSwitch")
	}
}

// TestZoneTable: the zone readers follow createZone and removeZone, and
// MoveZone gives a room a new, wiped slot.
func TestZoneTable(t *testing.T) {
	g, _ := newGame(t)
	load(t, g, drc)
	enter(t, g, 1)
	slot := func(room int) int {
		t.Helper()
		z, err := g.ZoneOf(room)
		if err != nil {
			t.Fatal(err)
		}
		return z
	}
	owner := func(z int) int {
		t.Helper()
		r, err := g.ZoneRoom(z)
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	if slot(0) != 0 || slot(1) != 1 || owner(0) != 0 || owner(1) != 1 || owner(2) != -1 || g.StayNo() != 1 {
		t.Fatalf("zones after entering room 1: rooms 0,1 in %d,%d, slots 0-2 of %d,%d,%d, stay %d",
			slot(0), slot(1), owner(0), owner(1), owner(2), g.StayNo())
	}
	onSwitch(t, g, 0xC3, 1)
	onSwitch(t, g, 0xE1, 1)

	// Room 0's zone goes two room changes after it was last loaded, and
	// room 2's scene takes its slot, the first free one.
	enter(t, g, 2)
	if slot(0) != -1 || owner(0) != 2 || slot(2) != 0 || slot(1) != 1 || g.StayNo() != 2 {
		t.Fatalf("after entering room 2: rooms 0,1,2 in %d,%d,%d, slot 0 of %d, stay %d",
			slot(0), slot(1), slot(2), owner(0), g.StayNo())
	}
	if on, _ := g.IsSwitch(0xC3, 1); !on {
		t.Fatal("room 1 lost 0xC3 to a room change")
	}
	if on, _ := g.IsSwitch(0xE1, 1); on {
		t.Fatal("room 1 kept room switch 0xE1 across a room change")
	}

	// Room 1's zone moves: room 5 takes its slot, the first free one, and
	// room 1 is created again, wiped, in the next.
	from, to, err := g.MoveZone(1, 5)
	if err != nil || from != 1 || to != 2 || slot(5) != 1 || owner(1) != 5 || owner(2) != 1 || g.StayNo() != 2 {
		t.Fatalf("MoveZone(1, 5) = %d, %d, %v; room 5 in %d, slot 1 of %d, slot 2 of %d, stay %d",
			from, to, err, slot(5), owner(1), owner(2), g.StayNo())
	}
	if on, _ := g.IsSwitch(0xC3, 1); on {
		t.Fatal("room 1's moved zone kept 0xC3")
	}
	if on, _ := g.IsSwitch(0xC3, 5); on {
		t.Fatal("room 5's zone, in room 1's old slot, has its 0xC3")
	}
	for _, c := range []struct {
		name        string
		room, other int
	}{
		{"same room", 1, 1},
		{"no zone", 9, 10},
		{"other has a zone", 1, 5},
		{"room out of range", addr.RoomCount, 10},
	} {
		if _, _, err := g.MoveZone(c.room, c.other); err == nil {
			t.Errorf("%s: MoveZone(%d, %d) succeeded", c.name, c.room, c.other)
		}
	}
	if _, err := g.ZoneOf(-1); err == nil {
		t.Error("ZoneOf(-1) succeeded")
	}
	if _, err := g.ZoneRoom(addr.ZoneCount); err == nil {
		t.Error("ZoneRoom(32) succeeded")
	}
}

// TestActors: a poller opens on its switch and stays open; SWC00 of type
// 0 clears its switch every frame Link is away, another type sets it once;
// an actor runs only in its stage, with its room's zone.
func TestActors(t *testing.T) {
	g, _ := newGame(t)
	load(t, g, drc)
	enter(t, g, 1)
	door := &Poller{Stage: drc.Name, Room: 1, No: 0xC3}
	gone := &Poller{Stage: drc.Name, Room: 9, No: 0xC3} // room 9 never loads
	plate := &Swc00{Stage: drc.Name, Room: 1, No: 0xC4, Enable: 0xFF}
	once := &Swc00{Stage: drc.Name, Room: 1, No: 0xC5, Enable: 0x86, Type: 1}
	frame := func() {
		t.Helper()
		if err := g.FrameWith(door, gone, plate, once); err != nil {
			t.Fatal(err)
		}
	}
	is := func(no int) bool {
		t.Helper()
		on, err := g.IsSwitch(no, 1)
		if err != nil {
			t.Fatal(err)
		}
		return on
	}
	frame()
	if ok, _ := door.Opened(); ok {
		t.Fatal("the door opened with 0xC3 clear")
	}
	onSwitch(t, g, 0xC3, 1)
	frame()
	ok, at := door.Opened()
	if !ok || at != g.FrameCount() {
		t.Fatalf("door opened %v on frame %d, want frame %d", ok, at, g.FrameCount())
	}
	if err := g.OffSwitch(0xC3, 1); err != nil {
		t.Fatal(err)
	}
	frame()
	if ok, again := door.Opened(); !ok || again != at {
		t.Fatalf("the door shut or reopened: %v, %d", ok, again)
	}

	// The plate: set while Link is in it, cleared the frame he leaves,
	// and cleared whoever set it.
	plate.SetIn(true)
	frame()
	if !is(0xC4) {
		t.Fatal("the plate did not set 0xC4 with Link in it")
	}
	plate.SetIn(false)
	frame()
	if is(0xC4) {
		t.Fatal("the plate kept 0xC4 with Link away")
	}
	onSwitch(t, g, 0xC4, 1) // a partner's write
	frame()
	if is(0xC4) {
		t.Fatal("the plate kept a 0xC4 set from outside")
	}

	// Type 1, enabled by dan switch 0x86: sets once, then is gone.
	once.SetIn(true)
	frame()
	if is(0xC5) {
		t.Fatal("a disabled SWC00 set its switch")
	}
	onSwitch(t, g, 0x86, 1)
	frame()
	if !is(0xC5) {
		t.Fatal("SWC00 type 1 did not set 0xC5")
	}
	if err := g.OffSwitch(0xC5, 1); err != nil {
		t.Fatal(err)
	}
	frame()
	if is(0xC5) {
		t.Fatal("a deleted SWC00 set 0xC5 again")
	}

	// In another stage nothing runs; a switch number that asserts halts.
	load(t, g, drcBoss)
	n := g.FrameCount()
	bad := &Poller{Stage: drcBoss.Name, Room: 0, No: 0xF0}
	if err := g.FrameWith(door, bad); !errors.Is(err, ErrAssert) || g.FrameCount() != n {
		t.Fatalf("a poller of 0xF0: %v, %d frames run", err, g.FrameCount()-n)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := g.RunWith(ctx, 0, bad); !errors.Is(err, ErrAssert) {
		t.Fatalf("RunWith with an asserting actor: %v", err)
	}
	ctx2, cancel2 := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel2()
	if err := g.RunWith(ctx2, 0, door); err != nil || g.FrameCount() == n {
		t.Fatalf("RunWith: %v after %d frames", err, g.FrameCount()-n)
	}
}

// TestShutter: daShutter_c sees its switch only once its 30-frame timer
// has run out: one set within its first 30 executes is swallowed and it
// stays shut, one set after opens it, and one set before it is created
// has it stand open. Frames of an event or the pause menu are no executes.
// Hiding its room deletes it, and showing the room or a new room scene
// creates it anew.
func TestShutter(t *testing.T) {
	for _, c := range []struct {
		name   string
		before func(g *FakeGame, frames func(n int)) // after the shutter is created
		frames int                                   // executes before the switch is set
		open   bool                                  // open 60 frames after it is set
		at     int                                   // the frame it opens on, after the switch is set
	}{
		{name: "set on its first frame", frames: 0},
		{name: "set on its 30th", frames: 29},
		{name: "set on its 31st", frames: 30, open: true, at: 1},
		{name: "set late", frames: 100, open: true, at: 1},
		{name: "an event's frames are no executes", frames: 29,
			before: func(g *FakeGame, frames func(n int)) {
				g.SetEvent(1, 0)
				frames(40)
				g.SetEvent(0, 0)
			}},
		{name: "nor the pause menu's", frames: 29,
			before: func(g *FakeGame, frames func(n int)) {
				g.SetPauseMenu(true)
				frames(40)
				g.SetPauseMenu(false)
			}},
	} {
		// A memory switch (the live memBit), a dan switch and a zone
		// switch reach the shutter alike (dSv_info_c::isSwitch,
		// d_save.cpp:1586-1603).
		for _, no := range []int{0x05, 0x85, 0xC3} {
			t.Run(fmt.Sprintf("%s, switch 0x%02X", c.name, no), func(t *testing.T) {
				g, _ := newGame(t)
				load(t, g, drc)
				enter(t, g, 1, 2)
				sh := &Shutter{Stage: drc.Name, Room: 1, No: no}
				var ages RoomAges
				frames := func(n int) {
					t.Helper()
					for range n {
						if err := g.FrameWith(sh, &ages); err != nil {
							t.Fatal(err)
						}
					}
				}
				frames(1) // created
				if c.before != nil {
					c.before(g, frames)
				}
				frames(c.frames)
				if sh.Executes() != c.frames || sh.Open() {
					t.Fatalf("%d executes, open %v; want %d, shut", sh.Executes(), sh.Open(), c.frames)
				}
				if _, n := ages.Youngest(); n != sh.Executes() {
					t.Fatalf("RoomAges counts %d executes, the shutter %d", n, sh.Executes())
				}
				onSwitch(t, g, no, 1)
				set := g.FrameCount()
				frames(60)
				if sh.Open() != c.open {
					t.Fatalf("open %v 60 frames after its switch, want %v", sh.Open(), c.open)
				}
				if ok, at := sh.Opened(); ok != c.open || ok && at != set+uint64(c.at) {
					t.Fatalf("opened %v on frame %d, want %v on %d", ok, at, c.open, set+uint64(c.at))
				}
				if c.open != (c.frames >= ShutterFrames) {
					t.Fatalf("open %v after %d executes: ShutterFrames is %d", c.open, c.frames, ShutterFrames)
				}
			})
		}
	}

	t.Run("created with its switch set", func(t *testing.T) {
		g, _ := newGame(t)
		load(t, g, drc)
		enter(t, g, 1, 2)
		onSwitch(t, g, 0xC3, 1)
		sh := &Shutter{Stage: drc.Name, Room: 1, No: 0xC3}
		if err := g.FrameWith(sh); err != nil {
			t.Fatal(err)
		}
		if ok, _ := sh.Opened(); !sh.Open() || ok {
			t.Fatalf("open %v, opened %v: want it standing open", sh.Open(), ok)
		}
	})

	t.Run("hidden, shown and reloaded", func(t *testing.T) {
		g, m := newGame(t)
		load(t, g, drc)
		enter(t, g, 1, 2)
		sh := &Shutter{Stage: drc.Name, Room: 1, No: 0xC3}
		frames := func(n int) {
			t.Helper()
			for range n {
				if err := g.FrameWith(sh); err != nil {
					t.Fatal(err)
				}
			}
		}
		st, _ := addr.RoomStatusAddr(1)
		status := func(flags byte, proc uint32) {
			t.Helper()
			if f, p := u8(t, m, st+roomStatusOffFlags), u32(t, m, st+roomStatusOffProcID); f != flags || p != proc {
				t.Fatalf("room 1's mFlags 0x%02X, mProcID %d; want 0x%02X, %d", f, p, flags, proc)
			}
		}
		frames(50) // created, then 49 executes
		enter(t, g, 2, 1)
		if err := g.HideRoom(1); err != nil { // the door closes behind Link
			t.Fatal(err)
		}
		status(roomHidden, 0)
		frames(10)
		if g.here(drc.Name, 1) || sh.Executes() != 49 {
			t.Fatalf("a hidden room's shutter still runs: %d executes", sh.Executes())
		}
		if err := g.ShowRoom(1); err != nil { // and opens again
			t.Fatal(err)
		}
		status(0, 0)
		frames(1)
		if n := sh.Executes(); n != 0 {
			t.Fatalf("shown again: %d executes, want a new shutter", n)
		}
		frames(40)
		if err := g.ReloadRoom(1); err != nil {
			t.Fatal(err)
		}
		status(0, 1)
		frames(1)
		if n := sh.Executes(); n != 0 {
			t.Fatalf("a new room scene: %d executes, want a new shutter", n)
		}
		if zoneOf(t, m, 1) < 0 {
			t.Fatal("hiding the room freed its zone")
		}
	})
}

// TestRoomAges: RoomAges counts each room's executes since its actors
// were created, as a shutter of the room would: none while the room is
// hidden or has no zone, none on an event's or the pause menu's frames,
// and from 0 again when the room is shown again, reloaded or its stage
// changes.
func TestRoomAges(t *testing.T) {
	g, _ := newGame(t)
	load(t, g, drc) // room 0
	enter(t, g, 1, 2)
	var ages RoomAges
	sh := &Shutter{Stage: drc.Name, Room: 2, No: 0x05}
	frames := func(n int) {
		t.Helper()
		for range n {
			if err := g.FrameWith(&ages, sh); err != nil {
				t.Fatal(err)
			}
		}
	}
	want := func(why string, room, execs int) {
		t.Helper()
		if r, n := ages.Youngest(); r != room || n != execs {
			t.Fatalf("%s: youngest room %d with %d executes, want %d with %d", why, r, n, room, execs)
		}
	}
	if r, _ := ages.Youngest(); r != -1 {
		t.Fatalf("before a frame: youngest room %d", r)
	}
	frames(11) // created, then 10 executes
	want("rooms 0-2", 0, 10)
	g.SetEvent(1, 0)
	frames(5)
	g.SetEvent(0, 0)
	g.SetPauseMenu(true)
	frames(5)
	g.SetPauseMenu(false)
	want("after an event and the pause menu", 0, 10)
	if err := g.ReloadRoom(2); err != nil {
		t.Fatal(err)
	}
	frames(4)
	want("room 2 reloaded", 2, 3)
	if sh.Executes() != 3 {
		t.Fatalf("the shutter of room 2 has %d executes, RoomAges 3", sh.Executes())
	}
	if err := g.HideRoom(2); err != nil {
		t.Fatal(err)
	}
	frames(2)
	want("room 2 hidden", 0, 16)
	if err := g.ShowRoom(2); err != nil {
		t.Fatal(err)
	}
	frames(1)
	want("room 2 shown again", 2, 0)
	load(t, g, Stage{Name: "M_DragB", Table: addr.TableDRC, KeyHUD: true}) // its room 0, the zones wiped
	frames(3)
	want("another stage", 0, 2)
}

// TestHideRoomClearsRoomSwitches: the room scene of a hidden room clears
// its zone's room switches as it deletes the actors (d_s_room.cpp:82),
// and leaves its zone switches.
func TestHideRoomClearsRoomSwitches(t *testing.T) {
	g, m := newGame(t)
	load(t, g, drc)
	enter(t, g, 2, 1)
	onSwitch(t, g, 0xE1, 1)
	onSwitch(t, g, 0xC3, 1)
	if err := g.HideRoom(1); err != nil {
		t.Fatal(err)
	}
	if switchSet(t, m, 0xE1, 1) || !switchSet(t, m, 0xC3, 1) {
		t.Fatalf("after the hide: 0xE1 %v, 0xC3 %v; want clear, set", switchSet(t, m, 0xE1, 1), switchSet(t, m, 0xC3, 1))
	}
	for _, f := range []func(int) error{g.HideRoom, g.ShowRoom, g.ReloadRoom} {
		if err := f(addr.RoomCount); err == nil {
			t.Fatal("room 64 taken")
		}
	}
}
