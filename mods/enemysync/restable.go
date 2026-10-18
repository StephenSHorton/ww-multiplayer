package enemysync

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mem"
)

// Mo2Res is the host's copy of the Mo2 archive's BCK pointers (design
// "host_side", BCK IDENTITY): the 56 entries of the Mo2 dRes_info_c's
// mRes at indices addr.Mo2BckFirst..Last. anm_init hands getObjectRes
// ("Mo2", bck) straight to setAnm, which stores it as mpAnm
// (d_a_mo2.cpp:137-153; m_Do_ext.cpp:1414-1449), so a Moblin's mpAnm is
// one of these pointers and its index is what crosses the wire: the
// pointers differ per machine, the index does not. There is no
// state-to-bck table anywhere.
//
// Resolve reads the table in two reads: the whole mObjectInfo (64 entries
// of 0x24) and, for the entry named "Mo2" with mCount != 0 and mpArchive
// in MEM1 (getResInfo's own rule, d_resorce.cpp:625-637), the 56 words at
// mRes + 4*Mo2BckFirst. The table is ok iff all 56 are distinct, 4-aligned
// and in MEM1. The host resolves it at every walk and, on a lookup miss,
// again at most once per MissReread.
type Mo2Res struct {
	ptrs  [addr.Mo2BckCount]uint32
	ok    bool
	entry int    // the Mo2 entry's index, -1 when none
	res   uint32 // its mRes
	count uint16 // its mCount
	why   string // why the table is not ok

	missNext time.Time

	// Stats are counters a probe or test reads: Reads is Resolve calls,
	// BckMiss is lookups that found no pointer, MissReads the re-reads a
	// miss triggered.
	Stats struct {
		Reads, BckMiss, MissReads uint64
	}
}

// MissReread is the least time between two table re-reads a lookup miss
// triggers.
const MissReread = 100 * time.Millisecond

// OK reports whether the last Resolve found a usable table.
func (r *Mo2Res) OK() bool { return r.ok }

// Why says why the table is not ok ("" when it is).
func (r *Mo2Res) Why() string { return r.why }

// Entry returns the Mo2 entry's index in mObjectInfo (-1 when none) and
// its mRes.
func (r *Mo2Res) Entry() (int, uint32) { return r.entry, r.res }

// Ptrs returns the 56 pointers of the last ok Resolve (index i is bck
// Mo2BckFirst+i).
func (r *Mo2Res) Ptrs() [addr.Mo2BckCount]uint32 { return r.ptrs }

// Resolve reads the table from m. A read error is returned; a table that
// is not usable is not an error: OK reports false and Why says why.
func (r *Mo2Res) Resolve(m mem.Reader) error {
	r.Stats.Reads++
	r.ok, r.entry, r.res, r.count, r.why = false, -1, 0, 0, ""
	n := addr.ObjectInfoCount * addr.ResInfoSize
	b, err := m.Read(addr.MObjectInfo, n)
	if err != nil {
		return fmt.Errorf("enemysync: mObjectInfo: %w", err)
	}
	if len(b) != n {
		return fmt.Errorf("enemysync: mObjectInfo: %d bytes read, want %d", len(b), n)
	}
	be := binary.BigEndian
	want := []byte(addr.Mo2Arc)
	for i := range addr.ObjectInfoCount {
		e := b[i*addr.ResInfoSize:]
		name := e[addr.ResInfoOffName : addr.ResInfoOffName+addr.ResInfoNameLen]
		if k := bytes.IndexByte(name, 0); k >= 0 {
			name = name[:k]
		}
		if !bytes.Equal(name, want) {
			continue
		}
		count := be.Uint16(e[addr.ResInfoOffCount:])
		arc := be.Uint32(e[addr.ResInfoOffArchive:])
		if count == 0 || !addr.InMEM1(arc) {
			continue // getResInfo skips it too
		}
		r.entry, r.res, r.count = i, be.Uint32(e[addr.ResInfoOffRes:]), count
		break
	}
	if r.entry < 0 {
		r.why = "no loaded Mo2 entry in mObjectInfo"
		return nil
	}
	if int(r.count) <= addr.Mo2BckLast {
		r.why = fmt.Sprintf("the Mo2 entry holds %d resources, fewer than the BCK range", r.count)
		return nil
	}
	at := r.res + 4*addr.Mo2BckFirst
	if mem.CheckRange(at, 4*addr.Mo2BckCount) != nil {
		r.why = fmt.Sprintf("the Mo2 entry's mRes 0x%08X is outside MEM1", r.res)
		return nil
	}
	p, err := m.Read(at, 4*addr.Mo2BckCount)
	if err != nil {
		return fmt.Errorf("enemysync: Mo2 mRes: %w", err)
	}
	if len(p) != 4*addr.Mo2BckCount {
		return fmt.Errorf("enemysync: Mo2 mRes: %d bytes read", len(p))
	}
	seen := map[uint32]bool{}
	for i := range r.ptrs {
		v := be.Uint32(p[4*i:])
		if !addr.InMEM1(v) || v%4 != 0 || seen[v] {
			r.why = fmt.Sprintf("Mo2 BCK 0x%02X's pointer 0x%08X is outside MEM1, misaligned or a duplicate", addr.Mo2BckFirst+i, v)
			r.ptrs = [addr.Mo2BckCount]uint32{}
			return nil
		}
		seen[v] = true
		r.ptrs[i] = v
	}
	r.ok = true
	return nil
}

// Index returns the BCK index of anm (a Moblin's mpAnm), or
// addr.Mo2BckNone when it is not one of the table's pointers or the table
// is not ok.
func (r *Mo2Res) Index(anm uint32) uint8 {
	if !r.ok || anm == 0 {
		return addr.Mo2BckNone
	}
	for i, p := range r.ptrs {
		if p == anm {
			return uint8(addr.Mo2BckFirst + i)
		}
	}
	return addr.Mo2BckNone
}

// Lookup is Index with the miss rule: a miss counts in Stats.BckMiss and,
// at most once per MissReread, re-reads the table from m and looks again
// (the archive may have been reloaded under the sampler).
func (r *Mo2Res) Lookup(m mem.Reader, anm uint32, now time.Time) (uint8, error) {
	if idx := r.Index(anm); idx != addr.Mo2BckNone {
		return idx, nil
	}
	r.Stats.BckMiss++
	if !r.ok || now.Before(r.missNext) {
		return addr.Mo2BckNone, nil
	}
	r.missNext = now.Add(MissReread)
	r.Stats.MissReads++
	if err := r.Resolve(m); err != nil {
		return addr.Mo2BckNone, err
	}
	return r.Index(anm), nil
}
