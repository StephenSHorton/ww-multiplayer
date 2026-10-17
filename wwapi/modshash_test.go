package wwapi_test

import (
	"crypto/sha256"
	"fmt"
	"reflect"
	"testing"

	"github.com/StephenSHorton/ww-multiplayer/wwapi"
)

// TestModsHash: only the client_required mods that load count, sorted by
// "id@version", hashed as the lines joined by "\n".
func TestModsHash(t *testing.T) {
	mods := []wwapi.Mod{
		mk(t, "zeta", "2.0.0", `"multiplayer":"client_required"`),
		mk(t, "alpha", "1.2.3", `"multiplayer":"client_required"`),
		mk(t, "local", "1.0.0", ``),
		mk(t, "hostonly", "1.0.0", `"multiplayer":"host_only"`),
		mk(t, "off", "1.0.0", `"multiplayer":"client_required","enabled_by_default":false`),
		mk(t, "broken", "1.0.0", `"multiplayer":"client_required","dependencies":{"absent":"*"}`),
	}
	id := wwapi.Resolve(mods, wwapi.ResolveOptions{}).ModsID()
	want := []string{"alpha@1.2.3", "zeta@2.0.0"}
	if !reflect.DeepEqual(id.List, want) {
		t.Fatalf("List = %v, want %v", id.List, want)
	}
	if id.Hash != sha256.Sum256([]byte("alpha@1.2.3\nzeta@2.0.0")) {
		t.Errorf("Hash = %x", id.Hash)
	}
	// A pinned value: the hash is on the wire, so its input format must
	// not drift.
	if got := wwapi.ModsIDOf([]string{"multiplayer@1.0.0", "coop@1.0.0"}); got.Hash != wwapi.ModsIDOf([]string{"coop@1.0.0", "multiplayer@1.0.0"}).Hash ||
		got.List[0] != "coop@1.0.0" {
		t.Errorf("ModsIDOf does not sort: %v", got.List)
	}
	const pinned = "c47ea77738fba9b096423d844491cffe8c8a2d96575b0355fa2f091d2c1296c1"
	if got := fmt.Sprintf("%x", wwapi.ModsIDOf([]string{"multiplayer@1.0.0", "coop@1.0.0"}).Hash); got != pinned {
		t.Errorf("hash of coop + multiplayer 1.0.0 = %s, want %s", got, pinned)
	}
}

// TestModsHashDiffers: a version change or a missing mod changes the
// hash; order and non-counting mods do not.
func TestModsHashDiffers(t *testing.T) {
	a := wwapi.ModsIDOf([]string{"coop@1.0.0", "multiplayer@1.0.0"})
	for _, l := range [][]string{
		{"multiplayer@1.0.0"},
		{"coop@1.0.1", "multiplayer@1.0.0"},
		{"coop@1.0.0", "multiplayer@1.0.0", "x@1.0.0"},
		{"coop@1.0.0multiplayer@1.0.0"},
	} {
		if wwapi.ModsIDOf(l).Hash == a.Hash {
			t.Errorf("%v hashes like %v", l, a.List)
		}
	}
	infos := []wwapi.ModInfo{
		{ID: "multiplayer", Version: "1.0.0", Status: wwapi.Loaded, Multiplayer: wwapi.ClientRequired},
		{ID: "example", Version: "0.1.0", Status: wwapi.Loaded, Multiplayer: wwapi.LocalOnly},
		{ID: "coop", Version: "1.0.0", Status: wwapi.Loaded, Multiplayer: wwapi.ClientRequired},
		{ID: "q", Version: "1.0.0", Status: wwapi.Quarantined, Multiplayer: wwapi.ClientRequired},
	}
	if got := wwapi.ModsHash(infos); got.Hash != a.Hash || !reflect.DeepEqual(got.List, a.List) {
		t.Errorf("ModsHash = %v %x, want %v", got.List, got.Hash, a.List)
	}
}
