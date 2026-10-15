package bind

import (
	"errors"
	"fmt"
	"strings"

	"github.com/StephenSHorton/ww-multiplayer/internal/coop/addr"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/gate"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/mem"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/snap"
)

// A tag write happens in two steps, so the user always answers a prompt
// about exactly the write that will be made:
//
//  1. a Plan (PlanBind, PlanScrub, PlanClear) checks the snapshot of one
//     tick and says what it would write and why, in Prompt; nothing is
//     written;
//  2. after the user said yes to Prompt (Confirm), Bind or Scrub makes the
//     write on a later tick, if the gates are open on that tick and the
//     file is still the one the plan was made for, then reads it back.
//
// The gates are IDLE as well as STABLE and HEARTBEAT (gate.CanWriteIdle):
// a stage with Link is loaded and settled, so no file select (where
// card_to_memory rewrites mReserve), and no pause menu, so the save menu
// isn't copying mReserve to the card mid-write.

// Kind is what a plan writes.
type Kind uint8

const (
	KindBind  Kind = 1 + iota // a new tag
	KindScrub                 // zeros over a leaked tag (§5.2)
	KindClear                 // zeros over any tag, at coop-tag clear (§5.5)
)

func (k Kind) String() string {
	switch k {
	case KindBind:
		return "bind"
	case KindScrub:
		return "scrub"
	case KindClear:
		return "clear"
	}
	return fmt.Sprintf("Kind(%d)", uint8(k))
}

// The errors the plans and writers return, each matched with errors.Is.
var (
	ErrGate         = errors.New("bind: the gates are closed")
	ErrNotConfirmed = errors.New("bind: the user has not confirmed this write")
	ErrChanged      = errors.New("bind: the loaded file changed since the plan")
	ErrVerify       = errors.New("bind: the tag did not read back as written")
	ErrUsed         = errors.New("bind: this plan was already carried out")

	ErrAlreadyBound = errors.New("bind: the file is already bound to this world")
	ErrForeign      = errors.New("bind: the file belongs to another co-op world")
	ErrNotOurs      = errors.New("bind: dSv_reserve_c holds data that is not a co-op tag")
	ErrSecondQuest  = errors.New("bind: second-quest files can't join co-op")
	ErrNotLeaked    = errors.New("bind: the tag has not leaked")
	ErrNoTag        = errors.New("bind: there is no tag to clear")
)

// Plan is one tag write, checked against one snapshot and waiting for
// the user's yes. Make one with PlanBind, PlanScrub or PlanClear; carry
// it out at most once. A Plan is not safe for concurrent use.
type Plan struct {
	Kind Kind
	// File is the file the plan was made for. The write happens only
	// while the loaded file is still exactly this.
	File File
	// Verdict is File's classification when planned.
	Verdict Verdict
	// Tag is the tag a bind writes (KindBind only).
	Tag Tag
	// Fresh is set on a bind of a file that has never been saved and
	// loaded back (mDateIPL 0). Its tag passes V5 only while the world
	// file records Tag.Nonce as a pending fresh bind, so the caller
	// records it there before calling Bind.
	Fresh bool
	// Prompt is what to ask the user, ending in a yes/no question.
	Prompt string

	write     [TagSize]byte
	confirmed bool
	used      bool
}

// Bytes returns the TagSize bytes the plan writes at 0x803C532C.
func (p *Plan) Bytes() [TagSize]byte { return p.write }

// Confirm records that the user said yes to p.Prompt. Only a confirmed
// plan is carried out.
func (p *Plan) Confirm() { p.confirmed = true }

// BindRequest is what a bind needs from the world.
type BindRequest struct {
	World WorldID // the world to bind to; required
	// Nonce is the bind's nonce; zero mints a new one.
	Nonce Nonce
	// WorldSeq is the world's seq now (tag +0x18).
	WorldSeq uint64
	// Fresh is the world file's pending fresh binds (Expect.Fresh), used
	// to see whether the file is already bound.
	Fresh func(Nonce) bool
}

// PlanBind plans binding the loaded file to req.World (§5.3). It refuses
// while the gates are closed, for a file bound to this world already, for
// one bound to another world (unbind it with coop-tag clear first), for
// bytes in dSv_reserve_c that are not a co-op tag, and for a second-quest
// file (a non-goal, §2; V3 refuses them anyway). A leaked or damaged tag
// is written over.
func PlanBind(s *snap.Snapshot, st gate.Status, req BindRequest) (*Plan, error) {
	if err := planGate(st); err != nil {
		return nil, err
	}
	if req.World.IsZero() {
		return nil, errors.New("bind: no world to bind to")
	}
	f := FileOf(s)
	if f.Slot >= FileSlots {
		return nil, fmt.Errorf("bind: mDataNum is %d, not a file slot 0-%d", f.Slot, FileSlots-1)
	}
	if f.ClearCount != 0 {
		return nil, fmt.Errorf("%w (mClearCount %d)", ErrSecondQuest, f.ClearCount)
	}
	v := Classify(f, Expect{World: req.World, Fresh: req.Fresh})
	switch {
	case v.State == Bound:
		return nil, ErrAlreadyBound
	case v.State == Foreign:
		return nil, fmt.Errorf("%w: %s; clear the tag with coop-tag clear first", ErrForeign, v.Why)
	case v.Garbage:
		return nil, fmt.Errorf("%w (% X ...); if nothing else uses it, zero it with coop-tag clear",
			ErrNotOurs, f.Tag[:8])
	}
	nonce := req.Nonce
	if nonce.IsZero() {
		var err error
		if nonce, err = NewNonce(); err != nil {
			return nil, err
		}
	}
	t := Tag{
		Layout:     LayoutV1,
		Slot:       f.Slot,
		ClearCount: f.ClearCount,
		World:      req.World,
		Nonce:      nonce,
		WorldSeq:   req.WorldSeq,
		NameHash:   NameHash(f.Name[:]),
	}
	p := &Plan{Kind: KindBind, File: f, Verdict: v, Tag: t, Fresh: f.DateIPL == 0, write: t.Encode()}
	p.Prompt = bindPrompt(p)
	return p, nil
}

// PlanScrub plans zeroing a leaked tag (§5.2): one that fails V2-V5
// under e. Anything else is refused with ErrNotLeaked.
func PlanScrub(s *snap.Snapshot, st gate.Status, e Expect) (*Plan, error) {
	if err := planGate(st); err != nil {
		return nil, err
	}
	f := FileOf(s)
	v := Classify(f, e)
	if v.State != Leaked {
		return nil, fmt.Errorf("%w: %s", ErrNotLeaked, v.Why)
	}
	p := &Plan{Kind: KindScrub, File: f, Verdict: v}
	p.Prompt = scrubPrompt(p)
	return p, nil
}

// PlanClear plans zeroing whatever tag bytes the file has, at the user's
// coop-tag clear (§5.5): the file leaves co-op at its next in-game save.
// With e it says in the prompt what the tag is; all-zero bytes are
// refused with ErrNoTag.
func PlanClear(s *snap.Snapshot, st gate.Status, e Expect) (*Plan, error) {
	if err := planGate(st); err != nil {
		return nil, err
	}
	f := FileOf(s)
	v := Classify(f, e)
	if v.Empty {
		return nil, ErrNoTag
	}
	p := &Plan{Kind: KindClear, File: f, Verdict: v}
	p.Prompt = clearPrompt(p)
	return p, nil
}

func planGate(st gate.Status) error {
	if !st.CanWriteIdle() {
		return fmt.Errorf("%w: %s", ErrGate, st.Why())
	}
	return nil
}

// Bind carries out a confirmed bind plan on the tick of snapshot s, whose
// gate status is st, and returns the tag written (see apply).
func Bind(m mem.Mem, p *Plan, s *snap.Snapshot, st gate.Status) (Tag, error) {
	if p == nil || p.Kind != KindBind {
		return Tag{}, errors.New("bind: Bind needs a bind plan")
	}
	if err := apply(m, p, s, st); err != nil {
		return Tag{}, err
	}
	return p.Tag, nil
}

// Scrub carries out a confirmed scrub or clear plan on the tick of
// snapshot s, whose gate status is st (see apply).
func Scrub(m mem.Mem, p *Plan, s *snap.Snapshot, st gate.Status) error {
	if p == nil || (p.Kind != KindScrub && p.Kind != KindClear) {
		return errors.New("bind: Scrub needs a scrub or clear plan")
	}
	return apply(m, p, s, st)
}

// apply makes p's write, in order:
//
//  1. p is confirmed and not yet used, and the gates are open on this
//     tick (CanWriteIdle);
//  2. the file in s, and the file read from memory now, are both p.File:
//     the game has not loaded, renamed or re-saved anything since the
//     user saw the prompt;
//  3. the TagSize bytes are written at 0x803C532C in one Write, the only
//     write this package makes;
//  4. they read back as written, and the rest of the file is unchanged.
//
// A failure before the write leaves memory untouched. ErrVerify means the
// write was made but didn't stick; the plan is used either way.
func apply(m mem.Mem, p *Plan, s *snap.Snapshot, st gate.Status) error {
	switch {
	case p.used:
		return ErrUsed
	case !p.confirmed:
		return ErrNotConfirmed
	case !st.CanWriteIdle():
		return fmt.Errorf("%w: %s", ErrGate, st.Why())
	case s == nil || FileOf(s) != p.File:
		return fmt.Errorf("%w (this tick's snapshot differs)", ErrChanged)
	}
	now, err := ReadFile(m)
	if err != nil {
		return err
	}
	if now != p.File {
		return fmt.Errorf("%w (memory differs from the snapshot)", ErrChanged)
	}
	p.used = true
	if err := writeTag(m, p.write); err != nil {
		return err
	}
	after, err := ReadFile(m)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrVerify, err)
	}
	want := p.File
	want.Tag = p.write
	if after != want {
		return fmt.Errorf("%w: read back % X", ErrVerify, after.Tag[:])
	}
	return nil
}

// writeTag is the package's only memory write: b over the tag bytes of
// mReserve, nothing before them and nothing past +0x2F.
func writeTag(m mem.Mem, b [TagSize]byte) error {
	if err := m.Write(addr.Reserve, b[:]); err != nil {
		return fmt.Errorf("bind: writing the tag: %w", err)
	}
	return nil
}

// TagRegion is the memory Bind and Scrub may write: the tag's 0x30 bytes
// at the start of mReserve.
var TagRegion = addr.Region{Name: "co-op tag", Start: addr.Reserve, End: addr.Reserve + TagSize}

// Describe is f in one line for prompts and coop-tag show.
func Describe(f File) string {
	saved := "saved and loaded"
	if f.DateIPL == 0 {
		saved = "never saved and loaded back"
	}
	return fmt.Sprintf("file slot %d, %q, clear count %d, %s", f.Slot, f.PlayerName(), f.ClearCount, saved)
}

func bindPrompt(p *Plan) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Bind this save file to co-op world %s?\n", p.Tag.World)
	fmt.Fprintf(&b, "  %s\n", Describe(p.File))
	failed := p.Verdict.Failed()
	switch {
	case p.Verdict.State == Leaked && p.Verdict.Tag.World != p.Tag.World && len(failed) == 1 && failed[0] == V5:
		// Only the tag's own world's host knows that world's pending
		// fresh binds, so this may be one of them (Appendix C items 49
		// and 133).
		fmt.Fprintf(&b, "  Its current tag, of world %s, fails only V5 (a never-saved New Game): it may\n", p.Verdict.Tag.World)
		b.WriteString("  be a fresh bind to that world not yet loaded from the card, or a tag leaked from another\n")
		b.WriteString("  file, and only that world's host can tell. Binding replaces it; if it is that world's\n")
		b.WriteString("  bind, the file leaves that world.\n")
	case p.Verdict.State == Leaked:
		fmt.Fprintf(&b, "  Its current tag is stale and will be replaced: %s.\n", p.Verdict.Why)
	case p.Verdict.Damaged:
		b.WriteString("  Its damaged co-op tag will be replaced.\n")
	}
	fmt.Fprintf(&b, "  ww-multiplayer writes a 0x%X-byte tag at 0x%08X (dSv_reserve_c, which the game\n", TagSize, addr.Reserve)
	b.WriteString("  saves with the file and never reads). It lands on the memory card at your next\n")
	b.WriteString("  in-game save. Nothing else in the file is written.\n")
	if p.Fresh {
		b.WriteString("  The file has not been saved yet: save in-game soon so the card holds the binding.\n")
	}
	b.WriteString("Bind it?")
	return b.String()
}

func scrubPrompt(p *Plan) string {
	var b strings.Builder
	fmt.Fprintf(&b, "This file carries a co-op tag for world %s that it was never bound with:\n", p.Verdict.Tag.World)
	fmt.Fprintf(&b, "  %s\n", Describe(p.File))
	for _, c := range p.Verdict.Checks {
		if c.Ran && !c.Pass {
			fmt.Fprintf(&b, "  %s failed: %s\n", c.Rule, c.Details)
		}
	}
	fmt.Fprintf(&b, "  Zeroing its 0x%X bytes at 0x%08X keeps the stale tag out of this file's next save.\n", TagSize, addr.Reserve)
	b.WriteString("  Nothing else is written.\n")
	b.WriteString("Scrub it?")
	return b.String()
}

func clearPrompt(p *Plan) string {
	var b strings.Builder
	v := p.Verdict
	switch {
	case v.HasTag:
		fmt.Fprintf(&b, "Clear this file's co-op tag (world %s, %s)?\n", v.Tag.World, v.State)
	default:
		fmt.Fprintf(&b, "Zero the first 0x%X bytes of this file's dSv_reserve_c (%s)?\n", TagSize, v.Why)
	}
	fmt.Fprintf(&b, "  %s\n", Describe(p.File))
	fmt.Fprintf(&b, "  ww-multiplayer zeroes 0x%X bytes at 0x%08X and writes nothing else.\n", TagSize, addr.Reserve)
	b.WriteString("  The file leaves co-op at your next in-game save; until then, loading it again\n")
	b.WriteString("  from the card brings the tag back.\n")
	b.WriteString("Clear it?")
	return b.String()
}
