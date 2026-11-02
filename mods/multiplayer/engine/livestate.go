package engine

import (
	"strconv"
	"sync"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/internal/session"
	"github.com/StephenSHorton/ww-multiplayer/mods/multiplayer/feed"
	"github.com/StephenSHorton/ww-multiplayer/mods/multiplayer/net"
	"github.com/StephenSHorton/ww-multiplayer/mods/multiplayer/vissync"
)

// LiveState is the mutable, mutex-guarded bridge between a running multiplayer
// session (the *network.Server / *network.Client that main owns) and the
// session controller the TUI drives. internal/tui and internal/session never
// import mods/multiplayer/net; instead Engine reads this holder under the
// lock and hands the controller plain values / session.Player.
//
// One LiveState is created in main() before tui.Run and shared across every
// session the user starts (Host -> Esc -> Join, etc.). runHostSession /
// runJoinSession call begin() at start and reset() on return, so when no
// session is live every accessor returns a zero value (which the panel renders
// as 0 / "—" / "waiting").
//
// Concurrency: mu guards the role/selfName/client/srv/hostIPs fields only. The
// network methods it calls (GetRemotePlayers, PlayerCount, LastRTT) are
// internally synchronized and GetRemotePlayers returns a deep copy, so the
// accessors snapshot the pointer under mu, release it, then call the network
// method — never holding a *network.RemotePlayer.
type LiveState struct {
	mu       sync.Mutex
	role     string          // "Host", "Join", or "" when idle
	selfName string          // this player's display name (for self-echo filtering)
	client   *network.Client // puppet-sync client (remote players + RTT)
	srv      *network.Server // host-only; nil on join / idle
	hostIPs  []string        // host-only shareable LAN IPs; nil otherwise

	// localX/Y/Z + hasLocalPos back session.Live's LocalX/Y/Z (#22 minimap): the
	// broadcaster's own world position, refreshed every broadcast-pose tick
	// via setLocalPos. hasLocalPos is false until the first tick lands (or
	// forever, on CLI paths that never wire a LiveState at all).
	localX, localY, localZ float32
	hasLocalPos            bool
	// localVitals + hasLocalVitals back session.Live's LocalVitals
	// (wind#55), set via SetLocalVitals.
	localVitals    vissync.Vitals
	hasLocalVitals bool
	// localPlace/hasLocalPlace back session.Live's LocalStage/Room/InPlay:
	// this game's place, read with the position (feed.ReadPlace).
	localPlace    network.Place
	hasLocalPlace bool

	// now is the clock remote places are aged by (time.Now when nil).
	now func() time.Time

	// chat receives incoming chat lines from the puppet-sync client's OnChat
	// callback (#20): Engine points it at the running session's
	// session.Output, whose Chat never blocks the client read loop. nil
	// drops the lines (no session, or CLI paths).
	chat func(from, text string)
}

// Begin marks the start of a session with the given role + local player name,
// resetting any prior client/server registration. hostIPs is nil for the join
// role. selfName is used to filter this player's OWN co-located connections out
// of the remote list (see remoteViews).
func (ls *LiveState) Begin(role, selfName string, hostIPs []string) {
	ls.mu.Lock()
	defer ls.mu.Unlock()
	ls.role = role
	ls.selfName = selfName
	ls.client = nil
	ls.srv = nil
	ls.hostIPs = hostIPs
	ls.localX, ls.localY, ls.localZ = 0, 0, 0
	ls.hasLocalPos = false
	ls.localVitals, ls.hasLocalVitals = vissync.Vitals{}, false
	ls.localPlace, ls.hasLocalPlace = network.Place{}, false
}

// SetChat points incoming chat at sink (nil drops it). begin() leaves it
// alone: Engine sets it around the whole session call.
func (ls *LiveState) SetChat(sink func(from, text string)) {
	ls.mu.Lock()
	ls.chat = sink
	ls.mu.Unlock()
}

// PushChat delivers one incoming chat line to the current sink, if any.
func (ls *LiveState) PushChat(from, text string) {
	ls.mu.Lock()
	sink := ls.chat
	ls.mu.Unlock()
	if sink != nil {
		sink(from, text)
	}
}

// SendChat forwards a chat line through the registered puppet-sync client.
// Nil-safe: a no-op (nil error) when no client is registered yet (e.g. the user
// types before the session has connected). Client.SendChat already bounds the
// write with the shared write deadline, so no extra timeout wrapping is needed.
func (ls *LiveState) SendChat(text string) error {
	ls.mu.Lock()
	c := ls.client
	ls.mu.Unlock()
	if c == nil {
		return nil
	}
	return c.SendChat(text)
}

// Reset clears the bridge when a session ends.
func (ls *LiveState) Reset() { ls.Begin("", "", nil) }

func (ls *LiveState) SetServer(s *network.Server) {
	ls.mu.Lock()
	ls.srv = s
	ls.mu.Unlock()
}

func (ls *LiveState) SetClient(c *network.Client) {
	ls.mu.Lock()
	ls.client = c
	ls.mu.Unlock()
}

// SetLocalPos records this player's own latest world position (#22
// minimap). Called from feed.BroadcastPose's send loop every tick it
// successfully reads Link's position — see the onPos callback threaded
// through runMultiplayerGoroutines, mirroring how onClient wires the
// puppet-sync *network.Client into LiveState.
func (ls *LiveState) SetLocalPos(x, y, z float32, place network.Place, placeOK bool) {
	ls.mu.Lock()
	ls.localX, ls.localY, ls.localZ = x, y, z
	ls.hasLocalPos = true
	ls.localPlace, ls.hasLocalPlace = place, placeOK && place.Stage != ""
	ls.mu.Unlock()
}

// SetLocalVitals records this player's own latest hearts, magic and
// rupees (wind#55): feed.BroadcastPose's onVitals, about four times a
// second. ok false (a failed read, no file loaded) clears them.
func (ls *LiveState) SetLocalVitals(v vissync.Vitals, ok bool) {
	ls.mu.Lock()
	ls.localVitals, ls.hasLocalVitals = v, ok
	ls.mu.Unlock()
}

// LocalVitals is the last SetLocalVitals; ok is false before one, or
// after one that couldn't read them.
func (ls *LiveState) LocalVitals() (v session.Vitals, ok bool) {
	ls.mu.Lock()
	defer ls.mu.Unlock()
	return sessionVitals(ls.localVitals), ls.hasLocalVitals
}

// sessionVitals is v as the session's network-free type.
func sessionVitals(v vissync.Vitals) session.Vitals {
	return session.Vitals{Life: v.Life, MaxLife: v.MaxLife, Magic: v.Magic, MaxMagic: v.MaxMagic, Rupees: v.Rupees}
}

// LocalPlace reports this game's own place; ok is false until the first
// broadcast-pose tick read one, and while the game is between stages.
func (ls *LiveState) LocalPlace() (session.Place, bool) {
	ls.mu.Lock()
	defer ls.mu.Unlock()
	if !ls.hasLocalPlace {
		return session.Place{}, false
	}
	p := ls.localPlace
	return session.Place{Stage: p.Stage, Room: int(p.Room), InPlay: p.InPlay}, true
}

func (ls *LiveState) clock() time.Time {
	if ls.now != nil {
		return ls.now()
	}
	return time.Now()
}

// Players returns the OTHER humans in the session, one entry per person, as
// network-free session.Player values. Returns nil when no client is registered
// yet. The raw client snapshot is de-duplicated (see remoteViews +
// dedupePlayersByName) so the ~2x connection fan-out doesn't leak into the UI.
func (ls *LiveState) Players() []session.Player {
	ls.mu.Lock()
	c, self := ls.client, ls.selfName
	ls.mu.Unlock()
	return dedupePlayersByName(remoteViews(c, self, ls.clock()))
}

// PlayerCount reports the truthful HUMAN count for BOTH roles. Whenever a
// puppet-sync client is registered, it's the de-duplicated other-humans count
// + 1 (self) — which stays correct even though each human opens two server
// connections. It falls back to the server's raw connection count only before
// the host's own client has registered (a brief startup window). 0 when idle.
func (ls *LiveState) PlayerCount() int {
	ls.mu.Lock()
	c, s, self := ls.client, ls.srv, ls.selfName
	ls.mu.Unlock()
	if c != nil {
		return len(dedupePlayersByName(remoteViews(c, self, time.Time{}))) + 1
	}
	if s != nil {
		return s.PlayerCount()
	}
	return 0
}

// remoteViews snapshots the puppet-sync client's remotes and converts them to
// network-free session.Player values, dropping this player's OWN co-located
// connections. Each human runs two connections (broadcast-pose + puppet-sync)
// under the same name; the puppet-sync client that backs the TUI therefore also
// sees its own broadcast twin as a same-named remote (GetRemotePlayers does NOT
// self-filter — the render loop does, via selfFilter). Dropping name==selfName
// here mirrors that render-side filter so the count/presence reflect real
// people. Returns nil when no client is registered. Position is nil for
// connections that haven't broadcast yet (e.g. a remote's own puppet leg),
// which dedupePlayersByName then collapses into the positioned twin.
func remoteViews(c *network.Client, selfName string, now time.Time) []session.Player {
	if c == nil {
		return nil
	}
	return playerViews(c.GetRemotePlayers(), selfName, now) // deep copy under the client's RLock
}

// playerViews converts remotes to session.Player values (remoteViews, which
// see), with each one's place as of now: its last place trailer, its age,
// and whether it is still believed (RemotePlayer.PlaceFresh). A zero now
// skips the places (PlayerCount needs none).
func playerViews(remotes []network.RemotePlayer, selfName string, now time.Time) []session.Player {
	out := make([]session.Player, 0, len(remotes))
	for i := range remotes {
		rp := &remotes[i]
		if selfName != "" && rp.Name == selfName {
			continue // our own broadcast/puppet twin — not another human
		}
		pv := playerView(*rp)
		if rp.HasPlace && !now.IsZero() {
			pv.Place = session.Place{Stage: rp.Place.Stage, Room: int(rp.Place.Room), InPlay: rp.Place.InPlay}
			pv.HasPlace = true
			pv.Fresh = rp.PlaceFresh(now)
			if !rp.PlaceAt.IsZero() && now.After(rp.PlaceAt) {
				pv.PlaceAge = now.Sub(rp.PlaceAt)
			}
		}
		out = append(out, pv)
	}
	return out
}

// playerView is one remote as a network-free session.Player: its tunic
// color, its position once it has broadcast one, and its vitals once its
// pose trailer carries them (wind#55).
func playerView(rp network.RemotePlayer) session.Player {
	pv := session.Player{ID: rp.ID, Name: rp.Name, Color: rp.Color, HasColor: rp.HasColor}
	if v, ok := vissync.DecodeVitals(rp.FaceState); ok {
		pv.Vitals, pv.HasVitals = sessionVitals(v), true
	}
	if rp.Position != nil {
		pv.X, pv.Y, pv.Z = rp.Position.PosX, rp.Position.PosY, rp.Position.PosZ
	}
	return pv
}

// dedupePlayersByName collapses the duplicate remote-player entries the network
// layer produces: every human opens TWO server connections (broadcast-pose +
// puppet-sync) under the SAME name, so both land in the server player list and
// in GetRemotePlayers. Entries are keyed by Name (falling back to a per-ID key
// when Name==""), so each human appears exactly once. When several entries
// share a key, the one carrying a known (non-zero) position wins — the
// broadcast connection carries the real pose, which PR-C's minimap needs — else
// the first seen is kept. Input order is otherwise preserved. Pure: no shared
// state, safe to unit-test with canned input.
func dedupePlayersByName(in []session.Player) []session.Player {
	if len(in) == 0 {
		return nil
	}
	out := make([]session.Player, 0, len(in))
	pos := make(map[string]int, len(in)) // key -> index into out
	for _, p := range in {
		key := p.Name
		if key == "" {
			key = "\x00id:" + strconv.Itoa(int(p.ID))
		}
		if i, ok := pos[key]; ok {
			// Same human already recorded: upgrade to the positioned entry when
			// the one we kept has no known position but this one does.
			if isZeroPos(out[i]) && !isZeroPos(p) {
				if !p.HasPlace && out[i].HasPlace {
					p.Place, p.HasPlace, p.Fresh, p.PlaceAge = out[i].Place, true, out[i].Fresh, out[i].PlaceAge
				}
				out[i] = p
			} else if !out[i].HasPlace && p.HasPlace || p.HasPlace && p.Fresh && !out[i].Fresh {
				// Keep the kept entry's position, take the place the other
				// connection carries (the broadcaster's positions bring it).
				out[i].Place, out[i].HasPlace, out[i].Fresh, out[i].PlaceAge = p.Place, true, p.Fresh, p.PlaceAge
			}
			continue
		}
		pos[key] = len(out)
		out = append(out, p)
	}
	return out
}

// isZeroPos reports whether a view has no known position (all coords zero). WW
// world coords are effectively never exactly the origin, so this cleanly
// distinguishes an un-broadcast connection from a positioned one.
func isZeroPos(p session.Player) bool {
	return p.X == 0 && p.Y == 0 && p.Z == 0
}

// Latency reports the client<->server RTT, or 0 when unknown.
func (ls *LiveState) Latency() time.Duration {
	ls.mu.Lock()
	c := ls.client
	ls.mu.Unlock()
	if c == nil {
		return 0
	}
	return c.LastRTT()
}

// LocalPos reports this player's own last-known world position for the #22
// minimap's centered "@". ok is false until the first broadcast-pose tick
// has landed (or forever, when no session is live / this LiveState was
// never wired to a broadcaster).
func (ls *LiveState) LocalPos() (x, y, z float32, ok bool) {
	ls.mu.Lock()
	defer ls.mu.Unlock()
	return ls.localX, ls.localY, ls.localZ, ls.hasLocalPos
}

// HostIPList returns a copy of the host's shareable LAN IPs (nil on join/idle).
func (ls *LiveState) HostIPList() []string {
	ls.mu.Lock()
	defer ls.mu.Unlock()
	if len(ls.hostIPs) == 0 {
		return nil
	}
	out := make([]string, len(ls.hostIPs))
	copy(out, ls.hostIPs)
	return out
}

// LiveStateClientSetter adapts a *LiveState into the onClient callback
// runMultiplayerGoroutines expects, returning nil (no registration) when ls is
// nil so CLI callers stay unaffected. Besides registering the client for the
// #19 status panel, it wires the client's OnChat (BEFORE Connect — onClient is
// invoked pre-Connect in feed.PuppetSync) to hand incoming lines to the
// running session's chat sink (#20).
func LiveStateClientSetter(ls *LiveState) func(*network.Client) {
	if ls == nil {
		return nil
	}
	return func(c *network.Client) {
		c.OnChat = ls.PushChat
		ls.SetClient(c)
	}
}

// LiveStateLocalPosSetter adapts a *LiveState into the onPos callback
// runMultiplayerGoroutines / feed.BroadcastPose expect, returning nil (no
// registration) when ls is nil so CLI callers stay unaffected. Mirrors
// LiveStateClientSetter above.
func LiveStateLocalPosSetter(ls *LiveState) feed.OnPos {
	if ls == nil {
		return nil
	}
	return ls.SetLocalPos
}

// LiveStateVitalsSetter adapts a *LiveState into BroadcastPose's onVitals
// callback (wind#55), nil when ls is nil, like LiveStateLocalPosSetter.
func LiveStateVitalsSetter(ls *LiveState) func(vissync.Vitals, bool) {
	if ls == nil {
		return nil
	}
	return ls.SetLocalVitals
}

// PuppetClient is the puppet-sync client registered with the bridge.
func (ls *LiveState) PuppetClient() *network.Client {
	ls.mu.Lock()
	defer ls.mu.Unlock()
	return ls.client
}
