package boatsync

import (
	"bytes"
	"encoding/binary"
	"math"
	"testing"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/mods/multiplayer/net"
	"github.com/StephenSHorton/ww-multiplayer/mods/multiplayer/playercolor"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mailbox"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mem"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/player"
)

func u16(v uint16) []byte { b := make([]byte, 2); binary.BigEndian.PutUint16(b, v); return b }
func u32(v uint32) []byte { b := make([]byte, 4); binary.BigEndian.PutUint32(b, v); return b }
func f32(v float32) []byte { return u32(math.Float32bits(v)) }

const (
	fakeShip = 0x80900000
	fakeBody = 0x80910000
	fakeHead = 0x80920000
)

// fakeShipMem lays out a daShip_c on the sea in fake RAM: at (1000, 5,
// -2000), heading 0x4000, mast up at frame 6.9995, the cannon out.
func fakeShipMem(t *testing.T, aboard bool) *mem.FakeMem {
	t.Helper()
	m := mem.NewFakeMem()
	must := func(err error) {
		if err != nil {
			t.Fatal(err)
		}
	}
	must(m.Write(addr.CurStageName, []byte("sea\x00\x00\x00\x00\x00")))
	must(m.Write(shipPtr, u32(fakeShip)))
	must(m.Write(fakeShip+addr.ActorOffProcName, u16(procShip)))
	must(m.Write(fakeShip+shipBase, append(append(f32(1000), f32(5)...), f32(-2000)...)))
	must(m.Write(fakeShip+offRoom, []byte{0x17}))
	must(m.Write(fakeShip+offShape, append(append(u16(0xFF00), u16(0x4000)...), u16(0x0100)...)))
	must(m.Write(fakeShip+offBodyAnm, u32(fakeBody)))
	must(m.Write(fakeShip+offHeadAnm, u32(fakeHead)))
	must(m.Write(fakeShip+offPart, []byte{partCannon}))
	must(m.Write(fakeShip+offStateFlag, u32(sflgSailOn|1)))
	must(m.Write(fakeShip+offSail, u16(0xE000)))
	must(m.Write(fakeShip+offTiller, u16(0x0123)))
	must(m.Write(fakeShip+offMastBck, u16(bckMastOn)))
	must(m.Write(fakeShip+offCannonYaw, u16(0x1800)))
	must(m.Write(fakeShip+offCannonPit, u16(0x2800)))
	must(m.Write(fakeShip+offHeadBck, u16(0x0C)))
	must(m.Write(fakeShip+offMastScale, f32(0.001)))
	must(m.Write(fakeBody+morfFrame, f32(6.9995)))
	must(m.Write(fakeHead+morfFrame, f32(300)))
	if aboard {
		must(m.Write(playerStatus0, u32(sttsShipRide)))
	}
	return m
}

func TestLocal(t *testing.T) {
	b, ok := Local(fakeShipMem(t, true), [3]float32{990, 0, -2010})
	want := Boat{Present: true, Aboard: true, MastOn: true, MastHide: true, SailOn: true, Part: partCannon, MastFrame: 7,
		HeadBck: 0x0C, HeadFrame: 255, Room: 0x17, Pos: [3]float32{10, 5, 10}, Angle: [3]int16{-0x100, 0x4000, 0x100},
		SailAngle: -0x2000, Tiller: 0x123, PartA: 0x1800, PartB: 0x2800, Stage: "sea"}
	if !ok || b != want {
		t.Fatalf("Local aboard = %+v %v\nwant %+v", b, ok, want)
	}
	// Parked: absolute. The crane: arm angle plus swing.
	m := fakeShipMem(t, false)
	_ = m.Write(fakeShip+offPart, []byte{partCrane})
	_ = m.Write(fakeShip+offCrane, u16(0x2000))
	_ = m.Write(fakeShip+offCraneSwng, u16(0x0100))
	_ = m.Write(fakeShip+offMastBck, u16(bckMastOff))
	_ = m.Write(fakeShip+offHeadBck, u16(bckMastOn)) // never a head bck
	b, ok = Local(m, [3]float32{1, 2, 3})
	if !ok || b.Aboard || b.Pos != [3]float32{1000, 5, -2000} || b.PartA != 0x2100 || b.PartB != 0 || b.MastOn || b.HeadBck != 0 {
		t.Fatalf("Local parked = %+v %v", b, ok)
	}
	// No ship, another process in the slot, or a stage change: none.
	if _, ok := Local(mem.NewFakeMem(), [3]float32{}); ok {
		t.Error("no ship reported ok")
	}
	m = fakeShipMem(t, true)
	_ = m.Write(fakeShip+addr.ActorOffProcName, u16(0x00A8))
	if _, ok := Local(m, [3]float32{}); ok {
		t.Error("a non-ship actor reported ok")
	}
	m = fakeShipMem(t, true)
	_ = m.Write(addr.NextStageEnable, []byte{1})
	if _, ok := Local(m, [3]float32{}); ok {
		t.Error("a stage change reported ok")
	}
	m = fakeShipMem(t, false)
	_ = m.Write(fakeShip+shipBase, f32(float32(math.NaN())))
	if _, ok := Local(m, [3]float32{}); ok {
		t.Error("a NaN position reported ok")
	}
}

func trailer(run []byte) []byte {
	face := make([]byte, player.FaceBytes+1+2)
	return append(face, run...)
}

func TestEncodeDecode(t *testing.T) {
	if RunOffset != 19 || RunOffset+RunBytes > network.MaxFaceBytes {
		t.Fatalf("the run ends at %d, past the server's %d-byte trailer cap", RunOffset+RunBytes, network.MaxFaceBytes)
	}
	b := Boat{Present: true, Aboard: true, SailOn: true, Part: 3, MastFrame: 9, HeadBck: 7, HeadFrame: 2, Room: -1,
		Pos: [3]float32{1, -2, 3}, Angle: [3]int16{1, -2, 3}, SailAngle: 4, Tiller: -5, PartA: 6, PartB: -7, Stage: "Siren"}
	run := Encode(b, true)
	if len(run) != RunBytes || run[runFlags] != RunPresent|RunAboard|RunSailOn {
		t.Fatalf("run % x", run)
	}
	got, ok := Decode(trailer(run))
	if !ok || got != b {
		t.Fatalf("round trip %+v %v, want %+v", got, ok, b)
	}
	// A stage name fills at most 7 bytes: the 8th stays NUL.
	long := Encode(Boat{Present: true, Stage: "ABCDEFGHIJ"}, true)
	if got, _ := Decode(trailer(long)); got.Stage != "ABCDEFG" || long[runStage+7] != 0 {
		t.Errorf("long stage %q", got.Stage)
	}
	// No boat: an all-zero run.
	if z := Encode(b, false); !bytes.Equal(z, make([]byte, RunBytes)) {
		t.Error("an absent boat's run is not zero")
	}
	if got, ok := Decode(trailer(make([]byte, RunBytes))); !ok || got.Present {
		t.Errorf("zero run = %+v %v", got, ok)
	}
	// An older sender (19 bytes, or a cut run): no run at all.
	if _, ok := Decode(trailer(run[:RunBytes-1])); ok {
		t.Error("a short run decoded")
	}
	if _, ok := Decode(trailer(nil)); ok {
		t.Error("a 19-byte trailer decoded")
	}
	// Garbage coordinates are no boat.
	bad := append([]byte(nil), run...)
	copy(bad[runPos:], f32(float32(math.Inf(1))))
	if got, ok := Decode(trailer(bad)); !ok || got.Present {
		t.Errorf("Inf position = %+v %v", got, ok)
	}
}

func TestParseSettings(t *testing.T) {
	all := Settings{On: true, Sail: true, Parts: true}
	for v, want := range map[string]Settings{
		"": all, "on": all, "OFF": {}, "0": {}, "none": {}, "nosail": {On: true, Parts: true},
		"noparts": {On: true, Sail: true}, "nosail+noparts": {On: true}, "nosail, noparts": {On: true},
	} {
		if got := ParseSettings(v); got != want {
			t.Errorf("ParseSettings(%q) = %+v, want %+v", v, got, want)
		}
	}
	t.Setenv("WW_BOATS", "off")
	if SettingsFromEnv().On {
		t.Error("WW_BOATS=off left boats on")
	}
}

func TestReadHere(t *testing.T) {
	m := mem.NewFakeMem()
	if ReadHere(m).OK {
		t.Error("no Link is in play")
	}
	_ = m.Write(addr.PlayerPtr, u32(0x80800000))
	_ = m.Write(addr.CurStageName, []byte("Siren\x00\x00\x00"))
	_ = m.Write(addr.StayNo, []byte{0x05})
	if h := ReadHere(m); !h.OK || h.Stage != "Siren" || h.Room != 5 {
		t.Errorf("ReadHere = %+v", h)
	}
	_ = m.Write(addr.NextStageEnable, []byte{1})
	if ReadHere(m).OK {
		t.Error("a stage change is in play")
	}
}

func remote(id byte, name string, b Boat, ok bool, last time.Time) network.RemotePlayer {
	face := make([]byte, player.FaceBytes+3)
	if ok {
		face = append(face, Encode(b, true)...)
	}
	return network.RemotePlayer{ID: id, Name: name, FaceState: face, LastPose: last}
}

func TestTrackerBuild(t *testing.T) {
	now := time.Unix(1000, 0)
	all := ParseSettings("")
	sea := Here{Stage: "sea", Room: 3, OK: true}
	aboard := Boat{Present: true, Aboard: true, MastOn: true, Pos: [3]float32{10, -5, 20}, Angle: [3]int16{0, 0x4000, 0}, Stage: "sea", Room: 40}
	parked := Boat{Present: true, Pos: [3]float32{5000, 0, -7000}, Angle: [3]int16{0, -0x2000, 0}, Stage: "sea", Room: 12}
	anchors := map[byte][3]float32{1: {100, 200, 300}, 2: {1, 1, 1}}

	var tr Tracker
	r1 := remote(1, "Tetra", aboard, true, now)
	r1.Color, r1.HasColor = playercolor.Color{R: 0xC0, G: 0x10, B: 0x20}, true
	r2 := remote(2, "Aryll", parked, true, now)
	blk := tr.Build(all, sea, []network.RemotePlayer{r2, r1}, anchors, "", now)
	if blk.Flags != mailbox.BoatFSail|mailbox.BoatFParts || len(blk.Entries) != 2 {
		t.Fatalf("block %+v", blk)
	}
	// Id order; the aboard one at its mini-Link plus the offset, in its colour.
	if e := blk.Entries[0]; e.Pos != [3]float32{110, 195, 320} || e.Parked || !e.MastOn || e.R != 0xC0 || e.Angle[1] != 0x4000 {
		t.Errorf("aboard entry %+v", e)
	}
	if e := blk.Entries[1]; e.Pos != parked.Pos || !e.Parked || e.R != 0 {
		t.Errorf("parked entry %+v", e)
	}

	// Aryll walks into a house: her run says no ship (or her poses stop
	// and the trailer goes stale). The parked boat stays on the sea.
	r2 = remote(2, "Aryll", Boat{}, true, now)
	blk = tr.Build(all, sea, []network.RemotePlayer{r2}, anchors, "", now)
	if len(blk.Entries) != 1 || blk.Entries[0].Pos != parked.Pos {
		t.Fatalf("held parked boat %+v", blk.Entries)
	}
	// ... and the local player goes ashore too: not drawn there, still held.
	if blk = tr.Build(all, Here{Stage: "Abesso", OK: true}, []network.RemotePlayer{r2}, anchors, "", now); len(blk.Entries) != 0 {
		t.Errorf("a sea boat drawn in Abesso: %+v", blk.Entries)
	}
	if blk = tr.Build(all, sea, []network.RemotePlayer{r2}, anchors, "", now); len(blk.Entries) != 1 {
		t.Error("the held boat did not come back on the sea")
	}
	// She boards again: the hold goes, and with no mini-Link anchor there
	// is no aboard boat to draw.
	r2 = remote(2, "Aryll", aboard, true, now)
	if blk = tr.Build(all, sea, []network.RemotePlayer{r2}, map[byte][3]float32{}, "", now); len(blk.Entries) != 0 {
		t.Errorf("an aboard boat without an anchor: %+v", blk.Entries)
	}
	if _, held := tr.parked[2]; held {
		t.Error("boarding kept the hold")
	}

	// A despawned pose: an aboard boat is not drawn.
	stale := remote(1, "Tetra", aboard, true, now.Add(-time.Minute))
	if blk = tr.Build(all, sea, []network.RemotePlayer{stale}, anchors, "", now); len(blk.Entries) != 0 {
		t.Errorf("a despawned remote's boat: %+v", blk.Entries)
	}

	// Leaving the session drops the hold.
	r2 = remote(2, "Aryll", parked, true, now)
	tr.Build(all, sea, []network.RemotePlayer{r2}, anchors, "", now)
	tr.Build(all, sea, nil, anchors, "", now)
	if len(tr.parked) != 0 {
		t.Errorf("holds after everyone left: %v", tr.parked)
	}

	// Off the sea the room counts (Tower of the Gods); self is skipped;
	// an old sender has no boat; at most BoatMax entries.
	tower := Here{Stage: "Siren", Room: 4, OK: true}
	inRoom := Boat{Present: true, Stage: "Siren", Room: 4}
	other := Boat{Present: true, Stage: "Siren", Room: 7}
	rs := []network.RemotePlayer{
		remote(1, "A", inRoom, true, now), remote(2, "B", other, true, now), remote(3, "me", inRoom, true, now),
		remote(4, "old", Boat{}, false, now), remote(5, "C", inRoom, true, now), remote(6, "D", inRoom, true, now),
		remote(7, "E", inRoom, true, now),
	}
	blk = tr.Build(all, tower, rs, anchors, "me", now)
	if len(blk.Entries) != mailbox.BoatMax {
		t.Errorf("tower entries %d, want %d (A, C, D)", len(blk.Entries), mailbox.BoatMax)
	}
	if _, held := tr.parked[4]; held {
		t.Error("an old sender got a boat")
	}
	// Not in play, or boats off: no entries; nosail clears the flag.
	if blk = tr.Build(all, Here{}, rs, anchors, "", now); len(blk.Entries) != 0 {
		t.Error("entries while not in play")
	}
	if blk = tr.Build(Settings{}, tower, rs, anchors, "", now); len(blk.Entries) != 0 || blk.Flags != 0 {
		t.Errorf("boats off: %+v", blk)
	}
	if blk = tr.Build(ParseSettings("nosail"), tower, rs, anchors, "", now); blk.Flags != mailbox.BoatFParts {
		t.Errorf("nosail flags 0x%X", blk.Flags)
	}
}

func TestSync(t *testing.T) {
	m := mem.NewFakeMem()
	m.Record(true)
	var y Sync
	blk := mailbox.BoatBlock{Flags: mailbox.BoatFSail, Entries: []mailbox.BoatEntry{{Pos: [3]float32{1, 2, 3}}}}
	if err := y.Write(m, blk); err != nil {
		t.Fatal(err)
	}
	w := m.Writes()
	if len(w) != 2 || w[0].Addr != mailbox.BoatAddr+mailbox.BoatEntriesOff || w[1].Addr != mailbox.BoatAddr {
		t.Fatalf("writes %+v: want the entries, then the magic", w)
	}
	if got, _ := mem.ReadU32(m, mailbox.BoatAddr); got != mailbox.BoatMagic {
		t.Errorf("magic 0x%08X", got)
	}
	if fl, _ := m.Read(mailbox.BoatAddr+mailbox.BoatEntriesOff, 1); fl[0] != mailbox.BoatEfShow {
		t.Errorf("entry flags 0x%02X", fl[0])
	}
	m.ResetWrites()
	for i := 0; i < RefreshEvery-1; i++ {
		_ = y.Write(m, blk)
	}
	if len(m.Writes()) != 0 {
		t.Errorf("unchanged block rewritten %d times", len(m.Writes()))
	}
	_ = y.Write(m, blk)
	if len(m.Writes()) != 2 {
		t.Error("the refresh did not rewrite the block")
	}
	if err := y.Off(m); err != nil {
		t.Fatal(err)
	}
	if got, _ := mem.ReadU32(m, mailbox.BoatAddr); got != 0 {
		t.Errorf("magic after Off 0x%08X", got)
	}
	m.ResetWrites()
	_ = y.Write(m, blk)
	if len(m.Writes()) != 2 {
		t.Error("Off did not force the next write")
	}
}
