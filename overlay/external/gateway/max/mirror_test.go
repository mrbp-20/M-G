//go:build gateway || gateway.max

package max

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/external/gateway/sessionstore"
	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// recordSender stands in for the surface that started a turn.
type recordSender struct {
	mu      sync.Mutex
	updates int
}

func (r *recordSender) SendSessionUpdate(string, interface{}) error {
	r.mu.Lock()
	r.updates++
	r.mu.Unlock()

	return nil
}

func (r *recordSender) RequestPermission(context.Context, acp.PermissionRequestParams) (*acp.PermissionResult, error) {
	return &acp.PermissionResult{Outcome: "selected", OptionID: "allow"}, nil
}

func (r *recordSender) RequestQuestion(context.Context, acp.QuestionRequestParams) (*acp.QuestionResult, error) {
	return &acp.QuestionResult{}, nil
}

func (r *recordSender) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()

	return r.updates
}

// TestBoundChat_ReadsTheChatOfASession: the map this bot keeps of the chats it
// serves is the way from a session back to the chat, and a session of another
// gateway is not this bot's business.
func TestBoundChat_ReadsTheChatOfASession(t *testing.T) {
	b := newTestBot(newStubRunner())
	b.store.Bind("max:user:1000001", "sess-direct")
	b.store.Bind("max:chat:900000001", "sess-group")
	b.store.Bind("max:chat:77:admin", "sess-admin")
	b.store.Bind("telegram:user:5", "sess-tg")

	if got, ok := b.boundChat("sess-direct"); !ok || got.UserID != 1000001 {
		t.Errorf("direct chat = %+v/%v, want user 1000001", got, ok)
	}
	if got, ok := b.boundChat("sess-group"); !ok || got.ChatID != 900000001 {
		t.Errorf("group chat = %+v/%v, want chat 900000001", got, ok)
	}
	if got, ok := b.boundChat("sess-admin"); !ok || got.ChatID != 77 {
		t.Errorf("admin-only chat = %+v/%v, want chat 77", got, ok)
	}
	if _, ok := b.boundChat("sess-tg"); ok {
		t.Error("a session of another gateway counted as this bot's chat")
	}
	if _, ok := b.boundChat("sess-unknown"); ok {
		t.Error("an unbound session counted as this bot's chat")
	}
}

// TestMirrorTurn_LeavesItsOwnTurnAlone: a turn the bot started in its own chat
// is already there; mirroring it again would show the answer twice.
func TestMirrorTurn_LeavesItsOwnTurnAlone(t *testing.T) {
	f := newSendServer(t)
	b := newTestBot(newStubRunner())
	b.setClient(f.client(t))
	b.store.Bind("max:user:7", "sess-1")
	own := b.newSender(context.Background(), f.client(t), Target{UserID: 7})

	got, release := b.MirrorTurn("sess-1", own)
	defer release()
	if got != own {
		t.Fatal("the bot's own turn was mirrored back into its chat")
	}
}

// TestMirrorTurn_DeliversIntoTheBoundChat is the bug from the chat: a turn
// started in the browser has to read in the chat its session is bound to.
func TestMirrorTurn_DeliversIntoTheBoundChat(t *testing.T) {
	f := newSendServer(t)
	b := newTestBot(newStubRunner())
	b.setClient(f.client(t))
	b.store.Bind("max:user:7", "sess-1")

	primary := &recordSender{}
	mirrored, release := b.MirrorTurn("sess-1", primary)
	if mirrored == nil {
		t.Fatal("the turn was not mirrored into its chat")
	}
	if err := mirrored.SendSessionUpdate("sess-1", acp.MessageChunkUpdate{Content: acp.ContentBlock{Type: "text", Text: "ответ из веба"}}); err != nil {
		t.Fatalf("update: %v", err)
	}
	release()

	if n := primary.count(); n != 1 {
		t.Fatalf("the browser got %d updates, want 1: the mirror must not swallow them", n)
	}
	var seen bool
	for _, sent := range f.sends() {
		if strings.Contains(sent.body.Text, "ответ из веба") {
			seen = true
		}
	}
	if !seen {
		t.Fatalf("the chat did not get the answer; sent: %+v", f.sends())
	}
}

// TestMirrorTurn_SaysNothingWithoutAChat: a session no chat owns is left as it
// was, and a turn mirrored into nothing must not break the browser's.
func TestMirrorTurn_SaysNothingWithoutAChat(t *testing.T) {
	f := newSendServer(t)
	b := newTestBot(newStubRunner())
	b.setClient(f.client(t))

	primary := &recordSender{}
	mirrored, release := b.MirrorTurn("sess-unknown", primary)
	defer release()
	if mirrored != acp.UpdateSender(primary) {
		t.Fatal("a turn of an unbound session was mirrored somewhere")
	}
	if len(f.sends()) != 0 {
		t.Fatalf("an unbound session posted %d messages", len(f.sends()))
	}
}

// TestSay_ShowsTheLineInTheChat: an answer without the question it replies to
// reads as a monologue in the chat.
func TestSay_ShowsTheLineInTheChat(t *testing.T) {
	f := newSendServer(t)
	b := newTestBot(newStubRunner())
	b.setClient(f.client(t))
	b.store.Bind("max:user:7", "sess-1")

	b.Say("sess-1", "проверка из веба")

	sent := f.sends()
	if len(sent) == 0 {
		t.Fatal("the line written in the browser never reached the chat")
	}
	if got := sent[0].body.Text; !strings.HasPrefix(got, "🌐 проверка из веба") {
		t.Fatalf("message = %q, want the line marked as written elsewhere", got)
	}
}

// silentSender stands in for a surface nobody answers on: it waits for the turn
// to end, the way a page whose tab was closed does.
type silentSender struct {
	mu  sync.Mutex
	err error
}

func (s *silentSender) SendSessionUpdate(string, interface{}) error { return nil }

func (s *silentSender) RequestPermission(ctx context.Context, _ acp.PermissionRequestParams) (*acp.PermissionResult, error) {
	s.await(ctx)

	return &acp.PermissionResult{Outcome: "cancelled", OptionID: "reject"}, nil
}

func (s *silentSender) RequestQuestion(ctx context.Context, _ acp.QuestionRequestParams) (*acp.QuestionResult, error) {
	s.await(ctx)

	return &acp.QuestionResult{}, nil
}

func (s *silentSender) await(ctx context.Context) {
	<-ctx.Done()
	s.mu.Lock()
	s.err = context.Cause(ctx)
	s.mu.Unlock()
}

func (s *silentSender) cause() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.err
}

// answeringSender stands in for the other surface answering: the test opens
// ready to say when it does, so the chat's prompt is posted first.
type answeringSender struct {
	ready   chan struct{}
	answers *acp.QuestionResult
}

func (a *answeringSender) SendSessionUpdate(string, interface{}) error { return nil }

func (a *answeringSender) RequestPermission(context.Context, acp.PermissionRequestParams) (*acp.PermissionResult, error) {
	<-a.ready

	return &acp.PermissionResult{Outcome: "selected", OptionID: "allow"}, nil
}

func (a *answeringSender) RequestQuestion(context.Context, acp.QuestionRequestParams) (*acp.QuestionResult, error) {
	<-a.ready

	return a.answers, nil
}

// waitCause waits for a withdrawn prompt to record why it was withdrawn.
func waitCause(t *testing.T, s *silentSender) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for s.cause() == nil {
		if time.Now().After(deadline) {
			t.Fatal("the other surface's prompt was left waiting after the answer")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// TestMirrorTurn_AsksTheChatAndThePageAtOnce is the report from the chat: a
// question of a turn started in the browser has to be answerable from the chat,
// or the person reading MAX can only watch the turn stall.
func TestMirrorTurn_AsksTheChatAndThePageAtOnce(t *testing.T) {
	f := newSendServer(t)
	b := newTestBot(newStubRunner())
	b.setClient(f.client(t))
	// An open bot: the person of the chat is allowed, so their press is not
	// turned away by the access check every press goes through (sender.go).
	b.cfg.DefaultAccess = config.AccessAll
	b.store.Bind("max:user:7", "sess-1")

	primary := &silentSender{}
	mirrored, release := b.MirrorTurn("sess-1", primary)
	defer release()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	type answer struct {
		res *acp.QuestionResult
		err error
	}
	done := make(chan answer, 1)
	go func() {
		res, err := mirrored.RequestQuestion(ctx, acp.QuestionRequestParams{
			SessionID: "sess-1",
			Questions: []acp.QuestionPrompt{{
				Header:   "Что доделать",
				Question: "Что именно доделать сейчас?",
				Options:  []acp.QuestionOption{{Label: "Отправку видео"}, {Label: "Приём файлов"}},
			}},
		})
		done <- answer{res, err}
	}()

	f.waitSends(t, 1, 5*time.Second)
	rows := buttons(t, f.sends()[0].body.Attachments)
	if len(rows) != 2 || len(rows[0]) != 2 {
		t.Fatalf("the chat was asked with %+v, want the two options", rows)
	}
	if err := b.HandleCallback(context.Background(), f.client(t), pressUpdate(t, Callback{
		CallbackID: "cb-1", Payload: rows[0][1].Payload, User: User{UserID: 7},
	})); err != nil {
		t.Fatalf("HandleCallback: %v", err)
	}
	select {
	case got := <-done:
		if got.err != nil {
			t.Fatalf("RequestQuestion: %v", got.err)
		}
		if len(got.res.Answers) != 1 || len(got.res.Answers[0]) != 1 || got.res.Answers[0][0] != "Приём файлов" {
			t.Fatalf("the press answered %+v, want the second option", got.res)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the turn did not continue after the chat answered")
	}
	waitCause(t, primary)
	if cause := primary.cause(); !errors.Is(cause, session.ErrAnsweredElsewhere) {
		t.Fatalf("the page prompt ended with %v, want it withdrawn as answered elsewhere", cause)
	}
}

// TestJobHeader_MarksAScheduledReport: a report a scheduled job sends is not a
// reply in the conversation, and the chat has to show that at a glance.
func TestJobHeader_MarksAScheduledReport(t *testing.T) {
	at := time.Date(2026, 10, 27, 18, 0, 0, 0, time.Local)
	if got, want := jobHeader("проверка обновлений Coddy", at), "⏰ Задание: проверка обновлений Coddy · 27.10, 18:00"; got != want {
		t.Fatalf("header = %q, want %q", got, want)
	}
	long := strings.Repeat("очень длинное описание ", 20)
	if got := jobHeader(long, at); len([]rune(got)) > jobHeaderRunes+40 {
		t.Fatalf("the header grew to %d runes", len([]rune(got)))
	}
	if got, want := jobHeader("   ", at), "⏰ Задание: задание · 27.10, 18:00"; got != want {
		t.Fatalf("an unnamed job gave %q, want %q", got, want)
	}
}

// TestReportSession_IsTheAdministratorsChat: a scheduled job reports into the
// conversation of an administrator, read from the binding that chat already
// holds - and a gateway whose administrator never wrote has nowhere to report,
// rather than a conversation minted on the spot.
func TestReportSession_IsTheAdministratorsChat(t *testing.T) {
	store := sessionstore.New()
	store.Bind("max:user:1000001", "sess_admin")
	bot := &Bot{cfg: &config.MaxGatewayConfig{Admins: []int64{1000002, 1000001}}, store: store}
	if got, ok := bot.reportSession(); !ok || got != "sess_admin" {
		t.Fatalf("report session = %q (ok=%v), want sess_admin", got, ok)
	}

	stranger := &Bot{cfg: &config.MaxGatewayConfig{Admins: []int64{1000003}}, store: sessionstore.New()}
	if got, ok := stranger.reportSession(); ok || got != "" {
		t.Fatalf("a gateway whose administrator never wrote reported into %q", got)
	}
}

// TestSayJobReport_RefusesToInventAConversation: a report must reach the
// administrator's own conversation, and a gateway whose administrator never
// wrote says it has nowhere to report instead of minting a chat - the caller
// then keeps the text in the job's own session.
func TestSayJobReport_RefusesToInventAConversation(t *testing.T) {
	at := time.Date(2026, 10, 27, 18, 0, 0, 0, time.Local)
	bot := &Bot{cfg: &config.MaxGatewayConfig{Admins: []int64{1000001}}, store: sessionstore.New()}
	if bot.SayJobReport("проверка обновлений Coddy", at, "Павел выпустил 1.3.58") {
		t.Fatal("a report was posted into a conversation that does not exist")
	}

	bound := &Bot{cfg: &config.MaxGatewayConfig{Admins: []int64{1000001}}, store: sessionstore.New()}
	bound.store.Bind("max:user:1000001", "sess_admin")
	if bound.SayJobReport("проверка обновлений Coddy", at, "   ") {
		t.Fatal("an empty report was posted")
	}
}

// TestMirrorTurn_ThePageAnswerWithdrawsTheChatPrompt: when the person answers in
// the browser, the chat says so instead of keeping a keyboard that does nothing.
func TestMirrorTurn_ThePageAnswerWithdrawsTheChatPrompt(t *testing.T) {
	f := newSendServer(t)
	b := newTestBot(newStubRunner())
	b.setClient(f.client(t))
	b.store.Bind("max:user:7", "sess-1")

	primary := &answeringSender{ready: make(chan struct{}), answers: &acp.QuestionResult{Answers: [][]string{{"Париж"}}}}
	mirrored, release := b.MirrorTurn("sess-1", primary)
	defer release()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan *acp.QuestionResult, 1)
	go func() {
		res, err := mirrored.RequestQuestion(ctx, acp.QuestionRequestParams{
			SessionID: "sess-1",
			Questions: []acp.QuestionPrompt{{
				Question: "Какой город?",
				Options:  []acp.QuestionOption{{Label: "Париж"}, {Label: "Рим"}},
			}},
		})
		if err != nil {
			t.Errorf("RequestQuestion: %v", err)
		}
		done <- res
	}()

	f.waitSends(t, 1, 5*time.Second)
	close(primary.ready)
	select {
	case res := <-done:
		if res == nil || len(res.Answers) != 1 || res.Answers[0][0] != "Париж" {
			t.Fatalf("the browser's answer came back as %+v", res)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the turn did not continue after the browser answered")
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		for _, edit := range f.editCalls() {
			if strings.Contains(edit.body.Text, "Ответ дан в другом окне") {
				return
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("the chat prompt was left waiting; edits: %+v", f.editCalls())
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// TestMirrorTurn_AsksAPermissionOnBothSurfaces: a tool call of a subagent stops
// the whole turn, so the chat has to be able to allow it even when the turn was
// started in the browser.
func TestMirrorTurn_AsksAPermissionOnBothSurfaces(t *testing.T) {
	f := newSendServer(t)
	b := newTestBot(newStubRunner())
	b.setClient(f.client(t))
	b.cfg.DefaultAccess = config.AccessAll
	b.store.Bind("max:user:7", "sess-1")

	primary := &silentSender{}
	mirrored, release := b.MirrorTurn("sess-1", primary)
	defer release()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan *acp.PermissionResult, 1)
	go func() {
		res, err := mirrored.RequestPermission(ctx, acp.PermissionRequestParams{
			SessionID:               "sess-1",
			EffectivePermissionMode: "ask",
			ToolCall:                acp.PermissionToolCall{Title: "Пишет файл"},
			Options: []acp.PermissionOption{
				{OptionID: "allow", Name: "Разрешить", Kind: "allow_once"},
				{OptionID: "reject", Name: "Отклонить", Kind: "reject_once"},
			},
		})
		if err != nil {
			t.Errorf("RequestPermission: %v", err)
		}
		done <- res
	}()

	f.waitSends(t, 1, 5*time.Second)
	rows := buttons(t, f.sends()[0].body.Attachments)
	if len(rows) != 1 || len(rows[0]) != 2 {
		t.Fatalf("the chat was asked with %+v, want one row of the two options", rows)
	}
	if err := b.HandleCallback(context.Background(), f.client(t), pressUpdate(t, Callback{
		CallbackID: "cb-7", Payload: rows[0][0].Payload, User: User{UserID: 7},
	})); err != nil {
		t.Fatalf("HandleCallback: %v", err)
	}
	select {
	case res := <-done:
		if res == nil || res.Outcome != "selected" || res.OptionID != "allow" {
			t.Fatalf("the press answered %+v, want the allow option selected", res)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the turn did not continue after the chat allowed the call")
	}
	waitCause(t, primary)
	if cause := primary.cause(); !errors.Is(cause, session.ErrAnsweredElsewhere) {
		t.Fatalf("the page prompt ended with %v, want it withdrawn as answered elsewhere", cause)
	}
}
