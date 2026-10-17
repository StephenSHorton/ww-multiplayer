package gui

import (
	"time"

	"github.com/StephenSHorton/ww-multiplayer/internal/session"
	"github.com/StephenSHorton/ww-multiplayer/mods/multiplayer/playercolor"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/log"
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
	Masked string `json:"masked"` // Addr masked, always (the page picks one; mask.go)
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
	// DolphinLabel is the Dolphin chip's text ("" for none: a CLI
	// player's row before its first pose).
	DolphinLabel string `json:"dolphinLabel"`
	// Area is "near" (same area), "far" or "" (unknown, or this player)
	// once in game, from the pose positions; Dist is the distance in
	// world units.
	Area  string `json:"area"`
	Dist  int    `json:"dist"`
	RTTMs int64  `json:"rttMs"`
	// File is the member's save-file hint (co-op): "File 2 “Link”:
	// bound".
	File string `json:"file,omitempty"`
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
	// CanStart says the host's Start button works (we host, in the
	// lobby phase); NotReady names who isn't ready yet: Start waits for
	// them, "Start anyway" doesn't.
	CanStart bool     `json:"canStart"`
	NotReady []string `json:"notReady"`
}

// PlayerView is one other player of a running session (Live.Players).
type PlayerView struct {
	Name  string `json:"name"`
	Color string `json:"color"` // "" while unknown
}

// StateView is the controller's Snapshot.
type StateView struct {
	Session    uint64       `json:"session"`
	Phase      string       `json:"phase"` // session.Phase.String()
	Running    bool         `json:"running"`
	Role       string       `json:"role"`
	Name       string       `json:"name"`
	Addr       string       `json:"addr"`       // for a retry only: never drawn (mask.go)
	AddrMasked string       `json:"addrMasked"` // Addr masked, always (the page picks one; mask.go)
	EndReason  string       `json:"endReason"`
	Failed     bool         `json:"failed"`
	Lobby      *LobbyView   `json:"lobby"`
	Players    []PlayerView `json:"players"`
	Count      int          `json:"playerCount"`
	HostAddrs  []string     `json:"hostAddrs"`
	LatencyMs  int64        `json:"latencyMs"`
	// Dialog is the dialog the state calls for (a join error, a kick, a
	// host that can't be reached); nil for none.
	Dialog *DialogView `json:"dialog"`
	// Banner is the reconnect banner; nil while the lobby connection is
	// fine.
	Banner *BannerView `json:"banner"`
	// ShowIPs is the switch this view was masked under (InfoView.ShowIPs).
	ShowIPs bool `json:"showIPs"`
	// Dolphin is this player's Dolphin once the host pressed Start.
	Dolphin GameDolphinView `json:"dolphin"`
	// Map is the Session screen's minimap; nil outside a lobby session.
	Map *MapView `json:"map"`
	// Color is this player's tunic color ("#RRGGBB", from its lobby row;
	// "" while unknown).
	Color string `json:"color"`
	// StartDolphinMyself is the saved "I'll start Dolphin myself"
	// (Bridge.SetStartDolphinMyself).
	StartDolphinMyself bool `json:"startDolphinMyself"`
	// Coop is the session's co-op side (coop.go); nil outside a lobby
	// session.
	Coop *CoopView `json:"coop"`
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
	// FromMasked is From with its IPs masked, set as TextMasked is: a
	// peer's name may be an IP.
	FromMasked string `json:"fromMasked,omitempty"`
	// Sync marks a log line of the sync feed: a co-op line users care
	// about (coop.go's syncLine); the All logs tab shows every line.
	Sync bool `json:"sync,omitempty"`
	// Lobby replies.
	Code    string `json:"code,omitempty"`    // lobby-notice
	Suggest string `json:"suggest,omitempty"` // lobby-notice: a free name
	// Patch is a "patch" event's progress (the setup's, not the
	// controller's: Bridge.emit).
	Patch *PatchView `json:"patch,omitempty"`
	// A "mod" event's mod, and its new status when that is what changed
	// ("" for one of the mod's own events, whose name is Text). The Mods
	// page reads them again with Bridge.Mods.
	Mod    string `json:"mod,omitempty"`
	Status string `json:"status,omitempty"`
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
	session.EventMod:            "mod",
}

var levelNames = map[log.Level]string{
	log.Info: "info",
	log.Net:  "net",
	log.Warn: "warn",
	log.Err:  "err",
	log.OK:   "ok",
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
		Session:    uint64(st.Session),
		Phase:      st.Phase.String(),
		Running:    running,
		Role:       string(st.Role),
		Name:       st.Name,
		Addr:       st.Addr,
		AddrMasked: hideIPs.addr(st.Addr),
		EndReason:  mk.text(st.EndReason),
		ShowIPs:    mk.show,
		Failed:     st.Failed,
		Players:    []PlayerView{},
		Count:      st.PlayerCount,
		HostAddrs:  append([]string{}, st.HostAddrs...),
		LatencyMs:  ms(st.Latency),
	}
	// Peers choose their names, and one may be an IP ("26.12.34.56", the
	// streamer's own, say): masked as free text. This player's own name
	// (Name, its own lobby row) is not.
	for _, p := range st.Players {
		pv := PlayerView{Name: mk.text(p.Name)}
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
		lv.CanStart = canStart(l, running)
		lv.NotReady = []string{}
		for _, m := range l.NotReady() {
			lv.NotReady = append(lv.NotReady, mk.text(m.Name))
		}
		for _, m := range l.Members {
			self := m.ID == l.Member && m.Via == session.ViaApp
			name := m.Name
			if !self {
				name = mk.text(name)
			}
			mv := MemberView{
				ID: m.ID, Name: name, Color: m.Color.Hex(), ColorName: colorName(m.Color),
				Ready: m.Ready, Host: m.Host, Self: self,
				Via: m.Via, Away: m.Away, Dolphin: m.Dolphin, DolphinLabel: dolphinLabel(m.Dolphin), RTTMs: ms(m.RTT), File: m.File,
				CanKick: l.Host && !self && running,
			}
			if self {
				v.Color = mv.Color
				if st.Dolphin.State != "" { // ours is fresher than the lobby's echo of it
					mv.Dolphin, mv.DolphinLabel = string(st.Dolphin.State), dolphinLabel(string(st.Dolphin.State))
				}
			} else {
				mv.Area, mv.Dist = area(st, m.Name)
			}
			lv.Members = append(lv.Members, mv)
		}
		v.Lobby = lv
		if !l.Legacy {
			v.Map = mapView(st, v.Color, mk)
		}
	}
	v.Dolphin = dolphinView(st.Dolphin, mk)
	v.Dialog = dialogFor(st, running, version, mk)
	if v.Dialog == nil {
		v.Dialog = gameDialog(st, running, mk)
	}
	if v.Dialog == nil {
		v.Dialog = coopDialog(st, running, mk)
	}
	v.Banner = bannerFor(st, running, now)
	if v.Banner == nil {
		v.Banner = dolphinBanner(st, running)
	}
	if v.Banner == nil {
		v.Banner = coopBanner(st, running)
	}
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
		v.Sync = syncLine(ev.Level, ev.Text)
	case session.EventChat, session.EventLobbyRequest:
		v.From = ev.From
	case session.EventLobbyNotice:
		v.Code, v.Text, v.Suggest = ev.Notice.Code, ev.Notice.Text, ev.Notice.Suggest
	case session.EventQuestion, session.EventQuestionClosed:
		// The dialog comes from the state (coop.go); the title is enough
		// for the log.
		v.Text = ev.Question.Title
	case session.EventAdvice:
		v.Text = ev.Advice.Text
	case session.EventMod:
		v.Mod = ev.Mod.ID
		if ev.Payload == nil { // a status change, not one of the mod's events
			v.Status = ev.Mod.State
		}
	}
	// The log and chat panels show no IP address unless the switch is on
	// (mask.go): the host's log lists its share addresses, a joiner's
	// names the host.
	v.Text, v.TextMasked = maskPair(v.Text, mk)
	v.From, v.FromMasked = maskPair(v.From, mk)
	return v
}

// maskPair is s as the window draws it under mk, and, when mk lets an IP
// through, s masked beside it (EventView.TextMasked).
func maskPair(s string, mk masker) (shown, masked string) {
	m := hideIPs.text(s)
	if mk.show && m != s {
		return s, m
	}
	return m, ""
}
