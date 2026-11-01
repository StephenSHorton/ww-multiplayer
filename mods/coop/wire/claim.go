package wire

import "fmt"

// Chest claims (M9, docs/08 §6.7.1). CLAIM (0x09, both ways) carries one
// player's ask for, or the hub's verdict on, the right to open one chest
// that a partner in the same stage and table could open too:
//
//	[op:u8][table:s8][no:u8][room:s8]   = 4 B
//
// Up (c->s) the op is ClaimAsk or ClaimRelease. Down (s->c) it is
// ClaimGrant, ClaimDeny or ClaimConsumed. A chest is named by its save table
// (the receiver's mDan.mStageNo, or STAGE_SEA2's for a funcType 4/5 chest,
// d_a_tbox.cpp:314-320) and its chest number, (params >> 7) & 0x1F
// (isTbox's bit, dSv_memBit_c::mTbox is one u32). room is the room the
// sender's Link is in (mStayNo), for the logs and the hub's scope check;
// -1 at a stage start. Only a peer granted proto.FeatureChestClaim sends or
// is sent one.
type Claim struct {
	Op    ClaimOp
	Table int8
	No    uint8
	Room  int8
}

// ClaimOp is CLAIM's op.
type ClaimOp uint8

const (
	ClaimAsk      ClaimOp = 1 // c->s: let me open this chest (sent again at 1 Hz as the renewal)
	ClaimRelease  ClaimOp = 2 // c->s: I no longer want it (walked away, room change, co-op off)
	ClaimGrant    ClaimOp = 3 // s->c: it is yours until you release it, it is consumed or the lease expires
	ClaimDeny     ClaimOp = 4 // s->c: another player holds it, or it is consumed
	ClaimConsumed ClaimOp = 5 // s->c: the world's chest bit is set: keep it held, never restore it
)

// MaxChestNo is the highest chest number: dSv_memBit_c::mTbox is one u32
// (d_save.h; isTbox's 1 << no).
const MaxChestNo = 31

const claimLen = 4

func (op ClaimOp) String() string {
	switch op {
	case ClaimAsk:
		return "CLAIM"
	case ClaimRelease:
		return "RELEASE"
	case ClaimGrant:
		return "GRANT"
	case ClaimDeny:
		return "DENY"
	case ClaimConsumed:
		return "CONSUMED"
	}
	return fmt.Sprintf("ClaimOp(%d)", uint8(op))
}

// Up reports whether op travels client to server.
func (op ClaimOp) Up() bool { return op == ClaimAsk || op == ClaimRelease }

// Down reports whether op travels server to client.
func (op ClaimOp) Down() bool { return op == ClaimGrant || op == ClaimDeny || op == ClaimConsumed }

func (*Claim) Sub() Sub { return SubClaim }

func (m *Claim) validate() error {
	if !m.Op.Up() && !m.Op.Down() {
		return bad("CLAIM op %d", m.Op)
	}
	if !validTable(m.Table) {
		return bad("CLAIM table %d (want 0..15)", m.Table)
	}
	if m.No > MaxChestNo {
		return bad("CLAIM chest %d (want 0..%d)", m.No, MaxChestNo)
	}
	if m.Room != -1 && !validRoom(m.Room) {
		return bad("CLAIM room %d (want -1 or 0..63)", m.Room)
	}
	return nil
}

func (m *Claim) appendBody(b []byte) []byte {
	return append(b, byte(m.Op), byte(m.Table), m.No, byte(m.Room))
}

func decodeClaim(body []byte, dir Dir) (*Claim, error) {
	if len(body) != claimLen {
		return nil, bad("CLAIM body is %d bytes, want %d", len(body), claimLen)
	}
	m := &Claim{Op: ClaimOp(body[0]), Table: int8(body[1]), No: body[2], Room: int8(body[3])}
	if (dir == Up && m.Op.Down()) || (dir == Down && m.Op.Up()) {
		return nil, bad("CLAIM %s does not travel %s", m.Op, dir)
	}
	return validated(m)
}
