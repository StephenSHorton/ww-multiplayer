// Package vissync is the session side of the remote players' visuals
// block (inject/include/wwvis.h, wwapi/mailbox vis.go): what the REL draws
// on and around the mini-Links (sword, shield and scabbard, name tags) and
// on the pause menu's sea chart. The sender's part is the pose trailer:
// LocalEquip reads what broadcast-pose appends after the appearance byte;
// the receiver's part is Build (a pure function of the session) and Sync
// (writes it when it changes).
//
// Pose trailer (docs/09 §5.4, extended here): [16 face bytes][appearance]
// [mSelectEquip[0]][mSelectEquip[1]]. The appearance byte's bit 0 is the
// casual clothes (tunicsync), bit 1 the sender is on the sea stage, bit 2
// its sword is out, bit 3 its shield is in its right hand. A sender
// before this sends 17 bytes (or 16), and every receiver reads only the
// bytes it knows, so the protocol version is unchanged; the server relays
// the trailer as is up to network.MaxFaceBytes (64).
package vissync

import (
	"bytes"
	"os"
	"sort"
	"strings"

	"github.com/StephenSHorton/ww-multiplayer/mods/multiplayer/net"
	"github.com/StephenSHorton/ww-multiplayer/mods/multiplayer/tunicsync"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mailbox"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mem"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/player"
)

// Appearance bits past tunicsync's casual bit (bit 0).
const (
	AppSea        byte = 0x02
	AppSwordHand  byte = 0x04
	AppShieldHand byte = 0x08
)

// TrailerEquipBytes is how many bytes follow the appearance byte.
const TrailerEquipBytes = 2

// daPy_lk_c fields (zeldaret/tww include/d/actor/d_a_player_main.h) and
// the joint the shield sits on in hand (assets/GZLE01/res/Object/Link.h).
const (
	offEquippedShieldModel = 0x0980 // J3DModel* mpEquippedShieldModel (:2028)
	offCLModel             = 0x032C // J3DModel* mpCLModel (wwapi.h DAPY_LK_C_MPCLMODEL_OFFSET)
	offEquipItem           = 0x3560 // u16 mEquipItem (:2245)
	itemSword              = 0x103  // daPyItem_SWORD_e (:934)
	j3dModelBaseMtx        = 0x24   // J3DModel::mBaseTransformMtx
	j3dModelNodeMtx        = 0x8C   // J3DModel::mpNodeMtx (wwapi.h J3DMODEL_MP_NODE_MTX_OFFSET)
	jointRHandA            = 0x0C   // CL_JNT_CL_RHANDA_e
	mtxBytes               = 0x30
)

// LocalEquip is one raw sample of what broadcast-pose adds to its
// trailer: the appearance bits AppSea / AppSwordHand / AppShieldHand and
// the two equip bytes. ok is false while Link isn't loaded. broadcast-pose
// sends it through an EquipSampler, which debounces AppShieldHand.
func LocalEquip(m mem.Reader) (app byte, equip [TrailerEquipBytes]byte, ok bool) {
	link, err := mem.ReadU32(m, addr.PlayerPtr)
	if err != nil || !addr.InMEM1(link) {
		return 0, equip, false
	}
	sel, err := m.Read(addr.SelectEquip, TrailerEquipBytes)
	if err != nil || len(sel) != TrailerEquipBytes {
		return 0, equip, false
	}
	copy(equip[:], sel)
	if st, err := m.Read(addr.CurStageName, addr.StageNameLen); err == nil && stageName(st) == seaStage {
		app |= AppSea
	}
	if it, err := mem.ReadU16(m, link+offEquipItem); err == nil && it == itemSword {
		app |= AppSwordHand
	}
	if shieldInHand(m, link) {
		app |= AppShieldHand
	}
	return app, equip, true
}

// EquipSampler is LocalEquip with AppShieldHand debounced: the shield's
// matrix is read while the game may be mid-calc, so one sample can catch
// the base matrix and the hand joint equal (or apart) for a frame they are
// not. The bit changes only when two consecutive samples agree on the new
// value, so a single torn read never flips the remote's shield. The zero
// value is ready; a sample without Link (ok false) starts it over.
type EquipSampler struct {
	last, shield, primed bool
}

// Sample is LocalEquip with the shield bit debounced.
func (e *EquipSampler) Sample(m mem.Reader) (app byte, equip [TrailerEquipBytes]byte, ok bool) {
	app, equip, ok = LocalEquip(m)
	if !ok {
		*e = EquipSampler{}
		return app, equip, ok
	}
	raw := app&AppShieldHand != 0
	if e.primed && raw == e.last {
		e.shield = raw
	}
	e.last, e.primed = raw, true
	app &^= AppShieldHand
	if e.shield {
		app |= AppShieldHand
	}
	return app, equip, ok
}

// shieldInHand: Link puts the shield's base matrix at the right hand's
// joint matrix exactly (setBaseTRMtx(rHandA_jnt_mtx), d_a_player_main.cpp
// :1310), so the two read equal byte for byte.
func shieldInHand(m mem.Reader, link uint32) bool {
	sh, err := mem.ReadU32(m, link+offEquippedShieldModel)
	if err != nil || !addr.InMEM1(sh) {
		return false
	}
	cl, err := mem.ReadU32(m, link+offCLModel)
	if err != nil || !addr.InMEM1(cl) {
		return false
	}
	node, err := mem.ReadU32(m, cl+j3dModelNodeMtx)
	if err != nil || !addr.InMEM1(node) {
		return false
	}
	a, err1 := m.Read(sh+j3dModelBaseMtx, mtxBytes)
	b, err2 := m.Read(node+jointRHandA*mtxBytes, mtxBytes)
	return err1 == nil && err2 == nil && len(a) == mtxBytes && bytes.Equal(a, b)
}

func stageName(b []byte) string {
	if i := bytes.IndexByte(b, 0); i >= 0 {
		b = b[:i]
	}
	return string(b)
}

// RemoteEquip is a remote's equip entry from its pose trailer; Valid is
// false for a sender without the equip bytes.
func RemoteEquip(face []byte) mailbox.VisEquip {
	if len(face) < player.FaceBytes+1+TrailerEquipBytes {
		return mailbox.VisEquip{}
	}
	app := face[player.FaceBytes]
	return mailbox.VisEquip{
		Valid:      true,
		Sword:      face[player.FaceBytes+1],
		Shield:     face[player.FaceBytes+2],
		SwordHand:  app&AppSwordHand != 0,
		ShieldHand: app&AppShieldHand != 0,
	}
}

// remoteOnSea reports whether rp is on the sea stage: by the place its
// last position carried (network place.go), else by its pose trailer's
// sea bit. The place comes first because relay scoping stops a remote's
// poses while it is in another stage than this game, so the trailer then
// keeps the bit of the last pose seen here; the position, which the
// server relays to everyone, keeps the marker live while this game is
// indoors and the remote sails.
func remoteOnSea(rp *network.RemotePlayer) bool {
	if rp.HasPlace {
		return rp.Place.Stage == seaStage
	}
	return RemoteOnSea(rp.FaceState)
}

// seaStage is the Great Sea's stage name (LocalEquip's AppSea test).
const seaStage = "sea"

// RemoteOnSea reports the trailer's sea bit.
func RemoteOnSea(face []byte) bool {
	return len(face) > player.FaceBytes && face[player.FaceBytes]&AppSea != 0
}

// Settings picks what the REL draws. The zero value draws nothing.
// Hair is a casual remote's katsura hair (its hat and buckle hidden).
type Settings struct {
	Names, Equip, SeaChart, Hair bool
}

// SettingsFromEnv is every visual on, less those turned off:
// WW_NAMETAGS=off, WW_EQUIP=off, WW_SEACHART=off, WW_HAIR=off (also 0,
// false, no).
func SettingsFromEnv() Settings {
	return Settings{Names: !envOff("WW_NAMETAGS"), Equip: !envOff("WW_EQUIP"), SeaChart: !envOff("WW_SEACHART"),
		Hair: !envOff("WW_HAIR")}
}

func envOff(k string) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(k))) {
	case "off", "0", "false", "no", "none":
		return true
	}
	return false
}

// Build is the block for this tick: the names and equip of the remotes
// drawn as mini-Links (linkSlot, the feeder's remote ID -> slot), and a
// sea chart marker for each remote whose trailer says it is on the sea
// stage, in ID order, at most mailbox.VisSeaMax, in its tunic color
// (white without a server color). Remotes named self (puppet-sync's
// self-echo filter) are skipped.
func Build(s Settings, remotes []network.RemotePlayer, linkSlot map[byte]int, self string) mailbox.VisBlock {
	var b mailbox.VisBlock
	if s.Names {
		b.Flags |= mailbox.VisFlagNames
	}
	if s.Equip {
		b.Flags |= mailbox.VisFlagEquip
	}
	if s.SeaChart {
		b.Flags |= mailbox.VisFlagSea
	}
	if s.Hair {
		b.Flags |= mailbox.VisFlagHair
	}
	sorted := append([]network.RemotePlayer(nil), remotes...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].ID < sorted[j].ID })
	for i := range sorted {
		rp := &sorted[i]
		if self != "" && rp.Name == self {
			continue
		}
		if slot, ok := linkSlot[rp.ID]; ok && slot >= 0 && slot < mailbox.VisSlots {
			b.Names[slot] = rp.Name
			b.Equip[slot] = RemoteEquip(rp.FaceState)
			b.Equip[slot].Casual = tunicsync.RemoteCasual(rp.FaceState)
		}
		if s.SeaChart && rp.Position != nil && remoteOnSea(rp) && len(b.Sea) < mailbox.VisSeaMax {
			e := mailbox.VisSea{X: rp.Position.PosX, Z: rp.Position.PosZ, Angle: rp.Position.RotY, R: 0xFF, G: 0xFF, B: 0xFF}
			if rp.HasColor {
				e.R, e.G, e.B = rp.Color.R, rp.Color.G, rp.Color.B
			}
			b.Sea = append(b.Sea, e)
		}
	}
	return b
}

// Writer is the memory Sync writes.
type Writer interface {
	Write(addr uint32, data []byte) error
}

// RefreshEvery is how many unchanged Writes pass before the block is
// written again anyway (about a second at puppet-sync's 60 Hz): a save
// state load or a soft reset puts other bytes in RAM behind Go's back.
const RefreshEvery = 60

// Sync writes the block when its bytes change, after a failed write and
// every RefreshEvery calls. Off writes the magic 0, so the REL draws none
// of it.
type Sync struct {
	last  []byte
	since int
}

// Write writes b if it differs from the last block written (or the
// refresh is due).
func (y *Sync) Write(w Writer, b mailbox.VisBlock) error {
	data := b.Bytes()
	y.since++
	if bytes.Equal(data, y.last) && y.since < RefreshEvery {
		return nil
	}
	y.since = 0
	if err := w.Write(mailbox.VisAddr+mailbox.VisGoStart, data); err != nil {
		y.last = nil
		return err
	}
	y.last = data
	return nil
}

// Off turns the block off (session end).
func (y *Sync) Off(w Writer) error {
	y.last = nil
	return w.Write(mailbox.VisAddr+mailbox.VisMagicOff, []byte{0, 0, 0, 0})
}
