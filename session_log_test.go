package main

import (
	"os"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/internal/report"
	"github.com/StephenSHorton/ww-multiplayer/internal/session"
)

func TestOpenSessionLog(t *testing.T) {
	dir := t.TempDir()
	t0 := time.Date(2026, 10, 16, 9, 30, 0, 0, time.UTC)
	st := session.State{Session: 2, Role: session.RoleJoin, Name: "Bea", Addr: "26.1.2.3", Coop: session.CoopOptions{On: true}}
	rep, done := openSessionLog(dir, st, func() time.Time { return t0 })
	if rep == nil {
		t.Fatal("no log")
	}
	rep.Log(report.Net, "connected")
	done()
	ents, _ := os.ReadDir(dir)
	if len(ents) != 1 || ents[0].Name() != "20261016T093000Z-join.log" {
		t.Fatalf("files %v", ents)
	}
	b, _ := os.ReadFile(dir + "/" + ents[0].Name())
	want := "2026-10-16T09:30:00.000Z INFO " + versionLine() + " " + runtime.GOOS + "/" + runtime.GOARCH + `: Join session 2 as "Bea" to 26.1.2.3 with co-op` + "\n" +
		"2026-10-16T09:30:00.000Z NET  connected\n"
	if string(b) != want {
		t.Errorf("log:\n%s\nwant:\n%s", b, want)
	}
	if !strings.Contains(sessionLogHeader(session.State{Session: 1, Role: session.RoleHost, Name: "Ann"}), `Host session 1 as "Ann"`) {
		t.Error("host header")
	}

	// A directory that can't be made: no log, and the session goes on.
	file := dir + "/" + ents[0].Name()
	if rep, done := openSessionLog(file+"/sub", st, time.Now); rep != nil || done != nil {
		t.Error("opened a log under a file")
	}
}
