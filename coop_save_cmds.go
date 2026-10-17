package main

// Co-op M4 offline save tools (docs/08-coop-design.md §3, §9 M4), on
// Dolphin's GCI files (or a raw card image, read only):
//
//   - save-diff <a> <b> [--slot n]: what differs between a slot of a and
//     one of b, per byte class (internal/coop/class), labelled as
//     coop-watch labels its edges;
//   - save-merge <a> <b> <out> [--slot n] [--iso <path>]: a's slot with
//     b's shared progress merged in, by the class table and the live
//     sync's own planner and holds, written to out, a new GCI file. With
//     --iso the M1 tables of the user's disc open the latching memory
//     switches and derive the DERIVED bytes (keys, max life, slots,
//     equipment, max magic); without it those are a's. a's co-op tag
//     stays only where it lets no unjudged progress into a world
//     (mergedTag).
//
// Neither touches Dolphin or an input file: save-merge refuses an out
// that exists or names an input, writes it exclusively, and reads it back
// to check its checksums before it reports success. Like the other co-op
// diagnostics, neither is listed in printHelp; the command registry
// (commands.go) dispatches them.

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"github.com/StephenSHorton/ww-multiplayer/internal/coop/bind"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/class"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/replica"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/savemerge"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/story"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/world"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/savefile"
)

const (
	saveDiffUsage  = "save-diff <a.gci> <b.gci> [--slot 0|1|2]"
	saveMergeUsage = "save-merge <a.gci> <b.gci> <out.gci> [--slot 0|1|2] [--iso <your Wind Waker iso|ciso>]"
)

// saveArgs are save-diff's and save-merge's parsed arguments.
type saveArgs struct {
	files []string // a, b, and save-merge's out
	slot  int      // -1: each file's only slot in use
	iso   string   // save-merge --iso
}

// parseSaveArgs reads n file paths and the flags, in any order; --slot
// and --iso take their value as the next argument or after "=", and
// --iso only when iso is true.
func parseSaveArgs(args []string, n int, iso bool) (saveArgs, error) {
	a := saveArgs{slot: -1}
	slotSet := false
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if !strings.HasPrefix(arg, "--") {
			a.files = append(a.files, arg)
			continue
		}
		name, value, hasValue := strings.Cut(strings.TrimPrefix(arg, "--"), "=")
		if name != "slot" && (name != "iso" || !iso) {
			return a, fmt.Errorf("unknown flag %s", arg)
		}
		if !hasValue {
			if i+1 >= len(args) {
				return a, fmt.Errorf("--%s needs a value", name)
			}
			i++
			value = args[i]
		}
		switch name {
		case "slot":
			if slotSet {
				return a, errors.New("--slot given twice")
			}
			v, err := strconv.Atoi(value)
			if err != nil || v < 0 || v >= savefile.Slots {
				return a, fmt.Errorf("--slot is 0, 1 or 2 (the file select's files top to bottom), not %q", value)
			}
			a.slot, slotSet = v, true
		case "iso":
			if a.iso != "" {
				return a, errors.New("--iso given twice")
			}
			if value == "" {
				return a, errors.New("--iso needs the path of your Wind Waker disc image")
			}
			a.iso = value
		}
	}
	if len(a.files) != n {
		return a, fmt.Errorf("give %d save files, not %d", n, len(a.files))
	}
	return a, nil
}

// runSaveDiff runs save-diff with the arguments after it.
func runSaveDiff(args []string) {
	a, err := parseSaveArgs(args, 2, false)
	if err != nil {
		exitUsage(err, saveDiffUsage)
	}
	exitOnCoopErr(saveDiff(os.Stdout, a))
}

// runSaveMerge runs save-merge with the arguments after it.
func runSaveMerge(args []string) {
	a, err := parseSaveArgs(args, 3, true)
	if err != nil {
		exitUsage(err, saveMergeUsage)
	}
	exitOnCoopErr(saveMerge(os.Stdout, a, loadCoopStageTables))
}

// saveInput is one input file's chosen slot.
type saveInput struct {
	path string
	kind savefile.Kind
	file *savefile.File
	slot *savefile.Slot
}

func (in *saveInput) String() string {
	return fmt.Sprintf("%s (%s, saved %d times), slot %d (file %d) %q",
		in.path, in.kind, in.file.SaveCount(), in.slot.N, in.slot.N+1, in.slot.PlayerName())
}

// readSaveInput reads path and picks its slot: n, or with n -1 the only
// slot in use. The slot must be a played file whose checksum holds.
func readSaveInput(path string, n int) (*saveInput, error) {
	f, kind, err := savefile.Read(path)
	if err != nil {
		return nil, err
	}
	in := &saveInput{path: path, kind: kind, file: f}
	slots := f.AllSlots()
	if n < 0 {
		var used []string
		for _, s := range slots {
			if s.State == savefile.InUse {
				used = append(used, fmt.Sprintf("%d %q", s.N, s.PlayerName()))
				in.slot = s
			}
		}
		switch len(used) {
		case 0:
			return nil, fmt.Errorf("%s: no slot holds a played file", path)
		case 1:
			return in, nil
		}
		return nil, fmt.Errorf("%s holds %d played files (slots %s): pick one with --slot", path, len(used), strings.Join(used, ", "))
	}
	in.slot = slots[n]
	if in.slot.State != savefile.InUse {
		return nil, fmt.Errorf("%s: slot %d (file %d) is %s", path, n, n+1, in.slot.State)
	}
	return in, nil
}

// readSaveInputs reads a and b of args.
func readSaveInputs(w io.Writer, args saveArgs) (a, b *saveInput, err error) {
	if a, err = readSaveInput(args.files[0], args.slot); err != nil {
		return nil, nil, err
	}
	if b, err = readSaveInput(args.files[1], args.slot); err != nil {
		return nil, nil, err
	}
	fmt.Fprintf(w, "a: %s\n", a)
	fmt.Fprintf(w, "b: %s\n", b)
	return a, b, nil
}

// saveClasses are the byte classes in the order the tools list them, with
// what each means for a merge.
var saveClasses = []struct {
	c    class.Class
	what string
}{
	{class.OR, "merge as a union"},
	{class.ORMasked, "merge within the sync policy: latching memory switches, the SyncMask's story bits"},
	{class.Max, "merge as the larger value"},
	{class.Derived, "each file computes them from the flags"},
	{class.PerPlayer, "each player's own"},
	{class.Never, "never synced"},
	{class.Tag, "the co-op binding tag"},
}

// edgesByClass groups the edges from a to b (coop-watch's diffSave over
// the image, whose offsets are dSv_info_c's) by the class of their byte.
// An inventory slot is named by its index: the class table splits mItems
// around the bottles, so coop-watch's span-relative label would not be.
// A flag bit outside a merging byte's class mask goes with PER_PLAYER, as
// it is each player's own (class.Rule): the bottle contents above bit 0
// of mGetItem[0..2], the Hero's Charm worn in mCollect[4].
func edgesByClass(a, b *world.Image) map[class.Class][]saveEdge {
	out := make(map[class.Class][]saveEdge)
	for _, e := range diffSave(a[:], b[:], -1) {
		if i := e.Off - addr.OffItems; i >= 0 && i < addr.SlotCount {
			e.Label = fmt.Sprintf("mItems[%d]", i)
		}
		r := class.Of(e.Off)
		c := r.Class
		if e.Bit >= 0 && c.Merges() && r.Mask&(1<<e.Bit) == 0 {
			c = class.PerPlayer
		}
		out[c] = append(out[c], e)
	}
	return out
}

// printEdges prints the edges of each class that has any, and returns
// how many there were.
func printEdges(w io.Writer, by map[class.Class][]saveEdge) int {
	n := 0
	for _, sc := range saveClasses {
		es := by[sc.c]
		if len(es) == 0 {
			continue
		}
		n += len(es)
		fmt.Fprintf(w, "\n%s (%s): %d\n", sc.c, sc.what, len(es))
		for _, e := range es {
			fmt.Fprintf(w, "  %s\n", e)
		}
	}
	return n
}

// saveDiff prints what differs between a's slot and b's.
func saveDiff(w io.Writer, args saveArgs) error {
	a, b, err := readSaveInputs(w, args)
	if err != nil {
		return err
	}
	fmt.Fprintln(w, `"+" is a bit b has and a lacks, "-" one a has and b lacks, "~" a value: a's -> b's.`)
	if printEdges(w, edgesByClass(&a.slot.Image, &b.slot.Image)) == 0 {
		fmt.Fprintln(w, "\nThe two slots are the same.")
	}
	return nil
}

// samePath reports whether p and q name the same file: the same absolute
// path (without case on Windows and macOS, whose file systems ignore it
// by default), or the same file on disk.
func samePath(p, q string) bool {
	ap, err1 := filepath.Abs(p)
	aq, err2 := filepath.Abs(q)
	if err1 == nil && err2 == nil {
		if ap == aq || (runtime.GOOS == "windows" || runtime.GOOS == "darwin") && strings.EqualFold(ap, aq) {
			return true
		}
	}
	sp, err1 := os.Stat(p)
	sq, err2 := os.Stat(q)
	return err1 == nil && err2 == nil && os.SameFile(sp, sq)
}

// saveMerge writes args.files[2], a's file with b's shared progress
// merged into a's slot. tablesFor loads the M1 tables of --iso.
func saveMerge(w io.Writer, args saveArgs, tablesFor func(iso string) (*coopStageTables, error)) error {
	out := args.files[2]
	for i, in := range args.files[:2] {
		if samePath(out, in) {
			return fmt.Errorf("%s is input %c: save-merge writes only a new file", out, 'a'+i)
		}
	}
	if _, err := os.Lstat(out); err == nil {
		return fmt.Errorf("%s exists: save-merge writes only a new file", out)
	} else if !errors.Is(err, fs.ErrNotExist) {
		return err
	}

	a, b, err := readSaveInputs(w, args)
	if err != nil {
		return err
	}
	if err := a.file.Header.CheckStat(); err != nil {
		return fmt.Errorf("a: %s: %w", a.path, err)
	}
	for _, in := range []*saveInput{a, b} {
		if cc := in.slot.Image[addr.OffClearCount]; cc != 0 {
			return fmt.Errorf("%s: slot %d is a second quest (clear count %d), which co-op doesn't merge", in.path, in.slot.N, cc)
		}
	}

	var warnings []string
	opt := savemerge.MergeOptions{}
	if args.iso != "" {
		st, err := tablesFor(args.iso)
		if err != nil {
			return err
		}
		fmt.Fprintf(w, "M1 tables: %s\n", st.from)
		opt.Policy = st.policy()
		opt.Derive = savemerge.Derive(st.t.Keys, st.t.Hearts)
	} else {
		warnings = append(warnings,
			"no --iso: memory switches are a's (the M1 tables of your disc say which latch), and so are the DERIVED bytes: small keys, max life, max magic, inventory slots and equipment may lag the merged flags. Pass --iso <your Wind Waker image> to merge and derive them.")
	}
	opt.Policy = story.Policy(opt.Policy)
	var behind []string
	for _, id := range replica.Milestones {
		if eventBitSet(&b.slot.Image, id) && !eventBitSet(&a.slot.Image, id) {
			behind = append(behind, fmt.Sprintf("0x%04X", id))
		}
	}
	if len(behind) > 0 {
		warnings = append(warnings, fmt.Sprintf("b is past %s, which a is not: a still resumes where it did (holds keep the resume-stage bits out), so a copy of b's file may suit better than a merge",
			strings.Join(behind, ", ")))
	}

	r, err := savemerge.Merge(&a.slot.Image, &b.slot.Image, opt)
	if err != nil {
		return err
	}
	fmt.Fprintln(w, `"+" is a bit the merge adds to a, "~" a value it raises or derives: a's -> the merged.`)
	edges := edgesByClass(&a.slot.Image, &r.Image)
	if printEdges(w, edges) == 0 {
		fmt.Fprintln(w, "\nb adds nothing to a.")
	}
	adds := len(edges[class.OR])+len(edges[class.ORMasked])+len(edges[class.Max]) > 0
	if s := mergedTag(a, b, &r.Image, adds); s != "" {
		warnings = append(warnings, s)
	}
	if len(r.Held) > 0 {
		fmt.Fprintf(w, "\nHeld (b has them; as in co-op, they stay out of a until their condition holds in a's own game): %d\n", len(r.Held))
		for _, h := range r.Held {
			for bit := range 8 {
				if h.Mask&(1<<bit) != 0 {
					label, _ := saveLabel(int(h.Off), bit, -1)
					fmt.Fprintf(w, "  %s: %s\n", label, h.Hold.Why)
				}
			}
		}
	}
	if len(r.Notes) > 0 {
		fmt.Fprintln(w, "\nDerived:")
		for _, n := range r.Notes {
			fmt.Fprintf(w, "  %s\n", n)
		}
	}
	for _, s := range warnings {
		fmt.Fprintf(w, "\nWARNING: %s\n", s)
	}

	merged, err := a.file.WithSlot(a.slot.N, &r.Image)
	if err != nil {
		return err
	}
	if err := writeNewFile(out, merged.Encode()); err != nil {
		return err
	}
	if err := checkMerged(out, a.slot.N, &r.Image); err != nil {
		os.Remove(out)
		return fmt.Errorf("%s did not read back as written, so it is removed: %w", out, err)
	}
	fmt.Fprintf(w, "\nWrote %s: slot %d (file %d) %q, saved %d times; read back, checksums right.\n",
		out, a.slot.N, a.slot.N+1, a.slot.PlayerName(), merged.SaveCount())
	fmt.Fprintln(w, "Put it in Dolphin's GCI folder (or import it in the Memory Card Manager) in place of a's file, with Dolphin closed.")
	return nil
}

// slotFile is slot n of image img as the tag rules see the file loaded
// from it: card_to_memory brings back mReserve with the rest, mDataNum is
// n, and mDateIPL is the stamp the game's save put into the card copy
// (addr.OffDateIPL).
func slotFile(n int, img *world.Image) bind.File {
	f := bind.File{Slot: uint8(n), ClearCount: img[addr.OffClearCount], DateIPL: binary.BigEndian.Uint64(img[addr.OffDateIPL:])}
	copy(f.Tag[:], img[addr.OffReserve:])
	copy(f.Name[:], img[addr.OffPlayerName:])
	return f
}

// mergedTag settles the merged slot's binding tag, img's, which
// savemerge.Merge leaves a's, and returns a warning saying what it means
// ("" for none). adds reports whether img holds shared progress (OR,
// OR_MASKED or MAX bytes) a lacks.
//
// A bound file's progress enters its world at its next session as the
// file's offline progress, with no bind judgement and no --coop-merge
// (§5.5; the hub's onBoundHello and onWorld). So a's tag stays only where
// that brings the world nothing it would not get anyway: b adds nothing,
// or b is bound to the same world, whose own next session brings the
// same. Otherwise the tag is cleared, and the merged file joins as an
// unbound file, which the host judges like any other (§5.3): one that adds
// nothing to the world binds after a prompt, one with progress the world
// lacks or one far behind it needs the host's --coop-merge. A tag that
// binds a to no world (leaked, or a newer layout's) is kept as it is and
// said to be what it is.
func mergedTag(a, b *saveInput, img *world.Image, adds bool) string {
	va := bind.Classify(slotFile(a.slot.N, &a.slot.Image), bind.Expect{})
	switch va.State {
	case bind.Bound:
	case bind.Leaked:
		return fmt.Sprintf("a carries a co-op tag of world %s that binds it to no world: %s. The merged file keeps those bytes, and a co-op session offers to scrub them",
			va.Tag.World, va.Why)
	case bind.Foreign:
		return "a carries a co-op tag a newer ww-multiplayer wrote, and the merged file keeps it: this version's co-op refuses the file"
	default:
		return ""
	}
	wid := va.Tag.World
	if vb := bind.Classify(slotFile(b.slot.N, &b.slot.Image), bind.Expect{World: wid}); vb.State == bind.Bound {
		return fmt.Sprintf("a and b are both bound to co-op world %s, and the merged file keeps a's tag: at its next session the host takes b's progress into that world as this file's offline progress, with no bind judgement and no --coop-merge, as b's own next session would",
			wid)
	}
	if !adds {
		return ""
	}
	clear(img[addr.OffReserve : addr.OffReserve+bind.TagSize])
	return fmt.Sprintf("a is bound to co-op world %s and b is not, so the merged file leaves a's tag out: a bound file's progress enters its world unjudged. At its next --coop join it is an unbound file the host judges like any other: it binds after a prompt if it adds nothing to the world, and needs the host's --coop-merge if it holds progress the world lacks (b's) or is far behind it",
		wid)
}

// eventBitSet reports whether img has story bit id: byte HH of mEvent,
// mask LL (onEventBit, d_save.cpp:1187-1189).
func eventBitSet(img *world.Image, id uint16) bool {
	b := addr.EventByte(id)
	return img[addr.OffEvent+b.Off]&b.Mask == b.Mask
}

// writeNewFile writes b to path, which must not exist, and syncs it; on
// a failure it removes what it made.
func writeNewFile(path string, b []byte) error {
	fh, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return err
	}
	_, err = fh.Write(b)
	if err == nil {
		err = fh.Sync()
	}
	if cerr := fh.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(path)
	}
	return err
}

// checkMerged reads path back as the game would load slot n: a GCI whose
// entry, copies and slot checksum hold (savefile.Verify) and whose slot is
// want.
func checkMerged(path string, n int, want *world.Image) error {
	f, kind, err := savefile.Read(path)
	if err != nil {
		return err
	}
	if kind != savefile.KindGCI {
		return fmt.Errorf("read back as a %s", kind)
	}
	if err := f.Verify(n); err != nil {
		return err
	}
	s, err := f.Slot(n)
	if err != nil {
		return err
	}
	if s.Image != *want {
		return fmt.Errorf("slot %d holds another image", n)
	}
	return nil
}
