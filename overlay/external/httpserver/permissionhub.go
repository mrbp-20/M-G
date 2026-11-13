//go:build http

package httpserver

import (
	"strings"
	"sync"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

type permissionWaitKey struct {
	sessionID  string
	toolCallID string
}

var (
	permissionWaitsMu sync.Mutex
	permissionWaits   = make(map[permissionWaitKey]chan *acp.PermissionResult)
	// permissionParams is what each wait is about: a chat attached after the
	// prompt went out is asked again from here, so the shape of the prompt has to
	// outlive the event that carried it to the page.
	permissionParams = make(map[permissionWaitKey]acp.PermissionRequestParams)
)

// init gives the rest of the process a way to see and answer a permission prompt
// the page holds: the gateway asks about one a chat never saw, and hands its
// answer back through the very same channel the page uses.
func init() {
	session.SetWaitingPermissionSource(waitingPermission, answerWaitingPermission)
}

// waitingPermission turns the live wait of a session into the prompt a chat can
// show: the tool call it is about and the labels of its options.
func waitingPermission(sessionID string) (session.WaitingPermission, bool) {
	permissionWaitsMu.Lock()
	defer permissionWaitsMu.Unlock()
	for key, params := range permissionParams {
		if key.sessionID != sessionID || len(params.Options) == 0 {
			continue
		}
		labels := make([]string, len(params.Options))
		for i, opt := range params.Options {
			labels[i] = optionLabel(opt)
		}

		return session.WaitingPermission{
			SessionID:  key.sessionID,
			ToolCallID: key.toolCallID,
			Title:      strings.TrimSpace(params.ToolCall.Title),
			Text:       permissionBody(params),
			Options:    labels,
		}, true
	}

	return session.WaitingPermission{}, false
}

// answerWaitingPermission completes the wait of a session with the option a chat
// offered, exactly as a press in the page would have.
func answerWaitingPermission(sessionID string, choice int) bool {
	permissionWaitsMu.Lock()
	for key, params := range permissionParams {
		if key.sessionID != sessionID {
			continue
		}
		if choice < 0 || choice >= len(params.Options) {
			permissionWaitsMu.Unlock()

			return false
		}
		ch := permissionWaits[key]
		delete(permissionWaits, key)
		delete(permissionParams, key)
		permissionWaitsMu.Unlock()
		if ch == nil {
			return false
		}
		ch <- &acp.PermissionResult{Outcome: "selected", OptionID: params.Options[choice].OptionID}

		return true
	}
	permissionWaitsMu.Unlock()

	return false
}

// optionLabel is what the person reads on a button: the name of the option, or
// its identifier when the request gave no name.
func optionLabel(opt acp.PermissionOption) string {
	if name := strings.TrimSpace(opt.Name); name != "" {
		return name
	}

	return strings.TrimSpace(opt.OptionID)
}

// permissionBody is the part of the prompt that shows what is being asked about:
// the text of the tool call's first content block.
func permissionBody(params acp.PermissionRequestParams) string {
	if len(params.ToolCall.Content) == 0 {
		return ""
	}

	return strings.TrimSpace(params.ToolCall.Content[0].Content.Text)
}

// rememberPermission keeps what the wait is about, so a chat attached later can
// be asked the same prompt.
func rememberPermission(params acp.PermissionRequestParams) {
	permissionWaitsMu.Lock()
	defer permissionWaitsMu.Unlock()
	k := permissionWaitKey{sessionID: params.SessionID, toolCallID: params.ToolCall.ToolCallID}
	permissionParams[k] = params
}

func registerPermissionWait(sessionID, toolCallID, sessionDir string) <-chan *acp.PermissionResult {
	permissionWaitsMu.Lock()
	defer permissionWaitsMu.Unlock()
	k := permissionWaitKey{sessionID: sessionID, toolCallID: toolCallID}
	ch := make(chan *acp.PermissionResult, 1)
	permissionWaits[k] = ch
	_ = sessionDir
	return ch
}

func unregisterPermissionWait(sessionID, toolCallID, sessionDir string) {
	permissionWaitsMu.Lock()
	defer permissionWaitsMu.Unlock()
	delete(permissionWaits, permissionWaitKey{sessionID: sessionID, toolCallID: toolCallID})
	_ = sessionDir
}

// CompletePermissionAnswer resolves a pending HTTP/streaming permission prompt.
func CompletePermissionAnswer(sessionID, toolCallID string, res *acp.PermissionResult) bool {
	permissionWaitsMu.Lock()
	defer permissionWaitsMu.Unlock()
	k := permissionWaitKey{sessionID: sessionID, toolCallID: toolCallID}
	ch, ok := permissionWaits[k]
	if !ok {
		return false
	}
	delete(permissionWaits, k)
	ch <- res
	return true
}
