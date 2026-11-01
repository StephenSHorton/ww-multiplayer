package mailbox

// The REL's re-create/delete table (inject/rel/refresh.c rf_rows, review
// idea 11), mirrored for the tools that read a row's state field: wwdev
// rf-ask peeks st_off before and after a request and names the value
// against the row's idle and done sets, so a wrong offset or width in the
// C table shows up as a mismatch in a live check (docs/TESTING.md RC-8)
// instead of a permanent GATE 9 or a wrong IN_STATE. TestRefreshRowsMatchC
// parses refresh.c and fails on any drift.

// RefreshRow* are rf_row's flags (refresh.c RF_ROW_*).
const (
	RefreshRowRecreate uint8 = 0x01 // delete, then create again (else delete only)
	RefreshRowAngleX   uint8 = 0x02 // the bit is home.angle.x's, not the params'
	RefreshRowCurRoom  uint8 = 0x04 // create reads it with the current room
	RefreshRowNoZone   uint8 = 0x08 // memory or dan switch only
	RefreshRowMemOnly  uint8 = 0x10 // memory switch only (room -1)
	RefreshRowNeed32   uint8 = 0x20 // NeedOff is a u32 (else a u8)
	RefreshRowKids     uint8 = 0x40 // KOKIIE: its SHAND hands, deleted with it
)

// RefreshRow is one rf_rows entry.
type RefreshRow struct {
	Proc    uint16
	StOff   uint16 // the state field's offset in the actor
	NeedOff uint16 // a field that must be non-zero (0: none)
	Shift   uint8  // the switch is (params >> Shift) & RefreshRowSwMask
	Flags   uint8  // RefreshRow*
	StW     uint8  // the state field's width: 1, 2 or 4
	Far10   uint8  // Link must be farther than Far10*10 XZ units (0: the generic 3D reach only)
	Idle    uint32 // bit v set: state v is the stale state the REL acts on
	Done    uint32 // bit v set: state v already shows the bit (IN_STATE); 0 for a delete row
}

func rfV(vs ...uint) uint32 {
	var m uint32
	for _, v := range vs {
		m |= 1 << v
	}
	return m
}

// RefreshRows is rf_rows in the C order, the door rows last (compiled
// in only with RefreshDoorRows).
var RefreshRows = []RefreshRow{
	{Proc: RefreshProcWall, StOff: 0x5AC, StW: 1, Idle: rfV(0)},
	{Proc: RefreshProcFloor, StOff: 0x2D8, StW: 1, Idle: rfV(0)},
	{Proc: RefreshProcIce, StOff: 0x458, StW: 4, Idle: rfV(0)},
	{Proc: RefreshProcMjdoor, StOff: 0x290, Flags: RefreshRowCurRoom, StW: 4, Idle: rfV(0)},
	{Proc: RefreshProcMkie, StOff: 0xF50, Shift: RefreshMkieSwShift, StW: 4, Idle: rfV(0)},
	{Proc: RefreshProcMkiek, StOff: 0x45C, StW: 4, Idle: rfV(0)},
	{Proc: RefreshProcMknjd, StOff: 0x43F, StW: 1, Idle: rfV(0, 8)},
	{Proc: RefreshProcVfan, StOff: 0x440, StW: 4, Idle: rfV(0)},
	{Proc: RefreshProcSwhit0, StOff: 0x551, Flags: RefreshRowRecreate | RefreshRowCurRoom, StW: 1, Idle: rfV(0), Done: rfV(4, 5)},
	{Proc: RefreshProcWarpt, StOff: 0x290, NeedOff: 0x340, Flags: RefreshRowRecreate | RefreshRowAngleX | RefreshRowNoZone | RefreshRowNeed32, StW: 4, Far10: 12, Idle: rfV(1), Done: rfV(0)},
	{Proc: RefreshProcMflft, StOff: 0x29A, Shift: RefreshMflftSwShift, Flags: RefreshRowRecreate | RefreshRowCurRoom, StW: 2, Far10: 50, Idle: rfV(0), Done: rfV(1)},
	{Proc: RefreshProcKokiie, StOff: 0x29A, Shift: RefreshKokiieSwShift, Flags: RefreshRowRecreate | RefreshRowKids, StW: 2, Far10: 100, Idle: rfV(0), Done: rfV(1)},
	{Proc: RefreshProcVmc, StOff: 0x4B8, Shift: RefreshVmcSwShift, Flags: RefreshRowRecreate, StW: 4, Far10: 30, Idle: rfV(0), Done: rfV(3)},
	{Proc: RefreshProcDoor10, StOff: 0x354, NeedOff: 0x308, Flags: RefreshRowRecreate | RefreshRowMemOnly, StW: 1, Far10: 25, Idle: rfV(1), Done: rfV(0)},
	{Proc: RefreshProcDoor12, StOff: 0x314, NeedOff: 0x2E4, Flags: RefreshRowRecreate | RefreshRowMemOnly, StW: 1, Far10: 25, Idle: rfV(1), Done: rfV(0)},
}

// RefreshRowOf is proc's row, nil for a proc with no table row (the five
// batch D procs have their own code in refresh.c).
func RefreshRowOf(proc uint16) *RefreshRow {
	for i := range RefreshRows {
		if RefreshRows[i].Proc == proc {
			return &RefreshRows[i]
		}
	}
	return nil
}

// StateClass names state value v against r: "idle" (the REL acts on it),
// "done" (IN_STATE), or "busy" (GATE 9: mid-break, mid-event, mid-grow).
func (r *RefreshRow) StateClass(v uint32) string {
	switch {
	case v < 32 && r.Idle&(1<<v) != 0:
		return "idle"
	case v < 32 && r.Done&(1<<v) != 0:
		return "done"
	}
	return "busy"
}
