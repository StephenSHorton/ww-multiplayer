// Package applog writes a session's log lines to a file of its own, for
// the TUI and (later) the GUI, whose users have no terminal scrollback
// (docs/01-architecture.md, "Logs"): <os.UserConfigDir>/ww-multiplayer/
// logs/<UTC time>-<role>.log, the lines the CLI would print, each with a
// timestamp and level. The newest Keep files are kept; a file stops
// growing at MaxBytes. Lines are redacted before they reach the file
// (Redact): the home directory becomes ~, and anything labelled a token
// is masked. Nothing is uploaded.
//
// Diagnostics builds the "Copy diagnostics" text a user pastes into a bug
// report from the same redacted lines.
package applog

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/wwapi/log"
)

const (
	// Keep is how many log files Open leaves in the directory, the new one
	// included.
	Keep = 20
	// MaxBytes is the most a log file grows to; past it one line says the
	// rest was dropped.
	MaxBytes = 8 << 20
	// TailLines is how many of the newest lines a File keeps in memory for
	// Diagnostics.
	TailLines = 200
)

// maxBytes is MaxBytes, smaller in a test.
var maxBytes int64 = MaxBytes

// DefaultDir is <os.UserConfigDir>/ww-multiplayer/logs.
func DefaultDir() (string, error) {
	cfg, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("applog: no config directory: %w", err)
	}
	return filepath.Join(cfg, "ww-multiplayer", "logs"), nil
}

// File is one session's log file. It is a log.Reporter and safe for
// concurrent use; Log never blocks on anything but the file write.
type File struct {
	mu     sync.Mutex
	f      *os.File
	path   string
	n      int64
	full   bool
	closed bool
	tail   []string // ring of the newest TailLines lines
	next   int
	now    func() time.Time
	red    Redactor
}

// Open creates a new log file for a session of role (for example "host"
// or "join") in dir, then removes the oldest files past Keep. now names the
// file and stamps its lines (time.Now in the app; a fixed clock in tests).
func Open(dir, role string, now func() time.Time) (*File, error) {
	if now == nil {
		now = time.Now
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("applog: %w", err)
	}
	stamp := now().UTC().Format("20060102T150405Z")
	base := stamp + "-" + safeRole(role)
	var f *os.File
	var err error
	path := ""
	for i := 0; i < 100; i++ { // two sessions in one second get -2, -3, ...
		path = filepath.Join(dir, base+".log")
		if i > 0 {
			path = filepath.Join(dir, fmt.Sprintf("%s-%d.log", base, i+1))
		}
		f, err = os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if !errors.Is(err, os.ErrExist) {
			break
		}
	}
	if err != nil {
		return nil, fmt.Errorf("applog: %w", err)
	}
	home, _ := os.UserHomeDir()
	lf := &File{f: f, path: path, now: now, red: Redactor{Home: home}}
	_ = Prune(dir, Keep) // best effort: an old file we can't remove is not the session's problem
	return lf, nil
}

// Path is the file's path.
func (l *File) Path() string { return l.path }

var levelTag = map[log.Level]string{log.Info: "INFO", log.Net: "NET ", log.Warn: "WARN", log.Err: "ERR ", log.OK: "OK  "}

// Log writes one line: "2006-01-02T15:04:05.000Z LEVEL msg", redacted. A
// message of several lines is written as several, each stamped.
func (l *File) Log(level log.Level, msg string) {
	msg = l.red.Redact(msg)
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return
	}
	ts := l.now().UTC().Format("2006-01-02T15:04:05.000Z")
	tag, ok := levelTag[level]
	if !ok {
		tag = "INFO"
	}
	for _, part := range strings.Split(strings.TrimRight(msg, "\r\n"), "\n") {
		line := ts + " " + tag + " " + strings.TrimRight(part, "\r")
		l.remember(line)
		l.write(line + "\n")
	}
}

// Panic writes a recovered panic and its stack, for a process whose user
// has no console to see it.
func (l *File) Panic(v any, stack []byte) {
	l.Log(log.Err, fmt.Sprintf("panic: %v\n%s", v, stack))
}

func (l *File) write(s string) {
	if l.full {
		return
	}
	if l.n+int64(len(s)) > maxBytes {
		l.full = true
		s = fmt.Sprintf("%s WARN log file reached %d bytes; later lines are not written (the app's log view still has them)\n", l.now().UTC().Format("2006-01-02T15:04:05.000Z"), maxBytes)
	}
	n, _ := l.f.WriteString(s)
	l.n += int64(n)
}

func (l *File) remember(line string) {
	if len(l.tail) < TailLines {
		l.tail = append(l.tail, line)
		return
	}
	l.tail[l.next] = line
	l.next = (l.next + 1) % TailLines
}

// Tail returns the newest lines written (at most TailLines), oldest first.
func (l *File) Tail() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]string, 0, len(l.tail))
	out = append(out, l.tail[l.next:]...)
	return append(out, l.tail[:l.next]...)
}

// Close closes the file; later lines are dropped.
func (l *File) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return nil
	}
	l.closed = true
	return l.f.Close()
}

// Prune removes the oldest *.log files in dir until keep remain. The
// names start with their UTC time, so name order is age order.
func Prune(dir string, keep int) error {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	var logs []string
	for _, e := range ents {
		if e.Type().IsRegular() && strings.HasSuffix(e.Name(), ".log") {
			logs = append(logs, e.Name())
		}
	}
	sort.Strings(logs)
	var errs []error
	for len(logs) > keep {
		if err := os.Remove(filepath.Join(dir, logs[0])); err != nil {
			errs = append(errs, err)
		}
		logs = logs[1:]
	}
	return errors.Join(errs...)
}

// safeRole keeps letters, digits and dashes of role for a file name.
func safeRole(role string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(role) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-':
			b.WriteRune(r)
		}
		if b.Len() >= 24 {
			break
		}
	}
	if b.Len() == 0 {
		return "session"
	}
	return b.String()
}

// Redactor takes what a log or a bug report should not carry out of a
// line.
type Redactor struct {
	// Home is the user's home directory, replaced by ~ (it usually holds
	// the user's account name). "" leaves paths alone.
	Home string
}

// tokenRe matches a value labelled as a token, secret, password or key:
// "token=abc", "token: abc", `"token":"abc"`. The lobby's reconnect token
// (docs/01) is the first secret the app has.
var tokenRe = regexp.MustCompile(`(?i)("?(?:token|secret|password|passwd|api[_-]?key)"?\s*[:=]\s*"?)([^\s",}]+)`)

// Redact returns line with the home directory as ~ and token values as
// <redacted>.
func (r Redactor) Redact(line string) string {
	if h := strings.TrimRight(r.Home, `\/`); len(h) > 1 {
		for _, form := range []string{h, filepath.ToSlash(h), strings.ReplaceAll(h, `\`, `\\`)} {
			line = replaceFold(line, form, "~")
		}
	}
	return tokenRe.ReplaceAllString(line, "${1}<redacted>")
}

// replaceFold replaces old in s, ignoring case on Windows (where paths do).
func replaceFold(s, old, repl string) string {
	if runtime.GOOS != "windows" {
		return strings.ReplaceAll(s, old, repl)
	}
	ls, lo := strings.ToLower(s), strings.ToLower(old)
	if len(ls) != len(s) || len(lo) != len(old) {
		return strings.ReplaceAll(s, old, repl) // case folding changed lengths: exact match only
	}
	var b strings.Builder
	for {
		i := strings.Index(ls, lo)
		if i < 0 {
			b.WriteString(s)
			return b.String()
		}
		b.WriteString(s[:i])
		b.WriteString(repl)
		s, ls = s[i+len(old):], ls[i+len(old):]
	}
}
