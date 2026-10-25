package replica

// Portions adapted from datstarkey/ww-online
// WWOnline.Client/Services/WorldFlagSyncService.cs:303-339 (ApplySalvage,
// SalvagedPoints) and WWOnline.Client/Data/GameMemoryAddresses.cs:377-391
// (the Salvage table), MIT:
//
// MIT License
//
// Copyright (c) 2026 datstarkey
//
// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included in all
// copies or substantial portions of the Software.
//
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
// SOFTWARE.
//
// What was taken is the idea and the matching rule (which point a salvage
// bit or a salvaged chart empties); the gates, the guard and the verify are
// this repo's, as for the key-door lock (writer_lock.go). Every offset was
// checked again in the decomp and the GZLE01 main.dol (wwapi/addr
// salvage.go).

import (
	"context"
	"encoding/binary"
	"fmt"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/wwapi/actors"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mem"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/save"
)

// The salvage catch-up: a sunken treasure the partner pulled up is gone
// here too, without waiting for its sea square to load again.
//
// The partner's end_salvage sets a salvage bit (kinds 2-4,
// onOceanSvBit(mRoomNo, mSaveNo)) or a chart's "salvaged" bit (kind 0,
// onCompleteCollectMap(mSaveNo)) (d_a_salvage.cpp:503-527). Both sync as
// ocean bits (class OR, CatOcean), written here at the receiver's next
// room or stage change (Timing OnRoomChange). But a point is checked
// against those bits only when its daSalvage_c is created, as its room
// loads (dSalvage_control_c::entry, d_salvage.cpp:85-110), and the crane's
// every-frame pass never sets a point unusable from them: it only clears
// the flag while the bit is unset (kinds 0, 2, 4) or follows the time of
// day and the moon (4, 6) (daSalvage_c::proc_wait, d_a_salvage.cpp:
// 286-337). At sea a square's rooms stay loaded as Link sails into the
// next, so a point registered before the bit landed stays salvageable,
// and pulling it up pays its item a second time. This sets that point's
// mFlag bit 0x01 (SalvageFlagUnusable), as proc_salvage_init's setFlag(id,
// 1) does (d_a_salvage.cpp:376-379): the light and the crane prompt go
// (calcAlpha, checkDistance and the draw all ask checkUsed, :414, :552,
// :583, :609, :630). It stays set: with the save bit set, proc_wait's
// clears never run (:288, :306, :325).
//
// Its gates, each read again just before the write (ApplySalvage):
//
//   - co-op armed in the view's world, not a dry run, co-op on;
//   - IDLE and STABLE, no stage change pending: no event runs, so the
//     receiver's own crane is not pulling the point up (the salvage is an
//     event, eventOrder, d_a_salvage.cpp:150-157), and the pause menu,
//     under which the sea chart writes the charts, is closed;
//   - the point's key in the landed set (landed.go: the bits this
//     replica's own verified writes set, for RefreshSettle), never a bit
//     that merely reads set, and reading set now;
//   - mTagData_p non-null, word-aligned and the table in MEM1, a daSalvage_c
//     executing in the tick's walk (the owner of the table's heap: only
//     the scene's create nulls the pointer, so it dangles after the
//     owner's delete), and the table sane (mSeaNum and mRoomNum within
//     their MAX, every mRoomNo -1 or a room, every mKind 0-6);
//   - the point registered (mRoomNo not -1), its flag bit clear, and its
//     position, room, save number and kind unchanged since the plan;
//   - kind 0 with a chart 1..128, or kind 2, 3 or 4 with a grid 0..49 and
//     a save number below 16 (31 is "no save bit", d_salvage.cpp:100-105).
//     Kind 5 and kind 6 (the moonlight points, whose event bits are
//     game-cleared and stay each player's, class syncmask.go) are never
//     touched.
//
// The write is one byte, old | 0x01, judged by WriteGuard.checkSalvage
// (TargetSalvage), and the next Observe reads the entry again: a point
// whose flag reads clear on the same entry is a verify failure; one gone
// (init_one, a new room's point in its slot, another table) is not.
//
// Left as it is: the bits themselves keep their timing. With CatOcean at
// OnRoomChange, two players in the same square race until the receiver
// leaves it; the catch-up closes the window from then on. Writing the
// ocean bits live would reach the Tingle Tuner's per-frame read of bit 15
// (daAgbsw0_c, d_a_agbsw0.cpp:490) too, a planner policy left for a
// separate change.

// SalvagePoint is one registered dSalvage_info_c as read.
type SalvagePoint struct {
	Index int       // its mInfo index, 0..159
	Pos   [3]uint32 // mPos, the f32 bits
	Room  int8      // mRoomNo
	Save  uint8     // mSaveNo
	Kind  uint8     // mKind
	Flag  uint8     // mFlag
}

// Usable reports whether the crane may pull p up now (checkUsed,
// d_salvage.cpp:265-270).
func (p SalvagePoint) Usable() bool { return p.Flag&addr.SalvageFlagUnusable == 0 }

// same reports whether q is the point p is: the same entry, place, room,
// save number and kind.
func (p SalvagePoint) same(q SalvagePoint) bool {
	return p.Index == q.Index && p.Pos == q.Pos && p.Room == q.Room && p.Save == q.Save && p.Kind == q.Kind
}

// FlagAddr is the address of p's mFlag in the table at base.
func (p SalvagePoint) FlagAddr(base uint32) uint32 {
	return addr.SalvageInfoAddr(base, p.Index) + addr.SalvageInfoOffFlag
}

func (p SalvagePoint) String() string {
	switch p.Kind {
	case 0:
		return fmt.Sprintf("salvage point %d (chart %d, mSaveNo %d)", p.Index, int(p.Save)-1, p.Save)
	case 2, 3, 4:
		return fmt.Sprintf("salvage point %d (sea grid %d bit %d, kind %d)", p.Index, p.Room, p.Save, p.Kind)
	}
	return fmt.Sprintf("salvage point %d (kind %d)", p.Index, p.Kind)
}

// Key returns the landed bit that empties p, with table loaded, the key
// end_salvage writes for it (d_a_salvage.cpp:503-527): kind 0 its chart
// (mSaveNo, 1..128), kinds 2-4 its grid and bit (mRoomNo 0..49, mSaveNo
// 0..15, the ranges onOceanSvBit asserts, d_save.cpp:1164-1167). ok is
// false for every other point.
func (p SalvagePoint) Key(table int8) (LandedBit, bool) {
	switch p.Kind {
	case 0:
		if p.Save >= 1 && p.Save <= 128 {
			return ChartKey(table, int(p.Save)), true
		}
	case 2, 3, 4:
		if p.Room >= 0 && int(p.Room) < addr.OceanGridCount && p.Save < 16 {
			return OceanKey(table, int(p.Room), int(p.Save)), true
		}
	}
	return LandedBit{}, false
}

// decodeSalvageInfo decodes mInfo[i] from its 0x38 bytes.
func decodeSalvageInfo(i int, b []byte) SalvagePoint {
	p := SalvagePoint{Index: i, Room: int8(b[addr.SalvageInfoOffRoomNo]), Save: b[addr.SalvageInfoOffSaveNo],
		Kind: b[addr.SalvageInfoOffKind], Flag: b[addr.SalvageInfoOffFlag]}
	for k := range p.Pos {
		p.Pos[k] = binary.BigEndian.Uint32(b[addr.SalvageInfoOffPos+4*k:])
	}
	return p
}

// ParseSalvage decodes a dSalvage_control_c's SalvageSize bytes into its
// registered points, or returns why the table does not look like one:
// mSeaNum or mRoomNum past their MAX, an mRoomNo below -1 or past room 63,
// an mKind past 6 (entry's ranges, d_salvage.cpp:46-160; init,
// :20-27).
func ParseSalvage(b []byte) ([]SalvagePoint, string) {
	if len(b) < addr.SalvageSize {
		return nil, fmt.Sprintf("%d bytes, not the table's 0x%X", len(b), addr.SalvageSize)
	}
	if sea, room := b[addr.SalvageOffSeaNum], b[addr.SalvageOffRoomNum]; sea > addr.SalvageSeaMax || room > addr.SalvageRoomMax {
		return nil, fmt.Sprintf("mSeaNum %d, mRoomNum %d: past MAX_SEA %d or MAX_ROOM %d", sea, room, addr.SalvageSeaMax, addr.SalvageRoomMax)
	}
	var out []SalvagePoint
	for i := range addr.SalvageInfoCount {
		at := addr.SalvageOffInfo + i*addr.SalvageInfoSize
		p := decodeSalvageInfo(i, b[at:at+addr.SalvageInfoSize])
		switch {
		case p.Room == -1:
			continue
		case p.Room < -1 || p.Room > 63:
			return nil, fmt.Sprintf("entry %d: mRoomNo %d is no room", i, p.Room)
		case p.Kind > 6:
			return nil, fmt.Sprintf("entry %d: mKind %d is no salvage kind", i, p.Kind)
		}
		out = append(out, p)
	}
	return out, ""
}

// readSalvageTable reads mTagData_p and the table it points at. why says
// why there is no table to act on (none this scene, or one that does not
// look like one); err is a read error.
func readSalvageTable(m mem.Reader) (base uint32, ps []SalvagePoint, why string, err error) {
	base, err = mem.ReadU32(m, addr.SalvageTagDataPtr)
	switch {
	case err != nil:
		return 0, nil, "", err
	case base == 0:
		return 0, nil, "no salvage table this scene (mTagData_p is null)", nil
	case base%4 != 0 || mem.CheckRange(base, addr.SalvageSize) != nil:
		return 0, nil, fmt.Sprintf("mTagData_p 0x%08X is not a table in MEM1", base), nil
	}
	b, err := m.Read(base, addr.SalvageSize)
	if err != nil {
		return 0, nil, "", err
	}
	ps, why = ParseSalvage(b)
	if why != "" {
		why = fmt.Sprintf("the table at 0x%08X: %s", base, why)
	}
	return base, ps, why, nil
}

// salvageOwner reports whether walk w holds an executing daSalvage_c: the
// owner of the table's heap (daSalvage_c::_create keeps the first one,
// the others' create fails once they have registered, d_a_salvage.cpp:
// 88-119).
func salvageOwner(w *actors.Walk) bool {
	for i := range w.Actors {
		if a := &w.Actors[i]; a.Proc == addr.ProcSalvage && a.InitState == 2 {
			return true
		}
	}
	return false
}

// SalvagePlan is one tick's salvage catch-up: the table's address as read
// and the points to mark.
type SalvagePlan struct {
	Base   uint32
	Table  int8
	Points []SalvagePoint
}

// SalvageTick is what a tick's salvage plan tells the WriteGuard.
type SalvageTick struct {
	Base   uint32
	Points map[int]SalvagePoint // by index
}

// SalvageStatus is how the salvage catch-up stands: points marked emptied
// this session, the last plan's held ones and the last reason.
type SalvageStatus struct {
	Marked   int
	Held     int
	LastHeld string
}

// maxSalvageHeld bounds what the catch-up remembers of held points.
const maxSalvageHeld = addr.SalvageInfoCount

// salvageState is the replica's part of the catch-up.
type salvageState struct {
	plan     *SalvagePlan
	marked   int
	held     map[LandedBit]string // a point's key -> the last reason it was held
	lastHeld string
	told     string // the last table-level reason logged
}

func (s *salvageState) hold(k LandedBit, why string) {
	if s.held == nil || len(s.held) >= maxSalvageHeld {
		s.held = map[LandedBit]string{}
	}
	s.held[k] = why
	s.lastHeld = why
}

// salvageLanded reports whether some salvage bit or salvaged chart of the
// loaded table landed this visit.
func salvageLanded(l Landed, table int8) bool {
	for b := range l.At {
		if (b.Kind == LandOcean || b.Kind == LandChart) && b.Table == table {
			return true
		}
	}
	return false
}

// reconcileSalvage plans this tick's salvage catch-up from o (the gates
// above that the tick's snapshot answers); ApplySalvage makes it after r.mu
// is released. r.mu is held.
func (r *Replica) reconcileSalvage(o *obs, armed bool) {
	sv := &r.sv
	sv.plan = nil
	if !armed || !r.haveView || r.viewWorld != r.sessionWorld || r.writer.DryRun() {
		return
	}
	if off, _ := r.writer.Disabled(); off {
		return
	}
	if !o.consistent() || !o.st.CanWriteIdle() {
		return
	}
	s := o.s
	l := r.writer.Landed()
	if l.Stage != s.StageName || !salvageLanded(l, s.Table) {
		return
	}
	base, ps, why, err := readSalvageTable(r.m)
	if err != nil {
		why = "the salvage table is unreadable: " + err.Error()
	}
	if why != "" {
		r.salvageTold(why)
		return
	}
	settle := r.cfg.RefreshSettle
	if settle == 0 {
		settle = RefreshSettle
	}
	now := time.Now()
	var cands []SalvagePoint
	for _, p := range ps {
		k, ok := p.Key(s.Table)
		if !ok || !p.Usable() {
			continue
		}
		at, ok := l.Has(k)
		if !ok {
			continue
		}
		if settle > 0 && now.Sub(at) < settle {
			sv.hold(k, fmt.Sprintf("%s landed %v ago, settling for %v", k, now.Sub(at).Round(time.Millisecond), settle))
			continue
		}
		if !salvageBitSet(&o.raw, k) {
			sv.hold(k, fmt.Sprintf("%s reads clear now", k))
			continue
		}
		cands = append(cands, p)
	}
	if len(cands) == 0 {
		return
	}
	w, err := actors.Read(r.m, 0)
	switch {
	case err != nil:
		r.salvageTold("the actor walk failed: " + err.Error())
		return
	case !salvageOwner(&w):
		r.salvageTold("no daSalvage_c executes: the table may belong to a deleted owner")
		return
	}
	sv.told = ""
	sv.plan = &SalvagePlan{Base: base, Table: s.Table, Points: cands}
	if wg := r.writer.WriteGuard(); wg != nil {
		t := SalvageTick{Base: base, Points: map[int]SalvagePoint{}}
		for _, p := range cands {
			t.Points[p.Index] = p
		}
		wg.SetSalvage(t)
	}
}

// salvageTold logs why, a table-level reason the catch-up waits, once
// until it changes. r.mu is held.
func (r *Replica) salvageTold(why string) {
	sv := &r.sv
	sv.lastHeld = why
	if why != sv.told {
		sv.told = why
		r.logf("co-op: salvage: waiting: %s", why)
	}
}

// salvageBitSet reports whether k, a LandOcean or LandChart bit, reads set
// in img.
func salvageBitSet(img *save.Image, k LandedBit) bool {
	off, mask, ok := salvageBitOff(k)
	return ok && img[off]&mask != 0
}

// salvageBitOff is k's image offset and mask: a grid's u16 in mOcean, or
// chart k.No's bit of the map's "complete" words.
func salvageBitOff(k LandedBit) (off int, mask byte, ok bool) {
	switch k.Kind {
	case LandOcean:
		b, err := addr.OceanBit(int(k.Room), k.No)
		if err != nil {
			return 0, 0, false
		}
		return addr.OffOcean + b.Off, b.Mask, true
	case LandChart:
		if k.No < 1 || k.No > 128 {
			return 0, 0, false
		}
		n := k.No - 1
		word, b := n/32, n%32
		return addr.OffMapComplete + 4*word + 3 - b/8, 1 << (b % 8), true
	}
	return 0, 0, false
}

// applySalvage makes the tick's salvage catch-up, after every other path
// (rep.WriteErr is the first error they had): not at all when one stalled
// or the game moved on. It takes r.mu for the result.
func (r *Replica) applySalvage(ctx context.Context, p *SalvagePlan, rep *Report) {
	if p == nil || len(p.Points) == 0 || stopped(rep.WriteErr) {
		return
	}
	res, err := r.writer.ApplySalvage(ctx, *p)
	r.mu.Lock()
	defer r.mu.Unlock()
	sv := &r.sv
	rep.Wrote += len(res.Wrote)
	if rep.WriteErr == nil && err != nil {
		rep.WriteErr = err
	}
	var lines []string
	for _, d := range res.Done {
		sv.marked++
		if k, ok := d.Key(p.Table); ok {
			delete(sv.held, k)
		}
		lines = append(lines, fmt.Sprintf("salvage: marked %s emptied: the partner salvaged it", d))
	}
	for _, h := range res.Held {
		if k, ok := h.Point.Key(p.Table); ok {
			sv.hold(k, h.Why)
		}
	}
	if len(lines) > 0 {
		r.logf("co-op: salvage: wrote %d byte(s) after heartbeat %d", len(res.Wrote), res.Beat)
		r.logLines(lines)
	}
}

// salvageStatus is the catch-up's part of Status. r.mu is held.
func (r *Replica) salvageStatus() SalvageStatus {
	return SalvageStatus{Marked: r.sv.marked, Held: len(r.sv.held), LastHeld: r.sv.lastHeld}
}
