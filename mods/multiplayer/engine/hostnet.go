package engine

import (
	"context"
	"net"
	"os"
	"os/signal"
	"strconv"
	"syscall"

	"github.com/StephenSHorton/ww-multiplayer/wwapi/log"
)

// DefaultPort is the server port used when the user doesn't pass --port
// (host/server/join) or an explicit :port (join).
const DefaultPort = 25565

// ShareAddrs turns the host's IPs into what a joiner should type: the bare
// IP on the default port (join adds it), ip:port otherwise, so a joiner
// doesn't silently dial the default port and retry forever. IPv6 literals
// are bracketed.
func ShareAddrs(ips []string, port int) []string {
	if port == DefaultPort {
		return ips
	}
	out := make([]string, len(ips))
	for i, ip := range ips {
		out[i] = net.JoinHostPort(ip, strconv.Itoa(port))
	}
	return out
}

// CLIMultiplayerContext returns a cancellable context wired to SIGINT/SIGTERM.
// Only the CLI entry points should use this — the TUI installs its own
// Ctrl+C handling via Bubble Tea's KeyMsg, and a competing signal.Notify
// would race with it.
//
// Only the FIRST signal is caught. The handler is then removed, so a second
// Ctrl+C gets the default behavior and kills the process at once — the way
// out if teardown is ever stuck.
func CLIMultiplayerContext(rep log.Reporter) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(context.Background())
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	go func() {
		select {
		case <-sigCh:
			signal.Stop(sigCh)
			rep.Log(log.Info, "Shutting down... (press Ctrl+C again to force quit)")
			cancel()
		case <-ctx.Done():
			signal.Stop(sigCh)
		}
	}()
	return ctx, cancel
}

// ListHostIPs walks the machine's non-loopback IPv4 addresses so `ww-multiplayer.exe
// host` can print something the joiner can type. Skips IPv6 (users don't
// want to type v6 literals), loopback (can't reach from another machine),
// and link-local.
func ListHostIPs() []string {
	var out []string
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return nil
	}
	for _, a := range addrs {
		ipnet, ok := a.(*net.IPNet)
		if !ok {
			continue
		}
		ip := ipnet.IP.To4()
		if ip == nil {
			continue
		}
		if ip.IsLoopback() || ip.IsLinkLocalUnicast() {
			continue
		}
		out = append(out, ip.String())
	}
	return out
}
