//go:build gateway || gateway.max

package max

import (
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/llm"
)

// TestHoldConversation_StartsWatchingTheConversation: switching a chat to a
// conversation where nothing runs must arm the watcher. Otherwise the chat holds
// a conversation that cannot answer here: a turn started in another window or by
// a service writes into the conversation, and the answer never leaves it.
//
// 24.11.2026 is the live case: the operator switched to a quiet conversation at
// 13:03:39, the voice service posted a turn at 13:05, and the answer "Понедельник."
// stayed inside the conversation — the gateway had not a single log line for it.
func TestHoldConversation_StartsWatchingTheConversation(t *testing.T) {
	c, rec := lateClient(t)
	conv := &tail{}
	conv.add(llm.RoleUser, "вопрос, заданный раньше")
	b := watchedBot(t, c, conv)

	b.holdConversation("max:user:1", "sess-1")

	conv.add(llm.RoleAssistant, "ответ, пришедший не из MAX")
	waitFor(t, func() bool { return rec.count() > 0 }, "the answer of the held conversation to reach the chat")

	if got := rec.all()[0]; !strings.Contains(got, "ответ, пришедший не из MAX") {
		t.Fatalf("not the answer that went in: %s", got)
	}
}

// TestHoldConversation_AlsoBindsTheChat: the binding and the watcher travel
// together, so a chat that holds a conversation is never one the gateway forgot
// to watch.
func TestHoldConversation_AlsoBindsTheChat(t *testing.T) {
	c, _ := lateClient(t)
	b := watchedBot(t, c, &tail{})

	b.holdConversation("max:user:7", "sess-7")

	if got := b.store.Peek("max:user:7"); got != "sess-7" {
		t.Fatalf("the chat does not hold the conversation: %q", got)
	}

	b.watchMu.Lock()
	_, watching := b.watching["sess-7"]
	b.watchMu.Unlock()
	if !watching {
		t.Fatal("the conversation the chat holds is not watched")
	}
}
