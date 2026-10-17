package launch

import (
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// TestMacPlan pins the prepare step's commands to
// scripts/setup-mac-dolphin.sh's, in its order, without running any.
func TestMacPlan(t *testing.T) {
	src, dst := "/Applications/Dolphin.app", "/Users/l/Applications/Dolphin.app"
	plist := filepath.Join(dst, "Contents", "Info.plist")
	bin := filepath.Join(dst, "Contents", "MacOS", "Dolphin")
	var got []string
	for _, s := range MacPlan(src, dst) {
		n := 0
		var line string
		if s.RemoveIfExists != "" {
			n++
			line = "rm -rf " + s.RemoveIfExists
		}
		if s.MkdirAll != "" {
			n++
			line = "mkdir -p " + s.MkdirAll
		}
		if len(s.Cmd) > 0 {
			n++
			line = strings.Join(s.Cmd, " ")
		}
		if n > 1 {
			t.Errorf("step %+v has more than one action", s)
		}
		if s.BestEffort {
			line += " || true"
		}
		if line != "" {
			got = append(got, line)
		}
	}
	want := []string{
		"rm -rf " + dst,
		"mkdir -p " + filepath.Dir(dst),
		"cp -R " + src + " " + dst,
		"xattr -cr " + dst + " || true",
		"plutil -replace CFBundleIdentifier -string org.dolphin-emu.dolphin-unhardened " + plist,
		"codesign --force --sign - " + bin,
		lsregister + " -f " + dst + " || true",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("plan:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	for _, s := range MacPlan(src, dst) {
		for _, a := range s.Cmd {
			if a == "sudo" || strings.Contains(a, "--options") || strings.Contains(a, "runtime") {
				t.Errorf("step %q needs sudo or keeps the hardened runtime", s.Cmd)
			}
		}
	}
	sign, id := MacVerify(dst)
	if strings.Join(sign, " ") != "codesign -d -vvv "+bin || strings.Join(id, " ") != "plutil -extract CFBundleIdentifier raw "+plist {
		t.Errorf("verify: %q, %q", sign, id)
	}
}

func TestGatherMacFacts(t *testing.T) {
	home := "/Users/l"
	cp := MacPreparedApp(home)
	files := map[string]string{
		filepath.Join(MacApp, "Contents", "Info.plist"): "<key>CFBundleShortVersionString</key><string>2509</string>",
		filepath.Join(cp, "Contents", "Info.plist"):     "<key>CFBundleShortVersionString</key><string>2506</string>",
	}
	for _, c := range []struct {
		orig, copy bool
		want       MacState
		calls      int
	}{
		// Calls: two plutil version reads per bundle, codesign of the copy.
		{false, false, MacNoDolphin, 0},
		{true, false, MacNeedsCopy, 2},
		{true, true, MacCopyOutdated, 5},
		{false, true, MacReady, 3},
	} {
		exists := func(p string) bool { return (p == MacApp && c.orig) || (p == cp && c.copy) }
		read := func(p string) ([]byte, error) {
			if s, ok := files[p]; ok {
				return []byte(s), nil
			}
			return nil, errors.New("no file")
		}
		r := &fakeRunner{signature: "CodeDirectory flags=0x2(adhoc)"}
		f := GatherMacFacts(home, exists, read, r)
		if got := MacCheck(f); got != c.want || len(r.calls) != c.calls {
			t.Errorf("orig %v copy %v: %v (calls %q), want %v", c.orig, c.copy, got, r.calls, c.want)
		}
	}
	// A copy that still has the hardened runtime.
	r := &fakeRunner{signature: "CodeDirectory v=1 flags=0x10000(runtime)"}
	f := GatherMacFacts(home, func(string) bool { return true }, func(string) ([]byte, error) { return nil, nil }, r)
	if MacCheck(f) != MacHardened {
		t.Errorf("hardened copy: %v", MacCheck(f))
	}
}

// versionRunner answers plutil's version reads per Info.plist (as plutil
// does for XML and binary plists alike) and codesign with an ad-hoc
// signature.
type versionRunner struct{ versions map[string]string }

func (v versionRunner) Run(name string, args ...string) ([]byte, error) {
	if name == "plutil" && len(args) == 4 && args[0] == "-extract" {
		if s, ok := v.versions[args[3]+"|"+args[1]]; ok {
			return []byte(s + "\n"), nil
		}
		return []byte("Could not extract value"), errors.New("exit status 1")
	}
	if name == "codesign" {
		return []byte("CodeDirectory v=20400 flags=0x2(adhoc)"), nil
	}
	return nil, nil
}

// TestGatherMacFactsBinaryPlist: the versions come from plutil, so binary
// Info.plists (which the XML regexp can't read) still show an updated
// Dolphin; plutil failing falls back to the XML plist.
func TestGatherMacFactsBinaryPlist(t *testing.T) {
	home := "/Users/l"
	cp := MacPreparedApp(home)
	op, cpl := filepath.Join(MacApp, "Contents", "Info.plist"), filepath.Join(cp, "Contents", "Info.plist")
	exists := func(string) bool { return true }
	binary := func(string) ([]byte, error) { return []byte("bplist00\x00\x01"), nil }
	short, build := MacVersionCmds(cp)
	if short[0] != "plutil" || short[2] != "CFBundleShortVersionString" || build[2] != "CFBundleVersion" || short[4] != cpl {
		t.Fatalf("MacVersionCmds = %q %q", short, build)
	}

	r := versionRunner{map[string]string{
		op + "|CFBundleShortVersionString": "2509", op + "|CFBundleVersion": "2509",
		cpl + "|CFBundleShortVersionString": "2506", cpl + "|CFBundleVersion": "2506",
	}}
	f := GatherMacFacts(home, exists, binary, r)
	if f.OriginalVersion != "2509" || f.CopyVersion != "2506" || MacCheck(f) != MacCopyOutdated {
		t.Errorf("binary plists, updated: %+v -> %v", f, MacCheck(f))
	}
	r.versions[cpl+"|CFBundleShortVersionString"], r.versions[cpl+"|CFBundleVersion"] = "2509", "2509"
	if f := GatherMacFacts(home, exists, binary, r); MacCheck(f) != MacReady {
		t.Errorf("binary plists, same version: %v", MacCheck(f))
	}
	// Short and build differ: "short (build)".
	r.versions[op+"|CFBundleVersion"] = "2509.1"
	if f := GatherMacFacts(home, exists, binary, r); f.OriginalVersion != "2509 (2509.1)" || MacCheck(f) != MacCopyOutdated {
		t.Errorf("short (build): %+v", f)
	}

	// plutil reads nothing: the XML plists decide.
	xml := func(p string) ([]byte, error) {
		v := "2509"
		if p == cpl {
			v = "2506"
		}
		return []byte("<key>CFBundleShortVersionString</key><string>" + v + "</string>"), nil
	}
	if f := GatherMacFacts(home, exists, xml, versionRunner{}); f.OriginalVersion != "" || MacCheck(f) != MacCopyOutdated {
		t.Errorf("XML fallback: %+v -> %v", f, MacCheck(f))
	}
}
