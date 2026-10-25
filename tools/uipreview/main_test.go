package main

import (
	"io"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestInject: the fake Bridge's tag goes before the page's first script
// (the head's theme script and the app's module both come after it).
func TestInject(t *testing.T) {
	got := inject("<head><script>a</script><script type=\"module\" src=\"app.js\"></script></head>")
	if !strings.HasPrefix(got, `<head><script src="/__fake.js"></script>`+"\n<script>a</script>") {
		t.Fatalf("got %q", got)
	}
	if got := inject("<head></head>"); got != "<head><script src=\"/__fake.js\"></script>\n</head>" {
		t.Fatalf("no script: got %q", got)
	}
}

// TestHandlerServesTheRealFrontend: the repo's frontend, with the fake
// in its page, and fake.js itself.
func TestHandlerServesTheRealFrontend(t *testing.T) {
	dir := filepath.Join("..", "..", "internal", "gui", "frontend")
	if _, err := os.Stat(filepath.Join(dir, "index.html")); err != nil {
		t.Skip("no frontend:", err)
	}
	h := handler(dir)
	for path, want := range map[string]string{"/": "/__fake.js", "/__fake.js": "window.go", "/app.js": "transport.js"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
		body, _ := io.ReadAll(rec.Body)
		if rec.Code != 200 || !strings.Contains(string(body), want) {
			t.Errorf("%s: %d, want %q in the body", path, rec.Code, want)
		}
	}
}
