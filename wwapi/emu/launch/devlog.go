package launch

import (
	"path/filepath"
	"strings"
)

// EnvDevLog turns the test Dolphins' file log on (DevLogArgs) for wwdev's
// dolphin2 and auto-recapture: "1", "true" or "on". Unset, they launch as
// before. The app's Dolphin launch never reads it.
const EnvDevLog = "WW_DOLPHIN_DEVLOG"

// DevLogEnabled reports whether getenv turns EnvDevLog on.
func DevLogEnabled(getenv func(string) string) bool {
	switch strings.ToLower(strings.TrimSpace(getenv(EnvDevLog))) {
	case "1", "true", "on", "yes":
		return true
	}
	return false
}

// DevLogArgs are the Dolphin arguments of a test launch that logs to
// <user dir>/Logs/dolphin.log (LogFile) and never stops on a modal
// popup: the log file on at Info, the OSReport channels (EXI and HLE: the
// game's and the mod's OSReport lines), MI (memory interface and map:
// Dolphin's "Invalid read/write ... PC = ..." MMU lines) and MASTER
// (where a panic alert is logged whether or not it pops up), then the
// panic handlers and the stop confirmation off, so an "Invalid read"
// panic is a log line and not a frozen instance.
//
// All -C, as MEM1Override: a session-only config layer over the user
// dir's, so nothing is written to the user dir's ini files. Dolphin does
// append to <user dir>/Logs/dolphin.log at Info and nothing truncates it,
// so the dev tools only pass these for a user dir set explicitly
// (USER_DIR_1 / USER_DIR_2) that is no player's profile
// (DevLogUserDirError, devlog_guard.go).
//
// The [Logs] keys are Dolphin's short log names, not the LogType enum
// names: MI, not MEMMAP, and MASTER, not MASTER_LOG (read from the
// LogManager table in Dolphin 2609a's binary, next to "Memory Interface
// & Memory Map" and "Master Log"); ww-online's scripts/dev-test.ps1
// writes MEMMAP and MASTER_LOG, which name no channel. "Logger" is in
// the same build's config-system name table (with Dolphin, GFX,
// DualShockUDPClient, Session, ...), so -C Logger.* parses as the
// Logger.ini layer, and the Options/Verbosity, Options/WriteToFile and
// Interface/ConfirmStop locations are config entries there.
// UNVERIFIED live: that the log manager picks the command-line layer up
// at startup (docs/TESTING.md "Test Dolphin logs").
var DevLogArgs = []string{
	"-C", "Logger.Options.WriteToFile=True",
	"-C", "Logger.Options.WriteToConsole=False",
	"-C", "Logger.Options.Verbosity=4", // Info (Notice 1, Error 2, Warning 3, Info 4, Debug 5)
	"-C", "Logger.Logs.OSREPORT=True",
	"-C", "Logger.Logs.OSREPORT_HLE=True",
	"-C", "Logger.Logs.MI=True",
	"-C", "Logger.Logs.MASTER=True",
	"-C", "Dolphin.Interface.UsePanicHandlers=False",
	"-C", "Dolphin.Interface.ConfirmStop=False",
}

// LogFile is where a Dolphin launched with -u userDir writes its log
// (Dolphin's F_MAINLOG_IDX: <user dir>/Logs/dolphin.log).
func LogFile(userDir string) string {
	return filepath.Join(userDir, "Logs", "dolphin.log")
}

// TestArgs is LaunchArgs plus DevLogArgs when devLog is set.
func TestArgs(userDir, iso, saveState string, devLog bool) []string {
	args := LaunchArgs(userDir, iso, saveState)
	if devLog {
		args = append(args, DevLogArgs...)
	}
	return args
}

// LaunchTest is Launch for the dev tools (wwdev dolphin2, auto-recapture):
// with devLog, Dolphin also gets DevLogArgs.
func LaunchTest(exe, userDir, iso, saveState string, devLog bool) (int, error) {
	return launch(exe, TestArgs(userDir, iso, saveState, devLog))
}
