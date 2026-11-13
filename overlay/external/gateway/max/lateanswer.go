//go:build gateway || gateway.max

package max

import (
	"context"
	"strings"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/llm"
)

// A turn started in another window — the web UI, the desktop window, the
// console — belongs to that surface: the gateway never wrapped it, because at
// the moment it started the chat was bound to a different conversation
// (mirror.go decides that at turn start and never again). Switching the chat to
// that conversation afterwards used to leave the person with the question card
// and no answer: the turn wrote into its own window.
//
// The answer is taken from the conversation itself. The transcript of a session
// is readable (history.go: SetTranscript) and grows as the turn writes into it,
// so the watcher below reads it like a tail and posts what appears. It is not
// the live event stream — tool steps stay in the other window — but the answer
// arrives here without waiting for the next /resume, which is what the person
// is waiting for.
const (
	// lateWatchLimit bounds the watch: a turn that runs longer than this is
	// left to its own window rather than watched forever.
	lateWatchLimit = 45 * time.Minute
)

var (
	// lateWatchPause is how often the conversation is read for new messages.
	lateWatchPause = 2 * time.Second
	// lateWatchIdle is how long the watcher waits for more after the turn
	// stopped, so a last message written a moment later still lands.
	lateWatchIdle = 8 * time.Second
)

// noteTurnElsewhere tells the person where the answer will appear. It is said
// only when the turn really runs in another window, because calling a finished
// conversation busy sends the person to wait for nothing.
const noteTurnElsewhere = "Ответ появится здесь, как только ход закончится: он идёт в другом окне."

// watchLateAnswer posts into target whatever the session writes from now on.
// It runs in the background and never blocks the switch itself.
func (b *Bot) watchLateAnswer(ctx context.Context, target Target, key, sessionID string) {
	if b.transcript == nil {
		b.log.Debug("max: the answer of another window is not watched", "reason", "no transcript reader")

		return
	}
	wctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), lateWatchLimit)
	defer cancel()

	seen := len(b.transcript(sessionID))
	posted := 0
	var quietSince time.Time

	// take posts what the conversation gained since the last look and reports
	// how many messages of the answer it sent.
	take := func() int {
		msgs := b.transcript(sessionID)
		if len(msgs) < seen {
			// The conversation was rewritten under us (an edit, a compaction):
			// start counting again rather than posting the wrong tail.
			seen = len(msgs)

			return 0
		}
		sent := 0
		for _, m := range msgs[seen:] {
			seen++
			if m.Role != llm.RoleAssistant {
				continue
			}
			if text := strings.TrimSpace(m.Content); text != "" && b.postLateAnswer(wctx, target, text) {
				sent++
			}
		}

		return sent
	}

	ticker := time.NewTicker(lateWatchPause)
	defer ticker.Stop()

	for {
		select {
		case <-wctx.Done():
			if posted == 0 {
				b.log.Warn("max: the answer of another window never came",
					"session", sessionID, "chat", target.ChatID, "reason", "watch expired")
			}

			return
		case <-ticker.C:
			if b.store != nil && b.store.Peek(key) != sessionID {
				b.log.Debug("max: the answer of another window is no longer wanted",
					"session", sessionID, "chat", target.ChatID, "reason", "chat moved")

				return
			}
			if sent := take(); sent > 0 {
				posted += sent
				quietSince = time.Now()
			}
			if posted == 0 {
				continue
			}
			// The turn is over, or it holds still after writing: either way the
			// answer it was going to give is here.
			if !b.turnActive(sessionID) || time.Since(quietSince) >= lateWatchIdle {
				b.log.Info("max: the answer of another window reached the chat",
					"session", sessionID, "chat", target.ChatID, "parts", posted)

				return
			}
		}
	}
}

// postLateAnswer sends one message of a turn that runs in another window.
func (b *Bot) postLateAnswer(ctx context.Context, target Target, text string) bool {
	c := b.connectedClient()
	if c == nil {
		b.log.Warn("max: the answer of another window not posted", "reason", "no connected client")

		return false
	}
	text = strings.TrimSpace(clipUnits(text, maxTextRunes-historySlack))
	if text == "" {
		return false
	}
	if b.sent.repeat(target, text) {
		b.log.Debug("max: duplicate answer dropped", "target", target, "runes", len([]rune(text)))

		return false
	}
	sctx, cancel := context.WithTimeout(ctx, actionTimeout*2)
	defer cancel()
	if _, err := c.Send(sctx, Outgoing{Target: target, Text: text}); err != nil {
		b.log.Warn("max: the answer of another window not posted", "chat", target.ChatID, "err", err)

		return false
	}

	return true
}

// watchSwitch starts watching when the switch landed on a conversation that
// runs a turn this bot did not start. It answers the note the chat gets, or an
// empty string when there is nothing to watch: our own turn delivers itself and
// a finished conversation has nothing to wait for.
func (b *Bot) watchSwitch(ctx context.Context, in inbound, key, sessionID string) string {
	if !b.turnActive(sessionID) || b.turnOurs(key) {
		return ""
	}
	go b.watchLateAnswer(ctx, b.answerTarget(in), key, sessionID)

	return noteTurnElsewhere
}
