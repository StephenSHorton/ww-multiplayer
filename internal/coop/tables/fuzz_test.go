package tables

import (
	"encoding/json"
	"testing"

	"github.com/StephenSHorton/ww-multiplayer/internal/coop/tables/tablestest"
)

// Fuzz target for the cache reader: Load reads a file in the user's
// config directory, which anything may have written. The seed corpus is
// built in code and runs as an ordinary test under `go test`; to fuzz it:
//
//	go test -run='^$' -fuzz=FuzzDecode -fuzztime=20s ./internal/coop/tables/

func FuzzDecode(f *testing.F) {
	tb := buildFixture(f, tablestest.Vanilla())
	good, err := json.Marshal(tb)
	if err != nil {
		f.Fatal(err)
	}
	// The smallest file decode accepts: every section empty.
	empty, err := json.Marshal(map[string]any{
		"schema": SchemaVersion, "builder": builderID(), "dol_sha256": tb.DOL, "stage_files_sha256": tb.StageFiles,
		"keys": map[string]any{}, "hearts": map[string]any{}, "switches": map[string]any{}, "enemies": map[string]any{},
	})
	if err != nil {
		f.Fatal(err)
	}
	only, err := json.Marshal(tb.Only(SectionKeys))
	if err != nil {
		f.Fatal(err)
	}
	for _, seed := range [][]byte{good, empty, only, good[:len(good)/3], nil, []byte("{}"), []byte(`{"schema":1}`), []byte("null")} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		got, err := decode(data, tb.ID)
		if err != nil {
			return
		}
		if !got.complete() || got.ID != tb.ID || got.Schema != SchemaVersion || got.Builder != builderID() {
			t.Fatalf("decoded tables that Load must refuse: %+v", got.ID)
		}
		// What decodes reads back the same once written, and reports.
		b, err := json.Marshal(got)
		if err != nil {
			t.Fatalf("re-encoding decoded tables: %v", err)
		}
		again, err := decode(b, tb.ID)
		if err != nil {
			t.Fatalf("re-decoding: %v", err)
		}
		if b2, _ := json.Marshal(again); string(b2) != string(b) {
			t.Fatalf("decode is not stable:\n%s\n%s", b, b2)
		}
		_ = got.Report()
		_ = got.Acceptance()
	})
}
