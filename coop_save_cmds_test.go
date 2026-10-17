package main

import (
	"bytes"
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/StephenSHorton/ww-multiplayer/internal/coop/bind"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/tables/tablestest"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/world"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/savefile"
)

// saveImage is a played file's image as a new file starts
// (dSv_player_status_a_c::init, d_save.cpp:24-47): 3 hearts, nothing
// equipped, every slot empty, and a name.
func saveImage(name string) world.Image {
	var img world.Image
	img[addr.OffMaxLife+1], img[addr.OffLife+1] = 12, 12
	for i := range 4 {
		img[addr.OffSelectEquip+i] = 0xFF
	}
	for i := range addr.SlotCount {
		img[addr.OffItems+i] = 0xFF
	}
	copy(img[addr.OffPlayerName:], name)
	return img
}

// writeSave writes a GCI file holding imgs in slots 0, 1, ... (a nil one
// stays a New Game) and returns its path.
func writeSave(t *testing.T, dir, name string, imgs ...*world.Image) string {
	t.Helper()
	f := savefile.New()
	for n, img := range imgs {
		if img == nil {
			continue
		}
		var err error
		if f, err = f.WithSlot(n, img); err != nil {
			t.Fatal(err)
		}
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, f.Encode(), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func setSaveEvent(img *world.Image, id uint16) {
	b := addr.EventByte(id)
	img[addr.OffEvent+b.Off] |= b.Mask
}

// setChest sets tbox bit no of table t; setMemSwitch memory switch no.
func setChest(t *testing.T, img *world.Image, table, no int) {
	t.Helper()
	b, err := addr.TboxBit(no)
	if err != nil {
		t.Fatal(err)
	}
	img[addr.OffMemBit(table)+b.Off] |= b.Mask
}

func setMemSwitch(t *testing.T, img *world.Image, table, no int) (off int, mask byte) {
	t.Helper()
	_, b, err := addr.SwitchBit(no)
	if err != nil {
		t.Fatal(err)
	}
	img[addr.OffMemBit(table)+b.Off] |= b.Mask
	return addr.OffMemBit(table) + b.Off, b.Mask
}

func TestParseSaveArgs(t *testing.T) {
	for _, c := range []struct {
		args    string
		n       int
		iso     bool
		want    saveArgs
		wantErr string
	}{
		{"a b", 2, false, saveArgs{files: []string{"a", "b"}, slot: -1}, ""},
		{"--slot 1 a b", 2, false, saveArgs{files: []string{"a", "b"}, slot: 1}, ""},
		{"a --slot=2 b", 2, false, saveArgs{files: []string{"a", "b"}, slot: 2}, ""},
		{"a b o --iso ww.iso --slot 0", 3, true, saveArgs{files: []string{"a", "b", "o"}, slot: 0, iso: "ww.iso"}, ""},
		{"a b o --iso=ww.ciso", 3, true, saveArgs{files: []string{"a", "b", "o"}, slot: -1, iso: "ww.ciso"}, ""},
		{"a b --iso ww.iso", 2, false, saveArgs{}, "unknown flag --iso"},
		{"a b --yes", 2, false, saveArgs{}, "unknown flag --yes"},
		{"a b --slot 3", 2, false, saveArgs{}, "--slot is 0, 1 or 2"},
		{"a b --slot x", 2, false, saveArgs{}, "--slot is 0, 1 or 2"},
		{"a b --slot 0 --slot 1", 2, false, saveArgs{}, "twice"},
		{"a b --slot", 2, false, saveArgs{}, "needs a value"},
		{"a b o --iso= ", 3, true, saveArgs{}, "--iso needs"},
		{"a b o --iso x --iso y", 3, true, saveArgs{}, "twice"},
		{"a", 2, false, saveArgs{}, "give 2 save files, not 1"},
		{"a b", 3, true, saveArgs{}, "give 3 save files, not 2"},
	} {
		got, err := parseSaveArgs(strings.Fields(c.args), c.n, c.iso)
		if c.wantErr != "" {
			if err == nil || !strings.Contains(err.Error(), c.wantErr) {
				t.Errorf("%q: %v, want %q", c.args, err, c.wantErr)
			}
			continue
		}
		if err != nil || !reflect.DeepEqual(got, c.want) {
			t.Errorf("%q: %+v, %v; want %+v", c.args, got, err, c.want)
		}
	}
}

func TestSaveDiff(t *testing.T) {
	dir := t.TempDir()
	a, b := saveImage("Link"), saveImage("Linh")
	setChest(t, &a, 3, 1)
	setChest(t, &b, 3, 2)
	b[addr.OffWalletSize] = 1
	b[addr.OffReserve] = 0x01
	a[addr.OffGetItem] = 0x05 // the telescope and an empty bottle held, which is a's own
	b[addr.OffGetItem] = 0x01
	pa := writeSave(t, dir, "a.gci", &a)
	pb := writeSave(t, dir, "b.gci", nil, &b)

	var out bytes.Buffer
	if err := saveDiff(&out, saveArgs{files: []string{pa, pb}, slot: -1}); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	for _, want := range []string{
		`a: ` + pa + ` (GCI, saved 2 times), slot 0 (file 1) "Link"`,
		`b: ` + pb + ` (GCI, saved 2 times), slot 1 (file 2) "Linh"`,
		"OR (merge as a union): 2",
		"  - saved t03 chest 1\n",
		"  + saved t03 chest 2\n",
		"MAX (merge as the larger value): 1\n  ~ mWalletSize: 0x00 -> 0x01",
		"PER_PLAYER (each player's own): 2\n  - mGetItem[0] bit 2\n  ~ mInfo+",
		"TAG (the co-op binding tag): 1\n  ~ mReserve+0x00: 0x00 -> 0x01",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("save-diff output lacks %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "OR_MASKED") || strings.Contains(got, "the same") {
		t.Errorf("save-diff output:\n%s", got)
	}

	out.Reset()
	if err := saveDiff(&out, saveArgs{files: []string{pa, pa}, slot: 0}); err != nil || !strings.Contains(out.String(), "The two slots are the same.") {
		t.Errorf("a against itself: %v\n%s", err, out.String())
	}
}

func TestSaveInputSlots(t *testing.T) {
	dir := t.TempDir()
	a, b := saveImage("Link"), saveImage("Aryll")
	two := writeSave(t, dir, "two.gci", &a, nil, &b)
	none := writeSave(t, dir, "none.gci")
	corrupt := writeSave(t, dir, "corrupt.gci", &a)
	raw, _ := os.ReadFile(corrupt)
	for _, blk := range []int{1, 2} {
		raw[savefile.HeaderSize+blk*savefile.BlockSize+8+0x10] ^= 0x40 // slot 0's packed image, both copies
	}
	if err := os.WriteFile(corrupt, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	junk := filepath.Join(dir, "junk.gci")
	os.WriteFile(junk, []byte("not a save"), 0o644)

	for _, c := range []struct {
		path    string
		slot    int
		wantN   int
		wantErr string
	}{
		{two, -1, 0, `holds 2 played files (slots 0 "Link", 2 "Aryll"): pick one with --slot`},
		{two, 2, 2, ""},
		{two, 1, 0, "slot 1 (file 2) is new game"},
		{none, -1, 0, "no slot holds a played file"},
		{corrupt, 0, 0, "slot 0 (file 1) is corrupted"},
		{corrupt, -1, 0, "no slot holds a played file"},
		{junk, -1, 0, "neither a GCI file"},
		{filepath.Join(dir, "gone.gci"), -1, 0, "gone.gci"},
	} {
		in, err := readSaveInput(c.path, c.slot)
		switch {
		case c.wantErr != "":
			if err == nil || !strings.Contains(err.Error(), c.wantErr) {
				t.Errorf("%s slot %d: %v, want %q", filepath.Base(c.path), c.slot, err, c.wantErr)
			}
		case err != nil || in.slot.N != c.wantN:
			t.Errorf("%s slot %d: %v", filepath.Base(c.path), c.slot, err)
		}
	}
}

// noTables fails the test if save-merge asks for tables.
func noTables(t *testing.T) func(string) (*coopStageTables, error) {
	return func(iso string) (*coopStageTables, error) {
		t.Errorf("tables loaded for %q without --iso", iso)
		return nil, os.ErrInvalid
	}
}

func TestSaveMerge(t *testing.T) {
	const metKorl, ev1001 = 0x0F80, 0x1001
	dir := t.TempDir()
	a, b := saveImage("Link"), saveImage("Tetra")
	setChest(t, &a, 3, 1)
	setChest(t, &b, 3, 2)
	swOff, swMask := setMemSwitch(t, &b, 3, 0x20)
	setSaveEvent(&b, metKorl)
	setSaveEvent(&b, ev1001)
	b[addr.OffGetItem+19], b[addr.OffItems+19] = 1, 0x2F // the hookshot
	b[addr.OffWalletSize] = 2
	b[addr.OffRupee+1] = 200
	tag := bind.Tag{Layout: bind.LayoutV1, Slot: 1, World: bind.WorldID{1, 2, 3, 4, 5, 6, 7, 8}, Nonce: bind.Nonce{9}}.Encode()
	copy(a[addr.OffReserve:], tag[:])
	pa := writeSave(t, dir, "a.gci", nil, &a)
	pb := writeSave(t, dir, "b.gci", nil, &b)
	before := map[string][]byte{}
	for _, p := range []string{pa, pb} {
		before[p], _ = os.ReadFile(p)
	}
	out := filepath.Join(dir, "merged.gci")

	var log bytes.Buffer
	if err := saveMerge(&log, saveArgs{files: []string{pa, pb, out}, slot: -1}, noTables(t)); err != nil {
		t.Fatalf("%v\n%s", err, log.String())
	}
	for p, b := range before {
		if now, _ := os.ReadFile(p); !bytes.Equal(now, b) {
			t.Errorf("%s changed", p)
		}
	}
	f, kind, err := savefile.Read(out)
	if err != nil || kind != savefile.KindGCI {
		t.Fatalf("read back: %v %s", err, kind)
	}
	if err := f.Verify(1); err != nil {
		t.Fatal(err)
	}
	s, _ := f.Slot(1)
	m := &s.Image
	c, _ := addr.TboxBit(1)
	c2, _ := addr.TboxBit(2)
	chests := m[addr.OffMemBit(3)+c.Off]
	switch {
	case chests&(c.Mask|c2.Mask) != c.Mask|c2.Mask:
		t.Errorf("chests 0x%02X", chests)
	case m[addr.OffWalletSize] != 2 || m[addr.OffRupee+1] != 0:
		t.Errorf("wallet %d rupees %d", m[addr.OffWalletSize], m[addr.OffRupee+1])
	case !eventBitSet(m, ev1001) || eventBitSet(m, metKorl):
		t.Errorf("0x1001 %v (syncs), MET_KORL %v (held)", eventBitSet(m, ev1001), eventBitSet(m, metKorl))
	case m[swOff]&swMask != 0:
		t.Error("a memory switch merged without the M1 tables")
	case m[addr.OffGetItem+19] != 1 || m[addr.OffItems+19] != 0xFF:
		t.Errorf("hookshot flag %d slot 0x%02X: the slot is a's without --iso", m[addr.OffGetItem+19], m[addr.OffItems+19])
	case [bind.TagSize]byte(m[addr.OffReserve:]) != tag:
		t.Error("a's tag not kept")
	case s.PlayerName() != "Link" || f.SaveCount() != 3:
		t.Errorf("name %q save count %d", s.PlayerName(), f.SaveCount())
	}
	for _, want := range []string{
		"+ saved t03 chest 2", "~ mWalletSize: 0x00 -> 0x02", "+ event 0x1001",
		"Held (b has them", "event 0x0F80: MET_KORL moves the resume stage",
		"WARNING: no --iso", "WARNING: b is past 0x0F80",
		// The tag names slot 1 and no name, and a was never saved with it.
		"WARNING: a carries a co-op tag of world 0102030405060708 that binds it to no world: the tag leaked from another file (V4: another name; V5: a never-saved New Game).",
		"Wrote " + out + `: slot 1 (file 2) "Link", saved 3 times; read back, checksums right.`,
	} {
		if !strings.Contains(log.String(), want) {
			t.Errorf("save-merge output lacks %q:\n%s", want, log.String())
		}
	}
	if strings.Contains(log.String(), "PER_PLAYER") || strings.Contains(log.String(), "mItems") || strings.Contains(log.String(), "is bound to") {
		t.Errorf("save-merge reported a byte it keeps, or a's leaked tag as a binding:\n%s", log.String())
	}
}

// bindSave gives img, the image of a file in slot n, a tag binding it to
// world w as bind.Bind would (V1-V5 pass), and the save's mDateIPL stamp.
func bindSave(img *world.Image, n int, w bind.WorldID) [bind.TagSize]byte {
	tag := bind.Tag{Layout: bind.LayoutV1, Slot: uint8(n), World: w, Nonce: bind.Nonce{byte(n + 1), 0x5A},
		NameHash: bind.NameHash(img[addr.OffPlayerName : addr.OffPlayerName+bind.PlayerNameSize])}.Encode()
	copy(img[addr.OffReserve:], tag[:])
	img[addr.OffDateIPL+7] = 0x2A
	return tag
}

// TestSaveMergeTag: a's tag stays only where no unjudged progress reaches
// its world through it (mergedTag), and the warning says what happens at
// the next join.
func TestSaveMergeTag(t *testing.T) {
	w, other := bind.WorldID{1, 2, 3, 4, 5, 6, 7, 8}, bind.WorldID{9, 9, 9, 9, 9, 9, 9, 9}
	for _, c := range []struct {
		name    string
		a, b    func(a, b *world.Image)
		aSlot   int
		kept    bool     // the merged slot holds a's tag bytes
		want    []string // in the log
		notWant []string
	}{
		{name: "a bound, b unbound with progress",
			a: func(a, b *world.Image) { bindSave(a, 0, w) }, b: func(a, b *world.Image) { setChest(t, b, 3, 2) },
			want: []string{"WARNING: a is bound to co-op world 0102030405060708 and b is not, so the merged file leaves a's tag out: a bound file's progress enters its world unjudged. At its next --coop join it is an unbound file the host judges like any other",
				"needs the host's --coop-merge if it holds progress the world lacks (b's)"}},
		{name: "a bound, b bound to another world", a: func(a, b *world.Image) { bindSave(a, 0, w) },
			b:    func(a, b *world.Image) { setChest(t, b, 3, 2); bindSave(b, 0, other) },
			want: []string{"and b is not, so the merged file leaves a's tag out"}},
		{name: "a and b bound to one world", kept: true, a: func(a, b *world.Image) { bindSave(a, 0, w) },
			b:    func(a, b *world.Image) { setChest(t, b, 3, 2); bindSave(b, 0, w) },
			want: []string{"WARNING: a and b are both bound to co-op world 0102030405060708, and the merged file keeps a's tag: at its next session the host takes b's progress into that world as this file's offline progress, with no bind judgement and no --coop-merge"}},
		{name: "a bound, b adds nothing", kept: true, a: func(a, b *world.Image) { setChest(t, a, 3, 2); bindSave(a, 0, w) },
			b: func(a, b *world.Image) { setChest(t, b, 3, 2) }, notWant: []string{"co-op tag", "co-op world"}},
		{name: "a's tag leaked into another slot", kept: true, aSlot: 1,
			a: func(a, b *world.Image) { bindSave(a, 0, w) }, b: func(a, b *world.Image) { setChest(t, b, 3, 2) },
			want:    []string{"WARNING: a carries a co-op tag of world 0102030405060708 that binds it to no world: the tag leaked from another file (V2: carried into another slot). The merged file keeps those bytes, and a co-op session offers to scrub them"},
			notWant: []string{"is bound to co-op world"}},
		{name: "a unbound", kept: true, b: func(a, b *world.Image) { setChest(t, b, 3, 2); bindSave(b, 0, w) },
			notWant: []string{"co-op tag", "co-op world"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			a, b := saveImage("Link"), saveImage("Tetra")
			if c.a != nil {
				c.a(&a, &b)
			}
			c.b(&a, &b)
			slots := make([]*world.Image, c.aSlot+1)
			slots[c.aSlot] = &a
			pa := writeSave(t, dir, "a.gci", slots...)
			pb := writeSave(t, dir, "b.gci", &b)
			out := filepath.Join(dir, "merged.gci")
			var log bytes.Buffer
			if err := saveMerge(&log, saveArgs{files: []string{pa, pb, out}, slot: -1}, noTables(t)); err != nil {
				t.Fatalf("%v\n%s", err, log.String())
			}
			for _, want := range c.want {
				if !strings.Contains(log.String(), want) {
					t.Errorf("log lacks %q:\n%s", want, log.String())
				}
			}
			for _, not := range append(c.notWant, "which needs the host's --coop-merge") {
				if strings.Contains(log.String(), not) {
					t.Errorf("log says %q:\n%s", not, log.String())
				}
			}
			f, _, err := savefile.Read(out)
			if err != nil {
				t.Fatal(err)
			}
			s, _ := f.Slot(c.aSlot)
			got := [bind.TagSize]byte(s.Image[addr.OffReserve:])
			switch {
			case c.kept && got != [bind.TagSize]byte(a[addr.OffReserve:]):
				t.Error("a's tag bytes not kept")
			case !c.kept && got != [bind.TagSize]byte{}:
				t.Errorf("tag % X, want it cleared", got)
			case !c.kept:
				if v := bind.Classify(slotFile(c.aSlot, &s.Image), bind.Expect{World: w}); v.State != bind.Unbound {
					t.Errorf("the merged file is %s, want unbound", v.State)
				}
			}
		})
	}
}

func TestSaveMergeRefuses(t *testing.T) {
	dir := t.TempDir()
	a, b := saveImage("Link"), saveImage("Tetra")
	setChest(t, &b, 3, 2)
	pa := writeSave(t, dir, "a.gci", &a)
	pb := writeSave(t, dir, "b.gci", &b)
	exists := filepath.Join(dir, "exists.gci")
	os.WriteFile(exists, []byte("keep me"), 0o644)
	second := b
	second[addr.OffClearCount] = 1
	pSecond := writeSave(t, dir, "second.gci", &second)
	badEntry := filepath.Join(dir, "badentry.gci")
	raw, _ := os.ReadFile(pa)
	binary.BigEndian.PutUint32(raw[0x2C:], 0x40) // iconAddr: CheckStat wants 0 (m_Do_MemCardRWmng.cpp:281)
	os.WriteFile(badEntry, raw, 0o644)
	sub := filepath.Join(dir, "sub")
	os.Mkdir(sub, 0o755)
	sep := string(filepath.Separator)
	// b through a link to its directory: only os.SameFile sees it is b.
	link := filepath.Join(t.TempDir(), "link")
	viaLink := ""
	if err := os.Symlink(dir, link); err == nil {
		viaLink = filepath.Join(link, "b.gci")
	}

	for _, c := range []struct {
		name    string
		files   []string
		wantErr string
		made    string // a file that must not exist afterwards
	}{
		{"out exists", []string{pa, pb, exists}, "exists: save-merge writes only a new file", ""},
		{"out is a", []string{pa, pb, pa}, "is input a", ""},
		// Not filepath.Join, which would clean it to pb.
		{"out is b, spelt otherwise", []string{pa, pb, sub + sep + ".." + sep + "b.gci"}, "is input b", ""},
		{"out is b, through a link", []string{pa, pb, viaLink}, "is input b", ""},
		{"second quest", []string{pa, pSecond, filepath.Join(dir, "o1.gci")}, "second quest", filepath.Join(dir, "o1.gci")},
		{"a's entry", []string{badEntry, pb, filepath.Join(dir, "o2.gci")}, "icon address 0x40", filepath.Join(dir, "o2.gci")},
		{"no directory", []string{pa, pb, filepath.Join(dir, "none", "o.gci")}, "o.gci", ""},
		{"tables fail", []string{pa, pb, filepath.Join(dir, "o3.gci")}, "no such disc", filepath.Join(dir, "o3.gci")},
	} {
		t.Run(c.name, func(t *testing.T) {
			if c.files[2] == "" {
				t.Skip("no symbolic links here")
			}
			var log bytes.Buffer
			args := saveArgs{files: c.files, slot: -1}
			tables := noTables(t)
			if c.name == "tables fail" {
				args.iso = "ww.iso"
				tables = func(string) (*coopStageTables, error) { return nil, errors.New("ww.iso: no such disc") }
			}
			err := saveMerge(&log, args, tables)
			if err == nil || !strings.Contains(err.Error()+log.String(), c.wantErr) {
				t.Errorf("%v, want %q\n%s", err, c.wantErr, log.String())
			}
			if strings.Contains(log.String(), "Wrote") {
				t.Errorf("reported a write:\n%s", log.String())
			}
			if c.made != "" {
				if _, err := os.Stat(c.made); err == nil {
					t.Errorf("%s was written", c.made)
				}
			}
		})
	}
	if b, _ := os.ReadFile(exists); string(b) != "keep me" {
		t.Errorf("an existing out was overwritten: %q", b)
	}
}

// TestSaveMergeISO merges with the vanilla fixture's tables: the latching
// crystal switch merges, and the derive gives a's file b's key, heart
// piece and hookshot.
func TestSaveMergeISO(t *testing.T) {
	useTablesDir(t, t.TempDir())
	iso := writeImage(t, "ww.iso", tablestest.ISO(tablestest.Vanilla()))
	dir := t.TempDir()
	a, b := saveImage("Link"), saveImage("Tetra")
	setChest(t, &b, 3, 1)                                // a Dragon Roost Cavern key chest (tablestest.Dungeon)
	b[addr.OffMemBit(3)+addr.MemBitOffKeyNum] = 1        // its key, unspent
	setChest(t, &b, 0, 0)                                // a sea heart-piece chest (tablestest.Vanilla)
	b[addr.OffMaxLife+1] = 13                            //
	swOff, swMask := setMemSwitch(t, &b, 3, 0x20)        // the latching crystal
	b[addr.OffGetItem+19], b[addr.OffItems+19] = 1, 0x2F // the hookshot
	b[addr.OffGetItem+6], b[addr.OffMaxMagic] = 1, 16    // the Deku Leaf
	pa := writeSave(t, dir, "a.gci", &a)
	pb := writeSave(t, dir, "b.gci", &b)
	out := filepath.Join(dir, "merged.gci")

	var log bytes.Buffer
	if err := saveMerge(&log, saveArgs{files: []string{pa, pb, out}, slot: -1, iso: iso}, loadCoopStageTables); err != nil {
		t.Fatalf("%v\n%s", err, log.String())
	}
	f, _, err := savefile.Read(out)
	if err != nil {
		t.Fatal(err)
	}
	s, _ := f.Slot(0)
	m := &s.Image
	switch {
	case m[swOff]&swMask == 0:
		t.Error("the latching crystal switch did not merge")
	case m[addr.OffMemBit(3)+addr.MemBitOffKeyNum] != 1:
		t.Errorf("DRC keys %d, want 1", m[addr.OffMemBit(3)+addr.MemBitOffKeyNum])
	case m[addr.OffMaxLife+1] != 13:
		t.Errorf("max life %d, want 13", m[addr.OffMaxLife+1])
	case m[addr.OffItems+19] != 0x2F || m[addr.OffItems+6] != 0x34 || m[addr.OffMaxMagic] != 16:
		t.Errorf("hookshot 0x%02X leaf 0x%02X magic %d", m[addr.OffItems+19], m[addr.OffItems+6], m[addr.OffMaxMagic])
	}
	for _, want := range []string{"M1 tables: " + iso, "Derived:", "small keys, table 3", "max life: 12 -> 13", "DERIVED (each file computes them from the flags)", "~ mItems[19]: 0xFF -> 0x2F", "max magic: 0 -> 16, from b's max magic"} {
		if !strings.Contains(log.String(), want) {
			t.Errorf("save-merge output lacks %q:\n%s", want, log.String())
		}
	}
	if strings.Contains(log.String(), "WARNING: no --iso") {
		t.Errorf("warned about --iso:\n%s", log.String())
	}
}
