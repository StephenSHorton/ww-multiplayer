package main

// The command bodies below moved verbatim from ww-multiplayer's
// commands.go (Phase D6), where they had moved from main's switch.

import (
	"fmt"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	coopsession "github.com/StephenSHorton/ww-multiplayer/mods/coop/session"
	"github.com/StephenSHorton/ww-multiplayer/mods/multiplayer/feed"
	"github.com/StephenSHorton/ww-multiplayer/mods/multiplayer/net"
)

func cmdDolphin2(string, []string) {
	reset := false
	for _, a := range os.Args[2:] {
		if a == "--reset" {
			reset = true
		}
	}
	runDolphin2(reset)
}

func cmdMpLocal(string, []string) {
	co, rest, err := coopsession.ExtractFlags(os.Args[2:])
	if err != nil {
		exitUsage(err, "mp-local [nameA] [nameB] "+coopsession.FlagsUsage)
	}
	if i := slices.Index(rest, "--"); co.On() && i >= 0 {
		rest = slices.Delete(rest, i, i+1) // with --coop, "--" only ends the options
	}
	nameA, nameB := "PlayerA", "PlayerB"
	if len(rest) > 0 {
		nameA = rest[0]
	}
	if len(rest) > 1 {
		nameB = rest[1]
	}
	for _, n := range []string{nameA, nameB} {
		if err := network.ValidatePlayerName(n); err != nil {
			exitUsage(err, "mp-local [nameA] [nameB]")
		}
	}
	runMpLocal(nameA, nameB, co)
}

func cmdScreenshot(string, []string) {
	out := ""
	if len(os.Args) > 2 {
		out = os.Args[2]
	}
	runScreenshot(out)
}

func cmdAutoRecapture(string, []string) {
	out := ""
	if len(os.Args) > 2 {
		out = os.Args[2]
	}
	runAutoRecapture(out)
}

func cmdAutoRecapturePair(string, []string) {
	out1, out2 := "", ""
	delaySecs := 5
	if len(os.Args) > 2 {
		out1 = os.Args[2]
	}
	if len(os.Args) > 3 {
		out2 = os.Args[3]
	}
	if len(os.Args) > 4 {
		if n, err := strconv.Atoi(os.Args[4]); err == nil && n > 0 {
			delaySecs = n
		}
	}
	runAutoRecapturePair(out1, out2, time.Duration(delaySecs)*time.Second)
}

func cmdMovePuppet(string, []string) {
	if len(os.Args) < 5 {
		fmt.Println("Usage: ww move-puppet <x> <y> <z> [slot=0]")
		os.Exit(1)
	}
	slot := 0
	if len(os.Args) > 5 {
		if v, err := strconv.Atoi(os.Args[5]); err == nil {
			slot = v
		}
	}
	runMovePuppet(os.Args[2], os.Args[3], os.Args[4], slot)
}

func cmdInput(string, []string) {
	// input <buttons-hex> <stickX-s8> <stickY-s8> [ms=1000]
	// Drives synthetic controller input through the C-side
	// pad_read_shim. If ms > 0, holds for that duration then
	// releases (input_enable=0). If ms == 0, sets-and-leaves —
	// caller is responsible for releasing.
	if len(os.Args) < 5 {
		fmt.Println("Usage: wwdev.exe input <buttons-hex> <stickX> <stickY> [ms=1000]")
		fmt.Println("  buttons mask: A=0x100 B=0x200 X=0x400 Y=0x800 Start=0x1000")
		fmt.Println("                Z=0x10 R=0x20 L=0x40")
		fmt.Println("                DPad L=1 R=2 D=4 U=8")
		fmt.Println("  stickX/Y: -128..+127 (0 = neutral)")
		fmt.Println("  ms: hold duration; 0 = set-and-leave (manual release)")
		os.Exit(1)
	}
	ms := 1000
	if len(os.Args) > 5 {
		if v, err := strconv.Atoi(os.Args[5]); err == nil {
			ms = v
		}
	}
	runInput(os.Args[2], os.Args[3], os.Args[4], ms)
}

func cmdInputProbe(string, []string) {
	// input-probe <buttons-hex> <stickX-s8> <stickY-s8> [ms=2000]
	// Direct write to JUTGamePad::mPadStatus[0] at 0x803ED818.
	// KNOWN NOT TO WORK: writes survive same-process readback
	// but don't propagate to game logic — either dual-mapping
	// or per-frame SI overwrite races. Kept as a diagnostic
	// for memory-write reachability against the input region.
	// For real input use the `input` subcommand instead.
	if len(os.Args) < 5 {
		fmt.Println("Usage: wwdev.exe input-probe <buttons-hex> <stickX> <stickY> [ms=2000]")
		os.Exit(1)
	}
	ms := 2000
	if len(os.Args) > 5 {
		if v, err := strconv.Atoi(os.Args[5]); err == nil {
			ms = v
		}
	}
	runInputProbe(os.Args[2], os.Args[3], os.Args[4], ms)
}

func cmdPokeU32(string, []string) {
	if len(os.Args) < 4 {
		fmt.Println("Usage: ww poke-u32 <addr-hex> <value-hex>")
		os.Exit(1)
	}
	runPokeU32(os.Args[2], os.Args[3])
}

func cmdShadowMode(string, []string) {
	if len(os.Args) < 3 {
		fmt.Println("Usage: ww shadow-mode <0|5>  (0=off 5=pose-feed)")
		os.Exit(1)
	}
	runShadowMode(os.Args[2])
}

func cmdPoseTest(string, []string) {
	mode := "mirror"
	dur := 30
	if len(os.Args) > 2 {
		mode = os.Args[2]
	}
	if len(os.Args) > 3 {
		if v, err := strconv.Atoi(os.Args[3]); err == nil {
			dur = v
		}
	}
	runPoseTest(mode, dur)
}

func cmdBroadcastLink(string, []string) {
	name := "Player"
	addr := "localhost:25565"
	if len(os.Args) > 2 {
		name = os.Args[2]
	}
	if len(os.Args) > 3 {
		addr = os.Args[3]
	}
	runBroadcastLink(name, addr)
}

func cmdPoseFakeLoop(string, []string) {
	name := "FakePlayer"
	addr := "localhost:25565"
	if len(os.Args) > 2 {
		name = os.Args[2]
	}
	if len(os.Args) > 3 {
		addr = os.Args[3]
	}
	feed.PoseFakeLoop(feedEnv, name, addr)
}

func cmdTunicRecolor(string, []string) {
	if len(os.Args) < 3 {
		fmt.Println("Usage: wwdev.exe tunic-recolor <color|#RRGGBB|reset>")
		os.Exit(1)
	}
	runTunicRecolor(os.Args[2])
}

func cmdTunicSlot(string, []string) {
	if len(os.Args) < 4 {
		fmt.Println("Usage: wwdev.exe tunic-slot <0|1|local> <color|#RRGGBB|off|casual>")
		os.Exit(1)
	}
	runTunicSlot(os.Args[2], os.Args[3])
}

func cmdDisasm(string, []string) {
	addr := uint32(0x800231E4)
	count := 40
	if len(os.Args) > 2 {
		var v uint64
		v, err := strconv.ParseUint(strings.TrimPrefix(os.Args[2], "0x"), 16, 32)
		if err != nil {
			fmt.Printf("bad addr: %v\n", err)
			os.Exit(1)
		}
		addr = uint32(v)
	}
	if len(os.Args) > 3 {
		n, err := strconv.Atoi(os.Args[3])
		if err == nil {
			count = n
		}
	}
	runDisasm(addr, count)
}

func cmdPokeVec3(string, []string) {
	if len(os.Args) < 6 {
		fmt.Println("Usage: wwdev.exe poke-vec3 <addr-hex> <x> <y> <z>")
		os.Exit(1)
	}
	runPokeVec3(os.Args[2], os.Args[3], os.Args[4], os.Args[5])
}

func cmdPeek(string, []string) {
	if len(os.Args) < 3 {
		fmt.Println("Usage: wwdev.exe peek <addr-hex> [n=4]")
		os.Exit(1)
	}
	n := uint32(4)
	if len(os.Args) > 3 {
		if v, err := strconv.ParseUint(os.Args[3], 10, 32); err == nil {
			n = uint32(v)
		}
	}
	runPeek(os.Args[2], n)
}

func cmdWarpForce(string, []string) {
	if len(os.Args) < 5 {
		fmt.Println("Usage: wwdev.exe warp-force <x> <y> <z>")
		fmt.Println("  Sets warp target + warp_force flag so C re-applies the warp")
		fmt.Println("  every frame. Use `warp-force-off` to release.")
		os.Exit(1)
	}
	runWarpForce(os.Args[2], os.Args[3], os.Args[4], true)
}

func cmdEyeAnimDlDump(string, []string) {
	if len(os.Args) < 3 {
		fmt.Println("Usage: wwdev.exe eye-anim-dl-dump <model-addr-hex> [n=256]")
		os.Exit(1)
	}
	n := uint32(256)
	if len(os.Args) > 3 {
		if v, err := strconv.ParseUint(os.Args[3], 10, 32); err == nil {
			n = uint32(v)
		}
	}
	runEyeAnimDLDump(os.Args[2], n)
}

func cmdEyeAnimWatch(string, []string) {
	if len(os.Args) < 4 {
		fmt.Println("Usage: wwdev.exe eye-anim-watch <addr-hex> <size-hex> [secs=10]")
		fmt.Println("  Watches a memory range for byte-level changes over time.")
		fmt.Println("  Reports byte offsets that ever differ from the initial sample.")
		os.Exit(1)
	}
	secs := 10
	if len(os.Args) > 4 {
		if v, err := strconv.Atoi(os.Args[4]); err == nil {
			secs = v
		}
	}
	runEyeAnimWatch(os.Args[2], os.Args[3], secs)
}

func cmdEyeAnimPoke(string, []string) {
	if len(os.Args) < 3 {
		fmt.Println("Usage: wwdev.exe eye-anim-poke <model-addr-hex> [secs=10]")
		os.Exit(1)
	}
	secs := 10
	if len(os.Args) > 3 {
		if v, err := strconv.Atoi(os.Args[3]); err == nil {
			secs = v
		}
	}
	runEyeAnimPoke(os.Args[2], secs)
}

func cmdFaceSyncWatch(string, []string) {
	secs := 10
	if len(os.Args) > 2 {
		if v, err := strconv.Atoi(os.Args[2]); err == nil {
			secs = v
		}
	}
	runFaceSyncWatch(secs)
}

func cmdFaceTevHammer(string, []string) {
	secs := 5
	texHex := "0099"
	if len(os.Args) > 2 {
		if v, err := strconv.Atoi(os.Args[2]); err == nil {
			secs = v
		}
	}
	if len(os.Args) > 3 {
		texHex = os.Args[3]
	}
	runFaceTevHammer(secs, texHex)
}

func cmdFaceMatHammer(string, []string) {
	if len(os.Args) < 3 {
		fmt.Println("Usage: wwdev.exe face-mat-hammer <idx> [hex=0099] [secs=0]")
		fmt.Println("  Tight-loop hammers mat[idx]'s tev_block.texno[0..1]. Run until Ctrl+C if secs=0.")
		os.Exit(1)
	}
	idx, _ := strconv.Atoi(os.Args[2])
	texHex := "0099"
	secs := 0
	if len(os.Args) > 3 {
		texHex = strings.TrimPrefix(os.Args[3], "0x")
	}
	if len(os.Args) > 4 {
		if v, err := strconv.Atoi(os.Args[4]); err == nil {
			secs = v
		}
	}
	runFaceMatHammer(idx, texHex, secs)
}

func cmdFaceSyncTevPoll(string, []string) {
	secs := 3
	faceHex := "9988997799669955"
	if len(os.Args) > 2 {
		if v, err := strconv.Atoi(os.Args[2]); err == nil {
			secs = v
		}
	}
	if len(os.Args) > 3 {
		faceHex = strings.TrimPrefix(os.Args[3], "0x")
	}
	runFaceSyncTevPoll(secs, faceHex)
}

func cmdFaceDlLoop(string, []string) {
	secs := 0 // 0 = until Ctrl+C
	forceLsb := -1
	if len(os.Args) > 2 {
		if v, err := strconv.Atoi(os.Args[2]); err == nil {
			secs = v
		}
	}
	if len(os.Args) > 3 {
		h := strings.TrimPrefix(os.Args[3], "0x")
		if v, err := strconv.ParseUint(h, 16, 8); err == nil {
			forceLsb = int(v)
		}
	}
	runFaceDlLoop(secs, forceLsb)
}

func cmdFaceSyncFake(string, []string) {
	if len(os.Args) < 4 {
		fmt.Println("Usage: wwdev.exe face-sync-fake <slot> <hex8>")
		fmt.Println("  hex8 = 8 hex bytes for [mat1_t0:u16][mat1_t1:u16][mat4_t0:u16][mat4_t1:u16]")
		fmt.Println("  e.g. 00000027000000270 to force open eyes; 0000FFFF0000FFFF for closed")
		os.Exit(1)
	}
	slot, _ := strconv.Atoi(os.Args[2])
	runFaceSyncFake(slot, os.Args[3])
}

func cmdFaceSyncFakeLoop(string, []string) {
	if len(os.Args) < 4 {
		fmt.Println("Usage: wwdev.exe face-sync-fake-loop <slot> <hex8> [secs=0]")
		os.Exit(1)
	}
	slot, _ := strconv.Atoi(os.Args[2])
	secs := 0
	if len(os.Args) > 4 {
		if v, err := strconv.Atoi(os.Args[4]); err == nil {
			secs = v
		}
	}
	runFaceSyncFakeLoop(slot, os.Args[3], secs)
}

func cmdPpcDisasm(string, []string) {
	if len(os.Args) < 3 {
		fmt.Println("Usage: wwdev.exe ppc-disasm <addr-hex> [num-words=32]")
		os.Exit(1)
	}
	addr, err := strconv.ParseUint(strings.TrimPrefix(os.Args[2], "0x"), 16, 32)
	if err != nil {
		fmt.Printf("bad addr %q: %v\n", os.Args[2], err)
		os.Exit(1)
	}
	n := 32
	if len(os.Args) > 3 {
		if v, err := strconv.Atoi(os.Args[3]); err == nil {
			n = v
		}
	}
	runPPCDisasm(uint32(addr), n)
}

func cmdTrackPos(string, []string) {
	secs := 5
	if len(os.Args) > 2 {
		if v, err := strconv.Atoi(os.Args[2]); err == nil {
			secs = v
		}
	}
	runTrackPos(secs)
}
