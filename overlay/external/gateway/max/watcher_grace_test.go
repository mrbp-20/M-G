//go:build gateway || gateway.max

package max

// The window of 22.11.2026, the fifth round of one and the same defect.
//
// A turn started outside MAX (the browser) delivers its own answer through the
// mirror and only then records where the watcher must start. The watcher skips a
// conversation while its turn runs, so everything looked right on paper - but
// the running flag clears a few hundred milliseconds before the mark lands, and
// a tick falling into that window posted the answer a second time.
//
// On my own instance it never happened; on Merlen's it happened twice: one block
// from the mirror at 20:43:25.3, then 101 and 3 542 runes from the watcher at
// 25.8 and 26.3. That is what a float looks like, so the test drives the window
// by hand instead of waiting for it: no luck needed, and the guard cannot be
// quietly removed later.

import (
	"context"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/llm"
)

// watchedBotBusy builds a bot whose runner can be told whether a turn runs in a
// conversation - the question the watcher asks before it posts anything.
func watchedBotBusy(t *testing.T, c *Client, conv *tail) (*Bot, *busyRunner) {
	t.Helper()
	b := watchedBot(t, c, conv)
	run := &busyRunner{stubRunner: newStubRunner(), busy: make(map[string]bool)}
	b.runner = run

	return b, run
}

func (r *busyRunner) set(sessionID string, busy bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.busy == nil {
		r.busy = make(map[string]bool)
	}
	r.busy[sessionID] = busy
}

func TestChatWatcher_GivesATurnThatJustEndedTimeToMarkItsOwnAnswer(t *testing.T) {
	c, rec := lateClient(t)
	conv := &tail{}
	b, run := watchedBotBusy(t, c, conv)
	b.watchGrace = 300 * time.Millisecond
	b.store.Bind("max:user:1", "sess-1")

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	// A turn runs in the conversation: the watcher stays quiet.
	run.set("sess-1", true)
	b.armWatcher(ctx, "sess-1")
	time.Sleep(30 * time.Millisecond)

	// The turn ends. The answer is already in the transcript and the running flag
	// is already cleared, but the mark telling the watcher "this was delivered"
	// has not landed - the exact window that produced Merlen's duplicate.
	conv.add(llm.RoleAssistant, "длинный ответ, который зеркало уже отправило")
	run.set("sess-1", false)
	time.Sleep(60 * time.Millisecond)

	if got := rec.count(); got != 0 {
		t.Fatalf("sends = %d inside the grace window, want none: the turn is still finishing its own delivery", got)
	}

	// The mark lands, as mirror.go now does it before releasing the chat.
	b.markTurnEnd("sess-1")
	time.Sleep(60 * time.Millisecond)

	if got := rec.count(); got != 0 {
		t.Fatalf("sends = %d after the mark, want none: the turn delivered this block itself", got)
	}
}

func TestChatWatcher_StillPostsWhatNobodyDeliveredAfterTheGrace(t *testing.T) {
	c, rec := lateClient(t)
	conv := &tail{}
	b, run := watchedBotBusy(t, c, conv)
	b.watchGrace = 40 * time.Millisecond
	b.store.Bind("max:user:1", "sess-1")

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	run.set("sess-1", true)
	b.armWatcher(ctx, "sess-1")
	time.Sleep(20 * time.Millisecond)

	// The turn is over and nobody delivered its answer: no mark will ever come.
	// This is the case of 20.11.2026, and the grace must only delay it, never
	// swallow it.
	run.set("sess-1", false)
	conv.add(llm.RoleAssistant, "ответ, который чату никто не доставил")

	waitFor(t, func() bool { return rec.count() >= 1 }, "the answer nobody delivered reaches the chat")
}
