package session

import "sync"

// WaitingPermission is a permission prompt some surface holds open for an answer.
// The surface that asked it owns the wait; everything else only needs to know that
// it is there, because a chat attached after the prompt went out has heard nothing
// about it - the person who switches the conversation in a messenger then meets a
// turn standing still on a permission they cannot even see, and the only way out is
// to answer it in the window they may not have open.
//
// It is the same story as WaitingQuestion, and it exists for the same reason: the
// surfaces a turn is mirrored into are asked at the moment the prompt is raised, so
// one attached later has to be told about it separately.
type WaitingPermission struct {
	SessionID  string
	ToolCallID string
	Title      string
	Text       string
	Options    []string
}

var (
	permissionWaitMu    sync.RWMutex
	permissionWaitGet   func(sessionID string) (WaitingPermission, bool)
	permissionWaitReply func(sessionID string, choice int) bool
)

// SetWaitingPermissionSource installs the surface that knows its own waits. The
// web server holds them, and it is the only place able to hand an answer to one.
func SetWaitingPermissionSource(get func(sessionID string) (WaitingPermission, bool), answer func(sessionID string, choice int) bool) {
	permissionWaitMu.Lock()
	defer permissionWaitMu.Unlock()
	permissionWaitGet = get
	permissionWaitReply = answer
}

// WaitingPermissionOf reports the prompt this session is waiting for, if any.
func WaitingPermissionOf(sessionID string) (WaitingPermission, bool) {
	permissionWaitMu.RLock()
	get := permissionWaitGet
	permissionWaitMu.RUnlock()
	if get == nil || sessionID == "" {
		return WaitingPermission{}, false
	}

	return get(sessionID)
}

// AnswerWaitingPermission hands the option at choice to the wait. It reports false
// when nothing waits any more - the answer came too late, because the prompt was
// answered on the other surface.
func AnswerWaitingPermission(sessionID string, choice int) bool {
	permissionWaitMu.RLock()
	answer := permissionWaitReply
	permissionWaitMu.RUnlock()
	if answer == nil || sessionID == "" {
		return false
	}

	return answer(sessionID, choice)
}
