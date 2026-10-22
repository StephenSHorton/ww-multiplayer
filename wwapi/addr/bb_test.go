package addr

import "testing"

// TestBbOffsets pins bb_class (d_a_bb.h:13-108) to the header's offsets
// and to each other: the class ends at mEnemyFire + enemyfire, enemyfire
// follows enemyice, the four spheres are a dCcD_Sph apart from
// mHeadAtSph (after mStts's 0x3C bytes), and the tail arrays hold
// BbTailLen cXyz / csXyz each (batch C "bb").
func TestBbOffsets(t *testing.T) {
	checkPins(t, []offPin{
		{"BbClassSize (d_a_bb.h:108)", BbClassSize, 0x1260},
		{"BbClassSize (mEnemyFire + enemyfire)", BbClassSize, BbOffFire + enemyFireClassSize},
		{"BbOffFire (mEnemyIce + enemyice, d_a_bb.h:107)", BbOffFire, BbOffIce + enemyIceClassSize},
		{"BbOffMorf (d_a_bb.h:16)", BbOffMorf, 0x2B4},
		{"BbOffEyeBtp (d_a_bb.h:18)", BbOffEyeBtp, 0x2CC},
		{"BbOffType (d_a_bb.h:23)", BbOffType, 0x2D8},
		{"BbOffState (d_a_bb.h:28)", BbOffState, 0x2DD},
		{"BbOffRider (d_a_bb.h:30)", BbOffRider, 0x2DF},
		{"BbOffM2F0 (d_a_bb.h:35)", BbOffM2F0, 0x2F0},
		{"BbOffM2F1 (d_a_bb.h:36)", BbOffM2F1, 0x2F1},
		{"BbOffDrawGate (d_a_bb.h:37)", BbOffDrawGate, 0x2F2},
		{"BbOffTimers (d_a_bb.h:45)", BbOffTimers, 0x318},
		{"BbOffAttackTimer (unk_318[1])", BbOffAttackTimer, 0x31A},
		{"BbOffInvuln (d_a_bb.h:55)", BbOffInvuln, 0x340},
		{"BbOffKnock (d_a_bb.h:58)", BbOffKnock, 0x348},
		{"BbOffM34C (d_a_bb.h:59)", BbOffM34C, 0x34C},
		{"BbOffM35D (d_a_bb.h:65)", BbOffM35D, 0x35D},
		{"BbOffM366 (d_a_bb.h:70)", BbOffM366, 0x366},
		{"BbOffM368 (d_a_bb.h:71)", BbOffM368, 0x368},
		{"BbOffMidEnd (unk_368 + 2)", BbOffMidEnd, BbOffM368 + 2},
		{"BbOffGroundH (mAcch 0x3B8 + m_ground_h 0x94)", BbOffGroundH, 0x3B8 + 0x94},
		{"BbOffHeadAtSph (mStts 0x580 + dCcD_Stts, d_a_bb.h:77)", BbOffHeadAtSph, 0x580 + ccSttsSize},
		{"BbOffHeadTgSph (d_a_bb.h:78)", BbOffHeadTgSph, BbOffHeadAtSph + ccSphSize},
		{"BbOffBodyTgSph (d_a_bb.h:79)", BbOffBodyTgSph, BbOffHeadTgSph + ccSphSize},
		{"BbOffBodyCoSph (d_a_bb.h:80)", BbOffBodyCoSph, BbOffBodyTgSph + ccSphSize},
		{"BbOffSphsEnd (unk_A6C, d_a_bb.h:81)", BbOffSphsEnd, BbOffBodyCoSph + ccSphSize},
		{"BbOffTailModels (d_a_bb.h:82)", BbOffTailModels, 0xA84},
		{"BbOffTailPos (unk_A84 + 9 pointers, d_a_bb.h:83)", BbOffTailPos, BbOffTailModels + 9*4},
		{"BbOffTailRot (unk_AA8 + cXyz[10], d_a_bb.h:84)", BbOffTailRot, BbOffTailPos + BbTailLen*12},
		{"BbOffTailVel (unk_B20 + csXyz[10], d_a_bb.h:85)", BbOffTailVel, BbOffTailRot + BbTailLen*6},
		{"BbOffTailRoot (unk_B5C + cXyz[10], d_a_bb.h:86)", BbOffTailRoot, BbOffTailVel + BbTailLen*12},
		{"BbOffTailRoot1 (unk_BD4[1])", BbOffTailRoot1, 0xBE0},
		{"BbOffHeadRot (d_a_bb.h:91)", BbOffHeadRot, 0xC00},
		{"BbOffBeak (unk_C00[11] + unk_C42[2], d_a_bb.h:93)", BbOffBeak, BbOffHeadRot + 11*6 + 2*6},
		{"BbOffC58 (d_a_bb.h:97)", BbOffC58, 0xC58},
		{"BbOffDead (d_a_bb.h:105)", BbOffDead, 0xC7E},
		{"BbOffIce (d_a_bb.h:106)", BbOffIce, 0xC80},
		{"BbOffIceShrink", BbOffIceShrink, 0xC86},
		{"BbOffIceFreeze", BbOffIceFreeze, 0xC8E},
		{"BbOffIceY (m028)", BbOffIceY, 0xCA8},
		{"BbExecToTail (daBb_Execute - tail_control)", BbExecToTail, 0x65A4},
	})
	if BbTailBlrOff >= BbExecToTail || BbFpBlr != 0x4E800020 || BbFpExec[1] != 0x7C0802A6 || BbFpTail[1] != BbFpExec[1] {
		t.Errorf("the tail fingerprint: blr +0x%X of 0x%X, words %08X %08X", BbTailBlrOff, BbExecToTail, BbFpExec, BbFpTail)
	}
}
