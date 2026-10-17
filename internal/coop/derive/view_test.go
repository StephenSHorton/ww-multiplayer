package derive

import (
	"testing"

	"github.com/StephenSHorton/ww-multiplayer/internal/coop/snap"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/tables"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/tables/keys/itemsrc"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
)

// snapAt returns a snapshot with table loaded (-1: none).
func snapAt(table int) *snap.Snapshot { return &snap.Snapshot{Table: int8(table)} }

// setSaved sets bit b of table t's saved memBit in s.
func setSaved(s *snap.Snapshot, t int, b addr.Bit) {
	s.Save[addr.InfoOffSavedata+addr.OffMemBit(t)+b.Off] |= b.Mask
}

// setLive sets bit b of the live memBit in s.
func setLive(s *snap.Snapshot, b addr.Bit) { s.Save[addr.InfoOffMemory+b.Off] |= b.Mask }

// setImage sets bit b of image byte b.Off in s.
func setImage(s *snap.Snapshot, b addr.Bit) { s.Save[addr.InfoOffSavedata+b.Off] |= b.Mask }

func must(b addr.Bit, err error) addr.Bit {
	if err != nil {
		panic(err)
	}
	return b
}

func TestFlagSet(t *testing.T) {
	tbox := itemsrc.Flag{Kind: itemsrc.TboxFlag, Table: addr.TableDRC, Bit: 5}
	tboxBit := must(addr.TboxBit(5))
	item := itemsrc.Flag{Kind: itemsrc.ItemFlag, Table: addr.TableFW, Bit: 0x21}
	itemBit := must(addr.MemBitItemBit(0x21))
	life := itemsrc.Flag{Kind: itemsrc.StageLifeFlag, Table: addr.TableFF}
	lifeBit := must(addr.DungeonItemBit(addr.DungeonItemStageLife))
	ocean := itemsrc.Flag{Kind: itemsrc.OceanFlag, Room: 7, Bit: 9}
	oceanBit := must(addr.OceanBit(7, 9))
	oceanBit.Off += addr.OffOcean
	chart := itemsrc.Flag{Kind: itemsrc.ChartFlag, Bit: 10}
	// Chart 10 is map bit 9: u32 word 0, its second-lowest byte, bit 1.
	chartBit := addr.Bit{Off: addr.OffMapComplete + 2, Mask: 1 << 1}

	tests := []struct {
		name       string
		table      int // loaded
		set        func(*snap.Snapshot)
		f          itemsrc.Flag
		want, know bool
	}{
		{"tbox clear", -1, func(*snap.Snapshot) {}, tbox, false, true},
		{"tbox saved, other table loaded", addr.TableFW, func(s *snap.Snapshot) { setSaved(s, addr.TableDRC, tboxBit) }, tbox, true, true},
		{"tbox saved, its table loaded", addr.TableDRC, func(s *snap.Snapshot) { setSaved(s, addr.TableDRC, tboxBit) }, tbox, true, true},
		{"tbox live, its table loaded", addr.TableDRC, func(s *snap.Snapshot) { setLive(s, tboxBit) }, tbox, true, true},
		{"tbox live, another table loaded", addr.TableFW, func(s *snap.Snapshot) { setLive(s, tboxBit) }, tbox, false, true},
		{"item in the visited word", addr.TableFW, func(s *snap.Snapshot) { setLive(s, itemBit) }, item, true, true},
		{"stage life", -1, func(s *snap.Snapshot) { setSaved(s, addr.TableFF, lifeBit) }, life, true, true},
		{"stage life of another table", -1, func(s *snap.Snapshot) { setSaved(s, addr.TableDRC, lifeBit) }, life, false, true},
		{"ocean", addr.TableSea, func(s *snap.Snapshot) { setImage(s, oceanBit) }, ocean, true, true},
		{"chart", -1, func(s *snap.Snapshot) { setImage(s, chartBit) }, chart, true, true},
		{"chart 0", -1, func(*snap.Snapshot) {}, itemsrc.Flag{Kind: itemsrc.ChartFlag}, false, false},
		{"chart 129", -1, func(*snap.Snapshot) {}, itemsrc.Flag{Kind: itemsrc.ChartFlag, Bit: 129}, false, false},
		{"unsaved", -1, func(*snap.Snapshot) {}, itemsrc.Flag{}, false, false},
		{"zone item", -1, func(*snap.Snapshot) {}, itemsrc.Flag{Kind: itemsrc.ZoneItemFlag, Room: 1, Bit: 0x40}, false, false},
		{"table 16", -1, func(*snap.Snapshot) {}, itemsrc.Flag{Kind: itemsrc.TboxFlag, Table: 16}, false, false},
		{"tbox 32", -1, func(*snap.Snapshot) {}, itemsrc.Flag{Kind: itemsrc.TboxFlag, Table: 3, Bit: 32}, false, false},
		{"halting ocean grid", -1, func(*snap.Snapshot) {}, itemsrc.Flag{Kind: itemsrc.HaltFlag, Room: 0x40}, false, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := snapAt(tt.table)
			tt.set(s)
			got, known := ViewOf(s).FlagSet(tt.f)
			if got != tt.want || known != tt.know {
				t.Errorf("FlagSet(%v) = %v, %v; want %v, %v", tt.f, got, known, tt.want, tt.know)
			}
			if Known(tt.f) != tt.know {
				t.Errorf("Known(%v) = %v, want %v", tt.f, !tt.know, tt.know)
			}
		})
	}
}

func TestSwitchSetAndKeyNum(t *testing.T) {
	s := snapAt(addr.TableDRC)
	_, b, _ := addr.SwitchBit(0x12)
	setLive(s, b)
	_, b2, _ := addr.SwitchBit(0x13)
	setSaved(s, addr.TableFW, b2)
	v := ViewOf(s)
	for _, tt := range []struct {
		table, sw  int
		want, know bool
	}{
		{addr.TableDRC, 0x12, true, true},
		{addr.TableFW, 0x12, false, true},
		{addr.TableFW, 0x13, true, true},
		{addr.TableDRC, 0x13, false, true},
		{addr.TableDRC, 0x80, false, false}, // a dungeon switch
		{addr.TableDRC, 0xFF, false, false},
		{-1, 0x12, false, false},
	} {
		if got, known := v.SwitchSet(tt.table, tt.sw); got != tt.want || known != tt.know {
			t.Errorf("SwitchSet(%d, 0x%X) = %v, %v; want %v, %v", tt.table, tt.sw, got, known, tt.want, tt.know)
		}
	}

	s.Save[addr.InfoOffMemory+addr.MemBitOffKeyNum] = 2
	s.Save[addr.InfoOffSavedata+addr.OffMemBit(addr.TableDRC)+addr.MemBitOffKeyNum] = 7
	s.Save[addr.InfoOffSavedata+addr.OffMemBit(addr.TableFW)+addr.MemBitOffKeyNum] = 1
	if got := v.KeyNum(addr.TableDRC); got != 2 {
		t.Errorf("KeyNum of the loaded table = %d, want the live 2", got)
	}
	if got := v.KeyNum(addr.TableFW); got != 1 {
		t.Errorf("KeyNum of another table = %d, want its saved 1", got)
	}
}

func TestRecordSet(t *testing.T) {
	s := snapAt(-1)
	s.Save[addr.InfoOffSavedata+addr.OffEvent+0x13] = 0x40
	s.Save[addr.InfoOffSavedata+addr.OffEvent+0xAC] = 0xF0 | tables.LetterRead
	s.Save[addr.InfoOffSavedata+addr.OffEvent+0xB5] = 0x02 // delivered, not read
	v := ViewOf(s)
	for _, tt := range []struct {
		r          tables.Record
		want, know bool
	}{
		{tables.Record{Kind: tables.RecordEventBit, ID: 0x1340}, true, true},
		{tables.Record{Kind: tables.RecordEventBit, ID: 0x1320}, false, true},
		{tables.Record{Kind: tables.RecordLetterRead, ID: 0xAC03}, true, true},
		{tables.Record{Kind: tables.RecordLetterRead, ID: 0xB503}, false, true},
		{tables.Record{}, false, false},
	} {
		if got, known := v.RecordSet(tt.r); got != tt.want || known != tt.know {
			t.Errorf("RecordSet(%+v) = %v, %v; want %v, %v", tt.r, got, known, tt.want, tt.know)
		}
	}
}
