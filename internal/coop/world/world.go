// Package world is the state the CoopHub keeps for one co-op world (§4.4):
//
//   - the merged 0x778-byte dSv_save_c image, with the per-class merge of
//     internal/coop/class, a tombstone set and a monotonic seq (World);
//   - the dan, zone and kill stores, scoped by the players' presence and
//     dropped when their last occupant leaves (Scopes);
//   - the world file that keeps a world across sessions: world.wwco,
//     written atomically, with a journal, rotated backups and a debounced
//     flush (Store).
//
// The merge is a join: every operation takes the image to its per-byte
// least upper bound with what was proposed, so the order, duplication and
// batching of proposals never change the result, and no byte is ever
// lowered. OR and OR_MASKED bytes merge by OR within their class mask, MAX
// bytes by MAX of the masked value; DERIVED, PER_PLAYER, NEVER and TAG
// bytes are never merged and are zero in the world image (receivers derive
// or keep their own, §3). The only way a bit leaves the image is a
// tombstone, which the host sets on purpose and which then refuses that
// bit for good.
//
// Nothing here is safe for concurrent use but Store, which the hub's own
// goroutine and the store's flush timer share.
//
// Decomp citations are zeldaret/tww at 4d78c994, as in internal/coop/addr.
package world

import (
	"bytes"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/internal/coop/addr"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/class"
)

// ImageSize is the size of the canonical image, dSv_save_c (d_save.h:916).
const ImageSize = addr.SaveImageSize

// Image is the canonical dSv_save_c image (§3): the saved image with the
// current table's live memBit folded into its saved slot, indexed by
// addr.Off* offsets. It has the layout of wire.Image and snap's image, so
// either converts to it.
type Image [ImageSize]byte

// ID is a worldID or a bindNonce: 8 bytes minted by Go at a bind (§5.3).
// It has the layout of bind.WorldID, bind.Nonce and wire.ID.
type ID [8]byte

func (id ID) String() string { return hex.EncodeToString(id[:]) }

// IsZero reports whether id is the zero ID, which names no world.
func (id ID) IsZero() bool { return id == ID{} }

// ParseID parses the 16 hex digits String prints.
func ParseID(s string) (ID, error) {
	var id ID
	if len(s) != 2*len(id) {
		return id, fmt.Errorf("world: ID %q is not %d hex digits", s, 2*len(id))
	}
	if _, err := hex.Decode(id[:], []byte(s)); err != nil {
		return ID{}, fmt.Errorf("world: ID %q is not hex", s)
	}
	return id, nil
}

// Gain is one image byte that gained bits: for an OR or OR_MASKED byte
// the bits gained, for a MAX byte its new value within the class mask. It
// has the layout and meaning of wire.Gain.
type Gain struct {
	Off uint16
	Val byte
}

// shared[off] is the bits of image byte off the world holds: the class
// mask of an OR, OR_MASKED or MAX byte, nothing of any other byte.
// isMax[off] marks the MAX bytes and isMasked[off] the OR_MASKED ones.
var shared, isMax, isMasked = func() (m [ImageSize]byte, x, o [ImageSize]bool) {
	for off := range m {
		if r := class.Of(off); r.Class.Merges() {
			m[off] = r.Mask
			x[off] = r.Class == class.Max
			o[off] = r.Class == class.ORMasked
		}
	}
	return m, x, o
}()

// SharedMask returns the bits of image byte off that the world can hold:
// its class mask if it merges, else 0 (also for an offset outside the
// image).
func SharedMask(off int) byte {
	if off < 0 || off >= ImageSize {
		return 0
	}
	return shared[off]
}

// Shared returns img with every bit the world never holds cleared: the
// DERIVED, PER_PLAYER, NEVER and TAG bytes, and the bits of merging bytes
// outside their class mask.
func Shared(img *Image) Image {
	var out Image
	for off := range out {
		out[off] = img[off] & shared[off]
	}
	return out
}

// Join returns the per-class merge of a and b: OR of the masked bits for
// OR and OR_MASKED bytes, the larger masked value for MAX bytes, zero for
// the bytes that never merge. It is commutative, associative and
// idempotent, and never lowers a shared byte of either side.
func Join(a, b *Image) Image {
	var out Image
	for off := range out {
		x, y := a[off]&shared[off], b[off]&shared[off]
		if isMax[off] {
			out[off] = max(x, y)
		} else {
			out[off] = x | y
		}
	}
	return out
}

// Diff returns the gains that take from to Join(from, to), in ascending
// offset order: for an OR byte the shared bits of to that from lacks, for
// a MAX byte to's masked value where it is above from's. It is empty
// exactly when to adds nothing to from, so a replica's Differ can use it
// for rising edges and a preflight for "is this file a subset".
func Diff(from, to *Image) []Gain {
	var gs []Gain
	for off := range from {
		f, t := from[off]&shared[off], to[off]&shared[off]
		var v byte
		if isMax[off] {
			if t > f {
				v = t
			}
		} else {
			v = t &^ f
		}
		if v != 0 {
			gs = append(gs, Gain{Off: uint16(off), Val: v})
		}
	}
	return gs
}

// Apply merges gs into img by the class rules and returns how many bytes
// changed. A gain for an offset outside the image or a byte that does not
// merge is skipped, and bits outside the class mask are ignored (a MAX
// value with any is skipped whole). Replicas use it to fold a GAINS relay
// into their view of the world.
func (img *Image) Apply(gs []Gain) int {
	before := *img
	for _, g := range gs {
		off := int(g.Off)
		if off >= ImageSize {
			continue
		}
		m := shared[off]
		switch {
		case m == 0:
		case isMax[off]:
			if g.Val&^m == 0 && g.Val > img[off]&m {
				img[off] = img[off]&^m | g.Val
			}
		default:
			img[off] |= g.Val & m
		}
	}
	n := 0
	for off := range img {
		if img[off] != before[off] {
			n++
		}
	}
	return n
}

// BitSet is a set of image bits: Mask(off) is the set's bits of byte off.
// The world keeps its tombstones in one; a replica can keep its ever-had
// set (§4.2 Planner) in another.
type BitSet [ImageSize]byte

// Has reports whether every bit of mask at off is in the set. It is false
// for an offset outside the image.
func (s *BitSet) Has(off int, mask byte) bool {
	return off >= 0 && off < ImageSize && s[off]&mask == mask
}

// Mask returns the set's bits of byte off (0 outside the image).
func (s *BitSet) Mask(off int) byte {
	if off < 0 || off >= ImageSize {
		return 0
	}
	return s[off]
}

// Add puts the bits of mask at off into the set and reports whether any
// was new. An offset outside the image adds nothing.
func (s *BitSet) Add(off int, mask byte) bool {
	if off < 0 || off >= ImageSize || s[off]&mask == mask {
		return false
	}
	s[off] |= mask
	return true
}

// AddImage puts every set bit of img into the set.
func (s *BitSet) AddImage(img *Image) {
	for off := range s {
		s[off] |= img[off]
	}
}

// Count is the number of bits in the set.
func (s *BitSet) Count() int {
	n := 0
	for _, b := range s {
		for ; b != 0; b &= b - 1 {
			n++
		}
	}
	return n
}

// Entries lists the set as {offset, bits} in ascending offset order.
func (s *BitSet) Entries() []Gain {
	var gs []Gain
	for off, b := range s {
		if b != 0 {
			gs = append(gs, Gain{Off: uint16(off), Val: b})
		}
	}
	return gs
}

// Policy narrows what the world accepts below the class masks: p[off] is
// the bits of byte off a proposal may set (for a MAX byte, the bits its
// value may have). Bits outside the class mask never count.
//
// OR_MASKED bytes are the memory switches and the story bits, whose
// syncable subset later milestones supply (the M1 latching-switch table,
// §6.2; the M4 SyncMask, §6.5). A bit that once enters the world stays,
// so the default policy fails closed on them: nothing of an OR_MASKED byte
// is accepted until Grant, GrantSwitch or GrantEventBit opens it.
type Policy [ImageSize]byte

// DefaultPolicy accepts every class-mask bit of the OR and MAX bytes and
// nothing of the OR_MASKED ones.
func DefaultPolicy() *Policy {
	var p Policy
	for off := range p {
		if !isMasked[off] {
			p[off] = shared[off]
		}
	}
	return &p
}

// FullPolicy accepts every class-mask bit, OR_MASKED bytes included: the
// most the class table allows. For tests and offline tools.
func FullPolicy() *Policy {
	p := Policy(shared)
	return &p
}

// Grant opens bits of byte off, within its class mask.
func (p *Policy) Grant(off int, bits byte) {
	if off >= 0 && off < ImageSize {
		p[off] |= bits & shared[off]
	}
}

// GrantSwitch opens memory switch no (0x00-0x7F) of save table t: the bit
// dSv_memBit_c::onSwitch sets (d_save.cpp:1084-1087) in t's saved slot.
// Dan and zone switches are not in the image; they travel as SCOPED
// (Scopes).
func (p *Policy) GrantSwitch(t, no int) error {
	if !addr.ValidTable(t) {
		return fmt.Errorf("world: save table %d out of range 0..%d", t, addr.TableCount-1)
	}
	store, b, err := addr.SwitchBit(no)
	if err != nil {
		return fmt.Errorf("world: %w", err)
	}
	if store != addr.SwitchMemory {
		return fmt.Errorf("world: switch 0x%X is a %s switch, not in the image", no, store)
	}
	p.Grant(addr.OffMemBit(t)+b.Off, b.Mask)
	return nil
}

// GrantSwitches opens every memory switch that synced reports as synced:
// the M1 latching-switch table's verdict for (save table, switch number)
// (§6.2; switches.Table.Synced).
func (p *Policy) GrantSwitches(synced func(t, no int) bool) {
	for t := 0; t < addr.TableCount; t++ {
		for no := 0; no < addr.MemBitSwitchCount; no++ {
			if synced(t, no) {
				p.GrantSwitch(t, no)
			}
		}
	}
}

// GrantEventBit opens the named event bits of id 0xHHLL: byte HH, mask LL
// (onEventBit, d_save.cpp:1187-1189). Every bit of LL must be a named one
// (class OR_MASKED event bytes 0x00-0x41).
func (p *Policy) GrantEventBit(id uint16) error {
	b := addr.EventByte(id)
	off := addr.OffEvent + b.Off
	if b.Off >= addr.EventBitBytes || b.Mask == 0 || b.Mask&^shared[off] != 0 {
		return fmt.Errorf("world: event bit 0x%04X is not a named event bit", id)
	}
	p.Grant(off, b.Mask)
	return nil
}

// Reason is why the world refused (part of) a proposal.
type Reason uint8

const (
	ReasonOffset    Reason = iota + 1 // the offset is outside the image
	ReasonClass                       // the byte does not merge (DERIVED, PER_PLAYER, NEVER, TAG)
	ReasonMask                        // bits outside the byte's class mask
	ReasonPolicy                      // bits the policy does not open
	ReasonTombstone                   // bits the host tombstoned
)

func (r Reason) String() string {
	switch r {
	case ReasonOffset:
		return "offset outside the image"
	case ReasonClass:
		return "byte does not merge"
	case ReasonMask:
		return "bits outside the class mask"
	case ReasonPolicy:
		return "not allowed by the policy"
	case ReasonTombstone:
		return "tombstoned"
	}
	return fmt.Sprintf("Reason(%d)", uint8(r))
}

// Reject is one refused part of a proposal: the bits refused at Off (for a
// MAX byte, the whole value) and why.
type Reject struct {
	Off uint16
	Val byte
	Why Reason
}

// Result is what one world operation did.
type Result struct {
	// Seq is the world's seq after the operation.
	Seq uint64
	// Delta is the effective change, the bytes that changed in ascending
	// offset order, as gains: what the hub relays in a GAINS s->c.
	Delta []Gain
	// Rejected is what the operation refused, in proposal order.
	Rejected []Reject
	// Cleared is, for Tombstone, the image bits it removed.
	Cleared []Gain
}

// Changed reports whether the operation changed the image. (A tombstone
// on bits the image lacks changes only the tombstones; Seq tells that.)
func (r Result) Changed() bool { return len(r.Delta) > 0 || len(r.Cleared) > 0 }

// MaxFreshBinds caps the pending fresh binds a world records. A fresh
// bind is cleared once its file is seen saved and loaded (§5.2 rule V5);
// one that never is (a New Game bound and abandoned) is dropped, oldest
// first, when the cap is reached.
const MaxFreshBinds = 16

// FreshBind is a pending fresh bind: the bindNonce Go minted for a file
// that had never been saved (mDateIPL 0, §5.2 V5), and when.
type FreshBind struct {
	Nonce  ID
	Minted time.Time
}

// World is one co-op world: its ID, the merged image, the tombstones, the
// pending fresh binds and the seq. Every change to any of them bumps the
// seq by one, so seq orders the world's history (and the journal).
type World struct {
	id      ID
	created time.Time
	seq     uint64
	image   Image
	tomb    BitSet
	fresh   map[ID]time.Time
	policy  *Policy
}

// New returns an empty world: seq 0, nothing merged, the DefaultPolicy.
func New(id ID, created time.Time) *World {
	return &World{id: id, created: msTime(created), fresh: map[ID]time.Time{}, policy: DefaultPolicy()}
}

// msTime is t in UTC at the millisecond the world file keeps, or the zero
// time.
func msTime(t time.Time) time.Time {
	if t.IsZero() {
		return time.Time{}
	}
	return time.UnixMilli(t.UnixMilli()).UTC()
}

// ID is the world's ID.
func (w *World) ID() ID { return w.id }

// Created is when the world was founded.
func (w *World) Created() time.Time { return w.created }

// Seq is the world's seq: the number of changes since it was founded.
func (w *World) Seq() uint64 { return w.seq }

// Image returns a copy of the merged image.
func (w *World) Image() Image { return w.image }

// Tombstones returns a copy of the tombstone set.
func (w *World) Tombstones() BitSet { return w.tomb }

// Policy returns the policy proposals are checked against. It is not part
// of the world file: the hub sets it every session (SetPolicy).
func (w *World) Policy() *Policy {
	p := *w.policy
	return &p
}

// SetPolicy sets the policy future proposals are checked against. It does
// not touch the image: bits a narrower policy would refuse stay (receivers
// check their own policy before they write, §4.2).
func (w *World) SetPolicy(p *Policy) {
	c := *p
	w.policy = &c
}

// Clone returns an independent copy of w.
func (w *World) Clone() *World {
	c := *w
	c.fresh = make(map[ID]time.Time, len(w.fresh))
	for n, t := range w.fresh {
		c.fresh[n] = t
	}
	p := *w.policy
	c.policy = &p
	return &c
}

// ApplyGains merges a replica's proposed gains (GAINS c->s), in any order
// and with any duplicates, and returns the effective delta. Each gain is
// checked against the class table, the policy and the tombstones; what
// fails is refused and listed in Rejected, the rest merges: OR bits within
// the mask, or a MAX value above the current one.
func (w *World) ApplyGains(gs []Gain) Result {
	next := w.image
	var rej []Reject
	for _, g := range gs {
		off := int(g.Off)
		switch {
		case off >= ImageSize:
			rej = append(rej, Reject{Off: g.Off, Val: g.Val, Why: ReasonOffset})
		case shared[off] == 0:
			rej = append(rej, Reject{Off: g.Off, Val: g.Val, Why: ReasonClass})
		case isMax[off] && g.Val&^shared[off] != 0:
			rej = append(rej, Reject{Off: g.Off, Val: g.Val, Why: ReasonMask})
		default:
			v := g.Val
			if out := v &^ shared[off]; out != 0 {
				rej = append(rej, Reject{Off: g.Off, Val: out, Why: ReasonMask})
				v &= shared[off]
			}
			rej = w.merge(&next, off, v, w.policy, rej)
		}
	}
	return w.commit(&next, rej)
}

// Merge merges a whole image (WORLD c->s, at join, reconnect and resync)
// and returns the effective delta. Only shared bits count: the bytes that
// never merge and the bits outside a class mask are ignored, so an image
// straight from a snapshot is fine. Bits the policy does not open or the
// host tombstoned are refused and listed in Rejected.
func (w *World) Merge(img *Image) Result {
	next := w.image
	var rej []Reject
	for off := range img {
		if v := img[off] & shared[off]; v != 0 {
			rej = w.merge(&next, off, v, w.policy, rej)
		}
	}
	return w.commit(&next, rej)
}

// merge folds the class-masked value v of byte off into next under pol and
// the tombstones, appending what it refuses to rej.
func (w *World) merge(next *Image, off int, v byte, pol *Policy, rej []Reject) []Reject {
	allowed, tomb := shared[off]&pol[off], w.tomb[off]
	if isMax[off] {
		switch {
		case v&^allowed != 0:
			return append(rej, Reject{Off: uint16(off), Val: v, Why: ReasonPolicy})
		case v&tomb != 0:
			return append(rej, Reject{Off: uint16(off), Val: v, Why: ReasonTombstone})
		}
		next[off] = max(next[off], v)
		return rej
	}
	if out := v &^ allowed; out != 0 {
		rej = append(rej, Reject{Off: uint16(off), Val: out, Why: ReasonPolicy})
		v &= allowed
	}
	if out := v & tomb; out != 0 {
		rej = append(rej, Reject{Off: uint16(off), Val: out, Why: ReasonTombstone})
		v &^= tomb
	}
	next[off] |= v
	return rej
}

// commit makes next the image if it differs, bumping the seq.
func (w *World) commit(next *Image, rej []Reject) Result {
	delta := Diff(&w.image, next)
	if len(delta) > 0 {
		w.image = *next
		w.seq++
	}
	return Result{Seq: w.seq, Delta: delta, Rejected: rej}
}

// Tombstone blocks the bits of mask at image byte off for good (`coop-world
// block`, §4.4): they are cleared from the image, and every later proposal
// of them is refused. Replicas that already applied them keep them. For a
// MAX byte, a value with any tombstoned bit is refused (so tombstoning bit
// 1 of a letter register refuses 2 and 3), and the current value loses
// those bits. It fails for a byte that does not merge or a mask with no
// bit of the class mask.
func (w *World) Tombstone(off int, mask byte) (Result, error) {
	if off < 0 || off >= ImageSize {
		return Result{Seq: w.seq}, fmt.Errorf("world: offset 0x%X is outside the 0x%X-byte image", off, ImageSize)
	}
	r := class.Of(off)
	if !r.Class.Merges() {
		return Result{Seq: w.seq}, fmt.Errorf("world: image byte 0x%03X (%s) is %s and never merges", off, r.Field, r.Class)
	}
	if mask&shared[off] == 0 {
		return Result{Seq: w.seq}, fmt.Errorf("world: mask 0x%02X has no bit of %s's class mask 0x%02X", mask, r.Field, shared[off])
	}
	mask &= shared[off]
	if !w.tomb.Add(off, mask) {
		return Result{Seq: w.seq}, nil
	}
	var cleared []Gain
	if c := w.image[off] & mask; c != 0 {
		cleared = []Gain{{Off: uint16(off), Val: c}}
		w.image[off] &^= mask
	}
	w.seq++
	return Result{Seq: w.seq, Cleared: cleared}, nil
}

// AddFreshBind records nonce as a pending fresh bind minted at minted (§5.2
// V5) and reports whether it was new. Past MaxFreshBinds the oldest is
// dropped.
func (w *World) AddFreshBind(nonce ID, minted time.Time) (bool, error) {
	if nonce.IsZero() {
		return false, errors.New("world: a fresh bind needs a nonce")
	}
	if _, ok := w.fresh[nonce]; ok {
		return false, nil
	}
	w.fresh[nonce] = msTime(minted)
	for len(w.fresh) > MaxFreshBinds {
		old := w.FreshBinds()[0]
		delete(w.fresh, old.Nonce)
	}
	w.seq++
	return true, nil
}

// IsFreshBind reports whether nonce is a pending fresh bind: bind.Expect's
// Fresh.
func (w *World) IsFreshBind(nonce ID) bool {
	_, ok := w.fresh[nonce]
	return ok
}

// ClearFreshBind forgets the pending fresh bind nonce, once its file has
// been seen with mDateIPL != 0, and reports whether it was pending.
func (w *World) ClearFreshBind(nonce ID) bool {
	if _, ok := w.fresh[nonce]; !ok {
		return false
	}
	delete(w.fresh, nonce)
	w.seq++
	return true
}

// FreshBinds lists the pending fresh binds, oldest first (ties by nonce).
func (w *World) FreshBinds() []FreshBind {
	fs := make([]FreshBind, 0, len(w.fresh))
	for n, t := range w.fresh {
		fs = append(fs, FreshBind{Nonce: n, Minted: t})
	}
	slices.SortFunc(fs, func(a, b FreshBind) int {
		if c := a.Minted.Compare(b.Minted); c != 0 {
			return c
		}
		return bytes.Compare(a.Nonce[:], b.Nonce[:])
	})
	return fs
}
