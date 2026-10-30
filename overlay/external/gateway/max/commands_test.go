//go:build gateway || gateway.max

package max

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/external/gateway/sessionstore"
	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

var (
	// longModelID is longer than the 1024-byte MAX callback payload limit once
	// the "model:" prefix is added, so it exercises the digest path.
	longModelID = "vllm/qwen3-235b-a22b-instruct-2507-fp8-extended-context-preview-" + strings.Repeat("x", 1100)
)

// modelRunner is a stub runner that records model changes.
type modelRunner struct {
	mu        sync.Mutex
	cfg       *config.Config
	live      map[string]*session.State
	setModel  []string // models applied, in order
	setErrors map[string]error
}

func newModelRunner(models ...string) *modelRunner {
	entries := make([]config.ModelEntry, len(models))
	for i, m := range models {
		entries[i] = config.ModelEntry{Model: m}
	}
	return &modelRunner{
		cfg: &config.Config{
			Models: entries,
			Agent:  config.Agent{Model: entries[0].Model},
		},
		live:      map[string]*session.State{},
		setErrors: map[string]error{},
	}
}

func (r *modelRunner) EnsureHTTPSession(_ context.Context, sessionID, cwd string) (*session.State, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if st, ok := r.live[sessionID]; ok {
		return st, nil
	}
	st := &session.State{ID: sessionID, CWD: cwd, Mode: session.ModeAgent}
	r.live[sessionID] = st
	return st, nil
}

func (r *modelRunner) HandleSessionPromptWithSender(_ context.Context, _ acp.SessionPromptParams, _ acp.UpdateSender, _ *session.PromptRunOpts) (*acp.SessionPromptResult, error) {
	return &acp.SessionPromptResult{}, nil
}

func (r *modelRunner) ForgetLiveSession(string) {}

func (r *modelRunner) HandleSessionCancel(acp.SessionCancelParams) {}

func (r *modelRunner) HandleSessionSetConfigOption(_ context.Context, params acp.SessionSetConfigOptionParams) (*acp.SessionSetConfigOptionResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.setErrors[params.Value]; err != nil {
		return nil, err
	}
	if st := r.live[params.SessionID]; st != nil {
		st.SetSelectedModelID(params.Value)
	}
	r.setModel = append(r.setModel, params.Value)
	return &acp.SessionSetConfigOptionResult{}, nil
}

func (r *modelRunner) Cfg() *config.Config {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.cfg
}

func (r *modelRunner) applied() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.setModel...)
}

func testModelBot(t *testing.T, runner *modelRunner) *Bot {
	t.Helper()
	cfg := &config.MaxGatewayConfig{
		Enabled:          true,
		Token:            "t",
		Admins:           []int64{1},
		DefaultAccess:    config.AccessAll,
		DefaultIsolation: config.IsolationIndividual,
	}
	return New(cfg, runner, "", slog.New(slog.DiscardHandler), "", "", nil)
}

func TestBuildModelKeyboard_TickOnCurrent(t *testing.T) {
	models := []config.ModelEntry{
		{Model: "openai/gpt-4o"},
		{Model: "rpa/qwen3.6-35b-a3b"},
	}
	rows := buildModelKeyboard(models, "rpa/qwen3.6-35b-a3b")
	if len(rows) != 2 {
		t.Fatalf("got %d rows, want 2", len(rows))
	}
	if got, want := rows[1][0].Text, "✓ rpa/qwen3.6-35b-a3b"; got != want {
		t.Errorf("current row text = %q, want %q", got, want)
	}
	if got := rows[0][0].Text; strings.HasPrefix(got, "✓") {
		t.Errorf("non-current row has a tick: %q", got)
	}
	if got, want := rows[0][0].Payload, "model:openai/gpt-4o"; got != want {
		t.Errorf("payload = %q, want %q", got, want)
	}
}

func TestBuildModelKeyboard_DigestsLongIDs(t *testing.T) {
	models := []config.ModelEntry{
		{Model: "openai/gpt-4o"},
		{Model: longModelID},
	}
	rows := buildModelKeyboard(models, "openai/gpt-4o")
	if len(rows) != 2 {
		t.Fatalf("got %d rows, want 2", len(rows))
	}
	payload := rows[1][0].Payload
	if !strings.HasPrefix(payload, "model:") {
		t.Fatalf("payload has no model prefix: %q", payload)
	}
	if len(payload) > maxCallbackPayload {
		t.Fatalf("payload is %d bytes, over the %d limit", len(payload), maxCallbackPayload)
	}
	if !strings.Contains(payload, callbackDigestMarker) {
		t.Fatalf("long model payload should use the digest marker, got %q", payload)
	}
}

func TestResolveModelCallback(t *testing.T) {
	models := []config.ModelEntry{
		{Model: "openai/gpt-4o"},
		{Model: longModelID},
	}
	cases := []struct {
		payload string
		want    string
		ok      bool
	}{
		{"openai/gpt-4o", "openai/gpt-4o", true},
		{modelCallbackValue(longModelID), longModelID, true},
		{"missing/model", "", false},
		{"#bad", "", false},
	}
	for _, tc := range cases {
		got, ok := resolveModelCallback(models, tc.payload)
		if got != tc.want || ok != tc.ok {
			t.Errorf("resolveModelCallback(%q) = (%q, %v), want (%q, %v)", tc.payload, got, ok, tc.want, tc.ok)
		}
	}
}

func TestHandleModelCommand_SendsKeyboard(t *testing.T) {
	f := newSendServer(t)
	r := newModelRunner("openai/gpt-4o", "rpa/qwen3.6-35b-a3b")
	b := testModelBot(t, r)
	b.setClient(f.client(t))

	in := inbound{chatID: 10, userID: 1, text: "/model", command: "model"}
	b.handleModelCommand(context.Background(), in, sessionstore.SessionKey(adapterName, 10, 1, config.IsolationIndividual, false))

	f.waitSends(t, 1, 5*time.Second)
	posted := f.sends()[0]
	if !strings.Contains(posted.body.Text, "Модель") {
		t.Fatalf("menu text = %q, want it to contain 'Модель'", posted.body.Text)
	}
	rows := buttons(t, posted.body.Attachments)
	if len(rows) != 2 {
		t.Fatalf("keyboard has %d rows, want 2", len(rows))
	}
	if rows[0][0].Text != "✓ openai/gpt-4o" {
		t.Errorf("first button = %q, want ✓ openai/gpt-4o", rows[0][0].Text)
	}
}

func TestHandleModelAction_AppliesModelAndEditsMenu(t *testing.T) {
	f := newSendServer(t)
	r := newModelRunner("openai/gpt-4o", "rpa/qwen3.6-35b-a3b")
	b := testModelBot(t, r)
	b.setClient(f.client(t))

	// Post the model menu so the message exists and carries its keyboard.
	in := inbound{chatID: 10, userID: 1, text: "/model", command: "model"}
	b.handleModelCommand(context.Background(), in, sessionstore.SessionKey(adapterName, 10, 1, config.IsolationIndividual, false))
	f.waitSends(t, 1, 5*time.Second)

	// Press the second model's button.
	payload := "model:" + modelCallbackValue("rpa/qwen3.6-35b-a3b")
	posted := f.sends()[0]
	err := b.HandleCallback(context.Background(), f.client(t), callbackUpdate(1, 10, messageFromSend(posted), payload))
	if err != nil {
		t.Fatalf("HandleCallback: %v", err)
	}

	if got := r.applied(); len(got) != 1 || got[0] != "rpa/qwen3.6-35b-a3b" {
		t.Fatalf("applied models = %v, want [rpa/qwen3.6-35b-a3b]", got)
	}
	edits := f.editCalls()
	if len(edits) != 1 {
		t.Fatalf("got %d edits, want 1", len(edits))
	}
	rows := buttons(t, edits[0].body.Attachments)
	if len(rows) != 2 || rows[1][0].Text != "✓ rpa/qwen3.6-35b-a3b" {
		t.Fatalf("edited keyboard did not tick the new model: %+v", rows)
	}
	answers := f.answered()
	if len(answers) != 1 || answers[0].callbackID != "cb-1" {
		t.Fatalf("callback answers = %+v, want one for cb-1", answers)
	}
}

func TestHandleModelAction_RefusesNonAdminInGroup(t *testing.T) {
	f := newSendServer(t)
	r := newModelRunner("openai/gpt-4o", "rpa/qwen3.6-35b-a3b")
	b := testModelBot(t, r)
	b.setClient(f.client(t))

	payload := "model:rpa/qwen3.6-35b-a3b"
	u := callbackUpdate(2, 20, Message{}, payload)
	// Mark the update as coming from a group chat.
	u.Message = groupMessageBody(20)

	err := b.HandleCallback(context.Background(), f.client(t), u)
	if err != nil {
		t.Fatalf("HandleCallback: %v", err)
	}
	if got := r.applied(); len(got) != 0 {
		t.Fatalf("non-admin in group applied model %v, want none", got)
	}
	answers := f.answered()
	if len(answers) != 1 || answers[0].notification != adminOnlyNote {
		t.Fatalf("notification = %q, want %q", answers[0].notification, adminOnlyNote)
	}
}

func TestHandleModelAction_AdminInGroupCanSwitch(t *testing.T) {
	f := newSendServer(t)
	r := newModelRunner("openai/gpt-4o", "rpa/qwen3.6-35b-a3b")
	b := testModelBot(t, r)
	b.setClient(f.client(t))

	payload := "model:rpa/qwen3.6-35b-a3b"
	u := callbackUpdate(1, 20, Message{}, payload)
	u.Message = groupMessageBody(20)

	err := b.HandleCallback(context.Background(), f.client(t), u)
	if err != nil {
		t.Fatalf("HandleCallback: %v", err)
	}
	if got := r.applied(); len(got) != 1 || got[0] != "rpa/qwen3.6-35b-a3b" {
		t.Fatalf("applied models = %v, want [rpa/qwen3.6-35b-a3b]", got)
	}
}

func TestHandleModelAction_TapOnCurrentLeavesMenuAlone(t *testing.T) {
	f := newSendServer(t)
	r := newModelRunner("openai/gpt-4o", "rpa/qwen3.6-35b-a3b")
	b := testModelBot(t, r)
	b.setClient(f.client(t))

	in := inbound{chatID: 10, userID: 1, text: "/model", command: "model"}
	b.handleModelCommand(context.Background(), in, sessionstore.SessionKey(adapterName, 10, 1, config.IsolationIndividual, false))
	f.waitSends(t, 1, 5*time.Second)

	// The current model after opening the menu is the configured default.
	payload := "model:" + modelCallbackValue("openai/gpt-4o")
	posted := f.sends()[0]
	if err := b.HandleCallback(context.Background(), f.client(t), callbackUpdate(1, 10, messageFromSend(posted), payload)); err != nil {
		t.Fatalf("HandleCallback: %v", err)
	}
	if got := r.applied(); len(got) != 1 {
		t.Fatalf("applied models = %v, want 1", got)
	}
	if len(f.editCalls()) != 0 {
		t.Fatalf("tap on current model edited the menu: %+v", f.editCalls())
	}
}

func TestHandleModelAction_UnknownModel(t *testing.T) {
	f := newSendServer(t)
	r := newModelRunner("openai/gpt-4o")
	b := testModelBot(t, r)
	b.setClient(f.client(t))

	u := callbackUpdate(1, 10, Message{}, "model:missing/model")
	if err := b.HandleCallback(context.Background(), f.client(t), u); err != nil {
		t.Fatalf("HandleCallback: %v", err)
	}
	if got := r.applied(); len(got) != 0 {
		t.Fatalf("applied models = %v, want none", got)
	}
	answers := f.answered()
	if len(answers) != 1 || !strings.Contains(answers[0].notification, "no longer configured") {
		t.Fatalf("notification = %q, want an error", answers[0].notification)
	}
}

// callbackUpdate builds a message_callback update for a single user.
// message carries the original message body so same-keyboard checks can work.
// callbackUpdate builds a message_callback update for a single user. It mirrors
// the update MAX really sends: the id of the message the button belongs to is
// inside message.body.mid, and the update carries no message_id of its own.
func callbackUpdate(userID, chatID int64, msg Message, payload string) Update {
	cb, _ := json.Marshal(Callback{
		CallbackID: "cb-1",
		Payload:    payload,
		User:       User{UserID: userID},
	})
	var rawMsg json.RawMessage
	if msg.Body.Mid != "" {
		rawMsg, _ = json.Marshal(msg)
	}
	return Update{
		UpdateType: "message_callback",
		ChatID:     chatID,
		UserID:     userID,
		Message:    rawMsg,
		Callback:   cb,
	}
}

// messageFromSend reconstructs a Message from a recorded send so that
// callback updates can carry the original body and attachments.
func messageFromSend(s wireCall) Message {
	var attachments []json.RawMessage
	if len(s.body.Attachments) > 0 {
		_ = json.Unmarshal(s.body.Attachments, &attachments)
	}
	return Message{
		Sender:    User{UserID: 1},
		Recipient: Recipient{ChatID: 10, ChatType: "dialog"},
		Body: Text{
			Mid:         "m1",
			Text:        s.body.Text,
			Attachments: attachments,
		},
	}
}

// groupMessageBody returns a raw message body whose chat type is not dialog.
func groupMessageBody(chatID int64) json.RawMessage {
	body, _ := json.Marshal(Message{
		Sender:    User{UserID: chatID + 1},
		Recipient: Recipient{ChatID: chatID, ChatType: "chat"},
		Body:      Text{Mid: "m1"},
	})
	return body
}

func TestFormatContextBreakdown_NamesTheConversationTheModelAndTheWindow(t *testing.T) {
	bd := &session.ContextBreakdown{
		Conversation:    156369,
		SystemPrompt:    5249,
		ToolDefinitions: 18452,
		Rules:           1958,
		Skills:          1261,
		EstimatedTotal:  183289,
	}
	head := contextHeader{
		sessionID: "sess_2dceef13829e2de3337455da",
		title:     "Проект Max-Gate",
		model:     "deepseek/deepseek-flash",
		window:    1000000,
	}
	got := formatContextBreakdown(bd, head)
	for _, want := range []string{
		"Беседа: «Проект Max-Gate»",
		"Номер: sess_2dceef13829e2de3337455da",
		"Модель: deepseek/deepseek-flash — окно 1 000 000 токенов",
		"Беседа: 156 369",
		"183 289 токенов",
		"— 18% окна",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("/context не содержит %q, ответ:\n%s", want, got)
		}
	}
}

func TestFormatContextBreakdown_WithoutAWindowOrATitle(t *testing.T) {
	bd := &session.ContextBreakdown{Conversation: 10, EstimatedTotal: 10}
	got := formatContextBreakdown(bd, contextHeader{sessionID: "sess_abc", model: "m"})
	for _, unwanted := range []string{"окно", "% окна", "Беседа: «"} {
		if strings.Contains(got, unwanted) {
			t.Errorf("/context без окна и названия не должен содержать %q, ответ:\n%s", unwanted, got)
		}
	}
	if !strings.Contains(got, "Номер: sess_abc") {
		t.Errorf("/context потерял номер беседы:\n%s", got)
	}
}

// MAX renders no markdown: whatever marks /context carries reach the chat as
// characters. The message is plain text, so none of them may appear.
func TestFormatContextBreakdown_HasNoMarkdown(t *testing.T) {
	bd := &session.ContextBreakdown{Conversation: 1000, SystemPrompt: 20, EstimatedTotal: 1020}
	got := formatContextBreakdown(bd, contextHeader{
		sessionID: "sess_abc",
		title:     "Проект Max-Gate",
		model:     "deepseek/deepseek-flash",
		window:    128000,
	})
	for _, mark := range []string{"**", "`", "```", "#", "~~~"} {
		if strings.Contains(got, mark) {
			t.Errorf("/context содержит разметку %q, а MAX её не рисует:\n%s", mark, got)
		}
	}
}

func TestFormatContextBreakdown_ShortensALongTitle(t *testing.T) {
	long := strings.Repeat("я", 200)
	got := formatContextBreakdown(
		&session.ContextBreakdown{EstimatedTotal: 1},
		contextHeader{sessionID: "s", title: long},
	)
	if strings.Contains(got, long) {
		t.Errorf("/context показал название целиком, а должен был укоротить:\n%s", got)
	}
	if !strings.Contains(got, "…") {
		t.Errorf("/context не отметил укороченное название многоточием:\n%s", got)
	}
}

func TestContextHeader_TakesTheModelItsWindowAndTheTitle(t *testing.T) {
	runner := newModelRunner("deepseek/deepseek-flash")
	runner.cfg.Models[0].MaxContextTokens = 1000000
	b := testModelBot(t, runner)
	st, err := runner.EnsureHTTPSession(context.Background(), "sess_ctx", "/w")
	if err != nil {
		t.Fatalf("сессия: %v", err)
	}
	st.SetTitlePinned("Проект Max-Gate")

	head := b.contextHeader(st)
	if head.sessionID != "sess_ctx" {
		t.Errorf("номер беседы = %q, хотим sess_ctx", head.sessionID)
	}
	if head.title != "Проект Max-Gate" {
		t.Errorf("название беседы = %q", head.title)
	}
	if head.model != "deepseek/deepseek-flash" {
		t.Errorf("модель = %q", head.model)
	}
	if head.window != 1000000 {
		t.Errorf("окно = %d, хотим 1000000", head.window)
	}
}

func TestGroupDigits(t *testing.T) {
	cases := map[int]string{
		0:       "0",
		7:       "7",
		999:     "999",
		1000:    "1 000",
		5249:    "5 249",
		183289:  "183 289",
		1000000: "1 000 000",
		-1234:   "-1 234",
	}
	for n, want := range cases {
		if got := groupDigits(n); got != want {
			t.Errorf("groupDigits(%d) = %q, хотим %q", n, got, want)
		}
	}
}
