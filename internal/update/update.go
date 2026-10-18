// Package update is the app's update check and one-click update (owner
// decision 2026-10-17, "one-click updates"): one HTTPS request to GitHub's
// latest-release API, a semver compare with the running version, and, when
// the player presses Update, the platform's release asset downloaded,
// checked against the release's SHA256SUMS and swapped in.
//
// It is pure Go and UI-free: internal/gui drives it for the window (the
// banner, the progress, the relaunch), package main for the CLI's and the
// TUI's one-line notice. Nothing here touches the network on its own; every
// request goes through a Client whose base URL a test points at a fake
// server on 127.0.0.1 (EnvURL).
//
// What is checked:
//   - a dev build ("dev", or anything that isn't a vX.Y.Z tag) never checks;
//   - a prerelease or draft release is never offered (GitHub's "latest"
//     already skips them; Offer checks again), but a prerelease build (a
//     tester's v0.5.0-rc1) is offered the final v0.5.0;
//   - the check runs at most every CheckEvery (Due), the last answer cached
//     (the app in its config, the CLI in its own cache file), so the app
//     never comes near GitHub's unauthenticated rate limit (60 an hour);
//   - a download is refused unless every URL is HTTPS (plain HTTP only to
//     a loopback address, for tests), the asset fits MaxAssetSize, and its
//     SHA-256 matches the release's SHA256SUMS line; nothing is swapped
//     before that.
package update

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/wwapi/semver"
)

// DefaultURL is GitHub's latest-release API for this repository.
const DefaultURL = "https://api.github.com/repos/StephenSHorton/ww-multiplayer/releases/latest"

// ReleasesURL is the releases page a notice points at.
const ReleasesURL = "https://github.com/StephenSHorton/ww-multiplayer/releases/latest"

// EnvURL overrides DefaultURL (tests: a fake release server on 127.0.0.1).
const EnvURL = "WW_UPDATE_URL"

// EnvNoCheck turns the CLI's and the TUI's check off ("1"); the app has
// its "Check for updates" switch, which the TUI follows too.
const EnvNoCheck = "WW_NO_UPDATE_CHECK"

// EnvSmoke ("1") marks Smoke's run of a new version: it leaves what the
// install moved aside (the old exe it may put back) alone.
const EnvSmoke = "WW_UPDATE_SMOKE"

// CheckEvery is how often a check may ask GitHub; between checks the
// cached answer is used.
const CheckEvery = 6 * time.Hour

// Limits on what a release may make the app download.
const (
	MaxAssetSize = 256 << 20 // the exe or the macOS zip
	MaxSumsSize  = 64 << 10  // SHA256SUMS
	MaxJSONSize  = 1 << 20   // the release JSON
)

// The release asset names (release.yml).
const (
	AssetWindows = "ww-multiplayer.exe"
	AssetMac     = "ww-multiplayer-macos.zip"
	AssetSums    = "SHA256SUMS"
)

// Errors a caller tells apart.
var (
	// ErrHashMismatch: the download is not the file SHA256SUMS names; it
	// was deleted and nothing was changed.
	ErrHashMismatch = errors.New("update: the download doesn't match the release's SHA256SUMS")
	// ErrTooLarge: the asset is over MaxAssetSize (or SHA256SUMS over
	// MaxSumsSize, the JSON over MaxJSONSize).
	ErrTooLarge = errors.New("update: the download is larger than allowed")
	// ErrNoAsset: the release has no asset for this platform, or no
	// SHA256SUMS line for it.
	ErrNoAsset = errors.New("update: the release has no download for this computer")
	// ErrInsecureURL: a URL is not HTTPS (and not a loopback test server).
	ErrInsecureURL = errors.New("update: refusing a URL that isn't HTTPS")
	// ErrUnsupported: this build can't update itself here (Linux, or a
	// macOS binary outside its .app); the releases page still works.
	ErrUnsupported = errors.New("update: this build can't update itself; download the new version from the releases page")
	// ErrServer: the server answered with something other than 200 OK
	// (a rate limit, an outage).
	ErrServer = errors.New("update: the server didn't answer")
	// ErrBadBuild: the new version didn't run (Smoke) once it was in place,
	// so the old one was put back.
	ErrBadBuild = errors.New("update: the new version didn't start")
)

// Release is the part of GitHub's release JSON the update reads.
type Release struct {
	Tag        string  `json:"tag_name"`
	Name       string  `json:"name"`
	HTMLURL    string  `json:"html_url"`
	Prerelease bool    `json:"prerelease"`
	Draft      bool    `json:"draft"`
	Assets     []Asset `json:"assets"`
}

// Asset is one release file.
type Asset struct {
	Name string `json:"name"`
	Size int64  `json:"size"`
	URL  string `json:"browser_download_url"`
}

// Asset returns the release's asset called name.
func (r Release) Asset(name string) (Asset, bool) {
	for _, a := range r.Assets {
		if a.Name == name {
			return a, true
		}
	}
	return Asset{}, false
}

// AssetName is the release asset for goos, or "" when the release has
// none (Linux builds from source).
func AssetName(goos string) string {
	switch goos {
	case "windows":
		return AssetWindows
	case "darwin":
		return AssetMac
	}
	return ""
}

// ParseTag parses a release tag or a build's version: "v" and a semantic
// version ("v0.4.1", "v0.5.0-rc1").
func ParseTag(tag string) (semver.Version, error) {
	s, ok := strings.CutPrefix(strings.TrimSpace(tag), "v")
	if !ok {
		return semver.Version{}, fmt.Errorf("%w: %q has no leading v", semver.ErrVersion, tag)
	}
	return semver.Parse(s)
}

// IsDev says the build isn't a release (version "dev", a local build, or
// anything ParseTag refuses): it never checks for updates.
func IsDev(current string) bool {
	_, err := ParseTag(current)
	return err != nil
}

// Newer says latest is a higher version than current; false when either
// doesn't parse.
func Newer(current, latest string) bool {
	c, err := ParseTag(current)
	if err != nil {
		return false
	}
	l, err := ParseTag(latest)
	if err != nil {
		return false
	}
	return l.Compare(c) > 0
}

// IsPrerelease says the tag has a prerelease part ("v0.5.0-rc1"), what
// release.yml publishes as a prerelease.
func IsPrerelease(tag string) bool {
	v, err := ParseTag(tag)
	return err == nil && len(v.Pre) > 0
}

// Offer says rel is an update to offer a build at current: a release
// build, a published non-prerelease, and newer.
func Offer(current string, rel Release) bool {
	if IsDev(current) || rel.Draft || rel.Prerelease || IsPrerelease(rel.Tag) {
		return false
	}
	return Newer(current, rel.Tag)
}

// State is a check's cached answer.
type State struct {
	// LastCheck is when GitHub was last asked (answered or not).
	LastCheck time.Time `json:"lastCheck"`
	// Latest is the newest release it named ("" before an answer), and
	// NotesURL its page.
	Latest   string `json:"latest"`
	NotesURL string `json:"notesURL"`
}

// Due says a check may ask GitHub now: never asked, last asked at least
// every ago, or a LastCheck in the future (a clock set back).
func Due(last, now time.Time, every time.Duration) bool {
	if last.IsZero() {
		return true
	}
	d := now.Sub(last)
	return d >= every || d < -time.Hour
}

// Result is a check's answer for a build.
type Result struct {
	Current string `json:"current"`
	Latest  string `json:"latest"`
	// Available says Latest is an update for Current (Offer).
	Available bool   `json:"available"`
	NotesURL  string `json:"notesURL"`
	// Cached says the answer came from the cache, not from GitHub now.
	Cached bool `json:"cached"`
}

// FromState is the answer the cache gives current.
func FromState(current string, st State) Result {
	r := Result{Current: current, Latest: st.Latest, NotesURL: st.NotesURL, Cached: true}
	r.Available = st.Latest != "" && Offer(current, Release{Tag: st.Latest})
	return r
}

// Notice is the CLI's and the TUI's one line about r, or "" when there is
// no update.
func Notice(r Result) string {
	if !r.Available {
		return ""
	}
	url := r.NotesURL
	if url == "" {
		url = ReleasesURL
	}
	return fmt.Sprintf("Update available: %s (you have %s). Download it from %s", r.Latest, r.Current, url)
}
