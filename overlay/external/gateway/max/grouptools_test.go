//go:build gateway || gateway.max

package max

// A group goes without the cards of the tool calls its turn makes: the card
// carries the command, the paths in it and whatever the output holds, and a
// group holds people the machine was not handed to. A direct chat keeps them.
// What a card-less group must still hear is that a call did not run - otherwise
// the answer reads as though the tool had worked - and it hears it without the
// command.

import (
	"context"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
)

// groupChat is the id of a group: MAX keeps a person's chat id positive and a
// group's negative, and that sign is the whole default.
const groupChat int64 = -1000000000001

// cardsBot is a bot of the tests whose chat entries the test writes out: the
// question of what a chat sees of the tool calls is answered by the entry, so a
// test has to be able to write one.
func cardsBot(t *testing.T, chats ...config.GatewayChatConfig) *Bot {
	t.Helper()
	cfg := &config.MaxGatewayConfig{Chats: chats}
	cfg.ApplyDefaults()

	return New(cfg, nil, "", slog.New(slog.DiscardHandler), "", filepath.Join(t.TempDir(), "gateway_max_cursor"), nil)
}

// quietWait is how long a test watches for a message that must not arrive: a
// card travels through the pacer, so "nothing yet" needs a moment, not a look.
const quietWait = 400 * time.Millisecond

func textsOf(calls []wireCall) string {
	var sb strings.Builder
	for _, c := range calls {
		sb.WriteString(c.body.Text)
		sb.WriteString("\n")
	}

	return sb.String()
}

// TestSender_A_GroupGoesWithoutTheToolCards: the turn runs a command, the group
// gets the answer and nothing else - no card, no command, no path.
func TestSender_A_GroupGoesWithoutTheToolCards(t *testing.T) {
	f := newSendServer(t)
	b := cardsBot(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := b.newSender(ctx, f.client(t), Target{ChatID: groupChat})

	if err := s.SendSessionUpdate("sess", acp.ToolCallUpdate{
		ToolCallID: "call-1",
		Title:      "Run: grep -r token /srv/data",
	}); err != nil {
		t.Fatalf("SendSessionUpdate the call: %v", err)
	}
	if err := s.SendSessionUpdate("sess", acp.ToolCallStatusUpdate{ToolCallID: "call-1", Status: "completed"}); err != nil {
		t.Fatalf("SendSessionUpdate the status: %v", err)
	}
	time.Sleep(quietWait)
	if got := len(f.sends()); got != 0 {
		t.Fatalf("%d messages before the answer, want none: a group goes without cards", got)
	}

	if err := s.SendSessionUpdate("sess", chunk("Вот итог.")); err != nil {
		t.Fatalf("SendSessionUpdate the answer: %v", err)
	}
	s.Flush()
	f.waitSends(t, 1, 5*time.Second)

	calls := f.sends()
	if got := len(calls); got != 1 {
		t.Fatalf("sends = %d, want only the answer", got)
	}
	if got := calls[0].body.Text; !strings.Contains(got, "Вот итог.") {
		t.Fatalf("the group got %q, want the answer", got)
	}
	if all := textsOf(calls); strings.Contains(all, "grep") || strings.Contains(all, "/srv/data") {
		t.Fatalf("the group saw the command: %q", all)
	}
}

// TestSender_A_DirectChatKeepsTheToolCards: the same turn in a direct chat still
// opens with the card, as it always did.
func TestSender_A_DirectChatKeepsTheToolCards(t *testing.T) {
	f := newSendServer(t)
	b := cardsBot(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := b.newSender(ctx, f.client(t), Target{UserID: 7})

	if err := s.SendSessionUpdate("sess", acp.ToolCallUpdate{ToolCallID: "call-1", Title: "websearch"}); err != nil {
		t.Fatalf("SendSessionUpdate the call: %v", err)
	}
	f.waitSends(t, 1, 5*time.Second)
	if err := s.SendSessionUpdate("sess", chunk("Вот итог.")); err != nil {
		t.Fatalf("SendSessionUpdate the answer: %v", err)
	}
	s.Flush()
	f.waitSends(t, 2, 5*time.Second)

	calls := f.sends()
	if got := len(calls); got != 2 {
		t.Fatalf("sends = %d, want the card and the answer", got)
	}
	if got := calls[0].body.Text; !strings.Contains(got, "websearch") {
		t.Fatalf("the card came as %q, want the call's name", got)
	}
}

// TestSender_AChatEntryCanAskForTheCardsBack: an explicit "show" beats the
// default of a group, so a chat can be debugged in place.
func TestSender_AChatEntryCanAskForTheCardsBack(t *testing.T) {
	f := newSendServer(t)
	b := cardsBot(t, config.GatewayChatConfig{ChatID: groupChat, ToolCards: config.ToolCardsShow})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := b.newSender(ctx, f.client(t), Target{ChatID: groupChat})

	if err := s.SendSessionUpdate("sess", acp.ToolCallUpdate{ToolCallID: "call-1", Title: "websearch"}); err != nil {
		t.Fatalf("SendSessionUpdate the call: %v", err)
	}
	f.waitSends(t, 1, 5*time.Second)

	if got := f.sends(); len(got) != 1 || !strings.Contains(got[0].body.Text, "websearch") {
		t.Fatalf("the group with an explicit show posted %d messages, want the card", len(got))
	}
}

// TestSender_A_GroupHearsOnceThatAToolDidNotRun: two failed calls, one line, and
// the line never carries the command.
func TestSender_A_GroupHearsOnceThatAToolDidNotRun(t *testing.T) {
	f := newSendServer(t)
	b := cardsBot(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := b.newSender(ctx, f.client(t), Target{ChatID: groupChat})

	updates := []interface{}{
		acp.ToolCallUpdate{ToolCallID: "call-1", Title: "Run: rm -rf /tmp/something"},
		acp.ToolCallStatusUpdate{ToolCallID: "call-1", Status: "failed"},
		acp.ToolCallUpdate{ToolCallID: "call-2", Title: "Run: systemctl restart coddy"},
		acp.ToolCallStatusUpdate{ToolCallID: "call-2", Status: "cancelled"},
		chunk("Отвечаю тем, что есть."),
	}
	for _, u := range updates {
		if err := s.SendSessionUpdate("sess", u); err != nil {
			t.Fatalf("SendSessionUpdate: %v", err)
		}
	}
	s.Flush()
	f.waitSends(t, 2, 5*time.Second)

	calls := f.sends()
	if got := len(calls); got != 2 {
		t.Fatalf("sends = %d, want the failure line and the answer", got)
	}
	if got := calls[0].body.Text; got != toolFailureNote {
		t.Fatalf("the group heard %q, want the failure note", got)
	}
	if all := textsOf(calls); strings.Contains(all, "rm -rf") || strings.Contains(all, "systemctl") {
		t.Fatalf("the failure line carried the command: %q", all)
	}
}

// TestSender_AChatEntryCanHideTheCardsOfADirectChat: "hide" works the other way
// round too, so the switch belongs to the chat and not to its sign.
func TestSender_AChatEntryCanHideTheCardsOfADirectChat(t *testing.T) {
	f := newSendServer(t)
	b := cardsBot(t, config.GatewayChatConfig{ChatID: 7, ToolCards: config.ToolCardsHide})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := b.newSender(ctx, f.client(t), Target{UserID: 7})

	if err := s.SendSessionUpdate("sess", acp.ToolCallUpdate{ToolCallID: "call-1", Title: "websearch"}); err != nil {
		t.Fatalf("SendSessionUpdate the call: %v", err)
	}
	time.Sleep(quietWait)
	if got := len(f.sends()); got != 0 {
		t.Fatalf("%d messages, want none: the entry hides the cards", got)
	}
}
