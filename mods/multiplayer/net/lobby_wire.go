package network

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
)

// Lobby wire format (docs/10-lobby.md, app design §3).
//
// A lobby connection is a third connection role, RoleLobby, in the
// protocol-2 hello: MsgJoin(name), then MsgHello [version:2][role=2][caps:2]
// with LobbyCapJSON set in caps. A server from before the lobby refuses role
// 2 with RejectRole ("unknown connection role 2"), and the app then joins
// directly as before; old clients never send it. So the lobby needs no
// ProtocolVersion bump.
//
// After the welcome, both sides exchange MsgLobby ('B') frames:
//
//	[sub:1][body: JSON object, UTF-8, at most MaxLobbyBody bytes]
//
// Unknown JSON fields are ignored, so either side can add fields later. An
// unknown sub-message is ignored too. A lobby connection also carries
// MsgChat (relayed like a player's) and the heartbeat (MsgPing/MsgPong).

// MsgLobby is a lobby sub-message, on a lobby connection only.
const MsgLobby byte = 'B'

// LobbyCapJSON is caps bit 0 of a lobby hello: the bodies are JSON. A
// lobby hello without it is refused with RejectRole. The other bits are
// reserved and sent as 0.
const LobbyCapJSON uint16 = 1 << 0

// CapModsHash is caps bit 0 of a player hello (RolePlayer): the hello
// carries the color block and then the 32-byte mods hash (handshake.go,
// docs/api/02-mods.md). Caps mean something per role, so it shares its
// bit with LobbyCapJSON; it means nothing in any other role's hello.
const CapModsHash uint16 = 1 << 0

// MaxLobbyBody caps a lobby sub-message's JSON body. The biggest is STATE,
// a few hundred bytes per member.
const MaxLobbyBody = 16 << 10

// Lobby sub-messages, the first byte of a MsgLobby payload.
const (
	LobbyHello   byte = 0x01 // c->s: first frame after the welcome (LobbyHelloBody)
	LobbyWelcome byte = 0x02 // s->c: the member slot (LobbyWelcomeBody)
	LobbyState   byte = 0x03 // s->c: full snapshot after every change (LobbyStateBody)
	LobbySet     byte = 0x04 // c->s: member updates (LobbySetBody)
	// 0x05 was COLOR (a color request not granted as asked). Colors may be
	// shared, so every request is granted and it is no longer sent; the
	// value stays unused.
	LobbySettings byte = 0x06 // c->s, host only: co-op settings (LobbySettingsBody)
	LobbyStart    byte = 0x07 // c->s host, then s->c all: LOBBY -> STARTING (LobbyStartBody)
	LobbyGo       byte = 0x08 // c->s host, then s->c all: STARTING -> IN_GAME (LobbyGoBody)
	LobbyRequest  byte = 0x09 // c->s, then s->host: a joiner asks the host (LobbyRequestBody)
	LobbyKick     byte = 0x0A // c->s host: remove a member (LobbyKickBody)
	LobbyNotice   byte = 0x0B // s->c: a refusal or a notice (LobbyNoticeBody)
)

// lobbySubNames names the sub-messages for logs and errors.
var lobbySubNames = map[byte]string{
	LobbyHello: "HELLO", LobbyWelcome: "WELCOME", LobbyState: "STATE", LobbySet: "SET",
	LobbySettings: "SETTINGS", LobbyStart: "START", LobbyGo: "GO",
	LobbyRequest: "REQUEST", LobbyKick: "KICK", LobbyNotice: "NOTICE",
}

// LobbySubName is sub's name ("STATE"), or its hex form when unknown.
func LobbySubName(sub byte) string {
	if n, ok := lobbySubNames[sub]; ok {
		return n
	}
	return fmt.Sprintf("0x%02X", sub)
}

// Lobby phases, LobbyStateBody.Phase. A session moves forward only:
// lobby -> starting (the host's START) -> in_game (the host's GO). It goes
// back to lobby only when the lobby empties.
const (
	PhaseLobby    = "lobby"
	PhaseStarting = "starting"
	PhaseInGame   = "in_game"
)

// Member Via values: how a member takes part.
const (
	ViaApp = "app" // a lobby connection (the desktop app)
	ViaCLI = "cli" // a player connection with no lobby member of its name (CLI join)
)

// Dolphin values a member reports (LobbySetBody.Dolphin). Free-form on the
// wire; these are the ones the app sends.
const (
	DolphinNone      = "none"
	DolphinLaunching = "launching"
	DolphinTitle     = "title"
	DolphinInGame    = "in_game"
)

// Notice codes (LobbyNoticeBody.Code).
const (
	NoticeNameTaken      = "name_taken"      // the name is a member's already; the connection closes
	NoticeNotHost        = "not_host"        // a host-only sub-message from a non-host
	NoticeWrongPhase     = "wrong_phase"     // START outside lobby, GO outside starting
	NoticeColorsLocked   = "colors_locked"   // a color change after Start (v1)
	NoticeSettingsLocked = "settings_locked" // a settings change other than merge after Start
	NoticeBadMessage     = "bad_message"     // a body that doesn't decode or a bad value
	NoticeNoMember       = "no_member"       // KICK of an unknown member
	NoticeKicked         = "kicked"          // the host removed you; the connection closes
	NoticeHelloFirst     = "hello_first"     // anything before HELLO
	NoticeSlowDown       = "slow_down"       // over the lobby's message rate; the excess was dropped
	NoticeModsDiffer     = "mods_differ"     // the HELLO's ModsHash is not the host's; the connection closes
)

// Co-op modes, LobbySettingsBody.Coop: "off", "new", or a world ID.
const (
	CoopOff = "off"
	CoopNew = "new"
)

// LobbySettingsBody is the host's co-op settings (SETTINGS, and inside
// STATE and START). Coop is CoopOff, CoopNew or a world ID.
type LobbySettingsBody struct {
	Coop         string `json:"coop"`
	DryRun       bool   `json:"dryRun"`
	Merge        bool   `json:"merge"`
	LiveSwitches bool   `json:"liveSwitches"`
	// Worlds is the host's world list, a summary for the joiners' view;
	// only on SETTINGS from the host and in STATE.
	Worlds []LobbyWorld `json:"worlds,omitempty"`
}

// LobbyWorld is one of the host's co-op worlds, as STATE lists it.
type LobbyWorld struct {
	ID    string `json:"id"`
	Seq   uint64 `json:"seq"`
	InUse bool   `json:"inUse,omitempty"`
}

// LobbyHelloBody is HELLO. Token, from an earlier WELCOME, reclaims a
// member slot the server still holds.
type LobbyHelloBody struct {
	Token   string `json:"token,omitempty"`
	App     string `json:"app,omitempty"`
	OS      string `json:"os,omitempty"`
	Color   string `json:"color,omitempty"` // "#RRGGBB" or a palette name
	Dolphin string `json:"dolphin,omitempty"`
	// ModsHash is the joiner's mods hash in lowercase hex (ModsHashHex):
	// a host whose own differs refuses the HELLO with NoticeModsDiffer.
	// Empty from a build before mods, which is admitted as before; a host
	// from before mods ignores it.
	ModsHash string `json:"modsHash,omitempty"`
}

// LobbyWelcomeBody is WELCOME.
type LobbyWelcomeBody struct {
	Member   int      `json:"member"`
	Token    string   `json:"token"`
	Host     bool     `json:"host"`
	Phase    string   `json:"phase"`
	Features []string `json:"features,omitempty"`
}

// LobbyMember is one row of STATE.
type LobbyMember struct {
	ID      int    `json:"id"`
	Name    string `json:"name"`
	Color   string `json:"color"` // "#RRGGBB"
	Ready   bool   `json:"ready"`
	Host    bool   `json:"host,omitempty"`
	Dolphin string `json:"dolphin,omitempty"`
	File    string `json:"file,omitempty"`
	RTTMs   int    `json:"rttMs,omitempty"`
	Via     string `json:"via"`
	// Away: the member's lobby connection dropped and the server holds
	// its slot for its token (LobbyHoldTimeout).
	Away bool `json:"away,omitempty"`
}

// LobbyStateBody is STATE: the whole lobby, sent to every member after
// every change. Rev grows with each change, so a client can drop an
// out-of-date snapshot.
type LobbyStateBody struct {
	Phase    string            `json:"phase"`
	Rev      uint64            `json:"rev"`
	Settings LobbySettingsBody `json:"settings"`
	Members  []LobbyMember     `json:"members"`
}

// LobbySetBody is SET; a nil field is left as it is.
type LobbySetBody struct {
	Color   *string `json:"color,omitempty"`
	Ready   *bool   `json:"ready,omitempty"`
	Dolphin *string `json:"dolphin,omitempty"`
	File    *string `json:"file,omitempty"`
}

// LobbyStartBody is START.
type LobbyStartBody struct {
	Settings LobbySettingsBody `json:"settings"`
}

// LobbyGoBody is GO. World is the co-op world the host serves, if any.
type LobbyGoBody struct {
	World string `json:"world,omitempty"`
}

// LobbyRequestBody is REQUEST; the server fills From.
type LobbyRequestBody struct {
	Kind string `json:"kind"` // "merge"
	From string `json:"from,omitempty"`
}

// LobbyKickBody is KICK.
type LobbyKickBody struct {
	Member int `json:"member"`
}

// LobbyNoticeBody is NOTICE. Suggest is a free name for NoticeNameTaken.
type LobbyNoticeBody struct {
	Code    string `json:"code"`
	Text    string `json:"text"`
	Suggest string `json:"suggest,omitempty"`
}

// Errors from the lobby codec.
var (
	ErrLobbyEmpty    = errors.New("lobby: empty payload")
	ErrLobbyTooLarge = errors.New("lobby: body too large")
)

// EncodeLobby builds a MsgLobby payload: sub, then body as JSON. A body
// that encodes to more than MaxLobbyBody bytes is refused.
func EncodeLobby(sub byte, body any) ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteByte(sub)
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(body); err != nil {
		return nil, fmt.Errorf("lobby %s: %w", LobbySubName(sub), err)
	}
	out := bytes.TrimSuffix(buf.Bytes(), []byte("\n"))
	if len(out)-1 > MaxLobbyBody {
		return nil, fmt.Errorf("%w: %s is %d bytes (max %d)", ErrLobbyTooLarge, LobbySubName(sub), len(out)-1, MaxLobbyBody)
	}
	return out, nil
}

// ParseLobby splits a MsgLobby payload into its sub-message and body. It
// checks only the size; DecodeLobby reads the body.
func ParseLobby(data []byte) (sub byte, body []byte, err error) {
	if len(data) < 1 {
		return 0, nil, ErrLobbyEmpty
	}
	if len(data)-1 > MaxLobbyBody {
		return 0, nil, fmt.Errorf("%w: %d bytes (max %d)", ErrLobbyTooLarge, len(data)-1, MaxLobbyBody)
	}
	return data[0], data[1:], nil
}

// DecodeLobby decodes a lobby body into v (a pointer to one of the Lobby*Body
// types). The body must be one JSON object; unknown fields are ignored. An
// empty body decodes as {}.
func DecodeLobby(body []byte, v any) error {
	if len(bytes.TrimSpace(body)) == 0 {
		body = []byte("{}")
	}
	if len(body) > MaxLobbyBody {
		return fmt.Errorf("%w: %d bytes (max %d)", ErrLobbyTooLarge, len(body), MaxLobbyBody)
	}
	t := bytes.TrimSpace(body)
	if len(t) == 0 || t[0] != '{' {
		return errors.New("lobby: body is not a JSON object")
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("lobby: %w", err)
	}
	if rest := bytes.TrimSpace(body[dec.InputOffset():]); len(rest) > 0 {
		return errors.New("lobby: data after the JSON object")
	}
	return nil
}
