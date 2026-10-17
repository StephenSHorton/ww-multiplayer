package main

// coop-world block's tests: what it parses, and how it reads a world's
// session marker (mods/coop/session).

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/mods/coop/procstate"
	coopsession "github.com/StephenSHorton/ww-multiplayer/mods/coop/session"
	"github.com/StephenSHorton/ww-multiplayer/mods/coop/world"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
)

// coTestChild is the test binary, run with -test.run=run and with the
// variables in env set.
func coTestChild(run string, env ...string) *exec.Cmd {
	cmd := exec.Command(os.Args[0], "-test.run="+run)
	// Under -race a clean exit otherwise sleeps 1s for the detector.
	cmd.Env = append(append(os.Environ(), env...), "GORACE="+strings.TrimSpace(os.Getenv("GORACE")+" atexit_sleep_ms=0"))
	return cmd
}

// coDeadPid returns the pid of a child process that has exited and been
// waited for, which no process has unless the OS reuses it in the
// meantime.
func coDeadPid(t *testing.T) int {
	t.Helper()
	cmd := coTestChild("^$")
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	return cmd.ProcessState.Pid()
}

// coSetProcState makes coopsession.ProcState f until t ends.
func coSetProcState(t *testing.T, f func(pid int) (bool, string, error)) {
	t.Helper()
	old := coopsession.ProcState
	coopsession.ProcState = f
	t.Cleanup(func() { coopsession.ProcState = old })
}

// coProcs is a coopsession.ProcState that sees only the processes in
// procs, by pid with their start tokens.
func coProcs(procs map[int]string) func(int) (bool, string, error) {
	return func(pid int) (bool, string, error) {
		start, ok := procs[pid]
		return ok, start, nil
	}
}

// coWorld creates a world under root, founded at created, with img merged.
func coWorld(t *testing.T, root string, id world.ID, created time.Time, img *world.Image) {
	t.Helper()
	w := world.New(id, created)
	if img != nil {
		w.Merge(img)
	}
	st, err := world.Create(root, w, world.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Stop(); err != nil {
		t.Fatal(err)
	}
}

func TestParseBlockTarget(t *testing.T) {
	// The bits, from the game's setters: onEventBit ORs the id's low byte
	// into mEvent[id >> 8] (d_save.cpp:1187-1189); onTbox, onSwitch and
	// onItem set bit no&0x1F of a big-endian u32 (d_save.cpp:1072-1075,
	// 1084-1087, 1112-1115) at dSv_memBit_c +0x00, +0x04 and +0x14
	// (d_save.h:684-686).
	mb := addr.OffMemBit(3)
	tests := []struct {
		args    []string
		off     int
		mask    byte
		wantErr string
	}{
		{args: []string{"byte", "0x10", "0x81"}, off: 0x10, mask: 0x81},
		{args: []string{"event", "0x0A02"}, off: addr.OffEvent + 0x0A, mask: 0x02},
		{args: []string{"event", "0x4180"}, off: addr.OffEvent + 0x41, mask: 0x80},
		{args: []string{"chest", "3", "0x1F"}, off: mb + 0x00, mask: 0x80},
		{args: []string{"chest", "3", "0"}, off: mb + 0x03, mask: 0x01},
		{args: []string{"switch", "3", "0x05"}, off: mb + 0x04 + 3, mask: 0x20},
		{args: []string{"switch", "3", "0x7F"}, off: mb + 0x04 + 12, mask: 0x80},
		{args: []string{"item", "3", "9"}, off: mb + 0x14 + 2, mask: 0x02},

		{args: []string{"byte", "0x10"}, wantErr: "takes 2 argument"},
		{args: []string{"byte", fmt.Sprint(addr.SaveImageSize), "1"}, wantErr: "offset"},
		{args: []string{"byte", "0", "0x100"}, wantErr: "mask"},
		{args: []string{"byte", "-1", "1"}, wantErr: "offset"},
		{args: []string{"event", "0x0A00"}, wantErr: "names no bit"},
		{args: []string{"event", "0x4201"}, wantErr: "no named event bit"},
		{args: []string{"event", "0x7901"}, wantErr: "register byte"},
		{args: []string{"event", "0x10000"}, wantErr: "event"},
		{args: []string{"switch", "3", "0x80"}, wantErr: "doesn't hold"},
		{args: []string{"switch", "3", "0xC0"}, wantErr: "doesn't hold"},
		{args: []string{"switch", fmt.Sprint(addr.TableCount), "0"}, wantErr: "save table"},
		{args: []string{"chest", "3", "32"}, wantErr: "out of range"},
		{args: []string{"item", "3", "64"}, wantErr: "out of range"},
		{args: []string{"kill", "3", "1"}, wantErr: "unknown bit kind"},
	}
	for _, tt := range tests {
		got, err := parseBlockTarget(tt.args)
		if tt.wantErr != "" {
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("parseBlockTarget(%q) = %+v, %v; want an error saying %q", tt.args, got, err, tt.wantErr)
			}
			continue
		}
		if err != nil || got.off != tt.off || got.mask != tt.mask || got.what == "" {
			t.Errorf("parseBlockTarget(%q) = %+v, %v; want byte 0x%03X mask 0x%02X", tt.args, got, err, tt.off, tt.mask)
		}
	}
}

func TestCoopWorldBlock(t *testing.T) {
	root := t.TempDir()
	id := world.ID{0xB1, 0x0C, 1, 2, 3, 4, 5, 6}
	chest, err := parseBlockTarget([]string{"chest", "3", "0x1F"})
	if err != nil {
		t.Fatal(err)
	}
	var img world.Image
	img[chest.off] = chest.mask
	coWorld(t, root, id, time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC), &img)
	file := filepath.Join(world.Path(root, id), "world.wwco")
	orig, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	if err := coopWorldBlock(&out, root, id, chest, false, false); !errors.Is(err, errNotDone) || !strings.Contains(out.String(), "The world has it now") {
		t.Errorf("without --yes: %v\n%s", err, out.String())
	}
	if now, _ := os.ReadFile(file); !bytes.Equal(now, orig) {
		t.Fatal("the world file changed without --yes")
	}

	// A session serves the world: no edit, unless --force. The marker is
	// checked for real: this process wrote it.
	marker := filepath.Join(world.Path(root, id), coopsession.MarkerName)
	if _, _, err := coopsession.WriteMarker(world.Path(root, id)); err != nil {
		t.Fatal(err)
	}
	inUse := "in use by a running session"
	if !procstate.Supported {
		inUse = "may be in use by a session"
	}
	if err := coopWorldBlock(&out, root, id, chest, true, false); err == nil || !strings.Contains(err.Error(), inUse) {
		t.Errorf("in use: %v", err)
	}
	// Another process with the marker's pid runs. By its start token it
	// is the marker's; by its pid alone it may not be.
	t.Run("running", func(t *testing.T) {
		coSetProcState(t, coProcs(map[int]string{4243: "t1"}))
		for _, c := range []struct{ file, want string }{
			{"pid 4243 since 2026-10-15 17:16:19\nhost=" + coopsession.Hostname() + " start=t1\n", "is in use by a running session (pid 4243 since 2026-10-15 17:16:19): stop that host first (Ctrl+C), or pass --force if you know no host serves the world"},
			{"pid 4243 since 2026-10-15 17:16:19\n", "may be in use by a session (pid 4243 since 2026-10-15 17:16:19): a process with pid 4243 runs, but this marker has no start time, so it may not be that host; stop that host first (Ctrl+C), or pass --force if no host serves the world"},
		} {
			if err := os.WriteFile(marker, []byte(c.file), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := coopWorldBlock(&out, root, id, chest, true, false); err == nil || !strings.Contains(err.Error(), c.want) {
				t.Errorf("marker %q: %v, want %q", c.file, err, c.want)
			}
		}
	})
	// One that can't be checked is taken as in use.
	if err := os.WriteFile(marker, []byte(fmt.Sprintf("pid %d since 2026-10-15 17:16:19\nhost=elsewhere-%s start=1\n", os.Getpid(), coopsession.Hostname())), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := coopWorldBlock(&out, root, id, chest, true, false); err == nil || !strings.Contains(err.Error(), "may be in use") || !strings.Contains(err.Error(), "another machine") {
		t.Errorf("can't tell: %v", err)
	}
	if now, _ := os.ReadFile(file); !bytes.Equal(now, orig) {
		t.Fatal("the world file changed while in use")
	}
	out.Reset()
	if err := coopWorldBlock(&out, root, id, chest, true, true); err != nil || !strings.Contains(out.String(), "Blocked save table 3 chest 0x1F") {
		t.Fatalf("--force: %v\n%s", err, out.String())
	}
	w, _, err := world.Load(root, id)
	if err != nil {
		t.Fatal(err)
	}
	tomb := w.Tombstones()
	if got := w.Image(); got[chest.off]&chest.mask != 0 || tomb.Count() != 1 {
		t.Errorf("after the block: byte 0x%02X, %d tombstones", got[chest.off], tomb.Count())
	}
	// Every later proposal of it is refused.
	if r := w.Merge(&img); len(r.Delta) != 0 {
		t.Errorf("the blocked bit merged again: %+v", r)
	}

	out.Reset()
	if err := coopWorldBlock(&out, root, id, chest, true, true); err != nil || !strings.Contains(out.String(), "already blocked") {
		t.Errorf("again: %v\n%s", err, out.String())
	}
	life := blockTarget{off: addr.OffLife, mask: 0xFF, what: "life"}
	if err := coopWorldBlock(&out, root, id, life, true, true); err == nil || !strings.Contains(err.Error(), "never merges") {
		t.Errorf("a PER_PLAYER byte: %v", err)
	}
	if err := coopWorldBlock(&out, root, world.ID{9}, chest, true, true); err == nil {
		t.Error("blocked in a world that does not exist")
	}

	// A marker a killed host left names a process that has ended: block
	// says so and goes on without --force, and leaves the marker.
	t.Run("stale marker", func(t *testing.T) {
		if !procstate.Supported {
			t.Skipf("procstate.State can't check a process on %s", runtime.GOOS)
		}
		stale := fmt.Sprintf("pid %d since 2026-10-15 17:16:19\n", coDeadPid(t))
		if err := os.WriteFile(marker, []byte(stale), 0o644); err != nil {
			t.Fatal(err)
		}
		other, err := parseBlockTarget([]string{"chest", "3", "0x1E"})
		if err != nil {
			t.Fatal(err)
		}
		out.Reset()
		if err := coopWorldBlock(&out, root, id, other, true, false); err != nil || !strings.Contains(out.String(), "stale session marker") ||
			!strings.Contains(out.String(), "did not end cleanly") || !strings.Contains(out.String(), "Blocked save table 3 chest 0x1E") {
			t.Errorf("stale: %v\n%s", err, out.String())
		}
		if b, err := os.ReadFile(marker); err != nil || string(b) != stale {
			t.Errorf("the marker after the block: %q, %v", b, err)
		}
	})
}
