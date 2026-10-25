package network

import (
	"bytes"
	"os"
	"strings"
	"time"
)

// Relay scoping (ww-online review idea 17; ww-multiplayer-handoff/archive/
// ww-online-review/DESIGN-big-items.md §4 "Relay scoping"). Before it, the
// server relayed every pose to every player and every enemy STATE to every
// enemy client. A pose is about 2 KB at broadcast-pose's 60 Hz, so with N
// players the server's pose egress grows as N*(N-1) whether or not anyone
// can see anyone; past two remotes that is the traffic that has to go.
// datstarkey/ww-online keeps each player's stage and room on its server and
// relays an event only to the players in the place it happened
// (WWOnline.Server/Hubs/PlayerEventRelay.cs:35 LocationTtl, :115-150 Check
// and Recipients); this is the same idea fitted to our routing:
//
//   - The place. A position may carry a place trailer: the sender's stage,
//     room and whether its game is in play (broadcast-pose reads it with
//     the enemy client's scope rule, feed.ReadPlace). The server keeps each
//     player connection's last place and relays it with the position.
//   - Positions still go to everyone. They are small (30 B with the place),
//     and the sea chart markers, the TUI minimap, the enemy host's roster
//     and the receiver's own stage gate all read them; the place riding
//     them is what tells a receiver why a remote's poses stopped.
//   - Poses go only to the players in the sender's stage (poseInScope).
//     The stage, not the room: the sea is one stage whose 49 squares are
//     rooms, a dungeon's rooms share one coordinate space, and the stage
//     gate the receiver applies (feed's gateFor) is a stage check.
//   - Nor do poses go to a connection that sends poses itself. Every human
//     runs two player connections under one name (pickColorLocked):
//     broadcast-pose sends and never reads a remote, puppet-sync draws and
//     never sends a pose. Before scoping, half of all pose egress went to
//     the broadcasters, which dropped it.
//   - Enemy STATE (enemy.go) is thinned, not withheld, for an enemy client
//     whose PSTATE puts it in another stage or room than the STATE's.
//   - Co-op is untouched: its world events are global by design.
//
// Everything fails open. A player whose place is unknown (a build from
// before the trailer, no position for PlaceStale, a game between stages,
// or a name two live broadcasters share) gets every pose, as before.
// WW_RELAY_SCOPE=off on the server turns the scoping off.
//
// Wire: no protocol version bump. A server from before the trailer reads
// the first 18 bytes of a position and relays 19, so a receiver sees no
// place and the old stage gate applies; a client from before it reads the
// first 19 bytes of a relay and ignores the rest, and sends no place, so
// the server relays it everything. The same rule as the color trailer of
// MsgPlayerList (docs/09-player-colors.md §5.1).
//
//	client -> server:  [position:18][tag:1 = 0xA7][flags:1][room:1][stage:8]
//	server -> client:  [playerID:1][position:18][tag][flags][room][stage:8]
//
// flags bit 0 is PlaceInPlay. stage is mCurStage.mName (d_stage.h:949,
// char[8]), NUL-padded; room is mStayNo (d_stage.h:930), -1 at a stage's
// start. Bytes after the stage are ignored, so a later build can append.

// placeTag opens a position's place trailer.
const placeTag = 0xA7

// Place trailer layout.
const (
	// PlaceStageLen is the stage field: char[8], as
	// dStage_startStage_c::mName.
	PlaceStageLen = 8
	// PlaceTrailerLen is the trailer after a position: tag, flags, room,
	// stage.
	PlaceTrailerLen = 3 + PlaceStageLen
	// PlaceInPlay is flags bit 0: the sender's game is in play (a stage
	// with Link loaded, no stage change pending, not the title or file
	// select).
	PlaceInPlay byte = 1 << 0
)

// PlaceStale is how long the server, and a receiver, believe a place: a
// broadcaster sends one with every position (16 ms), so an older one
// belongs to a game whose Link is gone (a stage load, the title screen)
// or a connection that went quiet. It is feed.PartnerStale's 3 s, and
// ww-online's LocationTtl.
const PlaceStale = 3 * time.Second

// Place is where a player's game is: its stage, its room and whether it
// is in play.
type Place struct {
	Stage  string // mCurStage.mName, at most PlaceStageLen bytes
	Room   int8   // mStayNo
	InPlay bool
}

// SameStage reports whether two places name the same stage.
func (p Place) SameStage(o Place) bool { return p.Stage == o.Stage }

// SameRoom reports whether two places name the same stage and room.
func (p Place) SameRoom(o Place) bool { return p.Stage == o.Stage && p.Room == o.Room }

// AppendPlace appends p's trailer to b. A stage name over PlaceStageLen
// bytes is cut to it (the game's own field holds no more).
func AppendPlace(b []byte, p Place) []byte {
	var flags byte
	if p.InPlay {
		flags |= PlaceInPlay
	}
	b = append(b, placeTag, flags, byte(p.Room))
	var stage [PlaceStageLen]byte
	copy(stage[:], p.Stage)
	return append(b, stage[:]...)
}

// ParsePlace reads a place trailer from tail, the bytes after a position.
// ok is false when there is none or it is malformed: a wrong tag, too
// short, or a stage name that is not NUL-padded printable ASCII (the
// game's stage names are short ASCII words: "sea", "M_NewD2", "Hyroom").
func ParsePlace(tail []byte) (p Place, ok bool) {
	if len(tail) < PlaceTrailerLen || tail[0] != placeTag {
		return Place{}, false
	}
	stage := tail[3 : 3+PlaceStageLen]
	n := bytes.IndexByte(stage, 0)
	if n < 0 {
		n = PlaceStageLen
	}
	for i, c := range stage {
		if i < n && (c < 0x20 || c > 0x7E) || i >= n && c != 0 {
			return Place{}, false
		}
	}
	return Place{Stage: string(stage[:n]), Room: int8(tail[2]), InPlay: tail[1]&PlaceInPlay != 0}, true
}

// PositionPlaceMessage is PositionMessage with the sender's place
// appended: [playerID:1][position:18][place trailer].
func PositionPlaceMessage(playerID byte, pos *PlayerPosition, p Place) []byte {
	return AppendPlace(PositionMessage(playerID, pos), p)
}

// ParsePositionPlace extracts a relayed position's place; ok is false for
// a relay without one (a sender or server from before the trailer).
func ParsePositionPlace(data []byte) (Place, bool) {
	if len(data) < 19 {
		return Place{}, false
	}
	return ParsePlace(data[19:])
}

// relayScopeFromEnv reads WW_RELAY_SCOPE: off (also 0, false, no, none)
// turns relay scoping off on this server; anything else leaves it on.
func relayScopeFromEnv() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("WW_RELAY_SCOPE"))) {
	case "off", "0", "false", "no", "none":
		return false
	}
	return true
}

// ScopeStats counts what relay scoping did on one server: the pose relays
// it wrote and the ones it held back, the enemy STATEs it relayed whole
// and the ones it thinned to their header, each with its bytes (payload
// bytes plus the 3-byte frame header). A test reads them to measure the
// saving; Server.ScopeStats returns a copy.
type ScopeStats struct {
	PosesSent, PosesScoped          int
	PoseBytesSent, PoseBytesScoped  int64
	StatesFull, StatesThinned       int
	StateBytesSent, StateBytesSaved int64
	PlacesRejected                  int
}

// placeState is a player connection's last place (guarded by s.mu).
type placeState struct {
	place Place
	at    time.Time // zero: none yet
}

// fresh reports whether the place is known and believed at now.
func (ps *placeState) fresh(now time.Time) bool {
	return !ps.at.IsZero() && now.Sub(ps.at) <= PlaceStale && ps.place.Stage != ""
}

// placeOfLocked is where player p's game is at now, for scoping what p
// receives. A connection that sends positions (broadcast-pose) has a place
// of its own. One that does not (puppet-sync, the receiving twin of every
// human: pickColorLocked) takes the place of the one live broadcaster
// under its name (lobbyKey, as enemyPlayerIDLocked pairs enemy
// connections). Two live broadcasters under one name (CLI sessions without
// a lobby do not force unique names) make the name ambiguous: no place,
// so both humans get every pose. ok is false for an unknown place. The
// caller holds s.mu.
func (s *Server) placeOfLocked(p *Player, now time.Time) (Place, bool) {
	if p.where.fresh(now) {
		return p.where.place, true
	}
	key := lobbyKey(p.Name)
	var found *Player
	for _, q := range s.players {
		if q == p || !q.where.fresh(now) || lobbyKey(q.Name) != key {
			continue
		}
		if found != nil {
			return Place{}, false // two broadcasters share the name
		}
		found = q
	}
	if found == nil {
		return Place{}, false
	}
	return found.where.place, true
}

// poseInScope reports whether a pose from a sender at from (ok fromOK)
// goes to a receiver at to (ok toOK): unless both places are known, both
// games are in play and the stages differ. A game between stages or on
// the title screen keeps getting every pose, so nothing is missing when
// it arrives.
func poseInScope(from Place, fromOK bool, to Place, toOK bool) bool {
	if !fromOK || !toOK || !from.InPlay || !to.InPlay {
		return true
	}
	return from.SameStage(to)
}
