package world

import (
	"fmt"
	"hash/crc32"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

var testTime = time.Date(2026, 10, 14, 15, 4, 5, 123e6, time.UTC)

func TestJournalRoundTrip(t *testing.T) {
	for _, r := range []Record{
		{Seq: 42, Time: testTime, Origin: 2, Op: OpGains, Gains: []Gain{{0x051, 0x04}, {0x3A4, 0x80}}},
		{Seq: 1, Time: testTime, Origin: 255, Op: OpWorld, Gains: []Gain{{0x000, 0x01}}},
		{Seq: 18446744073709551615, Time: testTime, Op: OpTombstone, Gains: []Gain{{0x777, 0xFF}}},
		{Seq: 7, Time: testTime, Op: OpFresh, Nonce: testID, Minted: testTime.Add(-time.Hour)},
		{Seq: 8, Time: testTime, Op: OpUnfresh, Nonce: testID},
	} {
		line := r.Format()
		got, err := ParseRecord(line)
		if err != nil {
			t.Fatalf("%q: %v", line, err)
		}
		if got.Seq != r.Seq || !got.Time.Equal(r.Time) || got.Origin != r.Origin || got.Op != r.Op ||
			!slices.Equal(got.Gains, r.Gains) || got.Nonce != r.Nonce || !got.Minted.Equal(r.Minted) {
			t.Errorf("%q parsed as %+v", line, got)
		}
	}
	if line := (Record{Seq: 42, Time: testTime, Origin: 2, Op: OpGains, Gains: []Gain{{0x051, 0x04}, {0x3A4, 0x80}}}).Format(); line != "42 2026-10-14T15:04:05.123Z 2 gains 051:04,3a4:80 056ce41e" {
		t.Errorf("format drifted: %q", line)
	}
}

// TestJournalRefuses: only the canonical form with a good CRC parses.
func TestJournalRefuses(t *testing.T) {
	good := Record{Seq: 42, Time: testTime, Origin: 2, Op: OpGains, Gains: []Gain{{0x051, 0x04}, {0x3A4, 0x80}}}
	resum := func(body string) string { return fmt.Sprintf("%s %08x", body, crc32.ChecksumIEEE([]byte(body))) }
	for _, line := range []string{
		"",
		good.Format()[:len(good.Format())-1], // CRC cut short
		strings.Replace(good.Format(), "051:04", "051:05", 1),      // CRC mismatch
		resum("42 2026-10-14T15:04:05.123Z 2 gains 051:04,3A4:80"), // upper-case hex
		resum("042 2026-10-14T15:04:05.123Z 2 gains 051:04"),       // leading zero
		resum("42 2026-10-14T15:04:05.123Z 2 gains 51:04"),         // short offset
		resum("42 2026-10-14T15:04:05.123Z 2 gains 3a4:80,051:04"), // not ascending
		resum("42 2026-10-14T15:04:05.123Z 2 gains 051:04,051:08"), // repeated offset
		resum("42 2026-10-14T15:04:05.123Z 2 gains 778:01"),        // past the image
		resum("42 2026-10-14T15:04:05.123Z 2 gains 051:00"),        // empty gain
		resum("42 2026-10-14T15:04:05.123Z 2 gains "),              // no gains
		resum("42 2026-10-14T15:04:05.123Z 2 tomb 051:04,052:01"),  // two tombstones
		resum("42 2026-10-14T15:04:05.123Z 256 gains 051:04"),      // origin past a byte
		resum("42 2026-10-14T15:04:05Z 2 gains 051:04"),            // no milliseconds
		resum("42 2026-10-14T15:04:05.123+01:00 2 gains 051:04"),   // not UTC
		resum("42 2026-10-14T15:04:05.123Z 2 grow 051:04"),         // unknown op
		resum("42 2026-10-14T15:04:05.123Z 2  gains 051:04"),       // double space
		resum("42 2026-10-14T15:04:05.123Z 2 fresh+ c00b010203040506"),
		resum("42 2026-10-14T15:04:05.123Z 2 fresh- c00b0102030405"),
	} {
		if r, err := ParseRecord(line); err == nil {
			t.Errorf("%q parsed as %+v", line, r)
		}
	}
}

// TestReadJournal: damaged and overlong lines are counted and skipped, the
// rest kept in order.
func TestReadJournal(t *testing.T) {
	path := filepath.Join(t.TempDir(), JournalName)
	a := Record{Seq: 1, Time: testTime, Op: OpGains, Gains: []Gain{{1, 1}}}.Format()
	b := Record{Seq: 2, Time: testTime, Op: OpGains, Gains: []Gain{{2, 2}}}.Format()
	c := Record{Seq: 3, Time: testTime, Op: OpGains, Gains: []Gain{{3, 3}}}.Format()
	long := strings.Repeat("x", 100<<10)
	content := a + "\n" + "garbage\n" + long + "\n" + "\n" + b + "\r\n" + c[:len(c)/2] // b saved by an editor with CRLF
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	recs, bad, err := readJournal(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 2 || recs[0].Seq != 1 || recs[1].Seq != 2 || bad != 3 {
		t.Errorf("%d records, %d bad", len(recs), bad)
	}
	if recs, bad, err := readJournal(filepath.Join(t.TempDir(), "none")); recs != nil || bad != 0 || err != nil {
		t.Errorf("missing journal: %v %d %v", recs, bad, err)
	}
}
