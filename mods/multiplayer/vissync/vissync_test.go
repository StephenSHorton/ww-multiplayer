package vissync

import (
	"bytes"
	"encoding/binary"
	"testing"

	"github.com/StephenSHorton/ww-multiplayer/mods/multiplayer/net"
	"github.com/StephenSHorton/ww-multiplayer/mods/multiplayer/playercolor"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mailbox"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mem"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/player"
)

func u32(v uint32) []byte { b := make([]byte, 4); binary.BigEndian.PutUint32(b, v); return b }

// fakeLink lays out a Link with a shield model and cl model in fake RAM.
func fakeLink(t *testing.T, shieldInHand bool) *mem.FakeMem {
	t.Helper()
	m := mem.NewFakeMem()
	const link, shield, cl, node = 0x80900000, 0x80910000, 0x80920000, 0x80930000
	must := func(err error) {
		if err != nil {
			t.Fatal(err)
		}
	}
	must(m.Write(addr.PlayerPtr, u32(link)))
	must(m.Write(link+offEquippedShieldModel, u32(shield)))
	must(m.Write(link+offCLModel, u32(cl)))
	must(m.Write(cl+j3dModelNodeMtx, u32(node)))
	hand := bytes.Repeat([]byte{0x3F, 0x80, 0, 0}, 12)
	must(m.Write(node+jointRHandA*mtxBytes, hand))
	base := hand
	if !shieldInHand {
		base = bytes.Repeat([]byte{0x40, 0, 0, 0}, 12)
	}
	must(m.Write(shield+j3dModelBaseMtx, base))
	must(m.Write(link+offEquipItem, []byte{0x01, 0x03}))
	must(m.Write(addr.SelectEquip, []byte{0x38, 0x3B}))
	must(m.Write(addr.CurStageName, []byte("sea\x00\x00\x00\x00\x00")))
	return m
}

func TestLocalEquip(t *testing.T) {
	app, eq, ok := LocalEquip(fakeLink(t, true))
	if !ok || app != AppSea|AppSwordHand|AppShieldHand || eq != [2]byte{0x38, 0x3B} {
		t.Fatalf("LocalEquip = %#x %v %v", app, eq, ok)
	}
	m := fakeLink(t, false)
	_ = m.Write(addr.CurStageName, []byte("sea_T\x00\x00\x00"))
	_ = m.Write(0x80900000+offEquipItem, []byte{0x01, 0x02})
	if app, _, ok := LocalEquip(m); !ok || app != 0 {
		t.Fatalf("LocalEquip (sheathed, off the sea) = %#x %v", app, ok)
	}
	if _, _, ok := LocalEquip(mem.NewFakeMem()); ok {
		t.Fatal("LocalEquip without Link reported ok")
	}
}

// TestEquipSamplerDebouncesShield: AppShieldHand turns on only after two
// consecutive in-hand samples and off only after two consecutive
// on-the-back ones; a single torn sample either way changes nothing, the
// other bits pass through every sample, and losing Link starts it over.
func TestEquipSamplerDebouncesShield(t *testing.T) {
	in, out := fakeLink(t, true), fakeLink(t, false)
	var e EquipSampler
	step := func(m *mem.FakeMem, want bool) {
		t.Helper()
		app, eq, ok := e.Sample(m)
		if !ok || eq != [2]byte{0x38, 0x3B} || app&(AppSea|AppSwordHand) != AppSea|AppSwordHand {
			t.Fatalf("Sample = %#x %v %v", app, eq, ok)
		}
		if got := app&AppShieldHand != 0; got != want {
			t.Fatalf("shield bit %v, want %v (app %#x)", got, want, app)
		}
	}
	step(in, false)  // first sample: nothing to agree with
	step(in, true)   // two in a row
	step(out, true)  // one torn sample keeps it
	step(in, true)   //
	step(out, true)  // first of two
	step(out, false) // two in a row clear it
	step(in, false)  // one sample does not set it
	step(out, false)
	if _, _, ok := e.Sample(mem.NewFakeMem()); ok {
		t.Fatal("Sample without Link reported ok")
	}
	step(in, false) // started over: needs two again
	step(in, true)
}

func trailer(app, sword, shield byte) []byte {
	return append(make([]byte, player.FaceBytes), app, sword, shield)
}

func TestRemoteEquip(t *testing.T) {
	if e := RemoteEquip(make([]byte, player.FaceBytes+1)); e.Valid {
		t.Error("a 17-byte trailer gave an equip entry")
	}
	e := RemoteEquip(trailer(0x01|AppSwordHand, 0x39, 0xFF))
	if !e.Valid || e.Sword != 0x39 || e.Shield != 0xFF || !e.SwordHand || e.ShieldHand {
		t.Errorf("RemoteEquip = %+v", e)
	}
	if !RemoteOnSea(trailer(AppSea, 0, 0)) || RemoteOnSea(make([]byte, player.FaceBytes)) {
		t.Error("RemoteOnSea")
	}
}

func TestBuild(t *testing.T) {
	red := playercolor.Color{R: 0xE0, G: 0x10, B: 0x20}
	remotes := []network.RemotePlayer{
		{ID: 7, Name: "Tetra", FaceState: trailer(AppSea, 0x38, 0x3B), Position: &network.PlayerPosition{PosX: 100, PosZ: -50, RotY: 0x4000}, Color: red, HasColor: true},
		{ID: 3, Name: "Aryll", FaceState: append(make([]byte, player.FaceBytes), 0x01), Position: &network.PlayerPosition{}}, // casual, no equip bytes
		{ID: 5, Name: "me", FaceState: trailer(AppSea, 0, 0), Position: &network.PlayerPosition{}},
		{ID: 9, Name: "Medli", FaceState: trailer(AppSea, 0xFF, 0xFF)}, // no position yet
	}
	b := Build(Settings{Names: true, Equip: true, SeaChart: true, Hair: true}, remotes, map[byte]int{7: 1, 3: 0, 5: 1}, "me")
	if b.Flags != mailbox.VisFlagNames|mailbox.VisFlagEquip|mailbox.VisFlagSea|mailbox.VisFlagHair {
		t.Errorf("flags %#x", b.Flags)
	}
	if b.Names != [2]string{"Aryll", "Tetra"} {
		t.Errorf("names %q", b.Names)
	}
	if b.Equip[0].Valid || !b.Equip[0].Casual || !b.Equip[1].Valid || b.Equip[1].Casual || b.Equip[1].Sword != 0x38 {
		t.Errorf("equip %+v", b.Equip)
	}
	if len(b.Sea) != 1 || b.Sea[0] != (mailbox.VisSea{X: 100, Z: -50, Angle: 0x4000, R: 0xE0, G: 0x10, B: 0x20}) {
		t.Errorf("sea %+v", b.Sea)
	}
	if b := Build(Settings{}, remotes, nil, ""); b.Flags != 0 || len(b.Sea) != 0 {
		t.Errorf("all off: %+v", b)
	}
}

func TestSettingsFromEnv(t *testing.T) {
	t.Setenv("WW_NAMETAGS", "off")
	t.Setenv("WW_EQUIP", "")
	t.Setenv("WW_SEACHART", "0")
	t.Setenv("WW_HAIR", "")
	if s := SettingsFromEnv(); s != (Settings{Equip: true, Hair: true}) {
		t.Errorf("SettingsFromEnv = %+v", s)
	}
	t.Setenv("WW_HAIR", "off")
	if s := SettingsFromEnv(); s != (Settings{Equip: true}) {
		t.Errorf("SettingsFromEnv = %+v", s)
	}
}

func TestSyncWritesOnChangeAndRefresh(t *testing.T) {
	m := mem.NewFakeMem()
	m.Record(true)
	var y Sync
	b := mailbox.VisBlock{Flags: mailbox.VisFlagNames, Names: [2]string{"a", ""}}
	if err := y.Write(m, b); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < RefreshEvery-1; i++ {
		_ = y.Write(m, b)
	}
	if n := len(m.Writes()); n != 1 {
		t.Fatalf("%d writes for an unchanged block, want 1", n)
	}
	_ = y.Write(m, b) // the refresh
	b.Names[1] = "b"
	_ = y.Write(m, b) // a change
	if n := len(m.Writes()); n != 3 {
		t.Fatalf("%d writes, want 3", n)
	}
	got, _ := m.Read(mailbox.VisAddr+mailbox.VisNamesOff+mailbox.VisNameBytes, 2)
	if string(got) != "b\x00" {
		t.Errorf("slot 1 name %q", got)
	}
	_ = y.Off(m)
	if mg, _ := mem.ReadU32(m, mailbox.VisAddr+mailbox.VisMagicOff); mg != 0 {
		t.Errorf("magic after Off %#x", mg)
	}
}

// TestBuildSeaByPlace (relay scoping): a remote's position place decides
// its sea chart marker over its pose trailer's sea bit, which goes stale
// once the server stops relaying its poses (another stage than ours).
// Without a place the trailer still decides.
func TestBuildSeaByPlace(t *testing.T) {
	remotes := []network.RemotePlayer{
		// Sailing now; its last pose seen here was indoors (no sea bit).
		{ID: 2, Name: "Tetra", FaceState: make([]byte, player.FaceBytes+1), Position: &network.PlayerPosition{PosX: 1}, Place: network.Place{Stage: "sea", Room: 26, InPlay: true}, HasPlace: true},
		// Gone indoors; its last pose seen here was at sea.
		{ID: 3, Name: "Aryll", FaceState: trailer(AppSea, 0, 0), Position: &network.PlayerPosition{PosX: 2}, Place: network.Place{Stage: "Abesso", InPlay: true}, HasPlace: true},
		// An old sender: the trailer bit.
		{ID: 4, Name: "Medli", FaceState: trailer(AppSea, 0, 0), Position: &network.PlayerPosition{PosX: 3}},
	}
	b := Build(Settings{SeaChart: true}, remotes, nil, "")
	if len(b.Sea) != 2 || b.Sea[0].X != 1 || b.Sea[1].X != 3 {
		t.Errorf("sea %+v, want Tetra (place) and Medli (trailer)", b.Sea)
	}
}
