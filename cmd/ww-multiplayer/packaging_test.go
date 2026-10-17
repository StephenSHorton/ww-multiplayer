package main

import (
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Release packaging (app PR 10): what the release publishes, that code
// signing stays optional, the Windows manifest, the macOS bundle, and the
// website deploying with releases. These read the workflow files as text
// (no YAML dependency); the step parser below knows their layout: jobs at
// two spaces, steps as "      - " items.

func readRepoFile(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.FromSlash(name))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

type wfStep struct {
	job  string
	text string // the whole step, "- name: ..." through its last line
}

// name returns the step's name: (or uses:) value.
func (s wfStep) name() string {
	for _, l := range strings.Split(s.text, "\n") {
		l = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(l), "- "))
		if v, ok := strings.CutPrefix(l, "name: "); ok {
			return v
		}
	}
	return strings.SplitN(strings.TrimSpace(s.text), "\n", 2)[0]
}

// ifCond returns the step's own if: condition ("" if none).
func (s wfStep) ifCond() string {
	for _, l := range strings.Split(s.text, "\n") {
		if v, ok := strings.CutPrefix(strings.TrimSpace(l), "if: "); ok && strings.HasPrefix(l, "        if: ") {
			return v
		}
	}
	return ""
}

var (
	jobLine  = regexp.MustCompile(`^  ([A-Za-z0-9_-]+):\s*$`)
	stepLine = regexp.MustCompile(`^      - `)
)

// workflowSteps splits a workflow's jobs into steps.
func workflowSteps(t *testing.T, wf string) []wfStep {
	t.Helper()
	var steps []wfStep
	job, inJobs := "", false
	var cur *wfStep
	flush := func() {
		if cur != nil {
			steps = append(steps, *cur)
			cur = nil
		}
	}
	for _, l := range strings.Split(wf, "\n") {
		switch {
		case l == "jobs:":
			inJobs = true
		case inJobs && jobLine.MatchString(l):
			flush()
			job = jobLine.FindStringSubmatch(l)[1]
		case inJobs && stepLine.MatchString(l):
			flush()
			cur = &wfStep{job: job, text: l}
		case cur != nil && (strings.HasPrefix(l, "        ") || strings.TrimSpace(l) == ""):
			cur.text += "\n" + l
		default:
			flush()
		}
	}
	flush()
	if len(steps) == 0 {
		t.Fatal("no steps parsed")
	}
	return steps
}

// TestReleaseAssets: a tag publishes the Windows exe, the macOS zip and
// SHA256SUMS, and SHA256SUMS covers every other asset.
func TestReleaseAssets(t *testing.T) {
	wf := readRepoFile(t, ".github/workflows/release.yml")
	want := []string{"ww-multiplayer.exe", "ww-multiplayer-macos.zip", "SHA256SUMS"}

	var publish, sums *wfStep
	for _, s := range workflowSteps(t, wf) {
		s := s
		if strings.Contains(s.text, "softprops/action-gh-release") {
			publish = &s
		}
		if strings.Contains(s.text, "sha256sum ") {
			sums = &s
		}
	}
	if publish == nil || sums == nil {
		t.Fatal("release.yml: no release step or no SHA256SUMS step")
	}
	if publish.job != "release" || sums.job != "release" {
		t.Errorf("release and SHA256SUMS steps are in jobs %q/%q, want release", publish.job, sums.job)
	}
	files := regexp.MustCompile(`(?m)^\s+artifacts/(\S+)\s*$`).FindAllStringSubmatch(publish.text, -1)
	var got []string
	for _, f := range files {
		got = append(got, f[1])
	}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("release files = %v, want %v", got, want)
	}
	sumLine := regexp.MustCompile(`sha256sum ([^>\n]+)> SHA256SUMS`).FindStringSubmatch(sums.text)
	if sumLine == nil {
		t.Fatal("no `sha256sum ... > SHA256SUMS` line")
	}
	for _, f := range want[:len(want)-1] {
		if !strings.Contains(" "+sumLine[1]+" ", " "+f+" ") {
			t.Errorf("SHA256SUMS doesn't cover %s (sha256sum %s)", f, sumLine[1])
		}
	}
	// The release job only runs for tags; workflow_dispatch is a dry run.
	if !strings.Contains(wf, "if: startsWith(github.ref, 'refs/tags/')") {
		t.Error("release job must be gated on a tag ref")
	}
	// The jobs upload what the release job downloads.
	for _, p := range []string{"path: ww-multiplayer.exe", "path: dist/ww-multiplayer-macos.zip"} {
		if !strings.Contains(wf, p) {
			t.Errorf("release.yml uploads no %q", p)
		}
	}
	if strings.Contains(wf, ".tar.gz") {
		t.Error("release.yml still mentions the old .tar.gz download")
	}
}

// TestSigningIsOptional: every step that uses a secret runs only when the
// job's HAS_* flag says all the secrets it uses exist, so a fork without
// secrets builds unsigned assets instead of failing. CI (build.yml) uses
// no secrets at all.
func TestSigningIsOptional(t *testing.T) {
	if s := readRepoFile(t, ".github/workflows/build.yml"); strings.Contains(s, "secrets.") {
		t.Error("build.yml uses secrets; PR builds (forks included) must not need any")
	}
	wf := readRepoFile(t, ".github/workflows/release.yml")
	// The build jobs hold the secrets; they get a read-only token. Only
	// the release job declares write access for itself.
	top := wf[:strings.Index(wf, "\njobs:")]
	if !regexp.MustCompile(`(?m)^permissions:\r?\n  contents: read\r?$`).MatchString(top) || strings.Contains(top, ": write") {
		t.Error("release.yml's workflow-level permissions must be contents: read")
	}
	flags := map[string]string{} // HAS_* -> its definition
	for _, m := range regexp.MustCompile(`(?m)^\s+(HAS_[A-Z_]+): (.+)$`).FindAllStringSubmatch(wf, -1) {
		flags[m[1]] = m[2]
	}
	for _, f := range []string{"HAS_WIN_SIGNING", "HAS_MAC_SIGNING", "HAS_MAC_NOTARY"} {
		if _, ok := flags[f]; !ok {
			t.Errorf("release.yml defines no %s", f)
		}
	}
	// Notarizing needs a Developer ID-signed app: the notary flag requires
	// every secret the signing flag does.
	for _, m := range regexp.MustCompile(`secrets\.[A-Z0-9_]+ != ''`).FindAllString(flags["HAS_MAC_SIGNING"], -1) {
		if !strings.Contains(flags["HAS_MAC_NOTARY"], m) {
			t.Errorf("HAS_MAC_NOTARY lacks %s (it needs the signing secrets too)", m)
		}
	}
	secretRef := regexp.MustCompile(`secrets\.([A-Z0-9_]+)`)
	gate := regexp.MustCompile(`env\.(HAS_[A-Z_]+) == 'true'`)
	signing := []string{"osslsigncode", "signtool", "codesign", "notarytool", "stapler", "security import"}
	gated := 0
	for _, s := range workflowSteps(t, wf) {
		uses := secretRef.FindAllStringSubmatch(s.text, -1)
		signs := false
		for _, k := range signing {
			signs = signs || strings.Contains(s.text, k)
		}
		if len(uses) == 0 && !signs {
			continue
		}
		// The gate is the step's if:, or (for the build step, which runs
		// either way) the expression that hands it the secret.
		var flag string
		if m := gate.FindStringSubmatch(s.ifCond()); m != nil {
			flag = m[1]
		} else if m := gate.FindStringSubmatch(s.text); m != nil && strings.Contains(s.text, m[0]+" && secrets.") {
			flag = m[1]
		}
		if flag == "" {
			t.Errorf("step %q uses secrets or signs but is not gated on an env.HAS_* == 'true' flag", s.name())
			continue
		}
		gated++
		def, ok := flags[flag]
		if !ok {
			t.Errorf("step %q is gated on undefined %s", s.name(), flag)
			continue
		}
		for _, u := range uses {
			if !strings.Contains(def, "secrets."+u[1]+" != ''") {
				t.Errorf("step %q uses secrets.%s, but %s doesn't require it", s.name(), u[1], flag)
			}
		}
	}
	if gated < 4 { // Windows sign, keychain import, build identity, notarize
		t.Errorf("found %d gated signing steps, want at least 4", gated)
	}
	// build-mac.sh ad-hoc signs unless it is handed an identity.
	sh := readRepoFile(t, "scripts/build-mac.sh")
	if !strings.Contains(sh, `SIGN_IDENTITY="${MACOS_SIGN_IDENTITY:--}"`) {
		t.Error("build-mac.sh must default to ad-hoc signing (-) without MACOS_SIGN_IDENTITY")
	}
}

// TestPagesDeployWithReleases: site/ describes the latest release, so it
// deploys when a release is published (or by hand), not on pushes to main.
func TestPagesDeployWithReleases(t *testing.T) {
	wf := readRepoFile(t, ".github/workflows/pages.yml")
	on := wf[strings.Index(wf, "\non:\n"):strings.Index(wf, "\npermissions:")]
	for _, w := range []string{"release:", "types: [published]", "workflow_dispatch:"} {
		if !strings.Contains(on, w) {
			t.Errorf("pages.yml on: lacks %q", w)
		}
	}
	if strings.Contains(on, "push:") {
		t.Error("pages.yml deploys on push; the site must wait for a release")
	}
	// A GITHUB_TOKEN release raises no release event, so release.yml
	// starts the deploy itself.
	if !strings.Contains(readRepoFile(t, ".github/workflows/release.yml"), "gh workflow run pages.yml") {
		t.Error("release.yml doesn't start pages.yml after publishing")
	}
	// A hand-published older or not-latest release must not move the site
	// off /releases/latest: the ref is the input or the latest release.
	if strings.Contains(wf, "github.event.release.tag_name") {
		t.Error("pages.yml deploys the release event's tag; it must deploy the latest release's")
	}
	if !strings.Contains(wf, "gh release view") {
		t.Error("pages.yml doesn't resolve the latest release with gh release view")
	}
	site := readRepoFile(t, "site/index.html")
	if !strings.Contains(site, `data-asset="ww-multiplayer-macos.zip"`) || strings.Contains(site, ".tar.gz") {
		t.Error("site/index.html's macOS button must fetch ww-multiplayer-macos.zip")
	}
}

// TestWindowsManifest: ww_windows.syso carries the icon and the committed
// application manifest, and the exe stays console-subsystem.
func TestWindowsManifest(t *testing.T) {
	m := readRepoFile(t, "packaging/ww-multiplayer.exe.manifest")
	for _, w := range []string{
		`requestedExecutionLevel level="asInvoker"`,
		`<dpiAwareness xmlns="http://schemas.microsoft.com/SMI/2016/WindowsSettings">permonitorv2,permonitor</dpiAwareness>`,
		`name="Microsoft.Windows.Common-Controls" version="6.0.0.0"`,
	} {
		if !strings.Contains(m, w) {
			t.Errorf("manifest lacks %s", w)
		}
	}
	syso, err := os.ReadFile("ww_windows.syso")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(syso, []byte(m)) {
		t.Error("ww_windows.syso doesn't embed packaging/ww-multiplayer.exe.manifest as committed; run go generate .")
	}
	ico, err := os.ReadFile(filepath.FromSlash("docs/img/icon.ico"))
	if err != nil {
		t.Fatal(err)
	}
	if len(ico) < 22 || !bytes.Contains(syso, ico[22:]) { // the image data after the 1-entry directory
		t.Error("ww_windows.syso doesn't embed docs/img/icon.ico; run go generate .")
	}
	if !strings.Contains(readRepoFile(t, "main.go"), "//go:generate rsrc -manifest packaging/ww-multiplayer.exe.manifest -ico docs/img/icon.ico -o ww_windows.syso") {
		t.Error("main.go's go:generate line must build ww_windows.syso from the manifest and the icon")
	}
	// One exe: never the GUI subsystem (it would break CLI stdout/stdin).
	for _, f := range []string{".github/workflows/release.yml", ".github/workflows/build.yml", "scripts/build-mac.sh"} {
		if strings.Contains(readRepoFile(t, f), "windowsgui") {
			t.Errorf("%s builds with -H windowsgui; the exe stays console-subsystem", f)
		}
	}
}

// TestMacBundle: build-mac.sh makes a real .app (bundle id, the tag's
// version, the icon, one minimum macOS for cgo, the linker and
// Info.plist) and zips it with ditto.
func TestMacBundle(t *testing.T) {
	sh := readRepoFile(t, "scripts/build-mac.sh")
	for _, w := range []string{
		`MACOS_MIN="12.0"`, // Go 1.26 (go.mod) needs macOS 12
		`export MACOSX_DEPLOYMENT_TARGET="$MACOS_MIN"`,
		`-mmacosx-version-min=${MACOS_MIN}`,
		`clang -target x86_64-apple-macos${MACOS_MIN}`,
		"<key>LSMinimumSystemVersion</key>\n    <string>${MACOS_MIN}</string>",
		"<key>CFBundleIdentifier</key>\n    <string>${BUNDLE_ID}</string>",
		"<key>CFBundleShortVersionString</key>\n    <string>${PLIST_VERSION}</string>",
		"<key>CFBundleIconFile</key>\n    <string>icon.icns</string>",
		`cp "$ROOT/docs/img/icon.icns" "$APP/Contents/Resources/icon.icns"`,
		`ditto -c -k --keepParent "$STAGE" "$ZIP"`,
		`ZIP="$DIST/ww-multiplayer-macos.zip"`,
	} {
		if !strings.Contains(sh, w) {
			t.Errorf("build-mac.sh lacks %q", w)
		}
	}
	if regexp.MustCompile(`macos1[01]\b|version-min=10`).MatchString(sh) {
		t.Error("build-mac.sh still targets an older macOS than MACOS_MIN")
	}
	if _, err := os.Stat(filepath.FromSlash("docs/img/icon.icns")); err != nil {
		t.Error(err)
	}
}
