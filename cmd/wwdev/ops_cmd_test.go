package main

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/wwapi/mem"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/ops/opstest"
)

// TestRunOps: status, echo and kill against the fake runtime.
func TestRunOps(t *testing.T) {
	m := mem.NewFakeMem()
	var out bytes.Buffer
	if err := runOps(m, []string{"status"}, &out); err == nil {
		t.Error("status before boot: no error")
	}
	rt := opstest.New(m)
	rt.Boot(nil)
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		for {
			select {
			case <-stop:
				return
			case <-time.After(time.Millisecond):
				rt.Frame()
			}
		}
	}()
	out.Reset()
	if err := runOps(m, []string{"status"}, &out); err != nil || !strings.Contains(out.String(), `dir "EVTR"`) {
		t.Errorf("status: %v\n%s", err, out.String())
	}
	out.Reset()
	if err := runOps(m, []string{"echo", "1", "2", "3"}, &out); err != nil || !strings.Contains(out.String(), "echo: ok") {
		t.Errorf("echo: %v\n%s", err, out.String())
	}
	out.Reset()
	if err := runOps(m, []string{"kill", "5", "0x1D5"}, &out); err != nil || !strings.Contains(out.String(), "not_found") {
		t.Errorf("kill: %v\n%s", err, out.String())
	}
	if err := runOps(m, []string{"bogus"}, &out); err == nil {
		t.Error("bogus: no error")
	}
}
