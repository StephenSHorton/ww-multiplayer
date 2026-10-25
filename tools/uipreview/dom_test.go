package main

import (
	"context"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"
)

// findChrome is a headless-capable Chrome or Edge: UIPREVIEW_CHROME, one
// on PATH, or Windows' usual install folders; "" for none.
func findChrome() string {
	if p := os.Getenv("UIPREVIEW_CHROME"); p != "" {
		return p
	}
	for _, n := range []string{"chrome", "google-chrome", "chromium", "chromium-browser", "msedge"} {
		if p, err := exec.LookPath(n); err == nil {
			return p
		}
	}
	if runtime.GOOS == "windows" {
		for _, p := range []string{
			`C:\Program Files\Google\Chrome\Application\chrome.exe`,
			`C:\Program Files (x86)\Google\Chrome\Application\chrome.exe`,
			`C:\Program Files (x86)\Microsoft\Edge\Application\msedge.exe`,
		} {
			if _, err := os.Stat(p); err == nil {
				return p
			}
		}
	}
	return ""
}

// dumpDOM loads the real frontend with the fake Bridge at ?query in
// headless Chrome and returns the DOM once its timers ran.
func dumpDOM(t *testing.T, chrome, base, query string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, chrome, "--headless=new", "--disable-gpu", "--no-first-run", "--no-default-browser-check",
		"--user-data-dir="+t.TempDir(), "--virtual-time-budget=6000", "--dump-dom", base+"/?"+query).Output()
	if err != nil {
		t.Fatalf("%s: chrome: %v", query, err)
	}
	return string(out)
}

// text is the text of the element with id in dom (its first text node).
func text(dom, id string) string {
	m := regexp.MustCompile(`id="` + regexp.QuoteMeta(id) + `"[^>]*>([^<]*)<`).FindStringSubmatch(dom)
	if m == nil {
		return "<no #" + id + ">"
	}
	return m[1]
}

// TestFrontendDOM drives the real frontend (fake Bridge, headless Chrome)
// through the review's cases: the sidebar's count for a pre-lobby host,
// the progress card's story line when story isn't shared and its keys
// line with no stage tables, Settings' update line during a session, and
// leaving Setup while a disc is being patched. It skips without Chrome.
func TestFrontendDOM(t *testing.T) {
	if testing.Short() {
		t.Skip("-short: no browser")
	}
	chrome := findChrome()
	if chrome == "" {
		t.Skip("no Chrome or Edge (set UIPREVIEW_CHROME)")
	}
	dir := filepath.Join("..", "..", "internal", "gui", "frontend")
	if _, err := os.Stat(filepath.Join(dir, "index.html")); err != nil {
		t.Skip("no frontend:", err)
	}
	srv := httptest.NewServer(handler(dir))
	defer srv.Close()
	// A browser that can't start here (no display libraries, a sandbox
	// refusal) is no frontend bug: skip.
	if _, err := exec.Command(chrome, "--headless=new", "--disable-gpu", "--no-first-run", "--user-data-dir="+t.TempDir(),
		"--dump-dom", "about:blank").Output(); err != nil {
		t.Skipf("%s doesn't run headless here: %v", chrome, err)
	}

	cases := []struct {
		name, query string
		check       func(dom string) string // "" when right
	}{
		{"legacy lobby counts its players", "s=ingame&legacy=1", func(d string) string {
			if got := text(d, "me-status"); got != "In game · 2 players" {
				return "sidebar status " + got
			}
			return ""
		}},
		{"a lobby counts its members", "s=ingame", func(d string) string {
			if got := text(d, "me-status"); got != "In game · 3 players" {
				return "sidebar status " + got
			}
			return ""
		}},
		{"a host that shares no story", "s=ingame&shares=nostory", func(d string) string {
			if strings.Contains(d, "No story held back") || !strings.Contains(d, "Story isn't shared in this session") {
				return "a host with no story: the card still says story is all there"
			}
			return ""
		}},
		{"nothing granted yet", "s=ingame&shares=none", func(d string) string {
			if strings.Contains(d, "No story held back") || !strings.Contains(d, "Story isn't shared yet") {
				return "nothing granted: the card still says story is all there"
			}
			return ""
		}},
		{"story shared", "s=ingame", func(d string) string {
			if !strings.Contains(d, "Held back: Tetra") {
				return "the held story bit is missing"
			}
			return ""
		}},
		{"no stage tables", "s=ingame&nokeys=1", func(d string) string {
			if got := text(d, "progress-nokeys"); !strings.Contains(got, "Small keys aren't derived") {
				return "no-tables line " + got
			}
			return ""
		}},
		{"stage tables", "s=ingame", func(d string) string {
			if strings.Contains(d, `id="progress-nokeys"`) || !strings.Contains(d, "Dragon Roost Cavern") {
				return "the key rows are wrong"
			}
			return ""
		}},
		{"the host edits what co-op shares", "s=ingame-host&click=rules-edit,rule-story,rule-switches", func(d string) string {
			switch {
			case !regexp.MustCompile(`id="rule-story"[^>]*aria-checked="false"`).MatchString(d):
				return "Story didn't turn off"
			case !regexp.MustCompile(`id="rule-switches"[^>]*aria-checked="false"`).MatchString(d):
				return "the second click undid the first"
			case !strings.Contains(d, "Always on") || !strings.Contains(d, "Off · set at Start"):
				return "World and Dry run aren't read-only"
			case !strings.Contains(d, "one key can open a door in each game"):
				return "no note for keys without switches"
			case text(d, "rules-edit") != "Done":
				return "the Edit button reads " + text(d, "rules-edit")
			}
			return ""
		}},
		{"a joiner's rules are the host's", "s=ingame", func(d string) string {
			if !regexp.MustCompile(`id="rules-edit"[^>]*hidden`).MatchString(d) || !strings.Contains(d, "The host chooses what co-op shares") {
				return "a joiner can edit, or isn't told who chooses"
			}
			return ""
		}},
		{"a peer without the host's tables", "s=ingame-host&tables=differ", func(d string) string {
			if !strings.Contains(d, `class="on part"`) {
				return "no part chip for a rule Blake isn't granted"
			}
			return ""
		}},
		{"the Room items card", "s=ingame", func(d string) string {
			if got := text(d, "room-count"); got != "10 of 17 items" || !strings.Contains(d, "5½ hearts max") || !strings.Contains(d, "Din's Pearl") {
				return "room card " + got
			}
			return ""
		}},
		{"no Room items before the world", "s=ingame&room=none", func(d string) string {
			if !regexp.MustCompile(`id="room-panel"[^>]*hidden`).MatchString(d) {
				return "the room card shows with no world"
			}
			return ""
		}},
		{"Warp to on the partner's row", "s=ingame&two=1", func(d string) string {
			if strings.Count(d, `class="rowact"`) != 1 || !strings.Contains(d, ">DRC room 3<") {
				return "no single Warp to, or no partner chip, with two players"
			}
			return ""
		}},
		{"no Warp to with three players", "s=ingame", func(d string) string {
			if strings.Contains(d, `class="rowact"`) {
				return "Warp to with two others"
			}
			return ""
		}},
		{"Settings in a session", "s=ingame&upd=1&click=nav-settings", func(d string) string {
			if got := text(d, "set-vertext"); !strings.Contains(got, "once you leave the session") {
				return "version line " + got
			}
			return ""
		}},
		{"Settings at Home", "s=settings&upd=1", func(d string) string {
			if got := text(d, "set-vertext"); !strings.Contains(got, "on the banner above") {
				return "version line " + got
			}
			return ""
		}},
		{"leaving Setup mid-patch asks", "s=setup-game&click=nav-play", func(d string) string {
			if !strings.Contains(d, `data-key="leave-setup"`) || text(d, "dlg-title") != "Your disc is still being patched" {
				return "no dialog on leaving Setup mid-patch"
			}
			if !regexp.MustCompile(`<section id="setup" class="screen">`).MatchString(d) {
				return "Setup was left"
			}
			return ""
		}},
		{"leaving Setup with no patch", "s=setup-dolphin&click=nav-play", func(d string) string {
			if strings.Contains(d, `data-key="leave-setup"`) {
				return "asked with no patch running"
			}
			if !regexp.MustCompile(`<section id="setup" class="screen" hidden`).MatchString(d) {
				return "still on Setup"
			}
			return ""
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if msg := c.check(dumpDOM(t, chrome, srv.URL, c.query)); msg != "" {
				t.Error(msg)
			}
		})
	}
}
