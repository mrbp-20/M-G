//go:build gateway || gateway.max

package max

import (
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// A direct chat promises no directory: the gateway's own is only where a new
// conversation starts. Enforcing it like a promise replaced a conversation
// opened with /resume by an empty one twenty seconds later (25.11.2026).
func TestTurnDir_ADirectChatPromisesNoDirectory(t *testing.T) {
	b := New(testCfg(), newStubRunner(), "/общий/каталог", slog.New(slog.DiscardHandler), "", "", nil)

	dir, promised, err := b.turnDir(context.Background(), 10, false)
	if err != nil {
		t.Fatalf("turnDir: %v", err)
	}
	if dir != "/общий/каталог" {
		t.Errorf("dir = %q, want the gateway's own", dir)
	}
	if promised {
		t.Error("a direct chat must not promise a directory")
	}
}

// The conversation /resume opened keeps its own directory: the chat's default is
// not a reason to throw it away.
func TestEnsureSession_KeepsTheConversationResumeOpened(t *testing.T) {
	r := newStubRunner()
	elsewhere := "/srv/data/1C Price/2026"
	r.live["sess_limit"] = &session.State{ID: "sess_limit", CWD: elsewhere, Mode: session.ModeAgent}
	b := New(testCfg(), r, "/srv/work", slog.New(slog.DiscardHandler), "", "", nil)
	key := "max:user:19527334"
	b.store.Bind(key, "sess_limit")

	st, err := b.ensureSession(context.Background(), key, "/srv/work", false)
	if err != nil {
		t.Fatalf("ensureSession: %v", err)
	}
	if st.ID != "sess_limit" {
		t.Errorf("session = %q, want the resumed one kept", st.ID)
	}
	if got := b.store.Get(key); got != "sess_limit" {
		t.Errorf("binding = %q, want the resumed one kept", got)
	}
}

// A group whose workspace the admin set still gets its own directory: there the
// promise is real, and the conversation has to be replaced.
func TestEnsureSession_ReplacesWhenTheChatPromisedTheDirectory(t *testing.T) {
	r := newStubRunner()
	r.live["sess_old"] = &session.State{ID: "sess_old", CWD: "/старое/место", Mode: session.ModeAgent}
	b := New(testCfg(), r, "/srv/work", slog.New(slog.DiscardHandler), "", "", nil)
	key := "max:chat:-7"
	b.store.Bind(key, "sess_old")
	dir := t.TempDir()

	st, err := b.ensureSession(context.Background(), key, dir, true)
	if err != nil {
		t.Fatalf("ensureSession: %v", err)
	}
	if st.ID == "sess_old" {
		t.Error("the conversation kept a directory the chat promised to another")
	}
	kept, err := r.EnsureHTTPSession(context.Background(), st.ID, "")
	if err != nil {
		t.Fatalf("EnsureHTTPSession: %v", err)
	}
	if got := kept.GetCWD(); got != dir {
		t.Errorf("new session works in %q, want %q", got, dir)
	}
}

func TestPlainText_DropsWhatAMessengerCannotRender(t *testing.T) {
	cases := []struct{ in, want string }{
		{"## Объём\nПоказатель", "Объём\nПоказатель"},
		{"В `L_POOLS` ровно одна", "В L_POOLS ровно одна"},
		{"видеть **все** варианты", "видеть все варианты"},
		{"```go\nfmt.Println()\n```", "fmt.Println()"},
		{"L_POOLS остаётся именем", "L_POOLS остаётся именем"},
		{"#хештег не заголовок", "#хештег не заголовок"},
		{"", ""},
	}
	for _, c := range cases {
		if got := plainText(c.in); got != c.want {
			t.Errorf("plainText(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestHistoryCard_CarriesNoMarkers(t *testing.T) {
	msgs := []llm.Message{
		{Role: llm.RoleUser, Content: "покажи `170625`", CreatedAt: time.Now().Format(time.RFC3339)},
		{Role: llm.RoleAssistant, Content: "## Объём\n\nВ `L_POOLS` **одна** коллекция", CreatedAt: time.Now().Format(time.RFC3339)},
	}
	card := historyCard("LIMPID POOLS Химия", msgs)
	for _, marker := range []string{"`", "**", "##"} {
		if strings.Contains(card, marker) {
			t.Errorf("card carries %q: %q", marker, card)
		}
	}
	if !strings.Contains(card, "L_POOLS") {
		t.Error("card lost the words it was carrying")
	}
}
