package main

// Batch C's probe tail (enemy-watch --kind bb; the P0-BB step of
// ww-multiplayer-handoff/design/enemy-mirror-batch-c.json test_plan): the
// file comment of enemy_kinds_probe.go says what it prints. The P0-MT
// tail (mtExtra) went with the Magtail row (R-20, after P0-MT failed).

import (
	"fmt"
	"strings"

	"github.com/StephenSHorton/ww-multiplayer/mods/enemysync"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mem"
)

// bbExecText is a Kargaroc's execute, *(*(actor+0xEC)+8), tail_control
// below it (exec - addr.BbExecToTail, rels/d_a_bb/symbols.txt:8,10,11,44),
// both functions' first four words, tail_control's blr word, and whether
// they are the batch's fingerprint (fp=ok: the REL calls tail_control;
// fp=miss: the tail collapses and fp_miss counts). "exec -" when the
// method table or the functions are not in MEM1.
func bbExecText(m mem.Reader, actor uint32) (string, error) {
	mtd, err := mem.ReadU32(m, actor+addr.ActorOffSubMtd)
	if err != nil {
		return "", err
	}
	if !addr.InMEM1(mtd) {
		return fmt.Sprintf("exec - (sub_method 0x%08X) fp=miss", mtd), nil
	}
	exec, err := mem.ReadU32(m, mtd+addr.MethodOffExecute)
	if err != nil {
		return "", err
	}
	tc := exec - addr.BbExecToTail
	if !addr.InMEM1(exec) || !addr.InMEM1(tc) || !addr.InMEM1(tc+addr.BbTailBlrOff) {
		return fmt.Sprintf("exec 0x%08X tc 0x%08X fp=miss", exec, tc), nil
	}
	words := func(at uint32) ([4]uint32, error) {
		var w [4]uint32
		for i := range w {
			v, err := mem.ReadU32(m, at+uint32(4*i))
			if err != nil {
				return w, err
			}
			w[i] = v
		}
		return w, nil
	}
	ew, err := words(exec)
	if err != nil {
		return "", err
	}
	tw, err := words(tc)
	if err != nil {
		return "", err
	}
	blr, err := mem.ReadU32(m, tc+addr.BbTailBlrOff)
	if err != nil {
		return "", err
	}
	fp := "miss"
	if ew == addr.BbFpExec && tw == addr.BbFpTail && blr == addr.BbFpBlr {
		fp = "ok"
	}
	return fmt.Sprintf("exec 0x%08X [%08X %08X %08X %08X] tc 0x%08X [%08X %08X %08X %08X] blr %08X fp=%s",
		exec, ew[0], ew[1], ew[2], ew[3], tc, tw[0], tw[1], tw[2], tw[3], blr, fp), nil
}

// colliderText is every collider of s, as allowExtra prints them.
func colliderText(b *strings.Builder, s *enemysync.KindSample) {
	for _, c := range s.St.Colliders {
		fmt.Fprintf(b, " | %s+0x%X at/tg/co 0x%X/0x%X/0x%X c %.1f %.1f %.1f r %.1f", c.Name, c.Off, c.AtSPrm, c.TgSPrm, c.CoSPrm, c.Center[0], c.Center[1], c.Center[2], c.R)
	}
}

// readVec reads a cXyz at at.
func readVec(m mem.Reader, at uint32) ([3]float32, error) {
	var v [3]float32
	b, err := m.Read(at, 12)
	if err != nil {
		return v, err
	}
	if len(b) != 12 {
		return v, fmt.Errorf("0x%08X: short read", at)
	}
	for i := range v {
		v[i] = mem.F32BE(b[4*i:])
	}
	return v, nil
}

// bbExtra is a Kargaroc's per-tick tail (P0-BB): the morf's last frame,
// current.angle, the class bytes, the would-be live gate from this
// tick's bytes, the tail root unk_BD4[1] beside unk_AA8[0], and every
// sphere.
func bbExtra(m mem.Reader, e *enemysync.Enemy, s *enemysync.KindSample) (string, error) {
	st, bb := &s.St, &s.Bb
	var b strings.Builder
	fmt.Fprintf(&b, "end %d cur %d/%d/%d status 0x%08X freeze %d type %d state %d m2df %d m2f2 0x%02X dead %d knock %.2f attack %d inv %d m366 %d m368 %d eye_btp %d",
		s.MorfEnd, st.CurAngle[0], st.CurAngle[1], st.CurAngle[2], s.Status, s.IceFreeze, bb.Type, bb.State, bb.Rider, bb.DrawGate, bb.Dead, bb.Knock,
		bb.AttackTimer, bb.Invuln, bb.M366, bb.M368, bb.EyeBtp)
	g := e.Live
	g.Mode, g.BbRider, g.BbKnock = bb.Type, bb.Rider, bb.Knock
	root, err := readVec(m, e.Addr+addr.BbOffTailRoot1)
	if err != nil {
		return "", err
	}
	tail0, err := readVec(m, e.Addr+addr.BbOffTailPos)
	if err != nil {
		return "", err
	}
	fmt.Fprintf(&b, " live %s root %.1f %.1f %.1f tail0 %.1f %.1f %.1f", okText(enemysync.BbLiveRefusal(g)),
		root[0], root[1], root[2], tail0[0], tail0[1], tail0[2])
	colliderText(&b, s)
	return b.String(), nil
}
