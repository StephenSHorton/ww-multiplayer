package network

import (
	"bytes"
	"errors"
	"reflect"
	"strings"
	"testing"
)

// lobbyBodyFor returns a fresh pointer to sub's body type, nil for an
// unknown sub.
func lobbyBodyFor(sub byte) any {
	switch sub {
	case LobbyHello:
		return &LobbyHelloBody{}
	case LobbyWelcome:
		return &LobbyWelcomeBody{}
	case LobbyState:
		return &LobbyStateBody{}
	case LobbySet:
		return &LobbySetBody{}
	case LobbySettings:
		return &LobbySettingsBody{}
	case LobbyStart:
		return &LobbyStartBody{}
	case LobbyGo:
		return &LobbyGoBody{}
	case LobbyRequest:
		return &LobbyRequestBody{}
	case LobbyKick:
		return &LobbyKickBody{}
	case LobbyNotice:
		return &LobbyNoticeBody{}
	}
	return nil
}

func TestLobbyCodecRoundTrip(t *testing.T) {
	ready, color := true, "#D02C2C"
	bodies := map[byte]any{
		LobbyHello:   &LobbyHelloBody{Token: "ab", App: "v0.4.0", OS: "windows", Color: "#D02C2C", Dolphin: DolphinNone, ModsHash: "c47ea77738fba9b096423d844491cffe8c8a2d96575b0355fa2f091d2c1296c1"},
		LobbyWelcome: &LobbyWelcomeBody{Member: 3, Token: "ff", Host: true, Phase: PhaseLobby, Features: []string{"chat"}},
		LobbyState: &LobbyStateBody{Phase: PhaseStarting, Rev: 9, Settings: LobbySettingsBody{Coop: CoopNew, Merge: true},
			Members: []LobbyMember{{ID: 1, Name: "Ann", Color: "#3C9A2E", Ready: true, Host: true, Via: ViaApp}, {ID: cliMemberBase + 4, Name: "Cli", Color: "#D02C2C", Ready: true, Via: ViaCLI}}},
		LobbySet:      &LobbySetBody{Color: &color, Ready: &ready},
		LobbySettings: &LobbySettingsBody{Coop: "0123456789abcdef", DryRun: true, Worlds: []LobbyWorld{{ID: "0123456789abcdef", Seq: 4}}},
		LobbyStart:    &LobbyStartBody{Settings: LobbySettingsBody{Coop: CoopOff}},
		LobbyGo:       &LobbyGoBody{World: "0123456789abcdef"},
		LobbyRequest:  &LobbyRequestBody{Kind: "merge", From: "Bo"},
		LobbyKick:     &LobbyKickBody{Member: 2},
		LobbyNotice:   &LobbyNoticeBody{Code: NoticeNameTaken, Text: "taken", Suggest: "Ann (2)"},
	}
	for sub, body := range bodies {
		data, err := EncodeLobby(sub, body)
		if err != nil {
			t.Fatalf("%s: encode: %v", LobbySubName(sub), err)
		}
		gotSub, raw, err := ParseLobby(data)
		if err != nil || gotSub != sub {
			t.Fatalf("%s: ParseLobby = %d, %v", LobbySubName(sub), gotSub, err)
		}
		back := lobbyBodyFor(sub)
		if err := DecodeLobby(raw, back); err != nil {
			t.Fatalf("%s: decode: %v", LobbySubName(sub), err)
		}
		if !reflect.DeepEqual(back, body) {
			t.Errorf("%s: round trip = %+v, want %+v", LobbySubName(sub), back, body)
		}
	}
}

// TestLobbyGoldenBytes pins the wire form: sub byte, then compact JSON
// with these field names. A GUI and a server of the same release must
// agree; a change here is a wire change.
func TestLobbyGoldenBytes(t *testing.T) {
	ready := true
	cases := []struct {
		sub  byte
		body any
		want string
	}{
		{LobbySet, LobbySetBody{Ready: &ready}, "\x04{\"ready\":true}"},
		{LobbyGo, LobbyGoBody{}, "\x08{}"},
		{LobbyKick, LobbyKickBody{Member: 7}, "\x0a{\"member\":7}"},
		{LobbyNotice, LobbyNoticeBody{Code: "x", Text: "<a&b>"}, "\x0b{\"code\":\"x\",\"text\":\"<a&b>\"}"},
		{LobbySettings, LobbySettingsBody{Coop: CoopOff}, "\x06{\"coop\":\"off\",\"dryRun\":false,\"merge\":false,\"liveSwitches\":false}"},
	}
	for _, c := range cases {
		got, err := EncodeLobby(c.sub, c.body)
		if err != nil || string(got) != c.want {
			t.Errorf("EncodeLobby(%s) = %q, %v; want %q", LobbySubName(c.sub), got, err, c.want)
		}
	}
	if MsgLobby != 'B' || RoleLobby != 2 || LobbyCapJSON != 1 {
		t.Fatalf("MsgLobby %q RoleLobby %d LobbyCapJSON %d, want 'B', 2, 1", MsgLobby, RoleLobby, LobbyCapJSON)
	}
}

func TestLobbyCodecRefusals(t *testing.T) {
	if _, _, err := ParseLobby(nil); !errors.Is(err, ErrLobbyEmpty) {
		t.Errorf("ParseLobby(nil) = %v, want ErrLobbyEmpty", err)
	}
	big := append([]byte{LobbySet}, bytes.Repeat([]byte(" "), MaxLobbyBody+1)...)
	if _, _, err := ParseLobby(big); !errors.Is(err, ErrLobbyTooLarge) {
		t.Errorf("ParseLobby(oversize) = %v, want ErrLobbyTooLarge", err)
	}
	if _, err := EncodeLobby(LobbyNotice, LobbyNoticeBody{Text: strings.Repeat("x", MaxLobbyBody)}); !errors.Is(err, ErrLobbyTooLarge) {
		t.Errorf("EncodeLobby(oversize) = %v, want ErrLobbyTooLarge", err)
	}
	var b LobbySetBody
	for _, bad := range []string{"[]", "1", `"x"`, "{", `{"ready":"yes"}`, `{} {}`, `{}}`, "null"} {
		if err := DecodeLobby([]byte(bad), &b); err == nil {
			t.Errorf("DecodeLobby(%q) accepted it", bad)
		}
	}
	for _, ok := range []string{"", " ", "{}", ` {"ready":true,"future":[1,2]} `} {
		if err := DecodeLobby([]byte(ok), &b); err != nil {
			t.Errorf("DecodeLobby(%q) = %v, want ok (unknown fields ignored)", ok, err)
		}
	}
}

// FuzzLobby: the 'B' codec. ParseLobby and DecodeLobby never panic, and
// whatever decodes re-encodes to a payload that parses and decodes to the
// same value (decode is a projection: unknown fields drop, what is kept
// is stable).
func FuzzLobby(f *testing.F) {
	for _, s := range []string{
		"\x01{\"token\":\"ab\",\"color\":\"red\"}", "\x01{\"app\":\"v0.4.0\",\"modsHash\":\"c47ea77738fba9b096423d844491cffe8c8a2d96575b0355fa2f091d2c1296c1\"}", "\x01{\"modsHash\":7}",
		"\x03{\"phase\":\"lobby\",\"rev\":1,\"members\":[{\"id\":1,\"name\":\"a\"}]}",
		"\x04{\"color\":\"#FF0000\",\"ready\":true}", "\x05{\"granted\":null,\"wanted\":\"#000000\"}", "\x07{}", "\x0b{\"code\":\"x\"}",
		"", "\x04", "\x04[1]", "\xff{}", "\x06{\"coop\":\"new\",\"worlds\":[{\"id\":\"x\",\"seq\":1e30}]}",
	} {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		sub, body, err := ParseLobby(data)
		if err != nil {
			return
		}
		v := lobbyBodyFor(sub)
		if v == nil {
			return
		}
		if DecodeLobby(body, v) != nil {
			return
		}
		enc, err := EncodeLobby(sub, v)
		if err != nil {
			if errors.Is(err, ErrLobbyTooLarge) {
				return // escaping can grow a body near the cap
			}
			t.Fatalf("re-encode of %+v: %v", v, err)
		}
		sub2, body2, err := ParseLobby(enc)
		if err != nil || sub2 != sub {
			t.Fatalf("re-encoded payload: sub %d err %v", sub2, err)
		}
		w := lobbyBodyFor(sub)
		if err := DecodeLobby(body2, w); err != nil {
			t.Fatalf("re-encoded %q does not decode: %v", body2, err)
		}
		enc2, _ := EncodeLobby(sub, w)
		if !bytes.Equal(enc, enc2) {
			t.Fatalf("not stable: %q then %q", enc, enc2)
		}
	})
}
