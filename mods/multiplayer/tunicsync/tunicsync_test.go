package tunicsync

import (
	"bytes"
	"testing"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/mods/multiplayer/net"
	"github.com/StephenSHorton/ww-multiplayer/mods/multiplayer/playercolor"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/avatar"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/avatar/avatartest"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mailbox"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/player"
)

// run ticks the tunic slots n game frames, the C side allocating as it
// would (the same helper as wwapi/avatar's tests).
func run(t *avatar.Tunics, f *avatartest.RAM, now *time.Time, n int) {
	for i := 0; i < n; i++ {
		t.Tick(*now)
		f.CAllocate()
		f.Frame()
		*now = now.Add(16 * time.Millisecond)
	}
}

// tunicWants is every slot's Want, for failure messages.
func tunicWants(ts *avatar.Tunics) []avatar.TunicWant {
	var w []avatar.TunicWant
	for s := 0; s < avatar.TunicSlots; s++ {
		w = append(w, ts.Want(s))
	}
	return w
}

// TestSyncTunics_CasualRemoteWithoutColor: a casual remote without a
// server color still gets the casual shirt, and turning the slot off
// afterwards is written.
func TestSyncTunics_CasualRemoteWithoutColor(t *testing.T) {
	f := avatartest.NewLinkRAM(false)
	ts := avatar.NewTunics(f, t.Logf)
	red := playercolor.Palette()[1].Color
	ts.Set(0, true, red)
	now := time.Unix(0, 0)
	run(ts, f, &now, 8)

	casualFace := append(make([]byte, player.FaceBytes), appearanceCasual)
	remotes := []network.RemotePlayer{{ID: 2, Name: "a", FaceState: casualFace}}
	SyncTunics(ts, playercolor.Vanilla(), true, remotes, map[byte]int{2: 0}, now)
	if f.U8(mailbox.Mailbox+mailbox.OffTunicEnable) != avatar.TunicEnableCasual {
		t.Error("casual remote without a server color is not casual")
	}
	ts.Set(0, false, playercolor.Color{})
	ts.Tick(now)
	if f.U8(mailbox.Mailbox+mailbox.OffTunicEnable) != 0 || !ts.Settled(0) {
		t.Error("casual -> off not written")
	}
}

// TestLocalAppearance: in the casual clothes the appearance byte has
// the casual bit.
func TestLocalAppearance(t *testing.T) {
	f := avatartest.NewLinkRAM(true)
	b, ok := LocalAppearance(f)
	if !ok || b != appearanceCasual {
		t.Errorf("LocalAppearance = %#x %v, want casual", b, ok)
	}
}

func TestRemoteCasual(t *testing.T) {
	face := make([]byte, player.FaceBytes)
	if RemoteCasual(face) {
		t.Error("a 16-byte trailer (pre-appearance sender) read as casual")
	}
	if !RemoteCasual(append(face, appearanceCasual)) || RemoteCasual(append(face, 0)) {
		t.Error("appearance byte not read")
	}
}

// TestSyncTunics_SlotsFollowRemotes: link slots take their remote's
// server color, a casual remote gets the casual shirt (enable 2), a free
// slot is off, and the
// local slot takes this client's own color.
func TestSyncTunics_SlotsFollowRemotes(t *testing.T) {
	f := avatartest.NewLinkRAM(false)
	ts := avatar.NewTunics(f, t.Logf)
	red, blue := playercolor.Palette()[1].Color, playercolor.Palette()[2].Color
	casualFace := append(make([]byte, player.FaceBytes), appearanceCasual)
	remotes := []network.RemotePlayer{
		{ID: 2, Name: "a", Color: red, HasColor: true},
		{ID: 3, Name: "b", Color: blue, HasColor: true, FaceState: casualFace},
	}
	now := time.Unix(0, 0)
	for i := 0; i < 8; i++ {
		SyncTunics(ts, playercolor.Vanilla(), true, remotes, map[byte]int{2: 1, 3: 0}, now)
		f.CAllocate()
		f.Frame()
	}
	if ts.Want(1) != (avatar.TunicWant{On: true, Color: red}) {
		t.Errorf("slot 1 want = %+v, want red", ts.Want(1))
	}
	if ts.Want(0) != (avatar.TunicWant{Casual: true}) {
		t.Errorf("casual remote's slot want = %+v, want casual", ts.Want(0))
	}
	if f.U8(mailbox.Mailbox+mailbox.OffTunicEnable) != avatar.TunicEnableCasual {
		t.Errorf("casual remote's slot enable = %d, want %d", f.U8(mailbox.Mailbox+mailbox.OffTunicEnable), avatar.TunicEnableCasual)
	}
	// This client is green, which needs no swap.
	if ts.Want(avatar.TunicSlotLocal).On {
		t.Errorf("local want = %+v, want off (green)", ts.Want(avatar.TunicSlotLocal))
	}
	if !bytes.Equal(f.SlotImage(1), avatartest.ExpectRecolor(red)) {
		t.Error("slot 1 image is not red")
	}
	SyncTunics(ts, playercolor.Vanilla(), true, remotes[:1], map[byte]int{2: 1}, now)
	if ts.Want(0).On || ts.Want(0).Casual {
		t.Error("free slot 0 is on")
	}
	if f.U8(mailbox.Mailbox+mailbox.OffTunicEnable) != 0 {
		t.Error("free slot 0 still casual in RAM")
	}
}

// TestSyncTunics_SharedColorBothSlots: players may share a color, so
// two remotes in the same color each get their own slot, both on, both
// drawn in that color.
func TestSyncTunics_SharedColorBothSlots(t *testing.T) {
	f := avatartest.NewLinkRAM(false)
	ts := avatar.NewTunics(f, t.Logf)
	red := playercolor.Palette()[1].Color
	remotes := []network.RemotePlayer{
		{ID: 2, Name: "a", Color: red, HasColor: true},
		{ID: 3, Name: "b", Color: red, HasColor: true},
	}
	now := time.Unix(0, 0)
	for i := 0; i < 8; i++ {
		SyncTunics(ts, playercolor.Vanilla(), true, remotes, map[byte]int{2: 0, 3: 1}, now)
		f.CAllocate()
		f.Frame()
	}
	for s := 0; s < 2; s++ {
		if ts.Want(s) != (avatar.TunicWant{On: true, Color: red}) {
			t.Errorf("slot %d want = %+v, want red", s, ts.Want(s))
		}
		if !bytes.Equal(f.SlotImage(s), avatartest.ExpectRecolor(red)) {
			t.Errorf("slot %d image is not red", s)
		}
		if !ts.Settled(s) {
			t.Errorf("slot %d not settled", s)
		}
	}
}

// TestSyncTunics_NoServerColorAllOff: against a server without colors
// (or before the first player list) every slot is off, even ones a
// previous run left enabled, and a remote without a server color is not
// drawn in a by-ID guess.
func TestSyncTunics_NoServerColorAllOff(t *testing.T) {
	f := avatartest.NewLinkRAM(false)
	for s := 0; s < avatar.TunicSlots; s++ {
		f.Mem[mailbox.Mailbox+mailbox.OffTunicEnable+uint32(s)] = 1
	}
	ts := avatar.NewTunics(f, t.Logf)
	remotes := []network.RemotePlayer{{ID: 2, Name: "a"}, {ID: 3, Name: "b"}}
	now := time.Unix(0, 0)
	for i := 0; i < 8; i++ {
		SyncTunics(ts, playercolor.Color{}, false, remotes, map[byte]int{2: 0, 3: 1}, now)
		f.CAllocate()
		f.Frame()
	}
	for s := 0; s < avatar.TunicSlots; s++ {
		if f.U8(mailbox.Mailbox+mailbox.OffTunicEnable+uint32(s)) != 0 || !ts.Settled(s) {
			t.Errorf("slot %d: enable=%d settled=%v, want off", s, f.U8(mailbox.Mailbox+mailbox.OffTunicEnable+uint32(s)), ts.Settled(s))
		}
	}
	// Own color known, remotes still without one: remote slots stay off.
	red := playercolor.Palette()[1].Color
	for i := 0; i < 8; i++ {
		SyncTunics(ts, red, true, remotes, map[byte]int{2: 0, 3: 1}, now)
		f.CAllocate()
		f.Frame()
	}
	if ts.Want(0).On || ts.Want(1).On || !ts.Want(avatar.TunicSlotLocal).On {
		t.Errorf("wants = %+v, want only the local slot on", tunicWants(ts))
	}
}

// TestSyncTunics_CasualSkipsColorGates: the casual shirt needs no image,
// so a casual remote gets enable 2 whatever holds the color pipeline
// back: no server color (haveMine false), Link not loaded (no hero
// texture), nothing allocated yet (state 0), a buffer size for another
// texture, or its slot's allocation failed (state 0xFD, buf 0).
func TestSyncTunics_CasualSkipsColorGates(t *testing.T) {
	casualFace := append(make([]byte, player.FaceBytes), appearanceCasual)
	remotes := []network.RemotePlayer{{ID: 2, Name: "a", FaceState: casualFace}, {ID: 3, Name: "b"}}
	slots := map[byte]int{2: 0, 3: 1}
	red := playercolor.Palette()[1].Color
	for _, tc := range []struct {
		name     string
		haveMine bool
		setup    func(f *avatartest.RAM)
	}{
		{"no server color", false, func(f *avatartest.RAM) {}},
		{"Link not loaded", true, func(f *avatartest.RAM) { f.Put32(addr.PlayerPtr, 0) }},
		{"not allocated", true, func(f *avatartest.RAM) {
			f.Mem[mailbox.Mailbox+mailbox.OffTunicState] = 0
			f.Put32(mailbox.Mailbox+mailbox.OffTunicBufSize, 0)
		}},
		{"size mismatch", true, func(f *avatartest.RAM) {
			f.Put32(mailbox.Mailbox+mailbox.OffTunicBufSize, 0x1234)
			f.Mem[mailbox.Mailbox+mailbox.OffTunicState] = 1
		}},
		{"alloc failed", true, func(f *avatartest.RAM) {
			f.Put32(mailbox.Mailbox+mailbox.OffTunicBufSize, uint32(len(avatartest.TestImage())))
			f.Put32(mailbox.Mailbox+avatar.OffTunicBuf(1), avatartest.HeapTop+0x1000)
			f.Mem[mailbox.Mailbox+mailbox.OffTunicState] = 0xFD
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := avatartest.NewLinkRAM(false)
			tc.setup(f)
			ts := avatar.NewTunics(f, t.Logf)
			now := time.Unix(0, 0)
			for i := 0; i < 4; i++ {
				SyncTunics(ts, red, tc.haveMine, remotes, slots, now)
				f.Frame()
			}
			if got := f.U8(mailbox.Mailbox + mailbox.OffTunicEnable); got != avatar.TunicEnableCasual || !ts.Settled(0) {
				t.Errorf("slot 0 enable = %d settled = %v, want casual", got, ts.Settled(0))
			}
			if got := f.U8(mailbox.Mailbox + mailbox.OffTunicEnable + 1); got == avatar.TunicEnableCasual {
				t.Error("non-casual slot 1 is casual")
			}
			if !tc.haveMine && (ts.Want(1).On || ts.Want(avatar.TunicSlotLocal).On) {
				t.Errorf("without a server color the colors are on: %+v", tunicWants(ts))
			}
		})
	}
}
