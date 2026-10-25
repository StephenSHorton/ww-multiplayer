package tables

import (
	"strings"
	"testing"

	"github.com/StephenSHorton/ww-multiplayer/wwapi/tables/hearts"
)

func TestRecordOf(t *testing.T) {
	for _, tt := range []struct {
		record string
		want   Record
	}{
		{"event bit 0x1340 (UNK_1340, d_save_event_flag.inc:143)", Record{RecordEventBit, 0x1340}},
		{"event bit 0x0F10", Record{RecordEventBit, 0x0F10}},
		{"letter register 0xAC03 (LETTER_BAITOS_MOM, d_save_event_flag.inc:549), read at d_a_obj_toripost.cpp:802", Record{RecordLetterRead, 0xAC03}},
		{"", Record{}},
		{"the event data", Record{}},
		{"event bit 0x13", Record{}},         // too short
		{"event bit 0x1340x", Record{}},      // no space after the id
		{"event bit 0x1300", Record{}},       // no mask
		{"event bit 0x1303", Record{}},       // two bits
		{"event bit 0x4201", Record{}},       // past the named bytes
		{"event bit 0xZZ40", Record{}},       // not hex
		{"letter register 0xAC01", Record{}}, // not the read value's mask
		{"letter register 0x4003", Record{}}, // not a register byte
		{"Event bit 0x1340", Record{}},       // the Catalogue's spelling only
		{"letter register 0xAC03 ", Record{RecordLetterRead, 0xAC03}},
	} {
		if got := RecordOf(hearts.Reward{Record: tt.record}); got != tt.want {
			t.Errorf("RecordOf(%q) = %+v, want %+v", tt.record, got, tt.want)
		}
	}
}

// Every Catalogue record the decomp shows reads as a save bit; the rest
// (no record, or one the event data holds) count nothing.
func TestCatalogueRecords(t *testing.T) {
	want := map[string]Record{
		"ivan":                      {RecordEventBit, 0x1340},
		"orca":                      {RecordEventBit, 0x0F10},
		"auction":                   {RecordEventBit, 0x1020},
		"letter-baitos-mom":         {RecordLetterRead, 0xAC03},
		"letter-komalis-father":     {RecordLetterRead, 0xB503},
		"letter-hoskits-girlfriend": {RecordLetterRead, 0xAE03},
		"kreeb":                     {RecordEventBit, 0x1B20},
		"sam":                       {RecordEventBit, 0x1B10},
		"linda-and-anton":           {RecordEventBit, 0x2280},
	}
	seen := 0
	for _, r := range hearts.Catalogue {
		got := RecordOf(r)
		w, ok := want[r.ID]
		switch {
		case ok:
			seen++
			if got != w {
				t.Errorf("%s: RecordOf = %+v, want %+v", r.ID, got, w)
			}
		case strings.HasPrefix(r.Record, "event bit 0x"):
			if got.Kind != RecordEventBit {
				t.Errorf("%s: %q reads as %+v", r.ID, r.Record, got)
			}
		case got.Kind != RecordNone:
			t.Errorf("%s: %q reads as %+v, want none", r.ID, r.Record, got)
		}
	}
	if seen != len(want) {
		t.Errorf("found %d of the %d expected rewards in the Catalogue", seen, len(want))
	}
}

// TestRecordsVerified keeps a guess out of the life derive: max life is
// add-only, so a reward whose record RecordOf reads must not be one its
// own entry calls inferred or unverified. The Goron trade's 0x3E04 was
// such a record (the bit is set in a talk branch; whether that event
// hands over the piece is event data).
func TestRecordsVerified(t *testing.T) {
	for _, r := range hearts.Catalogue {
		if RecordOf(r).Kind == RecordNone {
			continue
		}
		for _, field := range []string{r.Record, r.Note} {
			l := strings.ToLower(field)
			if strings.Contains(l, "unverified") || strings.Contains(l, "inferred") {
				t.Errorf("%s: record %q is live in the derive, but its entry says %q", r.ID, r.Record, field)
			}
		}
	}
	for _, r := range hearts.Catalogue {
		if r.ID == "traveling-merchant" && RecordOf(r).Kind != RecordNone {
			t.Errorf("traveling-merchant: RecordOf = %+v, want none until the event data is checked", RecordOf(r))
		}
	}
}

func FuzzRecordOf(f *testing.F) {
	for _, r := range hearts.Catalogue {
		f.Add(r.Record)
	}
	f.Add("letter register 0xFF03")
	f.Fuzz(func(t *testing.T, rec string) {
		r := RecordOf(hearts.Reward{Record: rec})
		hh, mm := int(r.ID>>8), byte(r.ID)
		switch r.Kind {
		case RecordNone:
			if r.ID != 0 {
				t.Fatalf("RecordNone with id 0x%04X", r.ID)
			}
		case RecordEventBit:
			if hh >= eventBitBytes || mm == 0 || mm&(mm-1) != 0 || !strings.HasPrefix(rec, "event bit 0x") {
				t.Fatalf("%q: event bit 0x%04X", rec, r.ID)
			}
		case RecordLetterRead:
			if hh < eventRegFirst || mm != LetterRead || !strings.HasPrefix(rec, "letter register 0x") {
				t.Fatalf("%q: letter register 0x%04X", rec, r.ID)
			}
		default:
			t.Fatalf("%q: kind %d", rec, r.Kind)
		}
	})
}
