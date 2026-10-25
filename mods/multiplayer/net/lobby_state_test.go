package network

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/mods/multiplayer/playercolor"
)

// testMachine is a lobbyMachine with deterministic tokens.
func testMachine(hold time.Duration) *lobbyMachine {
	m := newLobbyMachine(hold)
	n := 0
	m.newToken = func() string { n++; return fmt.Sprintf("tok%d", n) }
	return m
}

func findOut(outs []lobbyOut, to int, sub byte) (lobbyOut, bool) {
	for _, o := range outs {
		if o.to == to && o.sub == sub {
			return o, true
		}
	}
	return lobbyOut{}, false
}

func mustJoin(t *testing.T, m *lobbyMachine, conn int, name string, loopback bool, h LobbyHelloBody) *lobbyMember {
	t.Helper()
	mb, outs := m.hello(conn, name, loopback, h, time.Unix(0, 0))
	if mb == nil {
		t.Fatalf("hello %s refused: %+v", name, outs)
	}
	if _, ok := findOut(outs, conn, LobbyWelcome); !ok {
		t.Fatalf("hello %s: no WELCOME in %+v", name, outs)
	}
	return mb
}

func setBody(t *testing.T, b LobbySetBody) []byte {
	t.Helper()
	d, err := EncodeLobby(LobbySet, b)
	if err != nil {
		t.Fatal(err)
	}
	return d[1:]
}

func strp(s string) *string { return &s }
func boolp(b bool) *bool    { return &b }

func TestLobbyMachineHostAndNames(t *testing.T) {
	m := testMachine(time.Minute)
	remote := mustJoin(t, m, 1, "Remote", false, LobbyHelloBody{})
	if remote.host {
		t.Fatal("a remote member became host")
	}
	host := mustJoin(t, m, 2, "Host", true, LobbyHelloBody{})
	if !host.host {
		t.Fatal("the first loopback member is not host")
	}
	second := mustJoin(t, m, 3, "Local2", true, LobbyHelloBody{})
	if second.host {
		t.Fatal("a second loopback member became host too")
	}
	// Names are unique without case; the refusal suggests a free one and
	// closes the connection.
	mb, outs := m.hello(4, "HOST", false, LobbyHelloBody{}, time.Unix(0, 0))
	if mb != nil {
		t.Fatal("a duplicate name was admitted")
	}
	o, ok := findOut(outs, 4, LobbyNotice)
	if !ok || !o.closeConn || o.body.(LobbyNoticeBody).Code != NoticeNameTaken || o.body.(LobbyNoticeBody).Suggest != "HOST (2)" {
		t.Fatalf("duplicate: outs %+v, want name_taken suggesting %q and a close", outs, "HOST (2)")
	}
	long := strings.Repeat("x", MaxNameLen)
	mustJoin(t, m, 5, long, false, LobbyHelloBody{})
	if s := m.suggestName(long); len(s) > MaxNameLen || !strings.HasSuffix(s, " (2)") {
		t.Errorf("suggestName(32 bytes) = %q", s)
	}
}

// TestLobbyMachineColors: every color request is granted as asked, even
// one another member or a CLI player wears (owner decision 2026-10-16,
// docs/10 §4); no preference is classic green. Nothing sends COLOR.
func TestLobbyMachineColors(t *testing.T) {
	m := testMachine(time.Minute)
	pal := playercolor.Palette()
	red, blue := pal[1].Color, pal[2].Color
	a := mustJoin(t, m, 1, "Ann", true, LobbyHelloBody{Color: "red"})
	if a.color != red {
		t.Fatalf("Ann got %v, want red", a.color)
	}
	// The same color at HELLO: granted, shared.
	b, outs := m.hello(2, "Bo", false, LobbyHelloBody{Color: "#D02C2C"}, time.Unix(0, 0))
	if b == nil || b.color != red {
		t.Fatalf("Bo asking for Ann's red got %+v, want red", b)
	}
	noColorOrNotice(t, outs, 2)
	// No preference: green, even though nobody else wears it to begin
	// with, and again with green already worn.
	c := mustJoin(t, m, 3, "Cy", false, LobbyHelloBody{})
	d := mustJoin(t, m, 4, "Di", false, LobbyHelloBody{})
	if c.color != playercolor.Vanilla() || d.color != playercolor.Vanilla() {
		t.Fatalf("no preference: Cy %v Di %v, want green for both", c.color, d.color)
	}
	// A preference that doesn't parse is no preference.
	if e := mustJoin(t, m, 5, "Ed", false, LobbyHelloBody{Color: "chartreuse"}); e.color != playercolor.Vanilla() {
		t.Fatalf("bad preference: Ed %v, want green", e.color)
	}
	// SET to a color another member wears: granted, with STATE.
	outs = m.message(3, LobbySet, setBody(t, LobbySetBody{Color: strp("red")}))
	if c.color != red {
		t.Fatalf("SET red next to Ann's and Bo's red: Cy %v, outs %+v", c.color, outs)
	}
	if _, ok := findOut(outs, lobbyAll, LobbyState); !ok {
		t.Error("a granted SET sent no STATE")
	}
	noColorOrNotice(t, outs, 3)
	// A SET to the color one already has changes nothing.
	if outs := m.message(3, LobbySet, setBody(t, LobbySetBody{Color: strp("red")})); len(outs) != 0 {
		t.Errorf("SET to the same color: outs %+v, want none", outs)
	}
	// A CLI player's color is shareable too.
	m.setPlayers([]lobbyPlayer{{id: 9, name: "Cli", color: blue}})
	outs = m.message(2, LobbySet, setBody(t, LobbySetBody{Color: strp("blue")}))
	if b.color != blue {
		t.Fatalf("SET blue next to CLI blue: Bo %v, outs %+v", b.color, outs)
	}
	noColorOrNotice(t, outs, 2)
	if got := m.colors(); got["ann"] != red || got["bo"] != blue || got["cy"] != red || got["di"] != playercolor.Vanilla() {
		t.Errorf("colors() = %v", got)
	}
}

// TestLobbyMachineManyShareOneColor: more members than the palette has
// colors, all asking for one, all get it.
func TestLobbyMachineManyShareOneColor(t *testing.T) {
	m := testMachine(time.Minute)
	pink := playercolor.Palette()[6].Color
	for i := range len(playercolor.Palette()) + 3 {
		mb, outs := m.hello(i+1, fmt.Sprintf("P%d", i), false, LobbyHelloBody{Color: "pink"}, time.Unix(0, 0))
		if mb == nil || mb.color != pink {
			t.Fatalf("member %d asking for pink: %+v", i, mb)
		}
		noColorOrNotice(t, outs, i+1)
	}
}

// noColorOrNotice fails when outs sends conn a NOTICE or anything on the
// retired COLOR sub-message (0x05).
func noColorOrNotice(t *testing.T, outs []lobbyOut, conn int) {
	t.Helper()
	for _, o := range outs {
		if o.sub == 0x05 || (o.to == conn && o.sub == LobbyNotice) {
			t.Fatalf("unexpected %s to %d: %+v", LobbySubName(o.sub), o.to, o.body)
		}
	}
}

func TestLobbyMachinePhases(t *testing.T) {
	m := testMachine(time.Minute)
	host := mustJoin(t, m, 1, "Host", true, LobbyHelloBody{})
	joiner := mustJoin(t, m, 2, "Joiner", false, LobbyHelloBody{})

	settings, _ := EncodeLobby(LobbySettings, LobbySettingsBody{Coop: CoopNew, DryRun: true})
	if outs := m.message(2, LobbySettings, settings[1:]); len(outs) != 1 || outs[0].body.(LobbyNoticeBody).Code != NoticeNotHost {
		t.Fatalf("joiner SETTINGS: %+v", outs)
	}
	m.message(1, LobbySettings, settings[1:])
	if m.settings.Coop != CoopNew || !m.settings.DryRun || !m.expectsCoop() {
		t.Fatalf("settings %+v expectsCoop %v", m.settings, m.expectsCoop())
	}
	bad, _ := EncodeLobby(LobbySettings, LobbySettingsBody{Coop: "../../x"})
	if outs := m.message(1, LobbySettings, bad[1:]); outs[0].body.(LobbyNoticeBody).Code != NoticeBadMessage {
		t.Fatalf("bad co-op mode accepted: %+v", outs)
	}

	if outs := m.message(2, LobbyStart, []byte("{}")); outs[0].body.(LobbyNoticeBody).Code != NoticeNotHost {
		t.Fatalf("joiner START: %+v", outs)
	}
	if outs := m.message(1, LobbyGo, []byte("{}")); outs[0].body.(LobbyNoticeBody).Code != NoticeWrongPhase {
		t.Fatalf("GO before START: %+v", outs)
	}
	outs := m.message(1, LobbyStart, []byte("{}"))
	if m.phase != PhaseStarting {
		t.Fatalf("phase %q after START", m.phase)
	}
	if o, ok := findOut(outs, lobbyAll, LobbyStart); !ok || o.body.(LobbyStartBody).Settings.Coop != CoopNew {
		t.Fatalf("START outs %+v", outs)
	}
	// STATE goes before START, so a client sees the new phase first.
	if outs[0].sub != LobbyState {
		t.Errorf("first out after START is %s, want STATE", LobbySubName(outs[0].sub))
	}
	if outs := m.message(1, LobbyStart, []byte("{}")); outs[0].body.(LobbyNoticeBody).Code != NoticeWrongPhase {
		t.Fatalf("second START: %+v", outs)
	}
	// Colors are locked; ready is not.
	c := joiner.color
	outs = m.message(2, LobbySet, setBody(t, LobbySetBody{Color: strp("orange"), Ready: boolp(true)}))
	if joiner.color != c || !joiner.ready {
		t.Fatalf("after START: color %v ready %v", joiner.color, joiner.ready)
	}
	if o, ok := findOut(outs, 2, LobbyNotice); !ok || o.body.(LobbyNoticeBody).Code != NoticeColorsLocked {
		t.Fatalf("color change after START: %+v", outs)
	}
	// Settings: only merge may change.
	s2, _ := EncodeLobby(LobbySettings, LobbySettingsBody{Coop: CoopOff})
	if outs := m.message(1, LobbySettings, s2[1:]); outs[0].body.(LobbyNoticeBody).Code != NoticeSettingsLocked {
		t.Fatalf("co-op change after START: %+v", outs)
	}
	s3, _ := EncodeLobby(LobbySettings, LobbySettingsBody{Coop: CoopNew, DryRun: true, Merge: true})
	m.message(1, LobbySettings, s3[1:])
	if !m.settings.Merge {
		t.Fatal("merge did not change after START")
	}
	outs = m.message(1, LobbyGo, []byte(`{"world":"0123456789abcdef"}`))
	if m.phase != PhaseInGame || m.expectsCoop() {
		t.Fatalf("after GO: phase %q expectsCoop %v", m.phase, m.expectsCoop())
	}
	if o, ok := findOut(outs, lobbyAll, LobbyGo); !ok || o.body.(LobbyGoBody).World != "0123456789abcdef" {
		t.Fatalf("GO outs %+v", outs)
	}
	_ = host
}

func TestLobbyMachineHoldReclaimExpire(t *testing.T) {
	m := testMachine(time.Minute)
	t0 := time.Unix(1000, 0)
	host := mustJoin(t, m, 1, "Host", true, LobbyHelloBody{Color: "blue"})
	m.message(1, LobbySet, setBody(t, LobbySetBody{Ready: boolp(true)}))
	m.drop(1, t0)
	if host.conn != 0 || !m.snapshot().Members[0].Away {
		t.Fatal("dropped member not away")
	}
	// The name stays reserved; a wrong token doesn't reclaim.
	if mb, _ := m.hello(2, "Host", true, LobbyHelloBody{Token: "nope"}, t0); mb != nil {
		t.Fatal("a held name was taken without its token")
	}
	mb, outs := m.hello(3, "Host", true, LobbyHelloBody{Token: host.token}, t0.Add(30*time.Second))
	if mb != host || !mb.host || !mb.ready || mb.color != playercolor.Palette()[2].Color || mb.conn != 3 {
		t.Fatalf("reclaim: %+v", mb)
	}
	if o, _ := findOut(outs, 3, LobbyWelcome); o.body.(LobbyWelcomeBody).Member != host.id {
		t.Fatalf("reclaim WELCOME %+v", o)
	}
	if mb.dolphin != "" {
		t.Fatalf("a HELLO without a Dolphin state changed it: %q", mb.dolphin)
	}
	// Reclaim while the old connection still looks live: it is closed.
	// Its HELLO's Dolphin state replaces the old one (a LOBBY_SET lost
	// while the link was down).
	_, outs = m.hello(4, "Host", true, LobbyHelloBody{Token: host.token, Dolphin: DolphinInGame}, t0)
	if host.dolphin != DolphinInGame {
		t.Fatalf("reclaim kept the stale Dolphin state %q", host.dolphin)
	}
	if o, ok := findOut(outs, 3, LobbyNotice); !ok || !o.closeConn {
		t.Fatalf("takeover outs %+v", outs)
	}
	if m.drop(3, t0) != nil {
		t.Fatal("the replaced connection's drop changed the lobby")
	}
	m.drop(4, t0)
	if m.expire(t0.Add(59*time.Second)) != nil || len(m.members) != 1 {
		t.Fatal("expired early")
	}
	if m.expire(t0.Add(61*time.Second)) == nil || len(m.members) != 0 {
		t.Fatal("did not expire")
	}
	// A fresh join of the name is a new member.
	if mb := mustJoin(t, m, 5, "Host", true, LobbyHelloBody{Token: host.token}); mb.id == host.id {
		t.Fatal("expired slot reclaimed")
	}
}

func TestLobbyMachineEmptyLobbyStartsOver(t *testing.T) {
	m := testMachine(0)
	mustJoin(t, m, 1, "Host", true, LobbyHelloBody{})
	m.message(1, LobbyStart, nil)
	if m.phase != PhaseStarting {
		t.Fatal("no START")
	}
	m.drop(1, time.Now())
	if m.phase != PhaseLobby || len(m.members) != 0 {
		t.Fatalf("after the last member left: phase %q members %d", m.phase, len(m.members))
	}
}

func TestLobbyMachineCLIRowsAndKick(t *testing.T) {
	m := testMachine(time.Minute)
	mustJoin(t, m, 1, "Host", true, LobbyHelloBody{})
	app := mustJoin(t, m, 2, "App", false, LobbyHelloBody{})
	outs := m.setPlayers([]lobbyPlayer{
		{id: 3, name: "Cli", color: playercolor.Palette()[3].Color},
		{id: 4, name: "Cli", color: playercolor.Palette()[3].Color, posed: true},
		{id: 5, name: "app", color: app.color}, // the member's own pose connection
	})
	if _, ok := findOut(outs, lobbyAll, LobbyState); !ok {
		t.Fatal("no STATE for a new CLI player")
	}
	st := m.snapshot()
	if len(st.Members) != 3 {
		t.Fatalf("members %+v, want Host, App and one CLI row", st.Members)
	}
	cli := st.Members[2]
	if cli.Via != ViaCLI || cli.Name != "Cli" || !cli.Ready || cli.Dolphin != DolphinInGame || cli.ID != cliMemberBase+3 {
		t.Fatalf("CLI row %+v", cli)
	}
	if outs := m.setPlayers(m.players); outs != nil {
		t.Error("an unchanged player list sent STATE")
	}
	// Kick: the CLI row's player connections; a member's lobby and
	// player connections.
	outs = m.message(1, LobbyKick, []byte(fmt.Sprintf(`{"member":%d}`, cli.ID)))
	if len(outs) != 1 || outs[0].kickName != "Cli" {
		t.Fatalf("kick CLI: %+v", outs)
	}
	outs = m.message(1, LobbyKick, []byte(fmt.Sprintf(`{"member":%d}`, app.id)))
	o, ok := findOut(outs, 2, LobbyNotice)
	if !ok || !o.closeConn || o.body.(LobbyNoticeBody).Code != NoticeKicked {
		t.Fatalf("kick App: %+v", outs)
	}
	kicked := false
	for _, o := range outs {
		kicked = kicked || o.kickName == "App"
	}
	if !kicked || m.member(app.id) != nil {
		t.Fatalf("kick App: outs %+v, member still there: %v", outs, m.member(app.id) != nil)
	}
	if outs := m.message(2, LobbyKick, []byte(`{"member":1}`)); outs[0].body.(LobbyNoticeBody).Code != NoticeHelloFirst {
		t.Fatalf("kicked connection still talks: %+v", outs)
	}
	if outs := m.message(1, LobbyKick, []byte(`{"member":1}`)); outs[0].body.(LobbyNoticeBody).Code != NoticeNoMember {
		t.Fatalf("host kicking itself: %+v", outs)
	}
}

func TestLobbyMachineRequestGoesToHost(t *testing.T) {
	m := testMachine(time.Minute)
	mustJoin(t, m, 1, "Host", true, LobbyHelloBody{})
	mustJoin(t, m, 2, "Jo", false, LobbyHelloBody{})
	outs := m.message(2, LobbyRequest, []byte(`{"kind":"merge","from":"forged"}`))
	if len(outs) != 1 || outs[0].to != 1 || outs[0].body.(LobbyRequestBody).From != "Jo" {
		t.Fatalf("REQUEST outs %+v", outs)
	}
	if outs := m.message(2, LobbyRequest, []byte(`{"kind":"other"}`)); outs[0].body.(LobbyNoticeBody).Code != NoticeBadMessage {
		t.Fatalf("unknown request: %+v", outs)
	}
	if outs := m.message(2, 0x7F, []byte("garbage")); outs != nil {
		t.Fatalf("unknown sub: %+v", outs)
	}
}

func TestLobbyMachineFull(t *testing.T) {
	m := testMachine(time.Minute)
	for i := 0; i < MaxLobbyMembers; i++ {
		mustJoin(t, m, i+1, fmt.Sprintf("p%d", i), false, LobbyHelloBody{})
	}
	if mb, outs := m.hello(999, "late", false, LobbyHelloBody{}, time.Now()); mb != nil || outs[0].body.(LobbyNoticeBody).Code != NoticeFull {
		t.Fatalf("join past the cap: %v %+v", mb, outs)
	}
}

// TestLobbyMachineKickSticks: a kicked name stays refused, as a member and
// as a CLI row, until the lobby empties.
func TestLobbyMachineKickSticks(t *testing.T) {
	m := testMachine(time.Minute)
	mustJoin(t, m, 1, "Host", true, LobbyHelloBody{})
	bad := mustJoin(t, m, 2, "Griefer", false, LobbyHelloBody{})
	m.setPlayers([]lobbyPlayer{{id: 7, name: "Lurker", color: playercolor.Palette()[5].Color}})
	m.message(1, LobbyKick, []byte(fmt.Sprintf(`{"member":%d}`, bad.id)))
	m.message(1, LobbyKick, []byte(fmt.Sprintf(`{"member":%d}`, cliMemberBase+7)))
	if k := m.kickedNames(); !k["griefer"] || !k["lurker"] || len(k) != 2 {
		t.Fatalf("kickedNames %v", k)
	}
	// A fresh HELLO (no token needed for a new slot) in any case.
	mb, outs := m.hello(3, "GRIEFER", false, LobbyHelloBody{}, time.Unix(0, 0))
	if o, ok := findOut(outs, 3, LobbyNotice); mb != nil || !ok || !o.closeConn || o.body.(LobbyNoticeBody).Code != NoticeKicked {
		t.Fatalf("kicked name's HELLO: %v %+v", mb, outs)
	}
	// The lobby empties: a new session, nobody is kicked.
	m.drop(1, time.Unix(0, 0))
	m.expire(time.Unix(0, 0).Add(2 * time.Minute))
	if len(m.members) != 0 || len(m.kickedNames()) != 0 {
		t.Fatalf("after the lobby emptied: members %d kicked %v", len(m.members), m.kickedNames())
	}
	mustJoin(t, m, 4, "Griefer", false, LobbyHelloBody{})
}

// TestLobbyMachineReplaysStartAndGo: a member that reclaims its slot, or
// joins, after START gets START (and, in game, GO with the world) at its
// HELLO, after STATE.
func TestLobbyMachineReplaysStartAndGo(t *testing.T) {
	m := testMachine(time.Minute)
	t0 := time.Unix(0, 0)
	mustJoin(t, m, 1, "Host", true, LobbyHelloBody{})
	jo := mustJoin(t, m, 2, "Jo", false, LobbyHelloBody{})
	if _, outs := m.hello(9, "Early", false, LobbyHelloBody{}, t0); hasSub(outs, LobbyStart) || hasSub(outs, LobbyGo) {
		t.Fatalf("START or GO in the lobby phase: %+v", outs)
	}
	m.message(1, LobbySettings, []byte(`{"coop":"new"}`))
	m.message(1, LobbyStart, nil)

	m.drop(2, t0)
	_, outs := m.hello(3, "Jo", false, LobbyHelloBody{Token: jo.token}, t0)
	if !subsInOrder(outs, 3, LobbyWelcome, LobbyStart) || hasSubTo(outs, 3, LobbyGo) {
		t.Fatalf("reclaim in starting: %+v", outs)
	}
	if o, _ := findOut(outs, 3, LobbyStart); o.body.(LobbyStartBody).Settings.Coop != CoopNew {
		t.Fatalf("replayed START %+v", o.body)
	}
	if _, ok := findOut(outs, lobbyAll, LobbyState); !ok || indexOf(outs, lobbyAll, LobbyState) > indexOf(outs, 3, LobbyStart) {
		t.Fatalf("STATE does not come before the replayed START: %+v", outs)
	}

	m.message(1, LobbyGo, []byte(`{"world":"0123456789abcdef"}`))
	m.drop(3, t0)
	_, outs = m.hello(4, "Jo", false, LobbyHelloBody{Token: jo.token}, t0)
	if !subsInOrder(outs, 4, LobbyWelcome, LobbyStart, LobbyGo) {
		t.Fatalf("reclaim in game: %+v", outs)
	}
	if o, _ := findOut(outs, 4, LobbyGo); o.body.(LobbyGoBody).World != "0123456789abcdef" {
		t.Fatalf("replayed GO %+v", o.body)
	}
	// A late joiner gets both too.
	if _, outs := m.hello(5, "Late", false, LobbyHelloBody{}, t0); !subsInOrder(outs, 5, LobbyWelcome, LobbyStart, LobbyGo) {
		t.Fatalf("late joiner in game: %+v", outs)
	}
	// An empty lobby forgets GO.
	for _, c := range []int{1, 4, 5, 9} {
		m.drop(c, t0)
	}
	m.expire(t0.Add(2 * time.Minute))
	if m.goBody != nil || m.phase != PhaseLobby {
		t.Fatalf("empty lobby kept GO %+v / phase %s", m.goBody, m.phase)
	}
}

func hasSub(outs []lobbyOut, sub byte) bool {
	for _, o := range outs {
		if o.sub == sub {
			return true
		}
	}
	return false
}

func hasSubTo(outs []lobbyOut, to int, sub byte) bool {
	_, ok := findOut(outs, to, sub)
	return ok
}

func indexOf(outs []lobbyOut, to int, sub byte) int {
	for i, o := range outs {
		if o.to == to && o.sub == sub {
			return i
		}
	}
	return -1
}

// subsInOrder reports whether outs has subs for to, in that order.
func subsInOrder(outs []lobbyOut, to int, subs ...byte) bool {
	last := -1
	for _, s := range subs {
		i := indexOf(outs, to, s)
		if i <= last {
			return false
		}
		last = i
	}
	return true
}

// TestLobbyMachineHostSlotPasses: when the host's held slot expires, the
// first live member from the host's machine becomes host; with none, the
// next loopback HELLO (or reclaim) does. A remote member never does.
func TestLobbyMachineHostSlotPasses(t *testing.T) {
	m := testMachine(time.Minute)
	t0 := time.Unix(0, 0)
	host := mustJoin(t, m, 1, "Host", true, LobbyHelloBody{})
	remote := mustJoin(t, m, 2, "Remote", false, LobbyHelloBody{})
	local := mustJoin(t, m, 3, "Local2", true, LobbyHelloBody{})
	m.drop(1, t0)
	if !host.host || local.host {
		t.Fatal("a held host slot lost the host")
	}
	m.expire(t0.Add(2 * time.Minute))
	if m.member(host.id) != nil || !local.host || remote.host {
		t.Fatalf("after the host's slot expired: local %v remote %v", local.host, remote.host)
	}
	if st := m.snapshot(); !st.Members[1].Host || st.Members[1].Name != "Local2" {
		t.Fatalf("STATE after the hand-over %+v", st.Members)
	}
	if outs := m.message(3, LobbyStart, nil); m.phase != PhaseStarting {
		t.Fatalf("the new host can't start: %+v", outs)
	}

	// No live loopback member: nobody is host until one says HELLO.
	m2 := testMachine(time.Minute)
	mustJoin(t, m2, 1, "Host", true, LobbyHelloBody{})
	r := mustJoin(t, m2, 2, "Remote", false, LobbyHelloBody{})
	l := mustJoin(t, m2, 3, "Local2", true, LobbyHelloBody{})
	m2.drop(3, t0)
	m2.drop(1, t0)
	m2.expire(t0.Add(30 * time.Second)) // nothing yet
	if !m2.hasHost() {
		t.Fatal("host lost before the hold ran out")
	}
	m2.members[0].awayUntil = t0 // only the host's slot runs out
	m2.expire(t0.Add(time.Second))
	if m2.hasHost() || r.host {
		t.Fatalf("host after expiry with no live loopback member: remote %v", r.host)
	}
	if outs := m2.message(2, LobbyRequest, []byte(`{"kind":"merge"}`)); outs[0].body.(LobbyNoticeBody).Code != "no_host" {
		t.Fatalf("REQUEST with no host: %+v", outs)
	}
	m2.hello(4, "Local2", true, LobbyHelloBody{Token: l.token}, t0.Add(time.Second))
	if !l.host {
		t.Fatal("a loopback member reclaiming its slot did not become host")
	}
}

// FuzzLobbyMachine drives the lobby state machine with an arbitrary
// sequence of joins, sub-messages, drops, timer ticks and player-list
// changes, and checks its invariants after every step:
//
//   - names are unique among members (without case), and at most one is host
//   - the phase only moves forward (lobby -> starting -> in_game), and back
//     to lobby only when the lobby is empty
//   - a member's color never changes once the session has started
//   - a new member wears the color it asked for, or green without a
//     usable one, and a SET color in the lobby phase is granted as asked,
//     whoever else wears it; COLOR (0x05) is never sent
//   - every STATE has a higher rev than the one before, and every frame
//     encodes within MaxLobbyBody
//   - only the host's START and GO change the phase
//   - every live member has been sent START once the phase is starting,
//     and GO once it is in_game (also when it joined or reclaimed later)
func FuzzLobbyMachine(f *testing.F) {
	f.Add([]byte{0, 1, 0, 0, 2, 1, 1, 7, 0, 1, 8, 2, 3, 4})
	f.Add([]byte{0, 0, 1, 0, 1, 2, 1, 4, 1, 3, 0, 5, 5, 0})
	f.Add([]byte{0, 9, 1, 0, 9, 1, 3, 0, 4, 1, 0, 9, 1, 2})
	names := []string{"Ann", "ann", "Bo", "Cy", "Di", strings.Repeat("Z", MaxNameLen)}
	colors := []string{"", "red", "#D83030", "green", "blue", "#000000", "nonsense", "pink"}
	bodies := [][]byte{
		[]byte(`{"color":"red"}`), []byte(`{"color":"#2C5CE1","ready":true}`), []byte(`{"ready":false,"dolphin":"in_game","file":"File 2"}`),
		[]byte(`{"coop":"new","dryRun":true}`), []byte(`{"coop":"off","merge":true}`), []byte(`{}`), []byte(`{"world":"0123456789abcdef"}`),
		[]byte(`{"member":1}`), []byte(`{"member":2}`), []byte(`{"kind":"merge"}`), []byte(`[`), []byte(`{"color":"green"}`),
		[]byte(`{"file":"` + strings.Repeat("é\"\\", 60) + `"}`),
	}
	f.Add([]byte{0x80, 1, 0x20, 0, 2, 0x60, 3, 0, 0, 4, 61, 0, 0, 3, 0xA0})
	f.Fuzz(func(t *testing.T, ops []byte) {
		m := testMachine(time.Minute)
		// The first byte's top bit keys the lobby (an owner key): then a
		// host must be a member that presented it.
		m.keyed = len(ops) > 0 && ops[0]&0x80 != 0
		now := time.Unix(0, 0)
		nextConn := 0
		var conns []int
		rank := map[string]int{PhaseLobby: 0, PhaseStarting: 1, PhaseInGame: 2}
		lastRev := uint64(0)
		lockedColors := map[int]playercolor.Color{}
		gotStart, gotGo := map[int]bool{}, map[int]bool{} // by connection

		check := func(op string, outs []lobbyOut, before string) {
			for _, o := range outs {
				if o.sub == 0 {
					continue
				}
				if o.sub == 0x05 {
					t.Fatalf("%s: sent the retired COLOR: %+v", op, o.body)
				}
				if o.sub == LobbyStart || o.sub == LobbyGo {
					got := gotStart
					if o.sub == LobbyGo {
						got = gotGo
					}
					for _, mb := range m.members {
						if mb.conn != 0 && (o.to == lobbyAll || o.to == mb.conn) {
							got[mb.conn] = true
						}
					}
				}
				if _, err := EncodeLobby(o.sub, o.body); err != nil {
					t.Fatalf("%s: %s does not encode: %v", op, LobbySubName(o.sub), err)
				}
				if st, ok := o.body.(LobbyStateBody); ok {
					if st.Rev <= lastRev {
						t.Fatalf("%s: STATE rev %d after %d", op, st.Rev, lastRev)
					}
					lastRev = st.Rev
				}
			}
			keys := map[string]bool{}
			hosts := 0
			for _, mb := range m.members {
				if keys[mb.key] {
					t.Fatalf("%s: two members named %q", op, mb.key)
				}
				keys[mb.key] = true
				if mb.host {
					hosts++
					if m.keyed && !mb.owner {
						t.Fatalf("%s: host %q never presented the owner key", op, mb.name)
					}
				}
			}
			if hosts > 1 {
				t.Fatalf("%s: %d hosts", op, hosts)
			}
			if len(m.members) == 0 {
				clear(gotStart)
				clear(gotGo)
			}
			for _, mb := range m.members {
				if mb.conn == 0 {
					continue
				}
				if m.phase != PhaseLobby && !gotStart[mb.conn] {
					t.Fatalf("%s: %s is live in %s and never got START", op, mb.name, m.phase)
				}
				if m.phase == PhaseInGame && !gotGo[mb.conn] {
					t.Fatalf("%s: %s is live in game and never got GO", op, mb.name)
				}
			}
			if rank[m.phase] < rank[before] && len(m.members) != 0 {
				t.Fatalf("%s: phase went back from %s to %s", op, before, m.phase)
			}
			if m.phase == PhaseLobby {
				clear(lockedColors)
			} else {
				for _, mb := range m.members {
					if c, ok := lockedColors[mb.id]; ok && c != mb.color {
						t.Fatalf("%s: member %d's color changed after Start", op, mb.id)
					}
					lockedColors[mb.id] = mb.color
				}
			}
		}
		// asked is the color a request for s gives: s itself, or green
		// when it is empty or doesn't parse.
		asked := func(s string) playercolor.Color {
			if c, err := playercolor.Parse(s); err == nil && s != "" {
				return c
			}
			return playercolor.Vanilla()
		}

		for i := 0; i+2 < len(ops); i += 3 {
			op, a, b := ops[i]%7, int(ops[i+1]), int(ops[i+2])
			before := m.phase
			var outs []lobbyOut
			var desc string
			switch op {
			case 0: // a new connection says HELLO
				nextConn++
				conns = append(conns, nextConn)
				h := LobbyHelloBody{Color: colors[b%len(colors)]}
				if b&0x80 != 0 && len(m.members) > 0 {
					h.Token = m.members[a%len(m.members)].token
				}
				name := names[a%len(names)]
				var mb *lobbyMember
				mb, outs = m.helloAs(nextConn, name, b&0x40 != 0, b&0x20 != 0, h, now)
				desc = fmt.Sprintf("hello %d %q loopback %v owner %v %+v", nextConn, name, b&0x40 != 0, b&0x20 != 0, h)
				if mb != nil && mb.token != h.Token && mb.color != asked(h.Color) {
					t.Fatalf("%s: new member wears %v, want %v", desc, mb.color, asked(h.Color))
				}
				if before != PhaseLobby && m.phase != before {
					t.Fatalf("%s: HELLO changed the phase", desc)
				}
			case 1, 2: // a sub-message on some connection
				if len(conns) == 0 {
					continue
				}
				conn := conns[a%len(conns)]
				sub := byte(b%12) + 1
				body := bodies[(a+b)%len(bodies)]
				mb := m.memberByConn(conn)
				var c0 playercolor.Color
				if mb != nil {
					c0 = mb.color
				}
				outs = m.message(conn, sub, body)
				desc = fmt.Sprintf("conn %d %s %s", conn, LobbySubName(sub), body)
				var sb LobbySetBody
				if mb != nil && sub == LobbySet && before == PhaseLobby && DecodeLobby(body, &sb) == nil && sb.Color != nil {
					if want, err := playercolor.Parse(*sb.Color); err == nil && mb.color != want {
						t.Fatalf("%s: SET color %v not granted (holds %v, had %v)", desc, want, mb.color, c0)
					}
				}
				if m.phase != before && !(mb != nil && mb.host && (sub == LobbyStart || sub == LobbyGo)) &&
					!(sub == LobbyKick && len(m.members) == 0) {
					t.Fatalf("%s: phase %s -> %s", desc, before, m.phase)
				}
			case 3: // a connection drops
				if len(conns) == 0 {
					continue
				}
				conn := conns[a%len(conns)]
				outs = m.drop(conn, now)
				desc = fmt.Sprintf("drop %d", conn)
			case 4: // time passes
				now = now.Add(time.Duration(a) * time.Second)
				outs = m.expire(now)
				desc = fmt.Sprintf("expire +%ds", a)
			case 5: // the player connections change
				var ps []lobbyPlayer
				for j := 0; j < a%5; j++ {
					ps = append(ps, lobbyPlayer{id: byte(j + 1), name: names[(b+j)%len(names)] + "x",
						color: playercolor.Palette()[(b+j)%8].Color, posed: (b>>j)&1 == 1})
				}
				outs = m.setPlayers(ps)
				desc = fmt.Sprintf("players %v", ps)
			case 6: // a round trip
				if len(conns) > 0 {
					m.setRTT(conns[a%len(conns)], b)
				}
				continue
			}
			check(desc, outs, before)
		}
	})
}
