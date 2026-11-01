package addr

import "testing"

// TestStOffsets pins st_class (d_a_st.h:46-150, retail layout) to the
// header's offsets and to each other (wind#27).
func TestStOffsets(t *testing.T) {
	checkPins(t, []offPin{
		{"StClassSize (d_a_st.h:150)", StClassSize, 0x2368},
		{"StClassSize (mShadowId + 4 + m2364 + pad)", StClassSize, StOffShadowID + 8},
		{"StOffShadowID (mEnemyIce + enemyice, d_a_st.h:146)", StOffShadowID, StOffIce + enemyIceClassSize},
		{"StOffBehavior (d_a_st.h:49)", StOffBehavior, 0x2B4},
		{"StOffDeathSwitch (d_a_st.h:53)", StOffDeathSwitch, 0x2B8},
		{"StOffDrawGate (d_a_st.h:54)", StOffDrawGate, 0x2B9},
		{"StOffMorf (d_a_st.h:56)", StOffMorf, 0x2BC},
		{"StOffAction (d_a_st.h:59)", StOffAction, 0x2C6},
		{"StOffHairTimer (mTimers[2], d_a_st.h:70)", StOffHairTimer, 0x2F0},
		{"StOffInvuln (d_a_st.h:72)", StOffInvuln, 0x2F6},
		{"StOffM02FA (d_a_st.h:75)", StOffM02FA, 0x2FA},
		{"StOffM0304 (d_a_st.h:79)", StOffM0304, 0x304},
		{"StOffSph (mStts 0x510 + dCcD_Stts, d_a_st.h:88)", StOffSph, 0x510 + ccSttsSize},
		{"StOffWeaponSph (m054C[7], d_a_st.h:89)", StOffWeaponSph, StOffSph + StSphs*ccSphSize},
		{"StSphStride (a dCcD_Sph)", StSphStride, ccSphSize},
		{"StOffParts (d_a_st.h:94)", StOffParts, 0xED4},
		{"StOffHeadState (mParts[15].mPartState)", StOffHeadState, 0x152C},
		{"StOffHeadPos (mParts[15].mPartPos)", StOffHeadPos, 0x1530},
		{"StOffNunchaku (d_a_st.h:99; mParts + 26 st_p + mKe + mLineMat + mNun)", StOffNunchaku, 0x1DC8},
		{"StOffBokoPID (d_a_st.h:101)", StOffBokoPID, 0x1DCC},
		{"StOffMace (d_a_st.h:102)", StOffMace, 0x1DD0},
		{"StOffUpperPID (d_a_st.h:104)", StOffUpperPID, 0x1DD4},
		{"StOffHeadOff (d_a_st.h:105)", StOffHeadOff, 0x1DD8},
		{"StOffShake (d_a_st.h:110)", StOffShake, 0x1DE4},
		{"StOffBodyHP (d_a_st.h:118)", StOffBodyHP, 0x1E84},
		{"StOffDeathWait (d_a_st.h:119)", StOffDeathWait, 0x1E85},
		{"StOffIce (d_a_st.h:145)", StOffIce, 0x1FA8},
		{"StOffIceShrink", StOffIceShrink, 0x1FAE},
		{"StOffIceFreeze", StOffIceFreeze, 0x1FB6},
	})
	if StPartCount*StPartStride+StOffParts != 0x19CC {
		t.Errorf("mParts ends at 0x%X, mKe is at 0x19CC (d_a_st.h:95)", StOffParts+StPartCount*StPartStride)
	}
	if StBckWait < StBckFirst || StBckWait > StBckLast || StBckCount != 0x32 {
		t.Errorf("St BCK range 0x%X..0x%X wait 0x%X", StBckFirst, StBckLast, StBckWait)
	}
}
