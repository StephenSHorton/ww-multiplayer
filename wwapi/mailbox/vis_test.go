package mailbox

import (
	"encoding/binary"
	"fmt"
	"os"
	"strings"
	"testing"
)

// wwvisHeader is the C source of the visuals block constants.
const wwvisHeader = "../../inject/include/wwvis.h"

// TestVisHeaderPins: every Go constant equals its WWVIS_* define, and
// every define has a Go constant.
func TestVisHeaderPins(t *testing.T) {
	pinHeader(t, wwvisHeader, headerDefines(t, wwvisHeader), map[string]uint64{
		"WWVIS_OFF": uint64(VisOff), "WWVIS_SIZE": uint64(VisSize),
		"WWVIS_DRAW_FN_OFF": VisDrawFnOff, "WWVIS_SEACHART_FN_OFF": VisSeachartFnOff,
		"WWVIS_MAGIC_OFF": VisMagicOff, "WWVIS_FLAGS_OFF": VisFlagsOff, "WWVIS_NAMES_OFF": VisNamesOff,
		"WWVIS_EQUIP_OFF": VisEquipOff, "WWVIS_SEA_OFF": VisSeaOff, "WWVIS_MODELS_OFF": VisModelsOff,
		"WWVIS_STAT_TAGS_OFF": VisStatTagsOff, "WWVIS_STAT_EQUIP_OFF": VisStatEquipOff,
		"WWVIS_STAT_SEA_OFF": VisStatSeaOff, "WWVIS_STAT_ERR_OFF": VisStatErrOff, "WWVIS_MODELS2_OFF": VisModels2Off,
		"WWVIS_MAGIC": uint64(VisMagic), "WWVIS_SLOTS": VisSlots, "WWVIS_NAME_BYTES": VisNameBytes,
		"WWVIS_FLAG_NAMES": uint64(VisFlagNames), "WWVIS_FLAG_EQUIP": uint64(VisFlagEquip), "WWVIS_FLAG_SEACHART": uint64(VisFlagSea),
		"WWVIS_FLAG_HAIR": uint64(VisFlagHair), "WWVIS_EQF_CASUAL": VisEqfCasual,
		"WWVIS_EQ_SWORD": VisEqSword, "WWVIS_EQ_SHIELD": VisEqShield, "WWVIS_EQ_FLAGS": VisEqFlags,
		"WWVIS_EQF_VALID": VisEqfValid, "WWVIS_EQF_SWORD_HAND": VisEqfSwordHand, "WWVIS_EQF_SHIELD_HAND": VisEqfShieldHand,
		"WWVIS_SEA_MAX": VisSeaMax, "WWVIS_SEA_SIZE": VisSeaSize,
		"WWVIS_SE_X": VisSeX, "WWVIS_SE_Z": VisSeZ, "WWVIS_SE_ANGLE": VisSeAngle,
		"WWVIS_SE_R": VisSeR, "WWVIS_SE_G": VisSeG, "WWVIS_SE_B": VisSeB, "WWVIS_SE_FLAGS": VisSeFlags, "WWVIS_SEF_SHOW": VisSefShow,
		"WWVIS_MODEL_KINDS": VisModelKinds,
		"WWVIS_MK_GRIP_A":   VisMkGripA, "WWVIS_MK_GRIP_MS": VisMkGripMS, "WWVIS_MK_BLADE_A": VisMkBladeA,
		"WWVIS_MK_BLADE_MS": VisMkBladeMS, "WWVIS_MK_SHIELD_A": VisMkShieldA,
		"WWVIS_MODEL2_KINDS": VisModel2Kinds, "WWVIS_MK_SHIELD_MS": VisMkShieldMS, "WWVIS_MK_POD_MS": VisMkPodMS,
		"WWVIS_MK_KATSURA": VisMkKatsura, "WWVIS_ERR_SKIN": VisErrSkin,
		"WWVIS_ERR_NONE": VisErrNone, "WWVIS_ERR_RES": VisErrRes, "WWVIS_ERR_CREATE": VisErrCreate, "WWVIS_ERR_FONT": VisErrFont,
		"WWVIS_PHASE_PRE": VisPhasePre, "WWVIS_PHASE_POST": VisPhasePost,
	})
}

// TestVisLayout: the block follows the refresh request, its parts tile it
// in order, the slots match the mini-Link slots, and the high canary
// starts at its end.
func TestVisLayout(t *testing.T) {
	if VisOff != RefreshOff+RefreshSize || VisAddr != 0x804137D0 || VisOff+VisSize > RefreshSlackEnd {
		t.Fatalf("visuals block at +0x%X (0x%08X), size 0x%X", VisOff, VisAddr, VisSize)
	}
	if VisNamesOff+VisSlots*VisNameBytes != VisEquipOff || VisEquipOff+VisSlots*4 != VisSeaOff ||
		VisSeaOff+VisSeaMax*VisSeaSize != VisModelsOff || VisModelsOff+VisSlots*VisModelKinds*4 != VisStatTagsOff ||
		VisStatErrOff+4 != VisModels2Off || VisModels2Off+VisSlots*VisModel2Kinds*4 != int(VisSize) ||
		VisMkKatsura+1 != VisModelKinds+VisModel2Kinds {
		t.Error("visuals block parts do not tile it")
	}
	if VisSlots != MailboxPoseSlotCap {
		t.Errorf("VisSlots %d, mini-Link slots %d", VisSlots, MailboxPoseSlotCap)
	}
	if BoatRegion.Start != VisRegion.End || CanaryArenaHigh.Contains(VisAddr, 4) || CanaryArena.Contains(VisAddr, 4) || CanaryArenaHigh.Contains(VisAddr+VisSize-4, 4) {
		t.Errorf("canaries cover the visuals block: high %+v, block %+v", CanaryArenaHigh, VisRegion)
	}
	// T2's seachart_shim reaches the sea chart word with lis 0x8041 and a
	// positive 16-bit offset.
	if w := VisAddr + VisSeachartFnOff; w>>16 != 0x8041 || w&0x8000 != 0 {
		t.Errorf("sea chart word 0x%08X not reachable from lis 0x8041", w)
	}
}

// TestVisBlockBytes encodes names, equip and sea entries at their offsets.
func TestVisBlockBytes(t *testing.T) {
	b := VisBlock{
		Flags: VisFlagNames | VisFlagEquip,
		Names: [VisSlots]string{"Link", "Tetra\x01é is a very long name here"},
		Equip: [VisSlots]VisEquip{{Valid: true, Sword: 0x38, Shield: 0x3B, SwordHand: true}, {Casual: true}},
		Sea:   []VisSea{{X: 1, Z: -2, Angle: -1, R: 1, G: 2, B: 3}, {}, {}, {}, {X: 9}},
	}
	out := b.Bytes()
	if len(out) != VisGoEnd-VisGoStart {
		t.Fatalf("len %d", len(out))
	}
	at := func(off int) []byte { return out[off-VisGoStart:] }
	if binary.BigEndian.Uint32(at(VisMagicOff)) != VisMagic || binary.BigEndian.Uint32(at(VisFlagsOff)) != 3 {
		t.Error("magic/flags")
	}
	if string(at(VisNamesOff)[:5]) != "Link\x00" {
		t.Errorf("name 0 %q", at(VisNamesOff)[:5])
	}
	n1 := at(VisNamesOff + VisNameBytes)[:VisNameBytes]
	if string(n1[:7]) != "Tetra??" || n1[VisNameBytes-1] != 0 {
		t.Errorf("name 1 %q", n1)
	}
	if e := at(VisEquipOff)[:4]; e[0] != 0x38 || e[1] != 0x3B || e[2] != VisEqfValid|VisEqfSwordHand {
		t.Errorf("equip 0 % x", e)
	}
	if e := at(VisEquipOff + 4)[:4]; e[0] != 0xFF || e[1] != 0xFF || e[2] != VisEqfCasual {
		t.Errorf("equip 1 % x", e)
	}
	s := at(VisSeaOff)
	if binary.BigEndian.Uint32(s[VisSeX:]) != 0x3F800000 || binary.BigEndian.Uint16(s[VisSeAngle:]) != 0xFFFF ||
		s[VisSeR] != 1 || s[VisSeB] != 3 || s[VisSeFlags] != VisSefShow {
		t.Errorf("sea 0 % x", s[:VisSeaSize])
	}
	if len(out) != VisSeaOff+VisSeaMax*VisSeaSize-VisGoStart {
		t.Error("a fifth sea entry was written")
	}
}

// TestVisSlotsCoverRemoteLinks: every mini-Link slot T2 hands the REL's
// hook has a names, equip and models row, the Go side of minilink.c's
// _Static_assert(WWVIS_SLOTS >= MAX_REMOTE_LINKS); vissync writes rows up
// to VisSlots for the remotes the feeder puts in those slots.
func TestVisSlotsCoverRemoteLinks(t *testing.T) {
	if VisSlots < MaxRemoteLinks {
		t.Fatalf("VisSlots %d < MaxRemoteLinks %d: a remote Link would draw without its row", VisSlots, MaxRemoteLinks)
	}
	if VisSlots < MailboxPoseSlotCap {
		t.Fatalf("VisSlots %d < MailboxPoseSlotCap %d", VisSlots, MailboxPoseSlotCap)
	}
}

// TestVisSlackMapComments: the slack maps in wwremote.h and wwrefresh.h
// name the visuals block's real end, so nobody reads its tail as free
// (gems3 grew it to +0x9A8 and both headers still said +0x990); the free
// span ends where CanaryArenaHigh does.
func TestVisSlackMapComments(t *testing.T) {
	end := fmt.Sprintf("+0x%X", VisOff+VisSize)
	block := fmt.Sprintf("+0x%X..%s", VisOff, end)
	free := fmt.Sprintf("+0x%X", BoatOff+BoatSize) // the peer boats block follows the visuals block
	for _, c := range []struct{ path, want string }{
		{"../../inject/include/wwremote.h", block},
		{"../../inject/include/wwremote.h", fmt.Sprintf("%s..+0x%X free", free, CanaryArenaHigh.End-Mailbox)},
		{"../../inject/include/wwrefresh.h", block},
		{"../../inject/include/wwrefresh.h", "starts past that (" + free + ")"},
	} {
		b, err := os.ReadFile(c.path)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(b), c.want) {
			t.Errorf("%s: slack map does not say %q", c.path, c.want)
		}
	}
}
