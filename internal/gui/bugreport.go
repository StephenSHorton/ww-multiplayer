package gui

import (
	"runtime"
	"strings"

	"github.com/StephenSHorton/ww-multiplayer/internal/applog"
	"github.com/StephenSHorton/ww-multiplayer/internal/bugreport"
)

// BugReportView is a written bug report, for the window's toast.
type BugReportView struct {
	// Path is the zip, the home folder shown as ~ (applog.Redactor).
	Path string `json:"path"`
	// Files are its entries; Skipped what was left out and why.
	Files   []string `json:"files"`
	Skipped []string `json:"skipped"`
}

// bugSeams are where SaveBugReport reads the session logs and writes the
// zip (applog.DefaultDir, bugreport.DefaultDir; a test's own).
type bugSeams struct {
	logDir    func() (string, error)
	reportDir func() (string, error)
}

func (s *bugSeams) init() {
	if s.logDir == nil {
		s.logDir = applog.DefaultDir
	}
	if s.reportDir == nil {
		s.reportDir = bugreport.DefaultDir
	}
}

// SaveBugReport writes the bug-report zip (internal/bugreport): the newest
// session logs, this player's Dolphin log when Dolphin writes one, and
// about.txt (the diagnostics: version, OS, Dolphin, user folder, co-op
// status), every file redacted, no game files or saves. Nothing is sent:
// the window then opens its folder (OpenFolder "bugreports") for the
// player to attach it.
func (b *Bridge) SaveBugReport() (_ BugReportView, err error) {
	defer b.maskErr(&err)
	dir, err := b.bug.reportDir()
	if err != nil {
		return BugReportView{}, err
	}
	logs, _ := b.bug.logDir() // no log folder: a report without logs
	o := b.launchOptions()
	var status []string
	if s := b.CoopStatusText(); s != "co-op: not running" {
		status = strings.Split(s, "\n")
	}
	home := b.home()
	about := applog.Diagnostics{
		App: "ww-multiplayer " + b.version, OS: runtime.GOOS + "/" + runtime.GOARCH,
		Dolphin: o.Exe, UserDir: o.UserDir, LogFile: logs, Status: status, LogFiles: true,
	}.Text(applog.Redactor{Home: home})
	res, err := bugreport.Write(dir, bugreport.Options{
		LogDir: logs, DolphinUserDirs: []string{o.UserDir}, About: about, Home: home,
	})
	if err != nil {
		return BugReportView{}, err
	}
	return BugReportView{
		Path: applog.Redactor{Home: home}.Redact(res.Path), Files: res.Files,
		Skipped: append([]string{}, res.Skipped...),
	}, nil
}
