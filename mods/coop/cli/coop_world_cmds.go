package coopcli

// coop-world (#23; docs/08-coop-design.md §4.4, §5.4-5.5): the host's
// commands for its co-op worlds and the memory-card backups.
//
//   - coop-world list: the worlds in os.UserConfigDir()/ww-multiplayer/
//     worlds, read-only;
//   - coop-world restore [backup] [--yes]: coop_m2_cmds.go's card restore;
//   - coop-world block <world-id> <bit> [--yes] [--force]: tombstone one
//     shared bit (§4.4 "Tombstones"): it leaves the world, and every later
//     proposal of it is refused. Files that already have it keep it.
//
// block edits the world file, and only while no session serves the world:
// a running hub keeps the world in memory, so its next write would undo
// the edit. The host's session marker (coopsession.WriteMarker) tells. A marker
// whose process has ended on this machine is stale (coopMarker.state):
// block says so and goes on. --force overrides a marker whose process
// still runs, or one that can't be checked. Bound replicas learn the block
// from the hub at their next welcome (CTRL TOMBSTONE). The hub can also
// tombstone over the wire (hub.Hub.Tombstone, CTRL TOMBSTONE from the
// host's own connection), which M8's TUI panel will drive.

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/StephenSHorton/ww-multiplayer/mods/coop/bind"
	"github.com/StephenSHorton/ww-multiplayer/mods/coop/class"
	"github.com/StephenSHorton/ww-multiplayer/mods/coop/diag"
	coopsession "github.com/StephenSHorton/ww-multiplayer/mods/coop/session"
	"github.com/StephenSHorton/ww-multiplayer/mods/coop/world"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
)

const (
	coopWorldUsage      = "coop-world list | coop-world restore [backup] [--yes] | coop-world block <world-id> <bit> [--yes] [--force]"
	coopWorldBlockUsage = "coop-world block <world-id> (byte <off> <mask> | event <0xHHLL> | switch|chest|item <table> <no>) [--yes] [--force]"
)

// runCoopWorld runs coop-world with the arguments after it.
func runCoopWorld(args []string) {
	if len(args) == 0 {
		exitUsage(errors.New("say list, restore or block"), coopWorldUsage)
	}
	switch args[0] {
	case "restore":
		runCoopWorldRestore(args[1:])
	case "list":
		if len(args) > 1 {
			exitUsage(fmt.Errorf("list takes no arguments, not %q", args[1:]), coopWorldUsage)
		}
		root, err := world.Dir()
		if err == nil {
			err = coopsession.PrintWorlds(os.Stdout, root)
		}
		exitOnCoopErr(err)
	case "block":
		pos, flags, err := diag.SplitFlags(args[1:], "yes", "force")
		if err != nil {
			exitUsage(err, coopWorldBlockUsage)
		}
		if len(pos) < 2 {
			exitUsage(errors.New("name the world and the bit to block"), coopWorldBlockUsage)
		}
		id, err := bind.ParseWorldID(pos[0])
		if err != nil {
			exitUsage(err, coopWorldBlockUsage)
		}
		t, err := parseBlockTarget(pos[1:])
		if err != nil {
			exitUsage(err, coopWorldBlockUsage)
		}
		root, err := world.Dir()
		if err == nil {
			err = coopWorldBlock(os.Stdout, root, world.ID(id), t, flags["yes"], flags["force"])
		}
		exitOnCoopErr(err)
	default:
		exitUsage(fmt.Errorf("unknown coop-world command %q", args[0]), coopWorldUsage)
	}
}

// blockTarget is the bit (or bits) of the image a block names.
type blockTarget struct {
	off  int
	mask byte
	what string
}

// parseBlockTarget reads a block's bit: a raw image byte and mask, an
// event bit 0xHHLL (onEventBit, d_save.cpp:1187-1189), or a save table's
// chest, memory switch or pickup (onTbox, onSwitch, onItem: d_save.cpp:
// 1072-1075, 1084-1087, 1112-1115). Dan and zone switches and kills are
// not in the world image (they live in the hub's scoped stores), so they
// can't be blocked.
func parseBlockTarget(args []string) (blockTarget, error) {
	num := func(s, name string, max int) (int, error) {
		n, err := strconv.ParseInt(s, 0, 32)
		if err != nil || n < 0 || int(n) > max {
			return 0, fmt.Errorf("%s %q: want a number from 0 to 0x%X (0x for hex)", name, s, max)
		}
		return int(n), nil
	}
	need := func(n int) error {
		if len(args) != n+1 {
			return fmt.Errorf("%s takes %d argument(s)", args[0], n)
		}
		return nil
	}
	var t blockTarget
	switch args[0] {
	case "byte":
		if err := need(2); err != nil {
			return t, err
		}
		off, err := num(args[1], "offset", addr.SaveImageSize-1)
		if err != nil {
			return t, err
		}
		mask, err := num(args[2], "mask", 0xFF)
		if err != nil {
			return t, err
		}
		t = blockTarget{off: off, mask: byte(mask), what: fmt.Sprintf("image byte 0x%03X bits 0x%02X (%s)", off, mask, class.Of(off).Field)}
	case "event":
		if err := need(1); err != nil {
			return t, err
		}
		id, err := num(args[1], "event", 0xFFFF)
		if err != nil {
			return t, err
		}
		// onEventBit ORs the id's low byte into mEvent[id >> 8].
		b := addr.EventByte(uint16(id))
		switch {
		case b.Mask == 0:
			return t, fmt.Errorf("event 0x%04X names no bit: its low byte is the bit mask", id)
		case b.Off >= addr.EventRegFirst:
			return t, fmt.Errorf("event 0x%04X is in register byte 0x%02X, not an event bit: block it with byte", id, b.Off)
		case b.Off >= addr.EventBitBytes:
			return t, fmt.Errorf("event byte 0x%02X holds no named event bit", b.Off)
		}
		t = blockTarget{off: addr.OffEvent + b.Off, mask: b.Mask, what: fmt.Sprintf("event bit 0x%04X", id)}
	case "switch", "chest", "item":
		if err := need(2); err != nil {
			return t, err
		}
		tbl, err := num(args[1], "save table", addr.TableCount-1)
		if err != nil {
			return t, err
		}
		no, err := num(args[2], args[0], 0xFF)
		if err != nil {
			return t, err
		}
		var b addr.Bit
		switch args[0] {
		case "switch":
			var store addr.SwitchStore
			if store, b, err = addr.SwitchBit(no); err == nil && store != addr.SwitchMemory {
				err = fmt.Errorf("switch 0x%02X is a %s switch, which the world image doesn't hold", no, store)
			}
		case "chest":
			b, err = addr.TboxBit(no)
		case "item":
			b, err = addr.MemBitItemBit(no)
		}
		if err != nil {
			return t, err
		}
		t = blockTarget{off: addr.OffMemBit(tbl) + b.Off, mask: b.Mask, what: fmt.Sprintf("save table %d %s 0x%02X", tbl, args[0], no)}
	default:
		return t, fmt.Errorf("unknown bit kind %q: say byte, event, switch, chest or item", args[0])
	}
	return t, nil
}

// coopWorldBlock tombstones t in world id under root: it shows what the
// block does, and with yes edits the world file. It refuses while the
// world's session marker says a host may serve it, unless force.
func coopWorldBlock(w io.Writer, root string, id world.ID, t blockTarget, yes, force bool) error {
	dir := world.Path(root, id)
	cur, _, err := world.Load(root, id)
	if err != nil {
		return fmt.Errorf("co-op world %s: %w", id, err)
	}
	if m, ok := coopsession.ReadMarker(dir); ok && !force {
		switch use, why := m.State(); {
		case use == coopsession.MarkerStale:
			// The marker stays: the next host start says the session did
			// not end cleanly.
			fmt.Fprintf(w, "World %s has a stale session marker (%s; that process has ended): its last session did not end cleanly.\n", id, m)
		case use == coopsession.MarkerUnknown:
			return fmt.Errorf("co-op world %s may be in use by a session (%s): can't tell whether that process still runs (%s); stop that host first (Ctrl+C), or pass --force if you know it ended", id, m, why)
		case use == coopsession.MarkerLive && why != "": // by its pid alone
			return fmt.Errorf("co-op world %s may be in use by a session (%s): a process with pid %d runs, but %s, so it may not be that host; stop that host first (Ctrl+C), or pass --force if no host serves the world", id, m, m.PID(), why)
		default:
			return fmt.Errorf("co-op world %s is in use by a running session (%s): stop that host first (Ctrl+C), or pass --force if you know no host serves the world", id, m)
		}
	}
	// Try it on a copy first: it says what the block would clear, and
	// refuses a byte that never merges before anything is touched.
	r, err := cur.Clone().Tombstone(t.off, t.mask)
	if err != nil {
		return err
	}
	if r.Seq == cur.Seq() {
		fmt.Fprintf(w, "%s is already blocked in world %s.\n", strings.ToUpper(t.what[:1])+t.what[1:], id)
		return nil
	}
	mask := t.mask & world.SharedMask(t.off)
	fmt.Fprintf(w, "Block %s in co-op world %s for good?\n", t.what, id)
	fmt.Fprintf(w, "  Image byte 0x%03X (%s), bits 0x%02X.\n", t.off, class.Of(t.off).Field, mask)
	if len(r.Cleared) > 0 {
		fmt.Fprintln(w, "  The world has it now: it is taken out of the world.")
	}
	fmt.Fprintln(w, "  Every later proposal of it is refused. Files that already have it keep it.")
	if !yes {
		fmt.Fprintln(w, "Not blocked: re-run with --yes.")
		return errNotDone
	}
	st, _, err := world.Open(root, id, world.Options{})
	if err != nil {
		return err
	}
	res, err := st.Tombstone(t.off, t.mask)
	if serr := st.Stop(); err == nil {
		err = serr
	}
	if err != nil {
		return err
	}
	fmt.Fprintf(w, "Blocked %s in world %s (seq %d). Replicas learn it at their next welcome.\n", t.what, id, res.Seq)
	return nil
}
