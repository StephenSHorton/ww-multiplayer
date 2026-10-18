package savemerge

import (
	"strings"
	"testing"

	"github.com/StephenSHorton/ww-multiplayer/mods/coop/class"
	"github.com/StephenSHorton/ww-multiplayer/mods/coop/world"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/save"
)

// freshImage is a new file's image as dSv_player_status_a_c::init leaves
// it (d_save.cpp:24-47): 3 hearts, nothing equipped, every slot empty.
func freshImage(name string) save.Image {
	var img save.Image
	img[addr.OffMaxLife+1], img[addr.OffLife+1] = 12, 12
	for i := range 4 {
		img[addr.OffSelectEquip+i] = save.ItemNone
	}
	for i := range addr.SlotCount {
		img[addr.OffItems+i] = save.ItemNone
	}
	copy(img[addr.OffPlayerName:], name)
	return img
}

// setEvent sets story bit id in img.
func setEvent(img *save.Image, id uint16) {
	b := addr.EventByte(id)
	img[addr.OffEvent+b.Off] |= b.Mask
}

func hasEvent(img *save.Image, id uint16) bool {
	b := addr.EventByte(id)
	return img[addr.OffEvent+b.Off]&b.Mask == b.Mask
}

// memSwitch is image offset and mask of memory switch no of table t.
func memSwitch(t *testing.T, table, no int) (int, byte) {
	t.Helper()
	_, b, err := addr.SwitchBit(no)
	if err != nil {
		t.Fatal(err)
	}
	return addr.OffMemBit(table) + b.Off, b.Mask
}

func TestDiff(t *testing.T) {
	a, b := freshImage("Link"), freshImage("Lina")
	b[addr.OffWalletSize] = 1
	got := Diff(&a, &b)
	if len(got) != 2 {
		t.Fatalf("Diff = %+v", got)
	}
	if c := got[0]; c.Off != addr.OffWalletSize || c.A != 0 || c.B != 1 || c.Rule.Class != class.Max {
		t.Errorf("wallet change %+v", c)
	}
	if c := got[1]; c.Off != addr.OffPlayerName+3 || c.A != 'k' || c.B != 'a' || c.Rule.Class != class.PerPlayer {
		t.Errorf("name change %+v", c)
	}
	if d := Diff(&a, &a); len(d) != 0 {
		t.Errorf("Diff(a, a) = %+v", d)
	}
}

func TestMergeClasses(t *testing.T) {
	const metKorl = 0x0F80 // MET_KORL (d_save_event_flag.inc:111)
	tboxOff := addr.OffMemBit(3) + addr.MemBitOffTbox + 3
	swOff, swMask := memSwitch(t, 3, 0x05)
	granted := world.DefaultPolicy()
	granted.Grant(swOff, swMask)
	if err := granted.GrantEventBit(0x1001); err != nil {
		t.Fatal(err)
	}

	for _, c := range []struct {
		name  string
		pol   *world.Policy
		a, b  func(*save.Image)
		check func(t *testing.T, a, m *save.Image, r MergeResult)
	}{
		{"OR bits union", nil,
			func(a *save.Image) { a[tboxOff] = 0x01 },
			func(b *save.Image) { b[tboxOff] = 0x04; b[addr.OffTact] = 0x02 },
			func(t *testing.T, a, m *save.Image, r MergeResult) {
				if m[tboxOff] != 0x05 || m[addr.OffTact] != 0x02 {
					t.Errorf("chests %#x songs %#x", m[tboxOff], m[addr.OffTact])
				}
				if len(r.Gains) != 2 {
					t.Errorf("gains %+v", r.Gains)
				}
			}},
		{"OR_MASKED closed by default", nil,
			func(*save.Image) {},
			func(b *save.Image) { b[swOff] |= swMask; setEvent(b, 0x1001) },
			func(t *testing.T, a, m *save.Image, r MergeResult) {
				if *m != *a || len(r.Gains) != 0 {
					t.Errorf("default policy merged OR_MASKED bits: %+v", r.Gains)
				}
			}},
		{"OR_MASKED within the policy", granted,
			func(*save.Image) {},
			func(b *save.Image) { b[swOff] |= swMask | swMask<<1; setEvent(b, 0x1001); setEvent(b, 0x1002) },
			func(t *testing.T, a, m *save.Image, r MergeResult) {
				if m[swOff] != swMask || !hasEvent(m, 0x1001) || hasEvent(m, 0x1002) {
					t.Errorf("switch byte %#x, 0x1001 %v, 0x1002 %v", m[swOff], hasEvent(m, 0x1001), hasEvent(m, 0x1002))
				}
			}},
		{"MAX raised, never lowered", nil,
			func(a *save.Image) { a[addr.OffWalletSize] = 1; a[addr.OffArrowMax] = 60 },
			func(b *save.Image) { b[addr.OffWalletSize] = 2; b[addr.OffArrowMax] = 30 },
			func(t *testing.T, a, m *save.Image, r MergeResult) {
				if m[addr.OffWalletSize] != 2 || m[addr.OffArrowMax] != 60 {
					t.Errorf("wallet %d arrows %d", m[addr.OffWalletSize], m[addr.OffArrowMax])
				}
			}},
		{"PER_PLAYER, NEVER, TAG and DERIVED are a's", world.FullPolicy(),
			func(a *save.Image) { a[addr.OffReserve] = 0xAA },
			func(b *save.Image) {
				b[addr.OffRupee+1] = 99
				b[addr.OffLife+1] = 40
				b[addr.OffMaxLife+1] = 40
				b[addr.OffItems+19] = 0x2F
				b[addr.OffReserve] = 0x55
				b[addr.OffReserve+1] = 0x55
				b[addr.OffClearCount] = 1
				b[addr.OffEvent+0x50] = 0xFF // unnamed event byte: never
				b[addr.OffMemBit(5)+addr.MemBitOffKeyNum] = 3
			},
			func(t *testing.T, a, m *save.Image, r MergeResult) {
				if *m != *a {
					for _, c := range Diff(a, m) {
						t.Errorf("changed %s at 0x%03X (%s)", c.Rule.Field, c.Off, c.Rule.Class)
					}
				}
			}},
		{"held story bit", world.FullPolicy(),
			func(*save.Image) {},
			func(b *save.Image) { setEvent(b, metKorl); setEvent(b, 0x0801) },
			func(t *testing.T, a, m *save.Image, r MergeResult) {
				if hasEvent(m, metKorl) || hasEvent(m, 0x0801) {
					t.Error("resume-stage bits applied to a file without MET_KORL")
				}
				if len(r.Held) != 2 {
					t.Errorf("held %+v", r.Held)
				}
			}},
		{"hold released by a's own bit", world.FullPolicy(),
			func(a *save.Image) { setEvent(a, metKorl) },
			func(b *save.Image) { setEvent(b, metKorl); setEvent(b, 0x0801) },
			func(t *testing.T, a, m *save.Image, r MergeResult) {
				if !hasEvent(m, 0x0801) || len(r.Held) != 0 {
					t.Errorf("0x0801 %v held %+v", hasEvent(m, 0x0801), r.Held)
				}
			}},
	} {
		t.Run(c.name, func(t *testing.T) {
			a, b := freshImage("Link"), freshImage("Tetra")
			c.a(&a)
			c.b(&b)
			r, err := Merge(&a, &b, MergeOptions{Policy: c.pol})
			if err != nil {
				t.Fatal(err)
			}
			c.check(t, &a, &r.Image, r)
			if len(r.Derived) != 0 {
				t.Errorf("no derive, but derived %+v", r.Derived)
			}
		})
	}
}

// TestMergeProperties checks random pairs: the merge never clears a bit
// or lowers a MAX byte of a, takes nothing b lacks, changes no byte that
// doesn't merge, and is idempotent.
func TestMergeProperties(t *testing.T) {
	for seed := range uint64(40) {
		a, b := randImage(100+seed, "Link"), randImage(200+seed, "Tetra")
		pol := world.FullPolicy()
		if seed%2 == 1 {
			pol = nil
		}
		r, err := Merge(&a, &b, MergeOptions{Policy: pol})
		if err != nil {
			t.Fatal(err)
		}
		m := &r.Image
		join := world.Join(&a, &b)
		for off := range m {
			rule := class.Of(off)
			sm := world.SharedMask(off)
			switch {
			case !rule.Class.Merges():
				if m[off] != a[off] {
					t.Fatalf("seed %d: %s byte 0x%03X changed", seed, rule.Class, off)
				}
			case rule.Class == class.Max:
				if m[off]&sm < a[off]&sm || m[off]&^sm != a[off]&^sm || m[off]&sm > join[off] {
					t.Fatalf("seed %d: MAX byte 0x%03X: a %#x b %#x merged %#x", seed, off, a[off], b[off], m[off])
				}
			default:
				if m[off]&a[off] != a[off] || m[off]&^a[off]&^(b[off]&sm) != 0 {
					t.Fatalf("seed %d: OR byte 0x%03X: a %#x b %#x merged %#x", seed, off, a[off], b[off], m[off])
				}
			}
		}
		again, err := Merge(m, &b, MergeOptions{Policy: pol})
		if err != nil || again.Image != *m || len(again.Gains) != 0 {
			t.Fatalf("seed %d: merging b again changed %d byte(s): %v", seed, len(again.Gains), err)
		}
		self, _ := Merge(&a, &a, MergeOptions{Policy: pol})
		if self.Image != a {
			t.Fatalf("seed %d: Merge(a, a) != a", seed)
		}
	}
}

func TestMergeDeriveRules(t *testing.T) {
	a, b := freshImage("Link"), freshImage("Tetra")
	a[addr.OffMaxLife+1], a[addr.OffMaxMagic] = 20, 16
	b[addr.OffTact] = 1

	_, err := Merge(&a, &b, MergeOptions{Derive: func(img, _, _ *save.Image) []string {
		img[addr.OffRupee+1] = 1
		return nil
	}})
	if err == nil || !strings.Contains(err.Error(), "PER_PLAYER") {
		t.Errorf("a derive that changes a PER_PLAYER byte: %v", err)
	}

	r, err := Merge(&a, &b, MergeOptions{Derive: func(img, _, _ *save.Image) []string {
		img[addr.OffMaxLife], img[addr.OffMaxLife+1] = 0, 12
		img[addr.OffMaxMagic] = 0
		img[addr.OffItems+19] = 0x2F
		return []string{"lowered"}
	}})
	if err != nil {
		t.Fatal(err)
	}
	m := &r.Image
	if be16(m[addr.OffMaxLife:]) != 20 || m[addr.OffMaxMagic] != 16 {
		t.Errorf("max life %d magic %d: lowered", be16(m[addr.OffMaxLife:]), m[addr.OffMaxMagic])
	}
	if len(r.Notes) != 3 || r.Notes[0] != "lowered" {
		t.Errorf("notes %q", r.Notes)
	}
	if len(r.Derived) != 1 || r.Derived[0].Off != addr.OffItems+19 || len(r.Gains) != 1 || r.Gains[0].Off != addr.OffTact {
		t.Errorf("derived %+v gains %+v", r.Derived, r.Gains)
	}
}

// TestMergeHoldReleasedByDerive: b has the Master Sword and 0x2D10, a
// neither. The derive equips the sword the merged mCollect gives, which
// releases the hold on 0x2D10 (d_a_ship.cpp:4225), so a gets both.
func TestMergeHoldReleasedByDerive(t *testing.T) {
	a, b := freshImage("Link"), freshImage("Tetra")
	b[addr.OffCollectFlags] = 0x03
	b[addr.OffSelectEquip] = 0x39
	setEvent(&b, 0x2D10)

	r, err := Merge(&a, &b, MergeOptions{Policy: world.FullPolicy()})
	if err != nil {
		t.Fatal(err)
	}
	if hasEvent(&r.Image, 0x2D10) || len(r.Held) != 1 {
		t.Fatalf("without a derive: 0x2D10 %v held %+v", hasEvent(&r.Image, 0x2D10), r.Held)
	}

	r, err = Merge(&a, &b, MergeOptions{Policy: world.FullPolicy(), Derive: Derive(nil, nil)})
	if err != nil {
		t.Fatal(err)
	}
	if !hasEvent(&r.Image, 0x2D10) || len(r.Held) != 0 || r.Image[addr.OffSelectEquip] != 0x39 {
		t.Errorf("0x2D10 %v held %+v sword 0x%02X", hasEvent(&r.Image, 0x2D10), r.Held, r.Image[addr.OffSelectEquip])
	}
	if !strings.Contains(strings.Join(r.Notes, "\n"), "sword") {
		t.Errorf("notes %q", r.Notes)
	}
}
