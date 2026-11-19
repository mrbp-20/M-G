//go:build gateway || gateway.max

package max

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// flakyPlatform is a MAX that refuses the first few messages and then takes
// them: the shape of 19.11.2026, when the platform went quiet for fifteen
// seconds and an answer that was already written had nowhere to go.
type flakyPlatform struct {
	srv *httptest.Server

	mu      sync.Mutex
	refuse  int // how many POST /messages are refused before the first success
	status  int // the status those refusals carry
	attempt int // how many POST /messages arrived at all
	texts   []string
}

func newFlakyPlatform(t *testing.T, refuse, status int) *flakyPlatform {
	t.Helper()
	f := &flakyPlatform{refuse: refuse, status: status}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !(r.Method == http.MethodPost && r.URL.Path == "/messages") {
			w.WriteHeader(http.StatusOK)

			return
		}
		var body struct {
			Text string `json:"text"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.mu.Lock()
		f.attempt++
		refuse := f.refuse
		if refuse > 0 {
			f.refuse--
		} else {
			f.texts = append(f.texts, body.Text)
		}
		f.mu.Unlock()
		if refuse > 0 {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(f.status)
			_, _ = fmt.Fprintf(w, `{"code":%q,"message":"not now"}`, "service_unavailable")

			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"message":{"body":{"mid":"m1"}}}`)
	}))
	t.Cleanup(f.srv.Close)

	return f
}

func (f *flakyPlatform) client() *Client {
	return NewClient(testToken, WithHTTPClient(f.srv.Client()), WithBaseURL(f.srv.URL))
}

func (f *flakyPlatform) sent() []string {
	f.mu.Lock()
	defer f.mu.Unlock()

	return append([]string(nil), f.texts...)
}

func (f *flakyPlatform) attempts() int {
	f.mu.Lock()
	defer f.mu.Unlock()

	return f.attempt
}

// fastTextRetries makes the pauses of the answer retry short enough for a test
// and puts the real ones back afterwards.
func fastTextRetries(t *testing.T) {
	t.Helper()
	keep := textRetryPauses
	textRetryPauses = []time.Duration{time.Millisecond, time.Millisecond, time.Millisecond}
	t.Cleanup(func() { textRetryPauses = keep })
}

// TestSendText_RepeatsWhileThePlatformIsQuiet: two refusals and then the answer,
// which is the whole point - the same text, one delivery.
func TestSendText_RepeatsWhileThePlatformIsQuiet(t *testing.T) {
	fastTextRetries(t)
	p := newFlakyPlatform(t, 2, http.StatusServiceUnavailable)
	b := testBot(t)
	b.setClient(p.client())
	t.Cleanup(func() { b.setClient(nil) })

	if err := b.sendText(context.Background(), p.client(), Target{UserID: 1}, "ответ"); err != nil {
		t.Fatalf("sendText: %v", err)
	}
	if got := p.attempts(); got != 3 {
		t.Fatalf("attempts = %d, want 3 (two refusals and the one that worked)", got)
	}
	if got := p.sent(); len(got) != 1 || got[0] != "ответ" {
		t.Fatalf("delivered = %v, want exactly one answer", got)
	}
}

// TestSendText_DoesNotRepeatARefusalThatWillStand: a bad request is answered at
// once. Repeating it would make the person wait twice for the same refusal.
func TestSendText_DoesNotRepeatARefusalThatWillStand(t *testing.T) {
	fastTextRetries(t)
	p := newFlakyPlatform(t, 5, http.StatusBadRequest)
	b := testBot(t)
	b.setClient(p.client())
	t.Cleanup(func() { b.setClient(nil) })

	if err := b.sendText(context.Background(), p.client(), Target{UserID: 1}, "ответ"); err == nil {
		t.Fatal("a 400 must come back as an error")
	}
	if got := p.attempts(); got != 1 {
		t.Fatalf("attempts = %d, want 1", got)
	}
}

// TestSender_ParksAnAnswerMAXNeverTook is the case that started all this: the
// answer exists, the platform will not take it, and the chat must not be left
// with silence and no explanation anywhere.
func TestSender_ParksAnAnswerMAXNeverTook(t *testing.T) {
	fastTextRetries(t)
	p := newFlakyPlatform(t, 100, http.StatusServiceUnavailable)
	b := testBot(t)
	b.SetOutboxPath(filepath.Join(t.TempDir(), "gateway_max_outbox.json"))
	b.setClient(p.client())
	t.Cleanup(func() { b.setClient(nil) })

	s := b.newSender(context.Background(), p.client(), Target{UserID: 7})
	s.deliver(context.Background(), "ответ, который не ушёл")

	if got := b.outbox.length(); got != 1 {
		t.Fatalf("outbox holds %d answers, want 1", got)
	}
	items := b.outbox.take()
	if items[0].Target.UserID != 7 || !strings.Contains(items[0].Text, "не ушёл") {
		t.Fatalf("parked %+v, want the answer for user 7", items[0])
	}
}

// TestBot_FlushOutboxSendsWhatWasParked: the next connection is the way out.
func TestBot_FlushOutboxSendsWhatWasParked(t *testing.T) {
	fastTextRetries(t)
	p := newFlakyPlatform(t, 0, http.StatusServiceUnavailable)
	b := testBot(t)
	b.SetOutboxPath(filepath.Join(t.TempDir(), "gateway_max_outbox.json"))
	b.setClient(p.client())
	t.Cleanup(func() { b.setClient(nil) })
	b.outbox.add(Target{UserID: 7}, "пропущенный ответ")

	b.flushOutbox(context.Background())

	if got := b.outbox.length(); got != 0 {
		t.Fatalf("outbox holds %d answers after a delivery, want 0", got)
	}
	if got := p.sent(); len(got) != 1 || got[0] != "пропущенный ответ" {
		t.Fatalf("delivered = %v, want the parked answer", got)
	}
}

// TestOutbox_SurvivesARestart: what matters is that the file, not the process,
// keeps the answer.
func TestOutbox_SurvivesARestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gateway_max_outbox.json")
	first := newOutbox(path)
	first.add(Target{ChatID: -100}, "ответ в группу")
	first.add(Target{UserID: 42}, "ответ человеку")

	second := newOutbox(path)
	items := second.take()
	if len(items) != 2 {
		t.Fatalf("a fresh outbox holds %d answers, want 2", len(items))
	}
	if items[0].Target.ChatID != -100 || items[1].Target.UserID != 42 {
		t.Fatalf("order changed: %+v", items)
	}
}

// TestOutbox_ForgetsWhatIsTooOld: an answer found a day later is no longer an
// answer, and sending it would be worse than dropping it.
func TestOutbox_ForgetsWhatIsTooOld(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gateway_max_outbox.json")
	o := newOutbox(path)
	o.entries = []outboxEntry{
		{Target: Target{UserID: 1}, Text: "старый", At: time.Now().Add(-25 * time.Hour)},
		{Target: Target{UserID: 2}, Text: "свежий", At: time.Now()},
	}
	o.saveLocked()

	if got := newOutbox(path).take(); len(got) != 1 || got[0].Text != "свежий" {
		t.Fatalf("loaded %+v, want only the fresh answer", got)
	}
}

// TestOutbox_KeepsTheNewestWhenFull: a chat that stays unreachable must not grow
// the state file without end.
func TestOutbox_KeepsTheNewestWhenFull(t *testing.T) {
	o := newOutbox("")
	for i := 0; i < outboxMaxEntries+5; i++ {
		o.add(Target{UserID: int64(i)}, fmt.Sprintf("ответ %d", i))
	}
	items := o.take()
	if len(items) != outboxMaxEntries {
		t.Fatalf("outbox holds %d answers, want the cap %d", len(items), outboxMaxEntries)
	}
	if items[len(items)-1].Text != fmt.Sprintf("ответ %d", outboxMaxEntries+4) {
		t.Fatalf("the newest answer was dropped: %+v", items[len(items)-1])
	}
}
