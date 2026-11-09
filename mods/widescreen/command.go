package widescreen

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/wwapi"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/emu"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/emu/host"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/emu/launch"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mailbox"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mem"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/ops"
)

const usage = "widescreen [auto|off|16:9|16:10|21:9|32:9|W:H|<aspect>] | widescreen native <aspect> [--flags N]"

// Env is what the command takes from package main: Open attaches to the
// Dolphin with this PID (0: the one WW_DOLPHIN_PID / WW_DOLPHIN_INDEX
// select), as co-op's Env does.
type Env struct {
	Open func(pid uint32) (*emu.Dolphin, error)
}

// ErrClaimed: a session holds the mailbox; the ops queue has one writer.
var ErrClaimed = errors.New("widescreen: a session is playing on this Dolphin (its mailbox is claimed); change the setting there, or stop the session first")

// Auto reads the factor Dolphin's graphics settings imply for the Dolphin
// with this PID: 1 / launch.GFXAspect.XScale (1 = nothing to widen).
func autoFactor(pid uint32) (factor float64, why string, err error) {
	for _, p := range launch.Processes() {
		if p.PID != pid {
			continue
		}
		dir, _ := launch.ProcessUserDir(launch.Host(), p)
		w, h, _ := host.RenderSize(pid)
		x, why := launch.ReadGFXAspect(dir, p.Args).XScale(w, h)
		return 1 / x, why, nil
	}
	return 1, "", fmt.Errorf("widescreen auto: Dolphin %d not found", pid)
}

// Command is the `widescreen` CLI command.
func Command(env Env) wwapi.Command {
	return wwapi.Command{
		Name: "widescreen", Group: wwapi.GroupDev, Hidden: true,
		Run: func(_ string, args []string) {
			if err := run(env, args, os.Stdout); err != nil {
				fmt.Printf("ERROR: %v\nUsage: %s\n", err, usage)
				os.Exit(1)
			}
		},
	}
}

func run(env Env, args []string, w io.Writer) error {
	d, err := env.Open(0)
	if err != nil {
		return err
	}
	defer d.Close()
	return apply(d, d.PID(), args, w, autoFactor)
}

// checkContract is the strict contract check (tests replace it).
var checkContract = func(m mem.Reader) error {
	c, err := wwapi.CheckContract(m, wwapi.ContractOptions{})
	if err != nil {
		return err
	}
	return c.Err()
}

// apply is run over any Dolphin memory: with no argument it prints the
// block; with one it checks the contract and the mailbox claim, submits
// WS_SET and waits.
func apply(m mem.Mem, pid uint32, args []string, w io.Writer, auto func(pid uint32) (float64, string, error)) error {
	fs := flag.NewFlagSet("widescreen", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	flags := fs.Uint("flags", 0xFF, "flags (masked by what the blob supports)")
	// Flags may come after the positional words.
	var pos []string
	for len(args) > 0 {
		if err := fs.Parse(args); err != nil {
			return err
		}
		if fs.NArg() == 0 {
			break
		}
		pos = append(pos, fs.Arg(0))
		args = fs.Args()[1:]
	}
	if *flags > 0xFF {
		return fmt.Errorf("--flags %d does not fit a byte", *flags)
	}
	blk, err := mailbox.ReadWidescreen(m)
	if err != nil {
		return err
	}
	if len(pos) == 0 {
		fmt.Fprintf(w, "widescreen block @ 0x%08X: %s\n", mailbox.WsAddr, blk)
		return nil
	}
	mode := uint8(mailbox.WsModeClip)
	if pos[0] == "native" {
		mode = mailbox.WsModeNative
		pos = pos[1:]
		if len(pos) != 1 {
			return errors.New("widescreen native needs one aspect")
		}
	} else if len(pos) != 1 {
		return errors.New("too many words")
	}
	set, err := ParseSetting(pos[0])
	if err != nil {
		return err
	}
	if mode == mailbox.WsModeNative && (set.Auto || set.Off) {
		return errors.New("widescreen native needs an aspect (16:9, W:H or a decimal)")
	}
	if err := checkContract(m); err != nil {
		return err
	}
	if !blk.Present {
		return errors.New("widescreen: this blob has no widescreen block; re-patch with this version")
	}
	if b, err := m.Read(mailbox.Mailbox+mailbox.OffShadowMode, 1); err != nil {
		return err
	} else if b[0] != 0 {
		return ErrClaimed
	}
	if set.Auto {
		f, why, err := auto(pid)
		if err != nil {
			return err
		}
		fmt.Fprintf(w, "auto: %s\n", why)
		set = Setting{Aspect: f * standard}
	}
	req := RequestFor(set, mode, uint8(*flags))
	a, b, c := req.Words()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	q := ops.New(m)
	if err := q.Ready(); err != nil {
		return err
	}
	r, err := q.Run(ctx, mailbox.OpsOpWsSet, a, b, c)
	if err != nil {
		return err
	}
	if r != ops.ROK {
		return fmt.Errorf("widescreen: the game answered %s", r)
	}
	after, err := mailbox.ReadWidescreen(m)
	if err != nil {
		return err
	}
	fmt.Fprintf(w, "set: %s (lasts until the next boot or reset)\n", after)
	return nil
}
