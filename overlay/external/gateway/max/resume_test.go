//go:build gateway || gateway.max

package max

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strconv"
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/external/gateway/sessionstore"
	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// resumeRunner is a session manager stand-in for /resume: it lists the
// sessions the test names, reports where each came from, can refuse to load
// one and can say a session is running a turn.
type resumeRunner struct {
	*stubRunner

	rows    []acp.SessionListInfo
	origins map[string]string
	fail    map[string]error
	busy    map[string]bool

	listErr error
}

func newResumeRunner(rows []acp.SessionListInfo) *resumeRunner {
	return &resumeRunner{
		stubRunner: newStubRunner(),
		rows:       rows,
		origins:    map[string]string{},
		fail:       map[string]error{},
		busy:       map[string]bool{},
	}
}

func (r *resumeRunner) HandleSessionList(_ context.Context, _ acp.SessionListParams) (*acp.SessionListResult, error) {
	if r.listErr != nil {
		return nil, r.listErr
	}

	return &acp.SessionListResult{Sessions: append([]acp.SessionListInfo(nil), r.rows...)}, nil
}

func (r *resumeRunner) SessionOrigin(sessionID string) string { return r.origins[sessionID] }

func (r *resumeRunner) SessionTurnActiveInProcess(sessionID string) bool { return r.busy[sessionID] }

func (r *resumeRunner) EnsureHTTPSession(ctx context.Context, sessionID, cwd string) (*session.State, error) {
	if err := r.fail[sessionID]; err != nil {
		return nil, err
	}

	return r.stubRunner.EnsureHTTPSession(ctx, sessionID, cwd)
}

// resumeRows builds n sessions, newest first, titled "Беседа 1" upwards.
func resumeRows(n int) []acp.SessionListInfo {
	out := make([]acp.SessionListInfo, 0, n)
	for i := 1; i <= n; i++ {
		out = append(out, titled("sess_a"+strconv.Itoa(i), "Беседа "+strconv.Itoa(i)))
	}

	return out
}

// titled is a session row with a name; a row with no name is built directly.
func titled(id, title string) acp.SessionListInfo {
	t := title

	return acp.SessionListInfo{SessionID: id, Title: &t, CWD: "/work"}
}

// resumeKey is the mapping key of the test chat: a direct chat of user 1 with
// no user inside it.
func resumeKey() string {
	return sessionstore.SessionKey(adapterName, 10, 1, config.IsolationIndividual, false)
}

// testInbound is a message the test chat sends.
func testInbound(text string) inbound {
	return inbound{chatID: 10, userID: 1, text: text}
}

// resumePress is the message_callback update MAX sends when a button of the
// resume menu is pressed: the press carries the message it was made on, and
// the id of that message is what an edit addresses.
func resumePress(t *testing.T, payload, mid string) Update {
	t.Helper()
	cb := Callback{CallbackID: "cb1", Payload: payload, User: User{UserID: 1, FirstName: "Alice"}}
	raw, err := json.Marshal(cb)
	if err != nil {
		t.Fatalf("marshal the callback: %v", err)
	}
	body, err := json.Marshal(map[string]any{
		"sender":    map[string]any{"user_id": 1, "name": "Alice"},
		"recipient": map[string]any{"chat_id": 10, "chat_type": "dialog"},
		"body":      map[string]any{"mid": mid, "text": ""},
	})
	if err != nil {
		t.Fatalf("marshal the message: %v", err)
	}

	return Update{
		UpdateType: "message_callback",
		UserID:     1,
		User:       &cb.User,
		Callback:   raw,
		Message:    body,
	}
}

// newResumeBot wires a bot to the runner given and to the fake API of the
// package's sender tests, which records what reaches the wire.
func newResumeBot(t *testing.T, runner SessionRunner) (*Bot, *sendServer) {
	t.Helper()
	f := newSendServer(t)
	b := New(testCfg(), runner, "", slog.New(slog.DiscardHandler), "", "", nil)
	b.setClient(f.client(t))

	return b, f
}

// sentTexts is what the bot posted into the chat, in order.
func sentTexts(f *sendServer) []string {
	sends := f.sends()
	out := make([]string, 0, len(sends))
	for _, call := range sends {
		out = append(out, call.body.Text)
	}

	return out
}

// TestResumeKnownCommand pins /resume as a command the bot handles itself,
// never as a turn of the model.
func TestResumeKnownCommand(t *testing.T) {
	if !knownCommand(callbackActionResume) {
		t.Error("knownCommand(resume) = false, want true")
	}
	if changesSettings(callbackActionResume) {
		t.Error("changesSettings(resume) = true, want the command available to every member")
	}
}

// TestResumeMenuPaginatesTheSessions: nine sessions are two pages of eight and
// one, the first page says which page it is, and its navigation row asks for
// the second one by payload.
func TestResumeMenuPaginatesTheSessions(t *testing.T) {
	r := newResumeRunner(resumeRows(9))
	b, f := newResumeBot(t, r)

	b.handleResume(context.Background(), testInbound("/resume"), resumeKey(), "")
	sends := f.sends()
	if len(sends) != 1 {
		t.Fatalf("%d sends, want the menu", len(sends))
	}
	if text := sends[0].body.Text; !strings.Contains(text, "стр. 1/2") {
		t.Errorf("menu text = %q, want the page of the page count", text)
	}
	kb := buttons(t, sends[0].body.Attachments)
	if len(kb) != resumePageSize+1 {
		t.Fatalf("%d rows, want %d sessions and the navigation row", len(kb), resumePageSize)
	}
	nav := kb[len(kb)-1]
	if len(nav) != 1 || nav[0].Payload != "resume:p:1" || nav[0].Text != "▶" {
		t.Errorf("navigation row = %+v, want one ▶ asking for page 1", nav)
	}
}

// TestResumePageTurnsTheMenuInPlace: a tap on the navigation arrow rewrites
// the message that carries the menu - the same mid, the second page, one
// session and a way back.
func TestResumePageTurnsTheMenuInPlace(t *testing.T) {
	r := newResumeRunner(resumeRows(9))
	b, f := newResumeBot(t, r)
	c := b.connectedClient()

	if err := b.handleResumeAction(context.Background(), c, resumePress(t, "resume:p:1", "mid7"), "p:1"); err != nil {
		t.Fatalf("page turn: %v", err)
	}
	edits := f.editCalls()
	if len(edits) != 1 {
		t.Fatalf("%d edits, want the menu rewritten in place", len(edits))
	}
	if got := edits[0].query.Get("message_id"); got != "mid7" {
		t.Fatalf("edited message %q, want mid7", got)
	}
	if text := edits[0].body.Text; !strings.Contains(text, "стр. 2/2") {
		t.Errorf("edit text = %q, want the second page", text)
	}
	kb := buttons(t, edits[0].body.Attachments)
	if len(kb) != 2 {
		t.Fatalf("%d rows on the last page, want the session and the way back", len(kb))
	}
	if kb[1][0].Payload != "resume:p:0" || kb[1][0].Text != "◀" {
		t.Errorf("navigation row = %+v, want one ◀ asking for page 0", kb[1])
	}
}

// TestResumeButtonMarksTheCurrentSessionAndTheSurface: a label carries ▶️ on
// the chat's own conversation and the sign of where the session started, the
// gateway's own conversations staying unmarked.
func TestResumeButtonMarksTheCurrentSessionAndTheSurface(t *testing.T) {
	r := newResumeRunner(nil)
	r.origins["sess_tg"] = "gateway:telegram"
	r.origins["sess_web"] = ""
	r.origins["sess_print"] = session.PrintOrigin
	r.origins["sess_slack"] = "gateway:slack"
	r.origins["sess_max"] = "gateway:max"
	b, _ := newResumeBot(t, r)

	cases := []struct {
		id      string
		current bool
		want    string
	}{
		{"sess_max", false, "Беседа"},
		{"sess_max", true, "▶️ Беседа"},
		{"sess_tg", true, "▶️ ✈️ Беседа"},
		{"sess_web", false, "🌐 Беседа"},
		{"sess_print", false, "🌐 Беседа"},
		{"sess_slack", false, "💬 Беседа"},
	}
	for _, tc := range cases {
		row := titled(tc.id, "Беседа")
		if got := b.resumeButtonLabel(row, tc.current); got != tc.want {
			t.Errorf("label of %s (current=%v) = %q, want %q", tc.id, tc.current, got, tc.want)
		}
	}
}

// TestResumeButtonLabelIsShort: a title longer than the button holds is cut at
// 32 runes with an ellipsis, and a session with no title is named by the head
// of its id.
func TestResumeButtonLabelIsShort(t *testing.T) {
	r := newResumeRunner(nil)
	r.origins["sess_long"] = ""
	b, _ := newResumeBot(t, r)

	long := titled("sess_long", strings.Repeat("я", 60))
	label := b.resumeButtonLabel(long, true)
	if n := len([]rune(label)); n != resumeLabelMaxRunes {
		t.Fatalf("label %q is %d runes, want %d", label, n, resumeLabelMaxRunes)
	}
	if !strings.HasSuffix(label, "…") {
		t.Errorf("label %q does not end with an ellipsis", label)
	}

	untitled := acp.SessionListInfo{SessionID: "sess_dd76aabbcc"}
	if got, want := b.resumeButtonLabel(untitled, false), "🌐 sess_dd76…"; got != want {
		t.Errorf("label of an untitled session = %q, want %q", got, want)
	}

	// A runner that cannot say where a session started leaves it unmarked
	// rather than guessing at a surface.
	f := newSendServer(t)
	plain := New(testCfg(), newStubRunner(), "", slog.New(slog.DiscardHandler), "", "", nil)
	plain.setClient(f.client(t))
	if got, want := plain.resumeButtonLabel(untitled, false), "sess_dd76…"; got != want {
		t.Errorf("label without an origin reader = %q, want %q", got, want)
	}
}

// TestResumeSearchesByIdPrefixAndTitle: an id typed in full switches at once, a
// prefix and a piece of a title offer what they matched, and a query that
// matches nothing says so.
func TestResumeSearchesByIdPrefixAndTitle(t *testing.T) {
	rows := []acp.SessionListInfo{
		titled("sess_b7", "Правка шлюза"),
		titled("sess_b8", "Правка веба"),
		titled("sess_c1", "Отчёт"),
	}
	r := newResumeRunner(rows)
	b, f := newResumeBot(t, r)
	key := resumeKey()

	// The exact id switches the chat.
	b.handleResume(context.Background(), testInbound("/resume sess_b7"), key, "sess_b7")
	if got := b.store.Peek(key); got != "sess_b7" {
		t.Fatalf("the chat is on %q, want the session named in full", got)
	}
	if got := sentTexts(f)[0]; !strings.Contains(got, "Продолжаю") {
		t.Fatalf("confirmation = %q, want the switch reported", got)
	}

	// A prefix offers every id that starts with it.
	b.handleResume(context.Background(), testInbound("/resume sess_b"), key, "sess_b")
	menu := f.sends()[1]
	if !strings.Contains(menu.body.Text, "нашлось 2") {
		t.Errorf("menu text = %q, want both matches counted", menu.body.Text)
	}
	if kb := buttons(t, menu.body.Attachments); len(kb) != 2 {
		t.Fatalf("%d buttons for two matches", len(kb))
	}

	// A piece of a title is read the same, in any case.
	b.handleResume(context.Background(), testInbound("/resume ПРАВКА"), key, "ПРАВКА")
	if got := sentTexts(f)[2]; !strings.Contains(got, "нашлось 2") {
		t.Errorf("menu text = %q, want the two sessions whose title matched", got)
	}

	// Nothing matched.
	b.handleResume(context.Background(), testInbound("/resume zzz"), key, "zzz")
	if got := sentTexts(f)[3]; !strings.Contains(got, "Не нашёл") {
		t.Errorf("reply = %q, want the query reported as unmatched", got)
	}
	if got := b.store.Peek(key); got != "sess_b7" {
		t.Errorf("the chat is on %q after a query that matched nothing, want it left alone", got)
	}
}

// TestResumeKeepsTheBindingWhenTheSessionCannotBeLoaded: a conversation that
// does not open leaves the chat where it was.
func TestResumeKeepsTheBindingWhenTheSessionCannotBeLoaded(t *testing.T) {
	r := newResumeRunner([]acp.SessionListInfo{titled("sess_broken", "Сломанная")})
	r.fail["sess_broken"] = errors.New("bundle unreadable")
	b, f := newResumeBot(t, r)
	key := resumeKey()
	b.store.Bind(key, "sess_old")

	b.handleResume(context.Background(), testInbound("/resume sess_broken"), key, "sess_broken")
	if got := b.store.Peek(key); got != "sess_old" {
		t.Fatalf("the chat moved to %q, want it left on sess_old", got)
	}
	text := sentTexts(f)[0]
	if !strings.Contains(text, "Не удалось открыть") || !strings.Contains(text, "bundle unreadable") {
		t.Errorf("reply = %q, want the reason reported", text)
	}
}

// TestResumeSwitchesTheChat: a session that opens rewrites the mapping, names
// the session and says what it runs on, and asking for it again changes
// nothing.
func TestResumeSwitchesTheChat(t *testing.T) {
	r := newResumeRunner([]acp.SessionListInfo{titled("sess_b7", "Правка шлюза")})
	r.origins["sess_b7"] = "gateway:telegram"
	b, f := newResumeBot(t, r)
	key := resumeKey()
	b.store.Bind(key, "sess_old")

	b.handleResume(context.Background(), testInbound("/resume sess_b7"), key, "sess_b7")
	if got := b.store.Peek(key); got != "sess_b7" {
		t.Fatalf("the chat is on %q, want sess_b7", got)
	}
	text := sentTexts(f)[0]
	for _, want := range []string{"▶️ Продолжаю: ✈️ Правка шлюза", "sess_b7", "Модель: openai/gpt-4o", "режим: agent"} {
		if !strings.Contains(text, want) {
			t.Errorf("confirmation = %q, want %q in it", text, want)
		}
	}

	b.handleResume(context.Background(), testInbound("/resume sess_b7"), key, "sess_b7")
	if got := sentTexts(f)[1]; !strings.Contains(got, "уже открыта") {
		t.Errorf("second switch = %q, want it reported as the open one", got)
	}
	if got := b.store.Peek(key); got != "sess_b7" {
		t.Errorf("the chat is on %q after the repeat, want sess_b7", got)
	}
}

// TestResumeLogsTheSwitch: the mapping change is an Info line naming the old
// session, the new one, the person and the chat.
func TestResumeLogsTheSwitch(t *testing.T) {
	r := newResumeRunner([]acp.SessionListInfo{titled("sess_b7", "Правка шлюза")})
	f := newSendServer(t)
	var logBuf bytes.Buffer
	b := New(testCfg(), r, "", slog.New(slog.NewTextHandler(&logBuf, nil)), "", "", nil)
	b.setClient(f.client(t))
	key := resumeKey()
	b.store.Bind(key, "sess_old")

	b.handleResume(context.Background(), testInbound("/resume sess_b7"), key, "sess_b7")
	line := logBuf.String()
	for _, want := range []string{"session resumed", "old=sess_old", "new=sess_b7", "user=1", "chat=10"} {
		if !strings.Contains(line, want) {
			t.Errorf("log = %q, want %q in it", line, want)
		}
	}
}

// TestResumePickerTakesTheMenuAway: a tap on a session binds the chat and
// replaces the menu with the confirmation, so no keyboard outlives the choice.
func TestResumePickerTakesTheMenuAway(t *testing.T) {
	r := newResumeRunner([]acp.SessionListInfo{titled("sess_b7", "Правка шлюза")})
	b, f := newResumeBot(t, r)
	c := b.connectedClient()
	key := resumeKey()

	if err := b.handleResumeAction(context.Background(), c, resumePress(t, "resume:s:sess_b7", "mid9"), "s:sess_b7"); err != nil {
		t.Fatalf("pick: %v", err)
	}
	if got := b.store.Peek(key); got != "sess_b7" {
		t.Fatalf("the chat is on %q, want the picked session", got)
	}
	edits := f.editCalls()
	if len(edits) != 1 {
		t.Fatalf("%d edits, want the menu turned into the confirmation", len(edits))
	}
	if got := edits[0].query.Get("message_id"); got != "mid9" {
		t.Fatalf("edited message %q, want the one the press came from", got)
	}
	if text := edits[0].body.Text; !strings.Contains(text, "Продолжаю") {
		t.Errorf("edit text = %q, want the confirmation", text)
	}
	if !cleared(edits[0].body.Attachments) {
		t.Errorf("attachments = %s, want the keyboard taken away", edits[0].body.Attachments)
	}
}

// TestResumeCallbackIsAnswered: a resume tap is confirmed to MAX, silently, and
// a press that names a session the listing no longer holds is answered instead
// of binding the chat to a bundle that is gone.
func TestResumeCallbackIsAnswered(t *testing.T) {
	r := newResumeRunner([]acp.SessionListInfo{titled("sess_b7", "Правка шлюза")})
	b, f := newResumeBot(t, r)
	c := b.connectedClient()

	if err := b.HandleCallback(context.Background(), c, resumePress(t, "resume:s:sess_b7", "mid9")); err != nil {
		t.Fatalf("callback: %v", err)
	}
	answers := f.answered()
	if len(answers) != 1 || answers[0].callbackID != "cb1" || answers[0].notification != "" {
		t.Fatalf("answers = %+v, want one silent answer of cb1", answers)
	}

	// A digest payload of a session that is gone resolves to nothing, and the
	// chat stays where it was.
	b.store.Bind(resumeKey(), "sess_b7")
	gone := callbackDigestMarker + "0123456789"
	if err := b.handleResumeAction(context.Background(), c, resumePress(t, "resume:s:"+gone, "mid10"), "s:"+gone); err != nil {
		t.Fatalf("pick of a gone session: %v", err)
	}
	if got := b.store.Peek(resumeKey()); got != "sess_b7" {
		t.Fatalf("the chat is on %q after a gone pick, want sess_b7", got)
	}
	if got := sentTexts(f)[0]; !strings.Contains(got, "больше нет") {
		t.Errorf("reply = %q, want the session reported as gone", got)
	}
}

// TestResumeWithoutALister: a runner that cannot list sessions answers so,
// rather than panicking on a missing method.
func TestResumeWithoutALister(t *testing.T) {
	f := newSendServer(t)
	b := New(testCfg(), newStubRunner(), "", slog.New(slog.DiscardHandler), "", "", nil)
	b.setClient(f.client(t))

	b.handleResume(context.Background(), testInbound("/resume"), resumeKey(), "")
	texts := sentTexts(f)
	if len(texts) != 1 || !strings.Contains(texts[0], "недоступен") {
		t.Fatalf("replies = %v, want one saying the list is unavailable", texts)
	}
}

// TestResumeWithNoSessions: an empty server and a listing that failed are told
// apart.
func TestResumeWithNoSessions(t *testing.T) {
	r := newResumeRunner(nil)
	b, f := newResumeBot(t, r)

	b.handleResume(context.Background(), testInbound("/resume"), resumeKey(), "")
	if got := sentTexts(f)[0]; !strings.Contains(got, "Бесед пока нет") {
		t.Errorf("reply = %q, want the empty listing reported", got)
	}

	r.listErr = errors.New("disk on fire")
	b.handleResume(context.Background(), testInbound("/resume"), resumeKey(), "")
	if got := sentTexts(f)[1]; !strings.Contains(got, "disk on fire") {
		t.Errorf("reply = %q, want the failure reported", got)
	}
}

// TestResumeSaysWhenTheSessionIsBusy: a conversation running a turn is opened
// anyway, and the confirmation says the next message waits; a session the turn
// lock keeps from opening is reported with the same line, and the chat stays
// where it was.
func TestResumeSaysWhenTheSessionIsBusy(t *testing.T) {
	r := newResumeRunner([]acp.SessionListInfo{titled("sess_b7", "Правка шлюза"), titled("sess_b8", "Другая")})
	r.busy["sess_b7"] = true
	r.fail["sess_b8"] = session.ErrSessionTurnBusy
	b, f := newResumeBot(t, r)
	key := resumeKey()

	b.handleResume(context.Background(), testInbound("/resume sess_b7"), key, "sess_b7")
	if got := sentTexts(f)[0]; !strings.Contains(got, "идёт работа") {
		t.Errorf("confirmation = %q, want the running turn mentioned", got)
	}

	b.store.Bind(key, "sess_b7")
	b.handleResume(context.Background(), testInbound("/resume sess_b8"), key, "sess_b8")
	text := sentTexts(f)[1]
	if !strings.Contains(text, "Не удалось открыть") || !strings.Contains(text, "идёт работа") {
		t.Errorf("reply = %q, want the busy lock named with the refusal", text)
	}
	if got := b.store.Peek(key); got != "sess_b7" {
		t.Errorf("the chat is on %q after a busy refusal, want sess_b7", got)
	}
}
