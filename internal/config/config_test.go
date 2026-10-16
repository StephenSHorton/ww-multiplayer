package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func writeFile(t *testing.T, path, s string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(s), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestLoadMissingIsDefaults(t *testing.T) {
	c, warns := Load(filepath.Join(t.TempDir(), "config.json"))
	if len(warns) != 0 {
		t.Errorf("warnings for a missing file: %v", warns)
	}
	if !reflect.DeepEqual(known(c), known(Defaults())) {
		t.Errorf("got %+v, want the defaults", c)
	}
}

// known is c without its file bookkeeping, for comparisons.
func known(c Config) Config {
	c.extra, c.broken = nil, false
	if c.RecentHosts == nil {
		c.RecentHosts = []RecentHost{}
	}
	return c
}

func TestSaveLoadRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "config.json") // Save makes the directory
	c := Defaults()
	c.Name, c.Color, c.Port = "Stephen", "#D02C2C", 25570
	c.Dolphin = Dolphin{Exe: `C:\Dolphin\Dolphin.exe`, UserDir: `C:\u`, LaunchOnStart: false, InstallCheats: true}
	c.ISO = ISO{Patched: "p.iso", Vanilla: "v.ciso"}
	c.Coop = Coop{LastMode: "continue", LastWorld: "3fa9000000000000", DryRun: true}
	c.UI = UI{Window: Window{W: 1280, H: 800}, SetupDone: true}
	c.AddRecentHost("26.12.3.4", "Radmin", time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC))
	if err := c.Save(path); err != nil {
		t.Fatal(err)
	}
	got, warns := Load(path)
	if len(warns) != 0 {
		t.Errorf("warnings: %v", warns)
	}
	if !reflect.DeepEqual(known(got), known(c)) {
		t.Errorf("round trip:\n got %+v\nwant %+v", known(got), known(c))
	}
	ents, _ := os.ReadDir(filepath.Dir(path))
	if len(ents) != 1 {
		t.Errorf("Save left %d files: %v", len(ents), ents)
	}
}

// TestUnknownFieldsKept: a newer build's fields, top-level and inside a
// section this build knows, survive a load and save, and a newer schema
// version is not lowered.
func TestUnknownFieldsKept(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	writeFile(t, path, `{"version": 3, "name": "Ann", "lobby": {"autoReady": true},
		"dolphin": {"exe": "d.exe", "gpu": "vulkan"}, "ui": {"window": {"w": 900, "h": 700, "x": 5}}}`)
	c, warns := Load(path)
	if len(warns) != 1 || !strings.Contains(warns[0], "newer version") {
		t.Errorf("warnings: %v", warns)
	}
	c.Name = "Bea"
	c.Dolphin.Exe = "e.exe"
	if err := c.Save(path); err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	b, _ := os.ReadFile(path)
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	if m["version"] != 3.0 || m["name"] != "Bea" {
		t.Errorf("version/name: %v %v", m["version"], m["name"])
	}
	if l, _ := m["lobby"].(map[string]any); l["autoReady"] != true {
		t.Errorf("unknown section lost: %s", b)
	}
	d, _ := m["dolphin"].(map[string]any)
	if d["gpu"] != "vulkan" || d["exe"] != "e.exe" {
		t.Errorf("dolphin section: %v", d)
	}
	w, _ := m["ui"].(map[string]any)["window"].(map[string]any)
	if w["x"] != 5.0 || w["w"] != 900.0 {
		t.Errorf("ui.window: %v", w)
	}
}

func TestBadFileGivesDefaultsAndIsKept(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	for _, bad := range []string{`{"name": "Ann",`, `[1,2]`, `null`, "\x00\x01", ``} {
		writeFile(t, path, bad)
		c, warns := Load(path)
		if len(warns) != 1 || !strings.Contains(warns[0], "using the default settings") {
			t.Errorf("%q: warnings %v", bad, warns)
		}
		if !reflect.DeepEqual(known(c), known(Defaults())) {
			t.Errorf("%q: got %+v", bad, c)
		}
		c.Name = "New"
		if err := c.Save(path); err != nil {
			t.Fatal(err)
		}
		if b, err := os.ReadFile(path + ".bad"); err != nil || string(b) != bad {
			t.Errorf("%q: .bad holds %q, %v", bad, b, err)
		}
		if got, warns := Load(path); len(warns) != 0 || got.Name != "New" {
			t.Errorf("%q: after save %+v %v", bad, got, warns)
		}
	}
}

// TestBadFileKeptAcrossSaves: the app keeps the Config Load gave it and
// saves it on each settings change. Only the first Save moves the bad file
// to .bad; later ones must not replace it with the good file.
func TestBadFileKeptAcrossSaves(t *testing.T) {
	for _, bad := range []string{"{garbage", `{"name":"` + strings.Repeat("a", MaxSize) + `"}`} {
		path := filepath.Join(t.TempDir(), "config.json")
		writeFile(t, path, bad)
		c, warns := Load(path)
		if len(warns) != 1 {
			t.Fatalf("warnings: %v", warns)
		}
		for i, name := range []string{"One", "Two", "Three"} {
			c.Name = name
			if err := c.Save(path); err != nil {
				t.Fatalf("save %d: %v", i, err)
			}
			if b, err := os.ReadFile(path + ".bad"); err != nil || string(b) != bad {
				t.Fatalf("after save %d .bad holds %.40q, %v", i, b, err)
			}
			if got, warns := Load(path); len(warns) != 0 || got.Name != name {
				t.Fatalf("after save %d: %q %v", i, got.Name, warns)
			}
		}
		ents, _ := os.ReadDir(filepath.Dir(path))
		if len(ents) != 2 {
			t.Errorf("Save left %d files: %v", len(ents), ents)
		}
	}
}

// TestSaveFailureKeepsFile: a Save that fails before the final rename
// leaves config.json where it was, so the next Load still warns about it
// instead of silently giving the defaults.
func TestSaveFailureKeepsFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	writeFile(t, path, "{garbage")
	c, _ := Load(path)
	c.Name = "New"
	defer func(r func(string, string) error) { rename = r }(rename)
	rename = func(from, to string) error {
		if to == path && strings.Contains(filepath.Base(from), ".config-") {
			return errors.New("injected failure")
		}
		return os.Rename(from, to)
	}
	if err := c.Save(path); err == nil {
		t.Fatal("Save succeeded with its last rename failing")
	}
	if b, err := os.ReadFile(path); err != nil || string(b) != "{garbage" {
		t.Errorf("config.json after a failed Save: %q %v", b, err)
	}
	if _, err := os.Stat(path + ".bad"); err == nil {
		t.Error(".bad written by a failed Save")
	}
	if _, warns := Load(path); len(warns) != 1 {
		t.Errorf("Load after a failed Save: %v", warns)
	}
}

// TestTopLevelKeysMatchWithoutCase: a hand-edited "Name" is read as name,
// as encoding/json reads nested keys, an exact key wins over a folded one,
// and Save writes each field once, under its own name.
func TestTopLevelKeysMatchWithoutCase(t *testing.T) {
	c, warns := Parse([]byte(`{"Name":"Bob","PORT":1234,"Dolphin":{"Exe":"d.exe","gpu":"vulkan"},"color":"red","COLOR":"blue","Lobby":1}`))
	if len(warns) != 0 {
		t.Errorf("warnings: %v", warns)
	}
	if c.Name != "Bob" || c.Port != 1234 || c.Dolphin.Exe != "d.exe" || c.Color != "red" {
		t.Errorf("got name %q port %d exe %q color %q", c.Name, c.Port, c.Dolphin.Exe, c.Color)
	}
	b, err := c.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"Name", "PORT", "Dolphin", "COLOR"} {
		if _, ok := m[k]; ok {
			t.Errorf("%q written beside its field: %s", k, b)
		}
	}
	d, _ := m["dolphin"].(map[string]any)
	if m["name"] != "Bob" || m["port"] != 1234.0 || m["color"] != "red" || m["Lobby"] != 1.0 || d["exe"] != "d.exe" || d["gpu"] != "vulkan" {
		t.Errorf("saved: %s", b)
	}
	again, warns := Parse(b)
	if len(warns) != 0 || !reflect.DeepEqual(known(again), known(c)) {
		t.Errorf("reparse: %+v %v", known(again), warns)
	}
}

func TestTooLargeFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	writeFile(t, path, `{"name":"`+strings.Repeat("a", MaxSize)+`"}`)
	c, warns := Load(path)
	if len(warns) != 1 || !strings.Contains(warns[0], "larger than") || c.Name != "" {
		t.Errorf("%v %q", warns, c.Name)
	}
}

// TestBadFieldsKeepTheRest: a section of the wrong type, or a value out of
// range, gives that section's default and a warning; the rest is read.
func TestBadFieldsKeepTheRest(t *testing.T) {
	c, warns := Parse([]byte("\xEF\xBB\xBF" + `{"name": "Ann", "port": "x", "dolphin": {"exe": 5}, "iso": {"patched": "p.iso"},
		"color": "chartreuse-ish", "coop": {"lastMode": "maybe", "lastWorld": "ab"}, "ui": {"window": {"w": 10, "h": 10}},
		"recentHosts": [{"addr": ""}, {"addr": "1.2.3.4"}]}`))
	want := []string{`"dolphin" is not valid`, `"port" is not valid`, "not a tunic color", `co-op mode "maybe"`}
	if len(warns) != len(want) {
		t.Fatalf("warnings: %q", warns)
	}
	for i, w := range want {
		if !strings.Contains(warns[i], w) {
			t.Errorf("warning %d = %q, want %q", i, warns[i], w)
		}
	}
	d := Defaults()
	if c.Name != "Ann" || c.ISO.Patched != "p.iso" || c.Port != d.Port || c.Dolphin != d.Dolphin || c.Color != "" ||
		c.Coop.LastMode != "off" || c.Coop.LastWorld != "ab" || c.UI.Window != d.UI.Window || len(c.RecentHosts) != 1 {
		t.Errorf("got %+v", c)
	}
	if c, warns := Parse([]byte(`{"port": 70000, "name": "a\nb", "color": "red"}`)); len(warns) != 2 || c.Port != DefaultPort || c.Name != "" || c.Color != "red" {
		t.Errorf("%+v %q", c, warns)
	}
}

func TestSaveOverwritesAtomically(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	writeFile(t, path, `{"version":1,"name":"Old"}`)
	c, _ := Load(path)
	c.Name = "New"
	if err := c.Save(path); err != nil {
		t.Fatal(err)
	}
	if got, _ := Load(path); got.Name != "New" {
		t.Errorf("name %q", got.Name)
	}
	ents, _ := os.ReadDir(dir)
	if len(ents) != 1 {
		t.Errorf("files left: %v", ents)
	}
	// A directory in the way fails the save and leaves no temp file.
	bad := filepath.Join(dir, "isdir")
	if err := os.Mkdir(bad, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(bad, "x"), "x")
	if err := c.Save(bad); err == nil {
		t.Error("saving over a non-empty directory succeeded")
	}
	ents, _ = os.ReadDir(dir)
	for _, e := range ents {
		if strings.HasSuffix(e.Name(), ".tmp") {
			t.Errorf("temp file left: %s", e.Name())
		}
	}
}

func TestWithEnv(t *testing.T) {
	c := Defaults()
	c.Dolphin.Exe, c.Color = "saved.exe", "blue"
	env := map[string]string{"DOLPHIN_EXE": "env.exe", "WW_COLOR": "red", "USER_DIR_1": ""}
	got, used := c.WithEnv(func(k string) string { return env[k] })
	if got.Dolphin.Exe != "env.exe" || got.Color != "red" || got.Dolphin.UserDir != "" || got.ISO.Patched != "" {
		t.Errorf("got %+v", got)
	}
	if !reflect.DeepEqual(used, []string{"DOLPHIN_EXE", "WW_COLOR"}) {
		t.Errorf("used %v", used)
	}
	if c.Dolphin.Exe != "saved.exe" {
		t.Error("WithEnv changed its receiver")
	}
}

func TestAddRecentHost(t *testing.T) {
	var c Config
	t0 := time.Unix(0, 0)
	for i := 0; i < MaxRecentHosts+3; i++ {
		c.AddRecentHost(strings.Repeat("h", i+1), "", t0)
	}
	if len(c.RecentHosts) != MaxRecentHosts || c.RecentHosts[0].Addr != strings.Repeat("h", MaxRecentHosts+3) {
		t.Fatalf("hosts %v", c.RecentHosts)
	}
	c.AddRecentHost("hh", "Radmin", t0)
	c.AddRecentHost("HH", "", t0.Add(time.Hour))
	if h := c.RecentHosts[0]; h.Addr != "HH" || h.Label != "Radmin" || !h.LastUsed.Equal(t0.Add(time.Hour)) || len(c.RecentHosts) != MaxRecentHosts {
		t.Errorf("moved host %+v (%d hosts)", h, len(c.RecentHosts))
	}
}

func TestDirAndPath(t *testing.T) {
	t.Setenv("APPDATA", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	p, err := Path()
	if err != nil {
		t.Skip(err)
	}
	if filepath.Base(p) != "config.json" || filepath.Base(filepath.Dir(p)) != "ww-multiplayer" {
		t.Errorf("Path() = %s", p)
	}
}

// FuzzParse: Parse never panics; what it reads, Marshal writes, and a
// second Parse of that reads the same settings, without warnings unless the
// first had a newer version, and writes the same bytes.
func FuzzParse(f *testing.F) {
	for _, s := range []string{
		`{}`, `{"version":1,"name":"Ann","color":"#D02C2C","port":25565}`,
		`{"version":9,"x":{"y":[1,2,{"z":null}]},"dolphin":{"exe":"a","q":1}}`,
		`{"recentHosts":[{"addr":"1.2.3.4","label":"LAN","lastUsed":"2026-10-16T12:00:00Z"}]}`,
		`{"ui":{"window":{"w":"wide"}},"coop":{"lastMode":"new"}}`, `[]`, `{"port":1e400}`, "\xEF\xBB\xBF{}",
	} {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, b []byte) {
		c, _ := Parse(b)
		out, err := c.Marshal()
		if err != nil {
			t.Fatalf("Marshal: %v", err)
		}
		c2, warns := Parse(out)
		if len(warns) > 0 && c2.Version <= Version {
			t.Fatalf("re-parse warned: %q\n%s", warns, out)
		}
		if !reflect.DeepEqual(known(c), known(c2)) {
			t.Fatalf("re-parse changed settings:\n%+v\n%+v", known(c), known(c2))
		}
		out2, err := c2.Marshal()
		if err != nil || !bytes.Equal(out, out2) {
			t.Fatalf("not stable: %v\n%s\n%s", err, out, out2)
		}
	})
}
