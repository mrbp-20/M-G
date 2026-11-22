//go:build gateway || gateway.max

package max

// The answer of 22.11.2026 that could never go out.
//
// We measured 3 967 units and the platform counted 4 009, refused, and every
// restart repeated the refusal. Neither the platform nor the text was to blame:
// Client.Send passes the text through quietLinks, which puts a zero-width space
// after every dot of a host-like word so MAX stops drawing preview cards, and the
// platform counts the text it is given. A technical answer names files - "встреча.md",
// "burst.go" - so the difference is not a rounding error but two digits.
//
// The measure is now taken on the sent form, and the outbox cuts a parked answer
// the same way a live one is cut.

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// hostHeavyText names files, which is what an ordinary technical answer does.
func hostHeavyText(words int) string {
	var b strings.Builder
	for i := 0; i < words; i++ {
		fmt.Fprintf(&b, "файл-%04d.md ", i)
	}

	return strings.TrimSpace(b.String())
}

// strictClient refuses any text the platform would count as longer than limit,
// using the platform's own answer, so a test sees exactly what the person saw.
func strictClient(t *testing.T, limit int) (*Client, *lateRecorder) {
	t.Helper()
	rec := &lateRecorder{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		var body struct {
			Text string `json:"text"`
		}
		_ = json.Unmarshal(buf, &body)
		if got := textUnits(body.Text); got > limit {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprintf(w, `{"code":"proto.payload","message":"Field 'text' size (%d) must be at most %d"}`, got, limit)

			return
		}
		rec.add(string(body.Text))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":true}`))
	}))
	t.Cleanup(srv.Close)

	return NewClient("t", WithHTTPClient(srv.Client()), WithBaseURL(srv.URL)), rec
}

// pickyClient refuses any message holding marker and takes everything else, so a
// test can make one piece of a long answer fail while the pieces before it go
// through - a permanent refusal in the middle of an answer.
func pickyClient(t *testing.T, marker string) (*Client, *lateRecorder) {
	t.Helper()
	rec := &lateRecorder{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		var body struct {
			Text string `json:"text"`
		}
		_ = json.Unmarshal(buf, &body)
		if strings.Contains(body.Text, marker) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"code":"proto.payload","message":"Field 'text' is refused for the test"}`))

			return
		}
		rec.add(string(body.Text))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":true}`))
	}))
	t.Cleanup(srv.Close)

	return NewClient("t", WithHTTPClient(srv.Client()), WithBaseURL(srv.URL)), rec
}

func TestSplitText_MeasuresTheTextItActuallySends(t *testing.T) {
	text := hostHeavyText(300)

	if raw := textUnits(text); raw >= maxTextRunes {
		t.Fatalf("the fixture is %d raw units, want it to look short before defusing", raw)
	}
	if sent := sentUnits(text); sent <= maxTextRunes {
		t.Fatalf("the fixture is %d sent units, want it over the limit: that is the defect", sent)
	}

	pieces := splitText(text, maxTextRunes)
	if len(pieces) < 2 {
		t.Fatalf("pieces = %d, want at least two: the sent text does not fit one message", len(pieces))
	}
	for i, piece := range pieces {
		if got := sentUnits(piece); got > maxTextRunes {
			t.Fatalf("piece %d is %d sent units, over the limit of %d", i+1, got, maxTextRunes)
		}
	}
	if got, want := strings.Count(strings.Join(pieces, " "), ".md"), 300; got != want {
		t.Fatalf("the pieces hold %d file names, want %d: nothing may be lost", got, want)
	}
}

func TestSplitText_KeepsANormalAnswerWhole(t *testing.T) {
	text := "обычный ответ без имён файлов, короткий и спокойный"

	if got := splitText(text, maxTextRunes); len(got) != 1 {
		t.Fatalf("pieces = %d, want one: %q", len(got), got)
	}
}

func TestFlushOutbox_SendsWhatThePlatformRefusesWhole(t *testing.T) {
	c, rec := strictClient(t, maxTextRunes)
	b := watchedBot(t, c, &tail{})

	b.park(Target{UserID: 7}, hostHeavyText(300))
	b.flushOutbox(t.Context())

	if got := rec.count(); got < 2 {
		t.Fatalf("sends = %d, want the parked answer split into pieces", got)
	}
	if left := b.outbox.length(); left != 0 {
		t.Fatalf("outbox still holds %d entries, want none: every piece fitted", left)
	}
}

// A piece the platform still refuses must leave the rest of the answer waiting,
// not take it with it.
func TestFlushOutbox_KeepsTheTailWhenOnePieceIsRefused(t *testing.T) {
	c, rec := pickyClient(t, "файл-0290")
	b := watchedBot(t, c, &tail{})

	b.park(Target{UserID: 7}, hostHeavyText(300))
	b.flushOutbox(t.Context())

	if rec.count() == 0 {
		t.Fatal("nothing was sent, want the first pieces to go through")
	}
	if left := b.outbox.length(); left == 0 {
		t.Fatal("outbox is empty, want the refused tail kept for a later attempt")
	}
	kept := b.outbox.take()
	if len(kept) != 1 {
		t.Fatalf("kept = %d entries, want one holding the rest of the answer", len(kept))
	}
	if !strings.Contains(kept[0].Text, "файл-0290") || !strings.Contains(kept[0].Text, "файл-0299.md") {
		t.Fatal("the kept text does not hold the refused piece and everything behind it")
	}
}

// The stored entry is what a restart reads, so the same road must work on an
// outbox loaded from disk.
func TestFlushOutbox_WorksOnWhatARestartFinds(t *testing.T) {
	c, rec := strictClient(t, maxTextRunes)
	b := watchedBot(t, c, &tail{})
	b.outbox = newOutbox(t.TempDir() + "/outbox.json")
	b.park(Target{UserID: 7}, hostHeavyText(300))

	restarted := watchedBot(t, c, &tail{})
	restarted.outbox = newOutbox(b.outbox.path)
	restarted.flushOutbox(t.Context())

	if rec.count() < 2 {
		t.Fatalf("sends = %d after a restart, want the parked answer split too", rec.count())
	}
	if left := restarted.outbox.length(); left != 0 {
		t.Fatalf("outbox still holds %d entries after the restart", left)
	}
}
