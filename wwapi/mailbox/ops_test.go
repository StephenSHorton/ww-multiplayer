package mailbox

import "testing"

// wwopsHeader is the C source of the API region constants.
const wwopsHeader = "../../inject/include/wwops.h"

// TestOpsHeaderPins: every Go constant equals its WWAP_* define, and every
// define has a Go constant.
func TestOpsHeaderPins(t *testing.T) {
	pinHeader(t, wwopsHeader, headerDefines(t, wwopsHeader), map[string]uint64{
		"WWAP_ADDR": uint64(OpsAddr), "WWAP_SIZE": uint64(OpsSize), "WWAP_MAGIC": uint64(OpsMagic), "WWAP_LAYOUT": OpsLayout,
		"WWAP_MAGIC_OFF": OpsMagicOff, "WWAP_LAYOUT_OFF": OpsLayoutOff, "WWAP_DIR_COUNT_OFF": OpsDirCountOff,
		"WWAP_DIR_OFF": OpsDirOff, "WWAP_DIR_MAX": OpsDirMax, "WWAP_DIR_ENTRY_SIZE": OpsDirEntrySize,
		"WWAP_STAT_DONE_OFF": OpsStatDoneOff, "WWAP_STAT_REFUSED_OFF": OpsStatRefusedOff, "WWAP_STAT_BADCHECK_OFF": OpsStatBadOff,
		"WWAP_STAT_UNKNOWN_OFF": OpsStatUnknownOff, "WWAP_STAT_EVDROP_OFF": OpsStatEvDropOff,
		"WWAP_EV_WRITE_OFF": OpsEvWriteOff, "WWAP_EV_READ_OFF": OpsEvReadOff, "WWAP_EV_RING_OFF": OpsEvRingOff,
		"WWAP_CMD_HEAD_OFF": OpsCmdHeadOff, "WWAP_CMD_TAIL_OFF": OpsCmdTailOff, "WWAP_CMD_RING_OFF": OpsCmdRingOff,
		"WWAP_RING_END": OpsRingEnd, "WWAP_COOP_OFF": OpsCoopOff, "WWAP_COOP_SIZE": OpsCoopSize,
		"WWAP_RING": OpsRing, "WWAP_ENTRY_SIZE": OpsEntrySize, "WWAP_CMD_PER_FRAME": OpsCmdPerFrame, "WWAP_MAX_OPS": OpsMaxOps,
		"WWAP_E_KIND": OpsEKind, "WWAP_E_SEQ": OpsESeq, "WWAP_E_CHECK": OpsECheck, "WWAP_E_A": OpsEA, "WWAP_E_B": OpsEB, "WWAP_E_C": OpsEC,
		"WWAP_C_OP": OpsCOp, "WWAP_C_SEQ": OpsCSeq, "WWAP_C_RESULT": OpsCRes, "WWAP_C_CHECK": OpsCCheck,
		"WWAP_C_A": OpsCA, "WWAP_C_B": OpsCB, "WWAP_C_C": OpsCC,
		"WWAP_TAG_EVENTS": uint64(OpsTagEvents), "WWAP_TAG_COMMANDS": uint64(OpsTagCommands), "WWAP_TAG_COOP": uint64(OpsTagCoop),
		"WWAP_R_OK": OpsROK, "WWAP_R_BADCHECK": OpsRBadCheck, "WWAP_R_UNKNOWN": OpsRUnknown, "WWAP_R_NOTFOUND": OpsRNotFound,
		"WWAP_R_MISMATCH": OpsRMismatch, "WWAP_R_REFUSED": OpsRRefused, "WWAP_R_FAILED": OpsRFailed,
		"WWAP_OP_ECHO": OpsOpEcho, "WWAP_OP_KILL": OpsOpKill, "WWAP_OP_MOD_FIRST": OpsOpModFirst, "WWAP_OP_WS_SET": OpsOpWsSet,
		"WWAP_EV_BOOT": OpsEvBoot, "WWAP_EV_CMD": OpsEvCmd, "WWAP_EV_ECHO": OpsEvEcho, "WWAP_EV_MOD_FIRST": OpsEvModFirst,
	})
}

// TestOpsLayout: the region ends at the mailbox; its parts tile it in
// order; the rings hold whole entries.
func TestOpsLayout(t *testing.T) {
	if OpsAddr+OpsSize != Mailbox {
		t.Errorf("API region 0x%08X+0x%X ends at 0x%08X, not the mailbox 0x%08X", OpsAddr, OpsSize, OpsAddr+OpsSize, Mailbox)
	}
	if OpsDirOff+OpsDirMax*OpsDirEntrySize > OpsStatDoneOff || OpsStatEvDropOff+4 > OpsEvWriteOff ||
		OpsEvRingOff+OpsRing*OpsEntrySize > OpsCmdHeadOff || OpsCmdRingOff+OpsRing*OpsEntrySize != OpsRingEnd ||
		OpsRingEnd > OpsCoopOff || OpsCoopOff+OpsCoopSize != OpsSize {
		t.Error("the API region's parts overlap or leave a gap the header does not name")
	}
	if OpsRing&(OpsRing-1) != 0 {
		t.Errorf("OpsRing %d is not a power of two", OpsRing)
	}
}

// TestOpsChecks: the check bytes are the XORs wwops.h describes.
func TestOpsChecks(t *testing.T) {
	if got := OpsCmdCheck(2, 7, 0x01020304, 0, 0xFF); got != 2^7^(1^2^3^4)^0xFF {
		t.Errorf("OpsCmdCheck = 0x%02X", got)
	}
	if got := OpsEventCheck(0x0102, 9, 0, 0x10, 0); got != 1^2^9^0x10 {
		t.Errorf("OpsEventCheck = 0x%02X", got)
	}
}
