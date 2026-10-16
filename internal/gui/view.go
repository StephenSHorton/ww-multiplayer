package gui

import (
	"time"

	"github.com/StephenSHorton/ww-multiplayer/internal/playercolor"
	"github.com/StephenSHorton/ww-multiplayer/internal/report"
	"github.com/StephenSHorton/ww-multiplayer/internal/session"
)

// The JSON shapes the frontend reads. They are the controller's State and
// Event flattened into plain fields (colors as "#RRGGBB", durations as
// milliseconds, enums as strings), so the JS needs no knowledge of Go types.

// SwatchView is one palette color.
type SwatchView struct {
	Name string `json:"name"`
	Hex  string `json:"hex"`
	// Classic marks the vanilla green tunic (playercolor.IsVanilla).
	Classic bool `json:"classic"`
}

// RecentHostView is one of the config's recent hosts.
type RecentHostView struct {
	Addr   string `json:"addr"`   // for Join only: never drawn (mask.go)
	Masked string `json:"masked"` // Addr as the window shows it
	Label  string `json:"label"`  // the saved label, else LAN / Radmin VPN / ...
}

// InfoView is what the frontend loads once: the build, the palette and
// the form defaults from the config.
type InfoView struct {
	Version     string           `json:"version"`
	OS          string           `json:"os"`
	Palette     []SwatchView     `json:"palette"`
	Name        string           `json:"name"`
	Color       string           `json:"color"` // "#RRGGBB", or "" for none (green)
	Port        int              `json:"port"`
	RecentHosts []RecentHostView `json:"recentHosts"`
	MaxName     int              `json:"maxName"`
	// ShowIPs is the "Show IP addresses" switch (config ui.showIPs): on,
	// nothing is masked, and the per-address Show buttons go away.
	ShowIPs bool `json:"showIPs"`
	// DownloadsURL is the releases page (the version dialog's Open
	// downloads, in a browser).
	DownloadsURL string `json:"downloadsUrl"`
	// Warnings are the config's load warnings, for the UI to show once.
	Warnings []string `json:"warnings,omitempty"`
}

// MemberView is one lobby row.
type MemberView struct {
	ID        int    `json:"id"`
	Name      string `json:"name"`
	Color     string `json:"color"`
	ColorName string `json:"colorName"`
	Ready     bool   `json:"ready"`
	Host      bool   `json:"host"`
	Self      bool   `json:"self"`
	Via       string `json:"via"`
	Away      bool   `json:"away"`
	Dolphin   string `json:"dolphin"`
	RTTMs     int64  `json:"rttMs"`
	// CanKick: we host, and the row isn't ours.
	CanKick bool `json:"canKick"`
}

// LobbyView is State.Lobby.
type LobbyView struct {
	Member  int          `json:"member"`
	Host    bool         `json:"host"`
	Phase   string       `json:"phase"`
	Legacy  bool         `json:"legacy"`
	Members []MemberView `json:"members"`
	// Coop is the host's co-op setting ("off" until the app's co-op,
	// design PR 9).
	Coop string   `json:"coop"`
	Link LinkView `json:"link"`
	// CanStart says the host's Start button works. Starting the game
	// from the app is design PR 8, so it is false for now.
	CanStart bool `json:"canStart"`
}

// PlayerView is one other player of a running session (Live.Players).
type PlayerView struct {
	Name  string `json:"name"`
	Color string `json:"color"` // "" while unknown
}

// StateView is the controller's Snapshot.
type StateView struct {
	Session   uint64       `json:"session"`
	Phase     string       `json:"phase"` // session.Phase.String()
	Running   bool         `json:"running"`
	Role      string       `json:"role"`
	Name      string       `json:"name"`
	Addr      string       `json:"addr"`       // for a retry only: never drawn (mask.go)
	AddrShown string       `json:"addrMasked"` // Addr as the window shows it
	EndReason string       `json:"endReason"`
	Failed    bool         `json:"failed"`
	Lobby     *LobbyView   `json:"lobby"`
	Players   []PlayerView `json:"players"`
	Count     int          `json:"playerCount"`
	HostAddrs []string     `json:"hostAddrs"`
	LatencyMs int64        `json:"latencyMs"`
	// Dialog is the dialog the state calls for (a join error, a kick, a
	// host that can't be reached); nil for none.
	Dialog *DialogView `json:"dialog"`
	// Banner is the reconnect banner; nil while the lobby connection is
	// fine.
	Banner *BannerView `json:"banner"`
	// ShowIPs is the switch this view was masked under (InfoView.ShowIPs).
	ShowIPs bool `json:"showIPs"`
}

// EventView is one controller event. Kind is one of the eventKinds values.
type EventView struct {
	Kind    string `json:"kind"`
	Session uint64 `json:"session"`
	Time    string `json:"time"` // RFC 3339, UTC
	Level   string `json:"level,omitempty"`
	From    string `json:"from,omitempty"`
	Text    string `json:"text,omitempty"`
	// TextMasked is Text with its IPs masked, set only when the "Show IP
	// addresses" switch let Text through unmasked and they differ: the
	// log keeps it so turning the switch off masks the line again.
	TextMasked string `json:"textMasked,omitempty"`
	// Lobby replies.
	Code    string `json:"code,omitempty"`    // lobby-notice
	Suggest string `json:"suggest,omitempty"` // lobby-notice: a free name
	// Patch is a "patch" event's progress (the setup's, not the
	// controller's: Bridge.emit).
	Patch *PatchView `json:"patch,omitempty"`
}

// eventKinds names each controller event kind for the frontend.
var eventKinds = map[session.EventKind]string{
	session.EventLog:            "log",
	session.EventChat:           "chat",
	session.EventState:          "state",
	session.EventEnded:          "ended",
	session.EventQuestion:       "question",
	session.EventQuestionClosed: "question-closed",
	session.EventAdvice:         "advice",
	session.EventLobbyNotice:    "lobby-notice",
	session.EventLobbyStart:     "lobby-start",
	session.EventLobbyGo:        "lobby-go",
	session.EventLobbyRequest:   "lobby-request",
	session.EventLegacyHost:     "legacy-host",
}

var levelNames = map[report.Level]string{
	report.Info: "info",
	report.Net:  "net",
	report.Warn: "warn",
	report.Err:  "err",
	report.OK:   "ok",
}

func paletteView() []SwatchView {
	var out []SwatchView
	for _, n := range playercolor.Palette() {
		out = append(out, SwatchView{Name: n.Name, Hex: n.Color.Hex(), Classic: playercolor.IsVanilla(n.Color)})
	}
	return out
}

func colorName(c playercolor.Color) string {
	if n, ok := playercolor.Name(c); ok {
		return n
	}
	return c.Hex()
}

func ms(d time.Duration) int64 { return d.Milliseconds() }

// stateView is st for the frontend; version is this build's (the version
// dialog names it) and now the time the reconnect banner measures from.
func stateView(st session.State, running bool, version string, now time.Time, mk masker) StateView {
	v := StateView{
		Session:   uint64(st.Session),
		Phase:     st.Phase.String(),
		Running:   running,
		Role:      string(st.Role),
		Name:      st.Name,
		Addr:      st.Addr,
		AddrShown: mk.addr(st.Addr),
		EndReason: mk.text(st.EndReason),
		ShowIPs:   mk.show,
		Failed:    st.Failed,
		Players:   []PlayerView{},
		Count:     st.PlayerCount,
		HostAddrs: append([]string{}, st.HostAddrs...),
		LatencyMs: ms(st.Latency),
	}
	for _, p := range st.Players {
		pv := PlayerView{Name: p.Name}
		if p.HasColor {
			pv.Color = p.Color.Hex()
		}
		v.Players = append(v.Players, pv)
	}
	if l := st.Lobby; l != nil {
		lv := &LobbyView{Member: l.Member, Host: l.Host, Phase: string(l.Phase), Legacy: l.Legacy, Members: []MemberView{},
			Coop: l.Settings.Coop, Link: LinkView{State: string(l.Link.State), Attempts: l.Link.Attempts, Err: mk.text(l.Link.Err)}}
		if lv.Coop == "" {
			lv.Coop = "off"
		}
		for _, m := range l.Members {
			self := m.ID == l.Member && m.Via == session.ViaApp
			lv.Members = append(lv.Members, MemberView{
				ID: m.ID, Name: m.Name, Color: m.Color.Hex(), ColorName: colorName(m.Color),
				Ready: m.Ready, Host: m.Host, Self: self,
				Via: m.Via, Away: m.Away, Dolphin: m.Dolphin, RTTMs: ms(m.RTT),
				CanKick: l.Host && !self && running,
			})
		}
		v.Lobby = lv
	}
	v.Dialog = dialogFor(st, running, version, mk)
	v.Banner = bannerFor(st, running, now)
	return v
}

func eventView(ev session.Event, mk masker) EventView {
	kind, ok := eventKinds[ev.Kind]
	if !ok {
		kind = "unknown"
	}
	v := EventView{Kind: kind, Session: uint64(ev.Session), Time: ev.Time.UTC().Format(time.RFC3339Nano), Text: ev.Text}
	switch ev.Kind {
	case session.EventLog:
		v.Level = levelNames[ev.Level]
	case session.EventChat, session.EventLobbyRequest:
		v.From = ev.From
	case session.EventLobbyNotice:
		v.Code, v.Text, v.Suggest = ev.Notice.Code, ev.Notice.Text, ev.Notice.Suggest
	case session.EventQuestion, session.EventQuestionClosed:
		// Co-op questions are answered in a later PR (design PR 9); the
		// title is enough for the log.
		v.Text = ev.Question.Title
	}
	// The log and chat panels show no IP address unless the switch is on
	// (mask.go): the host's log lists its share addresses, a joiner's
	// names the host.
	if masked := hideIPs.text(v.Text); mk.show && masked != v.Text {
		v.TextMasked = masked
	} else {
		v.Text = masked
	}
	return v
}
