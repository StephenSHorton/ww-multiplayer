package addr

// The warship, daOship_c (include/d/actor/d_a_oship.h:88-151; proc
// fpcNm_OSHIP_e 0x00B4, ProcOship in remote.go, f_pc_name.h:189), as the
// enemy mirror's OSHIP row reads it (wind#46, docs/design/enemy-rows-next.md
// "Enemy mirror row: Warship"; the C copy is inject/include/wwenemy_kinds.h
// EMK_OSHIP_*). Offsets are from the actor in the retail layout (the
// VERSION > DEMO pad m296 present). Not an fopEn_enemy_c: no enemyice, no
// McaMorf, no BCK; its body is the bare J3DModel mpModel.
const (
	// OshipClassSize is sizeof(daOship_c) (d_a_oship.h:151).
	OshipClassSize = 0xE00
	// OshipOffProc is int mCurrentProc (d_a_oship.h:91): 0 WAIT, 1 ATTACK,
	// 2 DAMAGE, 3 DELETE (sinking), 4-7 RANGE_A..D (mode_tbl,
	// d_a_oship.cpp:765-774).
	OshipOffProc = 0x2AC
	// OshipOffSubMode .. OshipOffModelType are getArg's bytes
	// (d_a_oship.h:92-98, d_a_oship.cpp:986-1003): params & 0xFF, switch A
	// params >> 24, switch B home.angle.x & 0xFF, path, Triforce chart,
	// salvage point, model type home.angle.x >> 8 (0xFF normal, 00 the
	// gold ship).
	OshipOffSubMode   = 0x2B0
	OshipOffSwitchA   = 0x2B1
	OshipOffModelType = 0x2B6
	// OshipOffModel is J3DModel* mpModel (d_a_oship.h:123): the row's
	// MorfOff (the REL's EMK_MTX_MODEL reads it as the model itself).
	OshipOffModel = 0x4B0
	// OshipOffHitTimer is int mHitTimer (d_a_oship.h:138): 5 after a hit
	// (d_a_oship.cpp:378); the wire's INVULN is its low s16.
	OshipOffHitTimer = 0xD70
	// OshipOffAimY and OshipOffAimX are s16 mAimRotY / mAimRotX
	// (d_a_oship.h:144, 146): the HEAD and CANON joint callbacks read them
	// (d_a_oship.cpp:96-125); streamed as aux[0] and aux[1].
	OshipOffAimY = 0xD84
	OshipOffAimX = 0xD88

	// OshipProcDelete is mCurrentProc's DELETE: the ship is sinking.
	OshipProcDelete = 3
	// OshipModelNormal is mModelType for every ship but the gold one.
	OshipModelNormal = 0xFF
	// OshipHealthMax is createInit's max_health (d_a_oship.cpp:894).
	OshipHealthMax = 3
)
