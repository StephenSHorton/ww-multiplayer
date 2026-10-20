package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/StephenSHorton/ww-multiplayer/internal/inject"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mailbox"
)

// The fake FULL body, shanm table, DMC entry and profile list.
const (
	fakeFull     = uint32(0x80CA0000)
	fakeShanm    = uint32(0x80CB0000)
	fakeDMC      = uint32(0x80D00000)
	fakeProfList = uint32(0x80D10000)
	fakeProfile  = uint32(0x80D20000)
)

// TestRemoteSummaryFull: a FULL body in slot 1 reads the full tail and
// daPy fields (kind from body_status), the block's +0x1C fields print,
// the profile line sees the alias and the body profile, the shanm line
// the table's header; each fault is named.
func TestRemoteSummaryFull(t *testing.T) {
	m, _ := remoteRoom(t)
	blk := mailbox.RemoteBlock{Magic: mailbox.RemoteMagic, Seq: 9, Layout: mailbox.RemoteLayout, Role: mailbox.RemoteRoleHost,
		Flags: mailbox.RemoteFlagBodies | mailbox.RemoteFlagFullBodies, FullAllow: 0x02, HeapReason: mailbox.RemoteHGOpen, Flags2: mailbox.RemoteFlag2FullCo}
	blk.Slots[1] = mailbox.RemoteSlot{Active: true, Stage: "M_Dai", Room: 1, PoseSlot: 1, Gen: 2}
	if err := mailbox.WriteRemoteBlock(m, &blk); err != nil {
		t.Fatal(err)
	}
	st := remoteStatus(100)
	st.Bodies[1] = mailbox.RemoteBodyStatus{Actor: fakeFull, PID: 77, State: mailbox.RemoteBodyLive, Kind: mailbox.RemoteKindFull, Tries: 1}
	storeStatus(t, m, &st)

	// The body: proc 0xB5 / prof 0x27, daPy vtable, the tail, mCurProc
	// CROUCH_DEFENSE, mCyl Co SPrm without Set.
	store(t, m, fakeFull+addr.ActorOffProcName, u16b(inject.RELProcID))
	store(t, m, fakeFull+actorOffProf, u16b(mailbox.RemoteFullProc))
	store(t, m, fakeFull+mailbox.RemoteFullVPtrOff, u32b(dapyLkVtable))
	store(t, m, fakeFull+mailbox.RemoteFullDamageWaitOff, u16b(12))
	store(t, m, fakeFull+mailbox.RemoteFullMagicOff, append(u32b(mailbox.RemoteBodyMagic), 1, 2, 3, mailbox.RemoteBodyLive))
	store(t, m, fakeFull+mailbox.RemoteFullPhaseOff, []byte{mailbox.RemoteFPLive, mailbox.RemoteKindFull, 0, 5, 0, 0, 0, 9})
	store(t, m, fakeFull+mailbox.RemoteFullCurProcOff, u32b(0x0C))
	store(t, m, fakeFull+mailbox.RemoteFullCylOff+cylOffTgSPrm, u32b(0x05))
	store(t, m, fakeFull+mailbox.RemoteFullCylOff+cylOffCoSPrm, u32b(0x72))

	// The shanm table and the profile wiring.
	hdr := append(u32b(mailbox.RemoteShanmMagic), u32b(mailbox.RemoteShanmBytes)...)
	hdr = append(hdr, u32b(fakeShanm)...)
	for _, v := range []uint32{2598, 12, 0, 0, 3, 2} {
		hdr = append(hdr, u32b(v)...)
	}
	store(t, m, fakeShanm, hdr)
	store(t, m, mailbox.Mailbox+mailbox.OffRELShanm, u32b(fakeShanm))
	store(t, m, mailbox.RemoteDMCFullAddr, u32b(fakeDMC))
	store(t, m, mailbox.RemoteDMCRELAddr, u32b(fakeDMC))
	store(t, m, mailbox.RemoteProfileListPtr, u32b(fakeProfList))
	store(t, m, fakeProfList+4*uint32(mailbox.RemoteFullProc), u32b(fakeProfile))
	store(t, m, fakeProfList+4*uint32(inject.RELProcID), u32b(fakeProfile+0x40))
	store(t, m, fakeProfile+0x08, u16b(inject.RELProcID))
	store(t, m, fakeProfile+0x10, u32b(mailbox.RemoteFullSize))

	summary := func() (*remoteSnap, string) {
		t.Helper()
		s, err := readRemoteSnap(m)
		if err != nil {
			t.Fatal(err)
		}
		var out bytes.Buffer
		remoteSummary(&out, 1, s, s, 0, nil, false)
		return s, out.String()
	}
	s, out := summary()
	if v := s.valid(); v != "valid" {
		t.Errorf("valid: %q", v)
	}
	for _, want := range []string{
		"full: full_allow 0x02 heap_reason open flags2 0x0001 (Co on)",
		"profile: DMC[0x27] 0x80D00000 DMC[0xB5] 0x80D00000 aliased; list 0x80D10000 slot 0x27 -> 0x80D20000 g_profile_WWBODY (proc 0x00B5, size 0x4C80), slot 0xB5 -> 0x80D20040",
		"shanm: 0x80CB0000 'WWSA' ok: words 2598 create_diff 12 delete_diff 0 delete_scan 0 (ok) windows 3 joint_diff 2",
		"body: FULL LIVE err none actor 0x80CA0000 pid 77",
		"FULL read: proc 0x00B5 prof 0x0027 (ok) magic 'WWRB' slot 1 gen 2 state LIVE iframes_left 3 phase LIVE kind 1 create_frames 5 shanm_diff 9",
		"vptr 0x8038FDC0 daPy_lk_c ok", "cur_proc 0xC damage_wait 12", "tg_sprm 0x00000005 co_sprm 0x00000072 (Co off)",
		"body: lite LIVE err none actor 0x80C90000", // slot 0 stays a lite read
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}

	// Faults: no alias, the wrong profile, a bad table, a lite vtable in
	// a FULL body, Co set.
	store(t, m, mailbox.RemoteDMCFullAddr, u32b(fakeDMC+0x20))
	store(t, m, fakeProfile+0x10, u32b(0x600))
	store(t, m, fakeShanm+mailbox.RemoteSASelfOff, u32b(0))
	store(t, m, fakeFull+mailbox.RemoteFullVPtrOff, u32b(0x8038FD00))
	store(t, m, fakeFull+mailbox.RemoteFullCylOff+cylOffCoSPrm, u32b(0x73))
	store(t, m, fakeShanm+mailbox.RemoteSADeleteScanOff, u32b(1))
	_, out = summary()
	for _, want := range []string{"NOT ALIASED", "NOT the body profile (proc 0x00B5 size 0x600", "BAD: self 0x00000000, not the block",
		"NOT __vt__9daPy_lk_c 0x8038FDC0", "(Co SET)"} {
		if !strings.Contains(out, want) {
			t.Errorf("faulted output lacks %q:\n%s", want, out)
		}
	}
	store(t, m, fakeShanm+mailbox.RemoteSASelfOff, u32b(fakeShanm))
	if _, out = summary(); !strings.Contains(out, "delete_scan 1 (NOT 0: tracked words point into a deleted body)") {
		t.Errorf("delete scan:\n%s", out)
	}

	// A client block carrying the fields, or two allow bits, is not valid.
	blk.Role, blk.Seq = mailbox.RemoteRoleClient, 10
	if err := mailbox.WriteRemoteBlock(m, &blk); err != nil {
		t.Fatal(err)
	}
	if s, _ = summary(); !strings.Contains(s.valid(), "client block carries full_allow 0x02 heap_reason 1 flags2 0x0001") {
		t.Errorf("client: %q", s.valid())
	}
	blk.Role, blk.Seq, blk.FullAllow = mailbox.RemoteRoleHost, 11, 0x03
	if err := mailbox.WriteRemoteBlock(m, &blk); err != nil {
		t.Fatal(err)
	}
	if s, _ = summary(); !strings.Contains(s.valid(), "full_allow 0x03 is not one slot bit") {
		t.Errorf("two bits: %q", s.valid())
	}
}
