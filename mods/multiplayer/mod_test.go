package multiplayer

import (
	"testing"

	"github.com/StephenSHorton/ww-multiplayer/internal/session"
	"github.com/StephenSHorton/ww-multiplayer/wwapi"
)

// TestManifest: the embedded mod.json is the network provider's, every
// player needs it, and it needs the blob's avatar, face, tunic and input.
func TestManifest(t *testing.T) {
	m := New(nil).Manifest()
	if err := m.Err(); err != nil {
		t.Fatal(err)
	}
	if m.ID != "multiplayer" || m.Version.String() != "1.0.0" || !m.EnabledByDefault || m.Dev {
		t.Errorf("manifest %+v", m)
	}
	if !m.Provide(wwapi.ProvideNet) || m.Multiplayer != wwapi.ClientRequired {
		t.Errorf("provides %v, multiplayer %q", m.Provides, m.Multiplayer)
	}
	if miss := wwapi.PreE1Features().Missing(m.Needs); len(m.Needs) != 4 || miss != nil {
		t.Errorf("needs %v (missing from a pre-E1 blob: %v)", m.Needs, miss)
	}
}

// TestResolve: the mod loads on its own, as the net provider, and needs
// a repatch on a blob without its features.
func TestResolve(t *testing.T) {
	infos := wwapi.Resolve([]wwapi.Mod{New(nil)}, wwapi.ResolveOptions{}).Infos()
	if len(infos) != 1 || infos[0].Status != wwapi.Loaded {
		t.Fatalf("infos %+v", infos)
	}
	infos = wwapi.Resolve([]wwapi.Mod{New(nil)}, wwapi.ResolveOptions{Features: wwapi.KnownFeatures("avatar", "face")}).Infos()
	if infos[0].Status != wwapi.NeedsRepatch {
		t.Fatalf("without tunic and input: %s", infos[0].StatusString())
	}
}

type fakeEngine struct{ session.Engine }

// TestEngine: the engine is built once, on the first call.
func TestEngine(t *testing.T) {
	built := 0
	m := New(func() session.Engine { built++; return &fakeEngine{} })
	if built != 0 {
		t.Fatal("built before Engine")
	}
	e := m.Engine()
	if m.Engine() != e || built != 1 {
		t.Fatalf("built %d times", built)
	}
	if New(nil).Engine() != nil {
		t.Error("an engine without a builder")
	}
}

// TestAPI: other mods validate a player name as the server does.
func TestAPI(t *testing.T) {
	api, ok := New(nil).API().(API)
	if !ok {
		t.Fatalf("API is %T", New(nil).API())
	}
	if err := api.ValidatePlayerName("Link"); err != nil {
		t.Error(err)
	}
	for _, bad := range []string{"", "a\nb", string([]byte{0xff})} {
		if api.ValidatePlayerName(bad) == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}
