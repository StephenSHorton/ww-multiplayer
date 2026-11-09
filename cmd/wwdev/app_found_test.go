package main

import (
	"encoding/json"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/internal/gui"
	"github.com/StephenSHorton/ww-multiplayer/mods/coop/bind"
)

func TestParseAppURL(t *testing.T) {
	base, tok, ok := parseAppURL("  http://127.0.0.1:53211/?token=0a1b2c3d4e5f")
	if !ok || base != "http://127.0.0.1:53211" || tok != "0a1b2c3d4e5f" {
		t.Fatalf("got %q %q %v", base, tok, ok)
	}
	for _, line := range []string{
		"The app is at (keep this window open; Ctrl+C stops it):",
		"http://127.0.0.1:53211/",            // no token
		"http://192.168.1.5:53211/?token=ab", // not loopback
	} {
		if _, _, ok := parseAppURL(line); ok {
			t.Errorf("parsed %q", line)
		}
	}
}

func TestDecodeAppReply(t *testing.T) {
	res, err := decodeAppReply(200, []byte(`{"result":{"phase":"lobby"}}`))
	if err != nil || string(res) != `{"phase":"lobby"}` {
		t.Fatalf("result %s, %v", res, err)
	}
	if _, err := decodeAppReply(200, []byte(`{"error":"not ready yet: Joiner"}`)); err == nil || err.Error() != "not ready yet: Joiner" {
		t.Fatalf("method error %v", err)
	}
	if _, err := decodeAppReply(400, []byte(`{"error":"Host takes 1 arguments, got 0"}`)); err == nil || !strings.Contains(err.Error(), "takes 1") {
		t.Fatalf("call error %v", err)
	}
	if _, err := decodeAppReply(401, []byte("bad token\n")); err == nil || !strings.Contains(err.Error(), "401") {
		t.Fatalf("bad token %v", err)
	}
	if res, err := decodeAppReply(200, []byte(`{"result":null}`)); err != nil || string(res) != "null" {
		t.Fatalf("void %s, %v", res, err)
	}
}

func TestParseAppFoundArgs(t *testing.T) {
	env := func(k string) string {
		if k == envDevLogs {
			return "devlogs"
		}
		return ""
	}
	o, err := parseAppFoundArgs(nil, env)
	if err != nil || o.timeout != 30*time.Second || o.mode != "" || o.port != 0 || o.out != filepath.Join("devlogs", "latest", "app-found.json") {
		t.Fatalf("defaults %+v, %v", o, err)
	}
	o, err = parseAppFoundArgs([]string{"--exe", "x.exe", "--pids", "1,2", "--port", "4000", "--mode", "new", "--timeout", "45s", "--out", "r.json", "--world-root", "w"}, env)
	if err != nil || o.exe != "x.exe" || o.pids != "1,2" || o.port != 4000 || o.mode != "new" || o.timeout != 45*time.Second || o.out != "r.json" || o.worldRoot != "w" {
		t.Fatalf("got %+v, %v", o, err)
	}
	for _, bad := range [][]string{{"--mode", "fresh"}, {"--mode", "continue"}, {"--port", "70000"}, {"--timeout", "0s"}, {"--exe"}, {"stray"}} {
		if _, err := parseAppFoundArgs(bad, env); err == nil {
			t.Errorf("%q parsed", bad)
		}
	}
}

func TestAppFoundRequest(t *testing.T) {
	base := gui.CoopRequest{Mode: "auto", LiveSwitches: true, ISO: "d.iso"}
	r, err := appFoundRequest(base, "new")
	if err != nil || r.Mode != "new" || !r.LiveSwitches || r.ISO != "d.iso" || appFoundSetting(r) != "new" {
		t.Fatalf("new: %+v, %v", r, err)
	}
	r, err = appFoundRequest(base, "continue:00112233AABBCCDD")
	if err != nil || r.Mode != "continue" || appFoundSetting(r) != "00112233aabbccdd" {
		t.Fatalf("continue: %+v, %v", r, err)
	}
}

func TestAppChildEnv(t *testing.T) {
	env := appChildEnv([]string{"Path=C:\\bin", "AppData=C:\\real", "USER_DIR_2=C:\\d2", "ww_dolphin_pid=9", "KEEP=1"},
		map[string]string{"APPDATA": `C:\tmp\a`, "WW_DOLPHIN_PID": "42", "ISO_PATH": ""}, "USER_DIR_2")
	want := []string{"Path=C:\\bin", "KEEP=1", `APPDATA=C:\tmp\a`, "WW_DOLPHIN_PID=42"}
	if !slices.Equal(env, want) {
		t.Fatalf("env %q, want %q", env, want)
	}
}

// appStates is one poll's two windows, as the app's /api/call/State
// returns them.
func appStates(t *testing.T, host, join string) (gui.StateView, gui.StateView) {
	t.Helper()
	var a, b gui.StateView
	if err := json.Unmarshal([]byte(host), &a); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(join), &b); err != nil {
		t.Fatal(err)
	}
	return a, b
}

const appW = "0102030405060708"

func boundAppTag(pid uint32, slot uint8, w bind.WorldID) appTag {
	f := bind.File{Slot: slot, DateIPL: 1}
	copy(f.Name[:], "Link")
	tag := bind.Tag{Layout: bind.LayoutV1, Slot: slot, World: w, Nonce: bind.Nonce{9}, NameHash: bind.NameHash(f.Name[:])}
	b := tag.Encode()
	copy(f.Tag[:], b[:])
	return appTagOf(pid, f)
}

// TestAppFoundVerdicts: the run's verdicts over canned windows: the
// 11-08 session (the lobby on "Starting", a blocked founding) fails,
// the fixed one passes.
func TestAppFoundVerdicts(t *testing.T) {
	w := bind.WorldID{1, 2, 3, 4, 5, 6, 7, 8}
	fa, fb := boundAppTag(1, 0, w), boundAppTag(2, 1, w)
	if fa.World != appW {
		t.Fatalf("tag world %q", fa.World)
	}
	start := time.Unix(1000, 0)

	t.Run("adopted", func(t *testing.T) {
		o := newAppFoundObs("", appW, "x/adopted")
		o.Start, o.CoopBeforeStart = start, "auto"
		h, j := appStates(t,
			`{"phase":"in game","lobby":{"phase":"in_game","coop":"auto","members":[{},{}]},"coop":{"setting":"auto","world":"`+appW+`","on":true},
			  "banner":{"kind":"coop","title":"Co-op:","text":"your save belongs to world 01020304…, which wasn't on this computer, so it was started here from your save."}}`,
			`{"phase":"in game","lobby":{"phase":"in_game","coop":"auto"},"coop":{"setting":"auto","world":"`+appW+`","on":true}}`)
		o.observe(h, j, true, start.Add(1500*time.Millisecond))
		if !o.settled() || o.HostInGameMs != 1500 || !hostAdoptedLine(o.HostBanners) {
			t.Fatalf("obs %+v", o)
		}
		cs := appFoundVerdicts(o, &fa, &fb)
		for _, c := range cs {
			if !c.Pass {
				t.Errorf("%s failed: %s", c.Name, c.Detail)
			}
		}
		if len(cs) != 9 {
			t.Fatalf("%d checks: %+v", len(cs), cs)
		}
	})

	t.Run("11-08", func(t *testing.T) {
		o := newAppFoundObs("", appW, "x/adopted")
		o.Start, o.CoopBeforeStart = start, "auto"
		h, j := appStates(t,
			`{"phase":"starting","lobby":{"phase":"starting","coop":"auto"},"coop":{"setting":"auto","world":"auto","on":true},
			  "banner":{"kind":"coop","title":"Founding the new world:","text":"co-op is not running: this file is bound to world 01020304…, which isn't in your worlds.","action":"unbind","actionLabel":"Remove the tag","actionArg":"`+appW+`"},
			  "dialog":{"key":"1:found:bound","kind":"founding","title":"Found a new world","text":"Co-op is not running yet. This file is bound to world `+appW+`","check":{"ok":false,"text":"Loaded: File 1 \u201cLink\u201d"},"actions":[]}}`,
			`{"phase":"starting","lobby":{"phase":"starting","coop":"auto"},"coop":{"setting":"auto","on":false}}`)
		o.observe(h, j, false, start.Add(30*time.Second))
		if o.settled() {
			t.Fatal("settled")
		}
		cs := appFoundVerdicts(o, &fa, &fb)
		for _, name := range []string{"both in game", "joiner coop in W", "world adopted on host", "no blocked founding"} {
			if c := checkNamed(t, cs, name); c.Pass {
				t.Errorf("%s passed: %s", name, c.Detail)
			}
		}
		if c := checkNamed(t, cs, "no blocked founding"); !strings.HasPrefix(c.Detail, "host founding dialog:") {
			t.Errorf("blocked detail %q", c.Detail)
		}
	})

	t.Run("co-op off by default", func(t *testing.T) {
		o := newAppFoundObs("", appW, "x/adopted")
		o.CoopBeforeStart = "off"
		if c := checkNamed(t, appFoundVerdicts(o, &fa, &fb), "lobby coop auto"); c.Pass || c.Skipped {
			t.Errorf("off passed: %+v", c)
		}
		o.Mode = "new"
		if c := checkNamed(t, appFoundVerdicts(o, &fa, &fb), "lobby coop auto"); !c.Skipped {
			t.Errorf("--mode not skipped: %+v", c)
		}
	})

	t.Run("files", func(t *testing.T) {
		o := newAppFoundObs("", appW, "x/adopted")
		other := boundAppTag(2, 1, bind.WorldID{9})
		if c := checkNamed(t, appFoundVerdicts(o, &fa, &other), "files in W"); c.Pass {
			t.Errorf("B in another world passed: %s", c.Detail)
		}
		unread := appTag{PID: 2, Err: "failed to open dolphin process"}
		if c := checkNamed(t, appFoundVerdicts(o, &fa, &unread), "coop bind"); c.Pass || !strings.Contains(c.Detail, "failed to open") {
			t.Errorf("unread file: %+v", c)
		}
	})

	t.Run("the joiner reads the host's block", func(t *testing.T) {
		o := newAppFoundObs("", appW, "x/adopted")
		o.Start = start
		h, j := appStates(t, `{"phase":"in game"}`,
			`{"phase":"in game","banner":{"kind":"coop","title":"Co-op:","text":"the host's save file can't found a world, as it is bound to another co-op world."}}`)
		o.observe(h, j, false, start)
		if !strings.HasPrefix(o.Blocked, "joiner banner:") {
			t.Fatalf("blocked %q", o.Blocked)
		}
	})
}

// TestAppFoundWaitingDialogNotBlocked: auto's founding waits for the
// game with the same dialog kind; only a file that can't found a world
// is a block.
func TestAppFoundWaitingDialogNotBlocked(t *testing.T) {
	var v gui.StateView
	if err := json.Unmarshal([]byte(`{"dialog":{"kind":"founding","title":"Found a new world","check":{"ok":false,"text":"Waiting: waiting for your Dolphin to run Wind Waker"}},
		"banner":{"kind":"coop","title":"Founding the new world:","text":"waiting for your Dolphin to run Wind Waker."}}`), &v); err != nil {
		t.Fatal(err)
	}
	if b := blockedFounding(v); b != "" {
		t.Fatalf("waiting read as blocked: %s", b)
	}
}
