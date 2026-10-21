package enemysync

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"slices"
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
	count uint16 // its mCount: a reference count (dRes_info_c incCount/decCount, d_resorce.h:26-32), not a resource count
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
// (the archive may have been reloaded under the sampler, or loaded since
// a walk found the table not ok).
func (r *Mo2Res) Lookup(m mem.Reader, anm uint32, now time.Time) (uint8, error) {
	if idx := r.Index(anm); idx != addr.Mo2BckNone {
		return idx, nil
	}
	r.Stats.BckMiss++
	if now.Before(r.missNext) {
		return addr.Mo2BckNone, nil
	}
	r.missNext = now.Add(MissReread)
	r.Stats.MissReads++
	if err := r.Resolve(m); err != nil {
		return addr.Mo2BckNone, err
	}
	return r.Index(anm), nil
}

// ArcRes is Mo2Res for any kind's archive (design "animation": ArcRes
// per kind, MO2 'Mo2' 0x3E count 56, BK 'Bk' 0x29 count 43, CC 'CC' 0x14
// count 15): Count BCK pointers at mRes + 4*First of the loaded
// dRes_info_c named Name. The rules are Mo2Res's: the entry must have
// mCount != 0 (a reference count) and mpArchive in MEM1, and the table is
// ok iff every pointer is in MEM1, 4-aligned and distinct. Resolve makes
// two Reads. The host's KindTracker holds one per kind (the Moblin's
// included); Mo2Res stays for the Moblin probes.
type ArcRes struct {
	Name  string
	First int
	Count int

	ptrs  []uint32
	ok    bool
	entry int
	res   uint32
	count uint16
	why   string

	missNext time.Time

	// Stats as Mo2Res's.
	Stats struct {
		Reads, BckMiss, MissReads uint64
	}
}

// NewArcRes is the table of k's archive.
func NewArcRes(k *Kind) *ArcRes {
	return &ArcRes{Name: k.Arc, First: k.BckFirst, Count: k.BckCount(), entry: -1}
}

// OK reports whether the last Resolve found a usable table.
func (r *ArcRes) OK() bool { return r.ok }

// Why says why the table is not ok ("" when it is).
func (r *ArcRes) Why() string { return r.why }

// Entry returns the entry's index in mObjectInfo (-1 when none), its mRes
// and its mCount.
func (r *ArcRes) Entry() (int, uint32, uint16) { return r.entry, r.res, r.count }

// Ptrs returns the pointers of the last ok Resolve (index i is bck
// First+i), nil when the table is not ok.
func (r *ArcRes) Ptrs() []uint32 { return r.ptrs }

// Resolve reads the table from m. A read error is returned; a table that
// is not usable is not an error: OK reports false and Why says why.
func (r *ArcRes) Resolve(m mem.Reader) error {
	r.Stats.Reads++
	r.ok, r.entry, r.res, r.count, r.why, r.ptrs = false, -1, 0, 0, "", nil
	if r.Count <= 0 || r.First < 0 || r.First+r.Count > 0x100 || len(r.Name) >= addr.ResInfoNameLen {
		r.why = fmt.Sprintf("bad table %q 0x%X+%d", r.Name, r.First, r.Count)
		return nil
	}
	n := addr.ObjectInfoCount * addr.ResInfoSize
	b, err := m.Read(addr.MObjectInfo, n)
	if err != nil {
		return fmt.Errorf("enemysync: mObjectInfo: %w", err)
	}
	if len(b) != n {
		return fmt.Errorf("enemysync: mObjectInfo: %d bytes read, want %d", len(b), n)
	}
	be := binary.BigEndian
	want := []byte(r.Name)
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
		r.why = fmt.Sprintf("no loaded %s entry in mObjectInfo", r.Name)
		return nil
	}
	at := r.res + 4*uint32(r.First)
	if mem.CheckRange(at, 4*r.Count) != nil {
		r.why = fmt.Sprintf("the %s entry's mRes 0x%08X is outside MEM1", r.Name, r.res)
		return nil
	}
	p, err := m.Read(at, 4*r.Count)
	if err != nil {
		return fmt.Errorf("enemysync: %s mRes: %w", r.Name, err)
	}
	if len(p) != 4*r.Count {
		return fmt.Errorf("enemysync: %s mRes: %d bytes read", r.Name, len(p))
	}
	ptrs := make([]uint32, r.Count)
	seen := map[uint32]bool{}
	for i := range ptrs {
		v := be.Uint32(p[4*i:])
		if !addr.InMEM1(v) || v%4 != 0 || seen[v] {
			r.why = fmt.Sprintf("%s BCK 0x%02X's pointer 0x%08X is outside MEM1, misaligned or a duplicate", r.Name, r.First+i, v)
			return nil
		}
		seen[v] = true
		ptrs[i] = v
	}
	r.ptrs, r.ok = ptrs, true
	return nil
}

// Index returns the BCK index of anm, or addr.Mo2BckNone (0xFF) when it
// is not one of the table's pointers or the table is not ok.
func (r *ArcRes) Index(anm uint32) uint8 {
	if !r.ok || anm == 0 {
		return addr.Mo2BckNone
	}
	for i, p := range r.ptrs {
		if p == anm {
			return uint8(r.First + i)
		}
	}
	return addr.Mo2BckNone
}

// Lookup is Index with Mo2Res's miss rule (one re-read per MissReread).
func (r *ArcRes) Lookup(m mem.Reader, anm uint32, now time.Time) (uint8, error) {
	if idx := r.Index(anm); idx != addr.Mo2BckNone {
		return idx, nil
	}
	r.Stats.BckMiss++
	if now.Before(r.missNext) {
		return addr.Mo2BckNone, nil
	}
	r.missNext = now.Add(MissReread)
	r.Stats.MissReads++
	if err := r.Resolve(m); err != nil {
		return addr.Mo2BckNone, err
	}
	return r.Index(anm), nil
}

// KindTracker is Tracker over every kind: one ArcRes per kind and the
// restart detector per process ID. The probes derive a KindSample's Bck,
// AnmSeq and Morf through it.
type KindTracker struct {
	Res    [len(Kinds)]*ArcRes
	tracks map[uint32]*AnmTrack
}

// NewKindTracker is a tracker with every kind's table unresolved.
func NewKindTracker() *KindTracker {
	t := &KindTracker{tracks: map[uint32]*AnmTrack{}}
	for i := range Kinds {
		t.Res[i] = NewArcRes(&Kinds[i])
	}
	return t
}

// Derive fills s's Bck (through s.Kind's table, with the miss rule),
// AnmSeq and Morf for the actor with process ID pid at now.
func (t *KindTracker) Derive(m mem.Reader, pid uint32, s *KindSample, now time.Time) error {
	tr := t.tracks[pid]
	if tr == nil {
		tr = &AnmTrack{}
		t.tracks[pid] = tr
	}
	tr.Update(&s.Sample)
	s.Bck = addr.Mo2BckNone
	if !s.MorfOK || s.Kind.Row() == nil {
		return nil
	}
	idx, err := t.Res[s.Kind].Lookup(m, s.Anm, now)
	s.Bck = idx
	return err
}

// Keep drops the tracks of every actor not in pids.
func (t *KindTracker) Keep(pids []uint32) {
	for pid := range t.tracks {
		if !slices.Contains(pids, pid) {
			delete(t.tracks, pid)
		}
	}
}
