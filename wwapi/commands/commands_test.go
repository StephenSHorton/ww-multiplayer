package commands

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"testing"

	"github.com/StephenSHorton/ww-multiplayer/wwapi/write"
)

func echo(_ context.Context, args json.RawMessage) (Result, error) {
	return Result{OK: true, Code: string(args)}, nil
}

// TestNamespaces: the core view registers core.* only, a mod view
// mod.<id>.* only, and every view runs every name.
func TestNamespaces(t *testing.T) {
	r := New()
	m, err := r.Mod("example")
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		r    *Registry
		name string
		want error
	}{
		{r, "core.player.warp", nil},
		{r, "mod.example.mark", ErrNamespace},
		{m, "mod.example.mark", nil},
		{m, "mod.other.mark", ErrNamespace},
		{m, "core.actor.kill", ErrNamespace},
		{r, "core.player.warp", ErrExists},
	} {
		if err := c.r.Register(c.name, Spec{}, echo); !errors.Is(err, c.want) {
			t.Errorf("Register(%q) = %v, want %v", c.name, err, c.want)
		}
	}
	if err := r.Register("Core.Bad Name", Spec{}, echo); err == nil {
		t.Error("a bad name registered")
	}
	if err := r.Register("core.nil", Spec{}, nil); err == nil {
		t.Error("a nil handler registered")
	}
	if _, err := m.Mod("x"); err == nil {
		t.Error("a mod view derived a view")
	}
	if _, err := r.Mod("core"); err == nil {
		t.Error("a mod view for core")
	}
	if got := m.Names(); !slices.Equal(got, []string{"core.player.warp", "mod.example.mark"}) {
		t.Errorf("Names %v", got)
	}
	res, err := m.Execute(context.Background(), "core.player.warp", map[string]int{"x": 1})
	if err != nil || res.Code != `{"x":1}` {
		t.Errorf("Execute: %+v, %v", res, err)
	}
	if res, err = r.Execute(context.Background(), "mod.example.mark", json.RawMessage(`[1]`)); err != nil || res.Code != `[1]` {
		t.Errorf("Execute raw: %+v, %v", res, err)
	}
	if _, err := r.Execute(context.Background(), "core.none", nil); !errors.Is(err, ErrUnknown) {
		t.Errorf("unknown: %v", err)
	}
}

// TestWriteGate: the gate refuses only commands that declare writes.
func TestWriteGate(t *testing.T) {
	r := New()
	refuse := errors.New("no")
	r.SetWriteGate(func() error { return refuse })
	if err := r.Register("core.read", Spec{}, echo); err != nil {
		t.Fatal(err)
	}
	if err := r.Register("core.write", Spec{Writes: []write.Range{{Addr: 0x80000000, Len: 4}}}, echo); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Execute(context.Background(), "core.read", nil); err != nil {
		t.Errorf("read refused: %v", err)
	}
	res, err := r.Execute(context.Background(), "core.write", nil)
	if !errors.Is(err, ErrWritesRefused) || !errors.Is(err, refuse) || res.Code != "writes_refused" {
		t.Errorf("write: %+v, %v", res, err)
	}
	if spec, ok := r.Lookup("core.write"); !ok || len(spec.Writes) != 1 {
		t.Errorf("Lookup: %+v %v", spec, ok)
	}
	r.SetWriteGate(nil)
	if _, err := r.Execute(context.Background(), "core.write", nil); err != nil {
		t.Errorf("write with no gate: %v", err)
	}
	h := Unsupported("core.actor.kill")
	if res, err := h(context.Background(), nil); !errors.Is(err, ErrUnsupported) || res.Code != "unsupported" {
		t.Errorf("Unsupported: %+v, %v", res, err)
	}
}
