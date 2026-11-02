//go:build http

package httpserver

import (
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
)

// questionParamsOf builds the request the composer sends for one question.
func questionParamsOf(sessionID, requestID string) acp.QuestionRequestParams {
	return acp.QuestionRequestParams{
		SessionID: sessionID,
		RequestID: requestID,
		Questions: []acp.QuestionPrompt{{
			Header:   "Замена файла",
			Question: "Заменить версию из Downloads?",
			Options: []acp.QuestionOption{
				{Label: "Да - заменить"},
				{Label: "Пока не менять"},
			},
			Custom: true,
		}},
	}
}

// TestWaitingQuestion_IsKnownWhileItWaits: a chat attached after the question went
// out has to be able to ask about it, so the wait keeps what it is about.
func TestWaitingQuestion_IsKnownWhileItWaits(t *testing.T) {
	params := questionParamsOf("s1", "r1")
	ch := registerQuestionWait(params.SessionID, params.RequestID)
	rememberQuestion(params)
	t.Cleanup(func() { unregisterQuestionWait(params.SessionID, params.RequestID) })

	q, ok := waitingQuestion("s1")
	if !ok {
		t.Fatal("the waiting question was not found")
	}
	if q.RequestID != "r1" || q.Text != params.Questions[0].Question || len(q.Options) != 2 {
		t.Fatalf("the question came back as %+v", q)
	}
	if !q.Custom {
		t.Fatal("the question lost its free-text answer")
	}
	if _, ok := waitingQuestion("s2"); ok {
		t.Fatal("a session with no wait reported a question")
	}

	// An answer from the other surface completes the very wait the page holds.
	if !answerWaitingQuestion("s1", 1, "") {
		t.Fatal("the answer was refused")
	}
	res := <-ch
	if len(res.Answers) != 1 || len(res.Answers[0]) != 1 || res.Answers[0][0] != "Пока не менять" {
		t.Fatalf("the page got %+v, want the label of the chosen option", res)
	}
	if _, ok := waitingQuestion("s1"); ok {
		t.Fatal("the answered question is still reported as waiting")
	}
}

// TestWaitingQuestion_WrittenAnswerIsTaken: the person may write the answer instead
// of pressing an option, and the page gets that text as it was written.
func TestWaitingQuestion_WrittenAnswerIsTaken(t *testing.T) {
	params := questionParamsOf("s3", "r3")
	ch := registerQuestionWait(params.SessionID, params.RequestID)
	rememberQuestion(params)
	t.Cleanup(func() { unregisterQuestionWait(params.SessionID, params.RequestID) })

	if !answerWaitingQuestion("s3", -1, "оставить как есть") {
		t.Fatal("the written answer was refused")
	}
	res := <-ch
	if len(res.Answers) != 1 || res.Answers[0][0] != "оставить как есть" {
		t.Fatalf("the page got %+v, want the written text", res)
	}
}

// TestWaitingQuestion_AnswerAfterTheWaitEnded: a late answer changes nothing, which
// is what keeps the card of a question answered elsewhere from failing.
func TestWaitingQuestion_AnswerAfterTheWaitEnded(t *testing.T) {
	if answerWaitingQuestion("s9", 0, "") {
		t.Fatal("an answer to nothing was accepted")
	}
}
