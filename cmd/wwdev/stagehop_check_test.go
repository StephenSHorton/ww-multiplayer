package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/StephenSHorton/ww-multiplayer/wwapi/stage/stagetest"
)

// hopStart is a PLYR entry answering to point (the ship byte set, as on
// the disc) whose parameters name room.
func hopStart(point, room int) stagetest.Actor {
	return stagetest.Actor{Name: "Link", Params: 0xFFFF0000 | uint32(room), Angle: [3]int16{0, 0, int16(0xFF00 | point)}, SetID: 0xFFFF}
}

// hopDisc is a synthetic disc shaped like the stages HOPS.md read:
//   - "Drc": room 0 has points 0 and 5, room 2 has 1 and 5, room 4 only 7,
//     room 5 no PLYR chunk, room 9 an archive without a room.dzr, room 10
//     an empty PLYR chunk;
//   - "Boss": one room, numbered 9, points 0 and 1 (M_Dra09);
//   - "Lvl": stage.dzs has its own starts, point 3 in room 1 (then again
//     in room 0, never reached) and point 4 in room 2, which Lvl lacks
//     (Cave08), so the rooms' starts are never read.
func hopDisc() *stagetest.Disc {
	stag := stagetest.StagChunk(stagetest.StagInfo(3, 2, true))
	dzs := stagetest.DZx(stag)
	room := func(no int, chunks ...stagetest.Chunk) stagetest.Room {
		return stagetest.Room{No: no, DZR: stagetest.DZx(chunks...)}
	}
	plyr := func(room int, points ...int) stagetest.Chunk {
		var as []stagetest.Actor
		for _, p := range points {
			as = append(as, hopStart(p, room))
		}
		return stagetest.Actors("PLYR", as...)
	}
	var d stagetest.Disc
	d.AddStage(stagetest.Stage{Name: "Drc", DZS: dzs, Yaz0: true, Rooms: []stagetest.Room{
		room(0, stagetest.Actors("ACTR", stagetest.Actor{Name: "Bk"}), plyr(0, 0, 5)),
		room(2, plyr(2, 5, 1)),
		room(4, plyr(4, 7)),
		room(5, stagetest.Actors("ACTR", stagetest.Actor{Name: "Bk"})),
		{No: 9},
		room(10, stagetest.Chunk{Tag: "PLYR"}),
	}})
	d.AddStage(stagetest.Stage{Name: "Boss", DZS: dzs, Rooms: []stagetest.Room{room(9, plyr(9, 0, 1))}})
	d.AddStage(stagetest.Stage{Name: "Lvl", DZS: stagetest.DZx(stag, stagetest.Actors("PLYR", hopStart(3, 1), hopStart(4, 2), hopStart(3, 0))), Rooms: []stagetest.Room{
		room(0, plyr(0, 0)),
		room(1),
	}})
	d.AddStage(stagetest.Stage{Name: "NoStag", DZS: stagetest.DZx(), Rooms: []stagetest.Room{room(0, plyr(0, 0))}})
	return &d
}

func TestCheckStageHop(t *testing.T) {
	src := hopDisc()
	for _, c := range []struct {
		args []string
		room int8   // the room after the check
		want string // "" = accepted, else in the refusal
		out  string // in what it says
	}{
		{[]string{"Drc", "0", "0"}, 0, "", ""},
		{[]string{"drc", "5", "2"}, 2, "", ""}, // names match case-insensitively, as the game opens them
		{[]string{"Drc", "7", "4"}, 4, "", ""},
		{[]string{"Drc", "0", "4"}, 4, "room 4 of Drc has no start point 0", ""},    // HOPS.md's hang
		{[]string{"Drc", "7", "0"}, 0, "room 0 of Drc has no start point 7", ""},    // only in another room
		{[]string{"Drc", "0", "5"}, 5, "room 5 of Drc has no player starts", ""},    // no PLYR
		{[]string{"Drc", "0", "9"}, 9, "Room9.arc has no room.dzr", ""},             // nothing placed
		{[]string{"Drc", "0", "10"}, 10, "room 10 of Drc has no start point 0", ""}, // an empty PLYR asserts too
		{[]string{"Drc", "0", "3"}, 3, "Drc has no room 3", ""},                     // no archive
		{[]string{"Nope", "0", "0"}, 0, `the disc has no stage "Nope"`, ""},         // no stage
		{[]string{"NoStag", "0", "0"}, 0, "no STAG chunk", ""},                      // the play scene can't load it
		{[]string{"Drc", "7"}, 4, "", "using room 4, the first of Drc with start point 7"},
		{[]string{"Drc", "5"}, 0, "", "using room 0"},
		{[]string{"Drc", "9"}, 0, "no room of Drc has start point 9", ""},
		{[]string{"Boss"}, 9, "", "using room 9"}, // M_Dra09: its one room is 9
		{[]string{"Boss", "1"}, 9, "", "using room 9"},
		{[]string{"Boss", "0", "0"}, 0, "Boss has no room 0", ""},
		// stage.dzs's starts decide whatever the room's say, and the first
		// entry's room is the one loaded, whatever room is given.
		{[]string{"Lvl", "3", "1"}, 1, "", ""},
		{[]string{"Lvl", "3", "0"}, 1, "", "Room 0 ignored: Lvl's stage-level start 3 loads room 1"},
		{[]string{"Lvl", "0", "0"}, 0, "stage.dzs's player starts, which have no point 0", ""},
		{[]string{"Lvl", "3"}, 1, "", "the room of Lvl's stage-level start 3"},
		{[]string{"Lvl", "3", "40"}, 1, "", "Room 40 ignored"},                                        // kaze 8 40
		{[]string{"Lvl", "4", "1"}, 1, "Lvl's stage-level start 4 loads room 2, which Lvl lacks", ""}, // Cave08 0 1
		{[]string{"Lvl", "4"}, 0, "loads room 2, which Lvl lacks", ""},
	} {
		h, err := parseStageHop(c.args)
		if err != nil {
			t.Fatalf("%q: %v", c.args, err)
		}
		var out bytes.Buffer
		err = checkStageHop(&out, src, &h, len(c.args) > 2)
		switch {
		case c.want == "" && err != nil:
			t.Errorf("%q refused: %v", c.args, err)
		case c.want != "" && (err == nil || !strings.Contains(err.Error(), c.want)):
			t.Errorf("%q: %v, want %q", c.args, err, c.want)
		case h.Room != c.room:
			t.Errorf("%q: room %d, want %d", c.args, h.Room, c.room)
		case !strings.Contains(out.String(), c.out):
			t.Errorf("%q said %q, want %q", c.args, out.String(), c.out)
		}
	}
}

// TestStageHopRefusalLists: a refusal lists the starts that would work.
func TestStageHopRefusalLists(t *testing.T) {
	src := hopDisc()
	h := stageHop{Name: "Drc", Point: 0, Room: 4, Layer: -1}
	err := checkStageHop(&bytes.Buffer{}, src, &h, true)
	if err == nil {
		t.Fatal("accepted")
	}
	for _, want := range []string{
		"stage-hop Drc <point> <room>",
		"room 0: 0 0, 5 0",
		"room 2: 1 2, 5 2",
		"room 4: 7 4",
		"room 5: none",
		"room 9: none (no room.dzr)",
		"room 10: none",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal lacks %q:\n%v", want, err)
		}
	}
	h = stageHop{Name: "Lvl", Point: 0, Room: 0, Layer: -1}
	err = checkStageHop(&bytes.Buffer{}, src, &h, true)
	if err == nil || !strings.HasSuffix(err.Error(), "any room given is ignored): 3 -> room 1") || strings.Contains(err.Error(), "any room of") {
		t.Errorf("stage-level refusal (point 4's room 2 is missing, so only 3 -> room 1): %v", err)
	}
}

// TestVerifyStageHop: no disc only warns (and keeps room 0), a disc that
// isn't one is an error, and a real image is read and checked.
func TestVerifyStageHop(t *testing.T) {
	dir := t.TempDir()
	h := stageHop{Name: "Boss", Layer: -1}
	var out bytes.Buffer
	if err := verifyStageHop(&out, filepath.Join(dir, "missing.iso"), &h, false); err != nil || h.Room != 0 ||
		!strings.Contains(out.String(), "Could not verify") || !strings.Contains(out.String(), "using room 0") {
		t.Errorf("no disc: %v, room %d, %q", err, h.Room, out.String())
	}
	out.Reset()
	if err := verifyStageHop(&out, "", &h, true); err != nil || !strings.Contains(out.String(), "no disc is set") {
		t.Errorf("no ISO set: %v, %q", err, out.String())
	}

	junk := filepath.Join(dir, "junk.iso")
	if err := os.WriteFile(junk, []byte("not a disc"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := verifyStageHop(&bytes.Buffer{}, junk, &h, true); err == nil || !strings.Contains(err.Error(), "--no-check") {
		t.Errorf("unreadable disc: %v", err)
	}

	iso := filepath.Join(dir, "ww.iso")
	if err := os.WriteFile(iso, hopDisc().ISO(stagetest.MainDOL()), 0o644); err != nil {
		t.Fatal(err)
	}
	h = stageHop{Name: "Boss", Layer: -1}
	if err := verifyStageHop(&bytes.Buffer{}, iso, &h, false); err != nil || h.Room != 9 {
		t.Errorf("Boss from the image: %v, room %d", err, h.Room)
	}
	h = stageHop{Name: "Drc", Point: 0, Room: 4, Layer: -1}
	if err := verifyStageHop(&bytes.Buffer{}, iso, &h, true); err == nil || !strings.Contains(err.Error(), "room 4: 7 4") || !strings.Contains(err.Error(), iso) {
		t.Errorf("Drc 0 4 from the image: %v", err)
	}
}
