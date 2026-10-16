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
	Addr  string `json:"addr"`
	Label string `json:"label"`
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
}

// LobbyView is State.Lobby.
type LobbyView struct {
	Member  int          `json:"member"`
	Host    bool         `json:"host"`
	Phase   string       `json:"phase"`
	Legacy  bool         `json:"legacy"`
	Members []MemberView `json:"members"`
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
	Addr      string       `json:"addr"`
	EndReason string       `json:"endReason"`
	Failed    bool         `json:"failed"`
	Lobby     *LobbyView   `json:"lobby"`
	Players   []PlayerView `json:"players"`
	Count     int          `json:"playerCount"`
	HostAddrs []string     `json:"hostAddrs"`
	LatencyMs int64        `json:"latencyMs"`
}

// EventView is one controller event. Kind is one of the eventKinds values.
type EventView struct {
	Kind    string `json:"kind"`
	Session uint64 `json:"session"`
	Time    string `json:"time"` // RFC 3339, UTC
	Level   string `json:"level,omitempty"`
	From    string `json:"from,omitempty"`
	Text    string `json:"text,omitempty"`
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

func stateView(st session.State, running bool) StateView {
	v := StateView{
		Session:   uint64(st.Session),
		Phase:     st.Phase.String(),
		Running:   running,
		Role:      string(st.Role),
		Name:      st.Name,
		Addr:      st.Addr,
		EndReason: st.EndReason,
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
		lv := &LobbyView{Member: l.Member, Host: l.Host, Phase: string(l.Phase), Legacy: l.Legacy, Members: []MemberView{}}
		for _, m := range l.Members {
			lv.Members = append(lv.Members, MemberView{
				ID: m.ID, Name: m.Name, Color: m.Color.Hex(), ColorName: colorName(m.Color),
				Ready: m.Ready, Host: m.Host, Self: m.ID == l.Member && m.Via == session.ViaApp,
				Via: m.Via, Away: m.Away, Dolphin: m.Dolphin, RTTMs: ms(m.RTT),
			})
		}
		v.Lobby = lv
	}
	return v
}

func eventView(ev session.Event) EventView {
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
	return v
}
