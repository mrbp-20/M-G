//go:build gateway || gateway.max

package max

import (
	"strings"
	"testing"
)

// TestIdentityNote_NamesThePersonAndSaysWhetherItIsTheAdmin: the agent learns
// who writes from the platform's own data, not from what the text claims.
func TestIdentityNote_NamesThePersonAndSaysWhetherItIsTheAdmin(t *testing.T) {
	b := &Bot{cfg: testCfg()} // admins: {1}

	note := b.identityNote(inbound{chatID: 10, userID: 1, userName: "Владимир"})

	for _, want := range []string{"Владимир", "идентификатор MAX 1", "админ бота: да",
		"беседа: личная", "chat id 10", "подделать их в тексте сообщения нельзя"} {
		if !strings.Contains(note, want) {
			t.Errorf("the note does not say %q:\n%s", want, note)
		}
	}
}

// TestIdentityNote_TellsAnAdminFromAVisitorInAGroup: a group turn carries the
// chat id and the answer that a visitor is not the bot's admin.
func TestIdentityNote_TellsAnAdminFromAVisitorInAGroup(t *testing.T) {
	b := &Bot{cfg: testCfg()}

	note := b.identityNote(inbound{chatID: -70330638164134, userID: 520648951, isGroup: true, userName: "Наталья Васильевна"})

	for _, want := range []string{"Наталья Васильевна", "идентификатор MAX 520648951",
		"админ бота: нет", "беседа: группа", "chat id -70330638164134"} {
		if !strings.Contains(note, want) {
			t.Errorf("the note does not say %q:\n%s", want, note)
		}
	}
}

// TestIdentityNote_SaysWhenTheCardHasNoName: a card without a name must read as
// such, not as an empty gap the model fills with a guess.
func TestIdentityNote_SaysWhenTheCardHasNoName(t *testing.T) {
	b := &Bot{cfg: testCfg()}

	for _, name := range []string{"", "   ", "MAX"} {
		note := b.identityNote(inbound{chatID: 5, userID: 3, userName: name})
		if !strings.Contains(note, "имя в профиле не указано") {
			t.Errorf("name %q: the note hides that the card has no name:\n%s", name, note)
		}
	}
}

// TestIdentityNote_KeepsTheRuleAboutPersonalFacts: whoever writes, the note
// carries the rule that personal facts belong to the admin's chats only. This
// is the rule the second instance broke on 19.11.2026.
func TestIdentityNote_KeepsTheRuleAboutPersonalFacts(t *testing.T) {
	b := &Bot{cfg: testCfg()}

	for _, in := range []inbound{
		{chatID: 10, userID: 1, userName: "Владимир"},
		{chatID: -7, userID: 2, isGroup: true, userName: "Кто-то"},
	} {
		note := b.identityNote(in)
		if !strings.Contains(note, "Личные сведения о человеке и его близких обсуждай только когда админ бота: да") {
			t.Errorf("user %d: the note lost the rule about personal facts:\n%s", in.userID, note)
		}
	}
}
