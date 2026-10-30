//go:build gateway || gateway.max

package max

// The resume menu lists every session the bot has and binds the chat to the one
// that is pressed, so it belongs to the admins on every surface - a personal
// chat is not a safe exception, because the chat is bound to whatever session
// is chosen there (resume.go, dispatch.go). A button outlives the turn that drew
// it, and the access of the person who presses it is checked again at that
// moment, not only when the message came in (sender.go).

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
)

// pressAsUser is a message_callback update pressed by someone other than the
// admin of the fixture.
func pressAsUser(t *testing.T, userID int64, payload, mid string) Update {
	t.Helper()
	cb := Callback{CallbackID: "cb9", Payload: payload, User: User{UserID: userID, FirstName: "Bob"}}
	raw, err := json.Marshal(cb)
	if err != nil {
		t.Fatalf("marshal the callback: %v", err)
	}
	body, err := json.Marshal(map[string]any{
		"sender":    map[string]any{"user_id": userID, "name": "Bob"},
		"recipient": map[string]any{"chat_id": 10, "chat_type": "dialog"},
		"body":      map[string]any{"mid": mid, "text": ""},
	})
	if err != nil {
		t.Fatalf("marshal the message: %v", err)
	}

	return Update{
		UpdateType: "message_callback",
		UserID:     userID,
		User:       &cb.User,
		Callback:   raw,
		Message:    body,
	}
}

// TestResumeCallback_IsAdminOnlyInADirectChat: a member who presses a resume
// button must not be shown the list of the bot's conversations, and must not
// move the chat.
func TestResumeCallback_IsAdminOnlyInADirectChat(t *testing.T) {
	r := newResumeRunner(resumeRows(3))
	b, f := newResumeBot(t, r)
	// An open bot: the member passes the access check, so what stops them is
	// the admin-only rule of /resume, not the ACL in front of it.
	b.cfg.DefaultAccess = config.AccessAll
	c := b.connectedClient()

	if err := b.HandleCallback(context.Background(), c, pressAsUser(t, 2, "resume:p:1", "mid9")); err != nil {
		t.Fatalf("callback: %v", err)
	}
	if edits := f.editCalls(); len(edits) != 0 {
		t.Errorf("%d edits, want the menu refused for a member", len(edits))
	}
	texts := sentTexts(f)
	if len(texts) != 1 || !strings.Contains(texts[0], adminOnlyNote) {
		t.Fatalf("sent %q, want the admin-only note", texts)
	}
}

// TestResumeCommand_IsAdminOnlyInADirectChat: the typed command is refused to a
// member even where the chat is not shared.
func TestResumeCommand_IsAdminOnlyInADirectChat(t *testing.T) {
	r := newResumeRunner(resumeRows(3))
	b, f := newResumeBot(t, r)
	key := b.sessionKey(inbound{chatID: 10, userID: 2})

	b.cfg.DefaultAccess = config.AccessAll
	b.processMessage(context.Background(), inbound{
		chatID: 10, userID: 2, command: "resume", text: "/resume",
	}, key)

	texts := sentTexts(f)
	if len(texts) != 1 || !strings.Contains(texts[0], adminOnlyNote) {
		t.Fatalf("sent %q, want the admin-only note", texts)
	}
}

// TestCallback_AccessIsCheckedAgainOnEveryPress: access withdrawn after the
// keyboard was drawn closes the keyboard too. The press is confirmed, so the
// messenger's button stops spinning, and nothing else happens.
func TestCallback_AccessIsCheckedAgainOnEveryPress(t *testing.T) {
	f := newSendServer(t)
	cfg := testCfg() // admins only: user 1 yes, user 2 no
	b := New(cfg, newStubRunner(), "", discardLog(), "", "", nil)
	b.setClient(f.client(t))

	if err := b.HandleCallback(context.Background(), b.connectedClient(),
		pressAsUser(t, 2, callbackActionModel+":openai/gpt-4o", "mid5")); err != nil {
		t.Fatalf("callback: %v", err)
	}
	if got := f.answered(); len(got) != 1 {
		t.Fatalf("%d callbacks confirmed, want the press acknowledged once", len(got))
	}
	if got := b.store.LastModel(); got != "" {
		t.Errorf("last model = %q, want the press of a member to change nothing", got)
	}
	// The admin's own press still works on the same keyboard.
	if err := b.HandleCallback(context.Background(), b.connectedClient(),
		pressAsUser(t, 1, callbackActionModel+":openai/gpt-4o", "mid6")); err != nil {
		t.Fatalf("admin callback: %v", err)
	}
	if got := b.store.LastModel(); got != "openai/gpt-4o" {
		t.Errorf("last model = %q, want the admin's pick taken", got)
	}
}
