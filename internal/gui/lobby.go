package gui

import (
	"errors"
	"fmt"
	"net"
	"net/netip"
	"sort"
	"strconv"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/internal/config"
	"github.com/StephenSHorton/ww-multiplayer/internal/netutil"
	"github.com/StephenSHorton/ww-multiplayer/internal/session"
	"github.com/StephenSHorton/ww-multiplayer/mods/multiplayer/net"
)

// The Lobby screen's Go side (App PR 7, design §2.2-§2.4, §2.9): the
// dialogs a join error or a lobby event opens, the reconnect banner, and
// the host's share card. They are computed here, from the controller's
// State, so the frontend only shows them and the table of causes is
// tested in Go (lobby_test.go).

// DownloadsURL is the releases page the version dialog opens.
const DownloadsURL = "https://github.com/StephenSHorton/ww-multiplayer/releases"

// EnvListenHost makes a hosted lobby's server listen on one address
// instead of every interface ("127.0.0.1" for a local check that must not
// open a port on the network). The share card still lists this machine's
// addresses.
const EnvListenHost = "WW_LISTEN_HOST"

// Dialog kinds (DialogView.Kind).
const (
	dlgVersion     = "version"     // RejectVersion: the two builds differ
	dlgMods        = "mods"        // RejectMods or NoticeModsDiffer: the client_required mods differ
	dlgNameTaken   = "name_taken"  // the lobby has a member of that name
	dlgBadName     = "bad_name"    // RejectBadName
	dlgFull        = "full"        // the lobby or the server is full
	dlgKicked      = "kicked"      // the host removed us
	dlgReplaced    = "replaced"    // a newer connection took our slot
	dlgLegacy      = "legacy"      // a pre-lobby host: joined directly
	dlgUnreachable = "unreachable" // the first connect keeps failing
	dlgHostFailed  = "host_failed" // hosting couldn't start (the port, mostly)
	dlgFailed      = "failed"      // any other error that ended the session
)

// Dialog action IDs (DialogAction.ID); the frontend runs each.
const (
	actOK            = "ok"             // close the dialog
	actDownloads     = "downloads"      // open DownloadsURL
	actCopyVersion   = "copy-version"   // copy this build's version
	actJoinSuggested = "join-suggested" // join again as DialogView.Suggest
	actChangeName    = "change-name"    // Home, with the name field in focus
	actOpenMods      = "open-mods"      // the Mods page (Back returns Home)
	actRetry         = "retry"          // Bridge.Retry
	actLeave         = "leave"          // Bridge.Leave, then Home
)

// DialogAction is one button of a dialog.
type DialogAction struct {
	ID string `json:"id"`
	// Arg is the action's argument (a co-op question's ID, ...).
	Arg     string `json:"arg,omitempty"`
	Label   string `json:"label"`
	Primary bool   `json:"primary,omitempty"`
}

// DialogView is a dialog the frontend shows over the screen.
type DialogView struct {
	// Key names this dialog in this session: the frontend shows a key
	// once, and keeps it closed after the user closed it.
	Key   string `json:"key"`
	Kind  string `json:"kind"`
	Title string `json:"title"`
	Text  string `json:"text"`
	// Detail is the cause's own words (the host's reason, the last
	// connect error), shown small under Text.
	Detail string `json:"detail,omitempty"`
	// Suggest is a free name (name_taken).
	Suggest string `json:"suggest,omitempty"`
	// Live dialogs follow the state: the frontend closes one once the
	// state no longer has it (the host was reached after all).
	Live bool `json:"live,omitempty"`
	// Home says the frontend goes back to Home under the dialog: the
	// session is over.
	Home    bool           `json:"home,omitempty"`
	Actions []DialogAction `json:"actions"`

	// Co-op dialogs (coop.go) add: List, a short list under Text (a
	// bind's preflight, a scrub's failed checks, the founding's steps);
	// Cards, option cards with steps and a button of their own; Check, a
	// live check line; Note with its NoteAction button ("Open folder");
	// Cancel, the action Escape runs (a question's no); AutoYes, the
	// "Answer yes to co-op questions" box.
	List       []string      `json:"list,omitempty"`
	Cards      []DialogCard  `json:"cards,omitempty"`
	Check      *CheckView    `json:"check,omitempty"`
	Note       string        `json:"note,omitempty"`
	NoteAction *DialogAction `json:"noteAction,omitempty"`
	Cancel     string        `json:"cancel,omitempty"`
	AutoYes    bool          `json:"autoYes,omitempty"`
}

// BannerView is the strip over the Lobby or Session screen while the
// lobby connection is down after it was up, or this player's Dolphin is
// closed.
type BannerView struct {
	Kind     string `json:"kind"` // "reconnecting", or "dolphin" (game.go)
	Title    string `json:"title"`
	Text     string `json:"text"`
	Attempts int    `json:"attempts"`
	// Action is a button's action ID (DialogAction.ID) and ActionLabel
	// its text; "" for none.
	Action      string `json:"action,omitempty"`
	ActionLabel string `json:"actionLabel,omitempty"`
	// ActionArg is the action's argument (DialogAction.Arg).
	ActionArg string `json:"actionArg,omitempty"`
}

// ShareAddrView is one address of the host's share card.
type ShareAddrView struct {
	Addr   string `json:"addr"`   // what a friend types: ip, or ip:port off the default port; Copy copies it
	Masked string `json:"masked"` // Addr as the card shows it until Show (mask.go)
	Kind   string `json:"kind"`   // "lan", "radmin", "tailscale" or "other"
	Label  string `json:"label"`  // "LAN", "Radmin VPN", "Tailscale", "Other"
	// Code is Addr's join code (netutil.JoinCode; "" for an IPv6 one). It
	// holds the address, so it is masked like one: CodeMasked until Show.
	Code       string `json:"code"`
	CodeMasked string `json:"codeMasked"`
}

// ShareView is the host's share card: the port the session listens on
// (its port-forward hint) and the addresses.
type ShareView struct {
	Port  int             `json:"port"`
	Addrs []ShareAddrView `json:"addrs"`
}

// LinkView is the lobby connection's health (session.LobbyLink).
type LinkView struct {
	State    string `json:"state"` // "connecting", "up" or "reconnecting"
	Attempts int    `json:"attempts"`
	Err      string `json:"err,omitempty"`
}

var (
	radminNet    = netip.MustParsePrefix("26.0.0.0/8")
	tailscaleNet = netip.MustParsePrefix("100.64.0.0/10")
	lanNets      = []netip.Prefix{
		netip.MustParsePrefix("10.0.0.0/8"),
		netip.MustParsePrefix("172.16.0.0/12"),
		netip.MustParsePrefix("192.168.0.0/16"),
	}
	shareOrder = map[string]int{"lan": 0, "radmin": 1, "tailscale": 2, "other": 3, "local": 4}
)

// addrKind labels an IP for the share card (design §2.2).
func addrKind(ip string) (kind, label string) {
	a, err := netip.ParseAddr(ip)
	if err != nil {
		return "other", "Other"
	}
	a = a.Unmap()
	switch {
	case a.IsLoopback():
		// Only a session heard on this machine (two apps, or a local
		// check with WW_LISTEN_HOST=127.0.0.1); the share card lists none.
		return "local", "This computer"
	case radminNet.Contains(a):
		return "radmin", "Radmin VPN"
	case tailscaleNet.Contains(a):
		return "tailscale", "Tailscale"
	}
	for _, n := range lanNets {
		if n.Contains(a) {
			return "lan", "LAN"
		}
	}
	return "other", "Other"
}

// addrLabel is the share-card label (LAN, Radmin VPN, ...) of a join
// address, so a recent host can show it in place of its IP; "" for a
// host name.
func addrLabel(addr string) string {
	host, _, err := net.SplitHostPort(netutil.NormalizeAddr(addr, config.DefaultPort))
	if err != nil {
		return ""
	}
	if _, err := netip.ParseAddr(host); err != nil {
		return ""
	}
	_, label := addrKind(host)
	return label
}

// shareAddrs is the share card for ips and port: each IP labeled, with
// the port added off the default one (as the CLI's host prints them) and
// its join code, LAN first, then Radmin VPN, Tailscale and the rest. Masked is always the
// masked form, whatever the switch says: the page picks one when it draws
// (a reply from before the switch was turned off may come late).
func shareAddrs(ips []string, port int) []ShareAddrView {
	out := []ShareAddrView{}
	for _, ip := range ips {
		addr := ip
		if port != 0 && port != config.DefaultPort {
			addr = net.JoinHostPort(ip, strconv.Itoa(port))
		}
		kind, label := addrKind(ip)
		v := ShareAddrView{Addr: addr, Masked: hideIPs.addr(addr), Kind: kind, Label: label}
		codePort := port
		if codePort == 0 {
			codePort = config.DefaultPort
		}
		if a, err := netip.ParseAddr(ip); err == nil && codePort > 0 && codePort <= 65535 {
			if code, err := netutil.JoinCode(netip.AddrPortFrom(a, uint16(codePort))); err == nil {
				v.Code, v.CodeMasked = code, hideIPs.addr(code)
			}
		}
		out = append(out, v)
	}
	sort.SliceStable(out, func(i, j int) bool { return shareOrder[out[i].Kind] < shareOrder[out[j].Kind] })
	return out
}

// dialogFor is the dialog the state calls for, or nil. version is this
// build's.
func dialogFor(st session.State, running bool, version string, mk masker) *DialogView {
	if st.Session == 0 {
		return nil
	}
	key := func(kind string) string { return fmt.Sprintf("%d:%s", st.Session, kind) }
	l := st.Lobby
	if running {
		switch {
		case l != nil && l.Legacy:
			return &DialogView{Key: key(dlgLegacy), Kind: dlgLegacy, Title: "Older host",
				Text: "This host runs an older ww-multiplayer without a lobby, so you joined it directly. " +
					"Your game connects as soon as Dolphin is running; there is no Ready or Start.",
				Actions: []DialogAction{{ID: actOK, Label: "OK", Primary: true}}}
		case st.Role == session.RoleJoin && l != nil && l.Link.State == session.LinkConnecting && l.Link.Attempts > 0:
			d := &DialogView{Key: key(dlgUnreachable), Kind: dlgUnreachable, Title: "Can't reach the host", Live: true,
				Text: fmt.Sprintf("Couldn't reach the host at %s. Is the host running? Are you on the same network or VPN? Is port %d forwarded?",
					mk.addr(st.Addr), addrPort(st.Addr)),
				Actions: []DialogAction{{ID: actRetry, Label: "Retry", Primary: true}, {ID: actLeave, Label: "Cancel"}}}
			d.Detail = fmt.Sprintf("Still trying in the background. %d failed %s", l.Link.Attempts, plural(l.Link.Attempts, "attempt", "attempts"))
			if l.Link.Err != "" {
				d.Detail += ": " + mk.text(l.Link.Err)
			}
			return d
		}
		return nil
	}
	if !st.Failed || st.Err == nil {
		return nil
	}
	d := endDialog(st.Err, st, version)
	d.Key, d.Home = key(d.Kind), true
	d.Detail = mk.text(d.Detail) // a dial error names the IP
	return d
}

// endDialog is the dialog for the error a session ended on (design §2.3's
// table).
func endDialog(err error, st session.State, version string) *DialogView {
	ok := []DialogAction{{ID: actOK, Label: "OK", Primary: true}}
	var ne *network.LobbyNoticeError
	if errors.As(err, &ne) {
		n := ne.Notice
		switch n.Code {
		case network.NoticeNameTaken:
			d := &DialogView{Kind: dlgNameTaken, Title: "Name taken",
				Text:    fmt.Sprintf("Someone in that lobby is already called “%s”. Everyone needs a different name.", st.Name),
				Actions: []DialogAction{{ID: actChangeName, Label: "Change my name"}}}
			if n.Suggest != "" {
				d.Suggest = n.Suggest
				d.Actions = append([]DialogAction{{ID: actJoinSuggested, Label: fmt.Sprintf("Join as “%s”", n.Suggest), Primary: true}}, d.Actions...)
			} else {
				d.Actions[0].Primary = true
			}
			return d
		case network.NoticeFull:
			return &DialogView{Kind: dlgFull, Title: "Session full", Text: "That session is full.", Detail: n.Text, Actions: ok}
		case network.NoticeModsDiffer:
			return modsDialog(n.Text)
		case network.NoticeKicked:
			return &DialogView{Kind: dlgKicked, Title: "Removed", Text: "The host removed you from the session.", Actions: ok}
		case "replaced":
			return &DialogView{Kind: dlgReplaced, Title: "Joined elsewhere",
				Text: "You joined this lobby again from another window, so this one left it.", Actions: ok}
		}
	}
	var re *network.RejectError
	if errors.As(err, &re) {
		switch re.Code {
		case network.RejectVersion:
			return &DialogView{Kind: dlgVersion, Title: "Different versions", Detail: re.Reason,
				Text:    fmt.Sprintf("Your friend runs a different ww-multiplayer than you (you run %s). Both players need the same version.", version),
				Actions: []DialogAction{{ID: actDownloads, Label: "Open downloads", Primary: true}, {ID: actCopyVersion, Label: "Copy my version"}, {ID: actOK, Label: "Close"}}}
		case network.RejectMods:
			return modsDialog(re.Reason)
		case network.RejectBadName:
			return &DialogView{Kind: dlgBadName, Title: "Name refused", Text: "The host refused your name.", Detail: re.Reason,
				Actions: []DialogAction{{ID: actChangeName, Label: "Change my name", Primary: true}, {ID: actOK, Label: "Close"}}}
		case network.RejectServerFull:
			return &DialogView{Kind: dlgFull, Title: "Session full", Text: "That session is full.", Detail: re.Reason, Actions: ok}
		}
	}
	if st.Role == session.RoleHost {
		return &DialogView{Kind: dlgHostFailed, Title: "Couldn't host",
			Text:   "The session couldn't start. If another program (or another ww-multiplayer) uses the port, pick another port on Home.",
			Detail: st.EndReason, Actions: ok}
	}
	return &DialogView{Kind: dlgFailed, Title: "The session ended", Text: "The session stopped on an error.", Detail: st.EndReason, Actions: ok}
}

// bannerFor is the reconnect banner the state calls for, or nil.
func bannerFor(st session.State, running bool, now time.Time) *BannerView {
	l := st.Lobby
	if !running || l == nil || l.Legacy || l.Link.State != session.LinkReconnecting {
		return nil
	}
	hold := network.LobbyHoldTimeout
	b := &BannerView{Kind: "reconnecting", Title: "Reconnecting…", Attempts: l.Link.Attempts,
		Text: fmt.Sprintf("Lost the connection to the lobby. Your color and ready stay yours for %d seconds while it reconnects.", int(hold/time.Second))}
	if !l.Link.Since.IsZero() && now.Sub(l.Link.Since) > hold {
		b.Text = "Still can't reach the host; they may have closed the session. Still trying: press Leave to stop."
	}
	return b
}

// addrPort is the port a join to addr dials.
func addrPort(addr string) int {
	_, p, err := net.SplitHostPort(netutil.NormalizeAddr(addr, config.DefaultPort))
	if err != nil {
		return config.DefaultPort
	}
	n, err := strconv.Atoi(p)
	if err != nil {
		return config.DefaultPort
	}
	return n
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// modsDialog is the dialog for a host that refused us over mods: the
// reason as the network gives it (the host's list against ours), with
// the Mods page, where a compiled-in mod is turned on or off, as its
// first action.
func modsDialog(reason string) *DialogView {
	return &DialogView{Kind: dlgMods, Title: "Different mods", Text: reason,
		Actions: []DialogAction{{ID: actOpenMods, Label: "Open Mods", Primary: true}, {ID: actOK, Label: "Close"}}}
}
