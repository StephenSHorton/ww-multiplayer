package network

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/StephenSHorton/ww-multiplayer/mods/multiplayer/playercolor"
)

// The lobby's state machine (docs/10-lobby.md). lobbyMachine is pure: no
// sockets, no goroutines, no clock of its own. Each input (a member's
// HELLO, a sub-message, a dropped connection, the hold timer, a change in
// the player connections) returns the frames to send as lobbyOuts, which
// LobbyHub (lobby.go) delivers. That keeps every rule testable and
// fuzzable without a network (FuzzLobbyMachine).

// Lobby limits. They keep STATE under MaxLobbyBody whatever the members
// send.
const (
	// MaxLobbyMembers caps the lobby's members, live and held. A HELLO
	// past it gets NoticeFull and a close.
	MaxLobbyMembers = 24
	// maxCLIRows caps the CLI players STATE lists.
	maxCLIRows = 16
	// maxLobbyWorlds caps the host's world list in STATE.
	maxLobbyWorlds = 16
	// maxLobbyFile caps a member's file hint, maxLobbyDolphin its Dolphin
	// state, in bytes.
	maxLobbyFile    = 80
	maxLobbyDolphin = 16
	// cliMemberBase + a player ID is a CLI row's member ID in STATE (KICK
	// takes it); lobby members count up from 1 and never reach it.
	cliMemberBase = 1 << 16
)

// NoticeFull: the lobby has MaxLobbyMembers members.
const NoticeFull = "full"

// LobbyHoldTimeout is how long the server holds a member's slot (name,
// color, ready, host) after its lobby connection drops, for a HELLO with
// its token. A package var so tests can shorten it; NewServer captures it.
var LobbyHoldTimeout = 60 * time.Second

// coopWorldIDRe is a world ID as LobbySettingsBody.Coop carries it.
var coopWorldIDRe = regexp.MustCompile(`^[0-9a-f]{16}$`)

// lobbyMember is one member of the lobby.
type lobbyMember struct {
	id    int
	name  string
	key   string // lobbyKey(name): names are unique by it
	token string
	color playercolor.Color
	ready bool
	host  bool
	// loopback: its connection came from the host's machine, so it can
	// become host (at HELLO, or when the host's slot is gone) while the
	// lobby has no owner key.
	loopback bool
	// owner: a HELLO of this member carried the lobby's owner key
	// (owner_key.go), so it can become host when the lobby has one. Kept
	// under the token: a reclaim keeps it.
	owner   bool
	dolphin string
	file    string
	app, os string
	rttMs   int

	conn      int       // the live lobby connection's handle; 0 while away
	awayUntil time.Time // while away: when the slot is dropped
}

// lobbyPlayer is one player connection, as the lobby sees it.
type lobbyPlayer struct {
	id    byte
	name  string
	color playercolor.Color
	posed bool // a pose has arrived on it: its Dolphin is in game
}

// lobbyOut is one frame for LobbyHub to deliver.
type lobbyOut struct {
	to   int // a connection handle; lobbyAll for every live member
	sub  byte
	body any
	// closeConn closes connection to after the frame.
	closeConn bool
	// kickName, when set, closes every player connection of that name.
	kickName string
}

// lobbyAll addresses a lobbyOut to every live member.
const lobbyAll = -1

// lobbyMachine is the lobby's state. Not safe for concurrent use: LobbyHub
// serializes it.
type lobbyMachine struct {
	phase    string
	rev      uint64
	settings LobbySettingsBody
	members  []*lobbyMember // in join order
	players  []lobbyPlayer
	nextID   int
	hold     time.Duration
	newToken func() string
	// mods is the host's Mods (Server.SetMods): a HELLO with another
	// ModsHash is refused with NoticeModsDiffer.
	mods Mods
	// goBody is the host's GO, kept for members that (re)join in game.
	goBody *LobbyGoBody
	// kicked are the lobbyKeys the host kicked: refused (lobby, player
	// and co-op connections) until the lobby empties.
	kicked map[string]bool
	// keyed: the server has an owner key (Server.SetOwnerKey). Then only
	// a member that presented it can be host, and loopback counts for
	// nothing (canHost).
	keyed bool
	// noCoop, when set, refuses co-op in SETTINGS with this reason: a
	// dedicated server has no co-op hub to give the host
	// (Server.DenyCoop).
	noCoop string
}

func newLobbyMachine(hold time.Duration) *lobbyMachine {
	return &lobbyMachine{
		phase:    PhaseLobby,
		settings: LobbySettingsBody{Coop: CoopOff},
		hold:     hold,
		newToken: randomToken,
		kicked:   map[string]bool{},
	}
}

// randomToken is a reconnect token: 16 random bytes in hex.
func randomToken() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic("crypto/rand: " + err.Error()) // never fails on supported platforms
	}
	return hex.EncodeToString(b[:])
}

// lobbyKey is the form names are unique by: case-insensitive.
func lobbyKey(name string) string { return strings.ToLower(name) }

func (m *lobbyMachine) member(id int) *lobbyMember {
	for _, mb := range m.members {
		if mb.id == id {
			return mb
		}
	}
	return nil
}

func (m *lobbyMachine) memberByConn(conn int) *lobbyMember {
	if conn <= 0 {
		return nil
	}
	for _, mb := range m.members {
		if mb.conn == conn {
			return mb
		}
	}
	return nil
}

func (m *lobbyMachine) memberByKey(key string) *lobbyMember {
	for _, mb := range m.members {
		if mb.key == key {
			return mb
		}
	}
	return nil
}

// canHost reports whether mb may hold the host role: with an owner key,
// only a member that presented it; without one, a member from this
// machine (docs/10 §3).
func (m *lobbyMachine) canHost(mb *lobbyMember) bool {
	if m.keyed {
		return mb.owner
	}
	return mb.loopback
}

// isReclaim reports whether a HELLO under name with token would reclaim a
// member's slot (hello's reclaim case).
func (m *lobbyMachine) isReclaim(name, token string) bool {
	mb := m.memberByKey(lobbyKey(name))
	return mb != nil && token != "" && token == mb.token && mb.name == name
}

func (m *lobbyMachine) hasHost() bool {
	for _, mb := range m.members {
		if mb.host {
			return true
		}
	}
	return false
}

// cliRows are the player connections with no member of their name, one row
// per name (a CLI player runs two connections under one name), lowest
// player ID first, at most maxCLIRows.
func (m *lobbyMachine) cliRows() []LobbyMember {
	byName := map[string]*LobbyMember{}
	var order []string
	ps := append([]lobbyPlayer(nil), m.players...)
	sort.Slice(ps, func(i, j int) bool { return ps[i].id < ps[j].id })
	for _, p := range ps {
		k := lobbyKey(p.name)
		if m.memberByKey(k) != nil {
			continue
		}
		row, ok := byName[k]
		if !ok {
			if len(order) == maxCLIRows {
				continue
			}
			row = &LobbyMember{ID: cliMemberBase + int(p.id), Name: p.name, Color: p.color.Hex(), Ready: true, Via: ViaCLI}
			byName[k] = row
			order = append(order, k)
		}
		if p.posed {
			row.Dolphin = DolphinInGame
		}
	}
	rows := make([]LobbyMember, 0, len(order))
	for _, k := range order {
		rows = append(rows, *byName[k])
	}
	return rows
}

// snapshot is STATE.
func (m *lobbyMachine) snapshot() LobbyStateBody {
	st := LobbyStateBody{Phase: m.phase, Rev: m.rev, Settings: m.settings, Members: []LobbyMember{}}
	st.Settings.Worlds = append([]LobbyWorld(nil), m.settings.Worlds...)
	for _, mb := range m.members {
		st.Members = append(st.Members, LobbyMember{
			ID: mb.id, Name: mb.name, Color: mb.color.Hex(), Ready: mb.ready, Host: mb.host,
			Dolphin: mb.dolphin, File: mb.file, RTTMs: mb.rttMs, Via: ViaApp, Away: mb.conn == 0,
		})
	}
	st.Members = append(st.Members, m.cliRows()...)
	return st
}

// changed bumps rev and returns STATE for every live member.
func (m *lobbyMachine) changed() lobbyOut {
	m.rev++
	return lobbyOut{to: lobbyAll, sub: LobbyState, body: m.snapshot()}
}

func notice(to int, code, text string) lobbyOut {
	return lobbyOut{to: to, sub: LobbyNotice, body: LobbyNoticeBody{Code: code, Text: text}}
}

// suggestName is a free name for one that is taken: "Name (2)", "Name (3)",
// ..., within MaxNameLen.
func (m *lobbyMachine) suggestName(name string) string {
	for n := 2; n < 1000; n++ {
		suffix := fmt.Sprintf(" (%d)", n)
		base := name
		for len(base)+len(suffix) > MaxNameLen {
			_, size := utf8.DecodeLastRuneInString(base)
			base = base[:len(base)-size]
		}
		if cand := base + suffix; m.memberByKey(lobbyKey(cand)) == nil {
			return cand
		}
	}
	return ""
}

// hello admits the connection conn, whose join named name, as a member.
// loopback says the connection comes from this machine: the first such
// member becomes the host. It returns the member (nil when refused) and
// the frames to send.
func (m *lobbyMachine) hello(conn int, name string, loopback bool, h LobbyHelloBody, now time.Time) (*lobbyMember, []lobbyOut) {
	return m.helloAs(conn, name, loopback, false, h, now)
}

// helloAs is hello with owner: the hub accepted the HELLO's owner key
// (checkOwnerKeyLocked). With the lobby keyed, the first such member
// becomes the host instead of the first from this machine. A reclaim
// keeps the member's owner flag (owner false never clears it).
func (m *lobbyMachine) helloAs(conn int, name string, loopback, owner bool, h LobbyHelloBody, now time.Time) (*lobbyMember, []lobbyOut) {
	key := lobbyKey(name)
	var outs []lobbyOut
	mb := m.memberByKey(key)
	switch {
	case m.kicked[key]:
		return nil, []lobbyOut{{to: conn, sub: LobbyNotice, closeConn: true, body: LobbyNoticeBody{
			Code: NoticeKicked, Text: kickedText}}}
	case !modsMatchHex(m.mods, h.ModsHash):
		return nil, []lobbyOut{{to: conn, sub: LobbyNotice, closeConn: true, body: LobbyNoticeBody{
			Code: NoticeModsDiffer, Text: modsDifferReason(m.mods.List)}}}
	case mb != nil && h.Token != "" && h.Token == mb.token && mb.name == name:
		// Reclaim: the held slot, or a live one whose old connection the
		// server hasn't noticed is gone yet (it is closed now).
		if mb.conn != 0 && mb.conn != conn {
			outs = append(outs, lobbyOut{to: mb.conn, sub: LobbyNotice,
				body: LobbyNoticeBody{Code: "replaced", Text: "this lobby connection was replaced by a newer one"}, closeConn: true})
		}
		mb.conn, mb.awayUntil = conn, time.Time{}
		mb.loopback = loopback
		mb.owner = mb.owner || owner
		if h.Dolphin != "" {
			// Its Dolphin may have changed while the link was down (the
			// LOBBY_SET was lost): the HELLO carries the current state.
			mb.dolphin = clampField(h.Dolphin, maxLobbyDolphin)
		}
		if m.canHost(mb) && !m.hasHost() {
			mb.host = true // the host's slot expired while this one was away
		}
	case mb != nil:
		sug := m.suggestName(name)
		return nil, []lobbyOut{{to: conn, sub: LobbyNotice, closeConn: true, body: LobbyNoticeBody{
			Code: NoticeNameTaken, Text: fmt.Sprintf("the name %q is taken in this lobby; pick another", name), Suggest: sug}}}
	case len(m.members) >= MaxLobbyMembers:
		return nil, []lobbyOut{{to: conn, sub: LobbyNotice, closeConn: true, body: LobbyNoticeBody{
			Code: NoticeFull, Text: fmt.Sprintf("the lobby is full (%d members)", MaxLobbyMembers)}}}
	default:
		m.nextID++
		mb = &lobbyMember{id: m.nextID, name: name, key: key, token: m.newToken(), conn: conn,
			loopback: loopback, owner: owner, dolphin: clampField(h.Dolphin, maxLobbyDolphin)}
		mb.host = m.canHost(mb) && !m.hasHost()
		// The color asked for, whoever else wears it; classic green
		// without one (docs/10 §4).
		mb.color = playercolor.Vanilla()
		if h.Color != "" {
			if c, err := playercolor.Parse(h.Color); err == nil {
				mb.color = c
			}
		}
		m.members = append(m.members, mb)
	}
	mb.app, mb.os = clampField(h.App, 32), clampField(h.OS, 16)
	welcome := lobbyOut{to: conn, sub: LobbyWelcome, body: LobbyWelcomeBody{
		Member: mb.id, Token: mb.token, Host: mb.host, Phase: m.phase, Features: []string{"chat", "kick", "request"}}}
	outs = append([]lobbyOut{welcome}, outs...)
	outs = append(outs, m.changed())
	// A member that joins, or reclaims its slot, after START missed the
	// relayed START and GO: it gets them now, after STATE as live members
	// do. After a reclaim that can be a repeat; START and GO are
	// idempotent for their receivers.
	if m.phase != PhaseLobby {
		outs = append(outs, lobbyOut{to: conn, sub: LobbyStart, body: LobbyStartBody{Settings: m.settingsCopy()}})
	}
	if m.phase == PhaseInGame && m.goBody != nil {
		outs = append(outs, lobbyOut{to: conn, sub: LobbyGo, body: *m.goBody})
	}
	return mb, outs
}

// kickedText is the kicked notice's text, and the reason of the player and
// co-op refusal of a kicked name.
const kickedText = "the host removed you from this session"

// clampField keeps a free-text field printable and short: control
// characters dropped, invalid UTF-8 replaced, cut to max bytes on a rune
// boundary.
func clampField(s string, max int) string {
	s = strings.ToValidUTF8(s, "�")
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, s)
	for len(s) > max {
		_, size := utf8.DecodeLastRuneInString(s)
		s = s[:len(s)-size]
	}
	return s
}

// message handles one sub-message from the member on connection conn.
func (m *lobbyMachine) message(conn int, sub byte, body []byte) []lobbyOut {
	mb := m.memberByConn(conn)
	if mb == nil {
		return []lobbyOut{notice(conn, NoticeHelloFirst, "send HELLO first")}
	}
	switch sub {
	case LobbySet:
		var b LobbySetBody
		if err := DecodeLobby(body, &b); err != nil {
			return []lobbyOut{notice(conn, NoticeBadMessage, "SET: "+err.Error())}
		}
		return m.set(mb, b)
	case LobbySettings:
		var b LobbySettingsBody
		if err := DecodeLobby(body, &b); err != nil {
			return []lobbyOut{notice(conn, NoticeBadMessage, "SETTINGS: "+err.Error())}
		}
		return m.setSettings(mb, b)
	case LobbyStart:
		if !mb.host {
			return []lobbyOut{notice(conn, NoticeNotHost, "only the host can start the session")}
		}
		if m.phase != PhaseLobby {
			return []lobbyOut{notice(conn, NoticeWrongPhase, "the session has started already")}
		}
		m.phase = PhaseStarting
		start := lobbyOut{to: lobbyAll, sub: LobbyStart, body: LobbyStartBody{Settings: m.settingsCopy()}}
		return []lobbyOut{m.changed(), start}
	case LobbyGo:
		var b LobbyGoBody
		if err := DecodeLobby(body, &b); err != nil {
			return []lobbyOut{notice(conn, NoticeBadMessage, "GO: "+err.Error())}
		}
		if !mb.host {
			return []lobbyOut{notice(conn, NoticeNotHost, "only the host can send GO")}
		}
		if m.phase != PhaseStarting {
			return []lobbyOut{notice(conn, NoticeWrongPhase, "GO comes after START")}
		}
		if b.World != "" && !coopWorldIDRe.MatchString(b.World) {
			return []lobbyOut{notice(conn, NoticeBadMessage, "GO: bad world ID")}
		}
		m.phase = PhaseInGame
		m.goBody = &b
		return []lobbyOut{m.changed(), {to: lobbyAll, sub: LobbyGo, body: b}}
	case LobbyRequest:
		var b LobbyRequestBody
		if err := DecodeLobby(body, &b); err != nil || b.Kind != "merge" {
			return []lobbyOut{notice(conn, NoticeBadMessage, "REQUEST: want {\"kind\":\"merge\"}")}
		}
		for _, h := range m.members {
			if h.host && h.conn != 0 {
				return []lobbyOut{{to: h.conn, sub: LobbyRequest, body: LobbyRequestBody{Kind: b.Kind, From: mb.name}}}
			}
		}
		return []lobbyOut{notice(conn, "no_host", "the host is not in the lobby right now")}
	case LobbyKick:
		var b LobbyKickBody
		if err := DecodeLobby(body, &b); err != nil {
			return []lobbyOut{notice(conn, NoticeBadMessage, "KICK: "+err.Error())}
		}
		if !mb.host {
			return []lobbyOut{notice(conn, NoticeNotHost, "only the host can kick")}
		}
		return m.kick(mb, b.Member)
	}
	// HELLO again, server-to-client subs, unknown subs: ignored, so a
	// later client can send more.
	return nil
}

func (m *lobbyMachine) settingsCopy() LobbySettingsBody {
	s := m.settings
	s.Worlds = append([]LobbyWorld(nil), m.settings.Worlds...)
	return s
}

func (m *lobbyMachine) set(mb *lobbyMember, b LobbySetBody) []lobbyOut {
	var outs []lobbyOut
	dirty := false
	if b.Color != nil {
		want, err := playercolor.Parse(*b.Color)
		switch {
		case err != nil:
			outs = append(outs, notice(mb.conn, NoticeBadMessage, "SET color: "+err.Error()))
		case m.phase != PhaseLobby:
			if want != mb.color {
				outs = append(outs, notice(mb.conn, NoticeColorsLocked, "colors are locked once the session has started"))
			}
		case want != mb.color:
			// Granted as asked, even when another member wears it.
			mb.color, dirty = want, true
		}
	}
	if b.Ready != nil && *b.Ready != mb.ready {
		mb.ready, dirty = *b.Ready, true
	}
	if b.Dolphin != nil {
		if d := clampField(*b.Dolphin, maxLobbyDolphin); d != mb.dolphin {
			mb.dolphin, dirty = d, true
		}
	}
	if b.File != nil {
		if f := clampField(*b.File, maxLobbyFile); f != mb.file {
			mb.file, dirty = f, true
		}
	}
	if dirty {
		outs = append(outs, m.changed())
	}
	return outs
}

func (m *lobbyMachine) setSettings(mb *lobbyMember, b LobbySettingsBody) []lobbyOut {
	if !mb.host {
		return []lobbyOut{notice(mb.conn, NoticeNotHost, "only the host sets co-op")}
	}
	if b.Coop == "" {
		b.Coop = CoopOff
	}
	if b.Coop != CoopOff && b.Coop != CoopNew && !coopWorldIDRe.MatchString(b.Coop) {
		return []lobbyOut{notice(mb.conn, NoticeBadMessage, fmt.Sprintf("SETTINGS: co-op %.20q is not off, new or a world ID", b.Coop))}
	}
	if b.Coop != CoopOff && m.noCoop != "" {
		return []lobbyOut{notice(mb.conn, NoticeNoCoop, m.noCoop)}
	}
	if len(b.Worlds) > maxLobbyWorlds {
		b.Worlds = b.Worlds[:maxLobbyWorlds]
	}
	for _, w := range b.Worlds {
		if !coopWorldIDRe.MatchString(w.ID) {
			return []lobbyOut{notice(mb.conn, NoticeBadMessage, "SETTINGS: bad world ID in worlds")}
		}
	}
	if m.phase != PhaseLobby {
		// After Start only merge may change (app design §3.2).
		if b.Coop != m.settings.Coop || b.DryRun != m.settings.DryRun || b.LiveSwitches != m.settings.LiveSwitches {
			return []lobbyOut{notice(mb.conn, NoticeSettingsLocked, "only merging can change once the session has started")}
		}
		if b.Merge == m.settings.Merge {
			return nil
		}
		m.settings.Merge = b.Merge
		return []lobbyOut{m.changed()}
	}
	if b.Worlds == nil {
		b.Worlds = m.settings.Worlds // an update that leaves the list out keeps it
	}
	m.settings = b
	return []lobbyOut{m.changed()}
}

func (m *lobbyMachine) kick(host *lobbyMember, id int) []lobbyOut {
	if id >= cliMemberBase {
		for _, r := range m.cliRows() {
			if r.ID == id {
				// STATE follows from the player list once the
				// connections have closed. The name stays refused,
				// so a reconnecting CLI join stops.
				m.kicked[lobbyKey(r.Name)] = true
				return []lobbyOut{{to: 0, kickName: r.Name}}
			}
		}
	}
	target := m.member(id)
	if target == nil || target == host {
		return []lobbyOut{notice(host.conn, NoticeNoMember, fmt.Sprintf("no member %d to kick", id))}
	}
	m.remove(target)
	m.kicked[target.key] = true
	var outs []lobbyOut
	if target.conn != 0 {
		outs = append(outs, lobbyOut{to: target.conn, sub: LobbyNotice, closeConn: true,
			body: LobbyNoticeBody{Code: NoticeKicked, Text: kickedText}})
	}
	outs = append(outs, lobbyOut{to: 0, kickName: target.name})
	return append(outs, m.changed())
}

func (m *lobbyMachine) remove(mb *lobbyMember) {
	out := m.members[:0]
	for _, o := range m.members {
		if o != mb {
			out = append(out, o)
		}
	}
	m.members = out
	if len(m.members) == 0 {
		// An empty lobby starts over.
		m.phase = PhaseLobby
		m.settings = LobbySettingsBody{Coop: CoopOff}
		m.goBody = nil
		clear(m.kicked)
		return
	}
	if mb.host {
		// The host's slot is gone: the first live member that can host
		// (from the host's machine, or with the owner key) takes over,
		// else the next one to say HELLO.
		for _, o := range m.members {
			if m.canHost(o) && o.conn != 0 {
				o.host = true
				break
			}
		}
	}
}

// drop is connection conn ending. Its member, if it still has that
// connection, is held for m.hold; a hold of 0 removes it at once.
func (m *lobbyMachine) drop(conn int, now time.Time) []lobbyOut {
	mb := m.memberByConn(conn)
	if mb == nil {
		return nil
	}
	if m.hold <= 0 {
		m.remove(mb)
	} else {
		mb.conn, mb.awayUntil = 0, now.Add(m.hold)
	}
	return []lobbyOut{m.changed()}
}

// expire drops the held slots whose time is up at now.
func (m *lobbyMachine) expire(now time.Time) []lobbyOut {
	gone := false
	for _, mb := range append([]*lobbyMember(nil), m.members...) {
		if mb.conn == 0 && !now.Before(mb.awayUntil) {
			m.remove(mb)
			gone = true
		}
	}
	if !gone {
		return nil
	}
	return []lobbyOut{m.changed()}
}

// setPlayers takes the current player connections. STATE follows when the
// CLI rows changed.
func (m *lobbyMachine) setPlayers(ps []lobbyPlayer) []lobbyOut {
	before := m.cliRows()
	m.players = append(m.players[:0:0], ps...)
	after := m.cliRows()
	if len(m.members) == 0 || cliRowsEqual(before, after) {
		return nil
	}
	return []lobbyOut{m.changed()}
}

func cliRowsEqual(a, b []LobbyMember) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// setRTT records a member's lobby round trip; no STATE of its own (it
// rides along with the next change).
func (m *lobbyMachine) setRTT(conn int, ms int) {
	if mb := m.memberByConn(conn); mb != nil {
		mb.rttMs = ms
	}
}

// colors are the members' colors by lobbyKey(name), for rule 0 of the
// server's color pick (pickColorLocked).
func (m *lobbyMachine) colors() map[string]playercolor.Color {
	out := make(map[string]playercolor.Color, len(m.members))
	for _, mb := range m.members {
		out[mb.key] = mb.color
	}
	return out
}

// kickedNames are the lobbyKeys the host kicked, for the server's player
// and co-op refusal.
func (m *lobbyMachine) kickedNames() map[string]bool {
	out := make(map[string]bool, len(m.kicked))
	for k := range m.kicked {
		out[k] = true
	}
	return out
}

// expectsCoop says a co-op connection may arrive before the host has set
// its CoopHandler: the host chose co-op and the session isn't in game yet.
func (m *lobbyMachine) expectsCoop() bool {
	return len(m.members) > 0 && m.phase != PhaseInGame && m.settings.Coop != CoopOff
}
