//go:build gateway || gateway.max

package max

// MAX reads any word of the shape name.ext as a host name and attaches a preview
// card for it - a tool call carrying README.md filled the chat with cards for
// parked domains, and the server fetched those addresses itself. The documented
// switch is the query parameter disable_link_preview, and these tests hold every
// write path to it: a body field of the same name is dropped without a word,
// which is exactly the mistake the first attempt made.

import (
	"context"
	"testing"
)

func TestClient_Send_AsksMAXForNoLinkPreview(t *testing.T) {
	f := newSendServer(t)
	c := f.client(t)

	if _, err := c.Send(context.Background(), Outgoing{Target: Target{ChatID: 1}, Text: "⚙️ cat README.md"}); err != nil {
		t.Fatalf("send: %v", err)
	}
	sent := f.sends()
	if len(sent) != 1 {
		t.Fatalf("sends = %d, want 1", len(sent))
	}
	if got := sent[0].query.Get("disable_link_preview"); got != "true" {
		t.Fatalf("disable_link_preview = %q, want \"true\": without it MAX previews README.md as a domain", got)
	}
	// The flag stops the preview card; the zero-width space stops the hyperlink
	// the client would otherwise draw. Both are needed: the operator caught the
	// link alone on 2026-10-18, in a history card carrying a file name.
	want := "⚙️ cat README." + zeroWidth + "md"
	if sent[0].body.Text != want {
		t.Fatalf("text = %q, want %q", sent[0].body.Text, want)
	}
}

func TestClient_Edits_AskMAXForNoLinkPreview(t *testing.T) {
	f := newSendServer(t)
	c := f.client(t)

	if err := c.EditMarkdown(context.Background(), "m1", "смотрю burst.go"); err != nil {
		t.Fatalf("edit markdown: %v", err)
	}
	if err := c.EditText(context.Background(), "m1", "смотрю burst.go"); err != nil {
		t.Fatalf("edit text: %v", err)
	}
	edits := f.editCalls()
	if len(edits) != 2 {
		t.Fatalf("edits = %d, want 2", len(edits))
	}
	for i, call := range edits {
		if got := call.query.Get("disable_link_preview"); got != "true" {
			t.Fatalf("edit %d: disable_link_preview = %q, want \"true\": an edit puts the card back", i, got)
		}
		if want := "смотрю burst." + zeroWidth + "go"; call.body.Text != want {
			t.Fatalf("edit %d: text = %q, want %q", i, call.body.Text, want)
		}
	}
}

func TestClient_Send_ARealLinkKeepsItsWords(t *testing.T) {
	f := newSendServer(t)
	c := f.client(t)

	text := "ссылка: https://github.com/mrbp-20/MX-Gate-Code"
	if _, err := c.Send(context.Background(), Outgoing{Target: Target{ChatID: 1}, Text: text, Format: "markdown"}); err != nil {
		t.Fatalf("send: %v", err)
	}
	sent := f.sends()
	if len(sent) != 1 {
		t.Fatalf("sends = %d, want 1", len(sent))
	}
	if sent[0].body.Text != text {
		t.Fatalf("text = %q, want the link exactly as written", sent[0].body.Text)
	}
}
