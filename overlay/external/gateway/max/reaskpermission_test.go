//go:build gateway || gateway.max

package max

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// waitingPermissionOf builds the prompt the page holds while a chat attaches.
func waitingPermissionOf(sessionID, toolCallID string) session.WaitingPermission {
	return session.WaitingPermission{
		SessionID:  sessionID,
		ToolCallID: toolCallID,
		Title:      "Run: write",
		Text:       "Записать файл pogoda/krasnodar-3dnya.svg",
		Options:    []string{"Разрешить", "Отклонить"},
	}
}

// TestReaskPermission_NothingWaitingMeansNoCard: a switch into a conversation that
// waits for no approval stays as it was.
func TestReaskPermission_NothingWaitingMeansNoCard(t *testing.T) {
	b := newTestBot(&stubRunner{})
	f := newSendServer(t)
	b.setClient(f.client(t))
	session.SetWaitingPermissionSource(func(string) (session.WaitingPermission, bool) {
		return session.WaitingPermission{}, false
	}, nil)
	t.Cleanup(func() { session.SetWaitingPermissionSource(nil, nil) })

	b.reaskPermission(context.Background(), inbound{chatID: 10, userID: 1}, "s1")

	if got := f.sends(); len(got) != 0 {
		t.Fatalf("sends = %d, want none", len(got))
	}
}

// TestReaskPermission_AsksTheWaitingPromptAndAnswersIt is the defect of 13.11: the
// turn started in the browser while the chat was bound to another conversation, so
// the chat was never asked and the window waited on a card nobody could press.
func TestReaskPermission_AsksTheWaitingPromptAndAnswersIt(t *testing.T) {
	b := newTestBot(&stubRunner{})
	f := newSendServer(t)
	b.setClient(f.client(t))
	p := waitingPermissionOf("s1", "tc1")
	var chosen atomic.Int64
	chosen.Store(-1)
	session.SetWaitingPermissionSource(
		func(string) (session.WaitingPermission, bool) { return p, true },
		func(sessionID string, choice int) bool {
			chosen.Store(int64(choice))

			return sessionID == "s1"
		},
	)
	t.Cleanup(func() { session.SetWaitingPermissionSource(nil, nil) })

	done := make(chan struct{})
	go func() {
		defer close(done)
		b.reaskPermission(context.Background(), inbound{chatID: 10, userID: 1}, "s1")
	}()

	f.waitSends(t, 1, 2*time.Second)
	sent := f.sends()[0]
	for _, want := range []string{"A — Разрешить", "B — Отклонить", "Run: write", "Записать файл pogoda/krasnodar-3dnya"} {
		if !strings.Contains(sent.body.Text, want) {
			t.Fatalf("card = %q, want %q in it", sent.body.Text, want)
		}
	}
	token := askTokenFrom(t, sent)
	if _, ok := b.asked.resolve(token, 1, 1); !ok {
		t.Fatal("the press of an option was not taken")
	}
	<-done

	if got := chosen.Load(); got != 1 {
		t.Fatalf("the answer carried choice %d, want 1", got)
	}
	edits := f.editCalls()
	if len(edits) != 1 || !strings.Contains(edits[0].body.Text, "✅ Отклонить") {
		t.Fatalf("edits = %+v, want the card settled with the chosen option", edits)
	}
}

// TestReaskPermission_AnsweredElsewhereTheCardCloses: the window answers while the
// card is up, and the card must stop offering options that lead nowhere.
func TestReaskPermission_AnsweredElsewhereTheCardCloses(t *testing.T) {
	b := newTestBot(&stubRunner{})
	b.reaskEvery = 20 * time.Millisecond
	f := newSendServer(t)
	b.setClient(f.client(t))
	var asked atomic.Bool
	session.SetWaitingPermissionSource(
		func(string) (session.WaitingPermission, bool) {
			if asked.Load() {
				return session.WaitingPermission{}, false
			}

			return waitingPermissionOf("s1", "tc1"), true
		},
		func(string, int) bool { return false },
	)
	t.Cleanup(func() { session.SetWaitingPermissionSource(nil, nil) })

	done := make(chan struct{})
	go func() {
		defer close(done)
		b.reaskPermission(context.Background(), inbound{chatID: 10, userID: 1}, "s1")
	}()

	f.waitSends(t, 1, 2*time.Second)
	asked.Store(true)
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("the card did not close after the prompt stopped waiting")
	}
	edits := f.editCalls()
	if len(edits) != 1 || !strings.Contains(edits[0].body.Text, reaskElsewhereNote) {
		t.Fatalf("edits = %+v, want the card closed with the answered-elsewhere note", edits)
	}
}
