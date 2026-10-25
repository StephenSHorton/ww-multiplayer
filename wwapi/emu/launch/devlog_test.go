package launch

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestTestArgs(t *testing.T) {
	plain := TestArgs("ud", "game.iso", "s.sav", false)
	if !slices.Equal(plain, LaunchArgs("ud", "game.iso", "s.sav")) {
		t.Fatalf("devLog off: %q, want LaunchArgs", plain)
	}
	got := TestArgs("ud", "game.iso", "", true)
	if !slices.Equal(got[:len(got)-len(DevLogArgs)], LaunchArgs("ud", "game.iso", "")) {
		t.Fatalf("devLog on does not start with LaunchArgs: %q", got)
	}
	// Every extra is a -C System.Section.Key=Value pair, and the set
	// names the channels and switches the log needs.
	want := map[string]bool{
		"Logger.Options.WriteToFile=True": true, "Logger.Logs.OSREPORT=True": true,
		"Logger.Logs.OSREPORT_HLE=True": true, "Logger.Logs.MI=True": true, "Logger.Logs.MASTER=True": true,
		"Dolphin.Interface.UsePanicHandlers=False": true, "Dolphin.Interface.ConfirmStop=False": true,
	}
	for i := 0; i < len(DevLogArgs); i += 2 {
		if DevLogArgs[i] != "-C" {
			t.Fatalf("DevLogArgs[%d] = %q, want -C", i, DevLogArgs[i])
		}
		kv := DevLogArgs[i+1]
		k, _, ok := strings.Cut(kv, "=")
		if !ok || strings.Count(k, ".") != 2 {
			t.Errorf("%q is not System.Section.Key=Value", kv)
		}
		delete(want, kv)
	}
	if len(want) != 0 {
		t.Errorf("DevLogArgs lacks %v", want)
	}
	// The enum names name no Dolphin log channel.
	for _, a := range DevLogArgs {
		if strings.Contains(a, "MEMMAP") || strings.Contains(a, "MASTER_LOG") {
			t.Errorf("%q uses an enum name, not the channel's short name", a)
		}
	}
}

func TestDevLogEnabled(t *testing.T) {
	for v, want := range map[string]bool{"": false, "0": false, "off": false, "1": true, "true": true, " ON ": true} {
		if got := DevLogEnabled(func(string) string { return v }); got != want {
			t.Errorf("%s=%q: %v, want %v", EnvDevLog, v, got, want)
		}
	}
}

func TestLogFile(t *testing.T) {
	if got, want := LogFile("ud"), filepath.Join("ud", "Logs", "dolphin.log"); got != want {
		t.Errorf("LogFile = %q, want %q", got, want)
	}
}
