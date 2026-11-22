//go:build gateway || gateway.max

package max

// A line of the gateway's own - the mirrored answer carrying "🌐", a job report
// with its header - used to go out as one message of whatever length the turn
// wrote. On 22.11.2026 two answers of an evening were refused whole ("Field
// 'text' size (11562) must be at most 4000") and the chat saw neither of them:
// these tests pin the road that keeps that from happening.

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

// TestPost_SplitsALongLineIntoThePlatformLimit is the defect itself: 3000 emoji
// are 6000 units, so a rune count would have let them through as one text of
// 3000 and MAX would have refused the whole line.
func TestPost_SplitsALongLineIntoThePlatformLimit(t *testing.T) {
	f := newSendServer(t)
	s := testBot(t).newSender(context.Background(), f.client(t), Target{UserID: 7})

	s.post("🌐 " + strings.Repeat("🙂", 3000))

	calls := f.sends()
	if len(calls) < 2 {
		t.Fatalf("sends = %d, want the line cut into pieces: one text of 6000 units is refused whole", len(calls))
	}
	emoji := 0
	for i, call := range calls {
		if units := textUnits(call.body.Text); units > maxTextRunes {
			t.Fatalf("piece %d carries %d units, want at most %d", i, units, maxTextRunes)
		}
		emoji += strings.Count(call.body.Text, "🙂")
	}
	if emoji != 3000 {
		t.Fatalf("the pieces carry %d emoji of 3000: a long line must arrive whole, not be cut short", emoji)
	}
}

// TestPost_KeepsThePlacementMarkOnTheFirstPiece: the "🌐" says where the answer
// was written, and it makes no sense on the tail of a split line.
func TestPost_KeepsThePlacementMarkOnTheFirstPiece(t *testing.T) {
	f := newSendServer(t)
	s := testBot(t).newSender(context.Background(), f.client(t), Target{UserID: 7})

	s.post("🌐 " + strings.Repeat("слово ", 1200))

	calls := f.sends()
	if len(calls) < 2 {
		t.Fatalf("sends = %d, want a split line", len(calls))
	}
	if !strings.HasPrefix(calls[0].body.Text, "🌐") {
		t.Fatalf("the first piece starts with %q, want the mark", firstRunes(calls[0].body.Text, 12))
	}
	for i, call := range calls[1:] {
		if strings.Contains(call.body.Text, "🌐") {
			t.Fatalf("piece %d repeats the mark", i+1)
		}
	}
}

// TestPost_ParksALineThePlatformRefuses: a refusal that will stand must cost a
// delay, not the text. Before 22.11.2026 the line was simply dropped.
func TestPost_ParksALineThePlatformRefuses(t *testing.T) {
	fastTextRetries(t)
	b := testBot(t)
	b.SetOutboxPath(filepath.Join(t.TempDir(), "gateway_max_outbox.json"))
	s := b.newSender(context.Background(), refusingClient(t), Target{UserID: 7})

	s.post("строка, которую платформа не взяла")

	if got := b.outbox.length(); got != 1 {
		t.Fatalf("outbox holds %d lines, want 1: a refused line is kept for a later attempt", got)
	}
	items := b.outbox.take()
	if len(items) != 1 || !strings.Contains(items[0].Text, "не взяла") {
		t.Fatalf("parked %+v, want the refused line", items)
	}
}

// TestSplitText_CountsTheUnitsMAXCounts: the piece budget is the platform's own,
// so a surrogate pair counts as two.
func TestSplitText_CountsTheUnitsMAXCounts(t *testing.T) {
	pieces := splitText(strings.Repeat("🙂", 3000), maxTextRunes)
	if len(pieces) != 2 {
		t.Fatalf("pieces = %d, want 2: 3000 emoji are 6000 units", len(pieces))
	}
	for i, piece := range pieces {
		if units := textUnits(piece); units > maxTextRunes {
			t.Fatalf("piece %d carries %d units, want at most %d", i, units, maxTextRunes)
		}
	}
}

// firstRunes is a short, safe look at the head of a text for failure messages.
func firstRunes(text string, n int) string {
	r := []rune(text)
	if len(r) > n {
		r = r[:n]
	}

	return string(r)
}
