// Command uipreview serves the app's frontend (internal/gui/frontend) to a
// browser with a fake Bridge (fake.js) instead of Go's, so every screen
// can be looked at, and screenshotted, without a session, a disc or
// Dolphin. The fake answers each Bridge call from a canned scenario the
// page's ?s= names (home, setup, mods, lobby-host, lobby-join, ingame,
// ...; fake.js lists them); ?theme=dark or light picks the theme.
//
//	go run ./tools/uipreview                 # http://127.0.0.1:8765/?s=home
//	go run ./tools/uipreview -dir <frontend> # another copy (a before shot)
//
// Headless screenshots (docs/img/app-ui):
//
//	chrome --headless=new --window-size=1180,860 --screenshot=out.png "http://127.0.0.1:8765/?s=ingame"
//
// It is a developer tool: nothing in it reaches the app, and the real
// browser mode is `ww-multiplayer ui --browser`.
package main

import (
	_ "embed"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

//go:embed fake.js
var fakeJS []byte

func main() {
	dir := flag.String("dir", filepath.Join("internal", "gui", "frontend"), "the frontend folder to serve")
	port := flag.Int("port", 8765, "port on 127.0.0.1")
	flag.Parse()
	if _, err := os.Stat(filepath.Join(*dir, "index.html")); err != nil {
		log.Fatalf("uipreview: %s has no index.html (run it from the repo root, or pass -dir)", *dir)
	}
	http.Handle("/", handler(*dir))
	addr := fmt.Sprintf("127.0.0.1:%d", *port)
	log.Printf("uipreview: http://%s/?s=home", addr)
	log.Fatal(http.ListenAndServe(addr, nil))
}

// handler serves dir, with fake.js put into index.html ahead of the app's
// own module (a classic script runs first, so window.go is there when
// transport.js looks).
func handler(dir string) http.Handler {
	files := http.FileServer(http.Dir(dir))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		switch r.URL.Path {
		case "/__fake.js":
			w.Header().Set("Content-Type", "text/javascript")
			w.Write(fakeJS)
			return
		case "/", "/index.html":
			page, err := os.ReadFile(filepath.Join(dir, "index.html"))
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Write([]byte(inject(string(page))))
			return
		}
		files.ServeHTTP(w, r)
	})
}

// inject puts the fake Bridge's script tag before the first <script> (or
// at the end of <head>).
func inject(page string) string {
	tag := `<script src="/__fake.js"></script>`
	if i := strings.Index(page, "<script"); i >= 0 {
		return page[:i] + tag + "\n" + page[i:]
	}
	return strings.Replace(page, "</head>", tag+"\n</head>", 1)
}
