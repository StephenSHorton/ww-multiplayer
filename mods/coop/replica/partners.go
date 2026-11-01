package replica

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/StephenSHorton/ww-multiplayer/mods/coop/wire"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
)

// Co-op for three or more (DES step 5; docs/08 Appendix C item 171). The
// hub relays each player's PRESENCE with its sender (wire.PresenceDown,
// schema 2), so a replica keeps every partner's place and track apart:
//
//   - scope: a dan or zone store is written while any partner is in its
//     scope (partnerIn). The hub keeps a store while anyone is there, and
//     with two players this is item 122's rule unchanged; a bit held
//     because nobody was there waits for any partner to come back;
//   - warp: Warp to names its partner (WarpTo); with exactly one partner
//     the name may be left out, as v1.0's two-player warp did.

// MaxPartners bounds the partners a replica keeps: one per co-op
// connection the hub can have (network.CoopConn.ID is 1..255).
const MaxPartners = 255

// partnerView is one partner as this replica knows it: its last PRESENCE
// and what the warp learns from the run of them (partnerTrack).
type partnerView struct {
	p     wire.PresenceDown
	track partnerTrack
}

// PartnerStatus is one partner in Status.Partners.
type PartnerStatus struct {
	Name     string
	Presence wire.Presence
}

// gone reports whether m is a PRESENCE of nowhere with no stage at all:
// the hub's word for a player who left (hub.leaveView), or one whose game
// has not loaded a stage yet. Either way it has no place to keep.
func gone(m *wire.PresenceDown) bool {
	return m.Stage.IsEmpty() && m.Flags&wire.PresenceInGame == 0
}

// onPartnerPresence follows a relayed PRESENCE (under r.mu). Partners
// are kept by co-op connection (Origin), not by name: a player who
// reconnects has a new connection, and the old one's PRESENCE of nowhere
// may come after the new one's first, so it must drop only the old.
func (r *Replica) onPartnerPresence(m *wire.PresenceDown) {
	v := r.partners[m.Origin]
	if gone(m) {
		delete(r.partners, m.Origin)
		return
	}
	if v == nil {
		if len(r.partners) >= MaxPartners {
			return
		}
		v = &partnerView{}
		r.partners[m.Origin] = v
	}
	if addr.InCredits(m.Stage.String()) && !addr.InCredits(v.p.Stage.String()) {
		// GT Q5: that partner's game left the co-op world at the ending;
		// this game plays on and its own ending is its own.
		r.logf("co-op: %s has reached the credits; their game has left the co-op world, yours plays on", m.Name)
	}
	v.p = *m
	r.notePartnerRest(&v.track, &v.p.Presence)
	r.learnPartner(&v.track, &v.p.Presence)
}

// partnerNames is every partner's name, sorted, each once.
func (r *Replica) partnerNames() []string {
	ns := make([]string, 0, len(r.partners))
	for _, v := range r.partners {
		ns = append(ns, v.p.Name)
	}
	slices.Sort(ns)
	return slices.Compact(ns)
}

// sortedPartners is every partner by name, then connection.
func (r *Replica) sortedPartners() []*partnerView {
	vs := make([]*partnerView, 0, len(r.partners))
	for _, v := range r.partners {
		vs = append(vs, v)
	}
	slices.SortFunc(vs, func(a, b *partnerView) int {
		if c := strings.Compare(a.p.Name, b.p.Name); c != 0 {
			return c
		}
		return int(a.p.Origin) - int(b.p.Origin)
	})
	return vs
}

// byName is partner name's view: of its connections (a reconnect can
// leave two for a moment), the newest, which has the higher connection
// ID unless the IDs wrapped.
func (r *Replica) byName(name string) *partnerView {
	var got *partnerView
	for _, v := range r.sortedPartners() {
		if v.p.Name == name {
			got = v
		}
	}
	return got
}

// onlyPartner is the one partner's PRESENCE while there is exactly one.
func (r *Replica) onlyPartner() *wire.Presence {
	ns := r.partnerNames()
	if len(ns) != 1 {
		return nil
	}
	p := r.byName(ns[0]).p.Presence
	return &p
}

func (r *Replica) partnerStatus() []PartnerStatus {
	var ps []PartnerStatus
	for _, v := range r.sortedPartners() {
		ps = append(ps, PartnerStatus{Name: v.p.Name, Presence: v.p.Presence})
	}
	return ps
}

// partnerIn reports whether any partner's last PRESENCE, as the hub
// relayed it, puts it in scope k: in game, in k's stage and save table
// and, for a ZONE store, in its room. The hub keeps a store while anyone
// is in its scope, so a bit it relayed while a partner was there is
// written only while one still is; one that came while this game was
// not idle waits for a partner to come back. The hub relays every
// PRESENCE as it comes (hub.onPresence) and a gone peer's as nowhere
// under its name (hub.leaveView); with two players this is Appendix C
// item 122's rule, with more it is any of them (item 171).
func (r *Replica) partnerIn(k scopeKey) bool {
	for _, v := range r.partners {
		p := &v.p
		if p.Flags&wire.PresenceInGame == 0 || p.Stage != k.stage || p.Table != k.table {
			continue
		}
		if k.kind != wire.ScopeZone || p.Room == k.room {
			return true
		}
	}
	return false
}

// errNoPartner is a warp with no partner to go to.
var errNoPartner = errors.New("no word of a partner yet: is its co-op connected?")

// resolvePartner is the partner a warp to name goes to: name's, or with
// name "" the one partner there is.
func (r *Replica) resolvePartner(name string) (*partnerView, error) {
	if name != "" {
		if v := r.byName(name); v != nil {
			return v, nil
		}
		if len(r.partners) == 0 {
			return nil, fmt.Errorf("no partner %s: %w", name, errNoPartner)
		}
		return nil, fmt.Errorf("no partner %s here (partners: %s)", name, strings.Join(r.partnerNames(), ", "))
	}
	switch ns := r.partnerNames(); len(ns) {
	case 0:
		return nil, errNoPartner
	case 1:
		return r.byName(ns[0]), nil
	default:
		return nil, fmt.Errorf("say which partner to warp to: %s", strings.Join(ns, ", "))
	}
}
