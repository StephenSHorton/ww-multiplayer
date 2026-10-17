package wwapi_test

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/StephenSHorton/ww-multiplayer/internal/report"
	"github.com/StephenSHorton/ww-multiplayer/internal/session"
	"github.com/StephenSHorton/ww-multiplayer/wwapi"
)

// tmod is an inline test mod: a manifest built from a few fields plus
// optional Init and Run behaviour.
type tmod struct {
	m    wwapi.Manifest
	init func(c *wwapi.Context) error
	run  func(ctx context.Context) error
}

func (t *tmod) Manifest() wwapi.Manifest { return t.m }

func (t *tmod) Init(c *wwapi.Context) error {
	if t.init != nil {
		return t.init(c)
	}
	return nil
}

func (t *tmod) Run(ctx context.Context) error {
	if t.run != nil {
		return t.run(ctx)
	}
	return nil
}

// netMod is a tmod that implements NetProvider.
type netMod struct{ *tmod }

func (netMod) Engine() session.Engine { return nil }

// apiMod is a tmod with a PublicAPI.
type apiMod struct {
	*tmod
	v any
}

func (a apiMod) API() any { return a.v }

// manifest builds a mod.json from id, version and extra JSON fields
// ("" for none) and parses it, failing the test on an error.
func manifest(t *testing.T, id, version, extra string) wwapi.Manifest {
	t.Helper()
	m, err := wwapi.ParseManifest([]byte(manifestJSON(id, version, extra)))
	if err != nil {
		t.Fatalf("manifest %s: %v", id, err)
	}
	return m
}

func manifestJSON(id, version, extra string) string {
	s := fmt.Sprintf(`{"id":%q,"name":%q,"version":%q,"api":"^0.1"`, id, strings.ToUpper(id), version)
	if extra != "" {
		s += "," + extra
	}
	return s + "}"
}

func mk(t *testing.T, id, version, extra string) *tmod {
	t.Helper()
	return &tmod{m: manifest(t, id, version, extra)}
}

// statuses maps id -> Status.String() over a plan's infos.
func statuses(infos []wwapi.ModInfo) map[string]string {
	out := map[string]string{}
	for _, mi := range infos {
		out[mi.ID] = mi.StatusString()
	}
	return out
}

func ids(infos []wwapi.ModInfo) []string {
	var out []string
	for _, mi := range infos {
		out = append(out, mi.ID)
	}
	return out
}

// logRec is a concurrency-safe Reporter.
type logRec struct {
	mu    sync.Mutex
	lines []string
}

func (l *logRec) Log(level report.Level, msg string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.lines = append(l.lines, fmt.Sprintf("%d %s", level, msg))
}

func (l *logRec) text() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return strings.Join(l.lines, "\n")
}
