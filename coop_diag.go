package main

// Co-op M0 diagnostics (#23; docs/08-coop-design.md §8.2-8.3 and §11
// items 3-5). Each one points the internal/coop readers at the Dolphin
// that WW_DOLPHIN_INDEX / WW_DOLPHIN_PID select, so the owner can check on
// a live game what the design infers from the decomp before any milestone
// writes game data.
//
// Everything here is read-only except two opt-in debug writers, which
// refuse to run without --yes: `region-probe arm` (canary words over the
// two gaps beside the mod) and `stage-hop` (a raw mNextStage request).
// Like the other diagnostics, none of them is listed in printHelp.
//
// Citations follow internal/coop/addr: file:line is the zeldaret/tww
// decomp at 4d78c994, sym:N is line N of its config/GZLE01/symbols.txt.

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"math/bits"
	"os"
	"os/signal"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/internal/coop/addr"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/class"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/gate"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/mem"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/snap"
	"github.com/StephenSHorton/ww-multiplayer/internal/inject"
)

// coopDiagUsage is each co-op diagnostic's usage line. main's switch sends
// every name here to runCoopDiag.
var coopDiagUsage = map[string]string{
	"coop-probe":        "coop-probe",
	"coop-watch":        "coop-watch [secs=60, 0 = until Ctrl+C] [--all]",
	"actors":            "actors [max=1024]",
	"zone-dump":         "zone-dump",
	"frame-phase-probe": "frame-phase-probe [secs=5]",
	"heap-free":         "heap-free",
	"blob-verify-live":  "blob-verify-live",
	"region-probe":      "region-probe arm --yes [--force] | region-probe check",
	"stage-hop":         "stage-hop <stage> [point=0] [room=0] [layer=-1] --yes",
}

// coopDiagFlags is the --flags each diagnostic accepts.
var coopDiagFlags = map[string][]string{
	"coop-watch":   {"all"},
	"region-probe": {"yes", "force"},
	"stage-hop":    {"yes"},
}

// coopTickInterval is the co-op snapshot rate, 10 Hz (§4.2).
const coopTickInterval = 100 * time.Millisecond

func coopTick() { time.Sleep(coopTickInterval) }

// runCoopDiag runs co-op diagnostic cmd with the arguments after it.
func runCoopDiag(cmd string, args []string) {
	usage := coopDiagUsage[cmd]
	pos, flags, err := splitDiagFlags(args, coopDiagFlags[cmd]...)
	if err != nil {
		exitUsage(err, usage)
	}
	if !strings.ContainsAny(usage, "[<") && len(pos) > 0 {
		exitUsage(fmt.Errorf("%s takes no arguments", cmd), usage)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	switch cmd {
	case "coop-probe":
		err = withCoopMem(func(m mem.Mem) error { return coopProbe(os.Stdout, m, coopTick) })
	case "coop-watch":
		secs, perr := diagIntArg(pos, 0, 60, 0, 86400, "secs")
		if perr != nil {
			exitUsage(perr, usage)
		}
		err = withCoopMem(func(m mem.Mem) error {
			return coopWatch(ctx, os.Stdout, m, time.Duration(secs)*time.Second, flags["all"])
		})
	case "actors":
		n, perr := diagIntArg(pos, 0, maxActorWalk, 1, 1<<16, "max")
		if perr != nil {
			exitUsage(perr, usage)
		}
		err = withCoopMem(func(m mem.Mem) error {
			w, err := walkActors(m, n)
			if err != nil {
				return err
			}
			printActors(os.Stdout, w)
			return nil
		})
	case "zone-dump":
		err = withCoopMem(func(m mem.Mem) error {
			s, err := snap.Read(m)
			if err != nil {
				return err
			}
			zoneDump(os.Stdout, s)
			return nil
		})
	case "frame-phase-probe":
		secs, perr := diagIntArg(pos, 0, 5, 1, 600, "secs")
		if perr != nil {
			exitUsage(perr, usage)
		}
		err = withCoopMem(func(m mem.Mem) error {
			return framePhaseProbe(ctx, os.Stdout, m, time.Duration(secs)*time.Second)
		})
	case "heap-free":
		err = withCoopMem(func(m mem.Mem) error { return heapFree(os.Stdout, m) })
	case "blob-verify-live":
		err = withCoopMem(func(m mem.Mem) error { return blobVerifyLive(os.Stdout, m) })
	case "region-probe":
		if len(pos) != 1 || (pos[0] != "arm" && pos[0] != "check") {
			exitUsage(errors.New("say arm or check"), usage)
		}
		if pos[0] == "check" {
			err = withCoopMem(func(m mem.Mem) error { return regionCheck(os.Stdout, m) })
			break
		}
		fmt.Print(regionArmWarning())
		if !flags["yes"] {
			fmt.Println("Not armed: re-run with --yes to write the canaries.")
			os.Exit(1)
		}
		err = withCoopMem(func(m mem.Mem) error { return regionArm(os.Stdout, m, coopTick, flags["force"]) })
	case "stage-hop":
		h, perr := parseStageHop(pos)
		if perr != nil {
			exitUsage(perr, usage)
		}
		fmt.Print(h.warning())
		if !flags["yes"] {
			fmt.Println("Not sent: re-run with --yes to write mNextStage.")
			os.Exit(1)
		}
		err = withCoopMem(func(m mem.Mem) error { return stageHopRun(os.Stdout, m, h, coopTick) })
	default:
		err = fmt.Errorf("unknown co-op diagnostic %q", cmd)
	}
	if err != nil {
		fmt.Printf("ERROR: %v\n", err)
		os.Exit(1)
	}
}

// withCoopMem opens the Dolphin WW_DOLPHIN_INDEX / WW_DOLPHIN_PID select,
// as peek and dump do, and runs f over it.
func withCoopMem(f func(m mem.Mem) error) error {
	d, err := openDolphin(0)
	if err != nil {
		return err
	}
	defer d.Close()
	return f(mem.NewDolphinMem(d))
}

// splitDiagFlags separates "--name" flags from positional arguments and
// refuses a flag not in allowed.
func splitDiagFlags(args []string, allowed ...string) (pos []string, flags map[string]bool, err error) {
	flags = make(map[string]bool)
	for _, a := range args {
		name, ok := strings.CutPrefix(a, "--")
		if !ok {
			pos = append(pos, a)
			continue
		}
		if !slices.Contains(allowed, name) {
			return nil, nil, fmt.Errorf("unknown flag %s", a)
		}
		flags[name] = true
	}
	return pos, flags, nil
}

// diagIntArg parses positional argument i as an integer in lo..hi, or
// returns def when it is absent.
func diagIntArg(pos []string, i, def, lo, hi int, name string) (int, error) {
	if i >= len(pos) {
		return def, nil
	}
	if len(pos) > i+1 {
		return 0, fmt.Errorf("too many arguments: %q", pos[i+1:])
	}
	v, err := strconv.Atoi(pos[i])
	if err != nil || v < lo || v > hi {
		return 0, fmt.Errorf("%s must be a number from %d to %d, not %q", name, lo, hi, pos[i])
	}
	return v, nil
}

func be32(b []byte) uint32 { return binary.BigEndian.Uint32(b) }

// tableNames are the save tables (dSv_save_c::SaveStageTbl, d_save.h:888-904).
var tableNames = [addr.TableCount]string{
	"sea", "sea2", "FF", "DRC", "FW", "TotG", "ET", "WT",
	"GT", "Hyrule", "ship", "misc", "subdungeon", "subdungeon-new", "blue-chu", "test",
}

func tableName(t int) string {
	if addr.ValidTable(t) {
		return tableNames[t]
	}
	return "none"
}

// ---------------------------------------------------------------------
// coop-probe

// gateSnapshots takes gate.StableTicks+1 snapshots, calling tick between
// them, so the gates can judge STABLE's unchanged-ticks rule and whether
// the heartbeat moves. It returns the last snapshot, its gate status and
// the first snapshot's heartbeat.
func gateSnapshots(m mem.Mem, tick func()) (*snap.Snapshot, gate.Status, uint32, error) {
	var tr gate.Tracker
	var s *snap.Snapshot
	var st gate.Status
	var hb0 uint32
	for i := 0; i <= gate.StableTicks; i++ {
		if i > 0 {
			tick()
		}
		var err error
		if s, err = snap.Read(m); err != nil {
			return nil, gate.Status{}, 0, err
		}
		if i == 0 {
			hb0 = s.Heartbeat
		}
		st = tr.Observe(s)
	}
	return s, st, hb0, nil
}

// coopProbe is the one-shot report: the stage, save table, room and file,
// life, the HUD's pending counters, whether mReserve (the binding tag's
// home, §5.2) is clear, and the three gates with the reason any is closed.
func coopProbe(w io.Writer, m mem.Mem, tick func()) error {
	s, st, hb0, err := gateSnapshots(m, tick)
	if err != nil {
		return err
	}
	line := func(label, format string, a ...any) {
		fmt.Fprintf(w, "%-11s %s\n", label, fmt.Sprintf(format, a...))
	}
	line("stage", "%q point %d room %d layer %d (mCurStage 0x%08X)",
		s.StageName, s.StagePoint, s.StageRoom, s.StageLayer, addr.CurStage)
	line("room", "%d (mStayNo, the room Link is in)", s.StayNo)

	// The STAG chunk names the stage's table too (d_stage.h:1027-1029);
	// getSave loaded mDan.mStageNo from it, so the two should agree.
	stag := "mpStagInfo is null"
	if s.StagInfo != 0 {
		if prop, err := mem.ReadU8(m, s.StagInfo+addr.StagOffProp); err != nil {
			stag = fmt.Sprintf("mpStagInfo 0x%08X unreadable: %v", s.StagInfo, err)
		} else {
			t := addr.StagSaveTable(prop)
			stag = fmt.Sprintf("STAG says table %d, key HUD %v", t, prop&addr.StagPropKeyDisp != 0)
			if t != int(s.Table) {
				stag += " (DIFFERS)"
			}
		}
	}
	line("save table", "%d %s (mDan.mStageNo); %s", s.Table, tableName(int(s.Table)), stag)
	line("file", "slot %d (mDataNum)", s.DataNum)

	sv := s.Save[:]
	maxLife := binary.BigEndian.Uint16(sv[addr.OffMaxLife:])
	life := binary.BigEndian.Uint16(sv[addr.OffLife:])
	line("life", "%d of %d quarter hearts (%.2f of %.2f hearts)", life, maxLife, float64(life)/4, float64(maxLife)/4)

	pl := func(a uint32) []byte { return s.Player[a-snap.PlayerRegion.Start:] }
	lifeCount := math.Float32frombits(be32(pl(addr.ItemLifeCount)))
	rupeeCount := int32(be32(pl(addr.ItemRupeeCount)))
	line("pending", "key %d, max life %d, max magic %d (life %g, rupees %d)",
		s.KeyNumCount, s.MaxLifeCount, s.MaxMagicCount, lifeCount, rupeeCount)

	res := sv[addr.OffReserve : addr.OffReserve+addr.ReserveSize]
	if i := slices.IndexFunc(res, func(b byte) bool { return b != 0 }); i < 0 {
		line("mReserve", "all 0x%X bytes zero (0x%08X): no binding tag", addr.ReserveSize, addr.Reserve)
	} else {
		line("mReserve", "NOT all zero: first non-zero byte at +0x%02X; tag bytes % X", i, res[:addr.ReserveTagBytes])
	}

	line("next stage", "mEnable %d", s.NextEnable)
	line("idle input", "event mode %d, order count %d, pause %d, minigame %d, refight %d",
		s.EvtMode, s.EvtOrderCount, s.MenuPause, s.MiniGameType, s.RefightStageNo)
	line("Link", "mpPlayerPtr[0] 0x%08X", s.PlayerPtr)
	line("heartbeat", "spawn_trigger %d, +%d over %d ticks", s.Heartbeat, s.Heartbeat-hb0, gate.StableTicks)
	line("torn", "%d sensitive bytes differed between the two save reads", s.Torn)

	gateLine := func(name string, open bool, why string) {
		if open {
			line(name, "open")
		} else {
			line(name, "closed: %s", why)
		}
	}
	gateLine("STABLE", st.Stable, st.StableWhy)
	gateLine("IDLE", st.Idle, st.IdleWhy)
	gateLine("HEARTBEAT", st.Heartbeat, st.HeartbeatWhy)
	return nil
}

// ---------------------------------------------------------------------
// coop-watch

// coopWatch polls a snapshot every tick for d (0: until ctx ends) and
// prints each labelled edge in the save region (0x803C4C08, 0x12A0
// bytes), stamped with the time since it started.
func coopWatch(ctx context.Context, w io.Writer, m mem.Mem, d time.Duration, all bool) error {
	cw := &coopWatcher{all: all}
	start := time.Now()
	t := time.NewTicker(coopTickInterval)
	defer t.Stop()
	for {
		s, err := snap.Read(m)
		if err != nil {
			return err
		}
		at := time.Since(start)
		for _, l := range cw.step(s) {
			fmt.Fprintf(w, "%8.1fs  %s\n", at.Seconds(), l)
		}
		if d > 0 && at >= d {
			return nil
		}
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
		}
	}
}

// coopWatcher turns successive snapshots into coop-watch's lines.
type coopWatcher struct {
	all  bool // also show status_b, whose time of day changes every frame
	prev *snap.Snapshot
}

// where is the context a line of edges is read in.
func where(s *snap.Snapshot) string {
	return fmt.Sprintf("stage %q room %d, table %d %s, file %d",
		s.StageName, s.StayNo, s.Table, tableName(int(s.Table)), s.DataNum)
}

// step returns the lines for snapshot s: a line when the stage, room,
// table or file changed, then one per edge since the last snapshot. On a
// table change getSave and initDan reload the live memBit and mDan in
// bulk (d_stage.cpp:1675-1677), which is not the game setting anything,
// so those edges are counted instead of listed.
func (cw *coopWatcher) step(s *snap.Snapshot) []string {
	prev := cw.prev
	cw.prev = s
	if prev == nil {
		return []string{fmt.Sprintf("watching dSv_info_c 0x%08X (0x%X bytes) at 10 Hz: %s",
			addr.SaveInfo, addr.SaveInfoSize, where(s))}
	}
	var out []string
	if where(prev) != where(s) {
		out = append(out, "== now "+where(s))
	}
	reload := prev.Table != s.Table
	hidden := 0
	for _, e := range diffSave(prev.Save[:], s.Save[:], int(s.Table)) {
		if !cw.all && e.Off >= addr.OffStatusB && e.Off < addr.OffReturnPlace {
			continue
		}
		if reload && e.Off >= addr.InfoOffMemory && e.Off < addr.InfoOffZone {
			hidden++
			continue
		}
		out = append(out, e.String())
	}
	if hidden > 0 {
		out = append(out, fmt.Sprintf("   (table %d -> %d: getSave and initDan reloaded the live memBit and mDan; %d edges not shown)",
			prev.Table, s.Table, hidden))
	}
	return out
}

// saveEdge is one change in the save between two snapshots: a flag bit
// rising or falling, or (Bit -1) a byte of a value changing.
type saveEdge struct {
	Off      int // offset in dSv_info_c
	Bit      int
	Rise     bool
	Old, New byte
	Label    string
}

func (e saveEdge) String() string {
	switch {
	case e.Bit < 0:
		return fmt.Sprintf("~ %s: 0x%02X -> 0x%02X", e.Label, e.Old, e.New)
	case e.Rise:
		return "+ " + e.Label
	}
	return "- " + e.Label
}

// diffSave returns the edges from prev to cur, two dSv_info_c images, in
// offset order. liveTable is cur's mDan.mStageNo, the table the live
// memBit holds.
func diffSave(prev, cur []byte, liveTable int) []saveEdge {
	var out []saveEdge
	for off := range min(len(prev), len(cur)) {
		a, b := prev[off], cur[off]
		if a == b {
			continue
		}
		if _, flags := saveLabel(off, 0, liveTable); !flags {
			label, _ := saveLabel(off, -1, liveTable)
			out = append(out, saveEdge{Off: off, Bit: -1, Old: a, New: b, Label: label})
			continue
		}
		for bit := range 8 {
			mask := byte(1) << bit
			if (a^b)&mask == 0 {
				continue
			}
			label, _ := saveLabel(off, bit, liveTable)
			out = append(out, saveEdge{Off: off, Bit: bit, Rise: b&mask != 0, Old: a, New: b,
				Label: label + edgeNote(cur, off, bit, liveTable)})
		}
	}
	return out
}

// edgeNote adds context to a flag edge at off: for the live memBit,
// whether the live table's saved slot has the same bit (they differ from
// a game write to the live copy until putSave copies it back, §3.1); for
// a zone, the room it belongs to.
func edgeNote(cur []byte, off, bit, liveTable int) string {
	switch {
	case off >= addr.InfoOffMemory && off < addr.InfoOffDan && addr.ValidTable(liveTable):
		saved := addr.OffMemBit(liveTable) + off - addr.InfoOffMemory
		if cur[saved]&(1<<bit) != 0 {
			return " (saved slot set)"
		}
		return " (saved slot clear)"
	case off >= addr.InfoOffZone && off < addr.InfoOffRestart:
		z := (off - addr.InfoOffZone) / addr.ZoneSize
		return fmt.Sprintf(" (room %d)", int8(cur[addr.InfoOffZone+z*addr.ZoneSize+addr.ZoneOffRoomNo]))
	}
	return ""
}

// dSv_info_c members past mDataNum (d_save.h:998-1001).
const (
	infoOffNewFile        = 0x1291 // u8 mNewFile
	infoOffNoFile         = 0x1292 // u8 mNoFile
	infoOffMemCardCheckID = 0x1298 // u64 mMemCardCheckID
)

// saveLabel names what bit (0-7) of byte off of dSv_info_c means, or the
// byte itself for bit -1, and reports whether the byte is a set of flags,
// whose edges coop-watch prints bit by bit, rather than part of a value.
// liveTable is mDan.mStageNo, the table the live memBit holds.
func saveLabel(off, bit, liveTable int) (string, bool) {
	switch {
	case off < 0 || off >= addr.SaveInfoSize:
		return fmt.Sprintf("dSv_info_c+0x%X", off), false
	case off < addr.OffMemory:
		return playerLabel(off, bit)
	case off < addr.OffOcean:
		rel := off - addr.OffMemory
		return memBitLabel(fmt.Sprintf("saved t%02d", rel/addr.MemBitSize), rel%addr.MemBitSize, bit)
	case off < addr.OffEvent:
		// u16 per sea grid (onOceanSvBit, d_save.cpp:1164-1168).
		rel := off - addr.OffOcean
		return fmt.Sprintf("ocean grid %d bit %d", rel/2, beIndex(rel%2, 2, bit)), true
	case off < addr.OffReserve:
		return eventLabel("event", off-addr.OffEvent, bit, class.Of(off).Mask, true)
	case off < addr.OffImagePad:
		return fmt.Sprintf("mReserve+0x%02X", off-addr.OffReserve), false
	case off < addr.SaveImageSize:
		return "dSv_save_c padding", false
	case off < addr.InfoOffDan:
		t := "t??"
		if addr.ValidTable(liveTable) {
			t = fmt.Sprintf("t%02d", liveTable)
		}
		return memBitLabel("live "+t, off-addr.InfoOffMemory, bit)
	case off < addr.InfoOffZone:
		return danLabel(off-addr.InfoOffDan, bit)
	case off < addr.InfoOffRestart:
		rel := off - addr.InfoOffZone
		return zoneLabel(rel/addr.ZoneSize, rel%addr.ZoneSize, bit)
	case off < addr.InfoOffTmp:
		return fmt.Sprintf("mRestart+0x%02X", off-addr.InfoOffRestart), false
	case off < addr.InfoOffTurnRestart:
		// mTmp is a dSv_event_c of temporary bits (§3 "Temporary").
		return eventLabel("tmp", off-addr.InfoOffTmp, bit, 0, false)
	case off < addr.InfoOffDataNum:
		return fmt.Sprintf("mTurnRestart+0x%02X", off-addr.InfoOffTurnRestart), false
	case off == addr.InfoOffDataNum:
		return "mDataNum", false
	case off == infoOffNewFile:
		return "mNewFile", false
	case off == infoOffNoFile:
		return "mNoFile", false
	case off >= infoOffMemCardCheckID:
		return fmt.Sprintf("mMemCardCheckID+%d", off-infoOffMemCardCheckID), false
	}
	return "dSv_info_c padding", false
}

// beIndex inverts the game's bit math (addr.Bit): bit of byte rel of an
// array of big-endian size-byte words is flag number word*size*8 plus the
// bit's place in its word.
func beIndex(rel, size, bit int) int {
	word, b := rel/size, rel%size
	return word*size*8 + (size-1-b)*8 + bit
}

// memBitLabel names bit of byte rel of a dSv_memBit_c (d_save.h:684-689;
// its setters' bit math, d_save.cpp:1072-1139).
func memBitLabel(prefix string, rel, bit int) (string, bool) {
	switch {
	case rel < addr.MemBitOffSwitch:
		return fmt.Sprintf("%s chest %d", prefix, beIndex(rel-addr.MemBitOffTbox, 4, bit)), true
	case rel < addr.MemBitOffItem:
		return fmt.Sprintf("%s switch 0x%02X", prefix, beIndex(rel-addr.MemBitOffSwitch, 4, bit)), true
	case rel < addr.MemBitOffVisited:
		return fmt.Sprintf("%s item %d", prefix, beIndex(rel-addr.MemBitOffItem, 4, bit)), true
	case rel < addr.MemBitOffKeyNum:
		n := beIndex(rel-addr.MemBitOffVisited, 4, bit)
		l := fmt.Sprintf("%s visited room %d", prefix, n)
		if n < 32 {
			// onItem takes 0..63 into a one-word mItem, so items 32-63
			// share these bits (addr.MemBitItemBit).
			l += fmt.Sprintf(" or item %d", n+32)
		}
		return l, true
	case rel == addr.MemBitOffKeyNum:
		return prefix + " small keys", false
	case rel == addr.MemBitOffDungeonItem:
		return prefix + " " + dungeonItemName(bit), true
	}
	return prefix + " memBit padding", false
}

// dungeonItemName names mDungeonItem bit i (d_save.h:639-646).
func dungeonItemName(i int) string {
	names := [addr.DungeonItemCount]string{"map", "compass", "boss key", "boss defeated", "heart container taken", "boss demo seen"}
	if i >= 0 && i < len(names) {
		return names[i]
	}
	return fmt.Sprintf("dungeon item bit %d", i)
}

// danLabel names bit of byte rel of mDan (dSv_danBit_c, d_save.h:752-754;
// switches 0x80-0xBF, d_save.cpp:1238-1241).
func danLabel(rel, bit int) (string, bool) {
	switch {
	case rel == addr.DanOffStageNo:
		return "mDan.mStageNo (live table)", false
	case rel >= addr.DanOffSwitch:
		return fmt.Sprintf("dan switch 0x%02X", addr.SwitchDanFirst+beIndex(rel-addr.DanOffSwitch, 4, bit)), true
	}
	return fmt.Sprintf("mDan+0x%X", rel), false
}

// zoneLabel names bit of byte rel of mZone[z] (dSv_zone_c,
// d_save.h:785-798): switches 0xC0-0xEF as u16 words
// (dSv_zoneBit_c::onSwitch, d_save.cpp:1279-1282), one u16 of items
// (d_save.cpp:1307-1310) and 512 dead-actor bits (d_save.cpp:1326-1329).
func zoneLabel(z, rel, bit int) (string, bool) {
	p := fmt.Sprintf("zone %d", z)
	switch {
	case rel == addr.ZoneOffRoomNo:
		return p + " room", false
	case rel < addr.ZoneOffSwitch:
		return p + " padding", false
	case rel < addr.ZoneOffItem:
		return fmt.Sprintf("%s switch 0x%02X", p, addr.SwitchZoneFirst+beIndex(rel-addr.ZoneOffSwitch, 2, bit)), true
	case rel < addr.ZoneOffItem+2:
		return fmt.Sprintf("%s item %d", p, beIndex(rel-addr.ZoneOffItem, 2, bit)), true
	case rel < addr.ZoneOffActor:
		return p + " padding", false
	}
	return fmt.Sprintf("%s actor %d", p, beIndex(rel-addr.ZoneOffActor, 4, bit)), true
}

// eventLabel names bit of byte hh of a dSv_event_c: event bit id 0xHHLL
// in bytes 0x00-0x78, a register byte from 0x79 (onEventBit and
// setEventReg, d_save.cpp:1187-1210). With checkNames, a bit outside
// named, the byte's named-bit mask, is marked unnamed.
func eventLabel(prefix string, hh, bit int, named byte, checkNames bool) (string, bool) {
	if hh >= addr.EventRegFirst {
		return fmt.Sprintf("%s register byte 0x%02X", prefix, hh), false
	}
	if bit < 0 {
		return fmt.Sprintf("%s byte 0x%02X", prefix, hh), true
	}
	l := fmt.Sprintf("%s 0x%02X%02X", prefix, hh, 1<<bit)
	if checkNames && named&(1<<bit) == 0 {
		l += " (unnamed)"
	}
	return l, true
}

// coopSpans is the class table; coopSpanAt[off] indexes the span holding
// image byte off, or is -1.
var coopSpans, coopSpanAt = func() ([]class.Span, [addr.SaveImageSize]int) {
	spans := class.Spans()
	var at [addr.SaveImageSize]int
	for i := range at {
		at[i] = -1
	}
	for i, s := range spans {
		for off := s.Off; off < s.Off+s.Len && off < len(at); off++ {
			at[off] = i
		}
	}
	return spans, at
}()

// playerLabel names bit of byte off of dSv_player_c (image offsets below
// 0x380) by the class table's field names, with the charts and the sea
// map spelled out (dSv_player_map_c, d_save.cpp:851-933).
func playerLabel(off, bit int) (string, bool) {
	switch {
	case off >= addr.OffMapGet && off < addr.OffFmapBits:
		row := [...]string{"owned", "deciphered", "salvaged"}[(off-addr.OffMapGet)/16]
		return fmt.Sprintf("chart %d %s", beIndex((off-addr.OffMapGet)%16, 4, bit), row), true
	case off >= addr.OffFmapBits && off < addr.OffMap71:
		return fmt.Sprintf("sea map %d bit %d", off-addr.OffFmapBits, bit), true
	case off == addr.OffMapTriforce:
		return fmt.Sprintf("Triforce chart %d", bit), true
	case off >= addr.OffGetItem && off < addr.OffGetItem+addr.GetItemSize:
		// One array (d_save.h:131), which the class table splits at
		// [3]: a span-relative index would miscount.
		return fmt.Sprintf("mGetItem[%d] bit %d", off-addr.OffGetItem, bit), true
	case off >= addr.OffCollectFlags && off < addr.OffCollectFlags+5:
		return fmt.Sprintf("mCollect[%d] bit %d", off-addr.OffCollectFlags, bit), true
	}
	i := coopSpanAt[off]
	if i < 0 {
		return fmt.Sprintf("dSv_player_c+0x%X", off), false
	}
	sp := coopSpans[i]
	rel := off - sp.Off
	flags := sp.Class == class.OR || sp.Class == class.ORMasked
	switch {
	case !flags && sp.Len == 1:
		return sp.Field, false
	case !flags:
		return fmt.Sprintf("%s+0x%X", sp.Field, rel), false
	case sp.Len == 1:
		return fmt.Sprintf("%s bit %d", sp.Field, bit), true
	}
	return fmt.Sprintf("%s +%d bit %d", sp.Field, rel, bit), true
}

// ---------------------------------------------------------------------
// actors

// fopAc_ac_c fields and actor-queue facts addr leaves out.
const (
	actorOffCurPos    = 0x1F8 // current.pos: actor_place.pos is +0 (f_op_actor.h:213,289)
	actorOffMaxHealth = 0x284 // s8 max_health (f_op_actor.h:306)
	tagSize           = 0x14  // create_tag_class (c_tag.h:10-14)

	// maxActorWalk is where the walk gives up (§7.2: "stop after 1024
	// nodes").
	maxActorWalk = 1024
)

// actorGroups names fopAc_Group_e (f_op_actor.h:54-60).
var actorGroups = [...]string{"actor", "player", "enemy", "env", "npc"}

// k1Class is an enemy class the M6 light-shrink kill can drive (§7.2):
// where its enemyice member is. enemyice.mpActor (+0) points back at the
// actor once the class's create has run (for example d_a_bk.cpp:5270),
// and mLightShrinkTimer is at +6 (c_damagereaction.h:12-16).
type k1Class struct {
	name   string
	iceOff uint32
}

// k1Classes maps the §7.2 classes' proc names (fpcNm_*_e, f_pc_name.h,
// whose comments give the GZLE01 values) to their enemyice member (each
// class header in include/d/actor/). The design marks tn, ph, pt, pw and
// bo for a live check first; the self-pointer below is that check.
var k1Classes = map[int16]k1Class{
	0x79: {"fm", 0xAE8},   // f_pc_name.h:130; d_a_fm.h:213
	0xB6: {"bb", 0xC80},   // f_pc_name.h:191; d_a_bb.h:106
	0xBD: {"mo2", 0x2A54}, // f_pc_name.h:198; d_a_mo2.h:175
	0xBE: {"bk", 0x1264},  // f_pc_name.h:199; d_a_bk.h:140
	0xBF: {"st", 0x1FA8},  // f_pc_name.h:200; d_a_st.h:145
	0xC0: {"tn", 0x1524},  // f_pc_name.h:201; d_a_tn.h:162
	0xCB: {"am", 0xABC},   // f_pc_name.h:215; d_a_am.h:55
	0xCC: {"am2", 0xA8C},  // f_pc_name.h:216; d_a_am2.h:57
	0xCE: {"ks", 0x6A8},   // f_pc_name.h:218; d_a_ks.h:51
	0xCF: {"cc", 0x81C},   // f_pc_name.h:219; d_a_cc.h:92
	0xD2: {"ph", 0x998},   // f_pc_name.h:222; d_a_ph.h:68
	0xD5: {"pw", 0x884},   // f_pc_name.h:225; d_a_pw.h:84
	0xD7: {"bo", 0x9FC},   // f_pc_name.h:227; d_a_bo.h:15
	0xD8: {"ki", 0x924},   // f_pc_name.h:228; d_a_ki.h:85
	0xE1: {"rd", 0x6E0},   // f_pc_name.h:237; d_a_rd.h:172
	0xF9: {"pt", 0x7E4},   // f_pc_name.h:261; d_a_pt.h:73
}

// procName is a short name for proc p, where actors knows one.
func procName(p int16) string {
	switch p {
	case 0xA7: // fpcNm_SHIP_e, f_pc_name.h:176
		return "ship"
	case 0xA9: // fpcNm_PLAYER_e, f_pc_name.h:178
		return "player"
	}
	return k1Classes[p].name
}

// actorRow is one actor the walk found.
type actorRow struct {
	Node, Actor       uint32
	ProcID            uint32 // mBsPcId
	ProcName          int16
	SetID             uint16
	Group             uint8
	HomeRoom          int8
	Pos               [3]float32 // current.pos
	Health, MaxHealth int8
	// DataOK is whether the tag's mpData is &g_fopAcTg_Queue, as
	// cLs_Addition leaves it (c_list.cpp:40; cNd_SetObject,
	// c_node.cpp:98-103).
	DataOK bool
	// Ice is the enemyice check for a k1Classes actor, "" for others.
	Ice string
}

// actorWalk is the result of walking g_fopAcTg_Queue.
type actorWalk struct {
	Head, Tail uint32
	Size       int32  // the queue's mSize
	Type       uint32 // g_fopAc_type
	Rows       []actorRow
	// Stop says why the walk ended before the list did; "" when it
	// reached a null next.
	Stop string
	// Notes are inconsistencies that did not stop it.
	Notes []string
}

// walkActors walks g_fopAcTg_Queue, a node_list_class of every actor's
// actor_tag (fopAc_Create, f_op_actor.cpp:262-263), from head to tail and
// at most limit nodes. Each node is a create_tag_class at actor +0xC4 whose
// mpTagData is the actor (cTg_Create, c_tag.cpp:89-92). Every pointer is
// checked before it is followed: the node must lie in MEM1 with room for
// its actor before it, its prev must be the node before (the list did not
// change under the walk), mpTagData must be node - 0xC4, and the actor's
// actor_type must equal g_fopAc_type (fopAc_IsActor, f_op_actor.cpp:
// 116-119). The walk stops at the first failure and on a repeated node.
// The error is a read error; a bad list is reported in Stop.
func walkActors(m mem.Mem, limit int) (actorWalk, error) {
	var w actorWalk
	q, err := m.Read(addr.ActorQueue, addr.ActorQueueSize)
	if err != nil {
		return w, err
	}
	w.Head = be32(q[addr.ActorQueueOffHead:])
	w.Tail = be32(q[addr.ActorQueueOffTail:])
	w.Size = int32(be32(q[addr.ActorQueueOffSize:]))
	if w.Type, err = mem.ReadU32(m, addr.FopAcType); err != nil {
		return w, err
	}

	seen := make(map[uint32]bool)
	var prev uint32
	for node := w.Head; node != 0; {
		if len(w.Rows) >= limit {
			w.Stop = fmt.Sprintf("stopped at the limit of %d nodes", limit)
			break
		}
		if why := actorNodeBad(node); why != "" {
			w.Stop = fmt.Sprintf("node 0x%08X %s", node, why)
			break
		}
		if seen[node] {
			w.Stop = fmt.Sprintf("node 0x%08X comes round again: the list has a cycle", node)
			break
		}
		seen[node] = true
		tag, err := m.Read(node, tagSize)
		if err != nil {
			return w, err
		}
		if p := be32(tag[addr.TagOffPrev:]); p != prev {
			w.Stop = fmt.Sprintf("node 0x%08X has prev 0x%08X, not 0x%08X: the list changed during the walk", node, p, prev)
			break
		}
		actor := node - addr.ActorOffActorTag
		if d := be32(tag[addr.TagOffTagData:]); d != actor {
			w.Stop = fmt.Sprintf("node 0x%08X has mpTagData 0x%08X, not its actor 0x%08X", node, d, actor)
			break
		}
		a, err := m.Read(actor, addr.ActorSize)
		if err != nil {
			return w, err
		}
		if t := be32(a[addr.ActorOffActorType:]); t != w.Type {
			w.Stop = fmt.Sprintf("actor 0x%08X has actor_type 0x%08X, not g_fopAc_type 0x%08X", actor, t, w.Type)
			break
		}
		r := actorRow{
			Node:      node,
			Actor:     actor,
			ProcID:    be32(a[addr.ActorOffBsPcID:]),
			ProcName:  int16(binary.BigEndian.Uint16(a[addr.ActorOffProcName:])),
			SetID:     binary.BigEndian.Uint16(a[addr.ActorOffSetID:]),
			Group:     a[addr.ActorOffGroup],
			HomeRoom:  int8(a[addr.ActorOffHomeRoomNo]),
			Health:    int8(a[addr.ActorOffHealth]),
			MaxHealth: int8(a[actorOffMaxHealth]),
			DataOK:    be32(tag[addr.TagOffData:]) == addr.ActorQueue,
		}
		for i := range r.Pos {
			r.Pos[i] = math.Float32frombits(be32(a[actorOffCurPos+4*i:]))
		}
		r.Ice = iceCheck(m, r)
		w.Rows = append(w.Rows, r)
		prev, node = node, be32(tag[addr.TagOffNext:])
	}
	if w.Stop == "" {
		if prev != w.Tail {
			w.Notes = append(w.Notes, fmt.Sprintf("the last node is 0x%08X but mpTail is 0x%08X", prev, w.Tail))
		}
		if int32(len(w.Rows)) != w.Size {
			w.Notes = append(w.Notes, fmt.Sprintf("walked %d nodes but mSize is %d", len(w.Rows), w.Size))
		}
	}
	return w, nil
}

// actorNodeBad says why node cannot be an actor_tag, or "".
func actorNodeBad(node uint32) string {
	switch {
	case node&3 != 0:
		return "is not word-aligned"
	case mem.CheckRange(node, tagSize) != nil:
		return "is outside MEM1"
	case node < addr.MEM1Base+addr.ActorOffActorTag || mem.CheckRange(node-addr.ActorOffActorTag, addr.ActorSize) != nil:
		return "leaves no room in MEM1 for its actor"
	}
	return ""
}

// iceCheck reads a k1Classes actor's enemyice: whether mpActor points back
// at the actor, and the light-shrink timer.
func iceCheck(m mem.Mem, r actorRow) string {
	k, ok := k1Classes[r.ProcName]
	if !ok {
		return ""
	}
	b, err := m.Read(r.Actor+k.iceOff, 8)
	if err != nil {
		return "enemyice unreadable"
	}
	if self := be32(b); self != r.Actor {
		return fmt.Sprintf("enemyice.mpActor 0x%08X is not the actor", self)
	}
	return fmt.Sprintf("enemyice ok, shrink timer %d", int8(b[6]))
}

func printActors(w io.Writer, aw actorWalk) {
	fmt.Fprintf(w, "g_fopAcTg_Queue 0x%08X: head 0x%08X, tail 0x%08X, mSize %d; g_fopAc_type 0x%08X\n",
		addr.ActorQueue, aw.Head, aw.Tail, aw.Size, aw.Type)
	notQueued := 0
	for i, r := range aw.Rows {
		set := "-"
		if r.SetID != addr.ActorSetIDNone {
			set = fmt.Sprintf("%d", r.SetID)
		}
		group := fmt.Sprintf("group%d", r.Group)
		if int(r.Group) < len(actorGroups) {
			group = actorGroups[r.Group]
		}
		hp := ""
		if r.Group == 2 || r.MaxHealth != 0 { // fopAc_ENEMY_e, f_op_actor.h:57
			hp = fmt.Sprintf("  hp %d/%d", r.Health, r.MaxHealth)
		}
		extra := ""
		if r.Ice != "" {
			extra += "  " + r.Ice
		}
		if !r.DataOK {
			extra += "  mpData is not the queue"
			notQueued++
		}
		fmt.Fprintf(w, "%4d  0x%08X  pid %-6d proc 0x%04X %-6s set %-5s %-6s room %3d  (%9.1f %9.1f %9.1f)%s%s\n",
			i, r.Actor, r.ProcID, uint16(r.ProcName), procName(r.ProcName), set, group, r.HomeRoom,
			r.Pos[0], r.Pos[1], r.Pos[2], hp, extra)
	}
	fmt.Fprintf(w, "%d actors", len(aw.Rows))
	if aw.Stop != "" {
		fmt.Fprintf(w, "; walk stopped: %s", aw.Stop)
	}
	fmt.Fprintln(w)
	for _, n := range aw.Notes {
		fmt.Fprintf(w, "note: %s\n", n)
	}
	if notQueued > 0 {
		fmt.Fprintf(w, "note: %d tags have mpData other than &g_fopAcTg_Queue, which cLs_Addition sets (c_list.cpp:40)\n", notQueued)
	}
}

// ---------------------------------------------------------------------
// zone-dump

// zoneDump prints the 32 mZone slots and the rooms' mZoneNo map from one
// snapshot (§3.1 zone writes). A slot removeZone freed keeps its bits
// (only mRoomNo is reset, d_save.h:793), so a free slot with bits is shown.
func zoneDump(w io.Writer, s *snap.Snapshot) {
	fmt.Fprintf(w, "%s\n", where(s))
	var empty []int
	for z := range addr.ZoneCount {
		base := addr.InfoOffZone + z*addr.ZoneSize
		zb := s.Save[base : base+addr.ZoneSize]
		room := int8(zb[addr.ZoneOffRoomNo])
		sw, rsw := zoneSwitches(zb)
		item := binary.BigEndian.Uint16(zb[addr.ZoneOffItem:])
		dead := zoneActors(zb)
		if room == -1 && len(sw)+len(rsw)+len(dead) == 0 && item == 0 {
			empty = append(empty, z)
			continue
		}
		state := fmt.Sprintf("room %2d", room)
		if room == -1 {
			state = "free   "
		}
		fmt.Fprintf(w, "  zone %2d  %s  switches %s  room switches %s  items 0x%04X  dead actors %s\n",
			z, state, fmtNums(sw, true), fmtNums(rsw, true), item, fmtNums(dead, false))
	}
	if len(empty) > 0 {
		fmt.Fprintf(w, "  free and empty: zones %s\n", fmtNums(empty, false))
	}

	fmt.Fprintln(w, "mStatus[room].mZoneNo:")
	none := 0
	for room := range addr.RoomCount {
		z := s.ZoneNo[room]
		if z == -1 {
			none++
			continue
		}
		note := ""
		if _, ok := s.ZoneFor(room); !ok {
			if z < 0 || int(z) >= addr.ZoneCount {
				note = "  INVALID: no such zone"
			} else {
				held := int8(s.Save[addr.InfoOffZone+int(z)*addr.ZoneSize+addr.ZoneOffRoomNo])
				note = fmt.Sprintf("  INVALID: zone %d holds room %d", z, held)
			}
		}
		if int(s.StayNo) == room {
			note += "  <- Link"
		}
		fmt.Fprintf(w, "  room %2d -> zone %2d (count %d)%s\n", room, z, s.ZoneCount[room], note)
	}
	fmt.Fprintf(w, "  %d rooms have no zone\n", none)
}

// zoneSwitches lists the set zone switches (0xC0-0xDF) and room switches
// (0xE0-0xEF) of a dSv_zone_c.
func zoneSwitches(zb []byte) (zone, room []int) {
	for no := addr.SwitchZoneFirst; no < addr.SwitchEnd; no++ {
		st, b, _ := addr.SwitchBit(no)
		if zb[b.Off]&b.Mask == 0 {
			continue
		}
		if st == addr.SwitchRoom {
			room = append(room, no)
		} else {
			zone = append(zone, no)
		}
	}
	return zone, room
}

// zoneActors lists the dead-actor setIDs of a dSv_zone_c.
func zoneActors(zb []byte) []int {
	var out []int
	for id := range addr.ZoneActorCount {
		b, _ := addr.ZoneActorBit(id)
		if zb[b.Off]&b.Mask != 0 {
			out = append(out, id)
		}
	}
	return out
}

// fmtNums lists ns, at most 24 of them, in hex or decimal; "-" if none.
func fmtNums(ns []int, hex bool) string {
	if len(ns) == 0 {
		return "-"
	}
	const show = 24
	var parts []string
	for i, n := range ns {
		if i == show {
			parts = append(parts, fmt.Sprintf("... (%d in all)", len(ns)))
			break
		}
		if hex {
			parts = append(parts, fmt.Sprintf("0x%02X", n))
		} else {
			parts = append(parts, strconv.Itoa(n))
		}
	}
	return strings.Join(parts, ",")
}

// ---------------------------------------------------------------------
// frame-phase-probe

// phaseStallAfter is FrameWriter's heartbeat timeout (§4.2): a stretch
// this long without a beat counts as a stall.
const phaseStallAfter = 100 * time.Millisecond

// phaseAcc accumulates frame-phase-probe's reads of the heartbeat. Each
// observation is when a read finished (since the probe began), the value
// read, and how long the read took.
type phaseAcc struct {
	reads                     int
	readMin, readMax, readSum time.Duration
	readHist                  [40]int // reads by duration: bucket i holds those under 2^i µs

	have       bool
	lastAt     time.Duration // the last read
	lastHB     uint32
	chain      bool          // lastChange is a beat the next one can be timed against
	lastChange time.Duration // the read that saw the last beat
	quietSince time.Duration // the last beat, or the start
	stalled    bool          // the current quiet stretch was counted as a stall
	changes    int
	beats      uint64
	timedBeats uint64        // beats in timed intervals
	timedSpan  time.Duration // their total time
	steps      []time.Duration
	skipped    int
	regressed  int
	stalls     int
	detectSum  time.Duration
	detectMax  time.Duration
}

func (a *phaseAcc) observe(at time.Duration, hb uint32, took time.Duration) {
	a.reads++
	a.readSum += took
	if a.reads == 1 || took < a.readMin {
		a.readMin = took
	}
	a.readMax = max(a.readMax, took)
	a.readHist[min(bits.Len64(uint64(took/time.Microsecond)), len(a.readHist)-1)]++

	if !a.have {
		a.have, a.lastAt, a.lastHB, a.quietSince = true, at, hb, at
		return
	}
	switch d := int32(hb - a.lastHB); {
	case d == 0:
		if !a.stalled && at-a.quietSince >= phaseStallAfter {
			a.stalls++
			a.stalled = true
		}
	case d < 0:
		// A save state was loaded, or Dolphin restarted the game: start
		// timing afresh.
		a.regressed++
		a.chain = false
		a.quietSince, a.stalled = at, false
	default:
		a.changes++
		a.beats += uint64(d)
		gap := at - a.lastAt // the beat happened somewhere in here
		a.detectSum += gap
		a.detectMax = max(a.detectMax, gap)
		if d > 1 {
			a.skipped++
		}
		if a.chain {
			iv := at - a.lastChange
			a.timedSpan += iv
			a.timedBeats += uint64(d)
			if d == 1 {
				a.steps = append(a.steps, iv)
			}
		}
		a.chain, a.lastChange = true, at
		a.quietSince, a.stalled = at, false
	}
	a.lastAt, a.lastHB = at, hb
}

// phaseReport is what frame-phase-probe found.
type phaseReport struct {
	Reads                              int
	ReadMin, ReadP50, ReadP99, ReadMax time.Duration // P50 and P99 are bucket upper bounds
	Changes                            int
	Beats                              uint64
	Period                             time.Duration // mean time per beat
	Jitter                             time.Duration // standard deviation of single-beat intervals
	StepMin, StepMax                   time.Duration
	Skipped, Regressed, Stalls         int
	DetectMean, DetectMax              time.Duration
}

func (a *phaseAcc) report() phaseReport {
	r := phaseReport{
		Reads: a.reads, ReadMin: a.readMin, ReadMax: a.readMax,
		Changes: a.changes, Beats: a.beats,
		Skipped: a.skipped, Regressed: a.regressed, Stalls: a.stalls,
		DetectMax: a.detectMax,
	}
	r.ReadP50, r.ReadP99 = a.readPercentile(0.50), a.readPercentile(0.99)
	if a.changes > 0 {
		r.DetectMean = a.detectSum / time.Duration(a.changes)
	}
	if a.timedBeats > 0 {
		r.Period = a.timedSpan / time.Duration(a.timedBeats)
	}
	if n := len(a.steps); n > 0 {
		r.StepMin, r.StepMax = slices.Min(a.steps), slices.Max(a.steps)
		var mean float64
		for _, s := range a.steps {
			mean += float64(s)
		}
		mean /= float64(n)
		var v float64
		for _, s := range a.steps {
			v += (float64(s) - mean) * (float64(s) - mean)
		}
		r.Jitter = time.Duration(math.Sqrt(v / float64(n)))
	}
	return r
}

// readPercentile is the upper bound of the histogram bucket holding the
// p-th read duration.
func (a *phaseAcc) readPercentile(p float64) time.Duration {
	if a.reads == 0 {
		return 0
	}
	want := int(math.Ceil(p * float64(a.reads)))
	n := 0
	for i, c := range a.readHist {
		if n += c; n >= want {
			return time.Duration(1<<i) * time.Microsecond
		}
	}
	return a.readMax
}

// framePhaseProbe spin-reads spawn_trigger for d and reports the
// heartbeat's period and jitter as Go sees it, and how quickly Go sees a
// beat: the window a FrameWriter write lands in after one (§4.2.1).
func framePhaseProbe(ctx context.Context, w io.Writer, m mem.Mem, d time.Duration) error {
	var acc phaseAcc
	start := time.Now()
	for i := 0; ; i++ {
		t0 := time.Now()
		hb, err := mem.ReadU32(m, addr.SpawnTrigger)
		if err != nil {
			return err
		}
		took := time.Since(t0)
		acc.observe(t0.Sub(start)+took, hb, took)
		if time.Since(start) >= d {
			break
		}
		if i%1024 == 0 && ctx.Err() != nil {
			break
		}
	}
	printPhase(w, acc.report(), time.Since(start))
	return nil
}

func printPhase(w io.Writer, r phaseReport, ran time.Duration) {
	ms := func(d time.Duration) string { return fmt.Sprintf("%.2f ms", float64(d)/float64(time.Millisecond)) }
	us := func(d time.Duration) string { return fmt.Sprintf("%d µs", d/time.Microsecond) }
	fmt.Fprintf(w, "heartbeat  spawn_trigger 0x%08X over %.1f s: %d beats seen as %d changes\n",
		addr.SpawnTrigger, ran.Seconds(), r.Beats, r.Changes)
	if r.Changes == 0 {
		fmt.Fprintln(w, "           the heartbeat never moved: is emulation paused, or the ISO not patched?")
		return
	}
	hz := 0.0
	if r.Period > 0 {
		hz = float64(time.Second) / float64(r.Period)
	}
	fmt.Fprintf(w, "period     %s mean (%.1f Hz); single beats %s to %s, jitter (stddev) %s\n",
		ms(r.Period), hz, ms(r.StepMin), ms(r.StepMax), ms(r.Jitter))
	fmt.Fprintf(w, "missed     %d changes skipped a beat (Go did not poll in time), %d regressions, %d stalls of %v or more\n",
		r.Skipped, r.Regressed, r.Stalls, phaseStallAfter)
	fmt.Fprintf(w, "reads      %d, one every %s; latency min %s, p50 <= %s, p99 <= %s, max %s\n",
		r.Reads, us(ran/time.Duration(max(r.Reads, 1))), us(r.ReadMin), us(r.ReadP50), us(r.ReadP99), us(r.ReadMax))
	fmt.Fprintf(w, "detection  Go saw each beat within %s on average, %s at worst\n", us(r.DetectMean), us(r.DetectMax))
	fmt.Fprintf(w, "window     a FrameWriter read-OR-write lands about %s after a beat (detection + 2 reads), %s at worst;\n",
		us(r.DetectMean+2*r.ReadP50), us(r.DetectMax+2*r.ReadMax))
	fmt.Fprintln(w, "           that is the time in which a game store to the same byte would be lost (§4.2.1)")
}

// ---------------------------------------------------------------------
// heap-free

// Heap statics addr leaves out.
const (
	solidHeapErrors   uint32 = 0x803F68F4 // sym:18576
	commandHeapErrors uint32 = 0x803F6900 // sym:18579
	unknownHeapErrors uint32 = 0x803F6908 // sym:18581
	heapErrorsTotal   uint32 = 0x803F690C // heapErrors: every failure, any heap (sym:18582)
	commandHeapPtr    uint32 = 0x803F6930 // sym:18592; created by mDoExt_createCommandHeap (m_Do_ext.cpp:911)
	expHeapVtable     uint32 = 0x8039CC60 // __vt__10JKRExpHeap (sym:16882)

	// expHeapHeaderSize covers a JKRExpHeap through mTailUsedList
	// (JKRExpHeap.h:96-106).
	expHeapHeaderSize = addr.ExpHeapOffUsedTail + 4
	maxFreeBlocks     = 1 << 16
)

// heapCounters are the allocation-failure counters, in declaration order
// (m_Do_machine.cpp:35-41); the failure handler bumps one per failure by
// heap (m_Do_machine.cpp:121-135).
var heapCounters = []struct {
	name string
	a    uint32
}{
	{"solid", solidHeapErrors},
	{"game", addr.GameHeapErrors},
	{"zelda", addr.ZeldaHeapErrors},
	{"command", commandHeapErrors},
	{"archive", addr.ArchiveHeapErrors},
	{"unknown", unknownHeapErrors},
	{"total", heapErrorsTotal},
}

// heapInfo is one JKRExpHeap's free list.
type heapInfo struct {
	Name                  string
	Ptr, Start, End, Size uint32
	Blocks                int
	Free                  uint64 // what do_getTotalFreeSize returns (JKRExpHeap.cpp:487-496)
	Largest               uint32 // what do_getFreeSize returns (JKRExpHeap.cpp:459-470)
	// Problem says why the walk was cut short or the list looks
	// inconsistent; the totals then cover what was walked.
	Problem string
}

// walkHeap reads the JKRExpHeap whose pointer is at ptrAddr and sums its
// free list (mHeadFreeList, JKRExpHeap.h:103) the way do_getTotalFreeSize
// does. The heap must carry JKRExpHeap's vtable and sane bounds; each
// block must lie inside the heap with its content (the CMemBlock header,
// JKRExpHeap.h:36-42, then size bytes), link back to the block before it
// and not repeat. Free blocks are not checked for the 'HM' magic: the
// first is initiated with it but setFreeBlock zeroes it (JKRExpHeap.cpp:
// 85,575). The error is a read error; a bad list is reported in Problem.
func walkHeap(m mem.Mem, name string, ptrAddr uint32) (heapInfo, error) {
	h := heapInfo{Name: name}
	p, err := mem.ReadU32(m, ptrAddr)
	if err != nil {
		return h, err
	}
	if h.Ptr = p; p == 0 {
		h.Problem = "not created"
		return h, nil
	}
	hdr, err := m.Read(p, expHeapHeaderSize)
	if err != nil {
		h.Problem = fmt.Sprintf("heap pointer 0x%08X: %v", p, err)
		return h, nil
	}
	if vt := be32(hdr); vt != expHeapVtable {
		h.Problem = fmt.Sprintf("vtable 0x%08X is not JKRExpHeap's 0x%08X", vt, expHeapVtable)
		return h, nil
	}
	h.Start, h.End, h.Size = be32(hdr[addr.HeapOffStart:]), be32(hdr[addr.HeapOffEnd:]), be32(hdr[addr.HeapOffSize:])
	if h.Start >= h.End || mem.CheckRange(h.Start, int(h.End-h.Start)) != nil || h.Size != h.End-h.Start {
		h.Problem = fmt.Sprintf("bounds 0x%08X-0x%08X size 0x%X do not fit MEM1", h.Start, h.End, h.Size)
		return h, nil
	}
	tail := be32(hdr[addr.ExpHeapOffFreeTail:])
	seen := make(map[uint32]bool)
	var prev uint32
	for b := be32(hdr[addr.ExpHeapOffFreeHead:]); b != 0; {
		switch {
		case h.Blocks >= maxFreeBlocks:
			h.Problem = fmt.Sprintf("more than %d free blocks", maxFreeBlocks)
		case b&3 != 0 || b < h.Start || b > h.End-addr.MemBlockSize:
			h.Problem = fmt.Sprintf("free block 0x%08X is outside the heap", b)
		case seen[b]:
			h.Problem = fmt.Sprintf("free block 0x%08X comes round again: the list has a cycle", b)
		}
		if h.Problem != "" {
			return h, nil
		}
		seen[b] = true
		blk, err := m.Read(b, addr.MemBlockSize)
		if err != nil {
			return h, err
		}
		if bp := be32(blk[addr.MemBlockOffPrev:]); bp != prev {
			h.Problem = fmt.Sprintf("free block 0x%08X has prev 0x%08X, not 0x%08X: the heap changed during the walk", b, bp, prev)
			return h, nil
		}
		size := be32(blk[addr.MemBlockOffSize:])
		if uint64(b)+addr.MemBlockSize+uint64(size) > uint64(h.End) {
			h.Problem = fmt.Sprintf("free block 0x%08X of 0x%X bytes runs past the heap's end", b, size)
			return h, nil
		}
		h.Blocks++
		h.Free += uint64(size)
		h.Largest = max(h.Largest, size)
		prev, b = b, be32(blk[addr.MemBlockOffNext:])
	}
	if prev != tail {
		h.Problem = fmt.Sprintf("the last free block is 0x%08X but mTailFreeList is 0x%08X", prev, tail)
	}
	return h, nil
}

// heapFree reports the heap error counters, where the arena starts, and
// each JKRExpHeap's free space: the §4.5 margin an arena bump would take.
func heapFree(w io.Writer, m mem.Mem) error {
	fmt.Fprintln(w, "heap error counters (any increase means an allocation failed):")
	var parts []string
	for _, c := range heapCounters {
		v, err := mem.ReadU32(m, c.a)
		if err != nil {
			return err
		}
		parts = append(parts, fmt.Sprintf("%s %d", c.name, v))
	}
	fmt.Fprintf(w, "  %s\n", strings.Join(parts, "  "))

	codeEnd, err := mem.ReadU32(m, addr.JKRHeapCodeEnd)
	if err != nil {
		return err
	}
	arenaLo, err := mem.ReadU32(m, addr.OSArenaLo)
	if err != nil {
		return err
	}
	switch {
	case codeEnd >= addr.PatchedArenaLo:
		fmt.Fprintf(w, "arena: JKRHeap::mCodeEnd 0x%08X, 0x%X above the patched __OSArenaLo 0x%08X\n",
			codeEnd, codeEnd-addr.PatchedArenaLo, addr.PatchedArenaLo)
	default:
		fmt.Fprintf(w, "arena: JKRHeap::mCodeEnd 0x%08X is below the patched __OSArenaLo 0x%08X: a vanilla ISO, or not booted?\n",
			codeEnd, addr.PatchedArenaLo)
	}
	fmt.Fprintf(w, "       __OSArenaLo now 0x%08X (initArena moves it to the arena top, JKRHeap.cpp:94)\n", arenaLo)

	fmt.Fprintln(w, "JKRExpHeap free lists (free = getTotalFreeSize, largest = getFreeSize):")
	for _, hp := range []struct {
		name string
		a    uint32
	}{
		{"zelda", addr.ZeldaHeapPtr},
		{"game", addr.GameHeapPtr},
		{"archive", addr.ArchiveHeapPtr},
		{"command", commandHeapPtr},
	} {
		h, err := walkHeap(m, hp.name, hp.a)
		if err != nil {
			return err
		}
		fmt.Fprintf(w, "  %-8s 0x%08X", h.Name, h.Ptr)
		if h.Size != 0 {
			fmt.Fprintf(w, "  0x%08X-0x%08X (%d KiB): free %d KiB in %d blocks, largest %d KiB",
				h.Start, h.End, h.Size/1024, h.Free/1024, h.Blocks, h.Largest/1024)
		}
		if h.Problem != "" {
			fmt.Fprintf(w, "  [%s]", h.Problem)
		}
		fmt.Fprintln(w)
	}
	return nil
}

// ---------------------------------------------------------------------
// blob-verify-live

// byteRun is a run of differing bytes, as offsets.
type byteRun struct{ Off, Len int }

// blobDiff compares a live read-back with the bytes that should be there.
type blobDiff struct {
	Diff    int       // bytes that differ
	Runs    []byteRun // the first runs of differing bytes
	More    int       // runs past those
	AllZero bool      // live is all zero: nothing loaded there
}

func compareBlob(live, want []byte, maxRuns int) blobDiff {
	var d blobDiff
	d.AllZero = !slices.ContainsFunc(live, func(b byte) bool { return b != 0 })
	for i := 0; i < len(want); {
		if i < len(live) && live[i] == want[i] {
			i++
			continue
		}
		j := i
		for j < len(want) && (j >= len(live) || live[j] != want[j]) {
			j++
		}
		d.Diff += j - i
		if len(d.Runs) < maxRuns {
			d.Runs = append(d.Runs, byteRun{i, j - i})
		} else {
			d.More++
		}
		i = j
	}
	return d
}

// blobVerifyLive reads T2 back from the running game and compares it with
// the blob this binary patches in (internal/inject T2Bytes): the §4.5
// option B loader check. It writes nothing.
func blobVerifyLive(w io.Writer, m mem.Mem) error {
	want := inject.T2Bytes
	live, err := m.Read(inject.T2Address, len(want))
	if err != nil {
		return err
	}
	end := inject.T2Address + uint32(len(want))
	d := compareBlob(live, want, 8)
	fmt.Fprintf(w, "T2 0x%08X-0x%08X (0x%X bytes of inject.T2Bytes): ", inject.T2Address, end, len(want))
	switch {
	case d.Diff == 0:
		fmt.Fprintln(w, "MATCH")
		return nil
	case d.AllZero:
		fmt.Fprintln(w, "all zero: the section did not load (a vanilla ISO, or Dolphin dropped it)")
		return nil
	}
	first := d.Runs[0].Off
	fmt.Fprintf(w, "0x%X bytes differ in %d runs; the first at +0x%04X (0x%08X): live %02X, want %02X\n",
		d.Diff, len(d.Runs)+d.More, first, inject.T2Address+uint32(first), live[first], want[first])
	for _, r := range d.Runs {
		fmt.Fprintf(w, "  +0x%04X..+0x%04X (%d bytes)\n", r.Off, r.Off+r.Len, r.Len)
	}
	if d.More > 0 {
		fmt.Fprintf(w, "  ... and %d more runs\n", d.More)
	}
	fmt.Fprintln(w, "The mod's static variables (multiplayer.c) live in T2 and change as it runs, so differences")
	fmt.Fprintln(w, "inside its data are expected; differences in code mean a different build, a stale save state")
	fmt.Fprintln(w, "or a section that did not load.")
	return nil
}

// ---------------------------------------------------------------------
// region-probe

// canaryXor makes each canary word depend on its address, so a word
// copied or shifted by something else still reads as changed. The result
// for these regions (0xDA1A....) is not a MEM1 pointer.
const canaryXor uint32 = 0x5A5A5A5A

func canaryWord(a uint32) uint32 { return a ^ canaryXor }

// canaryChunk is the most region-probe writes in one call.
const canaryChunk = 0x400

// canaryRegions are the two gaps beside the mod the design wants proven
// free (§4.5, §11 item 5): the TRK stack up to T2, and the slack between
// the mailbox and the patched arena.
var canaryRegions = []addr.Region{addr.CanaryTRK, addr.CanaryArena}

// canaryForbidden is what a canary must never cover: the main stack and
// the header above it that boot writes, T2 with its slack and the
// mailbox, and the patched arena (§4.5 layout).
func canaryForbidden() []addr.Region {
	return []addr.Region{
		addr.MainStack,
		addr.MainStackHeader,
		{Name: "T2, its slack and the mailbox", Start: inject.T2Address, End: addr.Mailbox + addr.MailboxSize},
		{Name: "the arena", Start: addr.PatchedArenaLo, End: addr.MEM1End},
	}
}

// canarySafe checks that r is somewhere region-probe may write.
func canarySafe(r addr.Region) error {
	if r.Start >= r.End || r.Start%4 != 0 || r.End%4 != 0 {
		return fmt.Errorf("%s 0x%08X-0x%08X is not a word-aligned range", r.Name, r.Start, r.End)
	}
	if err := mem.CheckRange(r.Start, int(r.Size())); err != nil {
		return err
	}
	if t2End := inject.T2Address + uint32(len(inject.T2Bytes)); t2End > addr.Mailbox {
		return fmt.Errorf("T2 ends at 0x%08X, past the mailbox at 0x%08X: the layout changed, so the canary ranges are unproven", t2End, addr.Mailbox)
	}
	for _, f := range canaryForbidden() {
		if r.Start < f.End && f.Start < r.End {
			return fmt.Errorf("%s 0x%08X-0x%08X overlaps %s 0x%08X-0x%08X", r.Name, r.Start, r.End, f.Name, f.Start, f.End)
		}
	}
	return nil
}

// canaryPreflight refuses to arm unless the patched layout is running.
// On a vanilla ISO both regions are inside the arena, which is heap. The
// heartbeat must move (the mod runs) and JKRHeap::mCodeEnd, where
// initArena started the heaps (JKRHeap.cpp:77-88), must be at or above
// the patched __OSArenaLo.
func canaryPreflight(m mem.Mem, tick func()) error {
	codeEnd, err := mem.ReadU32(m, addr.JKRHeapCodeEnd)
	if err != nil {
		return err
	}
	if codeEnd < addr.PatchedArenaLo || codeEnd >= addr.MEM1End {
		return fmt.Errorf("JKRHeap::mCodeEnd is 0x%08X, not at or above the patched __OSArenaLo 0x%08X: is this the patched ISO, booted?",
			codeEnd, addr.PatchedArenaLo)
	}
	hb, err := mem.ReadU32(m, addr.SpawnTrigger)
	if err != nil {
		return err
	}
	tick()
	hb2, err := mem.ReadU32(m, addr.SpawnTrigger)
	if err != nil {
		return err
	}
	if hb2 == hb {
		return errors.New("the heartbeat is not moving: is emulation paused, or the ISO not patched?")
	}
	return nil
}

// armCanary fills r with canary words. It refuses unless r is safe, and,
// without force, when r already holds words that are neither zero nor a
// canary: memory something uses.
func armCanary(m mem.Mem, r addr.Region, force bool) error {
	if err := canarySafe(r); err != nil {
		return err
	}
	cur, err := m.Read(r.Start, int(r.Size()))
	if err != nil {
		return err
	}
	busy, first := 0, -1
	for i := 0; i < len(cur); i += 4 {
		if v := be32(cur[i:]); v != 0 && v != canaryWord(r.Start+uint32(i)) {
			if busy++; first < 0 {
				first = i
			}
		}
	}
	if busy > 0 && !force {
		return fmt.Errorf("%s: %d of %d words already hold data (first 0x%08X = 0x%08X), so something may use it; not arming (--force arms anyway)",
			r.Name, busy, len(cur)/4, r.Start+uint32(first), be32(cur[first:]))
	}
	buf := make([]byte, r.Size())
	for i := 0; i < len(buf); i += 4 {
		binary.BigEndian.PutUint32(buf[i:], canaryWord(r.Start+uint32(i)))
	}
	for off := 0; off < len(buf); off += canaryChunk {
		if err := m.Write(r.Start+uint32(off), buf[off:min(off+canaryChunk, len(buf))]); err != nil {
			return err
		}
	}
	return nil
}

// wordRun is a run [Start, End) of changed canary words.
type wordRun struct{ Start, End uint32 }

// canaryCheck is what became of one region's canaries.
type canaryCheck struct {
	Region                addr.Region
	Words, Intact, Zeroed int
	Runs                  []wordRun // the first runs of changed words
	More                  int
}

// checkCanary reads r back and finds the words that are no longer canaries.
func checkCanary(m mem.Mem, r addr.Region) (canaryCheck, error) {
	c := canaryCheck{Region: r}
	if err := canarySafe(r); err != nil {
		return c, err
	}
	cur, err := m.Read(r.Start, int(r.Size()))
	if err != nil {
		return c, err
	}
	c.Words = len(cur) / 4
	const maxRuns = 8
	open := false
	for i := 0; i < len(cur); i += 4 {
		a := r.Start + uint32(i)
		v := be32(cur[i:])
		if v == canaryWord(a) {
			c.Intact++
			open = false
			continue
		}
		if v == 0 {
			c.Zeroed++
		}
		switch {
		case open:
			c.Runs[len(c.Runs)-1].End = a + 4
		case len(c.Runs) < maxRuns:
			c.Runs = append(c.Runs, wordRun{a, a + 4})
			open = true
		default:
			c.More++
		}
	}
	return c, nil
}

func regionArmWarning() string {
	var b strings.Builder
	b.WriteString("WARNING: region-probe arm WRITES GAME RAM. It fills these ranges with canary words:\n")
	for _, r := range canaryRegions {
		fmt.Fprintf(&b, "  %-12s 0x%08X-0x%08X (0x%X bytes)\n", r.Name, r.Start, r.End, r.Size())
	}
	b.WriteString("Both are free only by inference (docs/08 §4.5). If anything does use them, the game can\n")
	b.WriteString("crash or misbehave. Nothing else is written: never T2 (0x80410000), the mailbox (0x80412F00)\n")
	b.WriteString("or the arena. A save state loaded afterwards restores the old RAM and wipes the canaries.\n")
	return b.String()
}

// regionArm arms both regions after canaryPreflight. A region that fails
// is skipped and reported; the other is still armed.
func regionArm(w io.Writer, m mem.Mem, tick func(), force bool) error {
	if err := canaryPreflight(m, tick); err != nil {
		return err
	}
	var errs []error
	for _, r := range canaryRegions {
		if err := armCanary(m, r, force); err != nil {
			errs = append(errs, err)
			fmt.Fprintf(w, "%-12s NOT armed: %v\n", r.Name, err)
			continue
		}
		fmt.Fprintf(w, "%-12s armed: %d words at 0x%08X-0x%08X\n", r.Name, r.Size()/4, r.Start, r.End)
	}
	if len(errs) == 0 {
		fmt.Fprintln(w, "Play on (boot, menus, stage changes), then run `region-probe check`.")
	}
	return errors.Join(errs...)
}

// regionCheck reports which canary words changed. It writes nothing.
func regionCheck(w io.Writer, m mem.Mem) error {
	for _, r := range canaryRegions {
		c, err := checkCanary(m, r)
		if err != nil {
			return err
		}
		fmt.Fprintf(w, "%-12s 0x%08X-0x%08X (%d words): ", r.Name, r.Start, r.End, c.Words)
		switch changed := c.Words - c.Intact; {
		case changed == 0:
			fmt.Fprintln(w, "all intact")
			continue
		case c.Intact == 0:
			fmt.Fprintf(w, "no canaries (%d words zero): never armed, or a reboot or save state restored this RAM\n", c.Zeroed)
			continue
		default:
			fmt.Fprintf(w, "%d changed (%d now zero, %d other)\n", changed, c.Zeroed, changed-c.Zeroed)
		}
		for _, run := range c.Runs {
			fmt.Fprintf(w, "  0x%08X-0x%08X (%d words)\n", run.Start, run.End, (run.End-run.Start)/4)
		}
		if c.More > 0 {
			fmt.Fprintf(w, "  ... and %d more runs\n", c.More)
		}
	}
	return nil
}

// ---------------------------------------------------------------------
// stage-hop

// stageHop is a debug stage change: what dComIfGp_setNextStage hands
// mNextStage (d_com_inf_game.cpp:667-669; dStage_nextStage_c::set,
// d_stage.cpp:26-31), without its other effects.
type stageHop struct {
	Name  string
	Point int16
	Room  int8
	Layer int8
}

// Bounds on a stage-hop request.
const (
	// stageHopLayerMax: getLayerNo returns a set layer as is
	// (d_com_inf_game.cpp:268) and it indexes the 12-entry
	// l_layerFuncTable_p unchecked (d_stage.cpp:2144-2150).
	stageHopLayerMax = 11
	// stageHopWipe is the wipe written: 0, setNextStage's default
	// (d_com_inf_game.h:2247-2249). dScnPly_Draw asserts wipe < 12
	// (d_s_play.cpp:292-308).
	stageHopWipe = 0
)

// parseStageHop parses `<stage> [point] [room] [layer]`.
func parseStageHop(pos []string) (stageHop, error) {
	h := stageHop{Layer: -1}
	if len(pos) < 1 || len(pos) > 4 {
		return h, errors.New("give a stage name and at most a point, a room and a layer")
	}
	h.Name = pos[0]
	if len(h.Name) == 0 || len(h.Name) >= addr.StageNameLen {
		return h, fmt.Errorf("stage name %q must be 1-%d characters (mName is char[8])", h.Name, addr.StageNameLen-1)
	}
	for _, c := range h.Name {
		if !(c == '_' || c >= '0' && c <= '9' || c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z') {
			return h, fmt.Errorf("stage name %q: only letters, digits and _", h.Name)
		}
	}
	num := func(i, lo, hi int, name string) (int, error) {
		if i >= len(pos) {
			return 0, nil
		}
		v, err := strconv.Atoi(pos[i])
		if err != nil || v < lo || v > hi {
			return 0, fmt.Errorf("%s must be a number from %d to %d, not %q", name, lo, hi, pos[i])
		}
		return v, nil
	}
	// Negative points mean restarts the raw write does not set up
	// (d_com_inf_game.cpp:2369-2403 use -1 and -3), so only >= 0.
	p, err := num(1, 0, math.MaxInt16, "point")
	if err != nil {
		return h, err
	}
	r, err := num(2, 0, addr.RoomCount-1, "room")
	if err != nil {
		return h, err
	}
	h.Point, h.Room = int16(p), int8(r)
	if len(pos) > 3 {
		l, err := num(3, -1, stageHopLayerMax, "layer")
		if err != nil {
			return h, err
		}
		h.Layer = int8(l)
	}
	return h, nil
}

// bytes is h laid out as dStage_startStage_c (d_stage.h:949-952).
func (h stageHop) bytes() [addr.StageSize]byte {
	var b [addr.StageSize]byte
	copy(b[addr.StageOffName:addr.StageNameLen-1], h.Name)
	binary.BigEndian.PutUint16(b[addr.StageOffPoint:], uint16(h.Point))
	b[addr.StageOffRoomNo] = byte(h.Room)
	b[addr.StageOffLayer] = byte(h.Layer)
	return b
}

func (h stageHop) warning() string {
	return fmt.Sprintf(`WARNING: stage-hop WRITES GAME RAM. It writes mNextStage (0x%08X-0x%08X) to ask for
stage %q point %d room %d layer %d. It is a raw write, so it skips what
dComIfGp_setNextStage also does (Link's last-scene speed and mode, and the restart point,
d_com_inf_game.cpp:671-690): a death or a restart after the hop can put Link somewhere odd.
A stage, point or room that does not exist can hang or crash the game. Debug sessions only.
`, addr.NextStage, addr.NextStage+addr.NextStageSize, h.Name, h.Point, h.Room, h.Layer)
}

// applyStageHop writes h into mNextStage if the gates allow it: STABLE
// (which includes no stage change pending), IDLE and HEARTBEAT. The name,
// point, room, layer and wipe go first and mEnable last, so the game never
// sees an enabled request half written.
func applyStageHop(m mem.Mem, h stageHop, st gate.Status) error {
	if !st.CanWriteIdle() {
		return fmt.Errorf("not now: %s", st.Why())
	}
	// The gates read mEnable a tick ago; read it again just before the
	// write so a loading zone Link just walked into wins.
	if en, err := mem.ReadU8(m, addr.NextStageEnable); err != nil {
		return err
	} else if en != 0 {
		return errors.New("not now: the game just asked for a stage change itself")
	}
	b := h.bytes()
	if err := m.Write(addr.NextStage, b[:]); err != nil {
		return err
	}
	if err := mem.WriteU8(m, addr.NextStage+addr.NextOffWipe, stageHopWipe); err != nil {
		return err
	}
	return mem.WriteU8(m, addr.NextStageEnable, 1)
}

// stageHopRun checks the gates, sends the hop, then waits up to 10 s for
// the new stage to be loaded.
func stageHopRun(w io.Writer, m mem.Mem, h stageHop, tick func()) error {
	_, st, _, err := gateSnapshots(m, tick)
	if err != nil {
		return err
	}
	if err := applyStageHop(m, h, st); err != nil {
		return err
	}
	fmt.Fprintf(w, "mNextStage set: %q point %d room %d layer %d\n", h.Name, h.Point, h.Room, h.Layer)
	for range 100 {
		tick()
		s, err := snap.Read(m)
		if err != nil {
			return err
		}
		if s.StageName == h.Name && s.NextEnable == 0 {
			fmt.Fprintf(w, "now in %s\n", where(s))
			return nil
		}
	}
	return errors.New("the new stage did not load within 10 s; check the game with coop-probe or a screenshot")
}
