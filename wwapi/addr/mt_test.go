package addr

import "testing"

// TestMtOffsets pins the Magtail's offsets (no decomp header: the retail
// d_a_mt.rel, C1-magtail.md, batch C "mt") to each other: the 8 body
// spheres are a dCcD_Sph apart after mStts's 0x3C bytes, the eye sphere
// follows them, the lunge blur follows the eye, the trail ring's three
// arrays are back to back, and the ice fields hang off the enemyice.
func TestMtOffsets(t *testing.T) {
	checkPins(t, []offPin{
		{"MtClassSize (g_profile_MT +0x10)", MtClassSize, 0x1CC0},
		{"MtOffMorf", MtOffMorf, 0x2C4},
		{"MtOffMode", MtOffMode, 0x454},
		{"MtOffAction (mode + 1)", MtOffAction, MtOffMode + 1},
		{"MtOffBodySph (mStts + dCcD_Stts)", MtOffBodySph, MtOffStts + ccSttsSize},
		{"MtBodySphStride (dCcD_Sph)", MtBodySphStride, ccSphSize},
		{"MtOffBodyShieldSPrm (sph[0] GObjTg mSPrm, +0x94)", MtOffBodyShieldSPrm, MtOffBodySph + 0x94},
		{"MtOffEyeSph (after the 8 body spheres)", MtOffEyeSph, MtOffBodySphsEnd},
		{"MtOffBlur (eye sphere + dCcD_Sph)", MtOffBlur, MtOffEyeSph + ccSphSize},
		{"MtOffRingAngle (ring pos + cXyz[64])", MtOffRingAngle, MtOffRingPos + MtRingLen*12},
		{"ring wiggle s16[64] (EMK_MT_RING_WIGGLE 0xB74)", MtOffRingAngle + MtRingLen*6, 0xB74},
		{"MtOffRingIdx (wiggle + s16[64])", MtOffRingIdx, 0xB74 + MtRingLen*2},
		{"MtOffIceShrink", MtOffIceShrink, 0x1906},
		{"MtOffKilled (EMK_MT_KILLED)", MtOffKilled, 0x1CBC},
	})
	if MtOffKilled >= MtClassSize || MtOffIce+enemyIceClassSize > MtOffKilled {
		t.Errorf("killed flag 0x%X, enemyice 0x%X..0x%X in a 0x%X class", MtOffKilled, MtOffIce, MtOffIce+enemyIceClassSize, MtClassSize)
	}
}
