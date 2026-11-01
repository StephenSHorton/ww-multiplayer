package hub

import (
	"encoding/binary"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/mods/coop/wire"
	"github.com/StephenSHorton/ww-multiplayer/mods/coop/world"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/save"
)

// Chest claims (M9, docs/08 §6.7.1). The hub is the only judge of who may
// open a chest two players could both reach: the first CLAIM for a
// (table, chest) nobody holds and the world has not opened is granted;
// every other is denied. A lease ends with the claimant's RELEASE, its
// disconnect, ClaimLease without a renewal (the claimant re-asks at 1 Hz
// while in range), or the chest's bit reaching the world (consumed: every
// claiming peer is told, so a held chest is never restored). The host's
// own replica claims through the same path; it gets no shortcut.

// ClaimLease is how long a granted claim lasts without a renewal.
const ClaimLease = 5 * time.Second

type chestKey struct {
	table int8
	no    uint8
}

type chestLease struct {
	owner   world.Player
	renewed time.Time
}

// chestOpened reports whether chest no of table t is set in img: the
// table's saved memBit's mTbox, a big-endian u32 (addr.TboxBit).
func chestOpened(img *save.Image, k chestKey) bool {
	off := addr.OffMemBit(int(k.table)) + addr.MemBitOffTbox
	return binary.BigEndian.Uint32(img[off:off+4])&(1<<k.no) != 0
}

// onClaim answers p's CLAIM.
func (h *Hub) onClaim(p *peer, m *wire.Claim) {
	if !p.bound() {
		h.complain(p, wire.NoticeNotBound, "refused CLAIM: your file is not bound to world %s", h.id)
		return
	}
	if p.features&FeatureChestClaim == 0 {
		h.complain(p, wire.NoticeRejected, "refused CLAIM: chest claims are off for this session")
		return
	}
	k := chestKey{table: m.Table, no: m.No}
	if h.leases == nil {
		h.leases = map[chestKey]chestLease{}
	}
	switch m.Op {
	case wire.ClaimRelease:
		if l, ok := h.leases[k]; ok && l.owner == p.id {
			delete(h.leases, k)
			h.logf("co-op: chest claim: %s released table %d chest %d", p, k.table, k.no)
		}
		return
	case wire.ClaimAsk:
	default:
		return // Decode only lets c->s ops through
	}
	img := h.store.Image()
	reply := &wire.Claim{Table: m.Table, No: m.No, Room: m.Room}
	now := h.now()
	l, held := h.leases[k]
	if held && l.owner != p.id && now.Sub(l.renewed) > ClaimLease {
		h.logf("co-op: chest claim: %s's lease on table %d chest %d expired", h.peerName(l.owner), k.table, k.no)
		delete(h.leases, k)
		held = false
	}
	switch {
	case chestOpened(&img, k):
		delete(h.leases, k)
		reply.Op = wire.ClaimConsumed
	case held && l.owner != p.id:
		reply.Op = wire.ClaimDeny
		h.logf("co-op: chest claim: denied %s table %d chest %d (held by %s)", p, k.table, k.no, h.peerName(l.owner))
	default:
		if !held {
			h.logf("co-op: chest claim: granted %s table %d chest %d", p, k.table, k.no)
		}
		h.leases[k] = chestLease{owner: p.id, renewed: now}
		reply.Op = wire.ClaimGrant
	}
	h.send(p, reply)
}

// consumeClaims ends every lease whose chest the world now has opened and
// tells each peer with chest claims, so a held chest stays held. Called
// after the world changed (GAINS, WORLD).
func (h *Hub) consumeClaims() {
	if len(h.leases) == 0 {
		return
	}
	img := h.store.Image()
	to := h.sorted(func(q *peer) bool { return q.bound() && q.features&FeatureChestClaim != 0 })
	for k := range h.leases {
		if !chestOpened(&img, k) {
			continue
		}
		delete(h.leases, k)
		h.logf("co-op: chest claim: table %d chest %d consumed", k.table, k.no)
		h.sendAll(to, &wire.Claim{Op: wire.ClaimConsumed, Table: k.table, No: k.no, Room: -1})
	}
}

// dropClaims ends p's leases (it left, or stopped being bound).
func (h *Hub) dropClaims(p *peer) {
	for k, l := range h.leases {
		if l.owner == p.id {
			delete(h.leases, k)
			h.logf("co-op: chest claim: %s gone, released table %d chest %d", p, k.table, k.no)
		}
	}
}

// peerName names player id for a log line.
func (h *Hub) peerName(id world.Player) string {
	if q := h.byID[id]; q != nil {
		return q.String()
	}
	return "a gone player"
}
