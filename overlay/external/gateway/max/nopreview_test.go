//go:build gateway || gateway.max

package max

// Unit tests of the guard against MAX's link previews. MAX reads a file name as
// a host name and attaches a card for it: README.md became a preview of
// http://readme.md, a parked domain that serves ad spam, so a chat full of tool
// calls filled with cards nobody sent. The guard puts a zero-width space after
// the dot, which a reader cannot see.

import (
	"context"
	"strings"
	"testing"
)

func TestQuietLinks_DefusesHostLikeWords(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"a file name", "README.md", "README." + zeroWidth + "md"},
		{"a name after a word", "см. burst.go", "см. burst." + zeroWidth + "go"},
		{"a name inside a path", "cmd/coddy/serve.go", "cmd/coddy/serve." + zeroWidth + "go"},
		{"two names", "bot.go, client.go", "bot." + zeroWidth + "go, client." + zeroWidth + "go"},
		{"a name with a line number", "README.md:12", "README." + zeroWidth + "md:12"},
		{"a name in a command", "grep -n '^## ' README.ru.md | cut -c1-80",
			"grep -n '^## ' README." + zeroWidth + "ru." + zeroWidth + "md | cut -c1-80"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := quietLinks(c.in); got != c.want {
				t.Fatalf("quietLinks(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

func TestQuietLinks_LeavesWhatIsNotAHost(t *testing.T) {
	cases := []struct {
		name string
		in   string
	}{
		{"a version", "1.3.17-max.46"},
		{"a decimal", "ждём 2.5 s"},
		{"a sentence", "Готово. Дальше по плану."},
		{"no dot at all", "Привет! Пришлите вопрос или задачу"},
		{"a link somebody meant", "see https://github.com/mrbp-20/MX-Gate-Code now"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := quietLinks(c.in); got != c.in {
				t.Fatalf("quietLinks(%q) = %q, it must not change", c.in, got)
			}
		})
	}
}

func TestQuietLinks_A_LinkKeepsItsWordsWhileANameBesideItLosesTheirs(t *testing.T) {
	got := quietLinks("https://example.com/a.md and README.md")
	want := "https://example.com/a.md and README." + zeroWidth + "md"
	if got != want {
		t.Fatalf("quietLinks = %q, want %q", got, want)
	}
}

func TestClient_Send_TakesTheHostReadingOut(t *testing.T) {
	f := newSendServer(t)
	c := f.client(t)

	if _, err := c.Send(context.Background(), Outgoing{Target: Target{ChatID: 1}, Text: "⚙️ cat README.md"}); err != nil {
		t.Fatalf("send: %v", err)
	}
	sent := f.sends()
	if len(sent) != 1 {
		t.Fatalf("sends = %d, want 1", len(sent))
	}
	if text := sent[0].body.Text; strings.Contains(text, "README.md") || !strings.Contains(text, "README."+zeroWidth+"md") {
		t.Fatalf("the text reached the wire as %q, so MAX would preview it", text)
	}
}

func TestClient_EditMarkdown_TakesTheHostReadingOut(t *testing.T) {
	f := newSendServer(t)
	c := f.client(t)

	if err := c.EditMarkdown(context.Background(), "m1", "смотрю burst.go"); err != nil {
		t.Fatalf("edit: %v", err)
	}
	edits := f.editCalls()
	if len(edits) != 1 {
		t.Fatalf("edits = %d, want 1", len(edits))
	}
	if text := edits[0].body.Text; strings.Contains(text, "burst.go") {
		t.Fatalf("the edit reached the wire as %q, so MAX would preview it", text)
	}
}
