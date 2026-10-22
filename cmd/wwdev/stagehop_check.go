package main

// stage-hop's start check: a hop to a start point the stage's player
// starts lack halts the game, so stage-hop reads the stage from the disc
// first and refuses one.
//
// Link is created from the first PLYR chunk the play scene decodes:
// stage.dzs's if it has one, else the start room's room.dzr
// (stage.DZx.Players; dStage_playerInit, d_stage.cpp:1428-1475). The
// start point picks the entry whose (u8)angle.z equals it, and none
// matching is a JUT_ASSERT (d_stage.cpp:1455-1463), which retail keeps
// (JUTAssert.h:9): the game OSPanics inside player creation with the
// stage-change fade still up, a black screen. Point numbers repeat across
// rooms, so the (point, room) pair has to match. The matched entry's room
// becomes the start room (d_stage.cpp:1479-1480); for stage.dzs's starts
// that happens before any room loads, so the game loads that room
// (dStage_Create, :2231-2232), not the one the hop asked for.

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"slices"
	"strconv"
	"strings"

	"github.com/StephenSHorton/ww-multiplayer/internal/inject"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/disc"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/emu/launch"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/stage"
)

// verifyStageHop checks h against the disc at iso (checkStageHop). With
// no disc there it only says so: the hop goes out unchecked. A disc that
// is there but can't be read is an error.
func verifyStageHop(w io.Writer, iso string, h *stageHop, roomGiven bool) error {
	if iso == "" {
		fmt.Fprintf(w, "Could not verify the start: no disc is set (%s). A start the stage lacks hangs the game.\n", launch.EnvISO)
		defaultRoomUnchecked(w, h, roomGiven)
		return nil
	}
	if _, err := os.Stat(iso); errors.Is(err, fs.ErrNotExist) {
		fmt.Fprintf(w, "Could not verify the start: no disc at %s (set %s). A start the stage lacks hangs the game.\n", iso, launch.EnvISO)
		defaultRoomUnchecked(w, h, roomGiven)
		return nil
	}
	d, err := inject.OpenDisc(iso)
	if err != nil {
		return fmt.Errorf("reading %s to check the start: %w (--no-check skips the check)", iso, err)
	}
	defer d.Close()
	if err := checkStageHop(w, d, h, roomGiven); err != nil {
		return fmt.Errorf("%w\n(checked against %s; --no-check skips the check)", err, iso)
	}
	return nil
}

// defaultRoomUnchecked notes the room an unchecked hop without one uses.
func defaultRoomUnchecked(w io.Writer, h *stageHop, roomGiven bool) {
	if !roomGiven {
		fmt.Fprintf(w, "No room given and none checked: using room %d. A stage without that room hangs (M_Dra09's only room is 9).\n", h.Room)
	}
}

// checkStageHop refuses h unless the list of player starts the game will
// search for h's stage and room has h.Point, naming the starts it has.
// The stage, and the room the game will load, must be on src. With
// stage.dzs's starts that room is the matched start's own (its
// parameters' low 6 bits, Actor.StartRoom; dStage_playerInit makes it the
// start room, d_stage.cpp:1479-1480, and dStage_Create loads it,
// :2231-2232), whatever h.Room says: h.Room is set to it, and a different
// room given is reported on w as ignored. Otherwise the room is h.Room
// and needs a room.dzr; when roomGiven is false it is picked, the
// lowest-numbered room with the point, and said on w.
func checkStageHop(w io.Writer, src disc.FileSource, h *stageHop, roomGiven bool) error {
	st, err := stage.LoadStage(src, h.Name)
	if errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("refused: the disc has no stage %q", h.Name)
	} else if err != nil {
		return fmt.Errorf("refused: %w", err)
	}
	point := int(h.Point)

	if st.DZS.HasPlayers() {
		// Link comes from stage.dzs whatever the room: its room.dzr's
		// PLYR returns at once (d_stage.cpp:1432).
		// The first entry with the point is the one taken (:1455-1460),
		// and its room is the one loaded (:1479-1480, :2231-2232).
		i := slices.IndexFunc(st.DZS.Players, func(a stage.Actor) bool { return a.StartPoint() == point })
		if i < 0 {
			return fmt.Errorf("refused: %s starts Link from stage.dzs's player starts, which have no point %d.\n%s",
				st.Name, point, startList(st))
		}
		room := st.DZS.Players[i].StartRoom()
		if _, ok := st.Room(room); !ok {
			return fmt.Errorf("refused: %s's stage-level start %d loads room %d, which %s lacks.\n%s",
				st.Name, point, room, st.Name, startList(st))
		}
		switch {
		case !roomGiven:
			fmt.Fprintf(w, "No room given: using room %d, the room of %s's stage-level start %d.\n", room, st.Name, point)
		case int(h.Room) != room:
			fmt.Fprintf(w, "Room %d ignored: %s's stage-level start %d loads room %d; using room %d.\n", h.Room, st.Name, point, room, room)
		}
		h.Room = int8(room)
		return nil
	}

	if !roomGiven {
		for _, r := range st.Rooms {
			if r.DZR != nil && slices.ContainsFunc(r.DZR.Players, func(a stage.Actor) bool { return a.StartPoint() == point }) {
				h.Room = int8(r.No)
				fmt.Fprintf(w, "No room given: using room %d, the first of %s with start point %d.\n", r.No, st.Name, point)
				return nil
			}
		}
		return fmt.Errorf("refused: no room of %s has start point %d.\n%s", st.Name, point, startList(st))
	}
	r, ok := st.Room(int(h.Room))
	switch {
	case !ok:
		return fmt.Errorf("refused: %s has no room %d.\n%s", st.Name, h.Room, startList(st))
	case r.DZR == nil:
		return fmt.Errorf("refused: %s has no room.dzr, so room %d has no player starts.\n%s", r.Path, h.Room, startList(st))
	case !r.DZR.HasPlayers():
		return fmt.Errorf("refused: room %d of %s has no player starts (no PLYR chunk).\n%s", h.Room, st.Name, startList(st))
	case !slices.ContainsFunc(r.DZR.Players, func(a stage.Actor) bool { return a.StartPoint() == point }):
		return fmt.Errorf("refused: room %d of %s has no start point %d.\n%s", h.Room, st.Name, point, startList(st))
	}
	return nil
}

// startList lists st's valid hops, what the game would find: with
// stage.dzs's starts, `<point> -> room N` for each point whose room the
// stage has (a room given is ignored); else `<point> <room>` pairs, a
// line per room.
func startList(st *stage.Stage) string {
	var b strings.Builder
	points := func(as []stage.Actor) []int {
		var ps []int
		for _, a := range as {
			ps = append(ps, a.StartPoint())
		}
		slices.Sort(ps)
		return slices.Compact(ps)
	}
	hops := func(ps []int, room int) string {
		var s []string
		for _, p := range ps {
			s = append(s, strconv.Itoa(p)+" "+strconv.Itoa(room))
		}
		return strings.Join(s, ", ")
	}
	if st.DZS.HasPlayers() {
		if len(st.DZS.Players) == 0 {
			return fmt.Sprintf("%s's stage.dzs has an empty PLYR chunk: no start point works.", st.Name)
		}
		// The first entry with a point decides its room, as the game's
		// search does.
		rooms := map[int]int{}
		for _, a := range st.DZS.Players {
			if _, ok := rooms[a.StartPoint()]; !ok {
				rooms[a.StartPoint()] = a.StartRoom()
			}
		}
		var hs []string
		for _, p := range points(st.DZS.Players) {
			if _, ok := st.Room(rooms[p]); ok {
				hs = append(hs, fmt.Sprintf("%d -> room %d", p, rooms[p]))
			}
		}
		if len(hs) == 0 {
			return fmt.Sprintf("Every start in %s's stage.dzs loads a room the stage lacks: no start point works.", st.Name)
		}
		fmt.Fprintf(&b, "Valid `stage-hop %s <point>` starts (stage.dzs's; each loads its own room, any room given is ignored): %s",
			st.Name, strings.Join(hs, ", "))
		return b.String()
	}
	fmt.Fprintf(&b, "Valid `stage-hop %s <point> <room>` starts:", st.Name)
	n := 0
	for _, r := range st.Rooms {
		switch {
		case r.DZR == nil:
			fmt.Fprintf(&b, "\n  room %d: none (no room.dzr)", r.No)
		case len(r.DZR.Players) == 0:
			fmt.Fprintf(&b, "\n  room %d: none", r.No)
		default:
			fmt.Fprintf(&b, "\n  room %d: %s", r.No, hops(points(r.DZR.Players), r.No))
			n++
		}
	}
	if n == 0 {
		fmt.Fprintf(&b, "\n  (no room of %s has a player start)", st.Name)
	}
	return b.String()
}
