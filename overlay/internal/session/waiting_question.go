package session

import "sync"

// WaitingQuestion is a question some surface is holding open for an answer. The
// surface that asked it owns the wait; everything else only needs to know that it
// is there, because a chat attached after the question went out has heard nothing
// about it - the person who switches the conversation in a messenger then meets a
// conversation that is busy with a question they cannot even see.
type WaitingQuestion struct {
	SessionID string
	RequestID string
	Header    string
	Text      string
	Options   []string
	Custom    bool
}

var (
	waitingMu    sync.RWMutex
	waitingGet   func(sessionID string) (WaitingQuestion, bool)
	waitingReply func(sessionID string, choice int, text string) bool
)

// SetWaitingQuestionSource installs the surface that knows its own waits. The web
// server holds them, and it is the only place able to hand an answer to one.
func SetWaitingQuestionSource(get func(sessionID string) (WaitingQuestion, bool), answer func(sessionID string, choice int, text string) bool) {
	waitingMu.Lock()
	defer waitingMu.Unlock()
	waitingGet = get
	waitingReply = answer
}

// WaitingQuestionOf reports the question this session is waiting for, if any.
func WaitingQuestionOf(sessionID string) (WaitingQuestion, bool) {
	waitingMu.RLock()
	get := waitingGet
	waitingMu.RUnlock()
	if get == nil || sessionID == "" {
		return WaitingQuestion{}, false
	}

	return get(sessionID)
}

// AnswerWaitingQuestion hands an answer to the wait: the text a person wrote, or
// else the option at choice. It reports false when nothing waits any more - the
// answer came too late, because the question was answered on the other surface.
func AnswerWaitingQuestion(sessionID string, choice int, text string) bool {
	waitingMu.RLock()
	answer := waitingReply
	waitingMu.RUnlock()
	if answer == nil || sessionID == "" {
		return false
	}

	return answer(sessionID, choice, text)
}
