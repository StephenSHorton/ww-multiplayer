package main

// Co-op M2 save-safety commands (#23; docs/08-coop-design.md §5.2-5.5,
// §11 items 9-10):
//
//   - coop-tag show [world-id]: the loaded file's binding tag in
//     dSv_reserve_c and how rules V1-V6 classify it (read-only), V5 with
//     the pending fresh binds of the world files on this machine;
//   - coop-tag clear --yes: zero the tag, so the file leaves co-op at its
//     next in-game save (§5.5 "Playing solo afterwards"). Without --yes it
//     only shows what it would do; with it, it still waits for the gates;
//   - coop-world restore [backup] [--yes]: list the memory-card backups,
//     or copy one back over the cards, only while Dolphin is closed (§5.4).
//
// The only game-RAM write here is coop-tag clear's, through
// internal/coop/bind's Scrub: 0x30 bytes at 0x803C532C, behind the gates
// and the user's --yes. Like the other co-op diagnostics, none of these is
// listed in printHelp; the command registry (commands.go) dispatches them.

import (
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/internal/coop/backup"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/bind"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/world"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mem"
)

const (
	coopTagUsage          = "coop-tag show [world-id] | coop-tag clear [--yes]"
	coopWorldRestoreUsage = "coop-world restore [backup] [--yes]"
)

// errNotDone is a command's error when it showed what it would do and
// stopped for want of --yes; the message has been printed already.
var errNotDone = errors.New("nothing changed")

// runCoopTag runs coop-tag with the arguments after it.
func runCoopTag(args []string) {
	pos, flags, err := splitDiagFlags(args, "yes")
	if err != nil {
		exitUsage(err, coopTagUsage)
	}
	if len(pos) == 0 {
		exitUsage(errors.New("say show or clear"), coopTagUsage)
	}
	worlds, _ := world.Dir() // "" looks up no fresh binds
	switch pos[0] {
	case "show":
		var session bind.WorldID
		switch {
		case len(pos) > 2:
			exitUsage(fmt.Errorf("too many arguments: %q", pos[2:]), coopTagUsage)
		case len(pos) == 2:
			if session, err = bind.ParseWorldID(pos[1]); err != nil {
				exitUsage(err, coopTagUsage)
			}
		}
		if flags["yes"] {
			exitUsage(errors.New("show writes nothing; --yes is for clear"), coopTagUsage)
		}
		err = withCoopMem(func(m mem.Mem) error { return coopTagShow(os.Stdout, m, coopTick, session, worlds) })
	case "clear":
		if len(pos) > 1 {
			exitUsage(fmt.Errorf("clear takes no arguments, not %q", pos[1:]), coopTagUsage)
		}
		err = withCoopMem(func(m mem.Mem) error { return coopTagClear(os.Stdout, m, coopTick, flags["yes"], worlds) })
	default:
		exitUsage(fmt.Errorf("unknown coop-tag command %q", pos[0]), coopTagUsage)
	}
	exitOnCoopErr(err)
}

// runCoopWorldRestore runs `coop-world restore` with the arguments after
// "restore".
func runCoopWorldRestore(args []string) {
	pos, flags, err := splitDiagFlags(args, "yes")
	if err != nil {
		exitUsage(err, coopWorldRestoreUsage)
	}
	if len(pos) > 1 {
		exitUsage(fmt.Errorf("one backup, not %q", pos), coopWorldRestoreUsage)
	}
	id := ""
	if len(pos) == 1 {
		id = pos[0]
	}
	root, err := backup.Dir()
	if err == nil {
		err = coopWorldRestore(os.Stdout, root, id, flags["yes"], backup.Dolphin)
	}
	exitOnCoopErr(err)
}

func exitOnCoopErr(err error) {
	switch {
	case err == nil:
	case errors.Is(err, errNotDone):
		os.Exit(1)
	default:
		fmt.Printf("ERROR: %v\n", err)
		os.Exit(1)
	}
}

// coopFreshBinds is V5's lookup of pending fresh binds for f's tag
// (Expect.Fresh): the world file, under the worlds root, of the world the
// tag names. It is nil when the tag names no world, root is "", or no
// readable file of that world is here (a joiner's machine): only that
// world's host knows then.
func coopFreshBinds(root string, f bind.File) func(bind.Nonce) bool {
	if root == "" {
		return nil
	}
	t, err := bind.Decode(f.Tag[:])
	if err != nil || t.World.IsZero() {
		return nil
	}
	w, _, err := world.Load(root, world.ID(t.World))
	if err != nil {
		return nil
	}
	return func(n bind.Nonce) bool { return w.IsFreshBind(world.ID(n)) }
}

// maybeFresh reports whether verdict v, made without the tag's world
// file, may be a pending fresh bind rather than a leak: V5 is the only
// rule that failed. A file bound before its first save and reload has
// mDateIPL 0 until it is loaded from the card (§5.2), and its nonce is on
// record only in the host's world file.
func maybeFresh(v bind.Verdict, fresh func(bind.Nonce) bool) bool {
	return fresh == nil && v.State == bind.Leaked && slices.Equal(v.Failed(), []bind.Rule{bind.V5})
}

// coopTagShow prints the loaded file, its tag bytes, the classification
// with every rule's outcome, and the gates. session, if not zero, is the
// session world V6 checks against; V5's pending fresh binds come from the
// world files under worlds (coopFreshBinds). It writes nothing.
func coopTagShow(w io.Writer, m mem.Mem, tick func(), session bind.WorldID, worlds string) error {
	s, st, _, err := gateSnapshots(m, tick)
	if err != nil {
		return err
	}
	f := bind.FileOf(s)
	fresh := coopFreshBinds(worlds, f)
	v := bind.Classify(f, bind.Expect{World: session, Fresh: fresh})
	pending := maybeFresh(v, fresh)
	line := func(label, format string, a ...any) {
		fmt.Fprintf(w, "%-9s %s\n", label, fmt.Sprintf(format, a...))
	}
	line("where", "%s", where(s))
	if !st.Stable {
		line("", "(%s: the file in RAM may not be the one you play)", st.StableWhy)
	}
	line("file", "%s", bind.Describe(f))
	line("tag", "% X (0x%X bytes at 0x%08X)", f.Tag[:], bind.TagSize, addr.Reserve)
	if pending {
		line("state", "LEAKED, OR A PENDING FRESH BIND: only V5 fails, and world %s's file, which would say, is not on this machine", v.Tag.World)
	} else {
		line("state", "%s: %s", strings.ToUpper(v.State.String()), v.Why)
	}
	if v.HasTag {
		line("world", "%s, bind nonce %s, world seq %d", v.Tag.World, v.Tag.Nonce, v.Tag.WorldSeq)
	}
	for _, c := range v.Checks {
		mark, details := "--", c.Details
		switch {
		case c.Ran && c.Pass:
			mark = "ok"
		case c.Ran && c.Rule == bind.V5 && fresh == nil:
			mark = "FAIL"
			details = fmt.Sprintf("mDateIPL is 0: never saved and loaded back; whether nonce %s is a pending fresh bind is in world %s's file, not here", v.Tag.Nonce, v.Tag.World)
		case c.Ran:
			mark = "FAIL"
		case c.Rule == bind.V6 && v.State == bind.Bound:
			details = "not checked: give the session's world ID to check it"
		case details == "":
			details = "not checked"
		}
		fmt.Fprintf(w, "  %s %-5s %s\n", c.Rule, mark, details)
	}
	if v.Fresh {
		line("", "V5 held only through a pending fresh bind")
	}
	switch {
	case pending:
		line("next", "if you bound this file and have not loaded it from the card since, it is bound: save in game, load it from the card and run this again, or run it on the host. Co-op scrubs it only once the host says it is no pending fresh bind")
	case v.State == bind.Leaked:
		line("next", "co-op scrubs this tag (after a prompt) so it never reaches this file's next save; coop-tag clear does it now")
	case v.State == bind.Foreign:
		line("next", "co-op refuses this file; coop-tag clear unbinds it")
	}
	return nil
}

// coopTagClear zeroes the loaded file's tag (§5.5): it shows the plan
// bind.PlanClear makes once the gates are open, and writes only with yes.
// V5's pending fresh binds come from the world files under worlds, as in
// coopTagShow.
func coopTagClear(w io.Writer, m mem.Mem, tick func(), yes bool, worlds string) error {
	s, st, _, err := gateSnapshots(m, tick)
	if err != nil {
		return err
	}
	fresh := coopFreshBinds(worlds, bind.FileOf(s))
	p, err := bind.PlanClear(s, st, bind.Expect{Fresh: fresh})
	switch {
	case errors.Is(err, bind.ErrNoTag):
		fmt.Fprintln(w, "This file has no co-op tag: nothing to clear.")
		return nil
	case err != nil:
		return fmt.Errorf("%w (try again in game, out of menus and cutscenes)", err)
	}
	if maybeFresh(p.Verdict, fresh) {
		fmt.Fprintf(w, "Careful: this tag may be a binding, not a leak. Only V5 fails, as it does for a file bound before its\n"+
			"first save and reload; world %s's file, which would say, is not on this machine. If you bound\n"+
			"this file, clearing the tag unbinds it.\n", p.Verdict.Tag.World)
	}
	fmt.Fprintln(w, p.Prompt)
	if !yes {
		fmt.Fprintln(w, "Not cleared: re-run with --yes to zero the tag.")
		return errNotDone
	}
	p.Confirm()
	if err := bind.Scrub(m, p, s, st); err != nil {
		return err
	}
	fmt.Fprintf(w, "Cleared the tag at 0x%08X. Save in-game to write the unbound file to the memory card.\n", addr.Reserve)
	return nil
}

// coopWorldRestore lists the backups under root (id ""), or restores
// backup id with yes, refusing while pf sees a Dolphin running.
func coopWorldRestore(w io.Writer, root, id string, yes bool, pf backup.ProcessFinder) error {
	if id == "" {
		return listCoopBackups(w, root)
	}
	b, err := backup.Open(root, id)
	if err != nil {
		return err
	}
	fmt.Fprintf(w, "Backup %s, made %s", b.ID, b.Created.Local().Format(time.DateTime))
	if b.Label != "" {
		fmt.Fprintf(w, " (%s)", b.Label)
	}
	fmt.Fprintln(w, ", restores:")
	raw := false
	for _, f := range b.Files {
		c := b.Cards[f.Card]
		raw = raw || c.Kind == backup.Raw
		use := map[backup.Use]string{backup.UseYes: ", in use", backup.UseNo: ", not in use"}[c.InUse] // as Dolphin's settings said then
		fmt.Fprintf(w, "  %s  (%d bytes, slot %s %s%s)\n", f.Source, f.Size, c.Slot, c.Kind, use)
	}
	if raw {
		fmt.Fprintln(w, "A raw card holds every game's saves: restoring it rolls all of them back.")
	}
	fmt.Fprintln(w, "The cards as they are now are backed up first, so the restore can be undone.")
	if !yes {
		fmt.Fprintln(w, "Not restored: close Dolphin and re-run with --yes.")
		return errNotDone
	}
	rep, err := backup.Restore(b, pf, backup.Options{Root: root})
	if rep != nil && rep.Safety != nil {
		fmt.Fprintf(w, "Backed up the cards as they were to %s.\n", rep.Safety.ID)
	}
	if err != nil {
		return err
	}
	for _, f := range rep.Restored {
		fmt.Fprintf(w, "Restored %s\n", f.Source)
	}
	for _, p := range rep.Left {
		fmt.Fprintf(w, "Left alone (not in the backup): %s\n", p)
	}
	if rep.Safety != nil {
		fmt.Fprintf(w, "To undo: %s coop-world restore %s --yes\n", exeCmd, rep.Safety.ID)
	}
	return nil
}

// listCoopBackups prints the backups under root, newest first.
func listCoopBackups(w io.Writer, root string) error {
	list, err := backup.List(root)
	if err != nil {
		return err
	}
	if len(list) == 0 {
		fmt.Fprintf(w, "No memory-card backups in %s yet: co-op makes one at every session start.\n", root)
		return nil
	}
	fmt.Fprintf(w, "Memory-card backups in %s, newest first:\n", root)
	for _, b := range list {
		label := ""
		if b.Label != "" {
			label = "  " + b.Label
		}
		fmt.Fprintf(w, "  %-20s %s  %d files, %d KiB%s\n",
			b.ID, b.Created.Local().Format(time.DateTime), len(b.Files), (b.Size()+1023)/1024, label)
	}
	fmt.Fprintf(w, "Restore one (with Dolphin closed): %s %s\n", exeCmd, strings.Replace(coopWorldRestoreUsage, "[backup] [--yes]", "<backup> --yes", 1))
	return nil
}
