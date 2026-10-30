//go:build gateway || gateway.max

package max

// Unit tests of the command menu: the request SetCommands makes (PATCH
// /me/commands, the raw Authorization header, the JSON body in the order it was
// given), the refusal it returns to its caller, publishCommands staying quiet
// about a failure, and the guards that keep the published list honest - every
// row a command the bot accepts, every command it accepts a row, and every row
// inside the platform's limits.

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"unicode/utf8"

	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// testCommands is the list the wire tests publish: two rows, in a known order,
// with a Cyrillic description so a mangled encoding shows up.
var testCommands = []BotCommand{
	{Name: "start", Description: "Приветствие и краткое знакомство"},
	{Name: "goal", Description: "Работать над целью до готовности"},
}

// commandRecorder is a stand-in Bot API for PATCH /me/commands: it records the
// request that arrived and answers with what the test asked for.
type commandRecorder struct {
	srv *httptest.Server

	// status and reply are the answer; a zero status is a 200 with "{}".
	status int
	reply  string

	mu     sync.Mutex
	calls  int
	method string
	path   string
	auth   string
	ctype  string
	body   []byte
}

func newCommandRecorder(t *testing.T, status int, reply string) *commandRecorder {
	t.Helper()
	r := &commandRecorder{status: status, reply: reply}
	r.srv = httptest.NewServer(http.HandlerFunc(r.handle))
	t.Cleanup(r.srv.Close)

	return r
}

func (r *commandRecorder) handle(w http.ResponseWriter, req *http.Request) {
	body := new(bytes.Buffer)
	_, _ = body.ReadFrom(req.Body)
	r.mu.Lock()
	r.calls++
	r.method = req.Method
	r.path = req.URL.Path
	r.auth = req.Header.Get("Authorization")
	r.ctype = req.Header.Get("Content-Type")
	r.body = body.Bytes()
	status, reply := r.status, r.reply
	r.mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	if status != 0 {
		w.WriteHeader(status)
	}
	if reply != "" {
		_, _ = w.Write([]byte(reply))
	} else {
		_, _ = w.Write([]byte(`{}`))
	}
}

// client talks to the recorder.
func (r *commandRecorder) client(t *testing.T) *Client {
	t.Helper()

	return NewClient(testToken, WithHTTPClient(r.srv.Client()), WithBaseURL(r.srv.URL))
}

// request is what reached the recorder.
type recordedRequest struct {
	calls        int
	method, path string
	auth, ctype  string
	body         []byte
}

func (r *commandRecorder) request() recordedRequest {
	r.mu.Lock()
	defer r.mu.Unlock()

	return recordedRequest{
		calls:  r.calls,
		method: r.method,
		path:   r.path,
		auth:   r.auth,
		ctype:  r.ctype,
		body:   slices.Clone(r.body),
	}
}

func TestSetCommandsPatchesWithTheListGiven(t *testing.T) {
	rec := newCommandRecorder(t, 0, "")
	if err := rec.client(t).SetCommands(context.Background(), testCommands); err != nil {
		t.Fatalf("SetCommands: %v", err)
	}

	got := rec.request()
	if got.calls != 1 {
		t.Fatalf("PATCH /me/commands: %d requests, want 1", got.calls)
	}
	if got.method != http.MethodPatch {
		t.Errorf("method = %s, want PATCH", got.method)
	}
	if got.path != "/me/commands" {
		t.Errorf("path = %q, want %q", got.path, "/me/commands")
	}
	// The token travels raw, the way the rest of the client sends it.
	if got.auth != testToken {
		t.Errorf("Authorization = %q, want the raw token %q", got.auth, testToken)
	}
	if got.ctype != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", got.ctype)
	}

	// The body is one object carrying the list and nothing else.
	var top map[string]json.RawMessage
	if err := json.Unmarshal(got.body, &top); err != nil {
		t.Fatalf("body %q is not a JSON object: %v", got.body, err)
	}
	if len(top) != 1 {
		t.Errorf("body carries %d fields, want only \"commands\": %s", len(top), got.body)
	}
	raw, ok := top["commands"]
	if !ok {
		t.Fatalf("body has no \"commands\" field: %s", got.body)
	}
	var sent []BotCommand
	if err := json.Unmarshal(raw, &sent); err != nil {
		t.Fatalf("commands %q are not a list of commands: %v", raw, err)
	}
	if !slices.Equal(sent, testCommands) {
		t.Errorf("commands sent = %+v, want %+v in that order", sent, testCommands)
	}
}

func TestSetCommandsNilClearsTheList(t *testing.T) {
	rec := newCommandRecorder(t, 0, "")
	if err := rec.client(t).SetCommands(context.Background(), nil); err != nil {
		t.Fatalf("SetCommands: %v", err)
	}

	got := rec.request()
	if !strings.Contains(string(got.body), `"commands":[]`) {
		t.Errorf("body = %s, want an empty list rather than null", got.body)
	}
}

func TestSetCommandsReturnsServerError(t *testing.T) {
	rec := newCommandRecorder(t, http.StatusInternalServerError, `{"code":"internal","message":"boom"}`)
	err := rec.client(t).SetCommands(context.Background(), testCommands)
	if err == nil {
		t.Fatal("SetCommands = nil, want the 500 back as an error")
	}
	if !IsStatus(err, http.StatusInternalServerError) {
		t.Errorf("SetCommands error = %v, want it to carry status 500", err)
	}
	if !strings.Contains(err.Error(), "boom") {
		t.Errorf("SetCommands error = %q, want the platform's message in it", err)
	}
}

func TestPublishCommandsSendsTheMenu(t *testing.T) {
	rec := newCommandRecorder(t, 0, "")
	var logs bytes.Buffer
	b := &Bot{log: slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))}
	b.setClient(rec.client(t))

	b.publishCommands(context.Background())

	got := rec.request()
	if got.calls != 1 {
		t.Fatalf("publishCommands: %d requests, want 1", got.calls)
	}
	var body struct {
		Commands []BotCommand `json:"commands"`
	}
	if err := json.Unmarshal(got.body, &body); err != nil {
		t.Fatalf("body %q: %v", got.body, err)
	}
	if !slices.Equal(body.Commands, botCommands()) {
		t.Errorf("published %+v, want botCommands()", body.Commands)
	}
	if strings.Contains(logs.String(), "level=WARN") {
		t.Errorf("a successful publish logged a warning:\n%s", logs.String())
	}
}

// TestPublishCommandsWarnsOnRefusal is the one behaviour publishCommands owes
// its caller: a platform that refuses the list is a warning in the log, not a
// failure that stops the bot.
func TestPublishCommandsWarnsOnRefusal(t *testing.T) {
	rec := newCommandRecorder(t, http.StatusInternalServerError, `{"code":"internal","message":"boom"}`)
	var logs bytes.Buffer
	b := &Bot{log: slog.New(slog.NewTextHandler(&logs, nil))}
	b.setClient(rec.client(t))

	b.publishCommands(context.Background())

	out := logs.String()
	if !strings.Contains(out, "level=WARN") || !strings.Contains(out, "publish commands") {
		t.Errorf("log = %q, want a WARN about publishing the commands", out)
	}
	if !strings.Contains(out, "boom") {
		t.Errorf("log = %q, want the platform's message in it", out)
	}
}

func TestPublishCommandsWithoutClient(t *testing.T) {
	b := &Bot{log: slog.New(slog.DiscardHandler)}

	// A stopped bot has no client; publishing is skipped, not a panic.
	b.publishCommands(context.Background())
}

// TestBotCommandsMatchGateway is the guard: the menu may list only what the bot
// accepts, and must list everything it accepts. Either way the drift fails
// here, before somebody types a command that answers nothing or wonders why a
// working command is not hinted.
func TestBotCommandsMatchGateway(t *testing.T) {
	menu := botCommands()
	listed := make(map[string]bool, len(menu))
	for _, c := range menu {
		if listed[c.Name] {
			t.Errorf("botCommands lists %q twice", c.Name)
		}
		listed[c.Name] = true
		if strings.HasPrefix(c.Name, "/") {
			t.Errorf("botCommands lists %q with a slash, the client adds it", c.Name)
		}
		if !knownCommand(c.Name) {
			t.Errorf("botCommands lists %q, which knownCommand refuses: the hint would promise a command the bot ignores", c.Name)
		}
	}

	// The short list a person meets first, the same shape Telegram publishes.
	// The rest of the settings catalogue (reasoning, think, nothink, toolset and
	// the aliases) works but stays out of the hints on purpose: adding one there
	// is a decision, and this row count makes it a spoken one.
	// /stop joined the list on 25.10 by the operator's decision: stopping a turn
	// is the one thing a person reaches for while the bot is busy and cannot
	// wait for it, so it belongs to the short list rather than to the catalogue.
	core := []string{"start", "help", "model", "stop", "agent", "plan", "ask", "context", "goal", "resume", "clear"}
	for _, name := range core {
		if !listed[name] {
			t.Errorf("botCommands is missing %q, which the bot accepts and a person meets first", name)
		}
	}
	if len(menu) != len(core) {
		t.Errorf("botCommands has %d rows, the short list has %d: a row was added or dropped without saying so", len(menu), len(core))
	}

	// The three modes come from the settings registry, not from the switch in
	// processMessage: if the registry ever stopped taking one, the hint would
	// name a mode the gateway does not switch. The other direction is already
	// held by knownCommand above.
	taken := make(map[string]bool, len(core))
	for _, sc := range session.SettingsCommands() {
		taken[sc.Name] = true
	}
	for _, name := range []string{"agent", "plan", "ask"} {
		if !taken[name] {
			t.Errorf("%q is not a settings command any more: the hint would name a mode the gateway cannot switch", name)
		}
	}
}

// TestBotCommandsWithinPlatformLimits holds the bounds of PATCH /me/commands:
// at most 32 rows, a name of 1-64 characters and a description of 1-128. They
// are counted in characters, the way the platform counts them, so a Russian
// description is measured as the person sees it.
func TestBotCommandsWithinPlatformLimits(t *testing.T) {
	menu := botCommands()
	if len(menu) == 0 {
		t.Fatal("botCommands is empty: the client would show no hints")
	}
	if len(menu) > maxMenuCommands {
		t.Errorf("botCommands has %d rows, the platform takes at most %d", len(menu), maxMenuCommands)
	}
	for _, c := range menu {
		if n := utf8.RuneCountInString(c.Name); n < 1 || n > maxMenuNameLen {
			t.Errorf("command %q has a name of %d characters, the platform takes 1-%d", c.Name, n, maxMenuNameLen)
		}
		if n := utf8.RuneCountInString(c.Description); n < 1 || n > maxMenuDescLen {
			t.Errorf("command %q has a description of %d characters, the platform takes 1-%d", c.Name, n, maxMenuDescLen)
		}
		if strings.TrimSpace(c.Description) != c.Description {
			t.Errorf("command %q has padded description %q", c.Name, c.Description)
		}
	}
}
