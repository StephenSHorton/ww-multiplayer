package main

// rf-ask <pid> [wait-ms=2000]: one actor_refresh request by hand, for the
// single-Dolphin re-create checks (docs/TESTING.md RC-3..RC-9). It names
// the actor as the relay would (replica.RefreshAskOf: proc, tag, pid,
// params, home room, bit kind and bit), writes the request in the relay's
// order (+0x04..+0x14, then the seq), waits for the REL's ack and prints
// the result, the gate, the proc state and the new pid. For a table row
// (mailbox.RefreshRows) it also peeks the row's state field before and
// after, on the old pid and on the new one, and names each value idle,
// done or busy against the row, so a wrong offset or width in refresh.c's
// rf_rows shows up as a mismatch (RC-8).
//
// It is for one Dolphin with no --coop-refresh relay running: a request
// still pending (seq != ack) is refused, since a second writer would make
// the relay misread its acks. It does not set the bit: set it first (the
// RC recipe's poke) and read it back.

import (
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"strconv"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/mods/coop/diag"
	"github.com/StephenSHorton/ww-multiplayer/mods/coop/replica"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/actors"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mailbox"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mem"
)

const rfAskUsage = "rf-ask <pid> [wait-ms=2000]   (one Dolphin, no --coop-refresh relay running; set the bit first)"

// rfAskEnv is what rfAsk reads besides m; tests replace it.
type rfAskEnv struct {
	find  func(m mem.Reader, pid uint32) (actors.Actor, bool, error) // the actor, re-read
	wait  time.Duration
	sleep func(time.Duration) // between ack polls
}

// rfFind walks the queue for pid and re-reads it as the relay does.
func rfFind(m mem.Reader, pid uint32) (actors.Actor, bool, error) {
	aw, err := actors.Read(m, actors.MaxNodes)
	if err != nil {
		return actors.Actor{}, false, err
	}
	a, ok := aw.ByPID(pid)
	if !ok {
		return actors.Actor{}, false, nil
	}
	typ, err := mem.ReadU32(m, addr.FopAcType)
	if err != nil {
		return actors.Actor{}, false, err
	}
	b, err := actors.RereadParams(m, a, typ, nil)
	if err != nil {
		return actors.Actor{}, false, err
	}
	return b, true, nil
}

// rfStateLine peeks row's state field on the actor at a.
func rfStateLine(m mem.Reader, row *mailbox.RefreshRow, a uint32) (string, error) {
	at := a + uint32(row.StOff)
	var v uint32
	var err error
	switch row.StW {
	case 1:
		var b uint8
		b, err = mem.ReadU8(m, at)
		v = uint32(b)
	case 2:
		var h uint16
		h, err = mem.ReadU16(m, at)
		v = uint32(h)
	default:
		v, err = mem.ReadU32(m, at)
	}
	if err != nil {
		return "", err
	}
	s := fmt.Sprintf("state +0x%03X u%d = %d (%s)", row.StOff, 8*int(row.StW), v, row.StateClass(v))
	if row.NeedOff != 0 {
		var n uint32
		if row.Flags&mailbox.RefreshRowNeed32 != 0 {
			n, err = mem.ReadU32(m, a+uint32(row.NeedOff))
		} else {
			var b uint8
			b, err = mem.ReadU8(m, a+uint32(row.NeedOff))
			n = uint32(b)
		}
		if err != nil {
			return "", err
		}
		s += fmt.Sprintf(", need +0x%03X = 0x%X", row.NeedOff, n)
	}
	return s, nil
}

// rfAsk sends one request for pid and reports the REL's answer.
func rfAsk(w io.Writer, m mem.Mem, pid uint32, env rfAskEnv) error {
	a, ok, err := env.find(m, pid)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("no actor of pid %d in the queue", pid)
	}
	proc := uint16(a.Proc)
	ask, ok, err := replica.RefreshAskOf(m, &a)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("pid %d (%s, params 0x%08X, room %d) is no refresh candidate: off the allow-list, a pending door row, a kind the REL refuses, or no switch",
			pid, mailbox.RefreshProcName(proc), a.Params, a.HomeRoom)
	}
	verb := "delete"
	if ask.Recreate {
		verb = "re-create"
	}
	fmt.Fprintf(w, "pid %d %s 0x%08X params 0x%08X room %d pos (%.1f %.1f %.1f): %s, bit kind %d bit %d (0x%02X), create room %d\n",
		pid, mailbox.RefreshProcName(proc), a.Addr, a.Params, a.HomeRoom, a.Pos[0], a.Pos[1], a.Pos[2], verb, ask.BitKind, ask.BitNo, ask.BitNo, ask.Room)
	row := mailbox.RefreshRowOf(proc)
	if row != nil {
		s, err := rfStateLine(m, row, a.Addr)
		if err != nil {
			return err
		}
		fmt.Fprintf(w, "before: %s\n", s)
	}
	st, err := mailbox.ReadRefresh(m)
	if err != nil {
		return err
	}
	if st.Seq != st.Ack {
		return fmt.Errorf("a request is still pending (seq %d, ack %d): is a --coop-refresh relay running here? rf-ask is for one Dolphin with none", st.Seq, st.Ack)
	}
	seq := max(st.Seq, st.Ack) + 1
	if seq == 0 {
		seq = 1
	}
	req := mailbox.RefreshRequest{Seq: seq, Proc: proc, PID: a.PID, Params: a.Params, Room: a.HomeRoom, BitKind: ask.BitKind, BitNo: ask.BitNo}
	body, seqBytes := req.Fields()
	if err := m.Write(mailbox.RefreshAddr+mailbox.RefreshProcOff, body); err != nil {
		return err
	}
	if err := m.Write(mailbox.RefreshAddr+mailbox.RefreshSeqOff, seqBytes); err != nil {
		return err
	}
	deadline := env.wait
	for spent := time.Duration(0); ; spent += 16 * time.Millisecond {
		if st, err = mailbox.ReadRefresh(m); err != nil {
			return err
		}
		if st.Ack == seq {
			break
		}
		if spent >= deadline {
			return fmt.Errorf("seq %d not acked after %s: no REL, a build without actor_refresh, or the game is paused", seq, deadline)
		}
		env.sleep(16 * time.Millisecond)
	}
	line := fmt.Sprintf("seq %d: %s (result %d), state 0x%04X", seq, mailbox.RefreshResultName(st.Result), st.Result, st.ProcState)
	switch st.Result {
	case mailbox.RefreshRGate:
		line += fmt.Sprintf(", %s (gate %d)", mailbox.RefreshGateName(st.Gate), st.Gate)
	case mailbox.RefreshRRecreated:
		line += fmt.Sprintf(", new pid %d", st.NewPID)
	case mailbox.RefreshRCreateFailed:
		line += ", gone until its room reloads"
	}
	fmt.Fprintln(w, line)
	if row == nil {
		return nil
	}
	// The old actor is deleted a frame later (fopAcM_delete queues it).
	if b, ok, err := env.find(m, pid); err != nil {
		return err
	} else if ok {
		s, err := rfStateLine(m, row, b.Addr)
		if err != nil {
			return err
		}
		fmt.Fprintf(w, "after, pid %d: %s\n", pid, s)
	} else {
		fmt.Fprintf(w, "after, pid %d: gone\n", pid)
	}
	if st.Result == mailbox.RefreshRRecreated && st.NewPID != math.MaxUint32 {
		if b, ok, err := env.find(m, st.NewPID); err != nil {
			return err
		} else if ok {
			s, err := rfStateLine(m, row, b.Addr)
			if err != nil {
				return err
			}
			fmt.Fprintf(w, "after, new pid %d: %s (it may still be creating: ask again for the new pid, IN_STATE expected)\n", st.NewPID, s)
		} else {
			fmt.Fprintf(w, "after, new pid %d: not in the queue yet (still creating)\n", st.NewPID)
		}
	}
	return nil
}

func runRfAsk(cmd string, args []string) {
	pos, _, err := diag.SplitFlags(args)
	if err != nil {
		exitUsage(err, rfAskUsage)
	}
	if len(pos) < 1 || len(pos) > 2 {
		exitUsage(errors.New("rf-ask takes a pid and an optional wait in ms"), rfAskUsage)
	}
	pid, err := strconv.ParseUint(pos[0], 0, 32)
	if err != nil {
		exitUsage(fmt.Errorf("pid: %w", err), rfAskUsage)
	}
	waitMS, err := diagIntArg(pos, 1, 2000, 1, 60000, "wait-ms")
	if err != nil {
		exitUsage(err, rfAskUsage)
	}
	env := rfAskEnv{find: rfFind, wait: time.Duration(waitMS) * time.Millisecond, sleep: time.Sleep}
	err = diag.WithMem(openDolphin, func(m mem.Mem) error { return rfAsk(os.Stdout, m, uint32(pid), env) })
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s: %v\n", cmd, err)
		os.Exit(1)
	}
}
