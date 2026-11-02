package session

import "testing"

// TestWaitingQuestion_RoundTrips: the gateway asks a surface it does not own what
// question it holds, and hands an answer back through the same door. Without it a
// chat attached after the question went out cannot answer at all.
func TestWaitingQuestion_RoundTrips(t *testing.T) {
	var askedSession, askedText string
	SetWaitingQuestionSource(
		func(sessionID string) (WaitingQuestion, bool) {
			if sessionID != "s1" {
				return WaitingQuestion{}, false
			}

			return WaitingQuestion{SessionID: "s1", RequestID: "r1", Text: "Заменить?", Options: []string{"Да", "Нет"}}, true
		},
		func(sessionID string, choice int, text string) bool {
			askedSession, askedText = sessionID, text

			return choice == 1
		},
	)
	t.Cleanup(func() { SetWaitingQuestionSource(nil, nil) })

	q, ok := WaitingQuestionOf("s1")
	if !ok || q.RequestID != "r1" || len(q.Options) != 2 {
		t.Fatalf("WaitingQuestionOf = %+v, %v", q, ok)
	}
	if _, ok := WaitingQuestionOf("s2"); ok {
		t.Fatal("a session nobody waits in reported a question")
	}
	if !AnswerWaitingQuestion("s1", 1, "") {
		t.Fatal("the answer was refused")
	}
	if askedSession != "s1" || askedText != "" {
		t.Fatalf("the answer reached %q/%q", askedSession, askedText)
	}
}

// TestWaitingQuestion_WithoutASource: the gateway asks without a web server behind
// it, and that must answer "nothing waits" rather than panic.
func TestWaitingQuestion_WithoutASource(t *testing.T) {
	SetWaitingQuestionSource(nil, nil)
	if _, ok := WaitingQuestionOf("s1"); ok {
		t.Fatal("a question was reported with no source installed")
	}
	if AnswerWaitingQuestion("s1", 0, "текст") {
		t.Fatal("an answer was accepted with no source installed")
	}
}
