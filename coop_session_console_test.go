package main

import (
	"context"
	"fmt"
	"net"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/internal/coop/derive"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/hub"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/replica"
	"github.com/StephenSHorton/ww-multiplayer/mods/multiplayer/net"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mailbox"
)

func TestParseCoopCommand(t *testing.T) {
	cases := []struct {
		line string
		want coopCommand
		err  string
	}{
		{"/help", coopCommand{verb: "help"}, ""},
		{"  /?  ", coopCommand{verb: "help"}, ""},
		{"/HELP", coopCommand{verb: "help"}, ""},
		{"/refresh", coopCommand{verb: "refresh"}, ""},
		{"/warp", coopCommand{verb: "warp"}, ""},
		{"/warp Tetra", coopCommand{verb: "warp", who: "Tetra"}, ""},
		{"/Warp  Tetra ", coopCommand{verb: "warp", who: "Tetra"}, ""},
		{"/status", coopCommand{verb: "status"}, ""},
		{"/status Link", coopCommand{verb: "status", who: "Link"}, ""},
		{"/coop off", coopCommand{verb: "coop off"}, ""},
		{"/coop ON Link", coopCommand{verb: "coop on", who: "Link"}, ""},
		{"/", coopCommand{}, "an empty command"},
		{"/   ", coopCommand{}, "an empty command"},
		{"/help me", coopCommand{}, "takes nothing more"},
		{"/coop", coopCommand{}, "/coop off, or /coop on"},
		{"/coop maybe", coopCommand{}, "say /coop off or /coop on"},
		{"/warp Tetra Link", coopCommand{}, "at most a player's name"},
		{"/jump", coopCommand{}, "no command /jump"},
		{"/warp " + strings.Repeat("x", 40), coopCommand{}, "player name is 40 bytes long"},
		{"/" + strings.Repeat("warp ", 50), coopCommand{}, "-byte command"},
	}
	for _, c := range cases {
		got, err := parseCoopCommand(c.line)
		switch {
		case c.err != "":
			if err == nil || !strings.Contains(err.Error(), c.err) {
				t.Errorf("%q: %+v, %v; want an error with %q", c.line, got, err, c.err)
			}
		case err != nil || got != c.want:
			t.Errorf("%q = %+v, %v; want %+v", c.line, got, err, c.want)
		}
	}
	for _, l := range []string{"/warp", "  /help", "/"} {
		if !isCoopCommand(l) {
			t.Errorf("%q is no command", l)
		}
	}
	for _, l := range []string{"y", "n", "", " yes /warp"} {
		if isCoopCommand(l) {
			t.Errorf("answer %q taken for a command", l)
		}
	}
}

// FuzzParseCoopCommand: on any line parseCoopCommand fails or returns one
// of its verbs, for a valid player or none, and the command it returns
// parses back to itself.
func FuzzParseCoopCommand(f *testing.F) {
	for _, s := range []string{"/help", "/warp Tetra", "/coop off Link", "/coop", "/refresh\x00", "/status \t x", "/"} {
		f.Add(s)
	}
	verbs := map[string]bool{"help": true, "refresh": true, "warp": true, "status": true, "coop off": true, "coop on": true}
	f.Fuzz(func(t *testing.T, line string) {
		cmd, err := parseCoopCommand(line)
		if err != nil {
			return
		}
		if len(strings.TrimSpace(line)) > maxCommandLen {
			t.Fatalf("%q: a %d-byte command parsed", line, len(line))
		}
		if !verbs[cmd.verb] {
			t.Fatalf("%q: verb %q", line, cmd.verb)
		}
		if cmd.who != "" && (cmd.verb == "help" || network.ValidatePlayerName(cmd.who) != nil || strings.ContainsAny(cmd.who, " \t\n")) {
			t.Fatalf("%q: player %q for /%s", line, cmd.who, cmd.verb)
		}
		again := "/" + cmd.verb
		if cmd.who != "" {
			again += " " + cmd.who
		}
		if back, err := parseCoopCommand(again); err != nil || back != cmd {
			t.Fatalf("%q parsed to %+v, which parses back to %+v, %v", line, cmd, back, err)
		}
	})
}

func TestCoopConsolePick(t *testing.T) {
	one := []string{"Link"}
	two := []string{"Link", "Tetra"}
	cases := []struct {
		names []string
		cmd   coopCommand
		want  string // the names picked, or the error's text
	}{
		{nil, coopCommand{verb: "warp"}, "no Dolphin runs co-op yet"},
		{one, coopCommand{verb: "warp"}, "Link"},
		{one, coopCommand{verb: "warp", who: "link"}, "Link"},
		{one, coopCommand{verb: "warp", who: "Tetra"}, "no player Tetra here (Link)"},
		{two, coopCommand{verb: "refresh"}, "say which player: /refresh Link or /refresh Tetra"},
		{two, coopCommand{verb: "warp", who: "Tetra"}, "Tetra"},
		{two, coopCommand{verb: "coop off"}, "Link Tetra"},
		{two, coopCommand{verb: "coop on", who: "Link"}, "Link"},
		{two, coopCommand{verb: "status"}, "Link Tetra"},
	}
	for _, c := range cases {
		con := newCoopConsole(&coLog{}, nil)
		for _, n := range c.names {
			con.syncs = append(con.syncs, &coopSync{name: n})
		}
		ss, err := con.pick(c.cmd)
		var got []string
		for _, s := range ss {
			got = append(got, s.name)
		}
		if g := strings.Join(got, " "); err != nil && !strings.Contains(err.Error(), c.want) || err == nil && g != c.want {
			t.Errorf("%v %+v: %q, %v; want %q", c.names, c.cmd, g, err, c.want)
		}
	}
}

// coConsoleSync is a session with one coop-sync pipeline over a fake game
// in the sea, not connected.
func coConsoleSync(t *testing.T, ctx context.Context, f coopFlags) (*coopSession, *coopSync, *coGame, *coLog) {
	t.Helper()
	log := &coLog{}
	cs := startCoopSession(log, f, coopEnv{backups: t.TempDir(), userDirs: []string{coUserDir(t)}, input: strings.NewReader("")})
	cg := newCoGame(t, "Link", coMine{life: 12})
	s, err := cs.newSync(ctx, "Link", cg.m, log)
	if err != nil {
		t.Fatal(err)
	}
	return cs, s, cg, log
}

// coWaitLog waits for log to have sub, a line a goroutine logs.
func coWaitLog(t *testing.T, log *coLog, sub string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !log.has(sub) {
		if time.Now().After(deadline) {
			t.Fatalf("no %q in:\n%s", sub, log)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestCoopConsole(t *testing.T) {
	t.Run("dry run", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		cs, _, _, log := coConsoleSync(t, ctx, coopFlags{on: true, yes: true, dryRun: true})
		cs.console.do(ctx, "/warp")
		coWaitLog(t, log, "co-op: /warp for Link: --coop-dry-run writes no game data")
		cs.console.do(ctx, "/coop off")
		coWaitLog(t, log, "co-op: Link writes nothing already (a dry run (--coop-dry-run))")
		cs.console.do(ctx, "/status")
		coWaitLog(t, log, "a dry run (--coop-dry-run); 0 bytes written, 0 escapes")
		cs.console.do(ctx, "/help")
		coWaitLog(t, log, "co-op:   "+coopCommandHelp[0])
		cs.console.do(ctx, "/jump")
		coWaitLog(t, log, "co-op: no command /jump: /help lists them")
	})
	t.Run("writing", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		cs, s, cg, log := coConsoleSync(t, ctx, coopFlags{on: true, yes: true})
		// Not connected, so not armed: an escape waits for the next tick,
		// which says not now.
		cs.console.do(ctx, "/refresh")
		cg.g.Frame()
		s.r.Tick()
		coWaitLog(t, log, "co-op: /refresh for Link: not now: co-op is not running for this file")
		coWaitLog(t, log, "; try again in a moment")

		cs.console.do(ctx, "/coop on")
		coWaitLog(t, log, "co-op: Link: nothing to turn on (writing)")
		cs.console.do(ctx, "/coop off")
		if st := s.r.Status(); st.WritesOff != "co-op writes are off for Link: you typed /coop off" {
			t.Fatalf("writes off %q", st.WritesOff)
		}
		coWaitLog(t, log, "co-op writes are off for Link: you typed /coop off. Pose sync goes on; type /coop on to write the world again.")
		cs.console.do(ctx, "/warp")
		coWaitLog(t, log, "co-op: /warp for Link: co-op writes are off: co-op writes are off for Link: you typed /coop off")
		cs.console.do(ctx, "/coop off")
		coWaitLog(t, log, "co-op: Link writes nothing already (writes off:")
		cs.console.do(ctx, "/coop on")
		coWaitLog(t, log, "co-op: writes are on again for Link (they were off: co-op writes are off for Link: you typed /coop off)")
		if st := s.r.Status(); st.WritesOff != "" {
			t.Fatalf("writes still off: %q", st.WritesOff)
		}
		if n := log.count("Pose sync goes on"); n != 1 {
			t.Errorf("the turn-off was said %d times:\n%s", n, log)
		}
	})
}

// TestCoopPartnerPos: a warp's partner position is the one other player's
// that pose sync heard, never the player's own (mp-local's twins hear
// each other); with two others there is none.
func TestCoopPartnerPos(t *testing.T) {
	srv := network.NewServer(0)
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	defer srv.Stop()
	at := fmt.Sprintf("127.0.0.1:%d", srv.Addr().(*net.TCPAddr).Port)
	cs := &coopSession{poses: map[string]*network.Client{}}
	if _, ok := cs.partnerPos("Link")(); ok {
		t.Fatal("a position without a pose client")
	}
	var nilSession *coopSession
	if f := nilSession.withPoseClient("Link", nil); f != nil {
		t.Fatal("a session without co-op wraps onClient")
	}
	link, tetra := coPose(t, cs, "Link", at), coPose(t, &coopSession{poses: map[string]*network.Client{}}, "Tetra", at)
	want := network.PlayerPosition{PosX: 1, PosY: 2, PosZ: 3}
	deadline := time.Now().Add(5 * time.Second)
	for {
		if err := link.SendPosition(&network.PlayerPosition{PosX: 9, PosY: 9, PosZ: 9}); err != nil {
			t.Fatal(err)
		}
		if err := tetra.SendPosition(&want); err != nil {
			t.Fatal(err)
		}
		if p, ok := cs.partnerPos("Link")(); ok {
			if p != (replica.Pos{X: 1, Y: 2, Z: 3}) {
				t.Fatalf("partner at %+v, want Tetra's", p)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("Tetra's position never came")
		}
		time.Sleep(5 * time.Millisecond)
	}
	aryll := coPose(t, &coopSession{poses: map[string]*network.Client{}}, "Aryll", at)
	for {
		if err := aryll.SendPosition(&network.PlayerPosition{PosX: 4, PosY: 5, PosZ: 6}); err != nil {
			t.Fatal(err)
		}
		if _, ok := cs.partnerPos("Link")(); !ok {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("a partner position with two others")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// The escapes' bytes are the ones package main's own tools write: the
// mailbox's warp fields (player.Warp) and stage-hop's mNextStage request.
func TestCoopEscapesMatchMain(t *testing.T) {
	if mailbox.WarpSeq != mailbox.Mailbox+mailbox.OffWarpSeq || replica.WarpAck != mailbox.Mailbox+mailbox.OffWarpAck ||
		replica.WarpPos != mailbox.Mailbox+mailbox.OffWarpX || mailbox.OffWarpY != mailbox.OffWarpX+4 || mailbox.OffWarpZ != mailbox.OffWarpX+8 {
		t.Errorf("the replica's warp fields (seq 0x%08X, ack 0x%08X, x/y/z 0x%08X) are not the mailbox's", mailbox.WarpSeq, replica.WarpAck, replica.WarpPos)
	}
	if replica.MaxStageLayer != stageHopLayerMax {
		t.Errorf("layer bound %d, stage-hop's %d", replica.MaxStageLayer, stageHopLayerMax)
	}
	for _, h := range []stageHop{
		{Name: "sea", Point: 0, Room: 44, Layer: -1},
		{Name: "M_NewD2", Point: 3, Room: 5, Layer: 0},
		{Name: "kindan", Point: 255, Room: 63, Layer: 11},
	} {
		r := replica.StageRequest{Name: h.Name, Point: h.Point, Room: h.Room, Layer: h.Layer}
		if err := r.Check(); err != nil {
			t.Errorf("%+v: %v", r, err)
		}
		if r.Bytes() != h.bytes() {
			t.Errorf("%+v: % X, stage-hop's % X", r, r.Bytes(), h.bytes())
		}
		fs := r.Fields()
		if len(fs) != 3 || fs[0].Addr != addr.NextStage || fs[1].Addr != addr.NextStage+addr.NextOffWipe || fs[1].Data[0] != stageHopWipe ||
			fs[2].Addr != addr.NextStageEnable || fs[2].Data[0] != 1 {
			t.Errorf("%+v: fields %+v, want stage-hop's order: the request, the wipe, then mEnable", r, fs)
		}
	}
}

// TestProgressionStatus: /status's M4 lines: the classes granted, the
// story bits the holds keep back and why (a held boss bit is no story
// bit), and with writes each key table and max life.
func TestProgressionStatus(t *testing.T) {
	metKorl := addr.EventByte(0x0F80)
	held := []replica.HeldBit{
		{Off: uint16(addr.OffEvent + metKorl.Off), Mask: metKorl.Mask, Hold: replica.Hold{ID: 0x0F80, Why: "MET_KORL moves the resume stage"}},
		{Off: uint16(addr.OffMemBit(addr.TableDRC) + addr.MemBitOffDungeonItem), Mask: 0x08, Hold: replica.Hold{Why: "the boss stage"}},
	}
	drc := derive.KeyStatus{Table: addr.TableDRC, Dungeon: "DRC", On: true, Seen: true, Target: 1,
		Facts: derive.KeyFacts{Table: addr.TableDRC, Sources: 4, Taken: 2, Doors: 4, Open: 1, Have: 1}}
	fw := derive.KeyStatus{Table: addr.TableFW, Dungeon: "FW", Why: "its M1 key invariant fails (coop-tables)"}
	none := derive.KeyStatus{Table: addr.TableSea, Why: "it is no dungeon with small keys"}
	for _, c := range []struct {
		name string
		st   replica.Status
		want []string
	}{
		{"writing", replica.Status{Features: hub.DefaultFeatures, Held: held, Derive: derive.Status{Keys: []derive.KeyStatus{none, drc, fw}, Life: true}}, []string{
			"progression granted: story|inventory|keys|life|magic",
			"story bits held back until this game catches up: " + replica.DefaultLabel(addr.OffEvent+metKorl.Off, 7) + " (MET_KORL moves the resume stage)",
			"small keys of table 3 (DRC): 1 key(s), 2 of 4 source(s) taken, 1 of 4 door(s) open",
			"small keys of table 4 (FW): not derived: its M1 key invariant fails (coop-tables)",
			"max hearts: derived",
		}},
		{"no tables, keys and life not granted", replica.Status{Features: hub.DefaultFeatures &^ hub.TableFeatures, Derive: derive.Status{NoKeyTable: true, LifeWhy: "no heart table (coop-tables)"}}, []string{
			"progression granted: story|inventory|magic",
			"story bits held back: none",
			"small keys: not derived (no co-op stage tables)",
			"max hearts: not derived: no heart table (coop-tables)",
		}},
		{"dry run", replica.Status{DryRun: true, Features: hub.FeatureImage, Held: held}, []string{
			"story bits held back until this game catches up: " + replica.DefaultLabel(addr.OffEvent+metKorl.Off, 7) + " (MET_KORL moves the resume stage)",
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := progressionStatus(c.st); !slices.Equal(got, c.want) {
				t.Errorf("got\n\t%s\nwant\n\t%s", strings.Join(got, "\n\t"), strings.Join(c.want, "\n\t"))
			}
		})
	}
}
