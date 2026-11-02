//go:build gateway || gateway.max

package max

import (
	"context"
	"encoding/json"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// askTokenFrom reads the token the option buttons of a sent card carry.
func askTokenFrom(t *testing.T, call wireCall) string {
	t.Helper()
	var attachments []struct {
		Payload struct {
			Buttons [][]struct {
				Payload string `json:"payload"`
			} `json:"buttons"`
		} `json:"payload"`
	}
	if err := json.Unmarshal(call.body.Attachments, &attachments); err != nil {
		t.Fatalf("attachments: %v", err)
	}
	for _, a := range attachments {
		for _, row := range a.Payload.Buttons {
			for _, b := range row {
				if !strings.HasPrefix(b.Payload, actionAsk+":") {
					continue
				}
				if parts := strings.Split(b.Payload, ":"); len(parts) >= 2 {
					return parts[1]
				}
			}
		}
	}
	t.Fatalf("no option button in %q", call.body.Text)

	return ""
}

// waitingQuestionOf builds the question the page holds while a chat attaches.
func waitingQuestionOf(sessionID, requestID string) session.WaitingQuestion {
	return session.WaitingQuestion{
		SessionID: sessionID,
		RequestID: requestID,
		Header:    "Замена файла",
		Text:      "Заменить версию из Downloads?",
		Options:   []string{"Да - заменить", "Пока не менять"},
		Custom:    true,
	}
}

// TestReask_NothingWaitingMeansNoCard: a switch into a conversation that waits for
// no answer stays as it was - one confirmation, no question invented.
func TestReask_NothingWaitingMeansNoCard(t *testing.T) {
	b := newTestBot(&stubRunner{})
	f := newSendServer(t)
	b.setClient(f.client(t))
	session.SetWaitingQuestionSource(func(string) (session.WaitingQuestion, bool) {
		return session.WaitingQuestion{}, false
	}, nil)
	t.Cleanup(func() { session.SetWaitingQuestionSource(nil, nil) })

	b.reaskQuestion(context.Background(), inbound{chatID: 10, userID: 1}, "s1")

	if got := f.sends(); len(got) != 0 {
		t.Fatalf("sends = %d, want none", len(got))
	}
}

// TestReask_AsksTheWaitingQuestionAndAnswersIt is the defect of 02.11: the question
// was asked in the browser before the chat was attached, so the chat heard nothing
// and the person could only meet "the conversation is busy".
func TestReask_AsksTheWaitingQuestionAndAnswersIt(t *testing.T) {
	b := newTestBot(&stubRunner{})
	f := newSendServer(t)
	b.setClient(f.client(t))
	q := waitingQuestionOf("s1", "r1")
	var chosen atomic.Int64
	chosen.Store(-1)
	session.SetWaitingQuestionSource(
		func(string) (session.WaitingQuestion, bool) { return q, true },
		func(sessionID string, choice int, text string) bool {
			chosen.Store(int64(choice))

			return sessionID == "s1" && text == ""
		},
	)
	t.Cleanup(func() { session.SetWaitingQuestionSource(nil, nil) })

	done := make(chan struct{})
	go func() {
		defer close(done)
		b.reaskQuestion(context.Background(), inbound{chatID: 10, userID: 1}, "s1")
	}()

	f.waitSends(t, 1, 2*time.Second)
	sent := f.sends()[0]
	for _, want := range []string{"A — Да - заменить", "B — Пока не менять", "Заменить версию из Downloads?"} {
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
	if len(edits) != 1 || !strings.Contains(edits[0].body.Text, "✅ Пока не менять") {
		t.Fatalf("edits = %+v, want the card settled with the chosen option", edits)
	}
}

// TestReask_AnsweredElsewhereTheCardCloses: the browser answers while the card is
// still up, and the card must stop offering options that lead nowhere.
func TestReask_AnsweredElsewhereTheCardCloses(t *testing.T) {
	b := newTestBot(&stubRunner{})
	b.reaskEvery = 20 * time.Millisecond
	f := newSendServer(t)
	b.setClient(f.client(t))
	q := waitingQuestionOf("s2", "r2")
	var waiting atomic.Bool
	waiting.Store(true)
	session.SetWaitingQuestionSource(
		func(string) (session.WaitingQuestion, bool) {
			if !waiting.Load() {
				return session.WaitingQuestion{}, false
			}

			return q, true
		},
		func(string, int, string) bool { return true },
	)
	t.Cleanup(func() { session.SetWaitingQuestionSource(nil, nil) })

	done := make(chan struct{})
	go func() {
		defer close(done)
		b.reaskQuestion(context.Background(), inbound{chatID: 10, userID: 1}, "s2")
	}()

	f.waitSends(t, 1, 2*time.Second)
	waiting.Store(false)
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("the card kept waiting for a question that ended")
	}
	edits := f.editCalls()
	if len(edits) != 1 || !strings.Contains(edits[0].body.Text, reaskElsewhereNote) {
		t.Fatalf("edits = %+v, want the card closed as answered elsewhere", edits)
	}
}

// TestReask_TheSameQuestionIsNotAskedTwice: a second switch into the same
// conversation must not post a second card for the same question.
func TestReask_TheSameQuestionIsNotAskedTwice(t *testing.T) {
	b := newTestBot(&stubRunner{})
	f := newSendServer(t)
	b.setClient(f.client(t))
	q := waitingQuestionOf("s3", "r3")
	session.SetWaitingQuestionSource(
		func(string) (session.WaitingQuestion, bool) { return q, true },
		func(string, int, string) bool { return false },
	)
	t.Cleanup(func() { session.SetWaitingQuestionSource(nil, nil) })
	if b.reasks == nil {
		b.reasks = newSeenIDs(64)
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		b.reaskQuestion(context.Background(), inbound{chatID: 10, userID: 1}, "s3")
	}()
	f.waitSends(t, 1, 2*time.Second)
	// A second switch for the same question, in the same chat, posts nothing.
	b.reaskQuestion(context.Background(), inbound{chatID: 10, userID: 1}, "s3")
	if got := f.sends(); len(got) != 1 {
		t.Fatalf("sends = %d, want one card for one question", len(got))
	}
	b.asked.resolve(askTokenFrom(t, f.sends()[0]), 0, 1)
	<-done
}
