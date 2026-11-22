//go:build gateway || gateway.max

package max

// The fourth round of the same defect, 22.11.2026 19:29: the mirror delivered one
// block of 1 329 units and the watcher posted the same answer under it as 35 +
// 1 292, because the transcript keeps a turn as its own messages while delivery
// sends the accumulated answer. Bytes never match there, so the turn's end is what
// the watcher is told about.

import (
	"context"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/llm"
)

func TestMirrorTurn_KeepsTheWatcherFromPostingTheBlockAgain(t *testing.T) {
	c, rec := lateClient(t)
	conv := &tail{}
	b := watchedBot(t, c, conv)
	b.store.Bind("max:user:1", "sess-1")

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	_, stop := b.MirrorTurn("sess-1", nil)
	b.armWatcher(ctx, "sess-1")

	conv.add(llm.RoleAssistant, "первая строка ответа")
	conv.add(llm.RoleAssistant, "вторая строка ответа")
	stop()

	time.Sleep(60 * time.Millisecond) // several ticks of a 5 ms watcher

	if got := rec.count(); got != 0 {
		t.Fatalf("sends = %d after a mirrored turn, want none: the mirror delivered the answer itself", got)
	}
}

func TestMirrorTurn_StillLetsTheAnswerThroughWhenTheChatMovedAway(t *testing.T) {
	c, rec := lateClient(t)
	conv := &tail{}
	b := watchedBot(t, c, conv)
	b.store.Bind("max:user:1", "sess-1")

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	chat, stop := b.MirrorTurn("sess-1", nil)
	if s, ok := chat.(*Sender); ok {
		s.Detach() // /resume moved the chat while the turn was running
	}
	b.armWatcher(ctx, "sess-1")

	conv.add(llm.RoleAssistant, "ответ, который чату никто не доставил")
	stop()

	waitFor(t, func() bool { return rec.count() >= 1 },
		"the answer of a turn whose chat moved away to be posted by the watcher")
}
