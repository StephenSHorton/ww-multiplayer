package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/mods/multiplayer/net"
)

func TestParseStatusArgs(t *testing.T) {
	for _, c := range []struct {
		args    []string
		addr    string
		json    bool
		timeout time.Duration
		err     string
	}{
		{[]string{"example.org"}, "example.org:25565", false, 5 * time.Second, ""},
		{[]string{"10.0.0.2:25570", "--json"}, "10.0.0.2:25570", true, 5 * time.Second, ""},
		{[]string{"--timeout", "2s", "WW-30AG-09AX"}, "192.168.1.42:25565", false, 2 * time.Second, ""},
		{[]string{"h", "--timeout=750ms"}, "h:25565", false, 750 * time.Millisecond, ""},
		{nil, "", false, 0, "missing the server's address"},
		{[]string{"a", "b"}, "", false, 0, `unexpected argument "b"`},
		{[]string{"a", "--timeout", "soon"}, "", false, 0, "not a positive duration"},
		{[]string{"a", "--timeout"}, "", false, 0, "needs a value"},
		{[]string{"a", "--loud"}, "", false, 0, "unknown option"},
	} {
		a, err := parseStatusArgs(c.args)
		if c.err != "" {
			if err == nil || !strings.Contains(err.Error(), c.err) {
				t.Errorf("%q: error %v, want %q", c.args, err, c.err)
			}
			continue
		}
		if err != nil || a.addr != c.addr || a.json != c.json || a.timeout != c.timeout {
			t.Errorf("%q = %+v, %v", c.args, a, err)
		}
	}
}

// TestReportStatus: the exit codes a HEALTHCHECK goes by, and the JSON.
func TestReportStatus(t *testing.T) {
	up := network.StatusBody{App: network.StatusApp, Version: "v1", Protocol: network.ProtocolVersion, Players: 2, Lobby: 1, Phase: "lobby", UptimeSecs: 61}
	old := up
	old.Protocol = network.ProtocolVersion - 1
	for _, c := range []struct {
		name string
		b    network.StatusBody
		err  error
		code int
		text string
	}{
		{"up", up, nil, statusUp, "up (ww-multiplayer v1, protocol"},
		{"other protocol", old, nil, statusOther, "both need the same version"},
		{"legacy", network.StatusBody{}, network.ErrStatusLegacy, statusOther, "older ww-multiplayer"},
		{"unreachable", network.StatusBody{}, errors.New("connection refused"), statusUnreachable, "unreachable: connection refused"},
		{"not ours", network.StatusBody{App: "other"}, errors.New(`from "other"`), statusOther, `from "other"`},
	} {
		var w bytes.Buffer
		if code := reportStatus(&w, statusArgs{addr: "h:1"}, c.b, c.err); code != c.code || !strings.Contains(w.String(), c.text) {
			t.Errorf("%s: exit %d %q, want %d %q", c.name, code, w.String(), c.code, c.text)
		}
		w.Reset()
		reportStatus(&w, statusArgs{addr: "h:1", json: true}, c.b, c.err)
		var out struct {
			OK     bool                `json:"ok"`
			Server *network.StatusBody `json:"server"`
		}
		if err := json.Unmarshal(w.Bytes(), &out); err != nil || out.OK != (c.code == statusUp) {
			t.Errorf("%s: JSON %s (%v)", c.name, w.String(), err)
		}
	}
}

// TestStatusAgainstServer: the probe and the report against a real server
// on loopback, as a container's HEALTHCHECK runs them.
func TestStatusAgainstServer(t *testing.T) {
	srv := network.NewServer(0)
	srv.AppVersion = "v0.0.0-test"
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	defer srv.Stop()
	a := statusArgs{addr: srv.Addr().String(), timeout: 2 * time.Second}
	b, err := network.ProbeStatus(context.Background(), a.addr, a.timeout)
	var w bytes.Buffer
	if code := reportStatus(&w, a, b, err); code != statusUp {
		t.Fatalf("exit %d: %s", code, w.String())
	}
}

// TestStatusExitCodes: the exit codes against fake listeners, as
// docs/11-self-hosting.md §5 has them: another program's reply (HTTP) is 1,
// a close with no reply (a server from before the probe) 3, a status
// answer with another protocol 3.
func TestStatusExitCodes(t *testing.T) {
	other, _ := json.Marshal(network.StatusBody{App: network.StatusApp, Version: "v0.4.9", Protocol: network.ProtocolVersion - 1, Phase: "lobby"})
	frame := append([]byte{network.MsgStatus, 0, 0}, other...)
	binary.BigEndian.PutUint16(frame[1:3], uint16(len(other)))
	for _, c := range []struct {
		name  string
		reply []byte
		code  int
		text  string
	}{
		{"http", []byte("HTTP/1.1 400 Bad Request\r\nContent-Length: 0\r\n\r\n"), statusUnreachable, "not ww-multiplayer"},
		{"closes", nil, statusOther, "older ww-multiplayer"},
		{"protocol 5", frame, statusOther, "both need the same version"},
	} {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		go func(reply []byte) {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			defer c.Close()
			c.Read(make([]byte, 3))
			c.Write(reply)
		}(c.reply)
		a := statusArgs{addr: ln.Addr().String(), timeout: 2 * time.Second}
		b, err := network.ProbeStatus(context.Background(), a.addr, a.timeout)
		var w bytes.Buffer
		if code := reportStatus(&w, a, b, err); code != c.code || !strings.Contains(w.String(), c.text) {
			t.Errorf("%s: exit %d %q, want %d %q", c.name, code, w.String(), c.code, c.text)
		}
		ln.Close()
	}
}
