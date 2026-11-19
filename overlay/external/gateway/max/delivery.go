//go:build gateway || gateway.max

package max

import (
	"context"
	"errors"
	"time"
)

// errNotConnected is what a send returns when the bot has no client: the run
// context ended, or the gateway is between two attempts at connecting.
var errNotConnected = errors.New("max: the bot is not connected")

// textRetryPauses are the waits before each further attempt at an answer.
//
// They exist because of 19.11.2026. The platform stopped answering for about
// fifteen seconds: the answer was written, posted once, timed out, and died with
// the socket. The journal carried "answer not delivered" while the person saw
// silence and had to ask whether the answer existed at all. Three further
// attempts cover a blip of that length and still fit inside flushTimeout.
var textRetryPauses = []time.Duration{2 * time.Second, 4 * time.Second, 8 * time.Second}

// sendText posts one message, repeating what may well not happen again. A
// refusal that will stand - a bad token, a chat that no longer exists, a message
// MAX will not take in any shape - comes back at once: repeating it would only
// make the person wait twice for the same refusal.
func (b *Bot) sendText(ctx context.Context, c *Client, target Target, text string) error {
	if c == nil {
		return errNotConnected
	}
	var err error
	for attempt := 0; ; attempt++ {
		if _, err = c.Send(ctx, Outgoing{Target: target, Text: text, Format: "markdown"}); err == nil {
			return nil
		}
		if attempt >= len(textRetryPauses) || !IsTransient(err) || ctx.Err() != nil {
			return err
		}
		select {
		case <-time.After(textRetryPauses[attempt]):
		case <-ctx.Done():
			return err
		}
	}
}

// park keeps an answer MAX never took, so the next connection can send it.
func (b *Bot) park(target Target, text string) {
	if b.outbox == nil {
		b.log.Error("max: answer lost, no outbox to keep it", "target", target, "runes", len([]rune(text)))

		return
	}
	b.outbox.add(target, text)
	b.log.Info("max: answer parked for a later attempt", "target", target, "waiting", b.outbox.length())
}

// flushOutbox sends what earlier attempts could not deliver. It runs once the
// bot has a client, which is the only moment a parked answer has a way out.
func (b *Bot) flushOutbox(ctx context.Context) {
	if b.outbox == nil {
		return
	}
	items := b.outbox.take()
	if len(items) == 0 {
		return
	}
	c := b.connectedClient()
	if c == nil {
		return
	}
	b.log.Info("max: sending parked answers", "waiting", len(items))
	delivered := 0
	for _, it := range items {
		if err := b.sendText(ctx, c, it.Target, it.Text); err != nil {
			b.log.Warn("max: parked answer still undelivered", "err", err,
				"target", it.Target, "waiting", len(items)-delivered)

			break
		}
		delivered++
	}
	if delivered > 0 {
		b.outbox.drop(delivered)
		b.log.Info("max: parked answers delivered", "count", delivered, "waiting", b.outbox.length())
	}
}

// SetOutboxPath keeps undelivered answers in path, so that a network blip costs
// a delay instead of the answer. An empty path keeps them in memory only.
func (b *Bot) SetOutboxPath(path string) *Bot {
	b.outbox = newOutbox(path)
	return b
}
