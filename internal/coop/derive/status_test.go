package derive_test

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/internal/coop/derive"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/tables"
)

// TestStatus: Status lists every key table, derived or not, with the
// loaded file's counts once the Deriver has measured it, and says whether
// max life is derived and why not.
func TestStatus(t *testing.T) {
	t.Run("no tables", func(t *testing.T) {
		st := derive.New(derive.Config{}).Status()
		if !st.NoKeyTable || len(st.Keys) != 0 || st.Life || !strings.Contains(st.LifeWhy, "no heart table") {
			t.Fatalf("%+v", st)
		}
	})

	tb := *vanillaTables(t)
	kr := *tb.Keys
	kr.Tables = slices.Clone(kr.Tables)
	for i := range kr.Tables {
		if kr.Tables[i].SaveTable == addr.TableFW {
			kr.Tables[i].Pass = false
			kr.Tables[i].Problems = []string{"2 small-key source(s) but 1 key door(s)"}
		}
	}
	tb.Keys = &kr
	r := newRig(t, &tb, drc)
	st := r.d.Status()
	if st.NoKeyTable || !st.Life || st.LifeWhy != "" || len(st.Keys) != len(kr.Tables) {
		t.Fatalf("%+v", st)
	}
	find := func(st derive.Status, table int) derive.KeyStatus {
		t.Helper()
		i := slices.IndexFunc(st.Keys, func(k derive.KeyStatus) bool { return k.Table == table })
		if i < 0 {
			t.Fatalf("no table %d in %+v", table, st.Keys)
		}
		return st.Keys[i]
	}
	if k := find(st, addr.TableDRC); !k.On || k.Seen || !strings.Contains(k.String(), "not seen yet") {
		t.Errorf("DRC before a tick: %+v, %q", k, k)
	}
	if k := find(st, addr.TableFW); k.On || !strings.Contains(k.String(), "table 4 (FW): not derived: its M1 key invariant fails") {
		t.Errorf("FW: %+v, %q", k, k)
	}

	r.run(time.Second)
	r.takeKey(1)
	r.land(addr.TableDRC, tbox(2)) // the partner's
	r.run(200 * time.Millisecond)
	k := find(r.d.Status(), addr.TableDRC)
	if !k.Seen || k.Facts.Taken != 2 || k.Facts.Sources != 4 || k.Facts.Doors != 4 || k.Target != 2 || k.Facts.Have != 1 {
		t.Fatalf("DRC after the keys: %+v", k)
	}
	if !strings.Contains(k.String(), "table 3 (DRC): 1 key(s), 2 of 4 source(s) taken, 0 of 4 door(s) open; to become 2") {
		t.Errorf("%q", k)
	}
	r.settle()
	k = find(r.d.Status(), addr.TableDRC)
	if k.Facts.Have != 2 || k.Pending || k.Target != 2 || strings.Contains(k.String(), "to become") {
		t.Errorf("DRC settled: %+v, %q", k, k)
	}
	// The partner's door: its switch is written, and held until Link's
	// next room change.
	r.land(addr.TableDRC, sw(0x10))
	r.settle()
	k = find(r.d.Status(), addr.TableDRC)
	if k.Facts.Open != 1 || k.Facts.Held != 1 || !slices.Equal(k.Held, []int{0x10}) || k.Target != 2 ||
		!strings.Contains(k.String(), "table 3 (DRC): 2 key(s), 2 of 4 source(s) taken, 1 of 4 door(s) open (door 0x10 counted after your next room change: its lock stays until then)") {
		t.Errorf("DRC with a door held: %+v, %q", k, k)
	}
	r.roomChange()
	r.settle()
	if k = find(r.d.Status(), addr.TableDRC); k.Facts.Held != 0 || len(k.Held) != 0 || k.Facts.Have != 1 || strings.Contains(k.String(), "room change") {
		t.Errorf("DRC after the room change: %+v, %q", k, k)
	}
}

// TestStatusNoHearts: a heart table that fails keeps max life off, said
// in Status.
func TestStatusNoHearts(t *testing.T) {
	tb := *vanillaTables(t)
	h := *tb.Hearts
	h.Pass, h.Problems = false, []string{"43 pieces"}
	tb.Hearts = &h
	st := derive.New(derive.Config{Tables: &tables.Tables{Keys: tb.Keys, Hearts: &h}}).Status()
	if st.Life || !strings.Contains(st.LifeWhy, "fails its check") {
		t.Fatalf("%+v", st)
	}
}
