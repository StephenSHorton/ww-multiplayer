package main

import (
	"errors"
	"fmt"
	"os"
	"runtime"
	"strings"

	"github.com/StephenSHorton/ww-multiplayer/internal/applog"
	"github.com/StephenSHorton/ww-multiplayer/internal/bugreport"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/emu/launch"
)

const bugReportUsage = "bug-report [--include FILE]... [--user-dir DIR]... [--out DIR]"

// bugReportArgs are bug-report's flags.
type bugReportArgs struct {
	include  []string // more text files (a terminal capture, mp-local's output)
	userDirs []string // Dolphin user folders whose Logs/dolphin.log goes in
	out      string   // where the zip goes ("" is bugreport.DefaultDir)
}

func parseBugReportArgs(args []string) (bugReportArgs, error) {
	var a bugReportArgs
	for i := 0; i < len(args); i++ {
		flag, val, has := strings.Cut(args[i], "=")
		switch flag {
		case "--include", "--user-dir", "--out":
		default:
			return a, fmt.Errorf("unknown argument %q", args[i])
		}
		if !has {
			if i+1 >= len(args) {
				return a, fmt.Errorf("%s needs a value", flag)
			}
			i++
			val = args[i]
		}
		if val == "" {
			return a, fmt.Errorf("%s needs a value", flag)
		}
		switch flag {
		case "--include":
			a.include = append(a.include, val)
		case "--user-dir":
			a.userDirs = append(a.userDirs, val)
		case "--out":
			if a.out != "" {
				return a, errors.New("--out given twice")
			}
			a.out = val
		}
	}
	return a, nil
}

// cmdBugReport writes the bug-report zip the app's "Bug report" button
// writes (internal/bugreport): the app's newest session logs, Dolphin's
// log, the --include files and about.txt, every file redacted, no game
// files or saves. The CLI reads no saved settings (owner decision 11), so
// Dolphin's user folders are --user-dir's, else bugReportDolphin's.
func cmdBugReport(_ string, args []string) {
	a, err := parseBugReportArgs(args)
	if err != nil {
		exitUsage(err, bugReportUsage)
	}
	exe, dirs := bugReportDolphin(launch.Host(), launch.FromEnv())
	if len(a.userDirs) == 0 {
		a.userDirs = dirs
	}
	if a.out == "" {
		if a.out, err = bugreport.DefaultDir(); err != nil {
			fmt.Println(err)
			os.Exit(1)
		}
	}
	logs, _ := applog.DefaultDir() // none: a report without the app's logs
	home, _ := os.UserHomeDir()
	about := applog.Diagnostics{
		App: versionLine(), OS: runtime.GOOS + "/" + runtime.GOARCH, Dolphin: exe,
		UserDir: strings.Join(a.userDirs, ", "), LogFile: logs, LogFiles: true,
	}.Text(applog.Redactor{Home: home})
	res, err := bugreport.Write(a.out, bugreport.Options{
		LogDir: logs, DolphinUserDirs: a.userDirs, Extra: a.include, About: about, Home: home,
	})
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	printBugReport(res)
}

// bugReportDolphin is the Dolphin the CLI's bug-report describes and the
// user folders it reads dolphin.log from when no --user-dir is given,
// found as the GUI finds them: DOLPHIN_EXE, else a running or installed
// Dolphin (launch.Detect), and USER_DIR_1 when it is set, else the folder
// that Dolphin plays on (launch.UserDir: a portable install's User folder,
// else Dolphin's usual one), then USER_DIR_2 (or its default) for a second
// instance.
func bugReportDolphin(e launch.Env, paths launch.Paths) (exe string, userDirs []string) {
	exe = paths.Exe
	if e.Getenv == nil || e.Getenv(launch.EnvExe) == "" {
		c, _ := launch.Detect(e)
		exe = c.Exe
	}
	first := paths.UserDir1
	if e.Getenv == nil || e.Getenv(launch.EnvUserDir1) == "" {
		first, _ = launch.UserDir(e, exe)
	}
	return exe, []string{first, paths.UserDir2}
}

// printBugReport prints what a report holds. The path is printed whole:
// it is the player's own terminal.
func printBugReport(res bugreport.Result) {
	fmt.Println("Bug report saved: " + res.Path)
	for _, f := range res.Files {
		fmt.Println("  " + f)
	}
	if len(res.Skipped) > 0 {
		fmt.Println("Left out:")
		for _, s := range res.Skipped {
			fmt.Println("  " + s)
		}
	}
	fmt.Println("Every file is redacted (home folder, IP addresses, join codes, tokens); nothing was sent. Attach the zip to your issue.")
}
