//go:build gateway || gateway.max

package max

// Unit tests of the turn's sender, against a fake Bot API of our own (the same
// stand poll_test.go uses, with the write endpoints added): the form of a send,
// the edit that carries the message id, the inline keyboard in the shape the
// schema asks for, the callback that is confirmed, an answer longer than one
// message, and the rate the transport holds the bot at.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
)

// wireBody is the JSON body of a write, with the attachments kept raw: a test
// has to tell "no keyboard at all" (the key is absent) from "take the keyboard
// away" (an empty list), which is the difference the API reads into an edit.
type wireBody struct {
	Text         string          `json:"text"`
	Format       string          `json:"format"`
	Attachments  json.RawMessage `json:"attachments"`
	Notification string          `json:"notification"`
}

// wireCall is one write that reached the fake.
type wireCall struct {
	query url.Values
	body  wireBody
	at    time.Time
}

// answerCall is one press that was confirmed.
type answerCall struct {
	callbackID   string
	notification string
}

// sendServer is a stand-in for the MAX Bot API: the writes are recorded with the
// body and the time they arrived, and a send is answered with the id of a new
// message (mid), which is what the sender edits by.
type sendServer struct {
	srv *httptest.Server

	mu      sync.Mutex
	posted  []wireCall
	edits   []wireCall
	answers []answerCall
	nextID  int
}

func newSendServer(t *testing.T) *sendServer {
	t.Helper()
	f := &sendServer{}
	f.srv = httptest.NewServer(http.HandlerFunc(f.handle))
	t.Cleanup(f.srv.Close)

	return f
}

func (f *sendServer) handle(w http.ResponseWriter, r *http.Request) {
	raw, _ := io.ReadAll(r.Body)
	var body wireBody
	_ = json.Unmarshal(raw, &body)
	call := wireCall{query: r.URL.Query(), body: body, at: time.Now()}
	switch {
	case r.Method == http.MethodPost && r.URL.Path == "/messages":
		f.mu.Lock()
		f.nextID++
		mid := fmt.Sprintf("m%d", f.nextID)
		f.posted = append(f.posted, call)
		f.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"message":{"body":{"mid":%q,"text":%q}}}`, mid, body.Text)
	case r.Method == http.MethodPut && r.URL.Path == "/messages":
		// The real API answers an edit that carries no message_id with HTTP 400
		// "Missing required parameter: message_id". A callback update from MAX
		// carries the id of the original message in message.body.mid, not in the
		// update's own field, so the fake refuses it too: a caller that reads the
		// wrong field fails here instead of on the live wire.
		if r.URL.Query().Get("message_id") == "" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = io.WriteString(w, `{"code":"proto.payload","message":"Missing required parameter: message_id"}`)
			return
		}
		f.mu.Lock()
		f.edits = append(f.edits, call)
		f.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"success":true}`)
	case r.Method == http.MethodPost && r.URL.Path == "/answers":
		f.mu.Lock()
		f.answers = append(f.answers, answerCall{
			callbackID:   r.URL.Query().Get("callback_id"),
			notification: body.Notification,
		})
		f.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"success":true}`)
	case r.Method == http.MethodGet && r.URL.Path == "/me":
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"user_id":1,"name":"Testbot","is_bot":true}`)
	default:
		http.NotFound(w, r)
	}
}

func (f *sendServer) client(t *testing.T) *Client {
	t.Helper()

	return NewClient(testToken, WithHTTPClient(f.srv.Client()), WithBaseURL(f.srv.URL))
}

func (f *sendServer) sends() []wireCall {
	f.mu.Lock()
	defer f.mu.Unlock()

	return append([]wireCall(nil), f.posted...)
}

func (f *sendServer) editCalls() []wireCall {
	f.mu.Lock()
	defer f.mu.Unlock()

	return append([]wireCall(nil), f.edits...)
}

func (f *sendServer) answered() []answerCall {
	f.mu.Lock()
	defer f.mu.Unlock()

	return append([]answerCall(nil), f.answers...)
}

// waitSends waits until n sends have reached the fake.
func (f *sendServer) waitSends(t *testing.T, n int, within time.Duration) {
	t.Helper()
	deadline := time.Now().Add(within)
	for {
		if len(f.sends()) >= n {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%d sends within %v, want %d", len(f.sends()), within, n)
		}
		time.Sleep(time.Millisecond)
	}
}

// buttons reads the inline keyboard of a recorded call. A call with no
// keyboard at all, and one that takes the keyboard away, come back empty.
func buttons(t *testing.T, raw json.RawMessage) [][]Button {
	t.Helper()
	if len(raw) == 0 || strings.TrimSpace(string(raw)) == "[]" {
		return nil
	}
	var atts []struct {
		Type    string `json:"type"`
		Payload struct {
			Buttons [][]Button `json:"buttons"`
		} `json:"payload"`
	}
	if err := json.Unmarshal(raw, &atts); err != nil {
		t.Fatalf("attachments %s: %v", raw, err)
	}
	if len(atts) != 1 || atts[0].Type != "inline_keyboard" {
		t.Fatalf("attachments %s, want one inline_keyboard", raw)
	}
	if len(atts[0].Payload.Buttons) == 0 {
		t.Fatalf("inline_keyboard %s carries no buttons", raw)
	}

	return atts[0].Payload.Buttons
}

// cleared reports whether an edit takes the keyboard of a message away.
func cleared(raw json.RawMessage) bool {
	return strings.TrimSpace(string(raw)) == "[]"
}

// testBot is a bot of the tests: no runner, a logger nobody reads, a cursor
// file in the temporary directory of the test.
func testBot(t *testing.T) *Bot {
	t.Helper()
	cfg := &config.MaxGatewayConfig{}
	cfg.ApplyDefaults()

	return New(cfg, nil, "", slog.New(slog.DiscardHandler), "", filepath.Join(t.TempDir(), "gateway_max_cursor"), nil)
}

// pressUpdate is the message_callback update MAX sends when a button is
// pressed.
func pressUpdate(t *testing.T, cb Callback) Update {
	t.Helper()
	raw, err := json.Marshal(cb)
	if err != nil {
		t.Fatalf("marshal the callback: %v", err)
	}

	return Update{
		UpdateType: "message_callback",
		UserID:     cb.User.UserID,
		User:       &cb.User,
		Callback:   raw,
	}
}

// chunk is one streamed piece of the answer.
func chunk(text string) acp.MessageChunkUpdate {
	return acp.MessageChunkUpdate{
		SessionUpdate: acp.UpdateTypeAgentMessageChunk,
		Content:       acp.ContentBlock{Type: acp.ContentTypeText, Text: text},
	}
}

// TestSender_PostsTheAnswerOnlyWhenTheTurnEnds is the order of a turn: a card
// the turn posts goes out at once, and the text stays out of the chat until the
// turn ends - the answer is the last message, never one the cards below it push
// up the chat.
func TestSender_PostsTheAnswerOnlyWhenTheTurnEnds(t *testing.T) {
	f := newSendServer(t)
	b := testBot(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := b.newSender(ctx, f.client(t), Target{UserID: 7})

	if err := s.SendSessionUpdate("sess", acp.ToolCallUpdate{ToolCallID: "call-1", Title: "websearch"}); err != nil {
		t.Fatalf("SendSessionUpdate: %v", err)
	}
	if err := s.SendSessionUpdate("sess", chunk("hello")); err != nil {
		t.Fatalf("SendSessionUpdate: %v", err)
	}
	f.waitSends(t, 1, 5*time.Second)
	if got := len(f.sends()); got != 1 {
		t.Fatalf("sends = %d before the turn ended, want only the tool card", got)
	}
	s.Flush()
	f.waitSends(t, 2, 5*time.Second)

	calls := f.sends()
	if got := len(calls); got != 2 {
		t.Fatalf("sends = %d, want the tool card and the answer", got)
	}
	if got := len(f.editCalls()); got != 0 {
		t.Fatalf("%d edits, want none: the answer is posted, not streamed", got)
	}
	posted := calls[1]
	if got := posted.query.Get("user_id"); got != "7" {
		t.Fatalf("the answer carried user_id %q, want 7", got)
	}
	if posted.body.Text != "hello" {
		t.Fatalf("the answer carried %q, want hello", posted.body.Text)
	}
	if len(posted.body.Attachments) != 0 {
		t.Fatalf("a plain answer carried attachments %s", posted.body.Attachments)
	}
	if got := calls[0].body.Text; !strings.Contains(got, "websearch") {
		t.Fatalf("the card came second: %q, want the tool call before the answer", got)
	}
}

// TestSender_KeepsOneBlockForTheWholeTurn: the text of a turn goes out as the
// closing answer even when tool calls broke it into blocks, so a turn reads as
// what was done and then what it has to say.
func TestSender_KeepsOneBlockForTheWholeTurn(t *testing.T) {
	f := newSendServer(t)
	b := testBot(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := b.newSender(ctx, f.client(t), Target{UserID: 7})

	for _, u := range []interface{}{
		chunk("Сначала посчитаю."),
		acp.ToolCallUpdate{ToolCallID: "call-1", Title: "websearch"},
		chunk("Вот итог."),
	} {
		if err := s.SendSessionUpdate("sess", u); err != nil {
			t.Fatalf("SendSessionUpdate: %v", err)
		}
	}
	s.Flush()
	f.waitSends(t, 2, 5*time.Second)

	calls := f.sends()
	answer := calls[len(calls)-1].body.Text
	if !strings.Contains(answer, "Сначала посчитаю.") || !strings.Contains(answer, "Вот итог.") {
		t.Fatalf("the closing answer lost part of the turn: %q", answer)
	}
	if !strings.Contains(answer, "Сначала посчитаю.\n\nВот итог.") {
		t.Fatalf("the blocks around the tool call were glued together: %q", answer)
	}
}

// TestSender_SplitsAnAnswerLongerThanOneMessage: MAX caps a message at 4000
// characters. A longer answer is cut into pieces that each fit, posted in order
// as the closing messages of the turn, and nothing of it is dropped.
func TestSender_SplitsAnAnswerLongerThanOneMessage(t *testing.T) {
	f := newSendServer(t)
	b := testBot(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := b.newSender(ctx, f.client(t), Target{UserID: 7})

	// 9000 characters in paragraphs of about 200: three times the budget.
	var sb strings.Builder
	for i := 0; sb.Len() < 9000; i++ {
		fmt.Fprintf(&sb, "## Section %d\n%s\n\n", i, strings.Repeat("x", 180))
	}
	answer := strings.TrimRight(sb.String(), "\n")
	if err := s.SendSessionUpdate("sess", chunk(answer)); err != nil {
		t.Fatalf("SendSessionUpdate: %v", err)
	}
	s.Flush()

	sends := f.sends()
	if got := len(f.editCalls()); got != 0 {
		t.Fatalf("%d edits, want none: the answer is posted, not streamed", got)
	}
	if len(sends) < 2 {
		t.Fatalf("%d sends for a %d character answer, want it cut into pieces",
			len(sends), utf8.RuneCountInString(answer))
	}
	pieces := make([]string, 0, len(sends))
	for _, call := range sends {
		pieces = append(pieces, call.body.Text)
	}
	for i, piece := range pieces {
		if n := utf8.RuneCountInString(piece); n > maxTextRunes {
			t.Fatalf("piece %d is %d characters, over the %d MAX takes", i+1, n, maxTextRunes)
		}
		if strings.TrimSpace(piece) == "" {
			t.Fatalf("piece %d is empty", i+1)
		}
	}
	if got, want := squeeze(strings.Join(pieces, "")), squeeze(answer); got != want {
		t.Fatalf("the pieces hold %d characters of the answer, want %d: text was dropped",
			utf8.RuneCountInString(got), utf8.RuneCountInString(want))
	}
}

// TestSplitText_KeepsEveryPieceWithinTheLimit pins the cutting itself: no piece
// over the limit, none empty, nothing lost, and a text that only just fits left
// alone.
func TestSplitText_KeepsEveryPieceWithinTheLimit(t *testing.T) {
	cases := map[string]string{
		"empty":            "",
		"one rune":         "a",
		"exactly the cap":  strings.Repeat("a", maxTextRunes),
		"one over the cap": strings.Repeat("a", maxTextRunes+1),
		"paragraphs":       strings.Repeat("line of text\n\n", 2000),
		"no break at all":  strings.Repeat("a", 3*maxTextRunes),
		"runes, not bytes": strings.Repeat("я", 2*maxTextRunes),
	}
	for name, text := range cases {
		t.Run(name, func(t *testing.T) {
			pieces := splitText(text, maxTextRunes)
			for i, piece := range pieces {
				if n := utf8.RuneCountInString(piece); n > maxTextRunes {
					t.Fatalf("piece %d is %d characters, over the %d limit", i+1, n, maxTextRunes)
				}
			}
			if got, want := squeeze(strings.Join(pieces, "")), squeeze(text); got != want {
				t.Fatalf("the pieces hold %d characters, want %d", utf8.RuneCountInString(got), utf8.RuneCountInString(want))
			}
			if want := utf8.RuneCountInString(text) <= maxTextRunes; want && len(pieces) != 1 {
				t.Fatalf("a text within the limit came back as %d pieces", len(pieces))
			}
		})
	}
}

// squeeze drops every whitespace rune: splitText trims the whitespace it breaks
// at, so this is how a test compares the pieces with what went in.
func squeeze(s string) string {
	return strings.Join(strings.Fields(s), "")
}

// TestAttachments_MatchTheSchema pins the JSON form of an inline keyboard
// against the example of schema.yaml: one attachment of type inline_keyboard
// whose payload carries the buttons as rows of callback buttons.
func TestAttachments_MatchTheSchema(t *testing.T) {
	got, err := json.Marshal(attachments([][]Button{
		{{Text: "Approve", Payload: "ask:tok:0"}, {Text: "Reject", Payload: "ask:tok:1"}},
	}))
	if err != nil {
		t.Fatalf("marshal the keyboard: %v", err)
	}
	const want = `[{"type":"inline_keyboard","payload":{"buttons":[[` +
		`{"type":"callback","text":"Approve","payload":"ask:tok:0"},` +
		`{"type":"callback","text":"Reject","payload":"ask:tok:1"}]]}}]`
	if !sameJSON(t, got, []byte(want)) {
		t.Fatalf("the keyboard went out as\n%s\nwant\n%s", got, want)
	}
	t.Logf("inline keyboard as it goes on the wire: %s", got)
	// An empty non-nil keyboard is how an edit takes a keyboard away.
	empty, err := json.Marshal(attachments([][]Button{}))
	if err != nil {
		t.Fatalf("marshal an empty keyboard: %v", err)
	}
	if string(empty) != "[]" {
		t.Fatalf("an empty keyboard went out as %s, want []", empty)
	}
	// Rows without keys are dropped rather than sent: the API refuses them.
	dropped, err := json.Marshal(attachments([][]Button{{}, {{Text: "Only", Payload: "p"}}}))
	if err != nil {
		t.Fatalf("marshal a keyboard with an empty row: %v", err)
	}
	if bytes := string(dropped); strings.Count(bytes, "Only") != 1 || strings.Count(bytes, "[[]") != 0 {
		t.Fatalf("an empty row was not dropped: %s", bytes)
	}
}

func sameJSON(t *testing.T, a, b []byte) bool {
	t.Helper()
	var x, y any
	if err := json.Unmarshal(a, &x); err != nil {
		t.Fatalf("decode %s: %v", a, err)
	}
	if err := json.Unmarshal([]byte(b), &y); err != nil {
		t.Fatalf("decode %s: %v", b, err)
	}

	return reflect.DeepEqual(x, y)
}

// TestSender_PermissionButtonsCarryEveryOption: a subagent's request goes to
// the chat as one message with the tool call and a button per option, and the
// press comes back through the payload of the button - not as text.
func TestSender_PermissionButtonsCarryEveryOption(t *testing.T) {
	f := newSendServer(t)
	b := testBot(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := b.newSender(ctx, f.client(t), Target{UserID: 7})

	done := make(chan *acp.PermissionResult, 1)
	go func() {
		res, err := s.RequestPermission(ctx, acp.PermissionRequestParams{
			SessionID:               "sess",
			EffectivePermissionMode: "ask",
			ToolCall:                acp.PermissionToolCall{Title: "Write a file", Content: []acp.ToolCallResultItem{{Content: acp.ContentBlock{Type: acp.ContentTypeText, Text: "rm -rf /"}}}},
			Options: []acp.PermissionOption{
				{OptionID: "allow", Name: "Allow once", Kind: "allow_once"},
				{OptionID: "reject", Name: "Reject", Kind: "reject_once"},
			},
		})
		if err != nil {
			t.Errorf("RequestPermission: %v", err)
		}
		done <- res
	}()

	f.waitSends(t, 1, 5*time.Second)
	posted := f.sends()[0]
	if !strings.Contains(posted.body.Text, "Write a file") || !strings.Contains(posted.body.Text, "rm -rf /") {
		t.Fatalf("the prompt said %q, want the tool call in it", posted.body.Text)
	}
	rows := buttons(t, posted.body.Attachments)
	if len(rows) != 1 || len(rows[0]) != 2 {
		t.Fatalf("the keyboard was %+v, want one row of the two options", rows)
	}
	if rows[0][0].Type != ButtonTypeCallback {
		t.Fatalf("the button type is %q, want %q", rows[0][0].Type, ButtonTypeCallback)
	}
	if rows[0][0].Text != "Allow once" || rows[0][1].Text != "Reject" {
		t.Fatalf("the buttons say %q and %q, want the names of the options", rows[0][0].Text, rows[0][1].Text)
	}
	if !strings.HasPrefix(rows[0][0].Payload, actionAsk+":") {
		t.Fatalf("the payload %q does not name the prompt", rows[0][0].Payload)
	}

	// The person presses "Allow once".
	if err := b.HandleCallback(ctx, f.client(t), pressUpdate(t, Callback{
		CallbackID: "cb-1", Payload: rows[0][0].Payload, User: User{UserID: 7},
	})); err != nil {
		t.Fatalf("HandleCallback: %v", err)
	}
	select {
	case res := <-done:
		if res == nil || res.Outcome != "selected" || res.OptionID != "allow" {
			t.Fatalf("the press answered %+v, want the allow option selected", res)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("RequestPermission did not return after the press")
	}

	// The press is confirmed, or the button keeps spinning for the person.
	answers := f.answered()
	if len(answers) != 1 {
		t.Fatalf("%d callbacks confirmed, want one", len(answers))
	}
	if answers[0].callbackID != "cb-1" {
		t.Fatalf("the confirmation carried callback_id %q, want cb-1", answers[0].callbackID)
	}
	if strings.TrimSpace(answers[0].notification) == "" {
		t.Fatal("the confirmation carried no notification")
	}

	// And the prompt is settled: it says what was chosen and loses its
	// keyboard, so no button of it can be pressed twice.
	edits := f.editCalls()
	if len(edits) != 1 {
		t.Fatalf("%d edits after the press, want the settled prompt", len(edits))
	}
	if !cleared(edits[0].body.Attachments) {
		t.Fatalf("the settled prompt kept its keyboard: %s", edits[0].body.Attachments)
	}
}

// TestHandleCallback_ConfirmsAPressNobodyWaitsFor: a button left over from a
// turn that ended, or a payload this bot never wrote, is confirmed all the
// same - a press MAX hands the bot has to be answered whatever the bot decides
// about it, or the keyboard of the presser waits forever.
func TestHandleCallback_ConfirmsAPressNobodyWaitsFor(t *testing.T) {
	f := newSendServer(t)
	b := testBot(t)
	ctx := context.Background()

	if err := b.HandleCallback(ctx, f.client(t), pressUpdate(t, Callback{
		CallbackID: "cb-9", Payload: "ask:deadbeef:0", User: User{UserID: 7},
	})); err != nil {
		t.Fatalf("HandleCallback: %v", err)
	}
	if err := b.HandleCallback(ctx, f.client(t), pressUpdate(t, Callback{
		CallbackID: "cb-8", Payload: "not-a-prompt", User: User{UserID: 7},
	})); err != nil {
		t.Fatalf("HandleCallback: %v", err)
	}
	answers := f.answered()
	if len(answers) != 2 {
		t.Fatalf("%d callbacks confirmed, want both presses confirmed", len(answers))
	}
	for i, want := range []string{"cb-9", "cb-8"} {
		if answers[i].callbackID != want {
			t.Fatalf("confirmation %d answered %q, want %q", i, answers[i].callbackID, want)
		}
	}
	// A callback update without an id has nothing to answer: it is an error,
	// not a silent success.
	raw := json.RawMessage(`{"payload":"ask:x:0","user":{"user_id":7}}`)
	err := b.HandleCallback(ctx, f.client(t), Update{UpdateType: "message_callback", Callback: raw})
	if err == nil {
		t.Fatal("a callback without callback_id was accepted")
	}
}

// TestSender_QuestionOptionsBecomeButtons: a question with options lists them in
// the message and asks with a button per option, because a button's own label is
// cut down to a word where the text of the option is not.
func TestSender_QuestionOptionsBecomeButtons(t *testing.T) {
	f := newSendServer(t)
	b := testBot(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := b.newSender(ctx, f.client(t), Target{ChatID: 42})

	done := make(chan *acp.QuestionResult, 1)
	go func() {
		res, err := s.RequestQuestion(ctx, acp.QuestionRequestParams{
			SessionID: "sess",
			Questions: []acp.QuestionPrompt{{
				Header:   "Capital",
				Question: "Which city?",
				Options:  []acp.QuestionOption{{Label: "Paris"}, {Label: "Rome"}},
			}},
		})
		if err != nil {
			t.Errorf("RequestQuestion: %v", err)
		}
		done <- res
	}()

	f.waitSends(t, 1, 5*time.Second)
	posted := f.sends()[0]
	if got := posted.query.Get("chat_id"); got != "42" {
		t.Fatalf("the question went to chat_id %q, want 42", got)
	}
	if !strings.Contains(posted.body.Text, "Capital") || !strings.Contains(posted.body.Text, "Which city?") {
		t.Fatalf("the question said %q, want its header and its text", posted.body.Text)
	}
	for _, want := range []string{"A — Paris", "B — Rome"} {
		if !strings.Contains(posted.body.Text, want) {
			t.Fatalf("the question said %q, want the options listed: %q missing", posted.body.Text, want)
		}
	}
	rows := buttons(t, posted.body.Attachments)
	if len(rows) != 2 || len(rows[0]) != 2 || len(rows[1]) != 1 || rows[1][0].Text != customAnswerButton {
		t.Fatalf("the keyboard was %+v, want the two options and the write-it button under them", rows)
	}
	if rows[0][0].Text != "A" || rows[0][1].Text != "B" {
		t.Fatalf("the buttons say %q and %q, want their letters", rows[0][0].Text, rows[0][1].Text)
	}
	if err := b.HandleCallback(ctx, f.client(t), pressUpdate(t, Callback{
		CallbackID: "cb-2", Payload: rows[0][1].Payload, User: User{UserID: 7},
	})); err != nil {
		t.Fatalf("HandleCallback: %v", err)
	}
	select {
	case res := <-done:
		if res == nil || len(res.Answers) != 1 || len(res.Answers[0]) != 1 || res.Answers[0][0] != "Rome" {
			t.Fatalf("the press answered %+v, want Rome", res)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("RequestQuestion did not return after the press")
	}
	// The settled prompt names the option in full, not by its letter.
	edits := f.editCalls()
	if len(edits) != 1 || !strings.Contains(edits[0].body.Text, "✅ Rome") {
		t.Fatalf("the settled prompt said %+v, want the chosen option named", edits)
	}
}

// TestPresses_ReadThePersonFromTheCallback is the report from the chat: the
// keyboard sits on a message the bot wrote, so a presser read from the message
// comes back as the bot and every press is refused as somebody else's.
func TestPresses_ReadThePersonFromTheCallback(t *testing.T) {
	b := testBot(t)
	token, answers := b.asked.offer(Target{UserID: 7}, []string{"A", "B"})
	press := func(user int64) (string, bool) {
		raw := fmt.Sprintf(`{"callback_id":"cb-1","payload":"ask:%s:1","user":{"user_id":%d}}`, token, user)
		u := Update{
			UpdateType: "message_callback",
			// MAX stamps the update with the bot itself: the prompt is its message.
			User:     &User{UserID: 500000001},
			Callback: json.RawMessage(raw),
		}
		cb, err := u.Pressed()
		if err != nil {
			t.Fatal(err)
		}

		return b.resolvePress(u, cb)
	}
	if _, ok := press(9); ok {
		t.Fatal("a press of somebody the prompt was not asked of was accepted")
	}
	note, ok := press(7)
	if !ok {
		t.Fatal("a press of the person the prompt was asked of was refused")
	}
	if note != "✅ B" {
		t.Fatalf("the press answered %q, want the chosen option confirmed", note)
	}
	select {
	case i := <-answers:
		if i.index != 1 {
			t.Fatalf("the press answered option %d, want 1", i.index)
		}
	default:
		t.Fatal("the press answered no prompt")
	}
}

// TestSender_TheWrittenAnswerIsOfferedAndTaken: the card carries the button that
// hands the answer over to a message, says what is expected once it is pressed,
// and the written text is the answer.
func TestSender_TheWrittenAnswerIsOfferedAndTaken(t *testing.T) {
	f := newSendServer(t)
	b := testBot(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := b.newSender(ctx, f.client(t), Target{ChatID: 42})

	done := make(chan *acp.QuestionResult, 1)
	go func() {
		res, err := s.RequestQuestion(ctx, acp.QuestionRequestParams{
			SessionID: "sess",
			Questions: []acp.QuestionPrompt{{
				Question: "Что делаем?",
				Options:  []acp.QuestionOption{{Label: "Первое"}, {Label: "Второе"}},
			}},
		})
		if err != nil {
			t.Errorf("RequestQuestion: %v", err)
		}
		done <- res
	}()

	f.waitSends(t, 1, 5*time.Second)
	rows := buttons(t, f.sends()[0].body.Attachments)
	if len(rows) != 2 || len(rows[1]) != 1 || rows[1][0].Text != customAnswerButton {
		t.Fatalf("the keyboard was %+v, want the options and the write-it button", rows)
	}
	if err := b.HandleCallback(ctx, f.client(t), pressUpdate(t, Callback{
		CallbackID: "cb-9", Payload: rows[1][0].Payload, User: User{UserID: 7},
	})); err != nil {
		t.Fatalf("HandleCallback: %v", err)
	}
	// The card now says what is expected of the next message.
	deadline := time.Now().Add(5 * time.Second)
	for {
		settled := ""
		for _, edit := range f.editCalls() {
			if strings.Contains(edit.body.Text, customAnswerNote) {
				settled = edit.body.Text
			}
		}
		if settled != "" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the card never asked for a written answer: %+v", f.editCalls())
		}
		time.Sleep(2 * time.Millisecond)
	}
	if !b.asked.write(7, "третий вариант") {
		t.Fatal("the written answer was not taken")
	}
	select {
	case res := <-done:
		if res == nil || len(res.Answers) != 1 || len(res.Answers[0]) != 1 || res.Answers[0][0] != "третий вариант" {
			t.Fatalf("the answer came back as %#v, want the written text", res)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the turn did not continue after the written answer")
	}
}

// TestSender_ACommandEndsTheWaitForAWrittenAnswer: the wait a command ended must
// end the question as well, or the turn stands there until it times out with
// nothing left to arrive.
func TestSender_ACommandEndsTheWaitForAWrittenAnswer(t *testing.T) {
	f := newSendServer(t)
	b := testBot(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := b.newSender(ctx, f.client(t), Target{ChatID: 42})

	done := make(chan *acp.QuestionResult, 1)
	go func() {
		res, err := s.RequestQuestion(ctx, acp.QuestionRequestParams{
			SessionID: "sess",
			Questions: []acp.QuestionPrompt{{
				Question: "Что делаем?",
				Options:  []acp.QuestionOption{{Label: "Первое"}},
			}},
		})
		if err != nil {
			t.Errorf("RequestQuestion: %v", err)
		}
		done <- res
	}()

	f.waitSends(t, 1, 5*time.Second)
	rows := buttons(t, f.sends()[0].body.Attachments)
	if err := b.HandleCallback(ctx, f.client(t), pressUpdate(t, Callback{
		CallbackID: "cb-c", Payload: rows[1][0].Payload, User: User{UserID: 7},
	})); err != nil {
		t.Fatalf("HandleCallback: %v", err)
	}
	// A command takes the person away from the prompt.
	b.asked.disarm(7)
	select {
	case res := <-done:
		if res == nil || len(res.Answers) != 1 || len(res.Answers[0]) != 0 {
			t.Fatalf("the question came back as %#v, want it ended without an answer", res)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the question kept waiting after the wait was withdrawn")
	}
}

// TestPresses_RefuseTheWrongPersonAndTheWrongOption: a prompt asked of one
// person is not answered by another, an option the prompt never offered is not
// answered at all, and a prompt is answered once.
func TestPresses_RefuseTheWrongPersonAndTheWrongOption(t *testing.T) {
	var p presses
	token, answers := p.offer(Target{UserID: 7}, []string{"Yes", "No"})

	if _, ok := p.resolve(token, 0, 9); ok {
		t.Fatal("a press from somebody else answered a private prompt")
	}
	if _, ok := p.resolve(token, 5, 7); ok {
		t.Fatal("a press of an option the prompt never offered was accepted")
	}
	label, ok := p.resolve(token, 1, 7)
	if !ok || label != "No" {
		t.Fatalf("the press resolved to %q, %v, want No", label, ok)
	}
	if _, ok := p.resolve(token, 0, 7); ok {
		t.Fatal("a prompt was answered twice")
	}
	select {
	case i := <-answers:
		if i.index != 1 {
			t.Fatalf("the answer carried %d, want 1", i.index)
		}
	default:
		t.Fatal("the answer never reached the prompt")
	}
}

// TestClient_HoldsWritesAtTwoASecond measures the rate the transport keeps: the
// API refuses a bot that writes faster than two requests a second, and the
// sender obeys that through this pace alone.
func TestClient_HoldsWritesAtTwoASecond(t *testing.T) {
	f := newSendServer(t)
	c := f.client(t)
	const writes = 5
	start := time.Now()
	for i := range writes {
		if _, err := c.Send(t.Context(), Outgoing{Target: Target{UserID: 7}, Text: fmt.Sprintf("piece %d", i)}); err != nil {
			t.Fatalf("send %d: %v", i, err)
		}
	}
	elapsed := time.Since(start)
	if len(f.sends()) != writes {
		t.Fatalf("%d sends arrived, want %d", len(f.sends()), writes)
	}
	gaps := time.Duration(writes-1) * writeGap
	if elapsed < gaps {
		t.Fatalf("%d writes took %v, want at least %v: the bot wrote faster than two a second", writes, elapsed, gaps)
	}
	if elapsed > gaps+5*time.Second {
		t.Fatalf("%d writes took %v, want about %v", writes, elapsed, gaps)
	}
}

// TestClient_LeavesReadsAlone: the pace is the rate of a write, not of a call.
// A GET - the long poll above all - must not be held back by the writes before
// it, or the bot would answer late because it had just sent something.
func TestClient_LeavesReadsAlone(t *testing.T) {
	f := newSendServer(t)
	c := f.client(t)
	if _, err := c.Send(t.Context(), Outgoing{Target: Target{UserID: 7}, Text: "one"}); err != nil {
		t.Fatalf("send: %v", err)
	}
	start := time.Now()
	for range 3 {
		if _, err := c.Me(t.Context()); err != nil {
			t.Fatalf("Me: %v", err)
		}
	}
	if elapsed := time.Since(start); elapsed >= writeGap {
		t.Fatalf("three reads took %v, want them unpaced", elapsed)
	}
}

// TestSender_StripsFilePlacementMarkers: the placement markers of shared files
// exist for the web UI, which draws the artifact card where they stand. A chat
// cannot render them, so they never reach it - alone, at the end, or inside a
// longer answer - while text that merely looks like a marker is left as it is.
func TestSender_StripsFilePlacementMarkers(t *testing.T) {
	cases := []struct {
		name   string
		answer string
		want   string
	}{
		{"a marker before the text", `<coddy_file id="0123456789abcdef01234567"/>Документ готов.`, "Документ готов."},
		{"a marker after the text", "Документ готов.\n\n<coddy_file id='abc123'/>", "Документ готов."},
		{"two markers", `<coddy_file id="a"/>Один.<coddy_file id="b"/>`, "Один."},
		{"no id, so not a marker", "Сравни a < b и <coddy_file/> без id.", "Сравни a < b и <coddy_file/> без id."},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newSendServer(t)
			b := testBot(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			s := b.newSender(ctx, f.client(t), Target{UserID: 7})

			if err := s.SendSessionUpdate("sess", chunk(c.answer)); err != nil {
				t.Fatalf("SendSessionUpdate: %v", err)
			}
			s.Flush()

			var texts []string
			for _, call := range f.sends() {
				texts = append(texts, call.body.Text)
			}
			for _, call := range f.editCalls() {
				texts = append(texts, call.body.Text)
			}
			if len(texts) == 0 {
				t.Fatal("nothing was sent")
			}
			for _, text := range texts {
				if strings.Contains(text, "<coddy_file id") {
					t.Fatalf("a placement marker reached the chat: %q", text)
				}
			}
			if got, want := texts[len(texts)-1], quietLinks(c.want); got != want {
				t.Fatalf("the answer reached the chat as %q, want %q", got, want)
			}
		})
	}
}
