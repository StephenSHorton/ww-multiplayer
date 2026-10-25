package gui

import (
	"fmt"
	"slices"
	"strings"

	"github.com/StephenSHorton/ww-multiplayer/internal/session"
	"github.com/StephenSHorton/ww-multiplayer/mods/coop/proto"
)

// The room: what co-op shares as rules the host changes while the session
// runs, the Room items card (what the world holds) and the Players card's
// co-op row facts, the app ideas of the ww-online review (their
// docs/screenshots room-owner-edit.png, room-items.png and room-player.png;
// WWOnline.Client/Views/RoomFlagsView.axaml, RoomItemsView.axaml and
// WWOnline.Shared/Models/RoomSettings.cs, learned from, nothing copied).
// Theirs are server rules any owner edits from a room page; ours are the
// host hub's features (proto.Feature), which the hub already changes
// while it runs (hub.SetFeatures: CTRL COOP_OFF to every peer, a fresh
// WORLD for one that gets bytes back), so only the host's app edits them,
// and only those a replica follows at its next tick (proto.Shareable).

// CoopRuleView is one row of the rules: a session feature (ID, the proto
// name) or a session choice ("merge", "dry-run").
type CoopRuleView struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	What  string `json:"what"`
	// On: the host allows it (the host's view, from its hub), or this
	// game is granted it (a joiner's, from its pipeline).
	On bool `json:"on"`
	// Edit: the host may turn it on or off now; Why says why not when
	// the row is read-only for the host.
	Edit bool   `json:"edit"`
	Why  string `json:"why,omitempty"`
	// Missing names the players the hub doesn't grant it although the
	// host allows it, MissingWhy why (hub grant: a peer gets what it asks
	// for, which a dry run keeps to the World, and no rule that reads
	// the stage tables unless its tables are the host's): the host's
	// view only.
	Missing    []string `json:"missing,omitempty"`
	MissingWhy string   `json:"missingWhy,omitempty"`
}

// CoopRulesView is the rules card.
type CoopRulesView struct {
	// Host: this player hosts, so the rules are its own; Edit: it can
	// change them now (its hub serves the world).
	Host bool           `json:"host"`
	Edit bool           `json:"edit"`
	Rows []CoopRuleView `json:"rows"`
	// Note is a line under the rows ("" for none).
	Note string `json:"note,omitempty"`
}

// coopRule is a row of the rules card.
type coopRule struct {
	id, label, what string
	f               proto.Feature // 0 for a session choice
	// why the host can't change it while the session runs ("" when it
	// can).
	why string
}

// coopRules are the rows, in the order players think of them (the
// progress card's SHARES, coop.js).
var coopRules = []coopRule{
	{"image", "World", "Chests, pickups, dungeon maps and compasses, charts and visited rooms", proto.FeatureImage,
		"It is the shared world itself, and every other rule hangs on it. Each player's Pause co-op writes stops it for their game."},
	{"story", "Story", "Story progress, held back where a game isn't ready for it", proto.FeatureStory, ""},
	{"inventory", "Items", "Items, songs, Triforce shards, pearls and bag sizes", proto.FeatureInventory, ""},
	{"switches", "Switches", "Switches that stay set: doors, gates, bridges", proto.FeatureSwitches, ""},
	{"puzzle", "Puzzles", "Room and dungeon puzzles solved", proto.FeaturePuzzle, ""},
	{"combat", "Kills", "Enemies that stay defeated once one of you beats them", proto.FeatureCombat, ""},
	{"keys", "Small keys", "Each dungeon's small keys, counted from the chests and doors", proto.FeatureKeys, ""},
	{"life", "Hearts", "Max hearts, from every Heart Container and Piece of Heart the world has taken", proto.FeatureLife, ""},
	{"magic", "Magic", "The magic meter", proto.FeatureMagic, ""},
	{"live-switches", "Live switches", "Switches of the room you stand in, while you are idle", proto.FeatureLiveSwitches, ""},
	{"merge", "Merging", "A file with progress the world lacks may still bind, and its progress joins the world", 0, ""},
	{"dry-run", "Dry run", "Co-op plans its writes and logs them, but writes nothing into the games", 0,
		"Each game's co-op writer is set up when co-op starts: choose it before Start."},
}

// sharesNote is the rules card's note when Small keys and Switches
// differ: a key door's switch is what spends its key (derive/keys.go), so
// keys without switches let one key open a door in each game.
const sharesNote = "Small keys are counted from the doors the switches share: with Switches off, one key can open a door in each game."

// rulesView is the rules card for st: the host's from its hub (what it
// allows, editable), a joiner's from its pipeline (what its game is
// granted). nil before co-op runs.
func rulesView(st session.State, mk masker) *CoopRulesView {
	host := st.Lobby != nil && st.Lobby.Host
	h := st.CoopHub
	var on map[string]bool
	switch {
	case host && h != nil:
		on = nameSet(h.Features)
	case len(st.CoopStatus) > 0 && !st.CoopStatus[0].DryRun:
		on = nameSet(strings.Join(st.CoopStatus[0].Shares, "|"))
	case len(st.CoopStatus) > 0:
		on = map[string]bool{}
	default:
		return nil
	}
	v := &CoopRulesView{Host: host, Edit: host && h != nil, Rows: []CoopRuleView{}}
	merge := st.Lobby != nil && st.Lobby.Settings.Merge
	dry := st.Lobby != nil && st.Lobby.Settings.DryRun
	for _, r := range coopRules {
		row := CoopRuleView{ID: r.id, Label: r.label, What: r.what, On: on[r.id], Why: r.why}
		switch r.id {
		case "merge":
			row.On = merge
		case "dry-run":
			row.On = dry
		}
		row.Edit = v.Edit && r.why == ""
		if !host {
			row.Why = ""
		}
		if host && h != nil && r.f != 0 && row.On {
			for _, p := range h.Peers {
				if p.Host || p.State != "bound" {
					continue
				}
				if !nameSet(p.Features)[r.id] {
					row.Missing = append(row.Missing, mk.text(p.Name))
				}
			}
			switch {
			case len(row.Missing) == 0:
			case r.f&proto.TableFeatures != 0:
				row.MissingWhy = "their stage tables differ from yours, or their game runs a dry run"
			default:
				row.MissingWhy = "their game runs a dry run"
			}
		}
		v.Rows = append(v.Rows, row)
	}
	if on["keys"] && !on["switches"] {
		v.Note = sharesNote
	}
	return v
}

// nameSet is a feature list ("image|story", proto.Feature.String) as a
// set.
func nameSet(s string) map[string]bool {
	m := map[string]bool{}
	for _, n := range strings.Split(s, "|") {
		if n = strings.TrimSpace(n); n != "" && n != "none" {
			m[n] = true
		}
	}
	return m
}

// SetCoopShares changes what co-op shares while the session runs (host):
// on is every rule turned on by ID (the World itself always stays). Each
// player's game follows at its next tick. The choice is saved for the
// next hosted co-op session (config.Coop.SharesOff), but for Live
// switches, which stays the lobby's Advanced choice at Start.
func (b *Bridge) SetCoopShares(on []string) (err error) {
	defer b.maskErr(&err)
	var feats []string
	for _, id := range on {
		if id == "image" {
			continue
		}
		feats = append(feats, id)
	}
	f, err := proto.ParseShares(feats)
	if err != nil {
		return err
	}
	if err := b.ctrl.SetCoopShares(feats); err != nil {
		return err
	}
	var off []string
	for _, r := range coopRules {
		if r.f != 0 && r.f&proto.Shareable != 0 && r.f != proto.FeatureLiveSwitches && f&r.f == 0 {
			off = append(off, r.id)
		}
	}
	b.mu.Lock()
	b.cfg.Coop.SharesOff = strings.Join(off, ",")
	b.mu.Unlock()
	b.save()
	return nil
}

// CoopRoomView is the Room items card: what the session's world holds
// (session.CoopRoom), as every player's game takes it.
type CoopRoomView struct {
	Items  []CoopRoomItemView `json:"items"`
	Equip  []CoopRoomItemView `json:"equip"`
	Songs  []CoopRoomItemView `json:"songs"`
	Pearls []CoopRoomItemView `json:"pearls"`
	Shards int                `json:"shards"`
	// Charts of ChartsTotal is the game's own count; Salvaged their
	// sunken treasure up; TriforceCharts the deciphered Triforce charts.
	Charts         int `json:"charts"`
	ChartsTotal    int `json:"chartsTotal"`
	Salvaged       int `json:"salvaged"`
	TriforceCharts int `json:"triforceCharts"`
	// Wallet is the rupee cap; Quiver and BombBag the capacities; Magic
	// the max magic (0, 16 or 32).
	Wallet  int `json:"wallet"`
	Quiver  int `json:"quiver"`
	BombBag int `json:"bombBag"`
	Magic   int `json:"magic"`
	// Hearts: the heart sources were counted (HeartsWhy: why not).
	// MaxHearts is the max life they give in whole hearts, with
	// MaxQuarters the quarter hearts past it.
	Hearts          bool   `json:"hearts"`
	HeartsWhy       string `json:"heartsWhy,omitempty"`
	Containers      int    `json:"containers"`
	ContainersTaken int    `json:"containersTaken"`
	Pieces          int    `json:"pieces"`
	PiecesTaken     int    `json:"piecesTaken"`
	Rewards         int    `json:"rewards"`
	Given           int    `json:"given"`
	Untracked       int    `json:"untracked"`
	MaxHearts       int    `json:"maxHearts"`
	MaxQuarters     int    `json:"maxQuarters"`
}

// CoopRoomItemView is one slot, song or pearl.
type CoopRoomItemView struct {
	Slot string `json:"slot,omitempty"`
	Name string `json:"name"`
	Have bool   `json:"have"`
}

// roomView is r as the card shows it; nil for nil.
func roomView(r *session.CoopRoom) *CoopRoomView {
	if r == nil {
		return nil
	}
	items := func(xs []session.CoopRoomItem) []CoopRoomItemView {
		out := make([]CoopRoomItemView, 0, len(xs))
		for _, x := range xs {
			out = append(out, CoopRoomItemView{Slot: x.Slot, Name: x.Name, Have: x.Have})
		}
		return out
	}
	return &CoopRoomView{
		Items: items(r.Items), Equip: items(r.Equip), Songs: items(r.Songs), Pearls: items(r.Pearls), Shards: r.Shards,
		Charts: r.Charts, ChartsTotal: r.ChartsTotal, Salvaged: r.Salvaged, TriforceCharts: r.TriforceCharts,
		Wallet: r.Wallet, Quiver: r.Quiver, BombBag: r.BombBag, Magic: r.Magic,
		Hearts: r.Hearts, HeartsWhy: r.HeartsWhy, Containers: r.Containers, ContainersTaken: r.ContainersTaken,
		Pieces: r.Pieces, PiecesTaken: r.PiecesTaken, Rewards: r.Rewards, Given: r.Given, Untracked: r.Untracked,
		MaxHearts: r.MaxLife / 4, MaxQuarters: r.MaxLife % 4,
	}
}

// memberCoop is a Players row's co-op facts once co-op runs: the chip
// ("Bound · DRC room 3": the host's hub knows every peer; a joiner knows
// only where its partner is) and whether Warp to works from that row (it
// is the one other player: co-op v1.0's warp goes to the partner, so
// there is no one else to pick).
func memberCoop(st session.State, name string, self bool, others int) (chip string, warp bool) {
	if !st.Coop.On {
		return "", false
	}
	if h := st.CoopHub; h != nil {
		i := slices.IndexFunc(h.Peers, func(p session.CoopPeer) bool { return p.Name == name })
		if i >= 0 && h.Peers[i].State != "" {
			p := h.Peers[i]
			chip = strings.ToUpper(p.State[:1]) + p.State[1:]
			if p.Stage != "" {
				chip += fmt.Sprintf(" · %s room %d", p.Stage, p.Room)
			}
		}
	} else if !self && others == 1 && len(st.CoopStatus) > 0 && st.CoopStatus[0].PartnerStage != "" {
		cs := st.CoopStatus[0]
		chip = fmt.Sprintf("%s room %d", cs.PartnerStage, cs.PartnerRoom)
	}
	return chip, !self && others == 1 && len(st.CoopStatus) > 0
}
