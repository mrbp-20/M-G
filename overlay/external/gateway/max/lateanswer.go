//go:build gateway || gateway.max

package max

import (
	"context"
)

// An answer can be written by a turn this chat never asked for: another window
// opened the conversation, or a background task woke the agent. The answer is
// taken from the conversation itself and posted into the chats that hold it -
// that is chatwatch.go, which lives as long as the bot rather than for one
// switch.
//
// What stays here is the promise made to the person at the moment of a switch:
// the chat is told where the answer will appear, because a turn that runs in
// another window delivers itself there and nowhere else. The note is said only
// when the turn really runs elsewhere - calling a finished conversation busy
// sends the person to wait for nothing.
const noteTurnElsewhere = "Ответ появится здесь, как только ход закончится: он идёт в другом окне."

// watchSwitch answers the note a chat gets when the switch landed on a
// conversation that runs a turn this bot did not start, and makes sure that
// conversation is watched. It reports an empty string when there is nothing to
// promise: our own turn delivers itself and a finished conversation has nothing
// to wait for.
func (b *Bot) watchSwitch(ctx context.Context, in inbound, key, sessionID string) string {
	if !b.turnActive(sessionID) || b.turnOurs(key) {
		return ""
	}
	b.armWatcher(b.watchContext(), sessionID)

	return noteTurnElsewhere
}
