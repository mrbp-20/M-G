//go:build gateway || gateway.max

package max

// A switch of the conversation while a turn is still running: what the chat
// stops receiving, and what it still gets. The defect these tests pin came from
// the field: /resume moved the chat to another session, the turn kept writing,
// and the file the person had asked for was dropped without a word.

import (
	"context"
	"testing"
)

func TestDetach_StopsTheTextOfTheTurn(t *testing.T) {
	f := newSendServer(t)
	s := testBot(t).newSender(context.Background(), f.client(t), Target{UserID: 7})
	s.responseBuf.WriteString("ответ, который уже не нужен этому чату")
	s.Detach()
	s.Flush()

	if got := len(f.sends()); got != 0 {
		t.Fatalf("sends = %d, want none: the chat belongs to another conversation now", got)
	}
	if s.responseBuf.Len() != 0 {
		t.Fatalf("buffer = %q, want it dropped", s.responseBuf.String())
	}
}

func TestDetach_StopsShortMessagesAndTheAnswer(t *testing.T) {
	f := newSendServer(t)
	s := testBot(t).newSender(context.Background(), f.client(t), Target{UserID: 7})
	s.Detach()

	s.post("служебная строка")
	s.responseBuf.WriteString("кусок ответа")
	s.Flush()

	if got := len(f.sends()); got != 0 {
		t.Fatalf("sends = %d, want the detached turn to stay quiet", got)
	}
}

func TestDetachChat_StopsTheTurnOfTheOldConversationOnly(t *testing.T) {
	b := testBot(t)
	chat := Target{UserID: 7}

	old := b.newSender(context.Background(), nil, chat)
	old.files = stubFiles{id: "sess-old"}
	b.trackSender(old)

	fresh := b.newSender(context.Background(), nil, chat)
	fresh.files = stubFiles{id: "sess-new"}
	b.trackSender(fresh)

	if stopped := b.detachChat(chat, "sess-new"); stopped != 1 {
		t.Fatalf("stopped = %d, want exactly the turn of the conversation left behind", stopped)
	}
	if !old.isDetached() {
		t.Error("the turn of the old conversation still writes into the chat")
	}
	if fresh.isDetached() {
		t.Error("the turn of the conversation now in the chat was stopped")
	}

	// The turns of the first check have ended, so the registry knows only the
	// one of another chat below.
	b.untrackSender(old)
	b.untrackSender(fresh)

	other := b.newSender(context.Background(), nil, Target{UserID: 99})
	other.files = stubFiles{id: "sess-old"}
	b.trackSender(other)
	if stopped := b.detachChat(chat, "sess-new"); stopped != 0 {
		t.Fatalf("stopped = %d, want another chat left alone", stopped)
	}
	if other.isDetached() {
		t.Error("a turn of another chat was stopped by a switch in this one")
	}
}
