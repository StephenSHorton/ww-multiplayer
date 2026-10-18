package update

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// ---------- versions ----------

func TestParseTagNewer(t *testing.T) {
	for _, c := range []struct {
		cur, latest string
		newer       bool
	}{
		{"v0.4.0", "v0.4.1", true},
		{"v0.4.0", "v0.5.0", true},
		{"v0.4.0", "v1.0.0", true},
		{"v0.4.1", "v0.4.1", false},
		{"v0.5.0", "v0.4.9", false},
		{"v0.10.0", "v0.9.0", false}, // numeric, not lexical
		{"v0.9.0", "v0.10.0", true},
		{"v0.5.0-rc1", "v0.5.0", true}, // a final is above its prereleases
		{"v0.5.0", "v0.5.0-rc2", false},
		{"v0.4.0+build", "v0.4.0", false},
		{"dev", "v9.9.9", false},
		{"v0.4.0", "latest", false},
		{"0.4.0", "v0.5.0", false}, // a tag needs its v
	} {
		if got := Newer(c.cur, c.latest); got != c.newer {
			t.Errorf("Newer(%q, %q) = %v, want %v", c.cur, c.latest, got, c.newer)
		}
	}
}

func TestIsDev(t *testing.T) {
	for v, dev := range map[string]bool{"dev": true, "": true, "v0.4.0": false, "v0.5.0-rc1": false, "0.4.0": true, "vfoo": true, "dev-abc123": true} {
		if got := IsDev(v); got != dev {
			t.Errorf("IsDev(%q) = %v, want %v", v, got, dev)
		}
	}
}

func TestOfferSkipsDevAndPrereleases(t *testing.T) {
	rel := Release{Tag: "v0.5.0"}
	if !Offer("v0.4.0", rel) {
		t.Fatal("v0.5.0 not offered to v0.4.0")
	}
	if Offer("dev", rel) {
		t.Fatal("offered to a dev build")
	}
	if !Offer("v0.5.0-rc1", rel) {
		t.Fatal("a prerelease build isn't offered its final")
	}
	for _, r := range []Release{
		{Tag: "v0.6.0-rc1"},
		{Tag: "v0.6.0", Prerelease: true},
		{Tag: "v0.6.0", Draft: true},
	} {
		if Offer("v0.4.0", r) {
			t.Errorf("offered %+v", r)
		}
	}
}

func TestDue(t *testing.T) {
	now := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
	for _, c := range []struct {
		last time.Time
		due  bool
	}{
		{time.Time{}, true},
		{now.Add(-time.Minute), false},
		{now.Add(-CheckEvery + time.Second), false},
		{now.Add(-CheckEvery), true},
		{now.Add(-48 * time.Hour), true},
		{now.Add(30 * time.Minute), false}, // a little clock skew
		{now.Add(3 * time.Hour), true},     // a clock set back
	} {
		if got := Due(c.last, now, CheckEvery); got != c.due {
			t.Errorf("Due(%v) = %v, want %v", c.last, got, c.due)
		}
	}
}

func TestNotice(t *testing.T) {
	if Notice(Result{Current: "v0.4.0", Latest: "v0.4.0"}) != "" {
		t.Fatal("a notice with no update")
	}
	n := Notice(Result{Current: "v0.4.0", Latest: "v0.5.0", Available: true, NotesURL: "https://github.com/x/y/releases/tag/v0.5.0"})
	if n != "Update available: v0.5.0 (you have v0.4.0). Download it from https://github.com/x/y/releases/tag/v0.5.0" {
		t.Fatalf("Notice = %q", n)
	}
	if !strings.Contains(Notice(Result{Current: "v0.4.0", Latest: "v0.5.0", Available: true}), ReleasesURL) {
		t.Fatal("no URL falls back to the releases page")
	}
	if strings.Contains(n, "\n") {
		t.Fatal("the notice is more than one line")
	}
}

func TestAssetName(t *testing.T) {
	if AssetName("windows") != "ww-multiplayer.exe" || AssetName("darwin") != "ww-multiplayer-macos.zip" || AssetName("linux") != "" {
		t.Fatal(AssetName("windows"), AssetName("darwin"), AssetName("linux"))
	}
}

// ---------- a fake release server ----------

type fakeRelease struct {
	srv      *httptest.Server
	mu       sync.Mutex
	rel      Release
	files    map[string][]byte
	sums     []byte // nil: computed from files
	latestN  atomic.Int32
	requests atomic.Int32
	status   int // non-zero: every latest request answers it
}

func newFakeRelease(t *testing.T, tag string, files map[string][]byte) *fakeRelease {
	t.Helper()
	f := &fakeRelease{files: files}
	f.srv = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.srv.Close)
	f.rel = Release{Tag: tag, HTMLURL: f.srv.URL + "/notes/" + tag}
	names := []string{}
	for n := range files {
		names = append(names, n)
	}
	names = append(names, AssetSums)
	for _, n := range names {
		f.rel.Assets = append(f.rel.Assets, Asset{Name: n, URL: f.srv.URL + "/dl/" + n})
	}
	f.fixSizes()
	return f
}

func (f *fakeRelease) sumsBytes() []byte {
	if f.sums != nil {
		return f.sums
	}
	var b bytes.Buffer
	for n, data := range f.files {
		h := sha256.Sum256(data)
		fmt.Fprintf(&b, "%s  %s\n", hex.EncodeToString(h[:]), n)
	}
	return b.Bytes()
}

func (f *fakeRelease) fixSizes() {
	for i, a := range f.rel.Assets {
		if a.Name == AssetSums {
			f.rel.Assets[i].Size = int64(len(f.sumsBytes()))
		} else {
			f.rel.Assets[i].Size = int64(len(f.files[a.Name]))
		}
	}
}

func (f *fakeRelease) serve(w http.ResponseWriter, r *http.Request) {
	f.requests.Add(1)
	f.mu.Lock()
	defer f.mu.Unlock()
	switch {
	case r.URL.Path == "/latest":
		f.latestN.Add(1)
		if r.Header.Get("User-Agent") == "" {
			http.Error(w, "no user agent", http.StatusForbidden)
			return
		}
		if f.status != 0 {
			http.Error(w, "nope", f.status)
			return
		}
		_ = json.NewEncoder(w).Encode(f.rel)
	case r.URL.Path == "/dl/"+AssetSums:
		w.Write(f.sumsBytes())
	case strings.HasPrefix(r.URL.Path, "/dl/"):
		data, ok := f.files[strings.TrimPrefix(r.URL.Path, "/dl/")]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Write(data)
	default:
		http.NotFound(w, r)
	}
}

func (f *fakeRelease) client() *Client {
	// The fake assets aren't programs: the new version "runs".
	return &Client{URL: f.srv.URL + "/latest", UserAgent: "ww-multiplayer/test",
		Smoke: func(context.Context, string, string) error { return nil }}
}

// ---------- check and cache ----------

func TestCheckCachesAndSkipsDev(t *testing.T) {
	f := newFakeRelease(t, "v0.5.0", map[string][]byte{AssetWindows: []byte("exe")})
	c := f.client()
	now := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
	ctx := context.Background()

	// A dev build never asks.
	r, st, err := c.Check(ctx, "dev", State{}, now)
	if err != nil || r.Available || st != (State{}) || f.latestN.Load() != 0 {
		t.Fatalf("dev: %+v %+v %v (requests %d)", r, st, err, f.latestN.Load())
	}

	r, st, err = c.Check(ctx, "v0.4.0", State{}, now)
	if err != nil || !r.Available || r.Latest != "v0.5.0" || r.Cached || f.latestN.Load() != 1 {
		t.Fatalf("first check: %+v %v (requests %d)", r, err, f.latestN.Load())
	}
	if !st.LastCheck.Equal(now) || st.Latest != "v0.5.0" || !strings.HasSuffix(st.NotesURL, "/notes/v0.5.0") {
		t.Fatalf("state = %+v", st)
	}
	// Within CheckEvery: the cache answers, no request.
	r, st2, err := c.Check(ctx, "v0.4.0", st, now.Add(time.Hour))
	if err != nil || !r.Available || !r.Cached || st2 != st || f.latestN.Load() != 1 {
		t.Fatalf("cached check: %+v %v (requests %d)", r, err, f.latestN.Load())
	}
	// After the update the same cache offers nothing.
	if r := FromState("v0.5.0", st); r.Available {
		t.Fatalf("v0.5.0 offered itself: %+v", r)
	}
	// Due again: asks.
	if _, _, err := c.Check(ctx, "v0.4.0", st, now.Add(CheckEvery)); err != nil || f.latestN.Load() != 2 {
		t.Fatalf("due check: %v (requests %d)", err, f.latestN.Load())
	}
}

func TestCheckFailureKeepsCacheAndRecordsAttempt(t *testing.T) {
	f := newFakeRelease(t, "v0.5.0", nil)
	f.status = http.StatusServiceUnavailable
	now := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
	old := State{LastCheck: now.Add(-7 * time.Hour), Latest: "v0.4.2", NotesURL: "https://github.com/n"}
	r, st, err := f.client().Check(context.Background(), "v0.4.0", old, now)
	if err == nil {
		t.Fatal("no error from a 503")
	}
	if !st.LastCheck.Equal(now) || st.Latest != "v0.4.2" || !r.Available || r.Latest != "v0.4.2" {
		t.Fatalf("after a failure: %+v %+v", r, st)
	}
	// Recorded: the next start doesn't ask again.
	if _, _, _ = f.client().Check(context.Background(), "v0.4.0", st, now.Add(time.Minute)); f.latestN.Load() != 1 {
		t.Fatalf("asked again right after a failure (%d)", f.latestN.Load())
	}
}

func TestCheckNoNetworkIsAnError(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	u := srv.URL
	srv.Close() // nothing listens there now
	c := &Client{URL: u + "/latest", UserAgent: "t"}
	r, _, err := c.Check(context.Background(), "v0.4.0", State{}, time.Now())
	if err == nil || r.Available {
		t.Fatalf("no server: %+v %v", r, err)
	}
}

func TestCheckIgnoresPrereleaseLatest(t *testing.T) {
	f := newFakeRelease(t, "v0.6.0-rc1", nil)
	r, st, err := f.client().Check(context.Background(), "v0.4.0", State{Latest: "v0.5.0"}, time.Now())
	if err != nil || r.Latest != "v0.5.0" || st.Latest != "v0.5.0" {
		t.Fatalf("a prerelease replaced the cached release: %+v %+v %v", r, st, err)
	}
	f.rel.Tag, f.rel.Prerelease = "v0.6.0", true
	r, _, _ = f.client().Check(context.Background(), "v0.4.0", State{}, time.Now())
	if r.Available {
		t.Fatalf("a release marked prerelease offered: %+v", r)
	}
}

func TestLatestRefusesBadAnswers(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/big":
			w.Write(bytes.Repeat([]byte(" "), MaxJSONSize+10))
		case "/notag":
			w.Write([]byte(`{"tag_name":"latest"}`))
		case "/badnotes":
			w.Write([]byte(`{"tag_name":"v1.0.0","html_url":"javascript:alert(1)"}`))
		}
	}))
	defer srv.Close()
	ctx := context.Background()
	if _, err := (&Client{URL: srv.URL + "/big"}).Latest(ctx); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("big: %v", err)
	}
	if _, err := (&Client{URL: srv.URL + "/notag"}).Latest(ctx); err == nil {
		t.Fatal("a tag that isn't a version was taken")
	}
	r, err := (&Client{URL: srv.URL + "/badnotes"}).Latest(ctx)
	if err != nil || r.HTMLURL != "" {
		t.Fatalf("a javascript: notes URL was kept: %+v %v", r, err)
	}
}

func TestSecureURL(t *testing.T) {
	for u, ok := range map[string]bool{
		"https://api.github.com/x":     true,
		"http://127.0.0.1:8080/latest": true,
		"http://localhost/latest":      true,
		"http://[::1]:9/x":             true,
		"http://api.github.com/x":      false,
		"http://192.168.1.5/x":         false,
		"ftp://example.com/x":          false,
		"file:///C:/x":                 false,
		"not a url":                    false,
	} {
		if err := SecureURL(u); (err == nil) != ok {
			t.Errorf("SecureURL(%q) = %v", u, err)
		}
	}
	if _, err := (&Client{URL: "http://example.com/latest"}).Latest(context.Background()); !errors.Is(err, ErrInsecureURL) {
		t.Fatalf("plain http to a remote host: %v", err)
	}
}

func TestRedirectToInsecureRefused(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://example.com/evil", http.StatusFound)
	}))
	defer srv.Close()
	_, err := (&Client{URL: srv.URL + "/latest"}).Latest(context.Background())
	if !errors.Is(err, ErrInsecureURL) {
		t.Fatalf("redirect to http: %v", err)
	}
}

func TestNewClientEnv(t *testing.T) {
	if c := NewClient(func(string) string { return "" }, "v1.0.0"); c.URL != DefaultURL || c.UserAgent != "ww-multiplayer/v1.0.0" {
		t.Fatalf("%+v", c)
	}
	if c := NewClient(func(k string) string {
		if k == EnvURL {
			return "http://127.0.0.1:1/latest"
		}
		return ""
	}, "v1.0.0"); c.URL != "http://127.0.0.1:1/latest" {
		t.Fatalf("%+v", c)
	}
}

// ---------- download ----------

func TestParseSums(t *testing.T) {
	a := strings.Repeat("ab", 32)
	b := strings.Repeat("CD", 32)
	m, err := ParseSums([]byte(a + "  ww-multiplayer.exe\n" + b + " *ww-multiplayer-macos.zip\n\n"))
	if err != nil || m["ww-multiplayer.exe"] != a || m["ww-multiplayer-macos.zip"] != strings.ToLower(b) {
		t.Fatalf("%v %v", m, err)
	}
	for _, bad := range []string{"abc  x\n", strings.Repeat("zz", 32) + "  x\n", a + "\n"} {
		if _, err := ParseSums([]byte(bad)); err == nil {
			t.Errorf("ParseSums(%q) took it", bad)
		}
	}
}

func TestDownloadVerifies(t *testing.T) {
	exe := []byte("new exe bytes v0.5.0")
	f := newFakeRelease(t, "v0.5.0", map[string][]byte{AssetWindows: exe})
	dir := t.TempDir()
	var last int64
	path, err := f.client().Download(context.Background(), f.rel, AssetWindows, dir, func(done, total int64) { last = done })
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(path) != filepath.Clean(dir) {
		t.Fatalf("the download is in %s, not the app's folder %s", path, dir)
	}
	got, _ := os.ReadFile(path)
	if !bytes.Equal(got, exe) || last != int64(len(exe)) {
		t.Fatalf("got %q, progress %d", got, last)
	}
}

func TestDownloadHashMismatchRefuses(t *testing.T) {
	f := newFakeRelease(t, "v0.5.0", map[string][]byte{AssetWindows: []byte("tampered")})
	f.sums = []byte(strings.Repeat("0", 64) + "  " + AssetWindows + "\n")
	f.fixSizes()
	dir := t.TempDir()
	_, err := f.client().Download(context.Background(), f.rel, AssetWindows, dir, nil)
	if !errors.Is(err, ErrHashMismatch) {
		t.Fatalf("err = %v, want ErrHashMismatch", err)
	}
	if left, _ := os.ReadDir(dir); len(left) != 0 {
		t.Fatalf("a refused download left %v", left)
	}
}

func TestDownloadNeedsSums(t *testing.T) {
	f := newFakeRelease(t, "v0.5.0", map[string][]byte{AssetWindows: []byte("x")})
	rel := f.rel
	rel.Assets = nil
	for _, a := range f.rel.Assets {
		if a.Name != AssetSums {
			rel.Assets = append(rel.Assets, a)
		}
	}
	if _, err := f.client().Download(context.Background(), rel, AssetWindows, t.TempDir(), nil); !errors.Is(err, ErrNoAsset) {
		t.Fatalf("no SHA256SUMS: %v", err)
	}
	// A SHA256SUMS without the asset's line.
	f.sums = []byte(strings.Repeat("a", 64) + "  other.zip\n")
	f.fixSizes()
	if _, err := f.client().Download(context.Background(), f.rel, AssetWindows, t.TempDir(), nil); !errors.Is(err, ErrNoAsset) {
		t.Fatalf("no line: %v", err)
	}
	if _, err := f.client().Download(context.Background(), f.rel, AssetMac, t.TempDir(), nil); !errors.Is(err, ErrNoAsset) {
		t.Fatalf("no asset: %v", err)
	}
}

func TestDownloadSizeLimit(t *testing.T) {
	old := assetLimit
	assetLimit = 16
	t.Cleanup(func() { assetLimit = old })
	big := bytes.Repeat([]byte("x"), 64)
	f := newFakeRelease(t, "v0.5.0", map[string][]byte{AssetWindows: big})
	dir := t.TempDir()
	// The declared size is over: refused before downloading.
	before := f.requests.Load()
	if _, err := f.client().Download(context.Background(), f.rel, AssetWindows, dir, nil); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("declared too large: %v", err)
	}
	if f.requests.Load() != before {
		t.Fatal("downloaded something for an asset declared too large")
	}
	// Declared small, but the server sends more: cut off and refused.
	for i, a := range f.rel.Assets {
		if a.Name == AssetWindows {
			f.rel.Assets[i].Size = 0
		}
	}
	if _, err := f.client().Download(context.Background(), f.rel, AssetWindows, dir, nil); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("sent too much: %v", err)
	}
	if left, _ := os.ReadDir(dir); len(left) != 0 {
		t.Fatalf("a refused download left %v", left)
	}
}

func TestDownloadShortRefused(t *testing.T) {
	f := newFakeRelease(t, "v0.5.0", map[string][]byte{AssetWindows: []byte("abc")})
	for i, a := range f.rel.Assets {
		if a.Name == AssetWindows {
			f.rel.Assets[i].Size = 10
		}
	}
	if _, err := f.client().Download(context.Background(), f.rel, AssetWindows, t.TempDir(), nil); err == nil || !strings.Contains(err.Error(), "ended after") {
		t.Fatalf("short download: %v", err)
	}
}

// ---------- the Windows swap ----------

// fakeFS records renames and removes over a set of existing paths.
type fakeFS struct {
	files  map[string]bool
	ops    []string
	failOn map[string]error // "rename <from>" / "remove <path>" -> error
}

func newFakeFS(paths ...string) *fakeFS {
	f := &fakeFS{files: map[string]bool{}, failOn: map[string]error{}}
	for _, p := range paths {
		f.files[p] = true
	}
	return f
}

func (f *fakeFS) Rename(from, to string) error {
	f.ops = append(f.ops, "rename "+from+" -> "+to)
	if err := f.failOn["rename "+from]; err != nil {
		return err
	}
	if !f.files[from] {
		return fs.ErrNotExist
	}
	delete(f.files, from)
	f.files[to] = true
	return nil
}

func (f *fakeFS) Remove(p string) error {
	f.ops = append(f.ops, "remove "+p)
	if err := f.failOn["remove "+p]; err != nil {
		return err
	}
	if !f.files[p] {
		return fs.ErrNotExist
	}
	delete(f.files, p)
	return nil
}

func (f *fakeFS) RemoveAll(p string) error {
	f.ops = append(f.ops, "removeall "+p)
	if err := f.failOn["removeall "+p]; err != nil {
		return err
	}
	delete(f.files, p)
	return nil
}

func (f *fakeFS) Stat(p string) (fs.FileInfo, error) {
	if !f.files[p] {
		return nil, fs.ErrNotExist
	}
	return nil, nil
}

func (f *fakeFS) Chmod(string, fs.FileMode) error { return nil }

func TestSwapExe(t *testing.T) {
	const exe, tmp = `C:\games\ww-multiplayer.exe`, `C:\games\.ww-update-1.tmp`
	f := newFakeFS(exe, tmp)
	old, err := SwapExe(f, exe, tmp)
	if err != nil || old != exe+".old" {
		t.Fatal(old, err)
	}
	if !f.files[exe] || !f.files[exe+".old"] || f.files[tmp] {
		t.Fatalf("after the swap: %v (%v)", f.files, f.ops)
	}
	want := []string{"remove " + exe + ".old", "rename " + exe + " -> " + exe + ".old", "rename " + tmp + " -> " + exe}
	if fmt.Sprint(f.ops) != fmt.Sprint(want) {
		t.Fatalf("ops = %v\nwant  %v", f.ops, want)
	}
}

func TestSwapExeRollsBack(t *testing.T) {
	const exe, tmp = `C:\games\ww-multiplayer.exe`, `C:\games\.ww-update-1.tmp`
	f := newFakeFS(exe, tmp)
	f.failOn["rename "+tmp] = errors.New("access denied")
	_, err := SwapExe(f, exe, tmp)
	if err == nil || !strings.Contains(err.Error(), "the old one is back") {
		t.Fatalf("err = %v", err)
	}
	if !f.files[exe] || f.files[exe+".old"] {
		t.Fatalf("the old exe isn't back: %v (%v)", f.files, f.ops)
	}
	// Moving the running exe aside fails: nothing changed.
	f = newFakeFS(exe, tmp)
	f.failOn["rename "+exe] = errors.New("in use")
	if _, err := SwapExe(f, exe, tmp); err == nil || !f.files[exe] || !f.files[tmp] {
		t.Fatalf("err = %v, files %v", err, f.files)
	}
}

func TestSwapExeOldStillHeld(t *testing.T) {
	const exe, tmp = `C:\games\ww-multiplayer.exe`, `C:\games\.ww-update-1.tmp`
	f := newFakeFS(exe, tmp, exe+".old")
	f.failOn["remove "+exe+".old"] = errors.New("in use by another process")
	if _, err := SwapExe(f, exe, tmp); err != nil {
		t.Fatal(err)
	}
	alt := fmt.Sprintf("%s.%d.old", exe, os.Getpid())
	if !f.files[exe] || !f.files[alt] || !f.files[exe+".old"] {
		t.Fatalf("files = %v", f.files)
	}
}

// flakyFS fails a rename n times with err, then works.
type flakyFS struct {
	*fakeFS
	from string
	n    int
	err  error
}

func (f *flakyFS) Rename(from, to string) error {
	if from == f.from && f.n > 0 {
		f.n--
		f.ops = append(f.ops, "rename "+from+" -> "+to+" (busy)")
		return f.err
	}
	return f.fakeFS.Rename(from, to)
}

// fastRetries makes every error transient and the waits instant, and
// counts the waits.
func fastRetries(t *testing.T) *int {
	t.Helper()
	sleeps := new(int)
	oldSleep, oldTransient := renameSleep, renameTransient
	renameSleep = func(time.Duration) { *sleeps++ }
	renameTransient = func(err error) bool { return isSharingError(err) }
	t.Cleanup(func() { renameSleep, renameTransient = oldSleep, oldTransient })
	return sleeps
}

func TestSwapExeRetriesABusyFile(t *testing.T) {
	const exe, tmp = `C:\games\ww-multiplayer.exe`, `C:\games\.ww-update-1.tmp`
	sleeps := fastRetries(t)
	// Antivirus holds the fresh download for a moment: a sharing violation.
	f := &flakyFS{fakeFS: newFakeFS(exe, tmp), from: tmp, n: 3, err: &os.LinkError{Op: "rename", Err: winErrorSharingViolation}}
	if _, err := SwapExe(f, exe, tmp); err != nil {
		t.Fatalf("a busy file failed the swap: %v (%v)", err, f.ops)
	}
	if !f.files[exe] || f.files[tmp] || *sleeps != 3 {
		t.Fatalf("files %v, sleeps %d", f.files, *sleeps)
	}
	// Busy for good: the retries run out and the old exe is back.
	*sleeps = 0
	f = &flakyFS{fakeFS: newFakeFS(exe, tmp), from: tmp, n: 1000, err: &os.LinkError{Op: "rename", Err: winErrorAccessDenied}}
	if _, err := SwapExe(f, exe, tmp); err == nil || !strings.Contains(err.Error(), "the old one is back") {
		t.Fatalf("err = %v", err)
	}
	if !f.files[exe] || f.files[exe+".old"] || *sleeps == 0 || *sleeps > 20 {
		t.Fatalf("files %v, sleeps %d", f.files, *sleeps)
	}
	// Not a sharing error: no retry.
	*sleeps = 0
	f = &flakyFS{fakeFS: newFakeFS(exe, tmp), from: tmp, n: 1, err: errors.New("disk full")}
	if _, err := SwapExe(f, exe, tmp); err == nil || *sleeps != 0 {
		t.Fatalf("err = %v, sleeps %d", err, *sleeps)
	}
}

func TestRenameTransientIsWindowsOnly(t *testing.T) {
	busy := &os.LinkError{Op: "rename", Err: winErrorSharingViolation}
	if renameTransient(busy) != (runtime.GOOS == "windows") {
		t.Fatal("renameTransient")
	}
	if renameTransient(errors.New("x")) {
		t.Fatal("a plain error is transient")
	}
}

func TestSwapExeRealFiles(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "ww-multiplayer.exe")
	os.WriteFile(exe, []byte("old"), 0o755)
	os.WriteFile(exe+".old", []byte("older"), 0o755)
	tmp := filepath.Join(dir, ".ww-update-x.tmp")
	os.WriteFile(tmp, []byte("new"), 0o600)
	if _, err := SwapExe(OS, exe, tmp); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(exe); string(b) != "new" {
		t.Fatalf("exe = %q", b)
	}
	if b, _ := os.ReadFile(exe + ".old"); string(b) != "old" {
		t.Fatalf(".old = %q", b)
	}
	if runtime.GOOS != "windows" {
		if fi, _ := os.Stat(exe); fi.Mode().Perm()&0o100 == 0 {
			t.Fatalf("the new exe isn't executable: %v", fi.Mode())
		}
	}
	CleanupOld(OS, "windows", exe)
	if _, err := os.Stat(exe + ".old"); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf(".old survived the cleanup: %v", err)
	}
	if _, err := os.Stat(exe); err != nil {
		t.Fatal("the cleanup removed the exe")
	}
}

func TestCleanupOldStaleTemps(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "ww-multiplayer.exe")
	os.WriteFile(exe, nil, 0o755)
	stale := filepath.Join(dir, ".ww-update-111.tmp")
	fresh := filepath.Join(dir, ".ww-update-222.tmp")
	staleDir := filepath.Join(dir, ".ww-update-333")
	other := filepath.Join(dir, "notes.tmp")
	for _, p := range []string{stale, fresh, other} {
		os.WriteFile(p, []byte("x"), 0o644)
	}
	os.Mkdir(staleDir, 0o755)
	long := time.Now().Add(-2 * StaleAfter)
	for _, p := range []string{stale, staleDir, other} {
		os.Chtimes(p, long, long)
	}
	CleanupOld(OS, "windows", exe)
	for p, want := range map[string]bool{exe: true, stale: false, fresh: true, other: true, staleDir: true} {
		if _, err := os.Stat(p); (err == nil) != want {
			t.Errorf("%s: there = %v, want %v", filepath.Base(p), err == nil, want)
		}
	}

	// macOS: the bundle's folder, unpacked folders too.
	app := filepath.Join(dir, "WW Multiplayer.app")
	bin := filepath.Join(app, "Contents", "MacOS", "ww-multiplayer")
	os.MkdirAll(filepath.Dir(bin), 0o755)
	os.WriteFile(bin, nil, 0o755)
	os.WriteFile(filepath.Join(staleDir, "f"), nil, 0o644)
	os.Chtimes(staleDir, long, long)
	CleanupOld(OS, "darwin", bin)
	if _, err := os.Stat(staleDir); err == nil {
		t.Error("the stale unpacked folder survived")
	}
	if _, err := os.Stat(bin); err != nil {
		t.Error("the cleanup removed the app")
	}
}

func TestRestoreMissing(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "ww-multiplayer.exe")
	if RestoreMissing(OS, exe) {
		t.Fatal("restored from nothing")
	}
	os.WriteFile(exe+".old", []byte("old"), 0o755)
	os.WriteFile(exe, []byte("new"), 0o755)
	if RestoreMissing(OS, exe) {
		t.Fatal("restored over a present exe")
	}
	os.Remove(exe) // antivirus took it
	if !RestoreMissing(OS, exe) {
		t.Fatal("not restored")
	}
	if b, _ := os.ReadFile(exe); string(b) != "old" {
		t.Fatalf("exe = %q", b)
	}
}

func TestCleanupOldPidNames(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "ww [x] multiplayer.exe") // glob metacharacters in the name
	os.WriteFile(exe, nil, 0o755)
	os.WriteFile(exe+".1234.old", nil, 0o755)
	os.WriteFile(exe+".old", nil, 0o755)
	os.WriteFile(filepath.Join(dir, "other.exe.old"), nil, 0o755)
	CleanupOld(OS, "windows", exe)
	left, _ := os.ReadDir(dir)
	var names []string
	for _, e := range left {
		names = append(names, e.Name())
	}
	if fmt.Sprint(names) != "[other.exe.old ww [x] multiplayer.exe]" {
		t.Fatalf("left %v", names)
	}
}

// ---------- macOS ----------

func TestBundleOf(t *testing.T) {
	b, ok := BundleOf(filepath.FromSlash("/Applications/WW Multiplayer/WW Multiplayer.app/Contents/MacOS/ww-multiplayer"))
	if !ok || b != filepath.FromSlash("/Applications/WW Multiplayer/WW Multiplayer.app") {
		t.Fatal(b, ok)
	}
	for _, p := range []string{"/usr/local/bin/ww-multiplayer", "/x/Contents/MacOS/ww", "/x/Foo.app/MacOS/ww"} {
		if _, ok := BundleOf(filepath.FromSlash(p)); ok {
			t.Errorf("BundleOf(%q) found a bundle", p)
		}
	}
}

func TestRelaunchCmd(t *testing.T) {
	exe := filepath.FromSlash("/A/WW Multiplayer.app/Contents/MacOS/ww-multiplayer")
	c := RelaunchCmd("darwin", exe, nil)
	if filepath.Base(c.Path) != "open" && !strings.HasSuffix(c.Args[0], "open") {
		t.Fatalf("mac relaunch runs %v", c.Args)
	}
	if fmt.Sprint(c.Args[1:]) != fmt.Sprint([]string{"-n", filepath.FromSlash("/A/WW Multiplayer.app")}) {
		t.Fatalf("args %q", c.Args)
	}
	c = RelaunchCmd("darwin", exe, []string{"ui", "--dev"})
	if fmt.Sprint(c.Args[1:]) != fmt.Sprint([]string{"-n", filepath.FromSlash("/A/WW Multiplayer.app"), "--args", "ui", "--dev"}) {
		t.Fatalf("args %q", c.Args)
	}
	c = RelaunchCmd("windows", `C:\g\ww-multiplayer.exe`, []string{"ui", "--browser"})
	if c.Path != `C:\g\ww-multiplayer.exe` || fmt.Sprint(c.Args) != fmt.Sprint([]string{`C:\g\ww-multiplayer.exe`, "ui", "--browser"}) {
		t.Fatalf("windows relaunch %q %q", c.Path, c.Args)
	}
}

func TestSwapBundle(t *testing.T) {
	const app, nw = "/A/WW Multiplayer.app", "/A/.ww-update-1/WW Multiplayer/WW Multiplayer.app"
	f := newFakeFS(app, nw)
	if err := SwapBundle(f, app, nw); err != nil {
		t.Fatal(err)
	}
	if !f.files[app] || f.files[nw] || f.files[app+".old"] {
		t.Fatalf("files %v ops %v", f.files, f.ops)
	}
	f = newFakeFS(app, nw)
	f.failOn["rename "+nw] = errors.New("cross-device")
	if err := SwapBundle(f, app, nw); err == nil || !f.files[app] || f.files[app+".old"] {
		t.Fatalf("no rollback: %v %v", err, f.files)
	}
}

type zipEntry struct {
	name string
	body string
	mode fs.FileMode
}

func writeZip(t *testing.T, path string, entries []zipEntry) {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, e := range entries {
		h := &zip.FileHeader{Name: e.name, Method: zip.Deflate}
		mode := e.mode
		if mode == 0 {
			mode = 0o644
		}
		h.SetMode(mode)
		w, err := zw.CreateHeader(h)
		if err != nil {
			t.Fatal(err)
		}
		w.Write([]byte(e.body))
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

func macZip() []zipEntry {
	return []zipEntry{
		{name: "WW Multiplayer/", mode: fs.ModeDir | 0o755},
		{name: "WW Multiplayer/WW Multiplayer.app/Contents/Info.plist", body: "<plist/>"},
		{name: "WW Multiplayer/WW Multiplayer.app/Contents/MacOS/ww-multiplayer", body: "new binary", mode: 0o755},
		{name: "WW Multiplayer/LICENSE", body: "MIT"},
		{name: "__MACOSX/WW Multiplayer/._LICENSE", body: "junk"},
	}
}

func TestUnzipAndFindApp(t *testing.T) {
	dir := t.TempDir()
	z := filepath.Join(dir, "a.zip")
	writeZip(t, z, macZip())
	out := filepath.Join(dir, "out")
	if err := Unzip(z, out); err != nil {
		t.Fatal(err)
	}
	app, err := FindApp(out, "ww-multiplayer")
	if err != nil || filepath.Base(app) != "WW Multiplayer.app" {
		t.Fatal(app, err)
	}
	b, _ := os.ReadFile(filepath.Join(app, "Contents", "MacOS", "ww-multiplayer"))
	if string(b) != "new binary" {
		t.Fatalf("binary = %q", b)
	}
	if runtime.GOOS != "windows" {
		fi, _ := os.Stat(filepath.Join(app, "Contents", "MacOS", "ww-multiplayer"))
		if fi.Mode().Perm()&0o111 == 0 {
			t.Fatalf("lost +x: %v", fi.Mode())
		}
	}
	if _, err := os.Stat(filepath.Join(out, "__MACOSX")); err == nil {
		t.Fatal("extracted __MACOSX")
	}
	if _, err := FindApp(out, "other"); err == nil {
		t.Fatal("found an app without the binary")
	}
}

func TestUnzipRefusesEscapes(t *testing.T) {
	for _, e := range []zipEntry{
		{name: "../evil", body: "x"},
		{name: "a/../../evil", body: "x"},
		{name: "/abs/evil", body: "x"},
		{name: `a\..\..\evil`, body: "x"},
		{name: "link", body: "../../outside", mode: fs.ModeSymlink | 0o777},
		{name: "link2", body: "/etc/passwd", mode: fs.ModeSymlink | 0o777},
	} {
		dir := t.TempDir()
		z := filepath.Join(dir, "a.zip")
		writeZip(t, z, []zipEntry{e})
		if err := Unzip(z, filepath.Join(dir, "out")); err == nil {
			t.Errorf("Unzip took %q", e.name)
		}
		if _, err := os.Stat(filepath.Join(dir, "evil")); err == nil {
			t.Errorf("%q escaped", e.name)
		}
	}
}

func TestUnzipRefusesLinkChains(t *testing.T) {
	dir := t.TempDir()
	if err := os.Symlink("x", filepath.Join(dir, "probe")); err != nil {
		t.Skip("no symlinks here:", err)
	}
	link := fs.ModeSymlink | 0o777
	for _, c := range []struct {
		name    string
		entries []zipEntry
	}{
		// a/b -> .. is dest; c -> a/b/.. reads as "a" but is dest's parent.
		{"chain", []zipEntry{{name: "a/", mode: fs.ModeDir | 0o755}, {name: "a/b", body: "..", mode: link}, {name: "c", body: "a/b/..", mode: link}, {name: "c/evil", body: "x"}}},
		{"dotdot inside", []zipEntry{{name: "a/b/l", body: "../x", mode: link}}},
		{"through a link", []zipEntry{{name: "l", body: "sub", mode: link}, {name: "l/evil", body: "x"}}},
		{"dir through a link", []zipEntry{{name: "l", body: "sub", mode: link}, {name: "l/d/", mode: fs.ModeDir | 0o755}}},
		{"link over a link", []zipEntry{{name: "l", body: "sub", mode: link}, {name: "l", body: "other", mode: link}}},
		{"backslash", []zipEntry{{name: "l", body: `..\x`, mode: link}}},
		{"empty", []zipEntry{{name: "l", body: "", mode: link}}},
	} {
		d := filepath.Join(dir, strings.ReplaceAll(c.name, " ", "-"))
		os.MkdirAll(d, 0o755)
		z := filepath.Join(d, "a.zip")
		writeZip(t, z, c.entries)
		if err := Unzip(z, filepath.Join(d, "out")); err == nil {
			t.Errorf("%s: Unzip took it", c.name)
		}
		if _, err := os.Stat(filepath.Join(d, "evil")); err == nil {
			t.Errorf("%s: escaped the folder", c.name)
		}
	}
	if !safeLink("WW Multiplayer.app/Contents/MacOS/ww-multiplayer") || !safeLink("./x") || safeLink("a/../b") || safeLink("/etc") {
		t.Error("safeLink")
	}
}

func TestUnzipKeepsInsideSymlink(t *testing.T) {
	dir := t.TempDir()
	z := filepath.Join(dir, "a.zip")
	writeZip(t, z, append(macZip(), zipEntry{name: "WW Multiplayer/ww-multiplayer", body: "WW Multiplayer.app/Contents/MacOS/ww-multiplayer", mode: fs.ModeSymlink | 0o777}))
	out := filepath.Join(dir, "out")
	err := Unzip(z, out)
	if err != nil && runtime.GOOS == "windows" && strings.Contains(err.Error(), "privilege") {
		t.Skip("symlinks need a privilege on this Windows")
	}
	if err != nil {
		t.Fatal(err)
	}
	if l, err := os.Readlink(filepath.Join(out, "WW Multiplayer", "ww-multiplayer")); err != nil || filepath.ToSlash(l) != "WW Multiplayer.app/Contents/MacOS/ww-multiplayer" {
		t.Fatalf("link = %q %v", l, err)
	}
}

func TestUnzipSizeLimitAndNotZip(t *testing.T) {
	dir := t.TempDir()
	bad := filepath.Join(dir, "bad.zip")
	os.WriteFile(bad, []byte("not a zip"), 0o644)
	if err := Unzip(bad, filepath.Join(dir, "o")); err == nil {
		t.Fatal("took a non-zip")
	}
}

// ---------- the whole install, on real files ----------

func TestInstallWindows(t *testing.T) {
	newExe := []byte("the v0.5.0 exe")
	f := newFakeRelease(t, "v0.5.0", map[string][]byte{AssetWindows: newExe})
	dir := t.TempDir()
	exe := filepath.Join(dir, "ww-multiplayer.exe")
	os.WriteFile(exe, []byte("the v0.4.0 exe"), 0o755)
	if err := f.client().Install(context.Background(), f.rel, "windows", exe, nil); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(exe); !bytes.Equal(b, newExe) {
		t.Fatalf("exe = %q", b)
	}
	if b, _ := os.ReadFile(exe + ".old"); string(b) != "the v0.4.0 exe" {
		t.Fatalf(".old = %q", b)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 2 {
		t.Fatalf("left behind: %v", entries)
	}
}

func TestInstallWindowsSmokeFailureRollsBack(t *testing.T) {
	f := newFakeRelease(t, "v0.5.0", map[string][]byte{AssetWindows: []byte("the v0.5.0 exe")})
	dir := t.TempDir()
	exe := filepath.Join(dir, "ww-multiplayer.exe")
	os.WriteFile(exe, []byte("the v0.4.0 exe"), 0o755)
	c := f.client()
	var ran, tag string
	c.Smoke = func(_ context.Context, e, tg string) error {
		ran, tag = e, tg
		if b, _ := os.ReadFile(e); string(b) != "the v0.5.0 exe" {
			t.Errorf("smoke ran %q", b)
		}
		return errors.New("quarantined")
	}
	err := c.Install(context.Background(), f.rel, "windows", exe, nil)
	if !errors.Is(err, ErrBadBuild) || !strings.Contains(err.Error(), "quarantined") {
		t.Fatalf("err = %v", err)
	}
	if ran != exe || tag != "v0.5.0" {
		t.Fatalf("smoke ran %q %q", ran, tag)
	}
	if b, _ := os.ReadFile(exe); string(b) != "the v0.4.0 exe" {
		t.Fatalf("exe = %q", b)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 1 {
		t.Fatalf("left behind: %v", entries)
	}
}

// TestSmokeRunsTheProgram runs Smoke on this test binary itself (which
// prints no version line) and on a missing file: both fail.
func TestSmokeRunsTheProgram(t *testing.T) {
	if err := Smoke(context.Background(), filepath.Join(t.TempDir(), "missing.exe"), "v1.0.0"); err == nil {
		t.Fatal("a missing exe passed")
	}
	self, err := os.Executable()
	if err != nil {
		t.Skip(err)
	}
	t.Setenv("WW_SMOKE_HELPER", "1")
	if err := Smoke(context.Background(), self, "v1.0.0"); err != nil {
		t.Fatalf("the helper failed: %v", err)
	}
	if err := Smoke(context.Background(), self, "v2.0.0"); err == nil || !strings.Contains(err.Error(), `"ww-multiplayer v1.0.0"`) {
		t.Fatalf("a wrong version passed: %v", err)
	}
}

// TestMain lets TestSmokeRunsTheProgram run this binary as the program:
// with WW_SMOKE_HELPER=1 and "version" it prints a version line.
func TestMain(m *testing.M) {
	if os.Getenv("WW_SMOKE_HELPER") == "1" && len(os.Args) > 1 && os.Args[1] == "version" {
		if os.Getenv(EnvNoCheck) != "1" {
			os.Exit(3)
		}
		fmt.Println("ww-multiplayer v1.0.0")
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func TestServerErrorIsErrServer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "rate limited", http.StatusForbidden)
	}))
	t.Cleanup(srv.Close)
	c := &Client{URL: srv.URL + "/latest"}
	if _, err := c.Latest(context.Background()); !errors.Is(err, ErrServer) {
		t.Fatalf("err = %v", err)
	}
}

func TestInstallWindowsHashMismatchTouchesNothing(t *testing.T) {
	f := newFakeRelease(t, "v0.5.0", map[string][]byte{AssetWindows: []byte("evil")})
	f.sums = []byte(strings.Repeat("1", 64) + "  " + AssetWindows + "\n")
	f.fixSizes()
	dir := t.TempDir()
	exe := filepath.Join(dir, "ww-multiplayer.exe")
	os.WriteFile(exe, []byte("old"), 0o755)
	if err := f.client().Install(context.Background(), f.rel, "windows", exe, nil); !errors.Is(err, ErrHashMismatch) {
		t.Fatalf("err = %v", err)
	}
	entries, _ := os.ReadDir(dir)
	if b, _ := os.ReadFile(exe); string(b) != "old" || len(entries) != 1 {
		t.Fatalf("exe %q, dir %v", b, entries)
	}
}

func TestInstallDarwin(t *testing.T) {
	dir := t.TempDir()
	z := filepath.Join(dir, "src.zip")
	writeZip(t, z, macZip())
	zb, _ := os.ReadFile(z)
	f := newFakeRelease(t, "v0.5.0", map[string][]byte{AssetMac: zb})

	root := filepath.Join(dir, "Applications", "WW Multiplayer")
	app := filepath.Join(root, "WW Multiplayer.app")
	exe := filepath.Join(app, "Contents", "MacOS", "ww-multiplayer")
	os.MkdirAll(filepath.Dir(exe), 0o755)
	os.WriteFile(exe, []byte("old binary"), 0o755)
	if !CanInstall("darwin", exe) || CanInstall("darwin", filepath.Join(dir, "ww-multiplayer")) || CanInstall("linux", exe) || !CanInstall("windows", exe) {
		t.Fatal("CanInstall")
	}
	bad := f.client()
	bad.Smoke = func(_ context.Context, e, _ string) error {
		if b, _ := os.ReadFile(e); string(b) != "new binary" {
			t.Errorf("smoke ran %q", b)
		}
		return errors.New("killed")
	}
	if err := bad.Install(context.Background(), f.rel, "darwin", exe, nil); !errors.Is(err, ErrBadBuild) {
		t.Fatalf("a new version that doesn't run: %v", err)
	}
	if b, _ := os.ReadFile(exe); string(b) != "old binary" {
		t.Fatalf("the bundle changed: %q", b)
	}
	if err := f.client().Install(context.Background(), f.rel, "darwin", exe, nil); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(exe); string(b) != "new binary" {
		t.Fatalf("binary = %q", b)
	}
	entries, _ := os.ReadDir(root)
	if len(entries) != 1 || entries[0].Name() != "WW Multiplayer.app" {
		t.Fatalf("left beside the app: %v", entries)
	}
	if err := f.client().Install(context.Background(), f.rel, "darwin", filepath.Join(dir, "loose"), nil); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("outside a bundle: %v", err)
	}
	if err := f.client().Install(context.Background(), f.rel, "linux", exe, nil); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("linux: %v", err)
	}
}

// ---------- the CLI cache ----------

func TestCacheFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "sub", "update-check.json")
	if st := LoadCache(p); st != (State{}) {
		t.Fatal(st)
	}
	st := State{LastCheck: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC), Latest: "v0.5.0", NotesURL: "https://x"}
	if err := SaveCache(p, st); err != nil {
		t.Fatal(err)
	}
	if got := LoadCache(p); !got.LastCheck.Equal(st.LastCheck) || got.Latest != st.Latest || got.NotesURL != st.NotesURL {
		t.Fatalf("%+v", got)
	}
	os.WriteFile(p, []byte("{nope"), 0o644)
	if st := LoadCache(p); st != (State{}) {
		t.Fatal("a bad cache wasn't the zero state")
	}
}

func TestCLINotice(t *testing.T) {
	cache := t.TempDir()
	t.Setenv("LOCALAPPDATA", cache) // os.UserCacheDir on Windows
	t.Setenv("XDG_CACHE_HOME", cache)
	t.Setenv("HOME", cache)
	f := newFakeRelease(t, "v0.5.0", nil)
	env := func(k string) string {
		if k == EnvURL {
			return f.srv.URL + "/latest"
		}
		return ""
	}
	if n := CLINotice(context.Background(), env, "dev", time.Second); n != "" || f.latestN.Load() != 0 {
		t.Fatalf("dev: %q", n)
	}
	n := CLINotice(context.Background(), env, "v0.4.0", 5*time.Second)
	if !strings.HasPrefix(n, "Update available: v0.5.0 (you have v0.4.0).") {
		t.Fatalf("notice = %q", n)
	}
	// Cached: no second request.
	if n2 := CLINotice(context.Background(), env, "v0.4.0", time.Second); n2 != n || f.latestN.Load() != 1 {
		t.Fatalf("second notice %q, requests %d", n2, f.latestN.Load())
	}
	off := func(k string) string {
		if k == EnvNoCheck {
			return "1"
		}
		return env(k)
	}
	if n := CLINotice(context.Background(), off, "v0.4.0", time.Second); n != "" {
		t.Fatalf("opted out: %q", n)
	}
}
