//go:build gateway || gateway.max

package max

// Unit tests of the two chat actions the gateway sends: mark_seen, which turns
// the single tick on a person's message into a second one, and typing_on, which
// keeps the chat showing the bot is working while the turn runs. Both go out on
// POST /chats/{chatId}/actions, whose body is the action alone.

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// actionRecorder is a stand-in Bot API for the actions route: it records what
// arrived and answers with the status the test asked for.
type actionRecorder struct {
	srv *httptest.Server

	mu      sync.Mutex
	actions []string
	paths   []string
	bodies  []map[string]any
	status  int
}

func newActionRecorder(t *testing.T) *actionRecorder {
	t.Helper()
	r := &actionRecorder{}
	r.srv = httptest.NewServer(http.HandlerFunc(r.handle))
	t.Cleanup(r.srv.Close)

	return r
}

func (r *actionRecorder) handle(w http.ResponseWriter, req *http.Request) {
	raw, _ := io.ReadAll(req.Body)
	if req.Method != http.MethodPost || !strings.HasSuffix(req.URL.Path, "/actions") {
		http.NotFound(w, req)
		return
	}
	var body map[string]any
	_ = json.Unmarshal(raw, &body)
	action, _ := body["action"].(string)

	r.mu.Lock()
	r.actions = append(r.actions, action)
	r.paths = append(r.paths, req.URL.Path)
	r.bodies = append(r.bodies, body)
	status := r.status
	r.mu.Unlock()

	if status != 0 {
		w.WriteHeader(status)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = io.WriteString(w, `{"success":true}`)
}

// seen copies what the recorder holds, so a check reads one consistent list.
func (r *actionRecorder) seen() ([]string, []string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	return append([]string(nil), r.actions...), append([]string(nil), r.paths...)
}

func (r *actionRecorder) client(t *testing.T) *Client {
	t.Helper()

	return NewClient("t", WithHTTPClient(r.srv.Client()), WithBaseURL(r.srv.URL))
}

func TestSendAction(t *testing.T) {
	rec := newActionRecorder(t)
	c := rec.client(t)

	if err := c.SendAction(context.Background(), 42, ActionMarkSeen); err != nil {
		t.Fatalf("SendAction: %v", err)
	}
	actions, paths := rec.seen()
	if len(actions) != 1 || actions[0] != "mark_seen" {
		t.Fatalf("actions = %v, want one mark_seen", actions)
	}
	if paths[0] != "/chats/42/actions" {
		t.Errorf("path = %q, want %q", paths[0], "/chats/42/actions")
	}
	// The body is the action and nothing else: the schema's ActionRequestBody
	// has one field, so a stray message_id would be a request the platform
	// never defined.
	if body := rec.bodies[0]; len(body) != 1 {
		t.Errorf("body = %v, want only the action", body)
	}
}

func TestSendActionRefusesAnEmptyRequest(t *testing.T) {
	rec := newActionRecorder(t)
	c := rec.client(t)

	if err := c.SendAction(context.Background(), 0, ActionMarkSeen); err == nil {
		t.Error("SendAction without a chat = nil, want an error")
	}
	if err := c.SendAction(context.Background(), 42, ""); err == nil {
		t.Error("SendAction without an action = nil, want an error")
	}
	if actions, _ := rec.seen(); len(actions) != 0 {
		t.Errorf("the API was called %d times for a refused request", len(actions))
	}
}

// TestTheChatIsMarkedSeenWhenAMessageArrives is the point of the change: a
// message the bot accepts is read at once, before the burst waits and before
// the turn starts, so the person sees the second tick rather than a message
// that looks delivered and forgotten.
func TestTheChatIsMarkedSeenWhenAMessageArrives(t *testing.T) {
	r := newStubRunner()
	b := newTestBot(r)
	rec := newActionRecorder(t)
	b.setClient(rec.client(t))

	if err := b.Handle(context.Background(), inboundMsg("m1", "hello")); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if got := r.waitTurn(t); got != "hello" {
		t.Fatalf("prompt = %q, want the message", got)
	}

	actions, paths := rec.seen()
	// The mark goes out in the background (dispatch.go), so the check waits for
	// it instead of assuming it landed before the turn ran.
	deadline := time.Now().Add(2 * time.Second)
	for len(actions) == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
		actions, paths = rec.seen()
	}
	if len(actions) == 0 || actions[0] != ActionMarkSeen {
		t.Fatalf("actions = %v, want mark_seen first", actions)
	}
	// The dialog of the test has chat_id 10 (inboundMsg); the action names it
	// in the path, not in the body.
	if paths[0] != "/chats/10/actions" {
		t.Errorf("path = %q, want %q", paths[0], "/chats/10/actions")
	}
}

// TestTypingIsShownWhileTheTurnRuns checks the hint goes out at once and is
// renewed: MAX shows an action for a few seconds, and a turn outlasts it.
func TestTypingIsShownWhileTheTurnRuns(t *testing.T) {
	b := testBot(t)
	rec := newActionRecorder(t)
	b.setClient(rec.client(t))
	b.typingEvery = 20 * time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stop := b.typingKeepAlive(ctx, 10)

	// The first action is sent by typingKeepAlive itself, before it returns.
	actions, _ := rec.seen()
	if len(actions) != 1 || actions[0] != ActionTyping {
		t.Fatalf("actions right after the call = %v, want one typing_on", actions)
	}

	// The client paces writes two a second, so the renewal cannot be seen
	// faster than that however short the interval is.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		actions, _ := rec.seen()
		if len(actions) >= 2 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	actions, _ = rec.seen()
	if len(actions) < 2 {
		t.Fatalf("actions = %v, want the hint renewed", actions)
	}
	for _, action := range actions {
		if action != ActionTyping {
			t.Errorf("action = %q, want only typing_on", action)
		}
	}

	// After stop nothing further goes out.
	stop()
	stopped := len(actions)
	time.Sleep(700 * time.Millisecond)
	if after, _ := rec.seen(); len(after) != stopped {
		t.Errorf("actions after stop = %d, want %d", len(after), stopped)
	}
}

// TestARefusedActionDoesNotStopTheTurn keeps the promise made where the calls
// are: an unread message is a worse chat, never a failed answer.
func TestARefusedActionDoesNotStopTheTurn(t *testing.T) {
	r := newStubRunner()
	b := newTestBot(r)
	rec := newActionRecorder(t)
	rec.status = http.StatusInternalServerError
	b.setClient(rec.client(t))
	b.typingEvery = 20 * time.Millisecond

	if err := b.Handle(context.Background(), inboundMsg("m1", "hello")); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if got := r.waitTurn(t); got != "hello" {
		t.Errorf("prompt = %q, want the turn to run anyway", got)
	}
}
