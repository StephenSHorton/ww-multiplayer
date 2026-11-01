package hub

import (
	"testing"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/mods/coop/wire"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
)

// chestGain is the GAINS byte that opens chest no of table t (mTbox, a
// big-endian u32).
func chestGain(t, no int) wire.Gain {
	off := addr.OffMemBit(t) + addr.MemBitOffTbox + 3 - no/8
	return wire.Gain{Off: uint16(off), Val: 1 << (no % 8)}
}

func claim(op wire.ClaimOp, table int8, no uint8) *wire.Claim {
	return &wire.Claim{Op: op, Table: table, No: no, Room: 1}
}

// verdicts returns the CLAIM answers in ms.
func verdicts(ms []wire.Msg) []wire.ClaimOp {
	var ops []wire.ClaimOp
	for _, c := range pick[*wire.Claim](ms) {
		ops = append(ops, c.Op)
	}
	return ops
}

func oneVerdict(t *testing.T, who string, ms []wire.Msg, want wire.ClaimOp) {
	t.Helper()
	if got := verdicts(ms); len(got) != 1 || got[0] != want {
		t.Fatalf("%s got %v, want [%s]", who, got, want)
	}
}

// claimRig is a hub with two bound peers, alice (1) and bob (2), on a
// clock the test moves.
func claimRig(t *testing.T) (*rig, *fakeConn, *fakeConn, *time.Time) {
	r := newRig(t, fullCfg())
	clock := time.Unix(1_800_000_000, 0)
	if err := r.h.do(func() { r.h.now = func() time.Time { return clock } }); err != nil {
		t.Fatal(err)
	}
	alice := r.join(newConn(1, "alice"))
	bob := r.join(newConn(2, "bob"))
	alice.take(t)
	bob.take(t)
	return r, alice, bob, &clock
}

// TestClaimRace: the first CLAIM for a chest wins it; the partner's is
// denied while the lease lasts; the owner's renewal is granted again.
func TestClaimRace(t *testing.T) {
	r, alice, bob, _ := claimRig(t)
	oneVerdict(t, "alice", r.exchange(alice, claim(wire.ClaimAsk, 3, 2)), wire.ClaimGrant)
	oneVerdict(t, "bob", r.exchange(bob, claim(wire.ClaimAsk, 3, 2)), wire.ClaimDeny)
	oneVerdict(t, "alice renewing", r.exchange(alice, claim(wire.ClaimAsk, 3, 2)), wire.ClaimGrant)
	// Another chest, and the same number in another table, are free.
	oneVerdict(t, "bob, chest 3", r.exchange(bob, claim(wire.ClaimAsk, 3, 3)), wire.ClaimGrant)
	oneVerdict(t, "bob, table 4", r.exchange(bob, claim(wire.ClaimAsk, 4, 2)), wire.ClaimGrant)
	if !r.log.has("chest claim: denied bob") {
		t.Error("no log line for the denial")
	}
}

// TestClaimRelease: a RELEASE frees the chest for the partner; a release
// by someone who doesn't hold it changes nothing.
func TestClaimRelease(t *testing.T) {
	r, alice, bob, _ := claimRig(t)
	oneVerdict(t, "alice", r.exchange(alice, claim(wire.ClaimAsk, 3, 2)), wire.ClaimGrant)
	if ms := r.exchange(bob, claim(wire.ClaimRelease, 3, 2)); len(ms) != 0 {
		t.Fatalf("a RELEASE got answers: %v", ms)
	}
	oneVerdict(t, "bob", r.exchange(bob, claim(wire.ClaimAsk, 3, 2)), wire.ClaimDeny)
	r.exchange(alice, claim(wire.ClaimRelease, 3, 2))
	oneVerdict(t, "bob after the release", r.exchange(bob, claim(wire.ClaimAsk, 3, 2)), wire.ClaimGrant)
}

// TestClaimDisconnect: a claimant that leaves loses its leases, so a
// chest is never lost to a gone player.
func TestClaimDisconnect(t *testing.T) {
	r, alice, bob, _ := claimRig(t)
	oneVerdict(t, "alice", r.exchange(alice, claim(wire.ClaimAsk, 3, 2)), wire.ClaimGrant)
	r.h.detach(alice)
	r.sync()
	oneVerdict(t, "bob", r.exchange(bob, claim(wire.ClaimAsk, 3, 2)), wire.ClaimGrant)
}

// TestClaimExpiry: a lease not renewed for ClaimLease goes to the next
// claimant; a renewed one does not.
func TestClaimExpiry(t *testing.T) {
	r, alice, bob, clock := claimRig(t)
	set := func(d time.Duration) {
		if err := r.h.do(func() { *clock = clock.Add(d) }); err != nil {
			t.Fatal(err)
		}
	}
	oneVerdict(t, "alice", r.exchange(alice, claim(wire.ClaimAsk, 3, 2)), wire.ClaimGrant)
	set(ClaimLease - time.Second)
	oneVerdict(t, "alice renewing", r.exchange(alice, claim(wire.ClaimAsk, 3, 2)), wire.ClaimGrant)
	set(ClaimLease - time.Second)
	oneVerdict(t, "bob before expiry", r.exchange(bob, claim(wire.ClaimAsk, 3, 2)), wire.ClaimDeny)
	set(2 * time.Second)
	oneVerdict(t, "bob after expiry", r.exchange(bob, claim(wire.ClaimAsk, 3, 2)), wire.ClaimGrant)
	oneVerdict(t, "alice now", r.exchange(alice, claim(wire.ClaimAsk, 3, 2)), wire.ClaimDeny)
}

// TestClaimConsumed: once the chest's bit reaches the world, every peer
// with chest claims is told CONSUMED, the lease ends, and later claims
// are answered CONSUMED.
func TestClaimConsumed(t *testing.T) {
	r, alice, bob, _ := claimRig(t)
	oneVerdict(t, "alice", r.exchange(alice, claim(wire.ClaimAsk, 3, 2)), wire.ClaimGrant)
	r.send(alice, gains(1, chestGain(3, 2)))
	for _, c := range []*fakeConn{alice, bob} {
		cs := pick[*wire.Claim](c.take(t))
		if len(cs) != 1 || cs[0].Op != wire.ClaimConsumed || cs[0].Table != 3 || cs[0].No != 2 {
			t.Fatalf("%s got %+v, want one CONSUMED for table 3 chest 2", c.name, cs)
		}
	}
	oneVerdict(t, "bob after", r.exchange(bob, claim(wire.ClaimAsk, 3, 2)), wire.ClaimConsumed)
	oneVerdict(t, "alice after", r.exchange(alice, claim(wire.ClaimAsk, 3, 2)), wire.ClaimConsumed)
	// A chest the world had opened before anyone claimed it.
	r.send(bob, gains(1, chestGain(3, 9)))
	alice.take(t)
	bob.take(t)
	oneVerdict(t, "alice, an opened chest", r.exchange(alice, claim(wire.ClaimAsk, 3, 9)), wire.ClaimConsumed)
}

// TestClaimFeatureOff: a peer without FeatureChestClaim is refused with a
// NOTICE and gets no verdict.
func TestClaimFeatureOff(t *testing.T) {
	r := newRig(t, Config{Features: DefaultFeatures &^ FeatureChestClaim, Policy: fullCfg().Policy, SyncedScoped: syncAll, SyncedKill: syncAll})
	alice := r.join(newConn(1, "alice"))
	alice.take(t)
	ms := r.exchange(alice, claim(wire.ClaimAsk, 3, 2))
	if len(verdicts(ms)) != 0 || len(pick[*wire.Notice](ms)) != 1 {
		t.Fatalf("got %v, want one NOTICE and no verdict", ms)
	}
}
