package addr

import "testing"

// TestTnOffsets pins tn_class (d_a_tn.h:29-165) to the header's offsets
// and to each other: the class ends at mpJntHit + 4, enemyfire follows
// enemyice, the damagereaction's fields hang off mDamageReaction, the
// parts are three 0x4C tn_p, and the colliders are a dCcD_Cyl or
// dCcD_Sph apart from mCoCyl to m1384 (TN design rel_changes R-1).
func TestTnOffsets(t *testing.T) {
	checkPins(t, []offPin{
		{"TnClassSize (d_a_tn.h:165)", TnClassSize, 0x1B08},
		{"TnClassSize (mpJntHit + 4)", TnClassSize, TnOffJntHit + 4},
		{"TnOffJntHit (mEnemyFire + enemyfire, d_a_tn.h:164)", TnOffJntHit, TnOffFire + enemyFireClassSize},
		{"TnOffFire (mEnemyIce + enemyice, d_a_tn.h:163)", TnOffFire, TnOffIce + enemyIceClassSize},
		{"TnOffBehavior (d_a_tn.h:33)", TnOffBehavior, 0x2BC},
		{"TnOffDeathSwitch (d_a_tn.h:37)", TnOffDeathSwitch, 0x2C0},
		{"TnOffM02C1 (d_a_tn.h:38)", TnOffM02C1, 0x2C1},
		{"TnOffMorf (d_a_tn.h:40)", TnOffMorf, 0x2C4},
		{"TnOffShieldMorf (d_a_tn.h:41)", TnOffShieldMorf, TnOffMorf + 4},
		{"TnOffArmorMorf (d_a_tn.h:42)", TnOffArmorMorf, TnOffShieldMorf + 4},
		{"TnOffPieces (d_a_tn.h:43)", TnOffPieces, 0x2D0},
		{"TnOffParts (d_a_tn.h:50)", TnOffParts, 0x2E0},
		{"TnPartStride (sizeof tn_p, d_a_tn.h:26)", TnPartStride, 0x4C},
		{"part 1 model (design R-7: 0x32C)", TnOffParts + 1*TnPartStride + TnPartOffModel, 0x32C},
		{"part 2 model (design R-7: 0x378)", TnOffParts + 2*TnPartStride + TnPartOffModel, 0x378},
		{"part 0 m08 (design R-7: 0x2E8)", TnOffParts + TnPartOffM08, 0x2E8},
		{"TnOffArmorColor (after mParts[3], d_a_tn.h:51)", TnOffArmorColor, TnOffPartsEnd},
		{"TnOffEquip (d_a_tn.h:52)", TnOffEquip, 0x3C5},
		{"TnOffCountDown (d_a_tn.h:56)", TnOffCountDown, 0x3E0},
		{"TnOffPlaySkip (d_a_tn.h:59)", TnOffPlaySkip, 0x3EE},
		{"TnOffInvuln (d_a_tn.h:60)", TnOffInvuln, 0x3F0},
		{"TnOffTargetAngle (d_a_tn.h:73)", TnOffTargetAngle, 0x416},
		{"TnOffDR (d_a_tn.h:84)", TnOffDR, 0x474},
		{"m0C30 (mDamageReaction + damagereaction, d_a_tn.h:85)", TnOffDR + DamageReactionSize, 0xC30},
		{"TnOffDRMode (c_damagereaction.h:80)", TnOffDRMode, 0x478},
		{"TnOffDRAction (c_damagereaction.h:81)", TnOffDRAction, 0x47A},
		{"TnOffDRM088 (design R-5: 0x4FC)", TnOffDRM088, 0x4FC},
		{"TnDRM088Len (csXyz[20])", TnDRM088Len, 20 * 6},
		{"TnOffTilt (design R-1 TILT: 0x900)", TnOffTilt, 0x900},
		{"TnOffDRInvincible", TnOffDRInvincible, 0x942},
		{"TnOffWeapon (d_a_tn.h:86)", TnOffWeapon, 0xC34},
		{"TnOffCoCyl (d_a_tn.h:94)", TnOffCoCyl, 0xC74},
		{"TnOffTgCyl (mCoCyl + dCcD_Cyl, d_a_tn.h:95)", TnOffTgCyl, TnOffCoCyl + ccCylSize},
		{"TnOffTgCyl", TnOffTgCyl, 0xDA4},
		{"TnOffHeadSph (mTgCyl + dCcD_Cyl, d_a_tn.h:96)", TnOffHeadSph, TnOffTgCyl + ccCylSize},
		{"TnOffHeadSph", TnOffHeadSph, 0xED4},
		{"TnOffDefSph (d_a_tn.h:97)", TnOffDefSph, TnOffHeadSph + ccSphSize},
		{"TnOffDefSph", TnOffDefSph, 0x1000},
		{"TnOffWeponSph (d_a_tn.h:98)", TnOffWeponSph, TnOffDefSph + ccSphSize},
		{"TnOffWepon2Sph (d_a_tn.h:99)", TnOffWepon2Sph, TnOffWeponSph + ccSphSize},
		{"TnOffHeadCenter (m1384 after mWepon2Sph, d_a_tn.h:100)", TnOffHeadCenter, TnOffWepon2Sph + ccSphSize},
		{"TnOffHeadCenter", TnOffHeadCenter, 0x1384},
		{"TnOffDefR (design R-1 DEF_R: 0x1124)", TnOffDefR, 0x1124},
		{"TnOffDefCenter (d_a_tn.h:108)", TnOffDefCenter, 0x13E4},
		{"TnOffM1402 (d_a_tn.h:116)", TnOffM1402, 0x1402},
		{"TnOffM140C (d_a_tn.h:120)", TnOffM140C, 0x140C},
		{"TnOffBokoPID (d_a_tn.h:122)", TnOffBokoPID, 0x1410},
		{"TnOffM1444 (d_a_tn.h:141)", TnOffM1444, 0x1444},
		{"TnOffMantPID (d_a_tn.h:143)", TnOffMantPID, 0x1448},
		{"TnOffM14F8 (d_a_tn.h:148)", TnOffM14F8, 0x14F8},
		{"TnOffM1518 (d_a_tn.h:154)", TnOffM1518, 0x1518},
		{"TnOffIce (d_a_tn.h:162)", TnOffIce, 0x1524},
		{"TnOffIceShrink (ice +6, design 0x152A)", TnOffIceShrink, 0x152A},
		{"TnOffIceFreeze (ice +0xE)", TnOffIceFreeze, 0x1532},
		{"ActorOffSubMtd (f_op_actor.h:274)", ActorOffSubMtd, 0xEC},
		{"MethodOffExecute (f_pc_method.h:13)", MethodOffExecute, 8},
		{"TnExecToPartMove (design P0-c)", TnExecToPartMove, 0x1040},
		{"TnExecToYoroiBreak (design P0-c)", TnExecToYoroiBreak, 0x224C},
	})
	if TnPieceArmor != 1 || TnPieceHelmet != 2 || TnPieceShield != 4 || TnPieceUnknown != 8 || TnEquipShield != 2 || TnEquipCape != 4 {
		t.Error("TN piece / equipment constants moved")
	}
	if TnActDefence != 10 || TnActFail != 20 || TnActYoganFail != 21 || TnActWaterFail != 22 || TnActDDozou != 23 || TnActSDemo != 25 || TnBehaviorFrozen != 15 {
		t.Error("TN action constants moved")
	}
	if TnJntMune1 != 0xA || TnJntItemL1 != 0xF || TnJntItemR1 != 0x17 || TnJntAtama1 != 0x1C {
		t.Error("TN joints moved")
	}
	if ActorOffSubMtd+4 > ActorSize {
		t.Error("sub_method runs past the actor R1 reads")
	}
}

// TestTnBckRange pins the Tn body BCK range (Tn.h:44-89; design D7).
func TestTnBckRange(t *testing.T) {
	checkPins(t, []offPin{
		{"TnBckFirst (AAT_KAITEN1)", TnBckFirst, 0x28},
		{"TnBckLast (S_DEMO_M2)", TnBckLast, 0x55},
		{"TnBckCount", TnBckCount, 46},
		{"TnBckWait (AWAIT1)", TnBckWait, 0x3D},
		{"TnBckTateOn (ATATE_ON1)", TnBckTateOn, 0x3C},
		{"TnBckArmorKihon (YKIHON1)", TnBckArmorKihon, 0x64},
	})
	if TnArc != "Tn" || TnBckArmorKihon <= TnBckLast || Mo2BckNone <= TnBckLast {
		t.Errorf("Tn archive %q or the armor/none index in the body range", TnArc)
	}
}
