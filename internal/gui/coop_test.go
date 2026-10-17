package gui

import (
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/internal/config"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/bind"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/world"
	"github.com/StephenSHorton/ww-multiplayer/internal/session"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/log"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/savefile"
)

// coopEngine is a fakeEngine with the co-op side: the console records
// its commands, CoopWorlds lists worlds, UnbindFile counts, and Live
// reports live.
type coopEngine struct {
	*fakeEngine
	cmu     sync.Mutex
	console []session.Command
	lines   []string
	unbinds int
	unbound string
	live    session.Live
	worlds  []session.CoopWorld
}

func (e *coopEngine) Command(c session.Command) error {
	e.cmu.Lock()
	defer e.cmu.Unlock()
	e.console = append(e.console, c)
	return nil
}

func (e *coopEngine) CommandLine(line string) error {
	e.cmu.Lock()
	defer e.cmu.Unlock()
	e.lines = append(e.lines, line)
	return nil
}

func (e *coopEngine) CoopWorlds() ([]session.CoopWorld, error) { return e.worlds, nil }

func (e *coopEngine) UnbindFile(world string) error {
	e.cmu.Lock()
	defer e.cmu.Unlock()
	e.unbinds++
	e.unbound = world
	return nil
}

func (e *coopEngine) Live() session.Live {
	e.cmu.Lock()
	defer e.cmu.Unlock()
	return e.live
}

func newCoopBridge(t *testing.T) (*Bridge, *coopEngine, string) {
	t.Helper()
	e := &coopEngine{fakeEngine: newFakeEngine()}
	path := filepath.Join(t.TempDir(), "config.json")
	b := NewBridge(Options{Version: "v9.9.9", Controller: session.New(e, session.Options{}), ConfigPath: path})
	b.findSave = func(string) (cardSave, error) { return cardSave{}, errors.New("no card here") }
	t.Cleanup(b.close)
	return b, e, path
}

const worldA = "0123456789abcdef"

// TestCoopSettingsRoundTrip: the host's choice goes to the lobby as its
// settings (with the world list), is saved, and reads back the same; a
// bad choice is refused before the wire.
func TestCoopSettingsRoundTrip(t *testing.T) {
	for _, r := range []CoopRequest{
		{Mode: "off"},
		{Mode: "new", DryRun: true},
		{Mode: "continue", World: worldA, Merge: true, LiveSwitches: true, ISO: `C:\Roms\ww.iso`},
	} {
		s, err := settingsOf(r)
		if err != nil {
			t.Fatal(err)
		}
		if back := requestOf(s, r.ISO); back != r {
			t.Errorf("%+v -> %+v -> %+v", r, s, back)
		}
	}
	for _, bad := range []CoopRequest{{Mode: "continue"}, {Mode: "continue", World: "nope"}, {Mode: "sideways"}} {
		if _, err := settingsOf(bad); err == nil {
			t.Errorf("settingsOf(%+v) took it", bad)
		}
	}

	b, e, path := newCoopBridge(t)
	e.worlds = []session.CoopWorld{{ID: worldA, Seq: 9}, {ID: "fedcba9876543210", Err: "damaged"}}
	if err := b.Host(HostRequest{Name: "Link"}); err != nil {
		t.Fatal(err)
	}
	e.out(t)
	r := CoopRequest{Mode: "continue", World: worldA, DryRun: true, ISO: "disc.iso"}
	if err := b.SetCoop(r); err != nil {
		t.Fatal(err)
	}
	e.mu.Lock()
	cmds := slices.Clone(e.cmds)
	e.mu.Unlock()
	if len(cmds) != 1 || cmds[0].Kind != session.LobbySetSettings {
		t.Fatalf("commands %+v", cmds)
	}
	s := cmds[0].Settings
	if s.Coop != worldA || !s.DryRun || s.Merge || len(s.Worlds) != 1 || s.Worlds[0] != (session.LobbyWorld{ID: worldA, Seq: 9}) {
		t.Fatalf("settings sent %+v", s)
	}
	c, _ := config.Load(path)
	if got := choiceOf(c.Coop); got != r {
		t.Fatalf("saved %+v, want %+v", got, r)
	}
	if err := b.SetCoop(CoopRequest{Mode: "continue"}); err == nil {
		t.Error("Continue without a world was taken")
	}
	// The host's own disc and a joiner's dry run reach the engine at Start.
	if o := b.coopOptions(); o.ISO != "disc.iso" || o.DryRun {
		t.Fatalf("coopOptions %+v", o)
	}
	b.SetMyCoopDryRun(true)
	if o := b.coopOptions(); !o.DryRun {
		t.Fatal("SetMyCoopDryRun did not count")
	}
	e.mu.Lock()
	fn := e.host[0].CoopFn
	e.mu.Unlock()
	if fn == nil || !fn().DryRun {
		t.Fatal("HostLobby got no CoopFn")
	}
}

// TestCoopWorldsView: the Continue list from the worlds on disk.
func TestCoopWorldsView(t *testing.T) {
	b, e, _ := newCoopBridge(t)
	at := time.Date(2026, 10, 1, 12, 30, 0, 0, time.Local)
	e.worlds = []session.CoopWorld{{ID: worldA, Seq: 4, Created: at, InUse: true, Text: "x"}, {ID: "fedcba9876543210", Stale: true, Err: "damaged"}}
	ws, err := b.CoopWorlds()
	if err != nil {
		t.Fatal(err)
	}
	want := []WorldView{{ID: worldA, Seq: 4, Created: "1 Oct 2026 12:30", InUse: true, Text: "x"}, {ID: "fedcba9876543210", Stale: true, Err: "damaged"}}
	if !slices.Equal(ws, want) {
		t.Fatalf("worlds %+v", ws)
	}
}

// cardWith is a card save whose slots hold the given files (name ""
// leaves a slot a new game), each with max life hearts*4 and the tag
// bind writes for world w (zero for none).
func cardWith(t *testing.T, files ...struct {
	name   string
	hearts int
	w      bind.WorldID
}) cardSave {
	t.Helper()
	f := savefile.New()
	for n, fl := range files {
		if fl.name == "" {
			continue
		}
		var img world.Image
		copy(img[addr.OffPlayerName:], fl.name)
		binary.BigEndian.PutUint16(img[addr.OffMaxLife:], uint16(fl.hearts*4))
		binary.BigEndian.PutUint64(img[addr.OffDateIPL:], 0x1234) // saved and loaded
		if !fl.w.IsZero() {
			tag := bind.Tag{Layout: bind.LayoutV1, Slot: uint8(n), World: fl.w, Nonce: bind.Nonce{1}, NameHash: bind.NameHash(img[addr.OffPlayerName : addr.OffPlayerName+bind.PlayerNameSize])}
			b := tag.Encode()
			copy(img[addr.OffReserve:], b[:])
		}
		var err error
		if f, err = f.WithSlot(n, &img); err != nil {
			t.Fatal(err)
		}
	}
	return cardSave{path: "card.gci", file: f}
}

type fileSpec = struct {
	name   string
	hearts int
	w      bind.WorldID
}

// TestCardPreview: the three files with name, hearts and bind state for
// the session's world, and the hint, for Continue and New world.
func TestCardPreview(t *testing.T) {
	this, _ := bind.ParseWorldID(worldA)
	other := bind.WorldID{9, 9, 9, 9, 9, 9, 9, 9}
	s := cardWith(t, fileSpec{"Link", 3, other}, fileSpec{"Aryll", 6, this}, fileSpec{})
	v := previewOf(s, worldA, false)
	if len(v.Files) != 3 {
		t.Fatalf("files %+v", v.Files)
	}
	f1, f2, f3 := v.Files[0], v.Files[1], v.Files[2]
	if f1.Name != "Link" || f1.Hearts != 3 || f1.Bind != "other" || f1.World != other.String() {
		t.Errorf("file 1 %+v", f1)
	}
	if f2.Name != "Aryll" || f2.Hearts != 6 || f2.Bind != "this" {
		t.Errorf("file 2 %+v", f2)
	}
	if f3.State != "empty" || f3.Bind != "" {
		t.Errorf("file 3 %+v", f3)
	}
	if !strings.Contains(v.Hint, "Load File 2 “Aryll”: it's bound to this world") || v.Short != `File 2 "Aryll": bound` {
		t.Errorf("hint %q / %q", v.Hint, v.Short)
	}

	// Another world: Aryll's file is bound elsewhere now; an unbound file
	// would be asked to bind.
	s = cardWith(t, fileSpec{"Link", 3, bind.WorldID{}}, fileSpec{"Aryll", 6, this})
	v = previewOf(s, "fedcba9876543210", false)
	if v.Files[0].Bind != "unbound" || v.Files[1].Bind != "other" || !strings.Contains(v.Hint, "File 1 “Link” isn't bound to this world yet") {
		t.Errorf("another world: %+v %q", v.Files, v.Hint)
	}
	// New world: the host founds it from a file; a joiner is asked to bind.
	if v = previewOf(s, "new", true); !strings.Contains(v.Hint, "the new world is founded from it") || v.Files[1].Bind != "other" {
		t.Errorf("new world, host: %+v %q", v.Files, v.Hint)
	}
	if v = previewOf(s, "new", false); !strings.Contains(v.Hint, "File 1 “Link” isn't bound") {
		t.Errorf("new world, joiner: %q", v.Hint)
	}
	// Only files bound elsewhere.
	s = cardWith(t, fileSpec{"Link", 3, other})
	if v = previewOf(s, worldA, false); !strings.Contains(v.Hint, "bound to another world") || v.Short != `File 1 "Link": bound elsewhere` {
		t.Errorf("bound elsewhere: %q / %q", v.Hint, v.Short)
	}
}

// TestFindSave: the preview reads the save from the GCI folder Dolphin
// uses in the user folder.
func TestFindSave(t *testing.T) {
	ud := filepath.Join(t.TempDir(), "Dolphin Emulator")
	dir := filepath.Join(ud, "GC", "USA", "Card A")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	s := cardWith(t, fileSpec{"Link", 3, bind.WorldID{}})
	if err := os.WriteFile(filepath.Join(dir, "01-GZLE-gczelda.gci"), s.file.Encode(), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "other.gci"), []byte("not a save"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := findSave(ud)
	if err != nil {
		t.Fatal(err)
	}
	if v := previewOf(got, "new", true); v.Files[0].Name != "Link" {
		t.Fatalf("read %+v", v.Files)
	}
	if _, err := findSave(t.TempDir()); err == nil {
		t.Error("a user folder without cards had a save")
	}
}

// TestQuestionDialogs: every question kind maps to its dialog, whose
// buttons answer it (and Escape says no).
func TestQuestionDialogs(t *testing.T) {
	key := func(p ...string) string { return "1:" + strings.Join(p, ":") }
	st := session.State{Session: 1, CoopBackup: `C:\backups\x`}
	bindQ := session.Question{ID: "q1", Kind: session.QuestionBind, FileName: "Link", FileSlot: 2, World: worldA, Details: []string{"preflight: subset"}}
	d := questionDialog(bindQ, st, key)
	if d.Kind != dlgBind || d.Title != "Bind “Link” (File 2) to world 01234567…?" || d.Cancel != "answer-no:q1" || !d.AutoYes || !d.Live {
		t.Fatalf("bind dialog %+v", d)
	}
	if d.Actions[0] != (DialogAction{ID: actYes, Arg: "q1", Label: "Bind", Primary: true}) || d.Actions[1].ID != actNo || d.Actions[1].Label != "Not now" {
		t.Fatalf("bind actions %+v", d.Actions)
	}
	if !slices.Equal(d.List, bindQ.Details) || !strings.Contains(d.Note, `C:\backups\x`) || d.NoteAction.ID != actBackups {
		t.Fatalf("bind list/note %+v", d)
	}
	fresh := bindQ
	fresh.TagWorld, fresh.MaybeFresh = "fedcba9876543210", true
	if d := questionDialog(fresh, st, key); !strings.Contains(d.Text, "may be a fresh bind") {
		t.Errorf("maybe-fresh bind: %q", d.Text)
	}
	scrub := session.Question{ID: "q2", Kind: session.QuestionScrub, FileName: "Link", FileSlot: 1, TagWorld: worldA, Failed: []string{"V2 failed: slot"}}
	d = questionDialog(scrub, st, key)
	if d.Kind != dlgScrub || d.Title != "This file carries a co-op tag from another file" || d.Actions[0].Label != "Remove the tag" || d.Actions[1].Label != "Leave it" || d.Actions[0].Arg != "q2" || !slices.Equal(d.List, scrub.Failed) {
		t.Fatalf("scrub dialog %+v", d)
	}
	scrub.MaybeFresh = true
	if d := questionDialog(scrub, st, key); !strings.Contains(d.Text, "only that world's host can tell") {
		t.Errorf("maybe-fresh scrub: %q", d.Text)
	}

	// Through the Bridge: the oldest open question is the state's dialog,
	// and Answer answers it.
	b, e, _ := newCoopBridge(t)
	if err := b.Join(JoinRequest{Addr: "127.0.0.1", Name: "Link"}); err != nil {
		t.Fatal(err)
	}
	out := e.out(t)
	answers := make(chan bool, 1)
	go func() { answers <- out.Ask(b.ctx, bindQ) }()
	var v StateView
	waitFor(t, func() bool { v = b.State(); return v.Dialog != nil })
	if v.Dialog.Kind != dlgBind || v.Dialog.Actions[0].Arg != "q1" {
		t.Fatalf("state dialog %+v", v.Dialog)
	}
	if err := b.Answer(v.Dialog.Actions[0].Arg, true); err != nil {
		t.Fatal(err)
	}
	if !<-answers {
		t.Fatal("Bind answered no")
	}
	if v := b.State(); v.Dialog != nil {
		t.Fatalf("dialog after the answer: %+v", v.Dialog)
	}
}

// TestAdviceAndRequestDialogs: far-behind and needs-merge advice as
// option cards (a joiner asks the host, the host allows it itself), no
// backup as a warning with the user-folder buttons; a joiner's merge
// request as the host's Allow dialog.
func TestAdviceAndRequestDialogs(t *testing.T) {
	key := func(p ...string) string { return "1:" + strings.Join(p, ":") }
	joiner := session.State{Session: 1, Role: session.RoleJoin}
	host := session.State{Session: 1, Role: session.RoleHost}
	d := adviceDialog(session.Advice{ID: "a1", Kind: session.AdviceFarBehind}, joiner, key)
	if d.Title != "The world is far ahead of your file" || len(d.Cards) != 2 || d.Cards[0].Title != "Copy the host's save" || len(d.Cards[0].Steps) != 3 ||
		d.Cards[1].Action != actAskMerge || d.Cancel != "dismiss-advice:a1" {
		t.Fatalf("far behind %+v", d)
	}
	// The host's own file far behind: no "copy the host's save".
	d = adviceDialog(session.Advice{ID: "a1", Kind: session.AdviceFarBehind}, host, key)
	if len(d.Cards) != 2 || d.Cards[0].Title != "Load a file bound to this world" || len(d.Cards[0].Steps) != 0 || d.Cards[1].Action != actSetMerge {
		t.Fatalf("far behind, host %+v", d)
	}
	if d := adviceDialog(session.Advice{ID: "a2", Kind: session.AdviceNeedsMerge}, host, key); len(d.Cards) != 1 || d.Cards[0].Action != actSetMerge {
		t.Fatalf("needs merge, host %+v", d)
	}
	if d := adviceDialog(session.Advice{ID: "a2", Kind: session.AdviceNeedsMerge}, joiner, key); len(d.Cards) != 1 || d.Cards[0].Action != actAskMerge {
		t.Fatalf("needs merge, joiner %+v", d)
	}
	d = adviceDialog(session.Advice{ID: "a3", Kind: session.AdviceNoBackup, Text: "co-op: no backup (denied)"}, joiner, key)
	if d.Title != "No memory-card backup" || d.Detail != "co-op: no backup (denied)" || d.Actions[0].ID != actUserDir || d.Actions[1].ID != actSetup {
		t.Fatalf("no backup %+v", d)
	}

	st := session.State{Session: 1, Lobby: &session.Lobby{Host: true}, Requests: []session.Request{{ID: "r1", Kind: "merge", From: "26.1.2.3"}}}
	d = coopDialog(st, true, masker{})
	if d.Kind != dlgMergeReq || d.Actions[0] != (DialogAction{ID: actAllow, Arg: "r1", Label: "Allow merging", Primary: true}) || strings.Contains(d.Text, "26.1.2.3") {
		t.Fatalf("merge request %+v", d)
	}
	// Priority: the founding, then questions, then requests, then advice.
	st.Advice = []session.Advice{{ID: "a1", Kind: session.AdviceNeedsMerge}}
	st.Questions = []session.Question{{ID: "q1", Kind: session.QuestionBind}}
	if d := coopDialog(st, true, masker{}); d.Kind != dlgBind {
		t.Fatalf("with a question open: %+v", d)
	}
	st.Founding = &session.Founding{Phase: session.FoundingWaiting, Why: "the pause menu is open"}
	if d := coopDialog(st, true, masker{}); d.Kind != dlgFounding || d.Check == nil || d.Check.OK || !strings.Contains(d.Check.Text, "pause menu") {
		t.Fatalf("founding: %+v", d)
	}
	// Blocked on a file bound elsewhere: Remove the tag names the tag's
	// world; "Continue that world" only when it is on this computer; and
	// another file opens the dialog again (its own key).
	st.Founding = &session.Founding{Phase: session.FoundingBlocked, Blocker: session.BlockBound, TagWorld: worldA, File: "file slot 0"}
	d = coopDialog(st, true, masker{})
	if d.Actions[0].ID != actUnbind || d.Actions[0].Arg != worldA || !strings.Contains(d.Text, worldA) || !strings.Contains(d.Text, "isn't on this computer") || strings.Contains(d.Text, "Continue that world") {
		t.Fatalf("blocked: %+v", d)
	}
	st.Founding.TagWorldHere = true
	if d2 := coopDialog(st, true, masker{}); !strings.Contains(d2.Text, "Continue that world") || d2.Key != d.Key {
		t.Fatalf("blocked, world here: %+v", d2)
	}
	st.Founding.File = "file slot 1"
	if d2 := coopDialog(st, true, masker{}); d2.Key == d.Key {
		t.Fatalf("another file kept the closed dialog's key %q", d2.Key)
	}
	st.Founding = &session.Founding{Phase: session.FoundingFounded, World: worldA}
	if d := coopDialog(st, true, masker{}); d.Kind != dlgBind {
		t.Fatalf("founded with a question open: %+v", d)
	}
	st.Questions, st.Requests, st.Advice = nil, nil, nil
	if d := coopDialog(st, true, masker{}); d.Kind != dlgFounded || !strings.Contains(d.Text, "Save in game so your card keeps the tag") {
		t.Fatalf("founded: %+v", d)
	}
	if d := coopDialog(st, false, masker{}); d != nil {
		t.Fatalf("a dialog after the session ended: %+v", d)
	}

	// A file bound to another world: Remove the tag.
	st = session.State{Session: 1, Lobby: &session.Lobby{}, Live: session.Live{CoopStatus: []session.CoopStatus{{FileState: "foreign", TagWorld: "fedcba9876543210", World: worldA}}}}
	if d := coopDialog(st, true, masker{}); d == nil || d.Kind != dlgForeign || d.Actions[0].ID != actUnbind || d.Actions[0].Arg != "fedcba9876543210" {
		t.Fatalf("foreign file: %+v", d)
	}
	// Refused before a welcome: the replica judged the tag against no
	// world (bound); the session's world comes from GO.
	st = session.State{Session: 1, Lobby: &session.Lobby{}, Coop: session.CoopOptions{On: true, World: worldA},
		Live: session.Live{CoopStatus: []session.CoopStatus{{StateCode: "refused", FileState: "bound", TagWorld: "fedcba9876543210"}}}}
	if d := coopDialog(st, true, masker{}); d == nil || d.Kind != dlgForeign {
		t.Fatalf("refused file bound elsewhere: %+v", d)
	}
	st.CoopStatus[0].TagWorld = worldA
	if d := coopDialog(st, true, masker{}); d != nil {
		t.Fatalf("a file bound to this world: %+v", d)
	}
}

// TestCoopPanelView: the co-op panel from the pipelines' structured
// status and the hub's, and its banner.
func TestCoopPanelView(t *testing.T) {
	cs := session.CoopStatus{Player: "Link", StateCode: "armed", World: worldA, Seq: 12, Written: 40, Escapes: 1, Failed: 0,
		Progression: []string{"progression granted: story"}, Puzzle: "puzzles: on", Combat: "combat: on", PartnerStage: "M_NewD2", PartnerRoom: 3}
	v := pipeView(cs)
	if v.Headline != "Armed: the world is written into your game" || v.Tone != "ok" || v.Paused || v.Partner != "M_NewD2, room 3" || v.Written != 40 || len(v.Progression) != 1 {
		t.Fatalf("armed %+v", v)
	}
	dry := cs
	dry.DryRun = true
	if v := pipeView(dry); v.Tone != "dry" || v.Paused {
		t.Fatalf("dry run %+v", v)
	}
	off := cs
	off.WritesOff = "you typed /coop off"
	if v := pipeView(off); !v.Paused || v.Tone != "warn" || !strings.Contains(v.Headline, "you typed /coop off") {
		t.Fatalf("writes off %+v", v)
	}
	unb := session.CoopStatus{StateCode: "unbound", Why: "no tag"}
	if v := pipeView(unb); v.Headline != "File not bound: no tag" {
		t.Fatalf("unbound %+v", v)
	}

	st := session.State{Session: 1, Coop: session.CoopOptions{On: true},
		Lobby: &session.Lobby{Member: 1, Members: []session.LobbyMember{{ID: 1, Name: "Link", Via: session.ViaApp}, {ID: 2, Name: "26.9.9.9", Via: session.ViaApp}}},
		Live: session.Live{CoopStatus: []session.CoopStatus{off}, CoopHub: &session.CoopHubStatus{World: worldA, Seq: 12, Merge: true,
			Peers: []session.CoopPeer{{Name: "Aryll", State: "bound", Stage: "sea", Room: 11}}}}}
	p := coopPanel(st, masker{})
	if len(p.Pipes) != 1 || p.Hub == nil || p.Hub.Peers[0].Where != "sea room 11" || !p.Hub.Merge || len(p.Partners) != 1 || strings.Contains(p.Partners[0], "26.9.9.9") {
		t.Fatalf("panel %+v", p)
	}
	b := coopBanner(st, true)
	if b == nil || b.Title != "Co-op writes turned off:" || b.Action != "coop-resume" {
		t.Fatalf("banner %+v", b)
	}
	st.CoopStatus = []session.CoopStatus{unb}
	if b := coopBanner(st, true); b == nil || b.Title != "File not bound." || b.Action != "" {
		t.Fatalf("unbound banner %+v", b)
	}
	// A file bound to another world ("Keep it" closed its dialog): the
	// banner still offers Remove the tag, for that tag's world.
	st.CoopStatus = []session.CoopStatus{{StateCode: "refused", FileState: "bound", TagWorld: "fedcba9876543210", World: worldA}}
	if b := coopBanner(st, true); b == nil || b.Action != actUnbind || b.ActionArg != "fedcba9876543210" {
		t.Fatalf("bound elsewhere banner %+v", b)
	}
	// The host's founding, its dialog closed: the banner says why it
	// waits and keeps Remove the tag for a bound file.
	st.CoopStatus = nil
	st.Founding = &session.Founding{Phase: session.FoundingWaiting, Why: "the pause menu is open"}
	if b := coopBanner(st, true); b == nil || b.Title != "Founding the new world:" || !strings.Contains(b.Text, "pause menu") || b.Action != "" {
		t.Fatalf("founding banner %+v", b)
	}
	st.Founding = &session.Founding{Phase: session.FoundingBlocked, Blocker: session.BlockBound, TagWorld: worldA, BlockText: "co-op: this file is bound to world " + worldA + " already"}
	if b := coopBanner(st, true); b == nil || b.Action != actUnbind || b.ActionArg != worldA {
		t.Fatalf("blocked founding banner %+v", b)
	}
	st.Founding.Blocker = session.BlockSecondQuest
	if b := coopBanner(st, true); b == nil || b.Action != "" {
		t.Fatalf("second quest banner %+v", b)
	}
	st.Founding = &session.Founding{Phase: session.FoundingFounded, World: worldA}
	if b := coopBanner(st, true); b != nil {
		t.Fatalf("banner after the founding %+v", b)
	}
}

// TestCoopButtons: the panel's buttons are the console's verbs; a typed
// "/..." line in the chat box goes to the console while co-op runs;
// Copy status is /status's text; Remove the tag reaches the engine.
func TestCoopButtons(t *testing.T) {
	b, e, _ := newCoopBridge(t)
	if err := b.Host(HostRequest{Name: "Link"}); err != nil {
		t.Fatal(err)
	}
	out := e.out(t)
	if err := b.CoopCommand("refresh"); !errors.Is(err, session.ErrNoCoop) {
		t.Fatalf("a button before co-op runs: %v", err)
	}
	out.(session.CoopOutput).Coop(session.CoopOptions{On: true, World: worldA})
	for _, btn := range []string{"refresh", "warp", "pause", "resume", "status"} {
		if err := b.CoopCommand(btn); err != nil {
			t.Fatal(err)
		}
	}
	if err := b.CoopCommand("explode"); err == nil {
		t.Error("an unknown button ran")
	}
	e.cmu.Lock()
	var verbs []session.Verb
	for _, c := range e.console {
		verbs = append(verbs, c.Verb)
	}
	e.live = session.Live{CoopStatus: []session.CoopStatus{{Lines: []string{"co-op: Link: armed", "co-op: Link:   story"}}}}
	e.cmu.Unlock()
	if want := []session.Verb{session.VerbRefresh, session.VerbWarp, session.VerbCoopOff, session.VerbCoopOn, session.VerbStatus}; !slices.Equal(verbs, want) {
		t.Fatalf("verbs %v, want %v", verbs, want)
	}
	if got := b.CoopStatusText(); got != "co-op: Link: armed\nco-op: Link:   story" {
		t.Fatalf("status text %q", got)
	}
	if _, err := b.SendChat("/warp"); err != nil {
		t.Fatal(err)
	}
	e.cmu.Lock()
	lines := slices.Clone(e.lines)
	e.cmu.Unlock()
	if !slices.Equal(lines, []string{"/warp"}) {
		t.Fatalf("console lines %v", lines)
	}
	if err := b.UnbindFile(worldA); err != nil || e.unbinds != 1 || e.unbound != worldA {
		t.Fatalf("UnbindFile %v %d %q", err, e.unbinds, e.unbound)
	}
	if err := b.SetAutoYes(true); err != nil || !b.State().Coop.AutoYes {
		t.Fatalf("SetAutoYes: %v", err)
	}
}

// TestSyncLine: the sync feed keeps the co-op lines users care about
// and leaves the start listing to All logs.
func TestSyncLine(t *testing.T) {
	for line, want := range map[string]bool{
		"co-op: set t03 DRC chest 4 (0x803C5000 |= 0x10)":                            true,
		"co-op: holding the boss's bits back: the boss stage reads them every frame": true,
		"co-op: armed in world 0123456789abcdef: written to this game: chests":       true,
		"[srv 12:00:00] co-op: Aryll (co-op 1) bound":                                true,
		"Co-op world 0123456789abcdef at seq 3 (dry run ...)":                        true,
		"co-op writes are off for Bravo: you typed /coop off. Pose sync goes on":     true,
		"co-op:   slot A GCI folder C:\\x (default, in use):":                        false,
		"co-op: stage tables 3d70 ...":                                               false,
		"co-op: backed up 1 memory-card file(s) to C:\\b":                            false,
		"Playing on Dolphin (pid 4): remote Links appear in your game.":              false,
	} {
		if got := syncLine(log.Info, line); got != want {
			t.Errorf("syncLine(%q) = %v", line, got)
		}
	}
	v := eventView(session.Event{Kind: session.EventLog, Level: log.OK, Text: "co-op: bound the file to world x"}, masker{})
	if !v.Sync {
		t.Error("an event of a sync line isn't marked")
	}
}

// waitFor polls ok for 5 s.
func waitFor(t *testing.T, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatal("timed out")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// TestFileHintToLobby: with co-op chosen, this player's card preview is
// in the state and its short hint goes to the lobby once (again in a
// new session); back to Off, the hint is cleared.
func TestFileHintToLobby(t *testing.T) {
	b, e, _ := newCoopBridge(t)
	b.findSave = func(string) (cardSave, error) { return cardWith(t, fileSpec{"Link", 3, bind.WorldID{}}), nil }
	hints := func() []string {
		e.mu.Lock()
		defer e.mu.Unlock()
		var out []string
		for _, c := range e.fakeEngine.cmds {
			if c.Kind == session.LobbySetFile {
				out = append(out, c.Text)
			}
		}
		return out
	}
	for round := 1; round <= 2; round++ {
		if err := b.Join(JoinRequest{Addr: "127.0.0.1", Name: "Link"}); err != nil {
			t.Fatal(err)
		}
		out := e.out(t)
		l := session.Lobby{Member: 1, Phase: session.LobbyPhaseLobby, Settings: session.LobbySettings{Coop: worldA},
			Members: []session.LobbyMember{{ID: 1, Name: "Link", Via: session.ViaApp}}}
		out.LobbyState(l)
		v := b.State()
		b.State()
		if v.Coop == nil || v.Coop.Cards == nil || v.Coop.Cards.Files[0].Name != "Link" {
			t.Fatalf("round %d: coop view %+v", round, v.Coop)
		}
		if got := hints(); len(got) != 1 || got[0] != `File 1 "Link": unbound` {
			t.Fatalf("round %d: hints sent %q", round, got)
		}
		l.Settings.Coop = "off"
		out.LobbyState(l)
		if v := b.State(); v.Coop.Cards != nil {
			t.Fatalf("round %d: cards with co-op off", round)
		}
		if got := hints(); got[len(got)-1] != "" {
			t.Fatalf("round %d: hint not cleared: %q", round, got)
		}
		e.mu.Lock()
		e.fakeEngine.cmds = nil
		e.mu.Unlock()
		b.Leave()
	}
}
