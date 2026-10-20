package main

// enemy-watch: step 1 (L1) of the host-authoritative Moblin prototype
// (ww-multiplayer-handoff/design/host-authoritative-prototype.md), a
// read-only probe of ONE Dolphin that confirms the daMo2_c offsets the
// mirror rests on against visible behaviour: it walks the actors with a
// private MO2 layout, keeps the keyed mode-0 Moblins of the room Link is
// in, and prints one compact line per Moblin every 16 ms (and a CSV with
// --csv): position, angles, health, the damagereaction mode and action,
// the attack type and phase, the morf's attribute, rate and frame, the
// pause byte, the Co and Tg cylinder centres (and the Co centre's offset
// from pos, which the design says is 35 units forward), the head sphere's
// centre against m28C8, the enemy_ice shrink timer, the invulnerability
// word and the tilt. Nothing is written.
//
// It is also the S2 pre-build probe (moblin-s2-v2 design,
// tests_and_live_checks[7]): every Moblin is sampled a second time
// through enemysync.ReadSample and the host's Tracker (the Mo2 resource
// table resolved at every walk, the lookup-miss re-read, the AnmTrack
// restart detector), so the bck, attr, anm_seq, morf, flags2 and blur
// columns are exactly what the host would stream. The table's state is
// printed at start and whenever it changes, its Stats at the end.

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"os/signal"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/mods/coop/diag"
	"github.com/StephenSHorton/ww-multiplayer/mods/enemysync"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/actors"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mailbox"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mem"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/stage"
)

const enemyWatchUsage = "enemy-watch [secs=10, 0 = until Ctrl+C] [--csv <path>] [--kind mo2|bk|cc]"

// mo2Layouts is the private walk layout: a Moblin's enemyice and its u8
// mMode (actors.Actor.Mode is then mo2 mMode; Actor.Ice.Mode is
// enemy_ice's own mode, which the actors probe prints as "mode").
// combat.Layouts() is shared and untouched.
var mo2Layouts = actors.Layouts{int16(stage.ProcMO2): {Ice: addr.Mo2OffIce, Mode: addr.Mo2OffMode, ModeU8: true}}

// The probe's timing.
const (
	enemyWatchTick = 16 * time.Millisecond
	enemyWalkEvery = 500 * time.Millisecond
	// mo2ReadLen is what one read of a Moblin covers: through the enemyice.
	mo2ReadLen = addr.Mo2OffIce + addr.EnemyIceSize
)

// splitValueFlags separates "--name value" / "--name=value" flags (in
// valued) and bare "--flag" flags (in allowed) from positional
// arguments.
func splitValueFlags(args []string, valued []string, allowed ...string) (pos []string, flags map[string]bool, values map[string]string, err error) {
	flags, values = map[string]bool{}, map[string]string{}
	for i := 0; i < len(args); i++ {
		a := args[i]
		name, ok := strings.CutPrefix(a, "--")
		if !ok {
			pos = append(pos, a)
			continue
		}
		if k, v, has := strings.Cut(name, "="); has && slices.Contains(valued, k) {
			values[k] = v
			continue
		}
		if slices.Contains(valued, name) {
			if i+1 >= len(args) {
				return nil, nil, nil, fmt.Errorf("%s needs a value", a)
			}
			values[name] = args[i+1]
			i++
			continue
		}
		if !slices.Contains(allowed, name) {
			return nil, nil, nil, fmt.Errorf("unknown flag %s", a)
		}
		flags[name] = true
	}
	return pos, flags, values, nil
}

// runEnemyWatch is the enemy-watch command.
func runEnemyWatch(cmd string, args []string) {
	pos, _, values, err := splitValueFlags(args, []string{"csv", "kind"})
	if err != nil {
		exitUsage(err, enemyWatchUsage)
	}
	secs, err := diagIntArg(pos, 0, 10, 0, 86400, "secs")
	if err != nil {
		exitUsage(err, enemyWatchUsage)
	}
	// --kind bk|cc is the generalised mirror's L0 probe
	// (enemy_kinds_probe.go); mo2, the default, is this probe unchanged.
	if name := values["kind"]; name != "" && name != "mo2" {
		k, ok := enemysync.KindByName(name)
		if !ok {
			exitUsage(fmt.Errorf("unknown --kind %q", name), enemyWatchUsage)
		}
		if values["csv"] != "" {
			exitUsage(errors.New("--csv is the Moblin probe's only"), enemyWatchUsage)
		}
		runEnemyKindWatch(cmd, secs, k.ID)
		return
	}
	var csv io.Writer
	if path := values["csv"]; path != "" {
		f, err := os.Create(path)
		if err != nil {
			exitUsage(err, enemyWatchUsage)
		}
		defer f.Close()
		csv = f
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	err = diag.WithMem(openDolphin, func(m mem.Mem) error {
		return enemyWatch(ctx, os.Stdout, csv, m, time.Duration(secs)*time.Second)
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s: %v\n", cmd, err)
		os.Exit(1)
	}
}

// mo2Sample is one Moblin as a tick read it.
type mo2Sample struct {
	Addr, PID uint32
	SetID     uint16
	HomeRoom  int8
	Pos       [3]float32
	AngleY    int16
	ShapeY    int16
	Health    int8
	DRMode    int16
	DRAction  int16
	AttackT   int32
	AttackP   int32
	Morf      uint32
	MorfOK    bool
	MorfAttr  uint8
	MorfRate  float32
	MorfFrame float32
	Pause     uint8
	CoCenter  [3]float32
	TgCenter  [3]float32
	HeadC     [3]float32
	M28C8     [3]float32
	IceTimer  int8
	IceMode   int8 // enemy_ice's mMode (what the actors probe's "mode" column shows), not mo2 mMode
	Mo2Mode   uint8
	Invuln    int16
	Tilt      int16
}

// readMo2 reads a's Moblin fields in one read of mo2ReadLen bytes (and one
// of the morf's frame control), checking the actor is still a.
func readMo2(m mem.Reader, a *actors.Actor) (mo2Sample, error) {
	b, err := m.Read(a.Addr, mo2ReadLen)
	if err != nil {
		return mo2Sample{}, err
	}
	if len(b) != mo2ReadLen {
		return mo2Sample{}, fmt.Errorf("actor 0x%08X: %d bytes read, want %d", a.Addr, len(b), mo2ReadLen)
	}
	be := binary.BigEndian
	s := mo2Sample{Addr: a.Addr, PID: be.Uint32(b[addr.ActorOffBsPcID:]), SetID: be.Uint16(b[addr.ActorOffSetID:]), HomeRoom: int8(b[addr.ActorOffHomeRoomNo])}
	if s.PID != a.PID || int16(be.Uint16(b[addr.ActorOffProcName:])) != a.Proc || int8(b[addr.ActorOffInitState]) != 2 || s.SetID != a.SetID {
		return s, fmt.Errorf("%w: %s changed under the probe", actors.ErrChanged, a)
	}
	vec := func(off int) (v [3]float32) {
		for i := range 3 {
			v[i] = math.Float32frombits(be.Uint32(b[off+4*i:]))
		}
		return v
	}
	s.Pos = vec(addr.ActorOffCurPos)
	s.AngleY = int16(be.Uint16(b[addr.ActorOffCurAngle+2:]))
	s.ShapeY = int16(be.Uint16(b[addr.ActorOffShapeAngle+2:]))
	s.Health = int8(b[addr.ActorOffHealth])
	s.DRMode = int16(be.Uint16(b[addr.Mo2OffDRMode:]))
	s.DRAction = int16(be.Uint16(b[addr.Mo2OffDRAction:]))
	s.AttackT = int32(be.Uint32(b[addr.Mo2OffAttackType:]))
	s.AttackP = int32(be.Uint32(b[addr.Mo2OffAttackPhase:]))
	s.Morf = be.Uint32(b[addr.Mo2OffMorf:])
	s.Pause = b[addr.ActorOffPauseFlag]
	s.CoCenter = vec(addr.Mo2OffCoCyl + addr.Mo2CcCenterOff)
	s.TgCenter = vec(addr.Mo2OffTgCyl + addr.Mo2CcCenterOff)
	s.HeadC = vec(addr.Mo2OffHeadSph + addr.Mo2CcCenterOff)
	s.M28C8 = vec(addr.Mo2OffHeadCenter)
	s.IceTimer = int8(b[addr.Mo2OffIceShrink])
	s.IceMode = int8(b[addr.Mo2OffIce+addr.EnemyIceOffMode])
	s.Mo2Mode = b[addr.Mo2OffMode]
	s.Invuln = int16(be.Uint16(b[addr.Mo2OffInvuln:]))
	s.Tilt = int16(be.Uint16(b[addr.Mo2OffTilt:]))
	if at := s.Morf + addr.McaMorfOffFrameCtrl; mem.CheckRange(at, addr.McaMorfFrameCtrlLen) == nil {
		fc, err := m.Read(at, addr.McaMorfFrameCtrlLen)
		if err != nil {
			return s, err
		}
		if len(fc) == addr.McaMorfFrameCtrlLen {
			s.MorfOK = true
			s.MorfAttr = fc[addr.McaMorfOffAttr-addr.McaMorfOffFrameCtrl]
			s.MorfRate = math.Float32frombits(be.Uint32(fc[addr.McaMorfOffRate-addr.McaMorfOffFrameCtrl:]))
			s.MorfFrame = math.Float32frombits(be.Uint32(fc[addr.McaMorfOffFrame-addr.McaMorfOffFrameCtrl:]))
		}
	}
	return s, nil
}

// keyedMoblins returns the keyed, executing Moblins of room among w's
// actors whose mMode is 0 or 1 (a carried one, 5, and a statue, 0xF, are
// refused; addr.Mo2OffMode), sorted by setID.
func keyedMoblins(w *actors.Walk, room int8) []actors.Actor {
	var out []actors.Actor
	for i := range w.Actors {
		a := &w.Actors[i]
		if a.Proc == int16(stage.ProcMO2) && a.Keyed() && a.InitState == 2 && a.ModeOK && (a.Mode == 0 || a.Mode == 1) && a.HomeRoom == room {
			out = append(out, *a)
		}
	}
	slices.SortFunc(out, func(a, b actors.Actor) int { return int(a.SetID) - int(b.SetID) })
	return out
}

// enemyScope reads the stage name and mStayNo.
func enemyScope(m mem.Reader) (stageName string, room int8, err error) {
	stageName, err = mem.ReadString(m, addr.CurStageName, addr.StageNameLen)
	if err != nil {
		return "", 0, err
	}
	r, err := mem.ReadS8(m, addr.StayNo)
	return stageName, r, err
}

const enemyCSVHeader = "t_ms,addr,pid,setID,room,x,y,z,angle_y,shape_y,health,dr_mode,dr_action,attack_type,attack_phase,morf,morf_attr,morf_rate,morf_frame,pause,co_x,co_y,co_z,co_dx,co_dy,co_dz,tg_x,tg_y,tg_z,head_x,head_y,head_z,m28c8_x,m28c8_y,m28c8_z,ice_timer,invuln,tilt,mo2_mode,ice_mode,bck,anm_attr,anm_seq,anm_morf,flags2,blur_on,blur_shape,blur_scale"

// mo2Table resolves tr's Mo2 table from m as the host's walk does and
// prints its state the first time and whenever it differs from *why
// (which it then sets).
func mo2Table(w io.Writer, m mem.Reader, tr *enemysync.Tracker, why *string, first bool) error {
	if err := tr.Res.Resolve(m); err != nil {
		return err
	}
	now := tr.Res.Why()
	if !first && now == *why {
		return nil
	}
	*why = now
	if now != "" {
		fmt.Fprintf(w, "Mo2 table: NOT resolved: %s (every bck reads 0xFF)\n", now)
		return nil
	}
	entry, res := tr.Res.Entry()
	ptrs := tr.Res.Ptrs()
	fmt.Fprintf(w, "Mo2 table: resolved, mObjectInfo[%d] mRes 0x%08X (BCK 0x%02X..0x%02X at 0x%08X..0x%08X)\n",
		entry, res, addr.Mo2BckFirst, addr.Mo2BckLast, ptrs[0], ptrs[addr.Mo2BckCount-1])
	return nil
}

// enemyWatch runs the probe over m for d (0: until ctx ends) and prints
// the Mo2 table's Stats at the end.
func enemyWatch(ctx context.Context, w, csv io.Writer, m mem.Reader, d time.Duration) error {
	var tr enemysync.Tracker
	err := enemyWatchWith(ctx, w, csv, m, d, &tr)
	st := tr.Res.Stats
	fmt.Fprintf(w, "Mo2 table stats: reads %d bck_miss %d miss_reads %d\n", st.Reads, st.BckMiss, st.MissReads)
	return err
}

// enemyWatchWith is enemyWatch over tr.
func enemyWatchWith(ctx context.Context, w, csv io.Writer, m mem.Reader, d time.Duration, tr *enemysync.Tracker) error {
	stageName, room, err := enemyScope(m)
	if err != nil {
		return err
	}
	rel, err := mailbox.ReadREL(m)
	if err != nil {
		return err
	}
	fmt.Fprintf(w, "stage %q room %d; REL words at 0x%08X: magic 0x%08X linked=%v build 0x%08X frames %d\n",
		stageName, room, mailbox.Mailbox+mailbox.OffRELMagic, rel.Magic, rel.Linked, rel.BuildID, rel.Frames)
	fmt.Fprintf(w, "layout: MO2 proc 0x%04X mode +0x%X morf +0x%X dr +0x%X/+0x%X attack +0x%X/+0x%X co/tg/head +0x%X/+0x%X/+0x%X centre +0x%X ice +0x%X\n",
		uint16(stage.ProcMO2), addr.Mo2OffMode, addr.Mo2OffMorf, addr.Mo2OffDRMode, addr.Mo2OffDRAction, addr.Mo2OffAttackType, addr.Mo2OffAttackPhase,
		addr.Mo2OffCoCyl, addr.Mo2OffTgCyl, addr.Mo2OffHeadSph, addr.Mo2CcCenterOff, addr.Mo2OffIceShrink)
	if csv != nil {
		fmt.Fprintln(csv, enemyCSVHeader)
	}
	start := time.Now()
	var deadline time.Time
	if d > 0 {
		deadline = start.Add(d)
	}
	var moblins []actors.Actor
	var nextWalk time.Time
	var resWhy string
	resFirst := true
	walk := func(now time.Time) error {
		nextWalk = now.Add(enemyWalkEvery)
		stageName, room, err = enemyScope(m)
		if err != nil {
			return err
		}
		aw, err := actors.ReadWith(m, 0, mo2Layouts)
		if err != nil {
			return err
		}
		was := make([]uint32, len(moblins))
		for i := range moblins {
			was[i] = moblins[i].PID
		}
		moblins = keyedMoblins(&aw, room)
		now2 := make([]uint32, len(moblins))
		for i := range moblins {
			now2[i] = moblins[i].PID
		}
		tr.Keep(now2)
		if err := mo2Table(w, m, tr, &resWhy, resFirst); err != nil {
			return err
		}
		resFirst = false
		if !slices.Equal(was, now2) {
			fmt.Fprintf(w, "[%6.3fs] walk: %d actors (%s), %d keyed mode-0/1 Moblins in room %d of %q:", now.Sub(start).Seconds(), len(aw.Actors), walkNote(&aw), len(moblins), room, stageName)
			for i := range moblins {
				a := &moblins[i]
				fmt.Fprintf(w, " [setID %d pid %d 0x%08X mode %d]", a.SetID, a.PID, a.Addr, a.Mode)
			}
			fmt.Fprintln(w)
		}
		return nil
	}
	if err := walk(start); err != nil {
		return err
	}
	t := time.NewTicker(enemyWatchTick)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case now := <-t.C:
			if !deadline.IsZero() && now.After(deadline) {
				return nil
			}
			if now.After(nextWalk) {
				if err := walk(now); err != nil {
					return err
				}
			}
			ms := now.Sub(start).Milliseconds()
			rewalk := false
			for i := range moblins {
				s, err := readMo2(m, &moblins[i])
				if err != nil {
					fmt.Fprintf(w, "[%6.3fs] %v\n", now.Sub(start).Seconds(), err)
					rewalk = true
					continue
				}
				// The host's sample of the same Moblin, derived as the
				// host derives it.
				a := &moblins[i]
				hs, err := enemysync.ReadSample(m, &enemysync.Moblin{Addr: a.Addr, PID: a.PID, SetID: a.SetID, HomeRoom: a.HomeRoom, Mode: uint8(a.Mode)})
				if err == nil {
					err = tr.Derive(m, a.PID, &hs, now)
				}
				var host *enemysync.Sample
				if err != nil {
					fmt.Fprintf(w, "[%6.3fs] host sample: %v\n", now.Sub(start).Seconds(), err)
					if errors.Is(err, actors.ErrChanged) {
						rewalk = true
					}
				} else {
					host = &hs
				}
				printMo2(w, ms, &s, host)
				if csv != nil {
					csvMo2(csv, ms, room, &s, host)
				}
			}
			if rewalk {
				if err := walk(now); err != nil {
					return err
				}
			}
		}
	}
}

// walkNote says whether the walk was whole.
func walkNote(aw *actors.Walk) string {
	if aw.Complete() {
		return "complete"
	}
	if aw.Stop != "" {
		return "stopped: " + aw.Stop
	}
	return strings.Join(aw.Notes, "; ")
}

// printMo2 prints one Moblin's tick on one line; h is the host's sample
// of it (nil when ReadSample or Derive failed).
func printMo2(w io.Writer, ms int64, s *mo2Sample, h *enemysync.Sample) {
	morf := "morf -"
	if s.MorfOK {
		morf = fmt.Sprintf("morf 0x%08X attr %d rate %.2f frame %.2f", s.Morf, s.MorfAttr, s.MorfRate, s.MorfFrame)
	} else if s.Morf != 0 {
		morf = fmt.Sprintf("morf 0x%08X (outside MEM1)", s.Morf)
	}
	fmt.Fprintf(w, "%7d setID %d pid %d 0x%08X r%d mo2_mode %d pos %.1f %.1f %.1f ang %d shape %d hp %d dr %d/%d atk %d/%d %s pause 0x%02X co %.1f %.1f %.1f (d %.1f %.1f %.1f) tg %.1f %.1f %.1f head %.1f %.1f %.1f m28C8 %.1f %.1f %.1f ice %d ice_mode %d inv %d tilt %d %s\n",
		ms, s.SetID, s.PID, s.Addr, s.HomeRoom, s.Mo2Mode, s.Pos[0], s.Pos[1], s.Pos[2], s.AngleY, s.ShapeY, s.Health, s.DRMode, s.DRAction, s.AttackT, s.AttackP, morf, s.Pause,
		s.CoCenter[0], s.CoCenter[1], s.CoCenter[2], s.CoCenter[0]-s.Pos[0], s.CoCenter[1]-s.Pos[1], s.CoCenter[2]-s.Pos[2],
		s.TgCenter[0], s.TgCenter[1], s.TgCenter[2], s.HeadC[0], s.HeadC[1], s.HeadC[2], s.M28C8[0], s.M28C8[1], s.M28C8[2],
		s.IceTimer, s.IceMode, s.Invuln, s.Tilt, anmText(h))
}

// anmText is the host-derived columns of h: the BCK index, attribute,
// restart counter, setAnm morf, flags2 and the sword blur (on/shape and
// scale).
func anmText(h *enemysync.Sample) string {
	if h == nil {
		return "bck - anm_attr - anm_seq - anm_morf - flags2 - blur -"
	}
	return fmt.Sprintf("bck 0x%02X anm_attr %d anm_seq %d anm_morf %d flags2 0x%02X blur %d/%d scale %.2f",
		h.Bck, h.AnmAttr, h.AnmSeq, h.Morf, h.Flags2, blurOn(h), h.BlurShape, h.BlurScale)
}

// blurOn is 1 when h's SlotF2BlurOn is set.
func blurOn(h *enemysync.Sample) int {
	if h.Flags2&enemysync.SlotF2BlurOn != 0 {
		return 1
	}
	return 0
}

// csvMo2 writes one Moblin's tick as a CSV row (enemyCSVHeader); the
// host-derived columns are empty when h is nil.
func csvMo2(w io.Writer, ms int64, room int8, s *mo2Sample, h *enemysync.Sample) {
	f := func(v float32) string { return strconv.FormatFloat(float64(v), 'f', 3, 32) }
	cols := []string{
		strconv.FormatInt(ms, 10), fmt.Sprintf("0x%08X", s.Addr), strconv.FormatUint(uint64(s.PID), 10), strconv.Itoa(int(s.SetID)), strconv.Itoa(int(room)),
		f(s.Pos[0]), f(s.Pos[1]), f(s.Pos[2]), strconv.Itoa(int(s.AngleY)), strconv.Itoa(int(s.ShapeY)), strconv.Itoa(int(s.Health)),
		strconv.Itoa(int(s.DRMode)), strconv.Itoa(int(s.DRAction)), strconv.Itoa(int(s.AttackT)), strconv.Itoa(int(s.AttackP)),
		fmt.Sprintf("0x%08X", s.Morf), strconv.Itoa(int(s.MorfAttr)), f(s.MorfRate), f(s.MorfFrame), strconv.Itoa(int(s.Pause)),
		f(s.CoCenter[0]), f(s.CoCenter[1]), f(s.CoCenter[2]), f(s.CoCenter[0] - s.Pos[0]), f(s.CoCenter[1] - s.Pos[1]), f(s.CoCenter[2] - s.Pos[2]),
		f(s.TgCenter[0]), f(s.TgCenter[1]), f(s.TgCenter[2]), f(s.HeadC[0]), f(s.HeadC[1]), f(s.HeadC[2]), f(s.M28C8[0]), f(s.M28C8[1]), f(s.M28C8[2]),
		strconv.Itoa(int(s.IceTimer)), strconv.Itoa(int(s.Invuln)), strconv.Itoa(int(s.Tilt)), strconv.Itoa(int(s.Mo2Mode)), strconv.Itoa(int(s.IceMode)),
	}
	if h != nil {
		cols = append(cols, fmt.Sprintf("0x%02X", h.Bck), strconv.Itoa(int(h.AnmAttr)), strconv.Itoa(int(h.AnmSeq)), strconv.Itoa(int(h.Morf)),
			fmt.Sprintf("0x%02X", h.Flags2), strconv.Itoa(blurOn(h)), strconv.Itoa(int(h.BlurShape)), f(h.BlurScale))
	} else {
		cols = append(cols, "", "", "", "", "", "", "", "")
	}
	fmt.Fprintln(w, strings.Join(cols, ","))
}
