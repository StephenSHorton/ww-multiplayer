package mailbox

import "github.com/StephenSHorton/ww-multiplayer/wwapi/addr"

// The API region (inject/include/wwops.h, whose WWAP_* defines these
// mirror; ops_test.go pins each one to the header): the C runtime's rings,
// a fixed range right below the mailbox (co-op M7, wind#90). T2 code ends
// below OpsAddr. T2 writes the header, the event ring and the command
// results; Go (wwapi/ops) writes the event read word, the command entries
// and the command head.
const (
	OpsAddr   uint32 = 0x80415000 // region start; T2's budget ends here
	OpsSize   uint32 = 0xF00      // ends at Mailbox
	OpsMagic  uint32 = 0x57574150 // 'WWAP'
	OpsLayout        = 1

	OpsMagicOff       = 0x00
	OpsLayoutOff      = 0x04
	OpsDirCountOff    = 0x06
	OpsDirOff         = 0x08
	OpsDirMax         = 4
	OpsDirEntrySize   = 8
	OpsStatDoneOff    = 0x28
	OpsStatRefusedOff = 0x2C
	OpsStatBadOff     = 0x30
	OpsStatUnknownOff = 0x34
	OpsStatEvDropOff  = 0x38
	OpsEvWriteOff     = 0x40
	OpsEvReadOff      = 0x44
	OpsEvRingOff      = 0x50
	OpsCmdHeadOff     = 0x150
	OpsCmdTailOff     = 0x154
	OpsCmdRingOff     = 0x160
	OpsRingEnd        = 0x260
	OpsCoopOff        = 0x300
	OpsCoopSize       = 0xC00

	OpsRing        = 16
	OpsEntrySize   = 16
	OpsCmdPerFrame = 2
	OpsMaxOps      = 32

	OpsEKind  = 0x00
	OpsESeq   = 0x02
	OpsECheck = 0x03
	OpsEA     = 0x04
	OpsEB     = 0x08
	OpsEC     = 0x0C
	OpsCOp    = 0x00
	OpsCSeq   = 0x01
	OpsCRes   = 0x02
	OpsCCheck = 0x03
	OpsCA     = 0x04
	OpsCB     = 0x08
	OpsCC     = 0x0C

	OpsTagEvents   uint32 = 0x45565452 // 'EVTR'
	OpsTagCommands uint32 = 0x434D4451 // 'CMDQ'
	OpsTagCoop     uint32 = 0x434F4F50 // 'COOP'

	OpsROK       = 1
	OpsRBadCheck = 2
	OpsRUnknown  = 3
	OpsRNotFound = 4
	OpsRMismatch = 5
	OpsRRefused  = 6
	OpsRFailed   = 7

	OpsOpEcho     = 1
	OpsOpKill     = 2
	OpsOpModFirst = 16
	OpsOpWsSet    = 16 // the widescreen mod's op (WWAP_OP_WS_SET; widescreen.go OpWsSet)

	OpsEvBoot     = 1
	OpsEvCmd      = 2
	OpsEvEcho     = 3
	OpsEvModFirst = 256
)

// OpsRegion is the API region; OpsRingsRegion the part T2 and Go write
// every frame (header and rings). The co-op world region past it is
// reserved and unwritten until co-op M7's world image lands.
var (
	OpsRegion      = addr.Region{Name: "API region", Start: OpsAddr, End: OpsAddr + OpsSize}
	OpsRingsRegion = addr.Region{Name: "API rings", Start: OpsAddr, End: OpsAddr + OpsRingEnd}
)

// OpsCmdCheck is a command entry's check byte: the XOR of op, seq and the
// bytes of a, b and c.
func OpsCmdCheck(op, seq uint8, a, b, c uint32) uint8 {
	return op ^ seq ^ xorWord(a) ^ xorWord(b) ^ xorWord(c)
}

// OpsEventCheck is an event entry's check byte: the XOR of the kind's two
// bytes, seq and the bytes of a, b and c.
func OpsEventCheck(kind uint16, seq uint8, a, b, c uint32) uint8 {
	return uint8(kind>>8) ^ uint8(kind) ^ seq ^ xorWord(a) ^ xorWord(b) ^ xorWord(c)
}

func xorWord(w uint32) uint8 { return uint8(w ^ w>>8 ^ w>>16 ^ w>>24) }
