package fakegame

import (
	"math"

	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
)

// The Moblin's animation as the host's stream reads it (addr/res.go,
// addr/mo2.go): LoadMo2Res lays one mObjectInfo entry out as the loaded
// "Mo2" archive, whose mRes points at a distinct stand-in per resource
// (Mo2Res), and SetMo2Anm gives a Moblin an mpMorf playing one of them
// as setAnm leaves it. Nothing else in the fake reads either.
const (
	// Mo2ResEntry is the mObjectInfo index the archive takes.
	Mo2ResEntry = 5
	// Mo2ResTable is the archive's mRes (Mo2BckLast+1 words); mo2Archive
	// stands in for its JKRArchive and mo2ResData for its resources, one
	// mo2ResSize apart.
	Mo2ResTable uint32 = 0x80D00000
	mo2Archive  uint32 = 0x80D01000
	mo2ResData  uint32 = 0x80D02000
	mo2ResSize         = 0x40
	// mo2MorfOff is where in a Moblin's block its mDoExt_McaMorf stand-in
	// lives: past every field the fake or a reader uses (the ice timer at
	// 0x2A5A), within enemyBlock.
	mo2MorfOff = 0x2C00
)

// Mo2Res returns the pointer mRes[idx] of the archive LoadMo2Res lays
// out (0 for an index past it).
func Mo2Res(idx uint8) uint32 {
	if int(idx) > addr.Mo2BckLast {
		return 0
	}
	return mo2ResData + mo2ResSize*uint32(idx)
}

// LoadMo2Res loads the "Mo2" archive into mObjectInfo[Mo2ResEntry] with
// mCount Mo2BckLast+1 and mRes at Mo2ResTable (d_resorce.h:40-47).
func (g *FakeGame) LoadMo2Res() {
	g.mu.Lock()
	defer g.mu.Unlock()
	e := addr.MObjectInfo + Mo2ResEntry*addr.ResInfoSize
	name := make([]byte, addr.ResInfoNameLen)
	copy(name, addr.Mo2Arc)
	g.store(e+addr.ResInfoOffName, name)
	g.st16(e+addr.ResInfoOffCount, addr.Mo2BckLast+1)
	g.st32(e+addr.ResInfoOffArchive, mo2Archive)
	g.st32(e+addr.ResInfoOffRes, Mo2ResTable)
	for i := range addr.Mo2BckLast + 1 {
		g.st32(Mo2ResTable+4*uint32(i), Mo2Res(uint8(i)))
	}
}

// SetMo2Anm is Moblin pid's setAnm(Mo2Res(bck), attr, morf, rate) with
// the frame control at frame (m_Do_ext.cpp:1414-1460): mpMorf (at
// addr.Mo2OffMorf) points at its stand-in, mpAnm is the resource, and
// setMorf leaves mCurMorf 0 and mMorfStep 1/morf for morf > 0, else
// mCurMorf 1.
func (e *Enemies) SetMo2Anm(g *FakeGame, pid uint32, bck, attr uint8, frame, rate float32, morf uint8) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	e.mu.Lock()
	defer e.mu.Unlock()
	i := e.find(pid)
	if i < 0 {
		return ErrNoActor
	}
	mf := e.live[i].at + mo2MorfOff
	g.st32(e.live[i].at+addr.Mo2OffMorf, mf)
	g.st32(mf+addr.McaMorfOffAnm, Mo2Res(bck))
	g.st8(mf+addr.McaMorfOffAttr, attr)
	g.st32(mf+addr.McaMorfOffRate, math.Float32bits(rate))
	g.st32(mf+addr.McaMorfOffFrame, math.Float32bits(frame))
	cur, step := float32(1), float32(0)
	if morf > 0 {
		cur, step = 0, 1/float32(morf)
	}
	g.st32(mf+addr.McaMorfOffCurMorf, math.Float32bits(cur))
	g.st32(mf+addr.McaMorfOffMorfStep, math.Float32bits(step))
	return nil
}
