package addr

import "testing"

// The allow batch's classes (enemy-mirror-allow.json "type_table" KI, AM,
// AM2), pinned to the decomp headers' offsets and to each other: the
// class size is the last member's end (enemyice 0x3B8, enemyfire 0x228,
// c_damagereaction.h:43,69), the first collider follows mStts's 0x3C
// bytes, the colliders are a dCcD_Cyl (0x130) or dCcD_Sph (0x12C) apart,
// m_ground_h hangs off mAcch, and the enemyice fields off mEnemyIce.

const (
	enemyIceClassSize  = 0x3B8 // sizeof(enemyice) (c_damagereaction.h:43)
	enemyFireClassSize = 0x228 // sizeof(enemyfire) (c_damagereaction.h:69)
	ccSttsSize         = 0x3C  // dCcD_Stts (d_a_cc.h:89-90: mStts 0x680, mCyl 0x6BC)
	ccSphSize          = 0x12C
	ccCylSize          = 0x130
)

type offPin struct {
	name      string
	got, want int
}

func checkPins(t *testing.T, pins []offPin) {
	t.Helper()
	for _, c := range pins {
		if c.got != c.want {
			t.Errorf("%s = 0x%X, want 0x%X", c.name, c.got, c.want)
		}
	}
}

// TestKiOffsets pins ki_class (d_a_ki.h:14-87).
func TestKiOffsets(t *testing.T) {
	checkPins(t, []offPin{
		{"KiClassSize (d_a_ki.h:87)", KiClassSize, 0xF04},
		{"KiClassSize (mEnemyFire + enemyfire)", KiClassSize, KiOffFire + enemyFireClassSize},
		{"KiOffFire (mEnemyIce + enemyice, d_a_ki.h:86)", KiOffFire, KiOffIce + enemyIceClassSize},
		{"KiOffMorf (d_a_ki.h:27)", KiOffMorf, 0x2B4},
		{"KiOffParams (d_a_ki.h:29)", KiOffParams, 0x2CC},
		{"KiOffPathIndex (d_a_ki.h:31)", KiOffPathIndex, 0x2CE},
		{"KiOffAction (d_a_ki.h:34)", KiOffAction, 0x2D1},
		{"KiOffBehavior (d_a_ki.h:36)", KiOffBehavior, 0x2D3},
		{"KiOffDrawGate (d_a_ki.h:37)", KiOffDrawGate, 0x2D4},
		{"KiOffCurrPathIndex (d_a_ki.h:38)", KiOffCurrPathIndex, 0x2D5},
		{"KiOffShrinkDelete (d_a_ki.h:42)", KiOffShrinkDelete, 0x2DC},
		{"KiOffTimers (d_a_ki.h:54)", KiOffTimers, 0x30C},
		{"KiOffInvuln (after mTimers[4], d_a_ki.h:55)", KiOffInvuln, KiOffTimers + 4*2},
		{"KiOffKnockback (d_a_ki.h:58)", KiOffKnockback, 0x31C},
		{"KiOffHeadBank (d_a_ki.h:60)", KiOffHeadBank, 0x322},
		{"KiOffAux1 (d_a_ki.h:62)", KiOffAux1, 0x326},
		{"KiOffAux2 (d_a_ki.h:63)", KiOffAux2, KiOffAux1 + 2},
		{"KiOffM338 (d_a_ki.h:70)", KiOffM338, 0x338},
		{"KiOffAcchCir (d_a_ki.h:73)", KiOffAcchCir, 0x33C},
		{"KiOffAcch (mAcchCir + dBgS_AcchCir 0x40, d_a_ki.h:74)", KiOffAcch, KiOffAcchCir + 0x40},
		{"KiOffAcch (d_a_ki.h:74)", KiOffAcch, 0x37C},
		{"KiOffGroundH (mAcch + 0x94)", KiOffGroundH, 0x410},
		{"KiOffShadow (d_a_ki.h:75)", KiOffShadow, 0x540},
		{"KiOffStts (d_a_ki.h:76)", KiOffStts, 0x544},
		{"KiOffAtSph (mStts + dCcD_Stts, d_a_ki.h:77)", KiOffAtSph, KiOffStts + ccSttsSize},
		{"KiOffAtSph", KiOffAtSph, 0x580},
		{"KiOffCoSph (m580 + dCcD_Sph, d_a_ki.h:78)", KiOffCoSph, KiOffAtSph + ccSphSize},
		{"KiOffCoSph", KiOffCoSph, 0x6AC},
		{"KiOffTgSph (m6AC + dCcD_Sph, d_a_ki.h:79)", KiOffTgSph, KiOffCoSph + ccSphSize},
		{"KiOffTgSph", KiOffTgSph, 0x7D8},
		{"KiOffSphsEnd (d_a_ki.h:80)", KiOffSphsEnd, KiOffTgSph + ccSphSize},
		{"KiOffDmgType (d_a_ki.h:81)", KiOffDmgType, 0x905},
		{"KiOffBtk (d_a_ki.h:84)", KiOffBtk, 0x920},
		{"KiOffIce (d_a_ki.h:85)", KiOffIce, 0x924},
		{"KiOffIceShrink (ice +6)", KiOffIceShrink, 0x92A},
		{"KiOffIceFreeze (ice +0xE)", KiOffIceFreeze, 0x932},
	})
	if KiActFail != 30 || KiActAttack != 3 || KiActAttackIndex != 10 || KiActDamage != 20 || KiBehaviorLunge != 3 || KiAttnUp != 50 {
		t.Error("KI action constants moved")
	}
}

// TestAmOffsets pins am_class (d_a_am.h:11-57).
func TestAmOffsets(t *testing.T) {
	checkPins(t, []offPin{
		{"AmClassSize (mEnemyFire + enemyfire)", AmClassSize, AmOffFire + enemyFireClassSize},
		{"AmClassSize", AmClassSize, 0x109C},
		{"AmOffFire (mEnemyIce + enemyice, d_a_am.h:56)", AmOffFire, AmOffIce + enemyIceClassSize},
		{"AmOffEyeJntHit (d_a_am.h:14)", AmOffEyeJntHit, 0x2B4},
		{"AmOffMorf (d_a_am.h:15)", AmOffMorf, 0x2B8},
		{"AmOffType (d_a_am.h:17)", AmOffType, 0x2C0},
		{"AmOffStartsInactive (d_a_am.h:19)", AmOffStartsInactive, 0x2C2},
		{"AmOffSwitch (d_a_am.h:20)", AmOffSwitch, 0x2C3},
		{"AmOffAction (d_a_am.h:21)", AmOffAction, 0x2C4},
		{"AmOffMode (d_a_am.h:22)", AmOffMode, AmOffAction + 1},
		{"AmOffHugeKnockback (d_a_am.h:23)", AmOffHugeKnockback, 0x2C6},
		{"AmOffBodyHit (d_a_am.h:24)", AmOffBodyHit, 0x2C7},
		{"AmOffCountDown (d_a_am.h:25)", AmOffCountDown, 0x2C8},
		{"AmOffPlaySkip (mCountDownTimers[1])", AmOffPlaySkip, 0x2CA},
		{"AmOffCountUp (d_a_am.h:26; after s16[4])", AmOffCountUp, AmOffCountDown + 4*2},
		{"AmOffTargetAngleY (d_a_am.h:27; after s16[5])", AmOffTargetAngleY, AmOffCountUp + 5*2},
		{"AmOffCurrBckIdx (d_a_am.h:30)", AmOffCurrBckIdx, 0x2E0},
		{"AmOffSwallowedPID (d_a_am.h:31)", AmOffSwallowedPID, 0x2E4},
		{"AmOffEyeballPos (d_a_am.h:35)", AmOffEyeballPos, 0x2F4},
		{"AmOffMouthPos (d_a_am.h:36)", AmOffMouthPos, AmOffEyeballPos + 0xC},
		{"AmOffWaistPos (d_a_am.h:37)", AmOffWaistPos, AmOffMouthPos + 0xC},
		{"AmOffJawPos (d_a_am.h:38)", AmOffJawPos, AmOffWaistPos + 0xC},
		{"AmOffEyeRot (d_a_am.h:40)", AmOffEyeRot, 0x330},
		{"AmOffEyeRotY", AmOffEyeRotY, 0x332},
		{"AmOffEmitter (d_a_am.h:45; after mTargetEyeRot csXyz)", AmOffEmitter, AmOffEyeRot + 2*6},
		{"AmOffAcch (d_a_am.h:49)", AmOffAcch, 0x404},
		{"AmOffGroundH (mAcch + 0x94)", AmOffGroundH, 0x498},
		{"AmOffStts (d_a_am.h:50)", AmOffStts, 0x5C8},
		{"AmOffEyeSph (mStts + dCcD_Stts, d_a_am.h:51)", AmOffEyeSph, AmOffStts + ccSttsSize},
		{"AmOffEyeSph", AmOffEyeSph, 0x604},
		{"AmOffMouthSph (d_a_am.h:52)", AmOffMouthSph, AmOffEyeSph + ccSphSize},
		{"AmOffMouthSph", AmOffMouthSph, 0x730},
		{"AmOffBodyCyl (d_a_am.h:53)", AmOffBodyCyl, AmOffMouthSph + ccSphSize},
		{"AmOffBodyCyl", AmOffBodyCyl, 0x85C},
		{"AmOffNeedleCyl (d_a_am.h:54)", AmOffNeedleCyl, AmOffBodyCyl + ccCylSize},
		{"AmOffNeedleCyl", AmOffNeedleCyl, 0x98C},
		{"AmOffIce (d_a_am.h:55)", AmOffIce, AmOffNeedleCyl + ccCylSize},
		{"AmOffIce", AmOffIce, 0xABC},
		{"AmOffIceShrink (ice +6)", AmOffIceShrink, 0xAC2},
		{"AmOffIceFreeze (ice +0xE)", AmOffIceFreeze, 0xACA},
	})
	if AmActItai != 3 || AmModeBombSwallow != 44 || AmModeBombThrash != 46 || AmModeDead != 47 || AmEyeUp != 250 || AmAttnUp != 330 || AmTgHitUp != 150 {
		t.Error("AM action/mode constants moved")
	}
}

// TestAm2Offsets pins am2_class (d_a_am2.h:11-58).
func TestAm2Offsets(t *testing.T) {
	checkPins(t, []offPin{
		{"Am2ClassSize (mEnemyIce + enemyice)", Am2ClassSize, Am2OffIce + enemyIceClassSize},
		{"Am2ClassSize", Am2ClassSize, 0xE44},
		{"Am2OffMorf (d_a_am2.h:15)", Am2OffMorf, 0x2B8},
		{"Am2OffBtk (d_a_am2.h:17)", Am2OffBtk, 0x2C0},
		{"Am2OffBrk (d_a_am2.h:18)", Am2OffBrk, 0x2C4},
		{"Am2OffType (d_a_am2.h:19)", Am2OffType, 0x2C8},
		{"Am2OffStartsInactive (d_a_am2.h:21)", Am2OffStartsInactive, 0x2CA},
		{"Am2OffSwitch (d_a_am2.h:22)", Am2OffSwitch, 0x2CB},
		{"Am2OffAction (d_a_am2.h:23)", Am2OffAction, 0x2CC},
		{"Am2OffMode (d_a_am2.h:24)", Am2OffMode, Am2OffAction + 1},
		{"Am2OffM2CE (d_a_am2.h:25)", Am2OffM2CE, 0x2CE},
		{"Am2OffWeakHit (d_a_am2.h:26)", Am2OffWeakHit, 0x2CF},
		{"Am2OffNotInHomeRoom (d_a_am2.h:27)", Am2OffNotInHomeRoom, 0x2D0},
		{"Am2OffAbyssTimer (d_a_am2.h:29)", Am2OffAbyssTimer, 0x2D2},
		{"Am2OffEyeballPos (d_a_am2.h:31)", Am2OffEyeballPos, 0x2E0},
		{"Am2OffNeedlePos (d_a_am2.h:32)", Am2OffNeedlePos, Am2OffEyeballPos + 0xC},
		{"Am2OffWeakPos (d_a_am2.h:33)", Am2OffWeakPos, Am2OffNeedlePos + 0xC},
		{"Am2OffCountDown (d_a_am2.h:38)", Am2OffCountDown, 0x334},
		{"Am2OffStunTimer (mCountDownTimers[2])", Am2OffStunTimer, 0x338},
		{"Am2OffCountUp (d_a_am2.h:39; after s16[5])", Am2OffCountUp, Am2OffCountDown + 5*2},
		{"Am2OffTargetAngleY (d_a_am2.h:40; after s16[4])", Am2OffTargetAngleY, Am2OffCountUp + 4*2},
		{"Am2OffCurrBckIdx (d_a_am2.h:43)", Am2OffCurrBckIdx, 0x34C},
		{"Am2OffPickedUpY (d_a_am2.h:46)", Am2OffPickedUpY, 0x358},
		{"Am2OffAcchCir (d_a_am2.h:48)", Am2OffAcchCir, 0x360},
		{"Am2OffAcch (d_a_am2.h:49)", Am2OffAcch, 0x3A0},
		{"Am2OffGroundH (mAcch + 0x94)", Am2OffGroundH, 0x434},
		{"Am2OffStts (d_a_am2.h:50)", Am2OffStts, 0x564},
		{"Am2OffBodyCyl (mStts + dCcD_Stts, d_a_am2.h:51)", Am2OffBodyCyl, Am2OffStts + ccSttsSize},
		{"Am2OffBodyCyl", Am2OffBodyCyl, 0x5A0},
		{"Am2OffNeedleCyl (d_a_am2.h:52)", Am2OffNeedleCyl, Am2OffBodyCyl + ccCylSize},
		{"Am2OffNeedleCyl", Am2OffNeedleCyl, 0x6D0},
		{"Am2OffEyeSph (d_a_am2.h:53)", Am2OffEyeSph, Am2OffNeedleCyl + ccCylSize},
		{"Am2OffEyeSph", Am2OffEyeSph, 0x800},
		{"Am2OffWeakSph (d_a_am2.h:54)", Am2OffWeakSph, Am2OffEyeSph + ccSphSize},
		{"Am2OffWeakSph", Am2OffWeakSph, 0x92C},
		{"Am2OffSmokeCb (d_a_am2.h:55)", Am2OffSmokeCb, Am2OffWeakSph + ccSphSize},
		{"Am2OffIce (d_a_am2.h:57)", Am2OffIce, 0xA8C},
		{"Am2OffIceShrink (ice +6)", Am2OffIceShrink, 0xA92},
		{"Am2OffIceFreeze (ice +0xE)", Am2OffIceFreeze, 0xA9A},
	})
	if Am2ActItai != 2 || Am2ActModoru != 4 || Am2ModeDance != 22 || Am2EyeDown != 15 || Am2AttnUp != 120 || Am2TgHitUp != 75 {
		t.Error("AM2 action constants moved")
	}
}

// TestAllowShared pins the generic fields the allow batch's samplers
// read: the actor's scale, m_ground_h, and a collider's radius and height.
func TestAllowShared(t *testing.T) {
	checkPins(t, []offPin{
		{"ActorOffScale (shape_angle csXyz 6 + pad 2)", ActorOffScale, ActorOffShapeAngle + 8},
		{"ActorOffSpeed (scale cXyz + 0xC)", ActorOffSpeed, ActorOffScale + 0xC},
		{"AcchOffGroundH (d_bg_s_acch.h:235)", AcchOffGroundH, 0x94},
		{"CcShapeOffR (centre + 0xC)", CcShapeOffR, 0x124},
		{"CcShapeOffH (centre + 0x10)", CcShapeOffH, 0x128},
	})
	if CcShapeOffR+4 > ccSphSize || CcShapeOffH+4 > ccCylSize {
		t.Error("a collider's radius or height runs past its dCcD_Sph or dCcD_Cyl")
	}
}

// TestAllowBckRanges pins the Ki, Am and Am2 BCK ranges (Ki.h:18-23,
// Am.h:25-36, Am2.h:11-20).
func TestAllowBckRanges(t *testing.T) {
	checkPins(t, []offPin{
		{"KiBckFirst (ATTACK1)", KiBckFirst, 0x0F},
		{"KiBckLast (WAIT1)", KiBckLast, 0x14},
		{"KiBckCount", KiBckCount, 6},
		{"KiBckWait (WAIT1)", KiBckWait, 0x14},
		{"AmBckFirst (BOM_NOMI)", AmBckFirst, 0x14},
		{"AmBckLast (SLEEP_LOOP)", AmBckLast, 0x1F},
		{"AmBckCount", AmBckCount, 12},
		{"AmBckWait (SLEEP_LOOP)", AmBckWait, 0x1F},
		{"AmBckDead (DEAD)", AmBckDead, 0x1A},
		{"Am2BckFirst (BURUBURU)", Am2BckFirst, 0x06},
		{"Am2BckLast (WAIT)", Am2BckLast, 0x0F},
		{"Am2BckCount", Am2BckCount, 10},
		{"Am2BckWait (WAIT)", Am2BckWait, 0x0F},
	})
	if KiArc != "Ki" || AmArc != "AM" || Am2Arc != "AM2" {
		t.Errorf("archive names %q %q %q", KiArc, AmArc, Am2Arc)
	}
	if AmBckDead < AmBckFirst || AmBckDead > AmBckLast || Mo2BckNone <= AmBckLast {
		t.Error("AM DEAD row or the none index out of range")
	}
}
