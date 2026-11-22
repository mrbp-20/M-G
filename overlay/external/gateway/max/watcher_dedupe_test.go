//go:build gateway || gateway.max

package max

// The three defects of 22.11.2026, all in the watcher road: an answer posted
// twice (the mirror and the watcher, 0.47 s apart), the watcher's copy cut to
// what fits one message with its last paragraphs gone, and that copy sent raw, so
// "**bold**" reached the chat as asterisks. These tests pin each one.

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/llm"
)

func TestChatWatcher_SplitsALongAnswerAndFormatsIt(t *testing.T) {
	c, rec := lateClient(t)
	conv := &tail{}
	b := watchedBot(t, c, conv)
	b.store.Bind("max:user:1", "sess-1")

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	b.armWatcher(ctx, "sess-1")

	answer := strings.Repeat("слово ", 900)
	conv.add(llm.RoleAssistant, answer)
	waitFor(t, func() bool { return rec.count() >= 2 }, "a long answer to come in pieces")

	words := 0
	for i, raw := range rec.all() {
		var body wireBody
		if err := json.Unmarshal([]byte(raw), &body); err != nil {
			t.Fatalf("piece %d is not a message: %v", i, err)
		}
		if units := textUnits(body.Text); units > maxTextRunes {
			t.Fatalf("piece %d carries %d units, want at most %d", i, units, maxTextRunes)
		}
		if body.Format != "markdown" {
			t.Fatalf("piece %d went out with format %q, want markdown: the chat drew the asterisks", i, body.Format)
		}
		words += strings.Count(body.Text, "слово")
	}
	if words != 900 {
		t.Fatalf("the pieces carry %d words of 900: a long answer must arrive whole, not cut to fit", words)
	}
}

func TestChatWatcher_KeepsItsHandsOffWhileATurnRuns(t *testing.T) {
	c, rec := lateClient(t)
	conv := &tail{}
	b := watchedBot(t, c, conv)
	b.store.Bind("max:user:1", "sess-1")
	busy := &busyRunner{stubRunner: newStubRunner(), busy: map[string]bool{"sess-1": true}}
	b.runner = busy

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	b.armWatcher(ctx, "sess-1")

	conv.add(llm.RoleAssistant, "ответ, который сейчас доставит зеркало")
	time.Sleep(60 * time.Millisecond) // several ticks of a 5 ms watcher

	if got := rec.count(); got != 0 {
		t.Fatalf("sends = %d while the turn runs, want none: the mirror is delivering this very answer", got)
	}

	busy.mu.Lock()
	busy.busy["sess-1"] = false
	busy.mu.Unlock()

	waitFor(t, func() bool { return rec.count() == 1 }, "the answer to be posted once the turn is over")
}

func TestChatWatcher_DoesNotRepeatTheAnswerTheMirrorDelivered(t *testing.T) {
	c, rec := lateClient(t)
	conv := &tail{}
	b := watchedBot(t, c, conv)
	b.store.Bind("max:user:1", "sess-1")
	b.selfPosted.mark("sess-1", "ответ, уже доставленный зеркалом")

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	b.armWatcher(ctx, "sess-1")

	conv.add(llm.RoleAssistant, "ответ, уже доставленный зеркалом")
	time.Sleep(60 * time.Millisecond)

	if got := rec.count(); got != 0 {
		t.Fatalf("sends = %d, want none: the mirror delivered the whole text, and the mark is on the whole text", got)
	}
}
