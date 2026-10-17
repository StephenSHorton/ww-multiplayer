package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/mods/coop/bind"
	"github.com/StephenSHorton/ww-multiplayer/mods/coop/replica"
	"github.com/StephenSHorton/ww-multiplayer/mods/coop/world"
	"github.com/StephenSHorton/ww-multiplayer/mods/multiplayer/net"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/fakegame"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mem"
)

func TestExtractCoopFlags(t *testing.T) {
	const id = "0123456789abcdef"
	tests := []struct {
		name    string
		args    []string
		want    coopFlags
		rest    []string
		wantErr string
	}{
		{name: "none", args: nil, rest: nil},
		{name: "no co-op", args: []string{"Link", "--port", "9000"}, rest: []string{"Link", "--port", "9000"}},
		// Without --coop the other co-op options are not taken: the
		// command's own parser sees the line exactly as before.
		{name: "--yes alone", args: []string{"--yes", "Link"}, rest: []string{"--yes", "Link"}},
		{name: "--coop-merge alone", args: []string{"--coop-merge"}, rest: []string{"--coop-merge"}},
		{name: "after --", args: []string{"Link", "--", "--coop"}, rest: []string{"Link", "--", "--coop"}},
		{name: "M3's options alone", args: []string{"--coop-dry-run", "--coop-live-switches", "--coop-iso", "ww.iso"}, rest: []string{"--coop-dry-run", "--coop-live-switches", "--coop-iso", "ww.iso"}},

		{name: "--coop", args: []string{"--coop"}, want: coopFlags{on: true}, rest: []string{}},
		{name: "anywhere", args: []string{"Link", "--coop", "--port", "9000"}, want: coopFlags{on: true}, rest: []string{"Link", "--port", "9000"}},
		{name: "all", args: []string{"--coop=new", "--coop-merge", "Link", "--yes"}, want: coopFlags{on: true, world: coopWorldNew, merge: true, yes: true}, rest: []string{"Link"}},
		{name: "NEW", args: []string{"--coop=NEW"}, want: coopFlags{on: true, world: coopWorldNew}, rest: []string{}},
		{name: "world ID", args: []string{"--coop=" + strings.ToUpper(id)}, want: coopFlags{on: true, world: id}, rest: []string{}},
		{name: "twice the same", args: []string{"--coop", "--coop=new", "--coop=new"}, want: coopFlags{on: true, world: coopWorldNew}, rest: []string{}},
		{name: "-- ends the options", args: []string{"--coop", "Link", "--", "--yes"}, want: coopFlags{on: true}, rest: []string{"Link", "--", "--yes"}},
		{name: "M3", args: []string{"Link", "--coop", "--coop-dry-run", "--coop-live-switches", "--coop-iso", "ww.iso"}, want: coopFlags{on: true, dryRun: true, liveSwitches: true, iso: "ww.iso"}, rest: []string{"Link"}},
		{name: "--coop-iso=", args: []string{"--coop-iso=C:/Roms/ww.iso", "--coop"}, want: coopFlags{on: true, iso: "C:/Roms/ww.iso"}, rest: []string{}},
		{name: "--coop-iso after --", args: []string{"--coop", "--", "--coop-iso", "x"}, want: coopFlags{on: true}, rest: []string{"--", "--coop-iso", "x"}},

		{name: "bad world", args: []string{"--coop=xyz"}, wantErr: "--coop=new, or a world ID"},
		{name: "zero world", args: []string{"--coop=0000000000000000"}, wantErr: "--coop=new, or a world ID"},
		{name: "empty world", args: []string{"--coop="}, wantErr: "--coop=new, or a world ID"},
		{name: "two worlds", args: []string{"--coop=new", "--coop=" + id}, wantErr: "give one world"},
		{name: "--coop-iso without a path", args: []string{"--coop", "--coop-iso"}, wantErr: "needs the path"},
		{name: "two discs", args: []string{"--coop", "--coop-iso=a", "--coop-iso=b"}, wantErr: "give one disc image"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, rest, err := extractCoopFlags(tt.args)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v, want one saying %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want || !reflect.DeepEqual(rest, tt.rest) {
				t.Errorf("extractCoopFlags(%q) = %+v, %q; want %+v, %q", tt.args, got, rest, tt.want, tt.rest)
			}
		})
	}
}

func TestCoopFlagsForJoin(t *testing.T) {
	tests := []struct {
		f       coopFlags
		wantErr string
	}{
		{coopFlags{}, ""},
		{coopFlags{on: true}, ""},
		{coopFlags{on: true, yes: true}, ""},
		{coopFlags{on: true, world: coopWorldNew}, "the host picks the world"},
		{coopFlags{on: true, world: "0123456789abcdef"}, "the host picks the world"},
		{coopFlags{on: true, merge: true}, "the host's choice"},
		{coopFlags{on: true, liveSwitches: true}, "--coop-live-switches is the host's choice"},
		{coopFlags{on: true, dryRun: true, iso: "ww.iso"}, ""},
	}
	for _, tt := range tests {
		err := tt.f.forJoin()
		if (err == nil) != (tt.wantErr == "") || err != nil && !strings.Contains(err.Error(), tt.wantErr) {
			t.Errorf("%+v.forJoin() = %v, want %q", tt.f, err, tt.wantErr)
		}
	}
}

// coWorld creates a world under root, founded at created, with img merged.
func coWorld(t *testing.T, root string, id world.ID, created time.Time, img *world.Image) {
	t.Helper()
	w := world.New(id, created)
	if img != nil {
		w.Merge(img)
	}
	st, err := world.Create(root, w, world.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Stop(); err != nil {
		t.Fatal(err)
	}
}

func TestSelectCoopWorld(t *testing.T) {
	root := t.TempDir()
	if ws, err := listCoopWorlds(filepath.Join(root, "missing")); err != nil || ws != nil {
		t.Errorf("a root that does not exist: %v, %v", ws, err)
	}
	if _, err := selectCoopWorld(root, ""); err == nil || !strings.Contains(err.Error(), "--coop=new") {
		t.Errorf("no world: %v", err)
	}
	var out bytes.Buffer
	if err := coopWorldList(&out, root); err != nil || !strings.Contains(out.String(), "No co-op worlds") {
		t.Errorf("list of none: %v\n%s", err, out.String())
	}

	old, young := world.ID{1, 2, 3, 4, 5, 6, 7, 8}, world.ID{8, 7, 6, 5, 4, 3, 2, 1}
	coWorld(t, root, old, time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC), nil)
	// What a world list skips: a stray file, a directory that is no world
	// ID, and one without a world file.
	if err := os.WriteFile(filepath.Join(root, "notes.txt"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	for _, d := range []string{"backups", "00000000000000ff"} {
		if err := os.Mkdir(filepath.Join(root, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if id, err := selectCoopWorld(root, ""); err != nil || id != old {
		t.Errorf("the only world: %s, %v", id, err)
	}

	coWorld(t, root, young, time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC), nil)
	_, err := selectCoopWorld(root, "")
	if err == nil || !strings.Contains(err.Error(), "--coop=<world-id>") || !strings.Contains(err.Error(), old.String()) || !strings.Contains(err.Error(), young.String()) {
		t.Errorf("two worlds: %v", err)
	}
	if id, err := selectCoopWorld(root, old.String()); err != nil || id != old {
		t.Errorf("named: %s, %v", id, err)
	}
	coSetProcState(t, coProcs(map[int]string{os.Getpid(): "self"}))
	if _, _, err := writeCoopMarker(world.Path(root, old)); err != nil {
		t.Fatal(err)
	}
	ws, err := listCoopWorlds(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(ws) != 2 || ws[0].id != young || ws[1].id != old || ws[1].marker == nil || ws[1].use != markerSelf || ws[0].marker != nil {
		t.Errorf("list: %+v, want the two worlds newest first, the old one in use", ws)
	}
	out.Reset()
	if err := coopWorldList(&out, root); err != nil || !strings.Contains(out.String(), "in use: pid") || strings.Index(out.String(), young.String()) > strings.Index(out.String(), old.String()) {
		t.Errorf("list: %v\n%s", err, out.String())
	}

	// A marker whose process has ended is stale, and says so.
	if err := os.WriteFile(filepath.Join(world.Path(root, young), coopMarkerName), []byte("pid 4242 since 2026-10-15 17:16:19\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := coopWorldList(&out, root); err != nil || !strings.Contains(out.String(), young.String()+"  founded") ||
		!strings.Contains(out.String(), "stale session marker (pid 4242 since 2026-10-15 17:16:19; that process has ended)") || strings.Count(out.String(), "in use") != 1 {
		t.Errorf("list with a stale marker: %v\n%s", err, out.String())
	}

	// One of a running process is in use, and says so when it is judged
	// by its pid alone, as block and the host start do.
	coSetProcState(t, coProcs(map[int]string{os.Getpid(): "self", 4243: "t1"}))
	for _, c := range []struct{ file, want string }{
		{"pid 4243 since 2026-10-15 17:16:19\nhost=" + coopHostname() + " start=t1\n", "in use: pid 4243 since 2026-10-15 17:16:19\n"},
		{"pid 4243 since 2026-10-15 17:16:19\n", "in use: pid 4243 since 2026-10-15 17:16:19 (by its pid alone: this marker has no start time)\n"},
	} {
		if err := os.WriteFile(filepath.Join(world.Path(root, young), coopMarkerName), []byte(c.file), 0o644); err != nil {
			t.Fatal(err)
		}
		out.Reset()
		if err := coopWorldList(&out, root); err != nil || !strings.Contains(out.String(), c.want) {
			t.Errorf("list with marker %q: %v\n%s\nwant %q", c.file, err, out.String(), c.want)
		}
	}
}

func TestCoopMarker(t *testing.T) {
	dir := t.TempDir()
	if m, ok := readCoopMarker(dir); ok {
		t.Fatalf("a marker before any: %q", m)
	}
	coSetProcState(t, coProcs(map[int]string{os.Getpid(): "self", 4243: "t1", 4244: ""}))
	prev, had, err := writeCoopMarker(dir)
	if err != nil || had {
		t.Fatalf("first marker: %q, %v, %v", prev, had, err)
	}
	first, ok := readCoopMarker(dir)
	if !ok || !strings.HasPrefix(first.text, fmt.Sprintf("pid %d since ", os.Getpid())) || first.pid != os.Getpid() || first.host != coopHostname() || first.start != "self" {
		t.Errorf("marker %q: pid %d, host %q, start %q", first.text, first.pid, first.host, first.start)
	}
	if use, why := first.state(); use != markerSelf {
		t.Errorf("this process's marker: %v (%s), want markerSelf", use, why)
	}
	if prev, had, err := writeCoopMarker(dir); err != nil || !had || prev != first {
		t.Errorf("second marker replaced %q (start %q), %v, %v; want %q (start %q)", prev, prev.start, had, err, first, first.start)
	}

	// What a marker says, by pid, machine and start token. Pid 4242 has
	// ended, 4243 runs with start token t1, and 4244 runs with none.
	self := os.Getpid()
	here := coopHostname()
	tests := []struct {
		name, file string
		want       coopMarkerState
		why        string
	}{
		{"one line, its process ended", "pid 4242 since 2026-10-15 17:16:19\n", markerStale, ""},
		{"one line, its process runs", "pid 4243 since 2026-10-15 17:16:19\n", markerLive, "this marker has no start time"},
		{"one line, this process", fmt.Sprintf("pid %d since 2026-10-15 17:16:19\n", self), markerSelf, ""},
		{"this pid, started at another time", fmt.Sprintf("pid %d since 2026-10-15 17:16:19\nhost=%s start=other\n", self, here), markerStale, ""},
		{"its process runs", "pid 4243 since 2026-10-15 17:16:19\nhost=" + here + " start=t1\n", markerLive, ""},
		{"its pid reused", "pid 4243 since 2026-10-15 17:16:19\nhost=" + here + " start=t0\n", markerStale, ""},
		{"its process ended", "pid 4242 since 2026-10-15 17:16:19\nhost=" + here + " start=t1\n", markerStale, ""},
		{"no start token now", "pid 4244 since 2026-10-15 17:16:19\nhost=" + here + " start=t1\n", markerLive, "its start time can't be read"},
		{"no start token then", "pid 4243 since 2026-10-15 17:16:19\nhost=" + here + " start=\n", markerLive, "this marker has no start time"},
		{"no host", "pid 4242 since 2026-10-15 17:16:19\nhost= start=t1\n", markerStale, ""},
		{"another machine", "pid 4242 since 2026-10-15 17:16:19\nhost=elsewhere-" + here + " start=t1\n", markerUnknown, "another machine, elsewhere-"},
		{"no pid", "pid x since 2026-10-15 17:16:19\n", markerUnknown, "names no process"},
		{"pid 0", "pid 0 since 2026-10-15 17:16:19\n", markerUnknown, "names no process"},
		{"empty", "", markerUnknown, "names no process"},
	}
	for _, tt := range tests {
		if err := os.WriteFile(filepath.Join(dir, coopMarkerName), []byte(tt.file), 0o644); err != nil {
			t.Fatal(err)
		}
		m, ok := readCoopMarker(dir)
		if !ok {
			t.Errorf("%s: no marker", tt.name)
			continue
		}
		if use, why := m.state(); use != tt.want || !strings.Contains(why, tt.why) || (tt.why == "") != (why == "") {
			t.Errorf("%s: %v (%q), want %v (%q)", tt.name, use, why, tt.want, tt.why)
		}
		if line, _, _ := strings.Cut(tt.file, "\n"); m.text != line {
			t.Errorf("%s: shown as %q, want the first line %q", tt.name, m.text, line)
		}
	}

	// A process that can't be checked leaves the marker in use.
	coSetProcState(t, func(int) (bool, string, error) { return false, "", errors.New("access denied") })
	if err := os.WriteFile(filepath.Join(dir, coopMarkerName), []byte("pid 4242 since 2026-10-15 17:16:19\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	denied, _ := readCoopMarker(dir)
	if use, why := denied.state(); use != markerUnknown || why != "access denied" {
		t.Errorf("procState fails: %v (%q)", use, why)
	}

	// Whatever the file holds, it reaches the terminal as one short line,
	// and names no process.
	if err := os.WriteFile(filepath.Join(dir, coopMarkerName), []byte("evil\x1b[2J\r\n"+strings.Repeat("x", 200)), 0o644); err != nil {
		t.Fatal(err)
	}
	if m, ok := readCoopMarker(dir); !ok || len(m.text) > 80 || strings.ContainsAny(m.text, "\x1b\r\n") || m.pid != 0 {
		t.Errorf("sanitized marker %q, pid %d", m.text, m.pid)
	}
	if err := os.WriteFile(filepath.Join(dir, coopMarkerName), []byte("pid 4242 "+strings.Repeat("é", 50)+"\nhost=\x1b[2J start=t1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if m, ok := readCoopMarker(dir); !ok || len(m.text) > 80 || strings.ToValidUTF8(m.text, "") != m.text || m.pid != 4242 {
		t.Errorf("long marker %q, pid %d", m.text, m.pid)
	} else if _, why := m.state(); !strings.Contains(why, "another machine") || strings.Contains(why, "\x1b") {
		t.Errorf("a host with a control character: %q", why)
	}
	removeCoopMarker(dir)
	if m, ok := readCoopMarker(dir); ok {
		t.Errorf("a marker after remove: %q", m)
	}

	// With the real check, this process's own marker is its own.
	if procStateSupported {
		coSetProcState(t, procState)
		if _, _, err := writeCoopMarker(dir); err != nil {
			t.Fatal(err)
		}
		m, _ := readCoopMarker(dir)
		if use, why := m.state(); use != markerSelf {
			t.Errorf("this process's marker %q (start %q), checked: %v (%s)", m, m.start, use, why)
		}
	}
}

// coAnswering is a coLog that types answer(question) into w when ask waits
// for one: the question is the last line ending in "?" it logged. ask drops
// what was read before it asked, so an answer typed earlier would be lost.
func coAnswering(w io.Writer, answer func(q string) string) *coLog {
	var mu sync.Mutex
	var q string
	return &coLog{on: func(msg string) {
		mu.Lock()
		defer mu.Unlock()
		switch {
		case strings.HasSuffix(msg, "?"):
			q = msg
		case strings.Contains(msg, "Type y and press Enter"):
			io.WriteString(w, answer(q)+"\n")
		}
	}}
}

// coInput is a pipe for a prompter's input, closed when the test ends.
func coInput(t *testing.T) (*io.PipeReader, *io.PipeWriter) {
	r, w := io.Pipe()
	t.Cleanup(func() { w.Close() })
	return r, w
}

func TestCoopPrompter(t *testing.T) {
	ctx := context.Background()

	t.Run("--yes", func(t *testing.T) {
		// A "n" waiting in stdin is no answer: --yes answers every
		// question, and the reader runs only for the commands.
		p := newCoopPrompter(true, strings.NewReader("n\n/help\n"))
		log := &coLog{}
		if !p.ask(ctx, log, "Bind this?\n  details\nBind it?") {
			t.Error("--yes answered no")
		}
		if !log.has("Bind this?") || !log.has("  details") || !log.has("yes (--yes)") {
			t.Errorf("log:\n%s", log)
		}
		if l, ok := <-p.cmds; !ok || l.text != "/help" {
			t.Errorf("the command read: %q, %v", l.text, ok)
		}
	})

	t.Run("a command is no answer", func(t *testing.T) {
		r, w := coInput(t)
		p := newCoopPrompter(false, r)
		if p.ask(ctx, coAnswering(w, func(string) string { return "/warp\nn" }), "Bind it?") {
			t.Fatal("answered yes")
		}
		select {
		case l := <-p.cmds:
			if l.text != "/warp" {
				t.Errorf("command %q", l.text)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("the command never reached the console")
		}
	})

	for _, tt := range []struct {
		typed string
		want  bool
	}{
		{"y", true}, {"yes", true}, {" Yes ", true}, {"Y", true},
		{"n", false}, {"", false}, {"no", false}, {"yes please", false},
	} {
		t.Run(fmt.Sprintf("typed %q", tt.typed), func(t *testing.T) {
			r, w := coInput(t)
			log := coAnswering(w, func(string) string { return tt.typed })
			if got := newCoopPrompter(false, r).ask(ctx, log, "Scrub it?"); got != tt.want {
				t.Errorf("ask = %v, want %v\n%s", got, tt.want, log)
			}
		})
	}

	t.Run("typed before the question", func(t *testing.T) {
		r, w := coInput(t)
		p := newCoopPrompter(false, r)
		io.WriteString(w, "y\n")
		if p.ask(ctx, coAnswering(w, func(string) string { return "n" }), "Bind it?") {
			t.Error("a line typed before the question answered it")
		}
	})

	t.Run("a triple-tapped yes", func(t *testing.T) {
		// Three lines typed at once for the first question: the two extra
		// ones were typed before the second was asked.
		r, w := coInput(t)
		p := newCoopPrompter(false, r)
		if !p.ask(ctx, coAnswering(w, func(string) string { return "y\ny\ny" }), "Bind it?") {
			t.Fatal("the first question was not answered yes")
		}
		if p.ask(ctx, coAnswering(w, func(string) string { return "n" }), "Scrub it?") {
			t.Error("a line typed for the first question answered the second")
		}
	})

	t.Run("a line waiting in stdin", func(t *testing.T) {
		// The real os.Stdin, a pipe that already holds a "y" when the
		// prompter starts and asks at once.
		for range 5 {
			pr, pw, err := os.Pipe()
			if err != nil {
				t.Fatal(err)
			}
			if _, err := pw.WriteString("y\n"); err != nil {
				t.Fatal(err)
			}
			stdin := os.Stdin
			os.Stdin = pr
			p := newCoopPrompter(false, nil)
			os.Stdin = stdin
			got := p.ask(ctx, coAnswering(pw, func(string) string { return "n" }), "Scrub it?")
			pw.Close()
			if got {
				t.Fatal("a line waiting in stdin answered the question")
			}
		}
	})

	t.Run("stdin closed", func(t *testing.T) {
		log := &coLog{}
		if newCoopPrompter(false, strings.NewReader("")).ask(ctx, log, "Bind it?") || !log.has("stdin is closed") {
			t.Errorf("EOF was not a no:\n%s", log)
		}
	})

	t.Run("session ends", func(t *testing.T) {
		cctx, cancel := context.WithCancel(ctx)
		cancel()
		r, _ := coInput(t)
		if newCoopPrompter(false, r).ask(cctx, &coLog{}, "Bind it?") {
			t.Error("a session that ended answered yes")
		}
	})
}

func TestParseBlockTarget(t *testing.T) {
	// The bits, from the game's setters: onEventBit ORs the id's low byte
	// into mEvent[id >> 8] (d_save.cpp:1187-1189); onTbox, onSwitch and
	// onItem set bit no&0x1F of a big-endian u32 (d_save.cpp:1072-1075,
	// 1084-1087, 1112-1115) at dSv_memBit_c +0x00, +0x04 and +0x14
	// (d_save.h:684-686).
	mb := addr.OffMemBit(3)
	tests := []struct {
		args    []string
		off     int
		mask    byte
		wantErr string
	}{
		{args: []string{"byte", "0x10", "0x81"}, off: 0x10, mask: 0x81},
		{args: []string{"event", "0x0A02"}, off: addr.OffEvent + 0x0A, mask: 0x02},
		{args: []string{"event", "0x4180"}, off: addr.OffEvent + 0x41, mask: 0x80},
		{args: []string{"chest", "3", "0x1F"}, off: mb + 0x00, mask: 0x80},
		{args: []string{"chest", "3", "0"}, off: mb + 0x03, mask: 0x01},
		{args: []string{"switch", "3", "0x05"}, off: mb + 0x04 + 3, mask: 0x20},
		{args: []string{"switch", "3", "0x7F"}, off: mb + 0x04 + 12, mask: 0x80},
		{args: []string{"item", "3", "9"}, off: mb + 0x14 + 2, mask: 0x02},

		{args: []string{"byte", "0x10"}, wantErr: "takes 2 argument"},
		{args: []string{"byte", fmt.Sprint(addr.SaveImageSize), "1"}, wantErr: "offset"},
		{args: []string{"byte", "0", "0x100"}, wantErr: "mask"},
		{args: []string{"byte", "-1", "1"}, wantErr: "offset"},
		{args: []string{"event", "0x0A00"}, wantErr: "names no bit"},
		{args: []string{"event", "0x4201"}, wantErr: "no named event bit"},
		{args: []string{"event", "0x7901"}, wantErr: "register byte"},
		{args: []string{"event", "0x10000"}, wantErr: "event"},
		{args: []string{"switch", "3", "0x80"}, wantErr: "doesn't hold"},
		{args: []string{"switch", "3", "0xC0"}, wantErr: "doesn't hold"},
		{args: []string{"switch", fmt.Sprint(addr.TableCount), "0"}, wantErr: "save table"},
		{args: []string{"chest", "3", "32"}, wantErr: "out of range"},
		{args: []string{"item", "3", "64"}, wantErr: "out of range"},
		{args: []string{"kill", "3", "1"}, wantErr: "unknown bit kind"},
	}
	for _, tt := range tests {
		got, err := parseBlockTarget(tt.args)
		if tt.wantErr != "" {
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("parseBlockTarget(%q) = %+v, %v; want an error saying %q", tt.args, got, err, tt.wantErr)
			}
			continue
		}
		if err != nil || got.off != tt.off || got.mask != tt.mask || got.what == "" {
			t.Errorf("parseBlockTarget(%q) = %+v, %v; want byte 0x%03X mask 0x%02X", tt.args, got, err, tt.off, tt.mask)
		}
	}
}

func TestCoopWorldBlock(t *testing.T) {
	root := t.TempDir()
	id := world.ID{0xB1, 0x0C, 1, 2, 3, 4, 5, 6}
	chest, err := parseBlockTarget([]string{"chest", "3", "0x1F"})
	if err != nil {
		t.Fatal(err)
	}
	var img world.Image
	img[chest.off] = chest.mask
	coWorld(t, root, id, time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC), &img)
	file := filepath.Join(world.Path(root, id), "world.wwco")
	orig, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	if err := coopWorldBlock(&out, root, id, chest, false, false); !errors.Is(err, errNotDone) || !strings.Contains(out.String(), "The world has it now") {
		t.Errorf("without --yes: %v\n%s", err, out.String())
	}
	if now, _ := os.ReadFile(file); !bytes.Equal(now, orig) {
		t.Fatal("the world file changed without --yes")
	}

	// A session serves the world: no edit, unless --force. The marker is
	// checked for real: this process wrote it.
	marker := filepath.Join(world.Path(root, id), coopMarkerName)
	if _, _, err := writeCoopMarker(world.Path(root, id)); err != nil {
		t.Fatal(err)
	}
	inUse := "in use by a running session"
	if !procStateSupported {
		inUse = "may be in use by a session"
	}
	if err := coopWorldBlock(&out, root, id, chest, true, false); err == nil || !strings.Contains(err.Error(), inUse) {
		t.Errorf("in use: %v", err)
	}
	// Another process with the marker's pid runs. By its start token it
	// is the marker's; by its pid alone it may not be.
	t.Run("running", func(t *testing.T) {
		coSetProcState(t, coProcs(map[int]string{4243: "t1"}))
		for _, c := range []struct{ file, want string }{
			{"pid 4243 since 2026-10-15 17:16:19\nhost=" + coopHostname() + " start=t1\n", "is in use by a running session (pid 4243 since 2026-10-15 17:16:19): stop that host first (Ctrl+C), or pass --force if you know no host serves the world"},
			{"pid 4243 since 2026-10-15 17:16:19\n", "may be in use by a session (pid 4243 since 2026-10-15 17:16:19): a process with pid 4243 runs, but this marker has no start time, so it may not be that host; stop that host first (Ctrl+C), or pass --force if no host serves the world"},
		} {
			if err := os.WriteFile(marker, []byte(c.file), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := coopWorldBlock(&out, root, id, chest, true, false); err == nil || !strings.Contains(err.Error(), c.want) {
				t.Errorf("marker %q: %v, want %q", c.file, err, c.want)
			}
		}
	})
	// One that can't be checked is taken as in use.
	if err := os.WriteFile(marker, []byte(fmt.Sprintf("pid %d since 2026-10-15 17:16:19\nhost=elsewhere-%s start=1\n", os.Getpid(), coopHostname())), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := coopWorldBlock(&out, root, id, chest, true, false); err == nil || !strings.Contains(err.Error(), "may be in use") || !strings.Contains(err.Error(), "another machine") {
		t.Errorf("can't tell: %v", err)
	}
	if now, _ := os.ReadFile(file); !bytes.Equal(now, orig) {
		t.Fatal("the world file changed while in use")
	}
	out.Reset()
	if err := coopWorldBlock(&out, root, id, chest, true, true); err != nil || !strings.Contains(out.String(), "Blocked save table 3 chest 0x1F") {
		t.Fatalf("--force: %v\n%s", err, out.String())
	}
	w, _, err := world.Load(root, id)
	if err != nil {
		t.Fatal(err)
	}
	tomb := w.Tombstones()
	if got := w.Image(); got[chest.off]&chest.mask != 0 || tomb.Count() != 1 {
		t.Errorf("after the block: byte 0x%02X, %d tombstones", got[chest.off], tomb.Count())
	}
	// Every later proposal of it is refused.
	if r := w.Merge(&img); len(r.Delta) != 0 {
		t.Errorf("the blocked bit merged again: %+v", r)
	}

	out.Reset()
	if err := coopWorldBlock(&out, root, id, chest, true, true); err != nil || !strings.Contains(out.String(), "already blocked") {
		t.Errorf("again: %v\n%s", err, out.String())
	}
	life := blockTarget{off: addr.OffLife, mask: 0xFF, what: "life"}
	if err := coopWorldBlock(&out, root, id, life, true, true); err == nil || !strings.Contains(err.Error(), "never merges") {
		t.Errorf("a PER_PLAYER byte: %v", err)
	}
	if err := coopWorldBlock(&out, root, world.ID{9}, chest, true, true); err == nil {
		t.Error("blocked in a world that does not exist")
	}

	// A marker a killed host left names a process that has ended: block
	// says so and goes on without --force, and leaves the marker.
	t.Run("stale marker", func(t *testing.T) {
		if !procStateSupported {
			t.Skipf("procState can't check a process on %s", runtime.GOOS)
		}
		stale := fmt.Sprintf("pid %d since 2026-10-15 17:16:19\n", coDeadPid(t))
		if err := os.WriteFile(marker, []byte(stale), 0o644); err != nil {
			t.Fatal(err)
		}
		other, err := parseBlockTarget([]string{"chest", "3", "0x1E"})
		if err != nil {
			t.Fatal(err)
		}
		out.Reset()
		if err := coopWorldBlock(&out, root, id, other, true, false); err != nil || !strings.Contains(out.String(), "stale session marker") ||
			!strings.Contains(out.String(), "did not end cleanly") || !strings.Contains(out.String(), "Blocked save table 3 chest 0x1E") {
			t.Errorf("stale: %v\n%s", err, out.String())
		}
		if b, err := os.ReadFile(marker); err != nil || string(b) != stale {
			t.Errorf("the marker after the block: %q, %v", b, err)
		}
	})
}

// coSecondQuest is a game playing a second-quest file.
func coSecondQuest(t *testing.T) *coGame {
	t.Helper()
	cg := newCoGame(t, "Link", coMine{life: 12})
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(cg.g.SecondQuestSave(0))
	cg.g.EnterFileSelect()
	must(cg.g.LoadFile(0))
	must(cg.g.LoadStage(coSea))
	return cg
}

func TestStartCoopHost(t *testing.T) {
	ctx := context.Background()
	env := func(m *mem.FakeMem, tick func()) coopEnv {
		return coopEnv{
			worlds:   t.TempDir(),
			backups:  t.TempDir(),
			userDirs: []string{coUserDir(t)},
			openMem:  func() (mem.Mem, func(), error) { return m, func() {}, nil },
			tick:     tick,
			input:    strings.NewReader(""), // asks nothing; leaves the test's stdin alone
		}
	}
	on := coopFlags{on: true}
	found := coopFlags{on: true, world: coopWorldNew}

	t.Run("no world yet", func(t *testing.T) {
		if _, err := startCoopHost(ctx, &coLog{}, func(string) {}, on, env(nil, nil)); err == nil || !strings.Contains(err.Error(), "--coop=new") {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("named world missing", func(t *testing.T) {
		f := coopFlags{on: true, world: "0123456789abcdef"}
		if _, err := startCoopHost(ctx, &coLog{}, func(string) {}, f, env(nil, nil)); err == nil || !strings.Contains(err.Error(), "coop-world list") {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("bound file", func(t *testing.T) {
		g, m, tick := m2Game(t, true)
		_ = g
		if _, err := startCoopHost(ctx, &coLog{}, func(string) {}, found, env(m, tick)); err == nil || !strings.Contains(err.Error(), "bound to world "+m2World.String()) {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("second quest", func(t *testing.T) {
		cg := coSecondQuest(t)
		if _, err := startCoopHost(ctx, &coLog{}, func(string) {}, found, env(cg.m, func() { cg.g.Frames(3) })); err == nil || !strings.Contains(err.Error(), "second quest") {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("leaked tag", func(t *testing.T) {
		cg := newCoGame(t, "Link", coMine{life: 12})
		cg.leakTag(bind.WorldID{1})
		cg.m.Record(true)
		log := &coLog{}
		cs, err := startCoopHost(ctx, log, func(string) {}, found, env(cg.m, func() { cg.g.Frames(3) }))
		if err != nil {
			t.Fatal(err)
		}
		defer cs.stop()
		// The world is founded; the leaked tag is the replica's to scrub
		// or overwrite, after a yes. Founding writes nothing.
		if !log.has("founded world") || len(cg.m.Writes()) != 0 {
			t.Errorf("founding: %d writes\n%s", len(cg.m.Writes()), log)
		}
	})
	t.Run("not in game", func(t *testing.T) {
		m := mem.NewFakeMem()
		g := fakegame.New(m) // at boot: no file, no stage
		cctx, cancel := context.WithCancel(ctx)
		ticks := 0
		tick := func() {
			g.Frame()
			if ticks++; ticks == 20 {
				cancel()
			}
		}
		log := &coLog{}
		if _, err := startCoopHost(cctx, log, func(string) {}, found, env(m, tick)); !errors.Is(err, context.Canceled) || !log.has("waiting for your save file") {
			t.Errorf("err = %v\n%s", err, log)
		}
	})
	t.Run("restart", func(t *testing.T) {
		cg := newCoGame(t, "Link", coMine{life: 12})
		e := env(cg.m, func() { cg.g.Frames(3) })
		log := &coLog{}
		cs, err := startCoopHost(ctx, log, func(string) {}, found, e)
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := readCoopMarker(cs.dir); !ok {
			t.Error("no session marker while the hub runs")
		}
		dir, id := cs.dir, cs.world
		cs.stop()
		cs.stop() // a second stop is harmless
		if _, ok := readCoopMarker(dir); ok {
			t.Error("the marker outlived the session")
		}
		// A session that did not stop leaves the marker: the next start
		// says so, after checking the marker's process.
		if !procStateSupported {
			t.Skipf("procState can't check a process on %s", runtime.GOOS)
		}
		// Here it is this process's own.
		if _, _, err := writeCoopMarker(dir); err != nil {
			t.Fatal(err)
		}
		log = &coLog{}
		cs, err = startCoopHost(ctx, log, func(string) {}, on, e)
		if err != nil {
			t.Fatal(err)
		}
		if cs.world != id || !log.has("was marked in use by this process") {
			t.Errorf("restart: world %s, want %s\n%s", cs.world, id, log)
		}
		cs.stop()
		// A killed host's marker names a process that has ended.
		if err := os.WriteFile(filepath.Join(dir, coopMarkerName), []byte(fmt.Sprintf("pid %d since 2026-10-15 17:16:19\n", coDeadPid(t))), 0o644); err != nil {
			t.Fatal(err)
		}
		log = &coLog{}
		cs, err = startCoopHost(ctx, log, func(string) {}, on, e)
		if err != nil {
			t.Fatal(err)
		}
		if cs.world != id || !log.has("but that process has ended: its last session did not end cleanly") {
			t.Errorf("restart after a kill: world %s, want %s\n%s", cs.world, id, log)
		}
		cs.stop()
		// Another process with the marker's pid runs: by its start token,
		// the marker's; by its pid alone, maybe not.
		coSetProcState(t, coProcs(map[int]string{4243: "t1"}))
		for _, c := range []struct{ file, want string }{
			{"pid 4243 since 2026-10-15 17:16:19\nhost=" + coopHostname() + " start=t1\n", "is marked in use by a process that still runs (pid 4243 since 2026-10-15 17:16:19)"},
			{"pid 4243 since 2026-10-15 17:16:19\n", "was marked in use (pid 4243 since 2026-10-15 17:16:19), and a process with pid 4243 runs, but this marker has no start time, so it may not be that host: if another host serves it"},
		} {
			if err := os.WriteFile(filepath.Join(dir, coopMarkerName), []byte(c.file), 0o644); err != nil {
				t.Fatal(err)
			}
			log = &coLog{}
			cs, err = startCoopHost(ctx, log, func(string) {}, on, e)
			if err != nil {
				t.Fatal(err)
			}
			cs.stop()
			if cs.world != id || !log.has(c.want) {
				t.Errorf("restart with marker %q: world %s, want %s and %q\n%s", c.file, cs.world, id, c.want, log)
			}
		}
	})
	t.Run("no backup", func(t *testing.T) {
		cg := newCoGame(t, "Link", coMine{life: 12})
		e := env(cg.m, func() { cg.g.Frames(3) })
		e.userDirs = []string{t.TempDir()} // no memory card
		log := &coLog{}
		cs, err := startCoopHost(ctx, log, func(string) {}, found, e)
		if err != nil {
			t.Fatal(err)
		}
		defer cs.stop()
		if cs.noTags == nil || !log.has("No co-op tag is written this session") {
			t.Errorf("noTags %v\n%s", cs.noTags, log)
		}
	})
}

// TestCoopSessionBackupLog: the session start names every card it backed
// up, with the files copied from it and whether Dolphin's settings select
// it (Dolphin.ini, or Dolphin's default for a slot it doesn't set), and
// warns when they select none: the owner checks there that they are
// theirs.
func TestCoopSessionBackupLog(t *testing.T) {
	// A raw card, with no Dolphin.ini: slot A's default is a GCI folder.
	ud := coUserDir(t)
	log := &coLog{}
	startCoopSession(log, coopFlags{on: true, yes: true}, coopEnv{backups: t.TempDir(), userDirs: []string{ud}})
	card := filepath.Join(ud, "GC", "MemoryCardA.USA.raw")
	for _, want := range []string{"backed up 1 memory-card file(s)", "Check that these are your cards", "slot A raw card " + card + " (default, not in use), 13 bytes", "select none of these cards"} {
		if !log.has(want) {
			t.Errorf("log lacks %q:\n%s", want, log)
		}
	}

	// The owner's layout: Dolphin's default GCI folder for slot A, with no
	// Dolphin.ini to select it. It is in use, and nothing is warned.
	owner := filepath.Join(t.TempDir(), "Dolphin Emulator")
	cardA := filepath.Join(owner, "GC", "USA", "Card A")
	if err := os.MkdirAll(cardA, 0o755); err != nil {
		t.Fatal(err)
	}
	saveA := filepath.Join(cardA, "01-GZLE-gczelda.gci")
	if err := os.WriteFile(saveA, append([]byte("GZLE01"), make([]byte, 58)...), 0o644); err != nil {
		t.Fatal(err)
	}
	log = &coLog{}
	startCoopSession(log, coopFlags{on: true, yes: true}, coopEnv{backups: t.TempDir(), userDirs: []string{owner}})
	for _, want := range []string{"backed up 1 memory-card file(s)", "slot A GCI folder " + cardA + " (default, in use):", "    " + saveA + ", 64 bytes"} {
		if !log.has(want) {
			t.Errorf("log lacks %q:\n%s", want, log)
		}
	}
	if log.has("select none of these cards") {
		t.Errorf("warned with the default GCI folder in use:\n%s", log)
	}

	// Dolphin.ini selects a GCI folder for slot B, which holds a save.
	gci := filepath.Join(ud, "GC", "USA", "Card B")
	if err := os.MkdirAll(gci, 0o755); err != nil {
		t.Fatal(err)
	}
	save := filepath.Join(gci, "01-GZLE-gczelda.gci")
	if err := os.WriteFile(save, append([]byte("GZLE01"), make([]byte, 58)...), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(ud, "Config"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ud, "Config", "Dolphin.ini"), []byte("[Core]\nSlotA = 0\nSlotB = 8\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	log = &coLog{}
	startCoopSession(log, coopFlags{on: true, yes: true}, coopEnv{backups: t.TempDir(), userDirs: []string{ud}})
	for _, want := range []string{"slot B GCI folder " + gci + " (default, in use):", "    " + save + ", 64 bytes", "not in use"} {
		if !log.has(want) {
			t.Errorf("log lacks %q:\n%s", want, log)
		}
	}
	if log.has("select none of these cards") {
		t.Errorf("warned with a card in use:\n%s", log)
	}
}

// TestCoopSessionNoBackup: without a memory-card backup a session runs,
// but nothing is ever written: the bind is not offered, the scrub of a
// leaked tag is declined, and both say why.
func TestCoopSessionNoBackup(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ga := newCoGame(t, "Link", coMine{life: 12})
	gb := newCoGame(t, "Tetra", coMine{life: 12})
	gb.leakTag(bind.WorldID{1})
	ga.m.Record(true)
	gb.m.Record(true)
	h := &coHost{t: t, ctx: ctx, rep: &coLog{}, env: coopEnv{
		worlds:   t.TempDir(),
		backups:  t.TempDir(),
		userDirs: []string{t.TempDir()},
		openMem:  func() (mem.Mem, func(), error) { return ga.m, func() {}, nil },
		tick:     func() { ga.g.Frames(3) },
	}}
	h.start(coopFlags{on: true, world: coopWorldNew, yes: true})
	defer h.stop()
	audit := &coAudit{}
	a := newCoPlayer(t, ctx, h, h.cs, ga, "Link", &coLog{}, audit, 1)
	b := newCoPlayer(t, ctx, h, h.cs, gb, "Tetra", &coLog{}, audit, 2)
	deadline := time.Now().Add(10 * time.Second)
	for !a.rep.has("could bind, but there is no memory-card backup") || !b.rep.has("scrub is not offered") {
		if time.Now().After(deadline) {
			t.Fatalf("no refusal\nA:\n%s\nB:\n%s", a.rep, b.rep)
		}
		a.step()
		b.step()
		time.Sleep(5 * time.Millisecond)
	}
	for range 30 {
		a.step()
		b.step()
	}
	for _, p := range []*coPlayer{a, b} {
		if n := len(p.m.Writes()); n != 0 {
			t.Errorf("%s: %d writes without a backup", p.name, n)
		}
		if st := p.s.r.Status(); st.State == replica.StateArmed {
			t.Errorf("%s armed without a bind", p.name)
		}
		if p.rep.has("Bind it?") || p.rep.has("Scrub it?") {
			t.Errorf("%s was asked:\n%s", p.name, p.rep)
		}
	}
}

// TestCoopSessionPrompts: the answers are the user's. B's file carries a
// leaked tag; the user says no to binding it and yes to scrubbing it, so
// the one write is the scrub, and the bind is not asked again, not even
// after a reconnect, whose WORLD runs the preflight again.
func TestCoopSessionPrompts(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ga := newCoGame(t, "Link", coMine{life: 12})
	gb := newCoGame(t, "Tetra", coMine{life: 12})
	gb.leakTag(bind.WorldID{1})
	gb.m.Record(true)
	h := &coHost{t: t, ctx: ctx, rep: &coLog{}, env: coopEnv{
		worlds:   t.TempDir(),
		backups:  t.TempDir(),
		userDirs: []string{coUserDir(t)},
		openMem:  func() (mem.Mem, func(), error) { return ga.m, func() {}, nil },
		tick:     func() { ga.g.Frames(3) },
		input:    strings.NewReader(""), // the host is asked nothing
	}}
	h.start(coopFlags{on: true, world: coopWorldNew})
	defer h.stop()
	in, answers := coInput(t)
	jcs := startCoopSession(&coLog{}, coopFlags{on: true}, coopEnv{backups: t.TempDir(), userDirs: []string{coUserDir(t)}, input: in})
	log := coAnswering(answers, func(q string) string {
		if q == "Scrub it?" {
			return "y"
		}
		return "n"
	})
	b := newCoPlayer(t, ctx, h, jcs, gb, "Tetra", log, &coAudit{}, 2)
	deadline := time.Now().Add(10 * time.Second)
	for !log.has("Bind it?") || len(gb.m.Writes()) == 0 {
		if time.Now().After(deadline) {
			t.Fatalf("not asked\n%s", log)
		}
		b.step()
		time.Sleep(5 * time.Millisecond)
	}
	for range 50 {
		b.step()
		time.Sleep(time.Millisecond)
	}
	const unbound = "the host serves world"
	welcomed := log.count(unbound)
	b.s.client.Disconnect()
	for log.count(unbound) == welcomed {
		if time.Now().After(deadline) {
			t.Fatalf("no reconnect\n%s", log)
		}
		b.step()
		time.Sleep(5 * time.Millisecond)
	}
	for range 50 {
		b.step()
		time.Sleep(time.Millisecond)
	}
	ws := b.tagWrites(t)
	if len(ws) != 1 || !bytes.Equal(ws[0].Data, make([]byte, bind.TagSize)) {
		t.Errorf("writes %+v, want the one scrub", ws)
	}
	if n, m := log.count("Bind it?"), log.count("Scrub it?"); n != 1 || m != 1 {
		t.Errorf("asked to bind %d times and to scrub %d times, want once each\n%s", n, m, log)
	}
	if st := b.s.r.Status(); st.State == replica.StateArmed {
		t.Errorf("armed after a no: %+v", st)
	}
}

// TestCoopSyncRun runs the pipeline as host and mp-local do (coopSync.run,
// the replica on its own ticker) until the file is bound and armed, then
// ends the session.
func TestCoopSyncRun(t *testing.T) {
	if testing.Short() {
		t.Skip("ticks at the replica's real rate")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ga := newCoGame(t, "Link", coMine{life: 12})
	ga.m.Record(true)
	frames := make(chan struct{})
	go func() {
		defer close(frames)
		for ctx.Err() == nil {
			ga.g.Frame()
			time.Sleep(2 * time.Millisecond)
		}
	}()
	h := &coHost{t: t, ctx: ctx, rep: &coLog{}, env: coopEnv{
		worlds:   t.TempDir(),
		backups:  t.TempDir(),
		userDirs: []string{coUserDir(t)},
		openMem:  func() (mem.Mem, func(), error) { return ga.m, func() {}, nil },
		tick:     func() { time.Sleep(10 * time.Millisecond) },
	}}
	h.start(coopFlags{on: true, world: coopWorldNew, yes: true})
	defer h.stop()
	log := &coLog{}
	s, err := h.cs.newSync(ctx, "Link", ga.m, log)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- s.run(ctx, h.addr) }()
	deadline := time.Now().Add(15 * time.Second)
	for s.r.Status().State != replica.StateArmed {
		if time.Now().After(deadline) {
			t.Fatalf("not armed: %+v\n%s", s.r.Status(), log)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if n := len(ga.m.Writes()); n != 1 {
		t.Errorf("%d writes, want the bind", n)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("run = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("run did not return after the session ended")
	}
	<-frames
}

// TestCoopSyncHostWithoutCoop: a host that runs without --coop refuses the
// co-op connection (RejectRole). Co-op is off for the session, said once,
// and the pipeline waits for the session to end instead of failing it.
func TestCoopSyncHostWithoutCoop(t *testing.T) {
	srv := network.NewServer(0)
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	defer srv.Stop()
	addr := fmt.Sprintf("127.0.0.1:%d", srv.Addr().(*net.TCPAddr).Port)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	log := &coLog{}
	cs := startCoopSession(log, coopFlags{on: true, yes: true}, coopEnv{backups: t.TempDir(), userDirs: []string{coUserDir(t)}})
	cg := newCoGame(t, "Link", coMine{life: 12})
	s, err := cs.newSync(ctx, "Link", cg.m, log)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- s.run(ctx, addr) }()
	deadline := time.Now().Add(10 * time.Second)
	for !log.has("co-op is off for this session") {
		select {
		case err := <-done:
			t.Fatalf("run returned %v before the session ended", err)
		default:
		}
		if time.Now().After(deadline) {
			t.Fatalf("no notice:\n%s", log)
		}
		time.Sleep(10 * time.Millisecond)
	}
	select {
	case err := <-done:
		t.Fatalf("run returned %v before the session ended", err)
	case <-time.After(100 * time.Millisecond):
	}
	if n := log.count("co-op is off"); n != 1 || log.has("type /coop on") {
		t.Errorf("said %d times, or offered /coop on:\n%s", n, log)
	}
	// The replica no longer ticks: an escape is answered at once, and
	// not as one to try again in a moment; /coop on cannot bring it back.
	select {
	case res := <-s.r.Escape(replica.EscapeRefresh):
		if res.Err == nil || errors.Is(res.Err, replica.ErrNotNow) {
			t.Fatalf("/refresh after co-op gave up: %+v", res)
		}
	case <-time.After(time.Second):
		t.Fatal("no answer to /refresh after co-op gave up")
	}
	s.command(ctx, coopCommand{verb: "coop on"})
	if st := s.r.Status(); st.WritesOff == "" || !log.has("/coop on cannot help") {
		t.Fatalf("/coop on after co-op gave up: writes off %q\n%s", st.WritesOff, log)
	}
	cancel()
	if err := <-done; err != nil {
		t.Errorf("run = %v", err)
	}
}

// TestCoopFarBehindAdvice: the preflight of a file far behind the world,
// on a host without --coop-merge, advises copying the host's save in:
// what the copy replaces, that the copy is already bound, and that the
// host first saves in game and loads its file from the card, since a copy
// of a file bound fresh and not loaded back since clears the host's
// pending fresh bind (Appendix C items 107 and 133). Nothing is asked.
func TestCoopFarBehindAdvice(t *testing.T) {
	r, err := replica.New(replica.Config{Mem: mem.NewFakeMem()})
	if err != nil {
		t.Fatal(err)
	}
	log := &coLog{}
	s := &coopSync{cs: &coopSession{}, rep: log, r: r, asked: map[coopFileKey]bool{}}
	s.preflight(replica.PreflightReport{Verdict: replica.PreflightFarBehind, NeedsMerge: true})
	for _, want := range []string{
		"co-op: the world is far ahead of this file: copy the host's save in, or the host can run with --coop-merge to union them; pose sync goes on.",
		"the copy replaces all three of your files with the host's: export yours first",
		"The copied file is the host's own, already bound to the world.",
		"Before the copy, the host should save in game and load its file from the card",
		"makes the host's own file look leaked when the host next starts",
	} {
		if n := log.count(want); n != 1 {
			t.Errorf("the advice has %q %d times, want once:\n%s", want, n, log)
		}
	}
	if len(log.lines) != 1 || len(s.asked) != 0 {
		t.Errorf("%d lines and %d bind questions, want the advice alone:\n%s", len(log.lines), len(s.asked), log)
	}
}
