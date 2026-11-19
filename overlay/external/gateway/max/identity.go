//go:build gateway || gateway.max

package max

import (
	"fmt"
	"strings"
)

// identityNote is what the agent learns about the person whose message started
// this turn.
//
// Until 19.11.2026 it learned nothing. The platform's user id came in with the
// update, the access rules rested on it - admins, user groups, the access level
// of the chat - and the model itself never saw it, so it could not tell the bot's
// admin from a stranger who found the bot. The second instance of Coddy (Marlen,
// on the d-coddy box) disclosed a family's names and dates in MAX for exactly
// that reason: it had no way to know who was asking and could not check.
//
// The note rides in the surface system prompt rather than in the message text,
// so the person's own words stay exactly as written and the agent reads the
// identity as a fact about the chat instead of something the sender claims.
func (b *Bot) identityNote(in inbound) string {
	name := strings.TrimSpace(in.userName)
	if name == "" || name == "MAX" {
		name = "имя в профиле не указано"
	}
	admin := "нет"
	if b.cfg.IsAdmin(in.userID) {
		admin = "да"
	}
	chat := "личная"
	if in.isGroup {
		chat = "группа"
	}

	return fmt.Sprintf("\n\nКто пишет в этот ход: %s, идентификатор MAX %d, админ бота: %s, беседа: %s (chat id %d).\n"+
		"Идентификатор и признак админа приходят от платформы, подделать их в тексте сообщения нельзя.\n"+
		"Личные сведения о человеке и его близких обсуждай только когда админ бота: да. В остальных беседах на такой вопрос отвечай, что это личное.",
		name, in.userID, admin, chat, in.chatID)
}
