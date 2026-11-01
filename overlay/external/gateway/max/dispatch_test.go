//go:build gateway || gateway.max

package max

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

func TestParseCommand(t *testing.T) {
	for _, tc := range []struct {
		in       string
		cmd, arg string
	}{
		{"/start", "start", ""},
		{"/help extra words", "help", "extra words"},
		{"/model", "model", ""},
		{"/model openai/gpt-4o", "model", "openai/gpt-4o"},
		{"plain text", "", ""},
		{"/", "", ""},
		{"/unknown", "unknown", ""},
	} {
		cmd, arg := parseCommand(tc.in)
		if cmd != tc.cmd || arg != tc.arg {
			t.Errorf("parseCommand(%q) = (%q, %q), want (%q, %q)", tc.in, cmd, arg, tc.cmd, tc.arg)
		}
	}
}

func TestKnownCommand(t *testing.T) {
	for _, cmd := range []string{"start", "help", "clear", "model", "context", "goal", "agent", "plan", "ask", "think"} {
		if !knownCommand(cmd) {
			t.Errorf("knownCommand(%q) = false, want true", cmd)
		}
	}
	if knownCommand("not-a-real-command") {
		t.Error("knownCommand(unknown) = true, want false")
	}
}

func TestAllowed(t *testing.T) {
	b := &Bot{
		cfg: &config.MaxGatewayConfig{
			Admins:           []int64{1},
			DefaultAccess:    config.AccessAdmins,
			DefaultIsolation: config.IsolationIndividual,
		},
		log: slog.New(slog.DiscardHandler),
	}
	if !b.allowed(10, 1, false) {
		t.Error("admin denied, want allowed")
	}
	if b.allowed(10, 2, false) {
		t.Error("non-admin allowed under admins access, want denied")
	}
	// A group under admin-only isolation refuses a non-admin member.
	b.cfg.DefaultIsolation = config.IsolationAdmin
	if b.allowed(10, 2, true) {
		t.Error("non-admin allowed in an admin-only group, want denied")
	}
}

// stubRunner records what the bot asks the session manager to do, and signals
// each finished turn on a channel so a test can wait for it.
type stubRunner struct {
	mu         sync.Mutex
	cfg        *config.Config
	live       map[string]*session.State
	prompts    []string
	sessions   []string
	restricted []bool
	imageParts [][]acp.ImagePartRef
	turns      chan string
	// busy is how many attempts answer "session busy" before the turn runs.
	busy int
	// cancelled records the session ids the bot stopped through the session.
	cancelled []string
}

func newStubRunner() *stubRunner {
	return &stubRunner{
		cfg: &config.Config{
			Models: []config.ModelEntry{{Model: "openai/gpt-4o", Multimodal: true}},
			Agent:  config.Agent{Model: "openai/gpt-4o"},
		},
		live:  map[string]*session.State{},
		turns: make(chan string, 16),
	}
}

func (r *stubRunner) EnsureHTTPSession(_ context.Context, sessionID, cwd string) (*session.State, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if st, ok := r.live[sessionID]; ok {
		return st, nil
	}
	st := &session.State{ID: sessionID, CWD: cwd, Mode: session.ModeAgent}
	r.live[sessionID] = st
	return st, nil
}

func (r *stubRunner) HandleSessionPromptWithSender(_ context.Context, params acp.SessionPromptParams, _ acp.UpdateSender, opts *session.PromptRunOpts) (*acp.SessionPromptResult, error) {
	r.mu.Lock()
	if r.busy > 0 {
		// The session's previous turn still holds it: a busy answer, not a turn.
		r.busy--
		r.mu.Unlock()

		return nil, session.ErrSessionTurnBusy
	}
	var sb strings.Builder
	for _, blk := range params.Prompt {
		sb.WriteString(blk.Text)
	}
	r.prompts = append(r.prompts, sb.String())
	r.sessions = append(r.sessions, params.SessionID)
	r.imageParts = append(r.imageParts, append([]acp.ImagePartRef(nil), params.ImageParts...))
	r.restricted = append(r.restricted, opts != nil && opts.Restriction != nil)
	r.mu.Unlock()
	r.turns <- sb.String()
	return &acp.SessionPromptResult{}, nil
}

func (r *stubRunner) ForgetLiveSession(string) {}
func (r *stubRunner) HandleSessionCancel(params acp.SessionCancelParams) {
	r.mu.Lock()
	r.cancelled = append(r.cancelled, params.SessionID)
	r.mu.Unlock()
}

func (r *stubRunner) cancelledIDs() []string {
	r.mu.Lock()
	defer r.mu.Unlock()

	return append([]string(nil), r.cancelled...)
}

func (r *stubRunner) HandleSessionSetConfigOption(_ context.Context, _ acp.SessionSetConfigOptionParams) (*acp.SessionSetConfigOptionResult, error) {
	return &acp.SessionSetConfigOptionResult{}, nil
}
func (r *stubRunner) Cfg() *config.Config { return r.cfg }

func (r *stubRunner) waitTurn(t *testing.T) string {
	t.Helper()
	select {
	case text := <-r.turns:
		return text
	case <-time.After(5 * time.Second):
		t.Fatal("no turn ran")
		return ""
	}
}

func (r *stubRunner) promptCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.prompts)
}

func testCfg() *config.MaxGatewayConfig {
	return &config.MaxGatewayConfig{
		Enabled:          true,
		Token:            "t",
		Admins:           []int64{1},
		DefaultAccess:    config.AccessAdmins,
		DefaultIsolation: config.IsolationIndividual,
	}
}

// armedPrompt registers a prompt that is waiting for a written answer, with the
// request to write taken off the channel the way the prompt in the turn takes it.
func armedPrompt(t *testing.T, b *Bot) chan pressAnswer {
	t.Helper()
	token, answers := b.asked.offer(Target{ChatID: 10, UserID: 1}, []string{"A", "B"})
	if !b.asked.arm(token, 1) {
		t.Fatal("the prompt refused to wait for a written answer")
	}
	select {
	case <-answers:
	default:
		t.Fatal("the request to write never reached the prompt")
	}

	return answers
}

// TestWrittenAnswer_IsTakenFromTheChat: the message after "Свой ответ" is the
// answer to the question, not a new turn.
func TestWrittenAnswer_IsTakenFromTheChat(t *testing.T) {
	b := newTestBot(&stubRunner{})
	answers := armedPrompt(t, b)

	if err := b.handleMessage(context.Background(), inboundMsg("m-1", "мой ответ")); err != nil {
		t.Fatalf("handleMessage: %v", err)
	}
	select {
	case got := <-answers:
		if got.text != "мой ответ" {
			t.Fatalf("the prompt got %+v, want the written text", got)
		}
	default:
		t.Fatal("the written answer never reached the prompt")
	}
	if b.asked.armed(1) {
		t.Fatal("the prompt still waits after the answer")
	}
	if b.asked.write(1, "второе сообщение") {
		t.Fatal("a second message was taken as the answer")
	}
}

// TestWrittenAnswer_ACommandIsNeverTheAnswer: a command runs as itself and the
// wait ends, so the answer nobody wrote cannot swallow the next question.
func TestWrittenAnswer_ACommandIsNeverTheAnswer(t *testing.T) {
	b := newTestBot(&stubRunner{})
	answers := armedPrompt(t, b)

	if err := b.handleMessage(context.Background(), inboundMsg("m-2", "/help")); err != nil {
		t.Fatalf("handleMessage: %v", err)
	}
	if b.asked.armed(1) {
		t.Fatal("the wait survived a command")
	}
	if b.asked.write(1, "поздно") {
		t.Fatal("a command left the prompt taking messages")
	}
	select {
	case got := <-answers:
		if !got.drop {
			t.Fatalf("the wait ended with %+v, want it released", got)
		}
	default:
		t.Fatal("the command left the question waiting for an answer nobody will write")
	}
}

// TestWrittenAnswer_AMessageWithoutTextAsksAgain: a message that carries no text
// is not the answer; the chat is told what is expected once more and the prompt
// goes on waiting.
func TestWrittenAnswer_AMessageWithoutTextAsksAgain(t *testing.T) {
	f := newSendServer(t)
	b := newTestBot(&stubRunner{})
	b.setClient(f.client(t))
	armedPrompt(t, b)

	if err := b.handleMessage(context.Background(), inboundMsg("m-3", "")); err != nil {
		t.Fatalf("handleMessage: %v", err)
	}
	sent := f.sends()
	if len(sent) == 0 || !strings.Contains(sent[0].body.Text, "Свой ответ: напишите его следующим сообщением") {
		t.Fatalf("the chat was told %+v, want the phrase again", sent)
	}
	if !b.asked.armed(1) {
		t.Fatal("the wait ended on a message without text")
	}
}

// quietAPI is a Bot API that answers every request with an empty success. A
// test bot's client must never reach the network: handleMessage marks the chat
// seen on every accepted message (dispatch.go, markSeen) and starts the
// "typing…" hint, so those calls have to cost a microsecond rather than a DNS
// lookup. Each bot gets a client of its own: the client paces its writes, and a
// shared one would make every test wait for the previous test's hint.
func quietAPI() *Client {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":true}`))
	}))

	return NewClient("t", WithHTTPClient(srv.Client()), WithBaseURL(srv.URL))
}

// newTestBot builds a Bot wired to a stub runner and a client that answers
// nothing (the stub returns an empty result, so the sender posts nothing).
func newTestBot(r *stubRunner) *Bot {
	b := New(testCfg(), r, "", slog.New(slog.DiscardHandler), "", "", nil)
	b.setClient(quietAPI())
	// The burst window is a wait for a person, not for a test: a short one
	// keeps the suite quick and still exercises the same path (burst.go).
	b.burstWindow = 20 * time.Millisecond
	return b
}

// inboundMsg builds the update MAX sends for a created message: the id of the
// message lives in message.body.mid, and the update carries no message_id,
// chat_id or user_id of its own - handleMessage reads them from the message.
func inboundMsg(mid, text string) Update {
	body, _ := json.Marshal(map[string]any{
		"sender":    map[string]any{"user_id": 1, "name": "Alice"},
		"recipient": map[string]any{"chat_id": 10, "chat_type": "dialog"},
		"body":      map[string]any{"mid": mid, "text": text},
	})
	return Update{UpdateType: "message_created", Message: body}
}

func TestHandleMessageRunsOneTurn(t *testing.T) {
	r := newStubRunner()
	b := newTestBot(r)

	if err := b.Handle(context.Background(), inboundMsg("m1", "hello")); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if got := r.waitTurn(t); got != "hello" {
		t.Errorf("prompt = %q, want %q", got, "hello")
	}
	if len(r.sessions) != 1 || r.sessions[0] == "" {
		t.Errorf("session id not minted: %v", r.sessions)
	}
}

func TestHandleDeduplicatesByMessageID(t *testing.T) {
	r := newStubRunner()
	b := newTestBot(r)
	u := inboundMsg("m1", "hello")

	if err := b.Handle(context.Background(), u); err != nil {
		t.Fatalf("first Handle: %v", err)
	}
	r.waitTurn(t)
	// The same message delivered again must not run a second turn.
	if err := b.Handle(context.Background(), u); err != nil {
		t.Fatalf("second Handle: %v", err)
	}
	time.Sleep(50 * time.Millisecond)
	if n := r.promptCount(); n != 1 {
		t.Errorf("prompts = %d, want 1", n)
	}
}

func TestHandleSkipsBotMessages(t *testing.T) {
	r := newStubRunner()
	b := newTestBot(r)
	body, _ := json.Marshal(map[string]any{
		"sender":    map[string]any{"user_id": 99, "is_bot": true},
		"recipient": map[string]any{"chat_id": 10, "chat_type": "dialog"},
		"body":      map[string]any{"mid": "m1", "text": "hi"},
	})
	u := Update{UpdateType: "message_created", Message: body}

	if err := b.Handle(context.Background(), u); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	select {
	case <-r.turns:
		t.Fatal("the bot's own message ran a turn")
	case <-time.After(200 * time.Millisecond):
	}
}

func TestImageAttachmentParsing(t *testing.T) {
	body, _ := json.Marshal(map[string]any{
		"sender":    map[string]any{"user_id": 1, "name": "Alice"},
		"recipient": map[string]any{"chat_id": 10, "chat_type": "dialog"},
		"body": map[string]any{
			"mid": "m1",
			"attachments": []map[string]any{
				{"type": "image", "payload": map[string]any{"photo_id": 7, "token": "tok", "url": "https://i.oneme.ru/x"}},
				{"type": "file", "payload": map[string]any{"file_id": 9}},
			},
		},
	})
	u := Update{UpdateType: "message_created", Message: body}
	m, err := u.Body()
	if err != nil {
		t.Fatalf("Body: %v", err)
	}
	imgs, err := m.ImageAttachments()
	if err != nil {
		t.Fatalf("ImageAttachments: %v", err)
	}
	if len(imgs) != 1 || imgs[0].PhotoID != 7 || imgs[0].URL != "https://i.oneme.ru/x" {
		t.Errorf("unexpected images: %+v", imgs)
	}
}

func TestNonMultimodalModelRejectsPhoto(t *testing.T) {
	replies, srv := replyServer(t)
	defer srv.Close()

	r := newStubRunner()
	r.cfg.Agent.Model = "openai/gpt-4"
	r.cfg.Models = []config.ModelEntry{{Model: "openai/gpt-4", Multimodal: false}}

	b := newTestBot(r)
	b.setClient(NewClient("t", WithHTTPClient(srv.Client()), WithBaseURL(srv.URL)))

	body, _ := json.Marshal(map[string]any{
		"sender":    map[string]any{"user_id": 1, "name": "Alice"},
		"recipient": map[string]any{"chat_id": 10, "chat_type": "dialog"},
		"body": map[string]any{
			"mid":  "m1",
			"text": "look",
			"attachments": []map[string]any{
				{"type": "image", "payload": map[string]any{"photo_id": 1, "url": "https://i.oneme.ru/x"}},
			},
		},
	})
	if err := b.Handle(context.Background(), Update{UpdateType: "message_created", Message: body}); err != nil {
		t.Fatalf("Handle: %v", err)
	}

	waitFor(t, func() bool { return len(replies()) > 0 }, "bot reply")
	if r.promptCount() != 0 {
		t.Errorf("prompts = %d, want 0", r.promptCount())
	}
	want := multimodalNote("openai/gpt-4")
	if got := replies()[0]; !strings.Contains(got, want) {
		t.Errorf("reply = %q, want it to contain %q", got, want)
	}
}

func TestMultimodalModelReceivesPhoto(t *testing.T) {
	img := imageServer(t)
	defer img.Close()
	_, srv := replyServer(t)
	defer srv.Close()

	r := newStubRunner()
	b := newTestBot(r)
	b.setClient(NewClient("t", WithHTTPClient(srv.Client()), WithBaseURL(srv.URL)))
	b.httpClient = img.Client()

	body, _ := json.Marshal(map[string]any{
		"sender":    map[string]any{"user_id": 1, "name": "Alice"},
		"recipient": map[string]any{"chat_id": 10, "chat_type": "dialog"},
		"body": map[string]any{
			"mid":  "m1",
			"text": "look",
			"attachments": []map[string]any{
				{"type": "image", "payload": map[string]any{"photo_id": 3, "url": img.URL + "/pic"}},
			},
		},
	})
	if err := b.Handle(context.Background(), Update{UpdateType: "message_created", Message: body}); err != nil {
		t.Fatalf("Handle: %v", err)
	}

	_ = r.waitTurn(t)
	r.mu.Lock()
	parts := r.imageParts[len(r.imageParts)-1]
	r.mu.Unlock()
	if len(parts) != 1 {
		t.Fatalf("got %d image parts, want 1", len(parts))
	}
	if !strings.HasPrefix(parts[0].DataURL, "data:image/png;base64,") {
		t.Errorf("unexpected data URL prefix: %q", parts[0].DataURL)
	}
	if parts[0].Name != "photo_3.png" {
		t.Errorf("name = %q, want photo_3.png", parts[0].Name)
	}
}

func replyServer(t *testing.T) (func() []string, *httptest.Server) {
	t.Helper()
	var mu sync.Mutex
	var texts []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/messages" {
			http.NotFound(w, r)
			return
		}
		var payload struct {
			Text string `json:"text"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err == nil {
			mu.Lock()
			texts = append(texts, payload.Text)
			mu.Unlock()
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"message": map[string]any{"body": map[string]any{"mid": "reply"}},
		})
	}))
	return func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), texts...)
	}, srv
}

func imageServer(t *testing.T) *httptest.Server {
	t.Helper()
	png, _ := base64.StdEncoding.DecodeString(tinyPNG)
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(png)
	}))
}

func waitFor(t *testing.T, cond func() bool, what string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func TestHandleAccessDenied(t *testing.T) {
	r := newStubRunner()
	b := newTestBot(r)
	body, _ := json.Marshal(map[string]any{
		"sender":    map[string]any{"user_id": 2, "name": "Bob"},
		"recipient": map[string]any{"chat_id": 10, "chat_type": "dialog"},
		"body":      map[string]any{"mid": "m1", "text": "hello"},
	})
	u := Update{UpdateType: "message_created", Message: body}

	if err := b.Handle(context.Background(), u); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	select {
	case <-r.turns:
		t.Fatal("a non-admin ran a turn under admins-only access")
	case <-time.After(200 * time.Millisecond):
	}
}

func TestClearCommandResetsSession(t *testing.T) {
	r := newStubRunner()
	b := newTestBot(r)

	if err := b.Handle(context.Background(), inboundMsg("m1", "/clear")); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	// /clear never reaches the session manager as a turn.
	select {
	case <-r.turns:
		t.Fatal("/clear ran a turn")
	case <-time.After(200 * time.Millisecond):
	}
}

// TestHandleWaitsOutABusySession: turns of one session are serialized, so a
// message that arrives while the previous one runs waits for its place instead
// of failing. What it must not do is answer with the internal error.
func TestHandleWaitsOutABusySession(t *testing.T) {
	oldWait, oldPause := turnBusyWait, turnBusyPause
	turnBusyWait, turnBusyPause = 2*time.Second, 5*time.Millisecond
	defer func() { turnBusyWait, turnBusyPause = oldWait, oldPause }()

	r := newStubRunner()
	r.busy = 3
	b := newTestBot(r)

	if err := b.Handle(context.Background(), inboundMsg("m1", "hello")); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if got := r.waitTurn(t); got != "hello" {
		t.Fatalf("prompt = %q, want the message to reach the turn", got)
	}
}

// TestHandleSaysTheChatIsBusyInRussian: a wait that runs out is reported in the
// chat's own language, not as the internal "session busy" text.
func TestHandleSaysTheChatIsBusyInRussian(t *testing.T) {
	oldWait, oldPause := turnBusyWait, turnBusyPause
	turnBusyWait, turnBusyPause = 40*time.Millisecond, 5*time.Millisecond
	defer func() { turnBusyWait, turnBusyPause = oldWait, oldPause }()

	f := newSendServer(t)
	r := newStubRunner()
	r.busy = 1 << 20
	b := newTestBot(r)
	b.setClient(f.client(t))

	if err := b.Handle(context.Background(), inboundMsg("m1", "hello")); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		for _, call := range f.sends() {
			if call.body.Text == busyNote {
				return
			}
			if strings.Contains(call.body.Text, "session busy") {
				t.Fatalf("the chat got the internal error %q, want %q", call.body.Text, busyNote)
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("no message said the chat is busy; sent: %+v", f.sends())
}

// TestAnUnknownSlashWord_ReachesTheAgentAsText is the defect of 01.11: a person
// quoted a line of ours that began with a slash, MAX hid the slash behind an
// invisible character, and the whole message was thrown away as somebody else's
// command - one answer, then silence, and not even a word about why.
func TestAnUnknownSlashWord_ReachesTheAgentAsText(t *testing.T) {
	r := newStubRunner()
	b := newTestBot(r)
	text := "/\ufeffrpa-feat (фича по BDD),\n\nА что это у нас такое? Расскажи мне поподробнее."

	if err := b.Handle(context.Background(), inboundMsg("m1", text)); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	got := r.waitTurn(t)
	want := "/rpa-feat (фича по BDD),\n\nА что это у нас такое? Расскажи мне поподробнее."
	if got != want {
		t.Fatalf("prompt = %q, want %q", got, want)
	}
}

// TestASettingsCommandWithAnInvisibleRune_IsStillACommand: the same character
// must not turn "/think" into prose, because the session reads its settings
// commands off the very text the gateway hands it.
func TestASettingsCommandWithAnInvisibleRune_IsStillACommand(t *testing.T) {
	r := newStubRunner()
	b := newTestBot(r)

	if err := b.Handle(context.Background(), inboundMsg("m1", "/\ufeffthink")); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if got := r.waitTurn(t); got != "/think" {
		t.Fatalf("prompt = %q, want %q", got, "/think")
	}
}
