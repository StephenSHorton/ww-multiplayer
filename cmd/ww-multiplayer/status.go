package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/internal/netutil"
	"github.com/StephenSHorton/ww-multiplayer/mods/multiplayer/net"
)

// `status`: the status probe (mods/multiplayer/net/status.go,
// docs/11-self-hosting.md "Health check") from the command line. A
// container's HEALTHCHECK runs it against its own server; a player runs it
// to tell "nothing there" from "a server of another version".

const statusUsage = "status <host[:port]|code> [--json] [--timeout D]"

// The status command's exit codes. A usage error exits 1, as every
// command's does (cliutil.ExitUsage).
const (
	statusUp          = 0 // a server of this protocol answered
	statusUnreachable = 1 // the dial or the probe failed
	statusOther       = 3 // something answered, but no server this build can play with
)

// statusArgs are `status`'s parsed arguments.
type statusArgs struct {
	addr    string
	json    bool
	timeout time.Duration
}

func parseStatusArgs(args []string) (statusArgs, error) {
	a := statusArgs{timeout: 5 * time.Second}
	var pos []string
	for i := 0; i < len(args); i++ {
		s := args[i]
		switch {
		case s == "--json" || s == "-json":
			a.json = true
		case s == "--timeout" || s == "-timeout" || strings.HasPrefix(s, "--timeout=") || strings.HasPrefix(s, "-timeout="):
			val := ""
			if k := strings.Index(s, "="); k >= 0 {
				val = s[k+1:]
			} else {
				if i+1 >= len(args) {
					return a, errors.New("--timeout needs a value, e.g. --timeout 3s")
				}
				i++
				val = args[i]
			}
			d, err := time.ParseDuration(val)
			if err != nil || d <= 0 {
				return a, fmt.Errorf("--timeout: %q is not a positive duration (e.g. 3s)", val)
			}
			a.timeout = d
		case len(s) > 1 && s[0] == '-':
			return a, fmt.Errorf("unknown option %q", s)
		default:
			pos = append(pos, s)
		}
	}
	switch {
	case len(pos) == 0:
		return a, errors.New("missing the server's address")
	case len(pos) > 1:
		return a, fmt.Errorf("unexpected argument %q", pos[1])
	}
	if err := netutil.ValidateAddr(pos[0]); err != nil {
		return a, err
	}
	a.addr = netutil.NormalizeAddr(pos[0], defaultPort)
	return a, nil
}

func cmdStatus(string, []string) {
	a, err := parseStatusArgs(os.Args[2:])
	if err != nil {
		exitUsage(err, statusUsage)
	}
	b, err := network.ProbeStatus(context.Background(), a.addr, a.timeout)
	os.Exit(reportStatus(os.Stdout, a, b, err))
}

// reportStatus prints the probe's outcome and returns the exit code.
func reportStatus(w io.Writer, a statusArgs, b network.StatusBody, err error) int {
	code := statusUp
	verdict := "up"
	switch {
	case errors.Is(err, network.ErrStatusLegacy):
		code, verdict = statusOther, "no status: an older ww-multiplayer or another program"
	case err != nil && b.App != "":
		code, verdict = statusOther, err.Error()
	case err != nil:
		code, verdict = statusUnreachable, "unreachable: "+err.Error()
	case b.Protocol != network.ProtocolVersion:
		code = statusOther
		verdict = fmt.Sprintf("protocol %d, this build speaks %d: both need the same version", b.Protocol, network.ProtocolVersion)
	}
	if a.json {
		out := struct {
			Addr   string              `json:"addr"`
			OK     bool                `json:"ok"`
			Status string              `json:"status"`
			Server *network.StatusBody `json:"server,omitempty"`
		}{Addr: a.addr, OK: code == statusUp, Status: verdict}
		if b.App != "" {
			out.Server = &b
		}
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		enc.Encode(out)
		return code
	}
	if b.App == "" {
		fmt.Fprintf(w, "%s: %s\n", a.addr, verdict)
		return code
	}
	v := b.Version
	if v == "" {
		v = "unknown version"
	}
	key := ""
	if b.OwnerKey {
		key = ", owner key set"
	}
	fmt.Fprintf(w, "%s: %s (%s %s, protocol %d; %d players, %d in the lobby, phase %s%s; up %s)\n",
		a.addr, verdict, b.App, v, b.Protocol, b.Players, b.Lobby, b.Phase, key, time.Duration(b.UptimeSecs)*time.Second)
	return code
}
