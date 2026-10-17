package tui

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/internal/session"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/log"
	tea "github.com/charmbracelet/bubbletea"
)

// coopEngine is a fakeEngine with co-op: its sessions ask one question
// when asked to (ask), log the answer, and record the console lines.
type coopEngine struct {
	fakeEngine
	ask    chan session.Question
	answer chan bool
	worlds []session.CoopWorld

	mu    sync.Mutex
	lines []string
	opts  session.HostOptions
}

func newCoopEngine() *coopEngine {
	return &coopEngine{ask: make(chan session.Question, 1), answer: make(chan bool, 1)}
}

func (e *coopEngine) Host(ctx context.Context, _ context.CancelFunc, o session.HostOptions, out session.Output) error {
	e.mu.Lock()
	e.opts = o
	e.mu.Unlock()
	out.Log(log.OK, "session up")
	for {
		select {
		case <-ctx.Done():
			return nil
		case q := <-e.ask:
			e.answer <- out.Ask(ctx, q)
		}
	}
}

func (e *coopEngine) Command(c session.Command) error { return nil }

func (e *coopEngine) CommandLine(line string) error {
	e.mu.Lock()
	e.lines = append(e.lines, line)
	e.mu.Unlock()
	return nil
}

func (e *coopEngine) CoopWorlds() ([]session.CoopWorld, error) { return e.worlds, nil }

func TestConnect_CoopChoices(t *testing.T) {
	m := newConnect()
	m.worlds = []session.CoopWorld{
		{ID: "0123456789abcdef", Seq: 42},
		{ID: "1111111111111111", Err: "damaged"},
		{ID: "fedcba9876543210", Seq: 3, InUse: true},
	}
	var labels []string
	for _, c := range m.coopChoices() {
		labels = append(labels, c.label)
	}
	want := []string{"Off", "New world (from the file you load)", "Continue 0123456789abcdef (seq 42)", "Continue fedcba9876543210 (seq 3), in use?"}
	if strings.Join(labels, "|") != strings.Join(want, "|") {
		t.Errorf("host choices %q, want %q", labels, want)
	}

	// Tab to the co-op row, step through it with right and enter, back
	// with left (wrapping), then connect.
	m.nameInput = "Link"
	m = send(m, tea.KeyMsg{Type: tea.KeyTab})
	if !m.isCoopFocused() {
		t.Fatalf("focus %d after one tab, want the co-op row", m.focused)
	}
	m = send(m, tea.KeyMsg{Type: tea.KeyRight}, tea.KeyMsg{Type: tea.KeyEnter})
	if m.hostCoop != 2 {
		t.Errorf("host choice %d, want 2", m.hostCoop)
	}
	m = send(m, tea.KeyMsg{Type: tea.KeyLeft}, tea.KeyMsg{Type: tea.KeyLeft}, tea.KeyMsg{Type: tea.KeyLeft})
	if m.hostCoop != 3 {
		t.Errorf("host choice %d after wrapping left, want 3", m.hostCoop)
	}
	if !strings.Contains(plain(m.view(80)), "Continue fedcba9876543210") {
		t.Errorf("the co-op row doesn't show the choice:\n%s", plain(m.view(80)))
	}
	m.focused = m.maxFocus()
	_, cmd := m.update(tea.KeyMsg{Type: tea.KeyEnter})
	msg := cmd().(connectedMsg)
	if msg.coop != (session.CoopOptions{On: true, World: "fedcba9876543210"}) {
		t.Errorf("host co-op %+v", msg.coop)
	}

	// A joiner's choice is its own: off, or on.
	m.role, m.focused = 1, 1
	m = send(m, tea.KeyMsg{Type: tea.KeyRight})
	if c := m.coopChoices()[m.coopIndex()]; c.opts != (session.CoopOptions{On: true}) || m.hostCoop != 3 {
		t.Errorf("join choice %+v (host's %d)", c, m.hostCoop)
	}
	m = send(m, tea.KeyMsg{Type: tea.KeyRight})
	if m.coopIndex() != 0 {
		t.Errorf("join choice %d after wrapping, want off", m.coopIndex())
	}
}

// TestConnect_CoopRowFits: a long world choice is cut, never wrapped, so
// the form keeps its height down to the width the Start Hosting button
// needs.
func TestConnect_CoopRowFits(t *testing.T) {
	m := newConnect()
	m.worlds = []session.CoopWorld{{ID: "0123456789abcdef", Seq: 123456789, InUse: true}}
	m.hostCoop, m.focused = 2, 1
	wantH := len(strings.Split(m.view(80), "\n"))
	for _, w := range []int{80, 46, 30, 24} {
		out := m.view(w)
		checkFits(t, out, w, wantH)
	}
}

// TestDashboard_CoopQuestion: a co-op question is shown in place of the
// log and answered with y; n answers no; neither key reaches the chat.
// A key that comes as the question is shown, or hard on another key, is
// typed for something else and never answers it (docs/08 item 164).
func TestDashboard_CoopQuestion(t *testing.T) {
	eng := newCoopEngine()
	ctrl := session.New(eng, session.Options{})
	m, err := startDashboard(ctrl, "Host", "Me", "", session.CoopOptions{On: true}, 80)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(m.leave)
	clk := time.Unix(1000, 0)
	m.now = func() time.Time { return clk }
	key := func(k string) {
		t.Helper()
		m, _ = m.update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)})
	}
	for len(m.logs) == 0 {
		m = nextEvent(t, m) // up to "session up": Host has its options
	}
	eng.mu.Lock()
	if !eng.opts.Coop.On || !m.coop {
		t.Errorf("the session's co-op options %+v, dashboard co-op %v", eng.opts.Coop, m.coop)
	}
	eng.mu.Unlock()
	for _, yes := range []bool{true, false} {
		eng.ask <- session.Question{Kind: session.QuestionBind, Player: "Me", Title: "Bind it?",
			Body: "Bind this save file to co-op world 0123456789abcdef?\n  File 2 \"Link\"\nBind it?", Details: []string{"preflight: subset"}}
		for len(m.questions) == 0 {
			m = nextEvent(t, m)
		}
		out := plain(m.view(80, 24))
		for _, want := range []string{"Co-op question for Me", "Bind it?", "preflight: subset", "y: yes, write it", "y: yes    n: no"} {
			if !strings.Contains(out, want) {
				t.Errorf("question view lacks %q:\n%s", want, out)
			}
		}
		answer, other := "n", "y"
		if yes {
			answer, other = "y", "n"
		}
		// Someone typing as the question comes up: the other answer's
		// key, at once and then on a steady run of keys, is swallowed
		// (answering it would reach eng.answer before the real one).
		key(other)
		for i := 0; i < 5; i++ {
			clk = clk.Add(questionSettle / 2)
			key(other)
		}
		if len(m.questions) == 0 {
			t.Fatal("a key typed as the question came up closed it")
		}
		// A pause, then the answer.
		clk = clk.Add(questionSettle)
		key(answer)
		if m.chatInput.Value() != "" {
			t.Errorf("the answer reached the chat input: %q", m.chatInput.Value())
		}
		select {
		case got := <-eng.answer:
			if got != yes {
				t.Errorf("answered %v, want %v", got, yes)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("the answer never reached the session")
		}
		for len(m.questions) > 0 {
			m = nextEvent(t, m)
		}
	}
}

// TestDashboard_ConsoleCommand: with co-op on, a line starting with '/'
// is a console command, not chat; without, it is chat as before.
func TestDashboard_ConsoleCommand(t *testing.T) {
	eng := newCoopEngine()
	ctrl := session.New(eng, session.Options{})
	m, err := startDashboard(ctrl, "Host", "Me", "", session.CoopOptions{On: true}, 80)
	if err != nil {
		t.Fatal(err)
	}
	m.chatInput.SetValue(" /warp Tetra ")
	m, _ = m.update(tea.KeyMsg{Type: tea.KeyEnter})
	eng.mu.Lock()
	lines := append([]string(nil), eng.lines...)
	eng.mu.Unlock()
	if len(lines) != 1 || lines[0] != "/warp Tetra" || len(eng.sentChat()) != 0 || len(m.chatLines) != 0 {
		t.Errorf("console %q, chat sent %q, chat shown %q", lines, eng.sentChat(), m.chatLines)
	}
	if last := m.logs[len(m.logs)-1]; last.msg != "> /warp Tetra" {
		t.Errorf("the command isn't echoed in the log: %+v", last)
	}
	m.leave()

	m, err = startDashboard(ctrl, "Host", "Me", "", session.CoopOptions{}, 80)
	if err != nil {
		t.Fatal(err)
	}
	defer m.leave()
	m.chatInput.SetValue("/warp")
	m, _ = m.update(tea.KeyMsg{Type: tea.KeyEnter})
	if got := eng.sentChat(); len(got) != 1 || got[0] != "/warp" {
		t.Errorf("without co-op, /warp sent as chat %q", got)
	}
}

// TestDashboardView_QuestionFits: a long question in a busy dashboard
// still fits the terminal, and its question line stays on screen.
func TestDashboardView_QuestionFits(t *testing.T) {
	q := session.Question{Player: "Link", Body: strings.Repeat("a long line of the bind prompt that wraps across the panel\n", 12) + "Bind it?",
		Details: []string{"preflight: subset: the file is a subset", "the file adds: nothing", "the world adds: story events 2"}}
	for _, sz := range [][2]int{{80, 24}, {120, 30}, {40, 16}} {
		m := busyDashboard("Host")
		m.coop = true
		m.coopStatus = []session.CoopStatus{{Player: "Link", State: "armed", Writes: "writing"}}
		m.questions = []session.Question{q, q}
		out := m.view(sz[0], sz[1])
		checkFits(t, out, sz[0], sz[1])
		if p := plain(out); !strings.Contains(p, "Bind it?") || !strings.Contains(p, "y: yes, write it") || sz[0] >= 80 && !strings.Contains(p, "1 more waiting") {
			t.Errorf("%dx%d: the question or its count is off screen:\n%s", sz[0], sz[1], p)
		}
	}
}

func TestRenderStatusCoop(t *testing.T) {
	out := plain(renderStatusCoop(80, 2, 0, nil, []session.CoopStatus{
		{Player: "Link", State: "armed", Writes: "writing"},
		{Player: "Tetra", State: "off: not connected", Writes: "a dry run (--coop-dry-run)"},
	}))
	if !strings.Contains(out, "Co-op   Link: armed; writing  Tetra: off: not connected;") {
		t.Errorf("co-op line:\n%s", out)
	}
	if strings.Contains(plain(renderStatus(80, 2, 0, nil)), "Co-op") {
		t.Error("a co-op line without co-op")
	}
}

// TestDashboard_QuestionsFromSnapshot: the snapshot's open questions are
// the dashboard's, so a question event the subscription dropped does not
// hide a question (the session would wait on it unseen) or leave a
// closed one up taking every key; an ended session shows none.
func TestDashboard_QuestionsFromSnapshot(t *testing.T) {
	eng := newCoopEngine()
	ctrl := session.New(eng, session.Options{})
	m, err := startDashboard(ctrl, "Host", "Me", "", session.CoopOptions{On: true}, 80)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(m.leave)
	clk := time.Unix(1000, 0)
	m.now = func() time.Time { return clk }
	for len(m.logs) == 0 {
		m = nextEvent(t, m)
	}

	// EventQuestion lost: the next status tick shows the question, which
	// then settles like any other.
	eng.ask <- session.Question{Kind: session.QuestionScrub, Player: "Me", Title: "Scrub it?", Body: "Scrub it?"}
	deadline := time.Now().Add(5 * time.Second)
	for len(m.questions) == 0 && time.Now().Before(deadline) {
		m = m.refreshStatus() // never drains the events
	}
	if len(m.questions) != 1 || m.questions[0].Title != "Scrub it?" {
		t.Fatalf("questions from the snapshot %+v", m.questions)
	}
	id := m.questions[0].ID
	m, _ = m.update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("n")})
	if len(m.questions) != 1 {
		t.Fatal("a key as the question came up answered it")
	}
	// The late EventQuestion adds no second copy.
	m = m.applyEvent(session.Event{Kind: session.EventQuestion, Session: m.sessID, Question: m.questions[0]})
	if len(m.questions) != 1 || m.questions[0].ID != id {
		t.Fatalf("questions after the late event %+v", m.questions)
	}
	clk = clk.Add(questionSettle)
	m, _ = m.update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("n")})
	select {
	case got := <-eng.answer:
		if got {
			t.Error("answered yes")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the answer never reached the session")
	}

	// EventQuestionClosed lost: the question is still shown, but no
	// longer open. A tick drops it; so does a key, whose Answer finds it
	// gone (ErrNoQuestion).
	for len(ctrl.Snapshot().Questions) > 0 {
		time.Sleep(time.Millisecond)
	}
	stale := m.questions
	m = m.refreshStatus()
	if len(m.questions) != 0 {
		t.Errorf("a closed question after a tick: %+v", m.questions)
	}
	m = m.setQuestions(stale)
	clk = clk.Add(questionSettle)
	m, _ = m.update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")})
	if len(m.questions) != 0 {
		t.Errorf("a closed question after a key: %+v", m.questions)
	}
	m.chatInput.SetValue("")
	m, _ = m.update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("h")})
	if m.chatInput.Value() != "h" {
		t.Errorf("the chat input after the stale question went: %q", m.chatInput.Value())
	}

	// The session ends with a question shown: it goes.
	m = m.setQuestions(stale)
	m = m.applyEvent(session.Event{Kind: session.EventEnded, Session: m.sessID, Text: "left"})
	if len(m.questions) != 0 {
		t.Errorf("questions after the end %+v", m.questions)
	}
	if strings.Contains(plain(m.view(80, 24)), "Co-op question") {
		t.Error("an ended dashboard shows a question")
	}
}
