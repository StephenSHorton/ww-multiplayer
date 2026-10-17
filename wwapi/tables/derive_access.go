package tables

import (
	"strconv"
	"strings"

	"github.com/StephenSHorton/ww-multiplayer/wwapi/tables/hearts"
)

// What M4's derived max life (internal/coop/derive) reads of the heart
// Catalogue beyond the stage files: the save bit that marks a reward
// given. hearts.Reward.Record says it in words for the report; RecordOf
// reads the two forms the Catalogue writes.

// RecordKind is how the save marks a reward given.
type RecordKind uint8

const (
	// RecordNone: nothing the save holds marks it (the event data names
	// the record, or the grant code is not decompiled), so max life can't
	// count it.
	RecordNone RecordKind = iota
	// RecordEventBit: event bit ID (0xHHLL: mEvent byte HH, mask LL) is
	// set (onEventBit, d_save.cpp:1187-1189).
	RecordEventBit
	// RecordLetterRead: letter register ID (0xHHMM: mEvent byte HH, field
	// mask MM) reads LetterRead. Only dLetter_read sets that
	// (d_letter.cpp:43-44), and only as the mailbox's DEFAULT_POST event
	// ends (daObjTpost_c::modeReceiveDemo, d_a_obj_toripost.cpp:800-802),
	// after its present cut has handed over the letter's item
	// (cutPresentProc, :193-200). That the event always runs that cut
	// first is the event data's (inferred).
	RecordLetterRead
)

// LetterRead is dLetterStts_e's READ_e (d_letter.cpp:10-15): a read
// letter's register, as dLetter_isRead compares it (d_letter.cpp:48-49).
// setEventReg ORs the value in unshifted and getEventReg returns the
// masked byte unshifted (d_save.cpp:1202-1210), so a letter register's
// mask is this value, 0x03, as every LETTER_* id has it
// (d_save_event_flag.inc:499-556).
const LetterRead = 3

// Record is the save bit a Catalogue reward's Record names.
type Record struct {
	Kind RecordKind
	ID   uint16
}

// mEvent's named ranges (dSv_event_c, d_save.h:716): the bits are in
// bytes 0x00-0x41, the registers in 0x79-0xFF (d_save_event_flag.inc:
// 6-494, :497-629), as wwapi/addr's EventBitBytes and
// EventRegFirst say.
const (
	eventBitBytes = 0x42
	eventRegFirst = 0x79
)

// RecordOf reads r's Record: "event bit 0xHHLL ..." for one named bit, or
// "letter register 0xHH03 ...", the two forms the Catalogue writes.
// Anything else, a bit of no named byte or a register outside the
// registers, is RecordNone, so a slip in the Catalogue counts nothing
// rather than the wrong bit.
func RecordOf(r hearts.Reward) Record {
	id, ok := recordID(r.Record, "event bit 0x")
	if ok && id>>8 < eventBitBytes {
		if m := byte(id); m&(m-1) == 0 {
			return Record{RecordEventBit, id}
		}
	}
	id, ok = recordID(r.Record, "letter register 0x")
	if ok && id>>8 >= eventRegFirst && byte(id) == LetterRead {
		return Record{RecordLetterRead, id}
	}
	return Record{}
}

// recordID reads the four hex digits after prefix in rec, which must end
// there or go on with a space. The low byte must not be 0.
func recordID(rec, prefix string) (uint16, bool) {
	rest, ok := strings.CutPrefix(rec, prefix)
	if !ok || len(rest) < 4 || len(rest) > 4 && rest[4] != ' ' {
		return 0, false
	}
	id, err := strconv.ParseUint(rest[:4], 16, 16)
	if err != nil || byte(id) == 0 {
		return 0, false
	}
	return uint16(id), true
}
