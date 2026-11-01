package main

// face <x> <z> [tol=0x200] [mag=100]: turn Link toward world (x, z) through
// his own input, for aimed tests (wind#41: a fire arrow at a torch, a bomb
// at a wall, a light arrow at a mirror).
//
// Poking Link's angle does not stick: the game re-derives current.angle,
// shape_angle, old.angle and the copies at +0x34DE and +0x3546 every frame
// (wind#41, measured on the Mac rig). His own path does: while the stick is
// held, daPy_lk_c turns him toward m34E8 (+0x34E8), the stick angle m34DC
// (+0x34DC, cM_atan2s(-x, y): up 0x0000, left 0x4000, down 0x8000, right
// 0xC000) plus the camera's yaw (d_a_player_main.cpp:10455-10490). The
// camera follows him, so face holds a light stick at target - yaw, reads
// the yaw back (m34E8 - m34DC) every step and re-aims, and lets go once
// shape_angle.y (+0x20E) is within tol of the target. A light stick
// (mag/127) turns him with a step or two at most; the distance moved is
// printed.

import (
	"errors"
	"fmt"
	"math"
	"os"
	"strconv"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/mods/coop/diag"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mem"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/player"
)

const faceUsage = "face <x> <z> [tol=512] [mag=100] [--stay]   (turn Link toward world (x, z) by stick input, then put him back where he stood unless --stay; tol in s16 angle units, 0x10000 = a full turn)"

// Link's fields face reads (daPy_lk_c, d_a_player_main.h; fopAc_ac_c).
const (
	faceOffShapeY = 0x20E  // s16 shape_angle.y
	faceOffStick  = 0x34DC // s16 m34DC, the stick's angle
	faceOffMove   = 0x34E8 // s16 m34E8, the stick's world angle
)

// yawTo is the s16 angle from (x, z) toward (tx, tz), as the game measures
// an actor's facing (cM_atan2s(dx, dz)).
func yawTo(x, z, tx, tz float64) int16 {
	a := math.Atan2(tx-x, tz-z) * 32768 / math.Pi
	return int16(int32(math.Round(a)))
}

// stickFor is the stick (x, y) whose angle, cM_atan2s(-x, y), is a, at
// magnitude mag.
func stickFor(a int16, mag float64) (int8, int8) {
	r := float64(a) * math.Pi / 32768
	return clampS8(-math.Sin(r) * mag), clampS8(math.Cos(r) * mag)
}

func clampS8(v float64) int8 {
	return int8(math.Max(-127, math.Min(127, math.Round(v))))
}

// angleDiff is a - b wrapped to the s16 range.
func angleDiff(a, b int16) int16 { return a - b }

func abs16(v int16) int32 {
	if v < 0 {
		return -int32(v)
	}
	return int32(v)
}

// faceCoarseMag is the light stick of the coarse turn (out of 127).
const faceCoarseMag = 40

// faceOvershoot is how far past the target face aims the stick (s16, ~11°).
const faceOvershoot = 0x0800

// faceEnv is what face reads and writes besides game memory; tests swap it.
type faceEnv struct {
	press   func(p player.Pad) error
	release func() error
	sleep   func(time.Duration)
	steps   int
	// back, when set, puts Link back at his start position afterwards
	// (the mod's warp writes his position only, so the facing stays).
	back func(p [3]float32) error
}

// faceLink turns Link at link toward (tx, tz) and reports where he ended.
func faceLink(m mem.Reader, link uint32, tx, tz float64, tol int16, mag float64, env faceEnv) (string, error) {
	pos := func() (float64, float64, error) {
		x, err := mem.ReadF32(m, link+addr.ActorOffCurPos)
		if err != nil {
			return 0, 0, err
		}
		z, err := mem.ReadF32(m, link+addr.ActorOffCurPos+8)
		return float64(x), float64(z), err
	}
	s16 := func(off uint32) (int16, error) {
		v, err := mem.ReadU16(m, link+off)
		return int16(v), err
	}
	x0, z0, err := pos()
	if err != nil {
		return "", err
	}
	y0, err := mem.ReadF32(m, link+addr.ActorOffCurPos+4)
	if err != nil {
		return "", err
	}
	defer func() { _ = env.release() }()
	cam := int16(0)
	known := false
	for i := 0; i < env.steps; i++ {
		x, z, err := pos()
		if err != nil {
			return "", err
		}
		want := yawTo(x, z, tx, tz)
		shape, err := s16(faceOffShapeY)
		if err != nil {
			return "", err
		}
		if known && abs16(angleDiff(want, shape)) <= int32(tol) {
			_ = env.release()
			moved := math.Hypot(x-x0, z-z0)
			line := fmt.Sprintf("faced (%.1f, %.1f): shape 0x%04X, target 0x%04X, off %d after %d step(s), moved %.1f",
				tx, tz, uint16(shape), uint16(want), angleDiff(want, shape), i, moved)
			if env.back != nil && moved > 1 {
				env.sleep(100 * time.Millisecond)
				if err := env.back([3]float32{float32(x0), y0, float32(z0)}); err != nil {
					return line, fmt.Errorf("put back: %w", err)
				}
				// The new spot changes the bearing a little: report it.
				x, z, err = pos()
				if err == nil {
					shape, _ = s16(faceOffShapeY)
					want = yawTo(x, z, tx, tz)
					line += fmt.Sprintf("; put back at (%.1f, %.1f): off %d", x, z, angleDiff(want, shape))
				}
			}
			return line, nil
		}
		// Aim past the target, on the far side of the turn: the pad's dead
		// zone snaps a small stick component to 0, so the stick's own angle
		// can miss the target by up to ~13°; aimed past it, Link sweeps
		// through the target and is let go there. Close in, one-frame taps.
		delta := angleDiff(want, shape)
		over := int16(faceOvershoot)
		if delta < 0 {
			over = -over
		}
		// Far off, a light stick turns him in place (wind#41: stick 40,
		// moved 0); close in, mag clears the dead zone for the last degrees.
		m := mag
		if abs16(delta) > 0x1000 {
			m = faceCoarseMag
		}
		sx, sy := stickFor(want+over-cam, m)
		if err := env.press(player.Pad{StickX: sx, StickY: sy}); err != nil {
			return "", err
		}
		hold := 35 * time.Millisecond
		if abs16(delta) > 0x1000 {
			hold = 80 * time.Millisecond
		}
		env.sleep(hold)
		stick, err := s16(faceOffStick)
		if err != nil {
			return "", err
		}
		move, err := s16(faceOffMove)
		if err != nil {
			return "", err
		}
		cam, known = move-stick, true
		if os.Getenv("WW_FACE_TRACE") != "" {
			fmt.Printf("step %d: want 0x%04X shape 0x%04X stick (%d,%d) m34DC 0x%04X m34E8 0x%04X cam 0x%04X\n",
				i, uint16(want), uint16(shape), sx, sy, uint16(stick), uint16(move), uint16(cam))
		}
		_ = env.release()
		env.sleep(30 * time.Millisecond)
	}
	return "", fmt.Errorf("not facing (%.1f, %.1f) within %d after %d steps", tx, tz, tol, env.steps)
}

func cmdFace(cmd string, args []string) {
	pos, flags, err := diag.SplitFlags(args, "stay")
	if err != nil || len(pos) < 2 || len(pos) > 4 {
		exitUsage(errors.New("face takes x z [tol] [mag]"), faceUsage)
	}
	tx, err1 := strconv.ParseFloat(pos[0], 64)
	tz, err2 := strconv.ParseFloat(pos[1], 64)
	if err1 != nil || err2 != nil {
		exitUsage(errors.New("x and z are numbers"), faceUsage)
	}
	tol, err := diagIntArg(pos, 2, 512, 16, 0x4000, "tol")
	if err != nil {
		exitUsage(err, faceUsage)
	}
	mag, err := diagIntArg(pos, 3, 100, 10, 127, "mag")
	if err != nil {
		exitUsage(err, faceUsage)
	}
	err = diag.WithMem(openDolphin, func(m mem.Mem) error {
		link, err := player.NewLink(m).Ptr()
		if err != nil {
			return err
		}
		env := faceEnv{
			press:   func(p player.Pad) error { return player.Override(m, p) },
			release: func() error { return player.Release(m) },
			sleep:   time.Sleep,
			steps:   60,
		}
		if !flags["stay"] {
			env.back = func(p [3]float32) error { return player.Warp(m, p, 2*time.Second) }
		}
		line, err := faceLink(m, link, tx, tz, int16(tol), float64(mag), env)
		if err != nil {
			return err
		}
		fmt.Println(line)
		return nil
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s: %v\n", cmd, err)
		os.Exit(1)
	}
}
