package coopsession

import (
	"context"
	"io"

	"github.com/StephenSHorton/ww-multiplayer/internal/session"
	"github.com/StephenSHorton/ww-multiplayer/mods/multiplayer/net"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/log"
)

// The golden tests' calls into the prompter and console, in one place.

// goldenAsk asks prompt the way a pipeline's offer does: through the
// coopAsker interface, with the question offerQuestion builds.
func goldenAsk(ctx context.Context, p *coopPrompter, rep log.Reporter, prompt string) bool {
	var a coopAsker = p
	return a.question(ctx, rep, session.Question{Kind: session.QuestionBind, Title: "t", Body: prompt, Details: []string{"d"}})
}

// goldenDo runs one console line the way the stdin reader hands it over.
func goldenDo(cs *Session, line string) {
	cs.console.do(context.Background(), line)
}

// newGoldenSession is a co-op side reading its answers from in.
func newGoldenSession(f Flags, in io.Reader) *Session {
	return &Session{flags: f, ask: newCoopPrompter(f.yes, in), poses: map[string]*network.Client{}}
}
