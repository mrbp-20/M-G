package session

import (
	"context"
	"errors"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
)

// ErrAnsweredElsewhere marks a prompt withdrawn because the same question was
// answered on another surface. It travels as the cause of the context the losing
// prompt waits on, so the surface that lost the race says somebody answered
// elsewhere instead of going quiet under a keyboard nobody can use any more.
//
// One value for every surface: the chat gateways and the browser bridge ask the
// same question of the same person, and a prompt must read the same way wherever
// it gave way.
var ErrAnsweredElsewhere = errors.New("answered on another surface")

// AnsweredQuestion tells an answer from a question nobody has answered yet. A
// question without options posts its text and contributes no answer, and an
// empty result is what a surface that gave way returns: neither ends the wait.
func AnsweredQuestion(res *acp.QuestionResult) bool {
	if res == nil {
		return false
	}
	for _, answer := range res.Answers {
		if len(answer) > 0 {
			return true
		}
	}

	return false
}

// PressedPermission tells an answer from a refusal. A surface that shows no
// prompt refuses at once - a tool call a subagent may not run, a prompt nobody
// could have seen - and that must not keep the surface somebody is reading from
// asking.
func PressedPermission(res *acp.PermissionResult) bool {
	return res != nil && res.Outcome != "" && res.Outcome != "cancelled"
}

// Withdrawal carries the reason a prompt ended: the cause of a context cancelled
// because another surface answered, and nil for any other end.
func Withdrawal(ctx context.Context) error {
	if errors.Is(context.Cause(ctx), ErrAnsweredElsewhere) {
		return ErrAnsweredElsewhere
	}

	return nil
}
