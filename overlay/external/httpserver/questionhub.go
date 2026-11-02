//go:build http

package httpserver

import (
	"strings"
	"sync"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

type questionWaitKey struct {
	sessionID string
	requestID string
}

var (
	questionWaitsMu sync.Mutex
	questionWaits   = make(map[questionWaitKey]chan *acp.QuestionResult)
	// questionParams is what each wait is about: a chat attached after the question
	// went out is asked again from here, so the shape of the question has to
	// outlive the event that carried it to the page.
	questionParams = make(map[questionWaitKey]acp.QuestionRequestParams)
)

// init gives the rest of the process a way to see and answer a wait the page holds:
// the gateway asks about a question a chat never saw, and hands its answer back
// through the very same channel the page uses.
func init() {
	session.SetWaitingQuestionSource(waitingQuestion, answerWaitingQuestion)
}

// waitingQuestion turns the live wait of a session into the question a chat can
// show: the first question of the request, with the labels of its options.
func waitingQuestion(sessionID string) (session.WaitingQuestion, bool) {
	questionWaitsMu.Lock()
	defer questionWaitsMu.Unlock()
	for key, params := range questionParams {
		if key.sessionID != sessionID || len(params.Questions) == 0 {
			continue
		}
		q := params.Questions[0]
		labels := make([]string, len(q.Options))
		for i, opt := range q.Options {
			labels[i] = opt.Label
		}

		return session.WaitingQuestion{
			SessionID: key.sessionID,
			RequestID: key.requestID,
			Header:    q.Header,
			Text:      q.Question,
			Options:   labels,
			Custom:    q.Custom,
		}, true
	}

	return session.WaitingQuestion{}, false
}

// answerWaitingQuestion completes the wait of a session with an answer that came
// from another surface: the option a chat offered, or the text somebody wrote.
func answerWaitingQuestion(sessionID string, choice int, text string) bool {
	questionWaitsMu.Lock()
	for key, params := range questionParams {
		if key.sessionID != sessionID || len(params.Questions) == 0 {
			continue
		}
		q := params.Questions[0]
		answer := strings.TrimSpace(text)
		if answer == "" {
			if choice < 0 || choice >= len(q.Options) {
				questionWaitsMu.Unlock()

				return false
			}
			answer = q.Options[choice].Label
		}
		ch := questionWaits[key]
		delete(questionWaits, key)
		delete(questionParams, key)
		questionWaitsMu.Unlock()
		if ch == nil {
			return false
		}
		ch <- &acp.QuestionResult{Answers: [][]string{{answer}}}

		return true
	}
	questionWaitsMu.Unlock()

	return false
}

func registerQuestionWait(sessionID, requestID string) <-chan *acp.QuestionResult {
	questionWaitsMu.Lock()
	defer questionWaitsMu.Unlock()
	k := questionWaitKey{sessionID: sessionID, requestID: requestID}
	ch := make(chan *acp.QuestionResult, 1)
	questionWaits[k] = ch
	return ch
}

// rememberQuestion keeps what the wait is about, so a chat attached later can be
// asked the same question.
func rememberQuestion(params acp.QuestionRequestParams) {
	questionWaitsMu.Lock()
	defer questionWaitsMu.Unlock()
	k := questionWaitKey{sessionID: params.SessionID, requestID: params.RequestID}
	questionParams[k] = params
}

func unregisterQuestionWait(sessionID, requestID string) {
	questionWaitsMu.Lock()
	defer questionWaitsMu.Unlock()
	k := questionWaitKey{sessionID: sessionID, requestID: requestID}
	delete(questionWaits, k)
	delete(questionParams, k)
}

// QuestionPending reports whether any HTTP/streaming question waits for a
// response in this session. It lets session listings expose the same prompt
// state to History that the live composer receives on its stream.
func QuestionPending(sessionID string) bool {
	questionWaitsMu.Lock()
	defer questionWaitsMu.Unlock()
	for key := range questionWaits {
		if key.sessionID == sessionID {
			return true
		}
	}
	return false
}

// CompleteQuestionAnswer resolves a pending HTTP/streaming question. Returns false if nothing was waiting.
func CompleteQuestionAnswer(sessionID, requestID string, res *acp.QuestionResult) bool {
	questionWaitsMu.Lock()
	defer questionWaitsMu.Unlock()
	k := questionWaitKey{sessionID: sessionID, requestID: requestID}
	ch, ok := questionWaits[k]
	if !ok {
		return false
	}
	delete(questionWaits, k)
	ch <- res
	return true
}
