package events

import (
	"errors"
	"strings"
	"testing"
)

func TestParseName(t *testing.T) {
	ok := map[string]string{
		"core.tick":                   "core",
		"core.stage_changed":          "core",
		"core.save.item_gained":       "core",
		"core._x":                     "core",
		"mod.coop.question":           "coop",
		"mod.multiplayer.peer_joined": "multiplayer",
		"mod._fixture.ping":           "_fixture",
		"mod.a1.b2.c3":                "a1",
	}
	for name, want := range ok {
		got, err := ParseName(name)
		if err != nil || got != want {
			t.Errorf("ParseName(%q) = %q, %v; want %q", name, got, err, want)
		}
	}
	bad := []string{
		"", "core", "mod", "mod.coop", "core.", ".core.tick", "core..tick",
		"Core.tick", "core.Tick", "core.1tick", "core.ti-ck", "core.ti ck",
		"game.tick", "mod.core.x", "mod.Coop.x", "mod.1x.y", "mod._.y", "mod.__x.y",
		"core.tick\n", "core.té", "mod.coop.",
		"core." + strings.Repeat("a", MaxNameLen),
	}
	for _, name := range bad {
		if _, err := ParseName(name); !errors.Is(err, ErrBadName) {
			t.Errorf("ParseName(%q) err = %v, want ErrBadName", name, err)
		}
	}
	if _, err := ParseName("core." + strings.Repeat("a", MaxNameLen-5)); err != nil {
		t.Errorf("a name of exactly MaxNameLen: %v", err)
	}
}

func TestValidModID(t *testing.T) {
	for _, id := range []string{"coop", "multiplayer", "a", "a_b", "a1", "_fixture", "_a1", strings.Repeat("a", MaxModIDLen)} {
		if !ValidModID(id) {
			t.Errorf("%q should be valid", id)
		}
	}
	for _, id := range []string{"", "core", "_", "__a", "1a", "_1a", "Coop", "co-op", "co.op", "a b", strings.Repeat("a", MaxModIDLen+1)} {
		if ValidModID(id) {
			t.Errorf("%q should be invalid", id)
		}
	}
}

// FuzzParseName checks ParseName's invariants on any input: it never
// panics; an accepted name is short, lowercase ASCII, and its owner is
// either core or a valid mod id that the name is namespaced under; and a
// mod view of that owner may emit it while every other view may not.
func FuzzParseName(f *testing.F) {
	for _, s := range []string{"core.tick", "mod.coop.question", "mod._f.x", "core", "mod.core.x", "a.b", "", "mod.coop", "core..x"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, name string) {
		owner, err := ParseName(name)
		if err != nil {
			if !errors.Is(err, ErrBadName) || owner != "" {
				t.Fatalf("%q: owner %q err %v", name, owner, err)
			}
			return
		}
		if len(name) > MaxNameLen {
			t.Fatalf("%q accepted at %d bytes", name, len(name))
		}
		for i := 0; i < len(name); i++ {
			c := name[i]
			if !(segByte(c) || c == '.') {
				t.Fatalf("%q accepted with byte %q", name, c)
			}
		}
		var prefix string
		if owner == CoreNamespace {
			prefix = "core."
		} else {
			if !ValidModID(owner) {
				t.Fatalf("%q: owner %q is not a valid mod id", name, owner)
			}
			prefix = "mod." + owner + "."
		}
		if !strings.HasPrefix(name, prefix) || len(name) == len(prefix) {
			t.Fatalf("%q does not sit under %q", name, prefix)
		}

		b := New(Options{})
		views := map[string]*Bus{"core": b}
		for _, id := range []string{"coop", "multiplayer", "_f"} {
			views[id] = mustMod(t, b, id)
		}
		if owner != CoreNamespace && views[owner] == nil {
			views[owner] = mustMod(t, b, owner)
		}
		for id, v := range views {
			err := v.Emit(namedEvent{name: name})
			if id == owner && err != nil {
				t.Fatalf("owner %q cannot emit %q: %v", id, name, err)
			}
			if id != owner && !errors.Is(err, ErrNamespace) {
				t.Fatalf("%q emitted %q: %v", id, name, err)
			}
		}
	})
}
