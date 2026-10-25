// Package boatsync is the session side of the peer boats (inject/include/
// wwboat.h, wwapi/mailbox boat.go, inject/rel/boats.c): each player's King
// of Red Lions, drawn in the other games where they sail it and where they
// left it.
//
// Sender (broadcast-pose): Local reads this game's ship (mpPlayerPtr[2],
// the daShip_c) every tick and Encode appends it to the pose trailer as a
// run of RunBytes after vissync's two equip bytes (offset RunOffset):
//
//	[0] flags  RunPresent, RunAboard, RunMastOn, RunMastHide, RunSailOn
//	[1] mPart  [2] mast frame  [3] head bck (0 = none)  [4] head frame
//	[5] s8 the ship's room
//	[6..18)  f32[3] the ship's position: relative to Link's current.pos
//	         while aboard, absolute while parked
//	[18..24) s16[3] shape_angle
//	[24..32) s16 mSailAngle, m0366 (tiller), part A, part B
//	[32..40) the stage (mCurStage.mName, char[8], NUL padded)
//
// The trailer is then 59 bytes, under network.MaxFaceBytes (64), which the
// server relays as is, so the protocol version is unchanged. A receiver
// from before it reads only the bytes it knows; a sender from before it
// sends 19 bytes, which Decode reads as no boat. A run is always sent
// once the equip bytes are (RunPresent clear when there is no ship), so a
// later run can follow at a fixed offset.
//
// Receiver (puppet-sync): a Tracker turns the remotes' runs into the
// block's entries each tick, Sync writes it when it changes. An aboard
// boat goes where the remote's mini-Link is drawn (the feeder's smoothed
// position) plus the streamed offset, so Link and boat move as one with
// no smoothing of their own; it shows while the remote's pose is live and
// on this game's stage. A parked boat is held where the remote left it,
// on the stage it was on, until the remote boards again, parks elsewhere
// or leaves the session: once the remote walks into another stage its
// poses stop coming (or the server stops relaying them), and the boat
// stays as vanilla leaves it.
package boatsync

import (
	"bytes"
	"encoding/binary"
	"math"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/mods/multiplayer/net"
	"github.com/StephenSHorton/ww-multiplayer/mods/multiplayer/vissync"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mailbox"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mem"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/player"
)

// RunOffset is where the boat run starts in the pose trailer: after the
// 16 face bytes, the appearance byte and the two equip bytes.
const RunOffset = player.FaceBytes + 1 + vissync.TrailerEquipBytes

// RunBytes is the run's length.
const RunBytes = 40

// Run flags (byte 0).
const (
	RunPresent  byte = 0x01 // the sender's game has a ship on its stage
	RunAboard   byte = 0x02 // its Link rides it (daPyStts0_SHIP_RIDE_e)
	RunMastOn   byte = 0x04 // the mast bck is FN_MAST_ON2
	RunMastHide byte = 0x08 // m03E8 < 0.5: the cannon or crane in the mast's place
	RunSailOn   byte = 0x10 // mStateFlag daSFLG_SAIL_ON_e
)

// Run field offsets.
const (
	runFlags     = 0
	runPart      = 1
	runMastFrame = 2
	runHeadBck   = 3
	runHeadFrame = 4
	runRoom      = 5
	runPos       = 6
	runAngle     = 18
	runSail      = 24
	runTiller    = 26
	runPartA     = 28
	runPartB     = 30
	runStage     = 32
)

// daShip_c (zeldaret/tww include/d/actor/d_a_ship.h; d_a_ship.rel is not in
// main.dol, so only the two fields ho_move reads through l_ship, +0x358
// and +0x364, are checked against inject/original.dol: 0x800E9F80,
// 0x800E9CF8). fopAc fields from f_op_actor.h.
const (
	shipPtr      = addr.PlayerPtr + 8 // mpPlayerPtr[2] (d_com_inf_game.h:791: 0 Link, 1 partner, 2 ship), 0x803CA75C
	procShip     = 0x00A7             // fpcNm_SHIP_e (f_pc_name.h:176)
	shipBase     = addr.ActorOffCurPos
	shipRead     = 0x3EC - shipBase // through m03E8
	offRoom      = 0x20A            // current.roomNo
	offShape     = 0x20C            // shape_angle
	offBodyAnm   = 0x298            // mDoExt_McaMorf* mpBodyAnm
	offHeadAnm   = 0x29C            // mpHeadAnm
	offPart      = 0x34E            // u8 mPart
	offStateFlag = 0x358            // u32 mStateFlag
	offSail      = 0x364            // s16 mSailAngle
	offTiller    = 0x366            // s16 m0366
	offMastBck   = 0x392            // u16 m0392, the mast bck's "Ship" index
	offCannonYaw = 0x394            // s16 m0394
	offCannonPit = 0x396            // s16 m0396
	offCrane     = 0x398            // s16 m0398 (CraneAngle)
	offCraneSwng = 0x39C            // s16 m039C
	offHeadBck   = 0x3B4            // u16 m03B4, the head bck's "Ship" index
	offMastScale = 0x3E8            // f32 m03E8: 1, or 0.001 with a part out
	morfFrame    = 0x68             // mDoExt_McaMorf mFrameCtrl (+0x58) .mFrame (+0x10)
	sflgSailOn   = 0x200            // daSFLG_SAIL_ON_e
	bckMastOn    = 0x0B             // dRes_INDEX_SHIP_BCK_FN_MAST_ON2_e (res/Object/Ship.h)
	bckFirst     = 0x05             // AKIBI1
	bckLast      = 0x0E             // KYAKKAN1
	bckMastOff   = 0x0A
	partCannon   = 2 // daShip_c::PART_CANNON_e
	partCrane    = 3 // PART_CRANE_e
	// playerStatus0 is g_dComIfG_gameInfo.play.mPlayerStatus[0][0]
	// (0x803C4C08 + 0x12A0 + 0x4A28), the word inject/rel/visuals.c reads too.
	playerStatus0 uint32 = 0x803CA8D0
	sttsShipRide  uint32 = 0x00010000 // daPyStts0_SHIP_RIDE_e (d_com_inf_game.h:53)
)

// Boat is one game's ship as the run carries it.
type Boat struct {
	Present, Aboard, MastOn, MastHide, SailOn bool
	Part, MastFrame, HeadBck, HeadFrame       uint8
	Room                                      int8
	// Pos is relative to the sender's Link while Aboard, else absolute.
	Pos               [3]float32
	Angle             [3]int16
	SailAngle, Tiller int16
	PartA, PartB      int16
	Stage             string
}

// Local reads this game's ship. link is Link's current.pos, the position
// broadcast-pose sends with this pose (an aboard ship is sent relative to
// it). ok is false when there is no ship to send: none on this stage, a
// stage change under way, or a read failed.
func Local(m mem.Reader, link [3]float32) (b Boat, ok bool) {
	if ns, err := mem.ReadS8(m, addr.NextStageEnable); err != nil || ns != 0 {
		return b, false
	}
	ship, err := mem.ReadU32(m, shipPtr)
	if err != nil || !addr.InMEM1(ship) {
		return b, false
	}
	if pn, err := mem.ReadU16(m, ship+addr.ActorOffProcName); err != nil || pn != procShip {
		return b, false
	}
	d, err := m.Read(ship+shipBase, shipRead)
	if err != nil || len(d) != shipRead {
		return b, false
	}
	st, err := m.Read(addr.CurStageName, addr.StageNameLen)
	if err != nil {
		return b, false
	}
	f := func(off uint32) []byte { return d[off-shipBase:] }
	be := binary.BigEndian
	s16 := func(off uint32) int16 { return int16(be.Uint16(f(off))) }
	b.Present = true
	if sts, err := mem.ReadU32(m, playerStatus0); err == nil && sts&sttsShipRide != 0 {
		b.Aboard = true
	}
	for i := range b.Pos {
		b.Pos[i] = math.Float32frombits(be.Uint32(f(shipBase + uint32(4*i))))
		if b.Aboard {
			b.Pos[i] -= link[i]
		}
		if !finite(b.Pos[i]) {
			return Boat{}, false
		}
	}
	for i := range b.Angle {
		b.Angle[i] = s16(offShape + uint32(2*i))
	}
	b.Room = int8(f(offRoom)[0])
	b.Part = f(offPart)[0]
	b.SailOn = be.Uint32(f(offStateFlag))&sflgSailOn != 0
	b.SailAngle, b.Tiller = s16(offSail), s16(offTiller)
	b.MastOn = be.Uint16(f(offMastBck)) == bckMastOn
	b.MastHide = math.Float32frombits(be.Uint32(f(offMastScale))) < 0.5
	switch b.Part {
	case partCannon:
		b.PartA, b.PartB = s16(offCannonYaw), s16(offCannonPit)
	case partCrane:
		b.PartA = s16(offCrane) + s16(offCraneSwng)
	}
	if hb := be.Uint16(f(offHeadBck)); hb >= bckFirst && hb <= bckLast && hb != bckMastOff && hb != bckMastOn {
		b.HeadBck = uint8(hb)
	}
	b.MastFrame = morfFrameByte(m, be.Uint32(f(offBodyAnm)))
	b.HeadFrame = morfFrameByte(m, be.Uint32(f(offHeadAnm)))
	b.Stage = cString(st)
	return b, true
}

// morfFrameByte is a morf's frame rounded to a whole frame, 0..255 (a
// finished bck sits at end - 0.001 and must arrive as the end frame).
func morfFrameByte(m mem.Reader, morf uint32) uint8 {
	if !addr.InMEM1(morf) {
		return 0
	}
	v, err := mem.ReadF32(m, morf+morfFrame)
	if err != nil || !finite(v) || v < 0 {
		return 0
	}
	if v > 255 {
		return 255
	}
	return uint8(math.Round(float64(v)))
}

func finite(v float32) bool {
	return !math.IsNaN(float64(v)) && !math.IsInf(float64(v), 0) && v > -1e6 && v < 1e6
}

func cString(b []byte) string {
	if i := bytes.IndexByte(b, 0); i >= 0 {
		b = b[:i]
	}
	return string(b)
}

// Encode is the run for b (ok false: an all-zero run, no boat).
func Encode(b Boat, ok bool) []byte {
	out := make([]byte, RunBytes)
	if !ok || !b.Present {
		return out
	}
	be := binary.BigEndian
	f := RunPresent
	for _, x := range []struct {
		on  bool
		bit byte
	}{{b.Aboard, RunAboard}, {b.MastOn, RunMastOn}, {b.MastHide, RunMastHide}, {b.SailOn, RunSailOn}} {
		if x.on {
			f |= x.bit
		}
	}
	out[runFlags], out[runPart], out[runMastFrame], out[runHeadBck], out[runHeadFrame] = f, b.Part, b.MastFrame, b.HeadBck, b.HeadFrame
	out[runRoom] = byte(b.Room)
	for i, v := range b.Pos {
		be.PutUint32(out[runPos+4*i:], math.Float32bits(v))
	}
	for i, v := range b.Angle {
		be.PutUint16(out[runAngle+2*i:], uint16(v))
	}
	be.PutUint16(out[runSail:], uint16(b.SailAngle))
	be.PutUint16(out[runTiller:], uint16(b.Tiller))
	be.PutUint16(out[runPartA:], uint16(b.PartA))
	be.PutUint16(out[runPartB:], uint16(b.PartB))
	copy(out[runStage:runStage+addr.StageNameLen-1], b.Stage) // NUL-terminated within the 8
	return out
}

// Decode reads the run in a pose trailer. ok is false for a trailer
// without one (an older sender); a run without RunPresent decodes with
// Present false.
func Decode(face []byte) (b Boat, ok bool) {
	if len(face) < RunOffset+RunBytes {
		return b, false
	}
	r := face[RunOffset : RunOffset+RunBytes]
	be := binary.BigEndian
	f := r[runFlags]
	if f&RunPresent == 0 {
		return Boat{}, true
	}
	b.Present, b.Aboard, b.MastOn, b.MastHide, b.SailOn = true, f&RunAboard != 0, f&RunMastOn != 0, f&RunMastHide != 0, f&RunSailOn != 0
	b.Part, b.MastFrame, b.HeadBck, b.HeadFrame = r[runPart], r[runMastFrame], r[runHeadBck], r[runHeadFrame]
	b.Room = int8(r[runRoom])
	for i := range b.Pos {
		b.Pos[i] = math.Float32frombits(be.Uint32(r[runPos+4*i:]))
		if !finite(b.Pos[i]) {
			return Boat{}, true
		}
	}
	for i := range b.Angle {
		b.Angle[i] = int16(be.Uint16(r[runAngle+2*i:]))
	}
	b.SailAngle, b.Tiller = int16(be.Uint16(r[runSail:])), int16(be.Uint16(r[runTiller:]))
	b.PartA, b.PartB = int16(be.Uint16(r[runPartA:])), int16(be.Uint16(r[runPartB:]))
	b.Stage = cString(r[runStage : runStage+addr.StageNameLen])
	return b, true
}

// Settings picks what the REL draws. The zero value draws nothing.
type Settings struct {
	On, Sail, Parts bool
}

// SettingsFromEnv reads WW_BOATS: unset or "on" draws everything; off, 0,
// false, no or none draws no boat; otherwise a list of nosail (the hull
// without the sail cloth) and noparts (no cannon or crane) joined by "," or
// "+".
func SettingsFromEnv() Settings {
	return ParseSettings(os.Getenv("WW_BOATS"))
}

// ParseSettings is SettingsFromEnv's parser.
func ParseSettings(v string) Settings {
	s := Settings{On: true, Sail: true, Parts: true}
	for _, w := range strings.FieldsFunc(strings.ToLower(v), func(r rune) bool { return r == ',' || r == '+' || r == ' ' }) {
		switch w {
		case "off", "0", "false", "no", "none":
			return Settings{}
		case "nosail":
			s.Sail = false
		case "noparts":
			s.Parts = false
		}
	}
	return s
}

// Here is this game's place: its stage and room (mStayNo).
type Here struct {
	Stage string
	Room  int8
	OK    bool // in play: Link loaded, no stage change under way
}

// ReadHere reads this game's place.
func ReadHere(m mem.Reader) Here {
	link, err := mem.ReadU32(m, addr.PlayerPtr)
	if err != nil || !addr.InMEM1(link) {
		return Here{}
	}
	if ns, err := mem.ReadS8(m, addr.NextStageEnable); err != nil || ns != 0 {
		return Here{}
	}
	st, err := m.Read(addr.CurStageName, addr.StageNameLen)
	if err != nil {
		return Here{}
	}
	room, err := mem.ReadS8(m, addr.StayNo)
	if err != nil {
		return Here{}
	}
	s := cString(st)
	return Here{Stage: s, Room: room, OK: s != "" && s != addr.StageTitle && s != addr.StageFileSelect}
}

// seaStage is the Great Sea, whose rooms are its grid squares: a boat on
// it shows in any room.
const seaStage = "sea"

// Tracker holds the parked boats between ticks. The zero value is ready.
type Tracker struct {
	parked map[byte]Boat
}

// Build is the block for this tick. smoothed maps a remote's id to where
// its mini-Link is drawn (the feeder's RemoteView.Pos); an aboard boat is
// placed from it, so a remote without one shows no aboard boat. Remotes
// named self are skipped; at most mailbox.BoatMax entries, in id order.
func (t *Tracker) Build(s Settings, here Here, remotes []network.RemotePlayer, smoothed map[byte][3]float32,
	self string, now time.Time) mailbox.BoatBlock {
	var blk mailbox.BoatBlock
	if s.Sail {
		blk.Flags |= mailbox.BoatFSail
	}
	if s.Parts {
		blk.Flags |= mailbox.BoatFParts
	}
	if t.parked == nil {
		t.parked = map[byte]Boat{}
	}
	sorted := append([]network.RemotePlayer(nil), remotes...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].ID < sorted[j].ID })
	seen := map[byte]bool{}
	freezeAfter, despawnAfter := network.PoseLivenessDefaults()
	for i := range sorted {
		rp := &sorted[i]
		if self != "" && rp.Name == self {
			continue
		}
		seen[rp.ID] = true
		b, ok := Decode(rp.FaceState)
		if !ok {
			continue
		}
		_, despawned := network.PoseLiveness(rp.LastPose, now, freezeAfter, despawnAfter)
		if b.Present && !despawned {
			if b.Aboard {
				delete(t.parked, rp.ID)
			} else {
				t.parked[rp.ID] = b
			}
		}
		if !s.On || !here.OK {
			continue
		}
		var e mailbox.BoatEntry
		switch {
		case b.Present && b.Aboard && !despawned:
			at, ok := smoothed[rp.ID]
			if !ok || b.Stage != here.Stage || !inRoom(b, here) {
				continue
			}
			e = entry(b)
			for k := range e.Pos {
				e.Pos[k] = at[k] + b.Pos[k]
			}
		default:
			p, ok := t.parked[rp.ID]
			if !ok || p.Stage != here.Stage || !inRoom(p, here) {
				continue
			}
			e = entry(p)
			e.Parked = true
		}
		if rp.HasColor {
			e.R, e.G, e.B = rp.Color.R, rp.Color.G, rp.Color.B
		}
		if len(blk.Entries) < mailbox.BoatMax {
			blk.Entries = append(blk.Entries, e)
		}
	}
	for id := range t.parked {
		if !seen[id] {
			delete(t.parked, id) // left the session
		}
	}
	return blk
}

// inRoom: on the sea any room; elsewhere (Tower of the Gods) only the
// room this game stands in, whose geometry is loaded.
func inRoom(b Boat, here Here) bool {
	return here.Stage == seaStage || b.Room < 0 || b.Room == here.Room
}

func entry(b Boat) mailbox.BoatEntry {
	return mailbox.BoatEntry{
		MastOn: b.MastOn, MastHide: b.MastHide, SailOn: b.SailOn,
		Part: b.Part, MastFrame: b.MastFrame, HeadBck: b.HeadBck, HeadFrame: b.HeadFrame,
		Pos: b.Pos, Angle: b.Angle, SailAngle: b.SailAngle, Tiller: b.Tiller, PartA: b.PartA, PartB: b.PartB,
	}
}

// Writer is the memory Sync writes.
type Writer interface {
	Write(addr uint32, data []byte) error
}

// RefreshEvery is how many unchanged writes pass before the block is
// written again anyway (a save state load or a soft reset puts other
// bytes in RAM behind Go's back).
const RefreshEvery = 60

// Sync writes the block when it changes: the entries first, then the
// magic and flags, so the REL never pairs a fresh magic with stale
// entries. Off writes the magic 0 (the REL then frees every boat).
type Sync struct {
	last  []byte
	since int
}

// Write writes b if it differs from the last block written (or the
// refresh is due).
func (y *Sync) Write(w Writer, b mailbox.BoatBlock) error {
	entries, hdr := b.EntryBytes(), b.Header()
	data := append(append([]byte(nil), hdr...), entries...)
	y.since++
	if bytes.Equal(data, y.last) && y.since < RefreshEvery {
		return nil
	}
	y.since = 0
	if err := w.Write(mailbox.BoatAddr+mailbox.BoatEntriesOff, entries); err != nil {
		y.last = nil
		return err
	}
	if err := w.Write(mailbox.BoatAddr+mailbox.BoatMagicOff, hdr); err != nil {
		y.last = nil
		return err
	}
	y.last = data
	return nil
}

// Off turns the block off.
func (y *Sync) Off(w Writer) error {
	y.last = nil
	return w.Write(mailbox.BoatAddr+mailbox.BoatMagicOff, []byte{0, 0, 0, 0})
}
