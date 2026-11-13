//go:build gateway || gateway.max

package max

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/external/gateway/sessionstore"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
)

// tail is a conversation that grows the way a turn writes into it.
type tail struct {
	mu   sync.Mutex
	msgs []llm.Message
}

func (t *tail) read(string) []llm.Message {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([]llm.Message, len(t.msgs))
	copy(out, t.msgs)

	return out
}

func (t *tail) add(role llm.Role, text string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.msgs = append(t.msgs, llm.Message{Role: role, Content: text})
}

// lateRecorder is a client that records what the bot sends, so a test can say
// what reached the chat.
type lateRecorder struct {
	mu   sync.Mutex
	reqs []string
}

func (r *lateRecorder) add(s string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.reqs = append(r.reqs, s)
}

func (r *lateRecorder) all() []string {
	r.mu.Lock()
	defer r.mu.Unlock()

	return append([]string(nil), r.reqs...)
}

func lateClient(t *testing.T) (*Client, *lateRecorder) {
	t.Helper()
	rec := &lateRecorder{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf, _ := io.ReadAll(io.LimitReader(r.Body, 64<<10))
		rec.add(string(buf))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":true}`))
	}))
	t.Cleanup(srv.Close)

	return NewClient("t", WithHTTPClient(srv.Client()), WithBaseURL(srv.URL)), rec
}

// busyRunner is a runner whose session is busy: a turn of another window runs in
// it, so the process knows about it while the gateway does not own it.
type busyRunner struct {
	*stubRunner
	mu   sync.Mutex
	busy map[string]bool
}

func (r *busyRunner) SessionTurnActiveInProcess(sessionID string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()

	return r.busy[sessionID]
}

func fastWatch(t *testing.T) {
	t.Helper()
	pause, idle := lateWatchPause, lateWatchIdle
	lateWatchPause, lateWatchIdle = 5*time.Millisecond, 20*time.Millisecond
	t.Cleanup(func() { lateWatchPause, lateWatchIdle = pause, idle })
}

func TestWatchLateAnswer_PostsTheAnswerOfAnotherWindow(t *testing.T) {
	fastWatch(t)
	c, rec := lateClient(t)
	b := New(testCfg(), newStubRunner(), "", slog.New(slog.DiscardHandler), "", "", nil)
	b.setClient(c)
	b.store = sessionstore.New()
	b.store.Bind("max:user:1", "sess-1")
	conv := &tail{}
	conv.add(llm.RoleUser, "вопрос из браузера")
	b.transcript = conv.read

	done := make(chan struct{})
	go func() {
		b.watchLateAnswer(context.Background(), Target{ChatID: 7}, "max:user:1", "sess-1")
		close(done)
	}()

	time.Sleep(20 * time.Millisecond)
	conv.add(llm.RoleAssistant, "ответ, который ждали в чате")

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("the watcher never finished after the answer appeared")
	}
	reqs := rec.all()
	if len(reqs) != 1 {
		t.Fatalf("messages sent = %d, want exactly one: %v", len(reqs), reqs)
	}
	if !strings.Contains(reqs[0], "ответ, который ждали в чате") {
		t.Errorf("the answer did not reach the chat: %s", reqs[0])
	}
}

func TestWatchLateAnswer_StopsWhenTheChatMovedAway(t *testing.T) {
	fastWatch(t)
	c, rec := lateClient(t)
	b := New(testCfg(), newStubRunner(), "", slog.New(slog.DiscardHandler), "", "", nil)
	b.setClient(c)
	b.store = sessionstore.New()
	b.store.Bind("max:user:1", "sess-other")
	conv := &tail{}
	b.transcript = conv.read

	done := make(chan struct{})
	go func() {
		b.watchLateAnswer(context.Background(), Target{ChatID: 7}, "max:user:1", "sess-1")
		close(done)
	}()
	conv.add(llm.RoleAssistant, "ответ уже никому не нужен")

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("the watcher kept running after the chat moved to another conversation")
	}
	if reqs := rec.all(); len(reqs) != 0 {
		t.Fatalf("a chat that left the conversation received %d messages: %v", len(reqs), reqs)
	}
}

func TestWatchSwitch_NotesOnlyATurnOfAnotherWindow(t *testing.T) {
	fastWatch(t)
	runner := &busyRunner{stubRunner: newStubRunner(), busy: map[string]bool{"sess-1": true}}
	b := New(testCfg(), runner, "", slog.New(slog.DiscardHandler), "", "", nil)
	b.setClient(quietAPI())
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	in := inbound{chatID: 7, userID: 1}

	if note := b.watchSwitch(ctx, in, "max:user:1", "sess-1"); note != noteTurnElsewhere {
		t.Fatalf("note = %q, want the one that says the answer comes from another window", note)
	}

	// The same busy conversation, but the turn is ours: it writes into the chat
	// itself, and promising an answer from elsewhere would be a lie.
	b.registerTurn("max:user:1", func() {})
	if note := b.watchSwitch(ctx, in, "max:user:1", "sess-1"); note != "" {
		t.Errorf("note for our own turn = %q, want none", note)
	}

	// A finished conversation has nothing to wait for either.
	b.unregisterTurn("max:user:1")
	if note := b.watchSwitch(ctx, in, "max:user:1", "sess-idle"); note != "" {
		t.Errorf("note for an idle conversation = %q, want none", note)
	}
}
