package main

// ops (wind#90): the C runtime's rings (inject/include/wwops.h,
// wwapi/ops) on a live Dolphin.
//
//	ops status                  the region header, counters and ring words
//	ops echo [a b c]            run ECHO and wait for it and its event
//	ops kill <pid> <proc> [set] run KILL (the actor goes, no save bit)
//	ops events                  drain the event ring once
//
// wwdev's queue writer is its own: run it while nothing else submits to
// this game (the app's Game does, for core.actor.kill).

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/mods/widescreen"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/heap"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mailbox"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mem"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/ops"
)

const opsUsage = "ops status | echo [a b c] | kill <pid> <proc> [setID] | events | ws <mode> <aspect> [flags]"

func cmdOps(_ string, args []string) {
	if len(args) == 0 {
		fmt.Println("usage: wwdev " + opsUsage)
		os.Exit(2)
	}
	d, err := findDolphin("GZLE01")
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	defer d.Close()
	if err := runOps(d, args, os.Stdout); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}

func parseWord(s string) (uint32, error) {
	v, err := strconv.ParseUint(s, 0, 32)
	return uint32(v), err
}

func runOps(m mem.Mem, args []string, w interface{ Write([]byte) (int, error) }) error {
	q := ops.New(m)
	p := func(f string, a ...any) { fmt.Fprintf(w, f, a...) }
	switch args[0] {
	case "status":
		hdr, err := m.Read(mailbox.OpsAddr, 0x48)
		if err != nil {
			return err
		}
		p("API region @ 0x%08X: magic %08X layout %d dir %d\n", mailbox.OpsAddr, mem.U32BE(hdr), mem.U16BE(hdr[4:]), mem.U16BE(hdr[6:]))
		for i := 0; i < mailbox.OpsDirMax; i++ {
			e := hdr[mailbox.OpsDirOff+i*mailbox.OpsDirEntrySize:]
			if t := mem.U32BE(e); t != 0 {
				p("  dir %q +0x%03X size 0x%X\n", string(e[:4]), mem.U16BE(e[4:]), mem.U16BE(e[6:]))
			}
		}
		s, err := q.Stats()
		if err != nil {
			return err
		}
		ht, err := m.Read(mailbox.OpsAddr+mailbox.OpsCmdHeadOff, 8)
		if err != nil {
			return err
		}
		p("events write %d read %d; commands head %d tail %d\n", mem.U32BE(hdr[mailbox.OpsEvWriteOff:]), mem.U32BE(hdr[mailbox.OpsEvReadOff:]), mem.U32BE(ht), mem.U32BE(ht[4:]))
		p("done %d refused %d bad_check %d unknown %d events_dropped %d\n", s.Done, s.Refused, s.BadCheck, s.Unknown, s.EventsDropped)
		if c, err := heap.ReadCounters(m); err == nil {
			p("heap errors: game %d zelda %d archive %d (solid %d)\n", c.Game, c.Zelda, c.Archive, c.Solid)
		}
		return q.Ready()
	case "echo":
		var a [3]uint32 = [3]uint32{0x11111111, 0x22222222, 0x33333333}
		for i := 1; i < len(args) && i <= 3; i++ {
			v, err := parseWord(args[i])
			if err != nil {
				return err
			}
			a[i-1] = v
		}
		if _, _, err := q.Events(); err != nil { // start past the old ones
			return err
		}
		start := time.Now()
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		r, err := q.Run(ctx, ops.OpEcho, a[0], a[1], a[2])
		if err != nil {
			return err
		}
		p("echo: %s in %v\n", r, time.Since(start).Round(time.Millisecond))
		evs, bad, err := q.Events()
		if err != nil {
			return err
		}
		ok := false
		for _, e := range evs {
			p("  event %d kind %d a %08X b %08X c %08X\n", e.N, e.Kind, e.A, e.B, e.C)
			if e.Kind == ops.EvEcho && e.A == a[0] && e.B == a[1] && e.C == a[2] {
				ok = true
			}
		}
		if bad != 0 || !ok || r != ops.ROK {
			return fmt.Errorf("echo: result %s, echo event seen %v, %d bad entries", r, ok, bad)
		}
		return nil
	case "ws":
		// ws <mode 0|1|2> <aspect> [flags]: WWAP_OP_WS_SET with the
		// aspect's factor (W:H, a decimal, or "off"); flags default 0xFF
		// (all the blob supports). One writer: not while mp-local runs.
		if len(args) < 3 || len(args) > 4 {
			return fmt.Errorf("usage: wwdev ops ws <mode 0|1|2> <aspect|off> [flags]")
		}
		mode, err := parseWord(args[1])
		if err != nil || mode > 2 {
			return fmt.Errorf("ws: mode %q is not 0, 1 or 2", args[1])
		}
		set, err := widescreen.ParseSetting(args[2])
		if err != nil || set.Auto {
			return fmt.Errorf("ws: aspect %q: want off, 16:9, W:H or a decimal", args[2])
		}
		flags := uint32(0xFF)
		if len(args) == 4 {
			if flags, err = parseWord(args[3]); err != nil || flags > 0xFF {
				return fmt.Errorf("ws: flags %q do not fit a byte", args[3])
			}
		}
		a, b, c := widescreen.Request{Mode: uint8(mode), Factor: float32(max(set.Factor(), 1)), Flags: uint8(flags)}.Words()
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		r, err := q.Run(ctx, mailbox.OpsOpWsSet, a, b, c)
		if err != nil {
			return err
		}
		blk, _ := mailbox.ReadWidescreen(m)
		p("ws mode %d factor %.4f flags 0x%02X: %s; block: %s\n", mode, set.Factor(), flags, r, blk)
		if r != ops.ROK {
			return fmt.Errorf("ws: %s", r)
		}
		return nil
	case "kill":
		if len(args) < 3 {
			return fmt.Errorf("usage: wwdev %s", opsUsage)
		}
		pid, err := parseWord(args[1])
		if err != nil {
			return err
		}
		proc, err := parseWord(args[2])
		if err != nil {
			return err
		}
		set := uint32(0xFFFFFFFF)
		if len(args) > 3 {
			if set, err = parseWord(args[3]); err != nil {
				return err
			}
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		r, err := q.Run(ctx, ops.OpKill, pid, proc&0xFFFF, set)
		if err != nil {
			return err
		}
		p("kill pid %d proc 0x%04X: %s\n", pid, proc&0xFFFF, r)
		return nil
	case "events":
		evs, bad, err := q.Events()
		if err != nil {
			return err
		}
		for _, e := range evs {
			p("event %d kind %d a %08X b %08X c %08X\n", e.N, e.Kind, e.A, e.B, e.C)
		}
		p("%d events, %d bad\n", len(evs), bad)
		return nil
	}
	return fmt.Errorf("usage: wwdev %s", opsUsage)
}
