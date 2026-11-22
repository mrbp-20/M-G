//go:build gateway || gateway.max

package max

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/external/gateway/sessionstore"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
)

// tail is a conversation that grows the way a turn writes into it.
type tail struct {
	mu   sync.Mutex
	msgs []llm.Message
}

func (t *tail) read(string) ([]llm.Message, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([]llm.Message, len(t.msgs))
	copy(out, t.msgs)

	return out, true
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

func (r *lateRecorder) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()

	return len(r.reqs)
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

// refusingClient answers every send with a server error, which is how a platform
// that is briefly unavailable looks from here.
func refusingClient(t *testing.T) *Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte(`{"code":"bad.gateway"}`))
	}))
	t.Cleanup(srv.Close)

	return NewClient("t", WithHTTPClient(srv.Client()), WithBaseURL(srv.URL))
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

// watchedBot builds a bot with one client, one conversation store and a
// transcript the test grows by hand.
func watchedBot(t *testing.T, c *Client, conv *tail) *Bot {
	t.Helper()
	b := New(testCfg(), newStubRunner(), "", slog.New(slog.DiscardHandler), "", "", nil)
	b.setClient(c)
	b.watchPause = 5 * time.Millisecond
	// The same pace for the grace: a test that needs the window of 22.11.2026
	// sets its own (watcher_grace_test.go), every other test keeps its speed.
	b.watchGrace = 20 * time.Millisecond
	b.store = sessionstore.New()
	b.transcript = conv.read
	b.outbox = newOutbox(filepath.Join(t.TempDir(), "outbox.json"))

	return b
}

// TestChatWatcher_PostsTheAnswerOfATurnNoChatStarted: a background task wakes
// the agent and it answers inside the conversation. No chat asked for that turn,
// so nothing wraps it - the answer must be taken from the conversation itself.
func TestChatWatcher_PostsTheAnswerOfATurnNoChatStarted(t *testing.T) {
	c, rec := lateClient(t)
	conv := &tail{}
	conv.add(llm.RoleUser, "вопрос из браузера")
	b := watchedBot(t, c, conv)
	b.store.Bind("max:user:1", "sess-1")

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	b.armWatcher(ctx, "sess-1")

	conv.add(llm.RoleAssistant, "ответ, который ждали в чате")
	waitFor(t, func() bool { return rec.count() == 1 }, "the answer to reach the chat")

	if !strings.Contains(rec.all()[0], "ответ, который ждали в чате") {
		t.Fatalf("the answer did not reach the chat: %s", rec.all()[0])
	}
}

// TestChatWatcher_PostsIntoEveryChatOfTheConversation: one conversation opened
// in two chats gets the answer in both. Picking the first binding silently would
// leave the other chat waiting forever.
func TestChatWatcher_PostsIntoEveryChatOfTheConversation(t *testing.T) {
	c, rec := lateClient(t)
	conv := &tail{}
	b := watchedBot(t, c, conv)
	b.store.Bind("max:user:1", "sess-1")
	b.store.Bind("max:chat:-100", "sess-1")

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	b.armWatcher(ctx, "sess-1")

	conv.add(llm.RoleAssistant, "одно и то же в оба чата")
	waitFor(t, func() bool { return rec.count() == 2 }, "both chats to get the answer")
}

// TestChatWatcher_StopsWhenNoChatHoldsTheConversation: /resume moved every chat
// off this conversation, so there is nowhere to deliver and nobody to tell.
func TestChatWatcher_StopsWhenNoChatHoldsTheConversation(t *testing.T) {
	c, rec := lateClient(t)
	conv := &tail{}
	b := watchedBot(t, c, conv)
	b.store.Bind("max:user:1", "sess-other")

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	b.armWatcher(ctx, "sess-1")

	conv.add(llm.RoleAssistant, "этот ответ уже никому не нужен")
	time.Sleep(60 * time.Millisecond)

	if n := rec.count(); n != 0 {
		t.Fatalf("a conversation no chat holds was posted %d times: %v", n, rec.all())
	}
	b.watchMu.Lock()
	_, still := b.watching["sess-1"]
	b.watchMu.Unlock()
	if still {
		t.Fatal("the watcher kept running for a conversation nobody holds")
	}
}

// TestChatWatcher_LeavesOurOwnTurnAlone: a turn this chat started delivers its
// own answer; posting it from the conversation as well would double every reply.
func TestChatWatcher_LeavesOurOwnTurnAlone(t *testing.T) {
	c, rec := lateClient(t)
	conv := &tail{}
	b := watchedBot(t, c, conv)
	b.store.Bind("max:user:1", "sess-1")
	b.registerTurn("max:user:1", func() {})
	t.Cleanup(func() { b.unregisterTurn("max:user:1") })

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	b.armWatcher(ctx, "sess-1")

	conv.add(llm.RoleAssistant, "ответ нашего же хода")
	time.Sleep(60 * time.Millisecond)

	if n := rec.count(); n != 0 {
		t.Fatalf("our own answer was posted again: %v", rec.all())
	}
}

// TestChatWatcher_DoesNotRepeatWhatWeDelivered: the answer written while our
// turn ran is remembered, so a watcher that sees it a beat later keeps quiet.
func TestChatWatcher_DoesNotRepeatWhatWeDelivered(t *testing.T) {
	c, rec := lateClient(t)
	conv := &tail{}
	b := watchedBot(t, c, conv)
	b.store.Bind("max:user:1", "sess-1")
	b.selfPosted.mark("sess-1", "ответ, который уже доставлен")

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	b.armWatcher(ctx, "sess-1")

	conv.add(llm.RoleAssistant, "ответ, который уже доставлен")
	time.Sleep(60 * time.Millisecond)

	if n := rec.count(); n != 0 {
		t.Fatalf("an answer we had delivered was posted twice: %v", rec.all())
	}
}

// TestChatWatcher_ParksTheAnswerWhenThePlatformRefuses: a network blip must cost
// a delay, not the answer - it goes to the outbox and leaves on the next connect.
func TestChatWatcher_ParksTheAnswerWhenThePlatformRefuses(t *testing.T) {
	// The watcher road retries before it parks (it takes the answer's road now),
	// so the pauses are squeezed to a millisecond: what is being checked is the
	// park at the end, not the patience.
	fastTextRetries(t)
	conv := &tail{}
	b := watchedBot(t, refusingClient(t), conv)
	b.store.Bind("max:user:1", "sess-1")

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	b.armWatcher(ctx, "sess-1")

	conv.add(llm.RoleAssistant, "ответ, который платформа не приняла")
	waitFor(t, func() bool { return b.outbox.length() > 0 }, "the answer to be parked")
}

func TestChatWatcher_NeverPostsToolCardsOrEmptyText(t *testing.T) {
	c, rec := lateClient(t)
	conv := &tail{}
	b := watchedBot(t, c, conv)
	b.store.Bind("max:user:1", "sess-1")

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	b.armWatcher(ctx, "sess-1")

	conv.add(llm.RoleAssistant, "   ")
	conv.add(llm.RoleTool, "результат инструмента")
	time.Sleep(60 * time.Millisecond)

	if n := rec.count(); n != 0 {
		t.Fatalf("empty text or a tool result reached the chat: %v", rec.all())
	}
}

func TestTargetOfKey(t *testing.T) {
	for _, tc := range []struct {
		key  string
		want Target
	}{
		{sessionstore.SessionKey("max", 5, 42, config.IsolationIndividual, false), Target{UserID: 42}},
		{sessionstore.SessionKey("max", -100, 42, config.IsolationIndividual, true), Target{ChatID: -100}},
	} {
		got, ok := targetOfKey(tc.key)
		if !ok || got != tc.want {
			t.Fatalf("targetOfKey(%q) = %v, %v; want %v", tc.key, got, ok, tc.want)
		}
	}
	for _, bad := range []string{"", "max", "max:user:", "max:room:5", "max:chat:not-a-number", "max:user:0"} {
		if _, ok := targetOfKey(bad); ok {
			t.Fatalf("targetOfKey(%q) accepted a key that addresses nobody", bad)
		}
	}
}

// TestWatchSwitch_NotesOnlyATurnOfAnotherWindow: the promise is made only when a
// turn really runs elsewhere.
func TestWatchSwitch_NotesOnlyATurnOfAnotherWindow(t *testing.T) {
	runner := &busyRunner{stubRunner: newStubRunner(), busy: map[string]bool{"sess-1": true}}
	b := New(testCfg(), runner, "", slog.New(slog.DiscardHandler), "", "", nil)
	b.setClient(quietAPI())
	b.watchPause = 5 * time.Millisecond
	b.store = sessionstore.New()
	b.store.Bind("max:user:1", "sess-1")
	b.transcript = (&tail{}).read
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

// lazyTail answers with nothing until the test loads it, which is how a
// conversation Coddy has not opened yet answers.
type lazyTail struct {
	mu     sync.Mutex
	msgs   []llm.Message
	loaded bool
}

func (l *lazyTail) read(string) ([]llm.Message, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.loaded {
		return nil, false
	}

	return append([]llm.Message(nil), l.msgs...), true
}

func (l *lazyTail) load(msgs ...llm.Message) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.msgs = append([]llm.Message(nil), msgs...)
	l.loaded = true
}

func (l *lazyTail) add(m llm.Message) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.msgs = append(l.msgs, m)
}

// TestChatWatcher_DoesNotReplayHistoryItFoundAfterArming: right after a restart
// Coddy answers with nothing for a conversation it has not loaded yet. Its
// history must not be mistaken for answers nobody has seen - on 20.11.2026 that
// pushed two hundred old answers into one chat within half a minute.
func TestChatWatcher_DoesNotReplayHistoryItFoundAfterArming(t *testing.T) {
	c, rec := lateClient(t)
	tl := &lazyTail{}
	b := New(testCfg(), newStubRunner(), "", slog.New(slog.DiscardHandler), "", "", nil)
	b.setClient(c)
	b.watchPause = 5 * time.Millisecond
	b.store = sessionstore.New()
	b.store.Bind("max:user:1", "sess-1")
	b.transcript = tl.read
	b.outbox = newOutbox(filepath.Join(t.TempDir(), "outbox.json"))

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	b.armWatcher(ctx, "sess-1")

	// The conversation loads afterwards, carrying everything written before the
	// watcher existed.
	tl.load(
		llm.Message{Role: llm.RoleUser, Content: "старый вопрос"},
		llm.Message{Role: llm.RoleAssistant, Content: "старый ответ"},
	)
	time.Sleep(80 * time.Millisecond)
	if n := rec.count(); n != 0 {
		t.Fatalf("history was replayed into the chat: %v", rec.all())
	}

	// An answer written after the conversation settled still has to arrive.
	tl.add(llm.Message{Role: llm.RoleAssistant, Content: "новый ответ"})
	waitFor(t, func() bool { return rec.count() == 1 }, "the new answer to reach the chat")
	if !strings.Contains(rec.all()[0], "новый ответ") {
		t.Fatalf("a new answer did not reach the chat: %s", rec.all()[0])
	}
}

// TestChatWatcher_DoesNotRepeatTheTextOfOurOwnTurn: the transcript keeps a
// turn's text as one block, so a turn that wrote four short lines sits there as
// a single message. The chat already got those lines one by one, and the watcher
// must not send the block again - that is the 895 characters of 20.11.2026.
func TestChatWatcher_DoesNotRepeatTheTextOfOurOwnTurn(t *testing.T) {
	c, rec := lateClient(t)
	conv := &tail{}
	conv.add(llm.RoleUser, "вопрос из чата")
	b := watchedBot(t, c, conv)
	b.store.Bind("max:user:1", "sess-1")

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	b.armWatcher(ctx, "sess-1")

	// Our own turn writes its text, and the turn ends: everything up to here
	// was delivered line by line.
	conv.add(llm.RoleAssistant, "первая реплика\n\nвторая реплика\n\nответ")
	b.markTurnEnd("sess-1")
	time.Sleep(60 * time.Millisecond)
	if n := rec.count(); n != 0 {
		t.Fatalf("our own turn was repeated as one block: %v", rec.all())
	}

	// A turn no chat started comes later and must arrive.
	conv.add(llm.RoleAssistant, "ответ после пробуждения")
	waitFor(t, func() bool { return rec.count() == 1 }, "the wake answer to reach the chat")
	if !strings.Contains(rec.all()[0], "после пробуждения") {
		t.Fatalf("the wake answer did not reach the chat: %s", rec.all()[0])
	}
}
