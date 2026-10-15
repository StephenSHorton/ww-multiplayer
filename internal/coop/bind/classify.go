package bind

import (
	"encoding/binary"
	"errors"
	"fmt"
	"strings"

	"github.com/StephenSHorton/ww-multiplayer/internal/coop/addr"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/mem"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/snap"
)

// File is the loaded save file as the tag rules see it: the tag bytes and
// the four facts V2-V5 compare them with. It is comparable, so a plan can
// tell whether the file changed under it.
type File struct {
	Tag        [TagSize]byte        // mReserve +0x00..+0x2F, 0x803C532C
	Slot       uint8                // mDataNum, 0x803C5E98 (d_save.h:998)
	ClearCount uint8                // mClearCount, 0x803C4DA8 (d_save.h:467)
	Name       [PlayerNameSize]byte // mPlayerName as stored, 0x803C4D64 (d_save.h:463)
	DateIPL    uint64               // status_b mDateIPL, 0x803C4C20 (d_save.h:72)
}

// PlayerName is the name before its NUL.
func (f File) PlayerName() string {
	n := f.Name[:]
	for i, c := range n {
		if c == 0 {
			n = n[:i]
			break
		}
	}
	return string(n)
}

// The File fields' offsets in the dSv_info_c bytes of a snapshot.
const (
	infoOffTag     = addr.InfoOffSavedata + addr.OffReserve
	infoOffDateIPL = addr.InfoOffSavedata + addr.OffDateIPL
	infoOffName    = addr.InfoOffSavedata + addr.OffPlayerName
	infoOffClear   = addr.InfoOffSavedata + addr.OffClearCount
)

// FileOf reads the File out of a snapshot's save bytes.
func FileOf(s *snap.Snapshot) File {
	var f File
	sv := s.Save[:]
	copy(f.Tag[:], sv[infoOffTag:])
	f.Slot = s.DataNum
	f.ClearCount = sv[infoOffClear]
	copy(f.Name[:], sv[infoOffName:])
	f.DateIPL = binary.BigEndian.Uint64(sv[infoOffDateIPL:])
	return f
}

// ReadFile reads the File from memory now: the tag, mDataNum,
// mClearCount, mPlayerName and mDateIPL, each read once.
func ReadFile(m mem.Mem) (File, error) {
	var f File
	tag, err := m.Read(addr.Reserve, TagSize)
	if err != nil {
		return f, fmt.Errorf("bind: reading the tag: %w", err)
	}
	copy(f.Tag[:], tag)
	if f.Slot, err = mem.ReadU8(m, addr.DataNum); err != nil {
		return f, fmt.Errorf("bind: reading mDataNum: %w", err)
	}
	if f.ClearCount, err = mem.ReadU8(m, addr.ClearCount); err != nil {
		return f, fmt.Errorf("bind: reading mClearCount: %w", err)
	}
	name, err := m.Read(addr.PlayerName, PlayerNameSize)
	if err != nil {
		return f, fmt.Errorf("bind: reading mPlayerName: %w", err)
	}
	copy(f.Name[:], name)
	date, err := m.Read(addr.DateIPL, 8)
	if err != nil {
		return f, fmt.Errorf("bind: reading mDateIPL: %w", err)
	}
	f.DateIPL = binary.BigEndian.Uint64(date)
	return f, nil
}

// State is a file's classification. The values are CO_HELLO's fileState
// byte (§4.4).
type State uint8

const (
	// Unbound: no co-op tag, or bytes that are not a valid one (V1).
	Unbound State = 0
	// Bound: a valid tag that passes V2-V6: this file is bound to the
	// session's world (or, with no session world given, to its tag's).
	Bound State = 1
	// Leaked: a valid tag the file was never bound with (fails one of
	// V2-V5). Go scrubs it so it never reaches the file's next save.
	Leaked State = 2
	// Foreign: a real binding to another world (fails V6 only), or a tag
	// of a layout this version can't read. Refused; never written.
	Foreign State = 3
)

func (s State) String() string {
	switch s {
	case Unbound:
		return "unbound"
	case Bound:
		return "bound"
	case Leaked:
		return "leaked"
	case Foreign:
		return "foreign"
	}
	return fmt.Sprintf("State(%d)", uint8(s))
}

// Rule is one of the validity rules V1-V6 (§5.2).
type Rule uint8

const (
	V1 Rule = 1 + iota // magic, layout and CRC are valid
	V2                 // tag slot == mDataNum
	V3                 // tag clear count == mClearCount
	V4                 // tag name hash == NameHash(mPlayerName)
	V5                 // mDateIPL != 0, or the bind is a pending fresh bind
	V6                 // tag world == the session's world
)

func (r Rule) String() string { return fmt.Sprintf("V%d", uint8(r)) }

// Expect is what the rules compare a tag with beyond the file itself.
type Expect struct {
	// World is the session's world. Zero means none is known (coop-tag
	// show without one): V6 is then skipped, and a tag that passes V1-V5
	// is Bound to its own world.
	World WorldID
	// Fresh reports whether a bind nonce is a pending fresh bind recorded
	// in the world file (§5.2 V5): a file bound before it was ever saved
	// and loaded back, which therefore still has mDateIPL 0. Nil means
	// none is.
	Fresh func(Nonce) bool
}

// Check is one rule's outcome.
type Check struct {
	Rule    Rule
	Ran     bool // false when there is no tag, or an earlier failure or no session world left it unchecked
	Pass    bool
	Details string
}

// Verdict is Classify's result.
type Verdict struct {
	State State
	// Tag is the decoded tag; valid only when HasTag.
	Tag    Tag
	HasTag bool
	// Checks holds V1-V6 in order.
	Checks [6]Check
	// Why is a one-line reason for State, for logs and prompts.
	Why string

	// Empty: all TagSize bytes are zero (every vanilla file).
	Empty bool
	// Garbage: non-zero bytes that are not a co-op tag (ErrMagic). Some
	// other tool may own them, so Bind refuses to write over them.
	Garbage bool
	// Damaged: a layout-1 tag that fails its CRC or zero checks. It is
	// ours, so Bind may write over it.
	Damaged bool
	// Fresh: V5 held only because the nonce is a pending fresh bind.
	Fresh bool
	// WorldChecked: V6 ran (Expect.World was given).
	WorldChecked bool
}

// Failed returns the rules that ran and failed, in order.
func (v Verdict) Failed() []Rule {
	var out []Rule
	for _, c := range v.Checks {
		if c.Ran && !c.Pass {
			out = append(out, c.Rule)
		}
	}
	return out
}

// Classify applies V1-V6 to f's tag:
//
//   - no tag, or one failing V1 because it isn't a co-op tag or is
//     damaged: Unbound;
//   - a tag of a newer layout (V1): Foreign, as this version can't judge
//     it;
//   - a tag failing any of V2-V5: Leaked, whatever world it names (a
//     stale tag binds no file to any world);
//   - a tag failing only V6: Foreign;
//   - otherwise Bound.
func Classify(f File, e Expect) Verdict {
	var v Verdict
	for i := range v.Checks {
		v.Checks[i].Rule = Rule(i + 1)
	}
	set := func(r Rule, pass bool, format string, a ...any) {
		v.Checks[r-1] = Check{Rule: r, Ran: true, Pass: pass, Details: fmt.Sprintf(format, a...)}
	}

	t, err := Decode(f.Tag[:])
	switch {
	case errors.Is(err, ErrEmpty):
		// No tag at all: no rule has anything to judge.
		v.State, v.Empty = Unbound, true
		v.Checks[0].Details = fmt.Sprintf("no tag: all 0x%X bytes zero", TagSize)
		v.Why = "no co-op tag"
		return v
	case errors.Is(err, ErrMagic):
		v.State, v.Garbage = Unbound, true
		set(V1, false, "%v", err)
		v.Why = "dSv_reserve_c holds bytes that are not a co-op tag"
		return v
	case errors.Is(err, ErrLayout):
		v.State = Foreign
		set(V1, false, "%v", err)
		v.Why = "a co-op tag written by a newer ww-multiplayer"
		return v
	case err != nil:
		v.State, v.Damaged = Unbound, true
		set(V1, false, "%v", err)
		v.Why = "a damaged co-op tag, which binds nothing"
		return v
	}
	v.Tag, v.HasTag = t, true
	set(V1, true, "layout %d, CRC good", t.Layout)

	set(V2, t.Slot == f.Slot, "tag slot %d, loaded file slot %d", t.Slot, f.Slot)
	set(V3, t.ClearCount == f.ClearCount, "tag clear count %d, file clear count %d", t.ClearCount, f.ClearCount)
	nh := NameHash(f.Name[:])
	set(V4, t.NameHash == nh, "tag name hash %08x, %q hashes to %08x", t.NameHash, f.PlayerName(), nh)
	switch {
	case f.DateIPL != 0:
		set(V5, true, "the file has been saved and loaded (mDateIPL %#x)", f.DateIPL)
	case e.Fresh != nil && e.Fresh(t.Nonce):
		v.Fresh = true
		set(V5, true, "never saved and loaded, but nonce %s is a pending fresh bind", t.Nonce)
	default:
		set(V5, false, "mDateIPL is 0: a file never saved and loaded, and nonce %s is no pending fresh bind", t.Nonce)
	}
	if failed := v.Failed(); len(failed) > 0 {
		v.State = Leaked
		v.Why = "the tag leaked from another file (" + leakReason(failed) + ")"
		return v
	}

	if e.World.IsZero() {
		v.State = Bound
		v.Why = fmt.Sprintf("bound to world %s (no session world to check it against)", t.World)
		return v
	}
	v.WorldChecked = true
	set(V6, t.World == e.World, "tag world %s, session world %s", t.World, e.World)
	if t.World != e.World {
		v.State = Foreign
		v.Why = fmt.Sprintf("bound to another co-op world, %s", t.World)
		return v
	}
	v.State = Bound
	v.Why = fmt.Sprintf("bound to this world, %s", t.World)
	return v
}

// leakReason names what each failed rule catches (§5.2's table).
func leakReason(failed []Rule) string {
	var parts []string
	for _, r := range failed {
		switch r {
		case V2:
			parts = append(parts, "V2: carried into another slot")
		case V3:
			parts = append(parts, "V3: a second quest")
		case V4:
			parts = append(parts, "V4: another name")
		case V5:
			parts = append(parts, "V5: a never-saved New Game")
		}
	}
	return strings.Join(parts, "; ")
}
