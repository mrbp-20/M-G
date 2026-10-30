//go:build gateway || gateway.max

package max

// /stop: the one command that does not wait its turn.
//
// Everything else the bot takes queues behind the answer being written - one
// worker per conversation, so the order of what was said stays the order of what
// is answered. A person who wants the model to stop cannot wait for the model to
// finish, so /stop is answered on intake, before the burst wait and before the
// queue, and it ends the turn in flight instead of joining it.

import (
	"context"
	"strconv"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
)

// handleStop ends the turn of this chat at once, together with what is waiting
// behind it. Waiting messages would start a new turn the moment the stopped one
// returns, which reads as if the stop did nothing.
func (b *Bot) handleStop(ctx context.Context, in inbound, key string) {
	// A group shares one conversation, so stopping it is the admins' call, as
	// moving and clearing it are (dispatch.go).
	if in.isGroup && !b.cfg.IsAdmin(in.userID) {
		b.reply(ctx, in, adminOnlyNote)

		return
	}
	dropped := b.dropQueued(key)
	// The turn is marked as the person's own stop before it is cut, so the error
	// the core may return for an interrupted generation never reaches the chat
	// (dispatch.go).
	b.markStopped(key)
	running := b.cancelTurn(key)
	// The way the web UI and the CLI stop a turn: the session writes the cancel
	// request, marks the turn cancelled and cancels it, so the core ends it
	// quietly rather than reporting a failure.
	if id := b.store.Peek(key); id != "" && b.runner != nil {
		b.runner.HandleSessionCancel(acp.SessionCancelParams{SessionID: id})
	}

	switch {
	case running, dropped > 0:
		b.log.Info("max: turn stopped", "user", in.userID, "chat", in.chatID,
			"running", running, "dropped", dropped)
		b.reply(ctx, in, stopNote(running, dropped))
	default:
		b.reply(ctx, in, "⏹ Сейчас ничего не выполняется.")
	}
}

// markStopped records that the person ended the turn of this chat.
func (b *Bot) markStopped(key string) {
	b.runningMu.Lock()
	if b.stoppedKeys == nil {
		b.stoppedKeys = make(map[string]bool)
	}
	b.stoppedKeys[key] = true
	b.runningMu.Unlock()
}

// personStopped reports whether the person ended the running turn, and forgets
// it once asked: the next turn of the chat is nobody's stop but its own.
func (b *Bot) personStopped(key string) bool {
	b.runningMu.Lock()
	defer b.runningMu.Unlock()
	if !b.stoppedKeys[key] {
		return false
	}
	delete(b.stoppedKeys, key)

	return true
}

// stopNote says what was stopped and how much of the queue went with it.
func stopNote(running bool, dropped int) string {
	switch {
	case !running:
		return "⏹ Снимаю очередь: " + countMessages(dropped) + "."
	case dropped == 0:
		return "⏹ Останавливаю."
	default:
		return "⏹ Останавливаю; из очереди снял " + countMessages(dropped) + "."
	}
}

// countMessages spells a number of messages the way the rest of the bot does.
func countMessages(n int) string {
	word := "сообщений"
	switch {
	case n%10 == 1 && n%100 != 11:
		word = "сообщение"
	case n%10 >= 2 && n%10 <= 4 && (n%100 < 12 || n%100 > 14):
		word = "сообщения"
	}

	return strconv.Itoa(n) + " " + word
}

// registerTurn puts the turn of key on record, so /stop can end it from the
// intake path of another goroutine.
func (b *Bot) registerTurn(key string, cancel context.CancelFunc) {
	b.runningMu.Lock()
	if b.running == nil {
		b.running = make(map[string]context.CancelFunc)
	}
	b.running[key] = cancel
	b.runningMu.Unlock()
}

// unregisterTurn forgets a turn that ended on its own.
func (b *Bot) unregisterTurn(key string) {
	b.runningMu.Lock()
	delete(b.running, key)
	b.runningMu.Unlock()
}

// cancelTurn ends the turn of key and reports whether there was one to end.
func (b *Bot) cancelTurn(key string) bool {
	b.runningMu.Lock()
	cancel, ok := b.running[key]
	delete(b.running, key)
	b.runningMu.Unlock()
	if ok {
		cancel()
	}

	return ok
}

// dropQueued takes back what waits in the worker queue of this chat. The worker
// counts every accepted job in inFlight, so a dropped one is released here.
func (b *Bot) dropQueued(key string) int {
	b.mu.Lock()
	ch, ok := b.workers[key]
	b.mu.Unlock()
	if !ok {
		return 0
	}
	dropped := 0
	for {
		select {
		case <-ch:
			b.inFlight.Done()
			dropped++
		default:
			return dropped
		}
	}
}
