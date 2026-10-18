// To regenerate the Windows resources (ww_windows.syso: the icon plus the
// application manifest, packaging/ww-multiplayer.exe.manifest) after
// changing docs/img/icon.ico or the manifest, run
// `go generate ./cmd/ww-multiplayer` from the repo root with rsrc on PATH
// (go generate runs in this directory, so the paths below start at ../..):
//   go install github.com/akavel/rsrc@v0.10.2
// The second line rebuilds docs/img/icon.icns (the macOS app icon) from
// the same icon.ico. Go's linker auto-includes any *.syso in the main
// package's directory (so the .syso lives here, next to main.go); the
// _windows suffix is a build constraint so non-Windows builds skip it.
//go:generate rsrc -manifest ../../packaging/ww-multiplayer.exe.manifest -ico ../../docs/img/icon.ico -o ww_windows.syso
//go:generate go run ../../tools/mkicns -o ../../docs/img/icon.icns ../../docs/img/icon.ico

package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/internal/cliutil"
	"github.com/StephenSHorton/ww-multiplayer/internal/gui"
	"github.com/StephenSHorton/ww-multiplayer/internal/netutil"
	"github.com/StephenSHorton/ww-multiplayer/internal/patch"
	"github.com/StephenSHorton/ww-multiplayer/internal/session"
	coopsession "github.com/StephenSHorton/ww-multiplayer/mods/coop/session"
	"github.com/StephenSHorton/ww-multiplayer/mods/multiplayer/engine"
	"github.com/StephenSHorton/ww-multiplayer/mods/multiplayer/feed"
	"github.com/StephenSHorton/ww-multiplayer/mods/multiplayer/net"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/avatar"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/log"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/player"
)

// version is overridden at build time by `go build -ldflags "-X main.version=..."`.
// release.yml passes the git tag (e.g. "v0.1.5"); local builds keep "dev".
var version = "dev"

// defaultPort is the server port used when the user doesn't pass --port
// (host/server/join) or an explicit :port (join). Keep this in sync with the
// mp-local / dolphin2 test harness in cmd/wwdev/mp_local.go, which intentionally
// stays hardcoded to 25565 (internal test rig, not user-facing).
const defaultPort = engine.DefaultPort

// exeCmd is the binary as the user types it on this platform —
// ".\ww-multiplayer.exe" on Windows, "./ww-multiplayer" everywhere else
// (cliutil.ExeCmd). Help text and error messages embed this so a macOS
// user copying a command from `help` doesn't end up with a `.exe` suffix
// that doesn't exist.
var exeCmd = cliutil.ExeCmd("ww-multiplayer")

// feedEnv is what the pose pipelines (mods/multiplayer/feed) take from
// package main: cliutil.OpenDolphin, and player connections with the
// session's color preference (newPlayerClientCtx).
var feedEnv = feed.Env{Open: cliutil.OpenDolphin, NewClient: engine.NewPlayerClientCtx}

func main() {
	// Drop macOS Finder's -psn_ argument first (app_face.go).
	os.Args = append(os.Args[:1:1], stripPSN(os.Args[1:], runtime.GOOS)...)
	cliutil.ApplyColorPrefFromArgs() // --color / WW_COLOR, docs/09-player-colors.md
	// No arguments, `tui` and `ui` open the app window or the TUI
	// (app_face.go); every other subcommand is the CLI's command registry
	// (commands.go).
	if f := appFace(os.Args[1:], gui.Available()); f != faceCLI {
		runFace(f, os.Args[1:])
		return
	}
	if len(os.Args) > 1 {
		runCommand(os.Args[1], os.Args[2:])
	}
}

// versionLine is what `version` / `--version` prints, e.g.
// "ww-multiplayer v0.3.0" for a release build or "ww-multiplayer dev".
func versionLine() string {
	return "ww-multiplayer " + version
}

// runPatch is the `patch` command: patch.Run (internal/patch) writes the
// patcher's lines on stdout, with the loaded mods' post-patch steps (the
// co-op stage tables), and a
// failure exits 1. An already-patched input exits 0, as it always has.
func runPatch(in, out string) {
	if _, err := patch.Run(context.Background(), in, out, os.Stdout, patch.Options{PostPatch: postPatchSteps()}); err != nil {
		os.Exit(1)
	}
}

// Same as broadcast-link but ALSO sends Link's full skeletal pose every
// tick. The receiver's puppet-sync writes that pose into mailbox.pose_buf
// and flips shadow_mode=5 so Link #2 animates from the wire instead of
// mirroring locally. Link's J3DModel is at daPy_lk_c + 0x032C; mpNodeMtx
// is at J3DModel + 0x8C; sizeof(Mtx) = 48; Link has 42 joints.
//
// Standalone-CLI wrapper. Preserves the `ww-multiplayer.exe broadcast-pose` entry
// point that scripts/mplay2.sh relies on. Installs the same SIGINT handler
// as host/join so Ctrl+C exits cleanly (the broadcast side doesn't touch
// shadow_mode so there's nothing to clean up — the signal handler just
// gets us out of the send tick immediately instead of on the next tick).
func runBroadcastPose(name, addr string) {
	rep := log.Stdout{}
	ctx, cancel := engine.CLIMultiplayerContext(rep)
	defer cancel()
	if err := feed.BroadcastPose(ctx, feedEnv, name, addr, 0, rep, nil, nil); err != nil {
		engine.LogSessionErr(rep, err)
		os.Exit(1)
	}
}

// Connects to a server, subscribes to remote position broadcasts, and writes
// the most recent remote player's position into the Dolphin mailbox every
// frame. The C-side frame hook then mirrors mailbox.p2_pos into the puppet
// actor's pos each frame. End-to-end loop:
//
//	remote player -> server -> puppet-sync -> mailbox -> C hook -> actor pos
//
// Standalone-CLI wrapper. Honors WW_SELF_NAME (kept for mplay2.sh); host/
// join call feed.PuppetSync directly with an explicit filter name. Installs
// the same SIGINT handler as host/join so mplay2.sh's Ctrl+C path resets
// the mailbox (shadow_mode=0 + pose_seqs[*]=0) instead of leaving Link #2
// frozen at the last received pose.
func runPuppetSync(name, addr string) {
	rep := log.Stdout{}
	ctx, cancel := engine.CLIMultiplayerContext(rep)
	defer cancel()
	err := feed.PuppetSync(ctx, feedEnv, name, addr, os.Getenv("WW_SELF_NAME"), 0, rep, nil, nil)
	clearMultiplayerState()
	if err != nil {
		engine.LogSessionErr(rep, err)
		os.Exit(1)
	}
}

func abs32(f float32) float32 {
	if f < 0 {
		return -f
	}
	return f
}

// runWarp is the CLI entry point for ad-hoc Link teleporting. Honors the
// usual env knobs for Dolphin selection (WW_DOLPHIN_INDEX / WW_DOLPHIN_PID).
func runWarp(xs, ys, zs string) {
	x, err := strconv.ParseFloat(xs, 32)
	if err != nil {
		fmt.Printf("bad X: %v\n", err)
		os.Exit(1)
	}
	y, err := strconv.ParseFloat(ys, 32)
	if err != nil {
		fmt.Printf("bad Y: %v\n", err)
		os.Exit(1)
	}
	z, err := strconv.ParseFloat(zs, 32)
	if err != nil {
		fmt.Printf("bad Z: %v\n", err)
		os.Exit(1)
	}
	d, err := cliutil.FindDolphin("GZLE01")
	if err != nil {
		fmt.Printf("ERROR: %v\n", err)
		os.Exit(1)
	}
	defer d.Close()
	if err := player.Warp(d, [3]float32{float32(x), float32(y), float32(z)}, 1*time.Second); err != nil {
		fmt.Printf("ERROR: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Warped Link to (%.1f, %.1f, %.1f).\n", x, y, z)
	posAfter, _ := player.NewLink(d).Position()
	if posAfter != nil {
		fmt.Printf("  Read-after-warp current.pos = (%.2f, %.2f, %.2f) — %s\n",
			posAfter.PosX, posAfter.PosY, posAfter.PosZ,
			map[bool]string{true: "stuck!", false: "REVERTED"}[abs32(posAfter.PosX-float32(x)) < 1.0])
	}
}

func runDebug() {
	fmt.Println("=== WW Multiplayer Debug Mode ===")
	fmt.Println()

	d, err := cliutil.FindDolphin("GZLE01")
	if err != nil {
		fmt.Printf("ERROR: %v\n", err)
		os.Exit(1)
	}
	defer d.Close()
	fmt.Printf("Dolphin found. GC RAM: 0x%X\n\n", d.GCRamBase())

	fmt.Println("Reading position for 5 seconds...")
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		pos, err := player.NewLink(d).Position()
		if err != nil {
			fmt.Printf("  waiting... (%v)\n", err)
			time.Sleep(500 * time.Millisecond)
			continue
		}
		fmt.Printf("  X:%10.1f  Y:%10.1f  Z:%10.1f  RotY:%6d\n",
			pos.PosX, pos.PosY, pos.PosZ, pos.RotY)
		time.Sleep(200 * time.Millisecond)
	}
	fmt.Println("\nDone.")
}

// extractPortFlag scans args for a "--port N" pair or a "--port=N" arg (in
// any position, so it works whether the user wrote `host --port 25566 Alice`
// or `host Alice --port=25566`) and returns the parsed port plus the
// remaining args with the flag and its value removed. The Go-flag spelling
// "-port N" / "-port=N" works too. Returns defaultPort when the flag is
// absent. A missing, non-numeric or out-of-range value, or any other "-" or
// "--" option, is an error, so a typo can't silently fall back to the
// default port or end up as the player name. A bare "--" ends the options:
// everything after it is positional, for a name that starts with '-'.
func extractPortFlag(args []string, defaultPort int) (port int, rest []string, err error) {
	port = defaultPort
	rest = make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		a := args[i]
		if strings.HasPrefix(a, "-port") {
			a = "-" + a // "-port N" / "-port=N" -> the "--" form
		}
		var val string
		switch {
		case a == "--port":
			if i+1 >= len(args) {
				return 0, nil, errors.New("--port needs a value, e.g. --port 25566")
			}
			i++ // consume the value
			val = args[i]
		case strings.HasPrefix(a, "--port="):
			val = strings.TrimPrefix(a, "--port=")
		case a == "--":
			// Everything after "--" is positional, so a player name may
			// start with '-'.
			rest = append(rest, args[i+1:]...)
			return port, rest, nil
		case len(a) > 1 && a[0] == '-':
			return 0, nil, fmt.Errorf("unknown option %q (put -- before a name that starts with '-')", args[i])
		default:
			rest = append(rest, a)
			continue
		}
		n, err := parsePort(val)
		if err != nil {
			return 0, nil, fmt.Errorf("--port: %w", err)
		}
		port = n
	}
	return port, rest, nil
}

// parsePort parses a TCP port number, accepting only 1-65535.
func parsePort(s string) (int, error) {
	n, err := strconv.Atoi(s)
	if err != nil || n < 1 || n > 65535 {
		return 0, fmt.Errorf("invalid port %q: must be a number from 1 to 65535", s)
	}
	return n, nil
}

// rejectExtraArgs errors when rest holds more than limit positional args,
// so a port typed without the flag (`server 25566`, `host Alice 25566`)
// stops the command instead of being dropped while it quietly uses 25565.
func rejectExtraArgs(rest []string, limit int) error {
	if len(rest) <= limit {
		return nil
	}
	extra := rest[limit]
	if _, err := parsePort(extra); err == nil {
		return fmt.Errorf("unexpected argument %q (did you mean --port %s?)", extra, extra)
	}
	return fmt.Errorf("unexpected argument %q", extra)
}

// parseHostArgs parses `host [name] [--port N]` into the player name ("" =
// let runHost pick one) and the port to listen on. A name the host would
// refuse (too long, control characters) is an error here, before Dolphin
// is touched.
func parseHostArgs(args []string) (name string, port int, err error) {
	port, rest, err := extractPortFlag(args, defaultPort)
	if err != nil {
		return "", 0, err
	}
	if err := rejectExtraArgs(rest, 1); err != nil {
		return "", 0, err
	}
	if len(rest) > 0 {
		name = rest[0]
		if err := network.ValidatePlayerName(name); err != nil {
			return "", 0, err
		}
	}
	return name, port, nil
}

// parseServerArgs parses `server [--port N]` into the port to listen on.
func parseServerArgs(args []string) (port int, err error) {
	port, rest, err := extractPortFlag(args, defaultPort)
	if err != nil {
		return 0, err
	}
	if err := rejectExtraArgs(rest, 0); err != nil {
		return 0, err
	}
	return port, nil
}

// parseJoinArgs parses `join <host-ip[:port]> [name] [--port N]` into the
// address to dial and the player name. The address gets defaultPort, or the
// --port value, when it names no port of its own. An address port that
// isn't a valid port, or one that disagrees with --port, is an error, and so
// is a name the host would refuse.
func parseJoinArgs(args []string) (addr, name string, err error) {
	flagPort, rest, err := extractPortFlag(args, 0) // 0 = no --port given
	if err != nil {
		return "", "", err
	}
	if len(rest) == 0 {
		return "", "", errors.New("missing the host's address")
	}
	if err := rejectExtraArgs(rest, 2); err != nil {
		return "", "", err
	}
	if len(rest) > 1 {
		name = rest[1]
		if err := network.ValidatePlayerName(name); err != nil {
			return "", "", err
		}
	}
	port := flagPort
	if port == 0 {
		port = defaultPort
	}
	// A join code with a typo would otherwise be dialed as a host name:
	// refuse it with the reason (which doesn't repeat the code).
	if netutil.LooksLikeJoinCode(rest[0]) {
		if _, err := netutil.ParseJoinCode(rest[0]); err != nil {
			return "", "", err
		}
	}
	// netutil.NormalizeAddr is IPv6-aware and always returns host:port.
	addr = netutil.NormalizeAddr(rest[0], port)
	host, p, _ := net.SplitHostPort(addr)
	if host == "" {
		return "", "", fmt.Errorf("missing the host in address %q", rest[0])
	}
	n, err := parsePort(p)
	if err != nil {
		return "", "", fmt.Errorf("address %q: %w", rest[0], err)
	}
	if flagPort != 0 && n != flagPort {
		return "", "", fmt.Errorf("address %q has port %d but --port says %d; give the port once", rest[0], n, flagPort)
	}
	return addr, name, nil
}

// exitUsage reports a command-line mistake with the command's usage line and
// exits non-zero.
func exitUsage(err error, usage string) {
	cliutil.ExitUsage(exeCmd, err, usage)
}

// runHost is the single-process host entry point: binds the TCP server on
// the given port (defaultPort unless --port overrides it), then spins up
// broadcast-pose + puppet-sync goroutines pointing at localhost. Replaces
// the old "run 3 terminals" workflow. Ctrl+C cancels the ctx, cleanly shuts
// down both goroutines and the server, and resets the patched ISO's
// mailbox so the next Dolphin frame doesn't keep rendering a stale Link #2.
// co.on (--coop) adds co-op: the hub on the server and a coop-sync
// pipeline (mods/coop/session).
//
// The session runs on a session.Controller (App PR 2) over the CLI's
// engine, which prints every line to stdout as engine.RunHostSession always has
// and asks the co-op questions on stdin; a failed session exits 1.
func runHost(name string, port int, co coopsession.Flags) {
	rep := log.Stdout{}
	ctx, cancel := engine.CLIMultiplayerContext(rep)
	defer cancel()
	ctrl := newCLIController(rep)
	if code := engine.RunCLISession(ctrl, rep, func() (session.ID, error) {
		return ctrl.Host(ctx, session.HostOptions{Name: name, Port: port, Coop: co.Options()})
	}); code != 0 {
		os.Exit(code)
	}
}

// newCLIController is the controller of a CLI host or join: the CLI's
// engine, with every session line going to rep (stdout).
func newCLIController(rep log.Reporter) *session.Controller {
	return session.New(newCLIEngine(), session.Options{Log: rep})
}

// runJoin is the single-process joiner entry point: just broadcast-pose +
// puppet-sync goroutines pointed at the host's address. Same signal
// handling and mailbox cleanup as runHost. co.on (--coop) adds a
// coop-sync pipeline. Like runHost, it runs on a session.Controller.
func runJoin(addr, name string, co coopsession.Flags) {
	rep := log.Stdout{}
	ctx, cancel := engine.CLIMultiplayerContext(rep)
	defer cancel()
	ctrl := newCLIController(rep)
	if code := engine.RunCLISession(ctrl, rep, func() (session.ID, error) {
		return ctrl.Join(ctx, session.JoinOptions{Addr: addr, Name: name, Coop: co.Options()})
	}); code != 0 {
		os.Exit(code)
	}
}

// clearMultiplayerState resets the env-selected Dolphin's mailbox so the
// next frame stops rendering Link #2 (avatar.Clear: shadow_mode = 0 and
// every pose_seqs[slot] zeroed). Best-effort: if Dolphin closed first
// there is nothing to reset (the mailbox is gone with the process).
func clearMultiplayerState() {
	d, err := cliutil.FindDolphin("GZLE01")
	if err != nil {
		return
	}
	defer d.Close()
	avatar.Clear(d)
}
