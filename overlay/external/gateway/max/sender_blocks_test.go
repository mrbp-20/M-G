package max

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
)

// TestSender_ATurnOfBlocksClosesWithOneAnswer: a turn says something, runs a
// tool, then says more. The chat hears about the tool at once - it is what the
// turn is doing - and the text reaches it at the end, as one answer with the
// blocks kept apart. A block posted mid-turn would sit above the cards that
// came after it and read as an answer that keeps being interrupted.
func TestSender_ATurnOfBlocksClosesWithOneAnswer(t *testing.T) {
	f := newSendServer(t)
	b := testBot(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := b.newSender(ctx, f.client(t), Target{UserID: 7})

	if err := s.SendSessionUpdate("sess", chunk("первый блок")); err != nil {
		t.Fatalf("first block: %v", err)
	}
	if err := s.SendSessionUpdate("sess", acp.ToolCallUpdate{Title: "grep"}); err != nil {
		t.Fatalf("tool call: %v", err)
	}
	f.waitSends(t, 1, 5*time.Second)
	if err := s.SendSessionUpdate("sess", chunk("второй блок")); err != nil {
		t.Fatalf("second block: %v", err)
	}
	if got := len(f.sends()); got != 1 {
		t.Fatalf("sends = %d before the turn ended, want only the tool card", got)
	}
	s.Flush()
	f.waitSends(t, 2, 5*time.Second)

	sends := f.sends()
	calls := 0
	var answer string
	for _, call := range sends {
		if strings.Contains(string(call.body.Attachments), `"det:`) {
			calls++
			continue
		}
		answer = call.body.Text
	}
	if want := "первый блок\n\nвторой блок"; answer != want {
		t.Fatalf("the answer is %q, want %q: one closing message, blocks apart", answer, want)
	}
	if calls != 1 {
		t.Fatalf("%d messages carried the call and its details button, want one", calls)
	}
}

// TestSender_TheAnswerGoesOutAsMarkdown: MAX renders markdown when the message
// asks for it, so the answer keeps the markers the agent wrote and the client
// draws bold and code from them - the same shape the web shows. The format has
// to travel with the text: without it the person reads the markers instead.
func TestSender_TheAnswerGoesOutAsMarkdown(t *testing.T) {
	f := newSendServer(t)
	b := testBot(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := b.newSender(ctx, f.client(t), Target{UserID: 7})

	if err := s.SendSessionUpdate("sess", chunk("**итог**: `ok`")); err != nil {
		t.Fatalf("SendSessionUpdate: %v", err)
	}
	s.Flush()
	f.waitSends(t, 1, 5*time.Second)

	sends := f.sends()
	if len(sends) == 0 {
		t.Fatal("nothing was sent")
	}
	if got, want := sends[0].body.Text, "**итог**: `ok`"; got != want {
		t.Fatalf("the message carried %q, want the answer as the agent wrote it (%q)", got, want)
	}
	if got := sends[0].body.Format; got != "markdown" {
		t.Fatalf("the message asked for format %q, want markdown: without it the client shows the markers", got)
	}
}
