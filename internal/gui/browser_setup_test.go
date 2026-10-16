package gui

import (
	"reflect"
	"strings"
	"testing"
)

// TestBrowserMethodsAllowlist: ui --browser serves exactly the Bridge's
// methods minus the window-only ones, each listed by name.
func TestBrowserMethodsAllowlist(t *testing.T) {
	bt := reflect.TypeOf(&Bridge{})
	for i := 0; i < bt.NumMethod(); i++ {
		name := bt.Method(i).Name
		if browserMethods[name] == windowOnly[name] {
			t.Errorf("%s must be in exactly one of browserMethods and windowOnly", name)
		}
	}
	for name := range browserMethods {
		if _, ok := bt.MethodByName(name); !ok {
			t.Errorf("browserMethods lists %s, which Bridge doesn't have", name)
		}
	}
	for name := range windowOnly {
		if _, ok := bt.MethodByName(name); !ok {
			t.Errorf("windowOnly lists %s, which Bridge doesn't have", name)
		}
	}
}

func TestBrowserSetupCalls(t *testing.T) {
	s, _ := startBrowser(t)
	code, body := api(t, s, "PickFile", `["iso"]`, nil)
	if code != 400 || !strings.Contains(body["error"].(string), "no method") {
		t.Fatalf("PickFile in a browser: %d %v", code, body)
	}
	code, body = api(t, s, "Setup", `[]`, nil)
	res, _ := body["result"].(map[string]any)
	if code != 200 || res == nil || res["needed"] != true || res["fileDialog"] != false {
		t.Fatalf("Setup: %d %v", code, body)
	}
	code, body = api(t, s, "SaveProfile", `[{"name":"Tetra","color":"red"}]`, nil)
	if code != 200 || body["error"] != nil {
		t.Fatalf("SaveProfile: %d %v", code, body)
	}
	code, body = api(t, s, "StartPatch", `[{"path":"C:\nope\none.iso"}]`, nil)
	if code != 200 || !strings.Contains(body["error"].(string), "can't open") {
		t.Fatalf("StartPatch of a missing disc: %d %v", code, body)
	}
	code, body = api(t, s, "PatchStatus", `[]`, nil)
	if res, _ := body["result"].(map[string]any); code != 200 || res["stage"] != "idle" {
		t.Fatalf("PatchStatus: %d %v", code, body)
	}
	if code, _ := api(t, s, "Nope", `[]`, nil); code != 400 {
		t.Fatalf("an unknown method: %d", code)
	}
}
