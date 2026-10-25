package feed

import (
	"bytes"

	"github.com/StephenSHorton/ww-multiplayer/mods/multiplayer/net"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mem"
)

// ReadPlace reads this game's place for the position's place trailer
// (network place.go: the server scopes this player's pose relays by it,
// and receivers gate this player's mini-Link by it). It is the enemy
// client's scope rule (mods/enemysync ReadScope, which this package
// cannot import: enemysync imports feed): the stage is mCurStage.mName
// (addr.CurStageName), the room mStayNo (addr.StayNo), and the game is
// in play with Link loaded, no stage change pending (mNextStage.mEnable,
// addr.NextStageEnable), no scene load requested (addr.OverlapReq) and a
// stage that is not the title or file select. TestReadPlaceMatchesScope
// (enemysync) holds the two together. ok is false when the reads fail.
func ReadPlace(m mem.Reader) (p network.Place, ok bool) {
	link, err := mem.ReadU32(m, addr.PlayerPtr)
	if err != nil {
		return p, false
	}
	ovlp, err := mem.ReadU32(m, addr.OverlapReq)
	if err != nil {
		return p, false
	}
	n := int(addr.NextStageEnable-addr.CurStageName) + 1
	b, err := m.Read(addr.CurStageName, n)
	if err != nil || len(b) != n {
		return p, false
	}
	name := b[:addr.StageNameLen]
	if i := bytes.IndexByte(name, 0); i >= 0 {
		name = name[:i]
	}
	room, err := mem.ReadS8(m, addr.StayNo)
	if err != nil {
		return p, false
	}
	p.Stage, p.Room = string(name), room
	pending := b[n-1] != 0
	p.InPlay = addr.InMEM1(link) && !pending && ovlp != addr.OverlapRequest && p.Stage != "" && p.Stage != addr.StageTitle && p.Stage != addr.StageFileSelect
	return p, true
}
