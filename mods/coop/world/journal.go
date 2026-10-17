package world

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"os"
	"strconv"
	"strings"
	"time"
)

// The journal, journal.log next to world.wwco: one line per change to the
// world, appended as it happens (§4.4: seq, time, origin, offsets). It is
// an audit trail of who changed what, and it recovers changes the last
// flush missed: Load replays every record whose seq is above the file's.
// A line reads
//
//	<seq> <time> <origin> <op> <args> <crc>
//
//	seq     the world's seq after the change, decimal
//	time    UTC, RFC 3339 with milliseconds
//	origin  the co-op connection that proposed it (0: the host itself)
//	op      gains | world | tomb | fresh+ | fresh-
//	args    gains, world, tomb: off:val pairs in hex, comma-separated,
//	        offsets ascending (the effective delta; for tomb the mask)
//	        fresh+: nonce@minted-time; fresh-: nonce
//	crc     CRC-32 (IEEE) of the line before " <crc>", 8 hex digits
//
// for example
//
//	42 2026-10-14T15:04:05.123Z 2 gains 051:04,3a4:80 056ce41e
//
// A line that does not parse, is not in this canonical form or fails its
// CRC (a write cut off by a crash, a hand edit) is skipped, never guessed
// at: whatever a bad line would replay, the replicas still hold and
// re-propose (§4.4 Self-healing).
//
// Replay goes by seq alone, so an older world file copied back over
// world.wwco gets the journal's later records again: the way to take a bit
// out of a world is a tombstone, never an old file.

// Op is what a journal record did.
type Op uint8

const (
	OpGains     Op = iota + 1 // ApplyGains: a GAINS c->s proposal
	OpWorld                   // Merge: a WORLD c->s image
	OpTombstone               // Tombstone
	OpFresh                   // AddFreshBind
	OpUnfresh                 // ClearFreshBind
)

var opNames = map[Op]string{
	OpGains: "gains", OpWorld: "world", OpTombstone: "tomb", OpFresh: "fresh+", OpUnfresh: "fresh-",
}

func (o Op) String() string {
	if s, ok := opNames[o]; ok {
		return s
	}
	return fmt.Sprintf("Op(%d)", uint8(o))
}

// Record is one journal line.
type Record struct {
	Seq    uint64
	Time   time.Time
	Origin Player
	Op     Op
	// Gains is the effective delta of OpGains and OpWorld, or the one
	// {offset, mask} of OpTombstone.
	Gains []Gain
	// Nonce is the fresh bind of OpFresh and OpUnfresh, and Minted when
	// an OpFresh one was minted.
	Nonce  ID
	Minted time.Time
}

const (
	journalTime = "2006-01-02T15:04:05.000Z"
	// maxJournalLine is the longest line Load reads: a delta of every
	// image byte is 0x778 x 7 bytes, about 13 KiB.
	maxJournalLine = 64 << 10
)

// Format returns r's journal line, without the newline.
func (r Record) Format() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%d %s %d %s ", r.Seq, r.Time.UTC().Format(journalTime), r.Origin, r.Op)
	switch r.Op {
	case OpFresh:
		fmt.Fprintf(&b, "%s@%s", r.Nonce, r.Minted.UTC().Format(journalTime))
	case OpUnfresh:
		b.WriteString(r.Nonce.String())
	default:
		for i, g := range r.Gains {
			if i > 0 {
				b.WriteByte(',')
			}
			fmt.Fprintf(&b, "%03x:%02x", g.Off, g.Val)
		}
	}
	line := b.String()
	return fmt.Sprintf("%s %08x", line, crc32.ChecksumIEEE([]byte(line)))
}

// ParseRecord parses one journal line (without its newline). It accepts
// only the canonical form Format writes.
func ParseRecord(line string) (Record, error) {
	var r Record
	bad := func(format string, args ...any) (Record, error) {
		return Record{}, fmt.Errorf("world: journal line: "+format, args...)
	}
	i := strings.LastIndexByte(line, ' ')
	if i < 0 || len(line)-i-1 != 8 {
		return bad("no CRC")
	}
	body := line[:i]
	sum, err := strconv.ParseUint(line[i+1:], 16, 32)
	if err != nil || uint32(sum) != crc32.ChecksumIEEE([]byte(body)) {
		return bad("CRC mismatch")
	}
	f := strings.Split(body, " ")
	if len(f) != 5 {
		return bad("%d fields, want 6", len(f)+1)
	}
	if r.Seq, err = strconv.ParseUint(f[0], 10, 64); err != nil {
		return bad("seq %q", f[0])
	}
	if r.Time, err = time.Parse(journalTime, f[1]); err != nil {
		return bad("time %q", f[1])
	}
	origin, err := strconv.ParseUint(f[2], 10, 8)
	if err != nil {
		return bad("origin %q", f[2])
	}
	r.Origin = Player(origin)
	for op, name := range opNames {
		if f[3] == name {
			r.Op = op
		}
	}
	switch r.Op {
	case 0:
		return bad("op %q", f[3])
	case OpFresh:
		nonce, minted, ok := strings.Cut(f[4], "@")
		if !ok {
			return bad("fresh bind %q", f[4])
		}
		if r.Nonce, err = ParseID(nonce); err != nil {
			return bad("%v", err)
		}
		if r.Minted, err = time.Parse(journalTime, minted); err != nil {
			return bad("minted %q", minted)
		}
	case OpUnfresh:
		if r.Nonce, err = ParseID(f[4]); err != nil {
			return bad("%v", err)
		}
	default:
		for _, p := range strings.Split(f[4], ",") {
			off, val, ok := strings.Cut(p, ":")
			if !ok || len(off) != 3 || len(val) != 2 {
				return bad("entry %q", p)
			}
			o, err1 := strconv.ParseUint(off, 16, 16)
			v, err2 := strconv.ParseUint(val, 16, 8)
			if err1 != nil || err2 != nil || o >= ImageSize || v == 0 {
				return bad("entry %q", p)
			}
			if n := len(r.Gains); n > 0 && uint16(o) <= r.Gains[n-1].Off {
				return bad("offsets not ascending at %q", p)
			}
			r.Gains = append(r.Gains, Gain{Off: uint16(o), Val: byte(v)})
		}
		if r.Op == OpTombstone && len(r.Gains) != 1 {
			return bad("tomb has %d entries, want 1", len(r.Gains))
		}
	}
	if r.Format() != line {
		return bad("not canonical")
	}
	return r, nil
}

// readJournal parses every record of the journal at path, in file order,
// and counts the lines it skipped. A missing journal has no records.
func readJournal(path string) ([]Record, int, error) {
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, 0, nil
	}
	if err != nil {
		return nil, 0, err
	}
	defer f.Close()
	var recs []Record
	bad := 0
	r := bufio.NewReaderSize(f, 64<<10)
	for {
		line, err := r.ReadSlice('\n')
		if errors.Is(err, bufio.ErrBufferFull) {
			// Longer than any line we write: skip to its end.
			bad++
			for errors.Is(err, bufio.ErrBufferFull) {
				_, err = r.ReadSlice('\n')
			}
			if err == io.EOF {
				break
			}
			if err != nil {
				return recs, bad, err
			}
			continue
		}
		if len(line) > 0 {
			text := bytes.TrimSuffix(bytes.TrimSuffix(line, []byte{'\n'}), []byte{'\r'}) // an editor's CRLF
			if rec, perr := ParseRecord(string(text)); perr == nil {
				recs = append(recs, rec)
			} else if len(bytes.TrimSpace(line)) > 0 {
				bad++
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return recs, bad, err
		}
	}
	return recs, bad, nil
}

// replay applies the records above w's seq to w, in order, and returns how
// many it applied. A record's changes merge like any proposal (under the
// class table and the tombstones, but not the session's policy, which
// already passed them), and w takes its seq.
func replay(w *World, recs []Record) int {
	n := 0
	for _, r := range recs {
		if r.Seq <= w.seq {
			continue
		}
		switch r.Op {
		case OpGains, OpWorld:
			next := w.image
			w.applyInto(&next, r.Gains)
			w.image = next
		case OpTombstone:
			if len(r.Gains) == 1 {
				w.Tombstone(int(r.Gains[0].Off), r.Gains[0].Val)
			}
		case OpFresh:
			w.AddFreshBind(r.Nonce, r.Minted)
		case OpUnfresh:
			w.ClearFreshBind(r.Nonce)
		}
		w.seq = r.Seq
		n++
	}
	return n
}

// applyInto merges gs into next under the class table and the tombstones.
func (w *World) applyInto(next *Image, gs []Gain) {
	full := FullPolicy()
	for _, g := range gs {
		off := int(g.Off)
		if off >= ImageSize || shared[off] == 0 || (isMax[off] && g.Val&^shared[off] != 0) {
			continue
		}
		w.merge(next, off, g.Val&shared[off], full, nil)
	}
}
