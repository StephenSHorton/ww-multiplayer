// Package bugreport writes the one zip a player attaches to an issue (the
// app's "Save bug report" and the CLI's bug-report): the app's newest
// session logs (internal/applog: the multiplayer and co-op lines of each
// session), Dolphin's own log when it writes one, any text files the CLI
// names, and about.txt, the "Copy diagnostics" text (applog.Diagnostics).
//
// Every file is redacted as the diagnostics are: the home directory
// becomes ~, token values <redacted> (applog.Redactor), IP addresses and
// join codes masked (redact.Text), and the host names a player types or
// hosts on, a dotted name with a port or one under a dynamic-DNS, tunnel,
// tailnet or LAN suffix (MaskHosts), since the zip is made to be posted in
// public. A bare name with no dot ("my-pc:25565") is not masked, and
// contents.txt says so. Game files and saves never go in: a file internal/gamefiles
// flags (a disc, a DOL or REL, a .sav state, a .gci or card image) is left
// out by name and content, as is any file that isn't text. Co-op worlds
// (world.wwco and its journal) are save data, so they are not collected
// either. Nothing is uploaded: the zip is written under the app's config
// directory for the player to attach.
//
// The idea is datstarkey/ww-online's WWOnline.Client/Services/BugReport.cs
// (MIT), which redacts only the user folder; this is a clean-room rewrite
// over our own redaction.
package bugreport

import (
	"archive/zip"
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/internal/applog"
	"github.com/StephenSHorton/ww-multiplayer/internal/gamefiles"
	"github.com/StephenSHorton/ww-multiplayer/internal/redact"
)

const (
	// SessionLogs is how many of the newest session logs a report holds.
	SessionLogs = 5
	// MaxFileBytes is the most of one file a report holds: its newest
	// bytes, from a line start (an older start is cut, and says so).
	MaxFileBytes = 4 << 20
	// Keep is how many reports Write leaves in its directory, the new one
	// included.
	Keep = 10
	// Prefix starts a report's file name.
	Prefix = "ww-multiplayer-bug-report-"
)

// DefaultDir is <os.UserConfigDir>/ww-multiplayer/bug-reports, next to the
// logs.
func DefaultDir() (string, error) {
	cfg, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("bugreport: no config directory: %w", err)
	}
	return filepath.Join(cfg, "ww-multiplayer", "bug-reports"), nil
}

// Options are what a report collects.
type Options struct {
	// LogDir is the app's session logs (applog.DefaultDir); its newest
	// SessionLogs *.log files go in under logs/. "" collects none.
	LogDir string
	// DolphinUserDirs are Dolphin user folders whose Logs/dolphin.log goes
	// in (under dolphin/, then dolphin-2/, ... for a second folder), when
	// there is one: Dolphin writes it only with its logger's "Write to
	// File" on.
	DolphinUserDirs []string
	// Extra are more text files, under extra/ by their base names (the
	// CLI's --include: a terminal capture, mp-local's output).
	Extra []string
	// About is about.txt (applog.Diagnostics.Text, already redacted; it is
	// redacted again all the same). "" writes none.
	About string
	// Home is the home directory the redaction replaces with ~.
	Home string
	// Now names the zip (UTC); nil is time.Now.
	Now func() time.Time
}

// Result is a written report.
type Result struct {
	Path string // the zip
	// Files are the zip's entries, about.txt and contents.txt included.
	Files []string
	// Skipped says, one line each, what was found but left out and why.
	Skipped []string
}

// entry is one file to pack: its name in the zip and where it is.
type entry struct{ name, path string }

// collect lists o's files; missing ones are no entry (an optional file),
// not a skip.
func collect(o Options) (es []entry, skipped []string) {
	if o.LogDir != "" {
		ents, err := os.ReadDir(o.LogDir)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			skipped = append(skipped, "session logs: "+err.Error())
		}
		var logs []string
		for _, e := range ents {
			if e.Type().IsRegular() && strings.HasSuffix(e.Name(), ".log") {
				logs = append(logs, e.Name())
			}
		}
		// applog names them by their UTC start, so name order is age.
		sort.Sort(sort.Reverse(sort.StringSlice(logs)))
		if len(logs) > SessionLogs {
			logs = logs[:SessionLogs]
		}
		for _, n := range logs {
			es = append(es, entry{"logs/" + n, filepath.Join(o.LogDir, n)})
		}
	}
	seen := map[string]bool{}
	n := 0
	for _, d := range o.DolphinUserDirs {
		if d == "" || seen[strings.ToLower(filepath.Clean(d))] {
			continue
		}
		seen[strings.ToLower(filepath.Clean(d))] = true
		p := filepath.Join(d, "Logs", "dolphin.log")
		if _, err := os.Stat(p); err != nil {
			continue
		}
		n++
		dir := "dolphin"
		if n > 1 {
			dir = fmt.Sprintf("dolphin-%d", n)
		}
		es = append(es, entry{dir + "/dolphin.log", p})
	}
	if n == 0 && len(seen) > 0 {
		// Said once, so a player with Dolphin's log on but another
		// folder (a portable install the CLI didn't find) knows to pass
		// --user-dir; a second folder without one is no news.
		var looked []string
		for _, d := range o.DolphinUserDirs {
			if d != "" && !slices.Contains(looked, d) {
				looked = append(looked, d)
			}
		}
		skipped = append(skipped, "dolphin.log: none in "+strings.Join(looked, ", ")+
			" (Dolphin writes it only with its logger's \"Write to File\" on)")
	}
	names := map[string]int{}
	for _, p := range o.Extra {
		base := zipName(filepath.Base(p))
		names[base]++
		if k := names[base]; k > 1 {
			ext := filepath.Ext(base)
			base = fmt.Sprintf("%s-%d%s", strings.TrimSuffix(base, ext), k, ext)
		}
		es = append(es, entry{"extra/" + base, p})
	}
	return es, skipped
}

// Write collects o's files, redacts each, and writes them with about.txt
// and contents.txt (what is in the zip and what was left out) to a new
// zip in dir, then removes the oldest reports past Keep. A file that can't
// be read, isn't text or is a game file is listed in Skipped and
// contents.txt, never packed; the report is written anyway.
func Write(dir string, o Options) (Result, error) {
	now := o.Now
	if now == nil {
		now = time.Now
	}
	red := applog.Redactor{Home: o.Home}
	clean := func(s string) string { return MaskHosts(redact.Text(red.Redact(s))) }

	es, skipped := collect(o)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return Result{}, fmt.Errorf("bugreport: %w", err)
	}
	stamp := now().UTC().Format("20060102T150405Z")
	path := filepath.Join(dir, Prefix+stamp+".zip")
	for i := 2; ; i++ {
		if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
			break
		}
		path = filepath.Join(dir, fmt.Sprintf("%s%s-%d.zip", Prefix, stamp, i))
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return Result{}, fmt.Errorf("bugreport: %w", err)
	}
	res := Result{Path: path}
	zw := zip.NewWriter(f)
	put := func(name, text string) error {
		w, err := zw.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Deflate, Modified: now()})
		if err != nil {
			return err
		}
		if _, err := io.WriteString(w, text); err != nil {
			return err
		}
		res.Files = append(res.Files, name)
		return nil
	}
	fail := func(err error) (Result, error) {
		zw.Close()
		f.Close()
		os.Remove(path)
		return Result{}, fmt.Errorf("bugreport: %w", err)
	}
	if o.About != "" {
		if err := put("about.txt", cleanLines(o.About, clean)); err != nil {
			return fail(err)
		}
	}
	for _, e := range es {
		text, why := readText(e.path)
		if why != "" {
			skipped = append(skipped, e.name+": "+why)
			continue
		}
		if err := put(e.name, cleanLines(text, clean)); err != nil {
			return fail(err)
		}
	}
	for _, s := range skipped {
		res.Skipped = append(res.Skipped, clean(s))
	}
	var c strings.Builder
	fmt.Fprintf(&c, "WW Multiplayer bug report, made %s\n", now().UTC().Format(time.RFC3339))
	c.WriteString("Every file is redacted: your home folder is ~, tokens are removed, and IP addresses, join codes and host names\n")
	c.WriteString("with a port or under a dynamic-DNS, tunnel, tailnet or LAN name (duckdns, playit, ts.net, ...) are masked.\n")
	c.WriteString("A computer name with no dot (\"my-pc:25565\") is not masked: check for one before posting.\n")
	c.WriteString("No game files, saves or co-op worlds are included.\n\nFiles:\n")
	for _, n := range res.Files {
		c.WriteString("  " + n + "\n")
	}
	if len(res.Skipped) > 0 {
		c.WriteString("\nLeft out:\n")
		for _, s := range res.Skipped {
			c.WriteString("  " + s + "\n")
		}
	}
	if err := put("contents.txt", c.String()); err != nil {
		return fail(err)
	}
	if err := zw.Close(); err != nil {
		f.Close()
		os.Remove(path)
		return Result{}, fmt.Errorf("bugreport: %w", err)
	}
	if err := f.Close(); err != nil {
		os.Remove(path)
		return Result{}, fmt.Errorf("bugreport: %w", err)
	}
	_ = prune(dir, Keep) // best effort
	return res, nil
}

// readText reads the newest MaxFileBytes of the file at p as text, or says
// why it is left out.
func readText(p string) (string, string) {
	f, err := os.Open(p)
	if err != nil {
		return "", "can't be read: " + err.Error()
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return "", "can't be read: " + err.Error()
	}
	if !st.Mode().IsRegular() {
		return "", "not a file"
	}
	h := make([]byte, gamefiles.HeaderLen)
	n, _ := io.ReadFull(f, h)
	if why := gamefiles.Check(filepath.ToSlash(p), h[:n]); len(why) > 0 {
		return "", "a game file or save (" + strings.Join(why, "; ") + "), never included"
	}
	cut := st.Size() > MaxFileBytes
	off := int64(0)
	if cut {
		off = st.Size() - MaxFileBytes
	}
	if _, err := f.Seek(off, io.SeekStart); err != nil {
		return "", "can't be read: " + err.Error()
	}
	b, err := io.ReadAll(io.LimitReader(f, MaxFileBytes))
	if err != nil {
		return "", "can't be read: " + err.Error()
	}
	if bytes.IndexByte(b, 0) >= 0 {
		return "", "not a text file"
	}
	if cut {
		if i := bytes.IndexByte(b, '\n'); i >= 0 {
			b = b[i+1:]
		}
		b = append([]byte(fmt.Sprintf("[the first %d bytes of this file are left out]\n", st.Size()-int64(len(b)))), b...)
	}
	return string(b), ""
}

// zipName is an --include file's base name as its zip entry: redacted as
// the text is (a capture named after a host, "26.1.2.3.log" or
// "me.duckdns.org:25570.txt"), with what a zip name can't hold made "_".
func zipName(base string) string {
	// Without its extension first: "me.duckdns.org.txt" is a host name
	// only up to ".txt".
	ext := filepath.Ext(base)
	if len(ext) > 1 && len(ext) <= 6 {
		base = MaskHosts(redact.Text(strings.TrimSuffix(base, ext))) + ext
	}
	base = MaskHosts(redact.Text(base))
	return strings.Map(func(r rune) rune {
		if r == '/' || r == '\\' || r == ':' || r < 0x20 {
			return '_'
		}
		return r
	}, base)
}

// cleanLines redacts text one line at a time (a mask never spans lines).
func cleanLines(text string, clean func(string) string) string {
	var b strings.Builder
	sc := bufio.NewScanner(strings.NewReader(text))
	sc.Buffer(make([]byte, 64<<10), MaxFileBytes+1)
	for sc.Scan() {
		b.WriteString(clean(strings.TrimRight(sc.Text(), "\r")))
		b.WriteByte('\n')
	}
	return b.String()
}

// prune removes the oldest reports in dir until keep remain; their names
// start with their UTC time.
func prune(dir string, keep int) error {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	var zips []string
	for _, e := range ents {
		if e.Type().IsRegular() && strings.HasPrefix(e.Name(), Prefix) && strings.HasSuffix(e.Name(), ".zip") {
			zips = append(zips, e.Name())
		}
	}
	sort.Strings(zips)
	var errs []error
	for len(zips) > keep {
		if err := os.Remove(filepath.Join(dir, zips[0])); err != nil {
			errs = append(errs, err)
		}
		zips = zips[1:]
	}
	return errors.Join(errs...)
}
