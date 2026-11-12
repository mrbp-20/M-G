//go:build (gateway || gateway.max) && gateway.max.groups

package max

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
)

// apiRecorder is a Bot API that answers like MAX and keeps what was asked of it,
// so a notification can be checked by what actually left the bot rather than by
// the function that was supposed to send it.
type apiRecorder struct {
	mu   sync.Mutex
	reqs []string
}

func (r *apiRecorder) add(s string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.reqs = append(r.reqs, s)
}

func (r *apiRecorder) all() []string {
	r.mu.Lock()
	defer r.mu.Unlock()

	return append([]string(nil), r.reqs...)
}

func recordingClient(t *testing.T) (*Client, *apiRecorder) {
	t.Helper()
	rec := &apiRecorder{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf := make([]byte, 8192)
		n, _ := r.Body.Read(buf)
		rec.add(r.URL.RawQuery + " | " + string(buf[:n]))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":true}`))
	}))
	t.Cleanup(srv.Close)
	c := NewClient("t", WithHTTPClient(srv.Client()), WithBaseURL(srv.URL))

	return c, rec
}

func groupCfg() *config.MaxGatewayConfig {
	cfg := testCfg()
	cfg.Chats = []config.GatewayChatConfig{{
		ChatID:    -100500,
		Workspace: "/srv/groups/podborka",
		Access:    config.AccessAdmins,
	}}

	return cfg
}

func TestGroupGate_Admit(t *testing.T) {
	gate := newGroupGate(groupCfg(), nil)
	if err := gate.Admit(-100500); err != nil {
		t.Fatalf("a listed group with a path and an access level: %v", err)
	}
	if err := gate.Admit(-777); err != errGroupNotAllowed {
		t.Errorf("an unlisted group = %v, want %v", err, errGroupNotAllowed)
	}
	if !gate.Enabled() {
		t.Error("Enabled() = false in a build with group support")
	}
	if !strings.Contains(gate.Note(), "включены") {
		t.Errorf("Note() = %q, want it to say groups are on", gate.Note())
	}
}

func TestGroupGate_AdmitWithoutAPath(t *testing.T) {
	cfg := testCfg()
	cfg.Chats = []config.GatewayChatConfig{{ChatID: -100500, Access: config.AccessAdmins}}
	if err := newGroupGate(cfg, nil).Admit(-100500); err != errGroupNoWorkspace {
		t.Errorf("a group without a path = %v, want %v", err, errGroupNoWorkspace)
	}
}

func TestGroupGate_ConfigProblems(t *testing.T) {
	cases := []struct {
		name string
		chat config.GatewayChatConfig
		want string // a substring of the problem, empty when the entry is usable
	}{
		{
			name: "a group without a path",
			chat: config.GatewayChatConfig{ChatID: -1, Access: config.AccessAdmins},
			want: "не указан путь",
		},
		{
			name: "a group with a relative path",
			chat: config.GatewayChatConfig{ChatID: -1, Workspace: "groups/x", Access: config.AccessAdmins},
			want: "не абсолютный",
		},
		{
			name: "a group without an access level",
			chat: config.GatewayChatConfig{ChatID: -1, Workspace: "/srv/x"},
			want: "не указан доступ",
		},
		{
			name: "a complete group",
			chat: config.GatewayChatConfig{ChatID: -1, Workspace: "/srv/x", Access: config.AccessAdmins},
		},
		{
			name: "a direct chat override",
			chat: config.GatewayChatConfig{ChatID: 42, Isolation: config.IsolationShared},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := testCfg()
			cfg.Chats = []config.GatewayChatConfig{tc.chat}
			problems := newGroupGate(cfg, nil).ConfigProblems()
			if tc.want == "" {
				if len(problems) != 0 {
					t.Fatalf("ConfigProblems = %v, want none", problems)
				}

				return
			}
			if len(problems) != 1 || !strings.Contains(problems[0], tc.want) {
				t.Fatalf("ConfigProblems = %v, want one problem mentioning %q", problems, tc.want)
			}
		})
	}
}

// TestBadGroupEntry_TheGatewayStillAnswers is the rule this whole change exists
// for: a mistyped group takes that group down, not the gateway. The admins hear
// about it once, and the personal chat keeps working.
func TestBadGroupEntry_TheGatewayStillAnswers(t *testing.T) {
	cfg := testCfg()
	cfg.Chats = []config.GatewayChatConfig{{ChatID: -7, Access: config.AccessAll}} // no workspace
	r := newStubRunner()
	b := New(cfg, r, "", slog.New(slog.DiscardHandler), "", "", nil)
	c, rec := recordingClient(t)
	b.setClient(c)
	b.burstWindow = time.Millisecond

	b.reportGroupProblems(context.Background())
	if reqs := rec.all(); len(reqs) != 1 {
		t.Fatalf("messages to the admins = %d, want exactly one, not one per restart: %v", len(reqs), reqs)
	} else if !strings.Contains(reqs[0], "не указан путь") {
		t.Errorf("the letter does not name the problem: %s", reqs[0])
	}

	if b.allowed(-7, 1, true) {
		t.Error("a group without a path must stay unanswered")
	}
	if err := b.Handle(context.Background(), inboundMsg("m1", "привет")); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if got := r.waitTurn(t); got != "привет" {
		t.Errorf("the personal chat stopped working: prompt = %q", got)
	}
}

func TestGroupGate_ReportsEachChatOnce(t *testing.T) {
	gate := newGroupGate(testCfg(), nil)
	if !gate.reportOnce(-5) {
		t.Fatal("the first refusal of a chat must be logged loudly")
	}
	if gate.reportOnce(-5) {
		t.Error("the second refusal of the same chat must stay quiet")
	}
	if !gate.reportOnce(-6) {
		t.Error("another chat has its own first refusal")
	}
}

func TestAllowed_GroupComesBeforeThePerson(t *testing.T) {
	b := New(groupCfg(), newStubRunner(), "", slog.New(slog.DiscardHandler), "", "", nil)
	b.setClient(quietAPI())

	if !b.allowed(-100500, 1, true) {
		t.Error("an admin in a listed group must be allowed")
	}
	if b.allowed(-999, 1, true) {
		t.Error("an unlisted group must be refused even for an admin")
	}
	if !b.allowed(42, 1, false) {
		t.Error("a direct chat is unaffected by the group gate")
	}
}

func TestGroupMessage_IsIgnoredWhenTheChatIsNotConfigured(t *testing.T) {
	r := newStubRunner()
	b := New(testCfg(), r, "", slog.New(slog.DiscardHandler), "", "", nil)
	b.setClient(quietAPI())
	b.burstWindow = time.Millisecond

	body, _ := json.Marshal(map[string]any{
		"sender":    map[string]any{"user_id": 1, "name": "Alice"},
		"recipient": map[string]any{"chat_id": -777, "chat_type": "chat"},
		"body":      map[string]any{"mid": "m1", "text": "привет"},
	})
	if err := b.Handle(context.Background(), Update{UpdateType: "message_created", Message: body}); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	select {
	case got := <-r.turns:
		t.Fatalf("a turn ran in an unconfigured group: %q", got)
	case <-time.After(150 * time.Millisecond):
	}
}

func TestHandleGroupEvent_TellsEveryAdmin(t *testing.T) {
	cfg := testCfg()
	cfg.Admins = []int64{1, 2}
	b := New(cfg, newStubRunner(), "", slog.New(slog.DiscardHandler), "", "", nil)
	c, rec := recordingClient(t)
	b.setClient(c)

	u := Update{UpdateType: "bot_added", ChatID: -100500, User: &User{UserID: 7, Name: "Иван"}}
	u.wire = json.RawMessage(`{"update_type":"bot_added","chat_id":-100500,"user":{"user_id":7,"name":"Иван"},"chat":{"title":"Подбор статей"}}`)
	if err := b.Handle(context.Background(), u); err != nil {
		t.Fatalf("Handle: %v", err)
	}

	reqs := rec.all()
	if len(reqs) != 2 {
		t.Fatalf("messages sent = %d, want one per admin: %v", len(reqs), reqs)
	}
	for _, want := range []string{"user_id=1", "user_id=2"} {
		if !strings.Contains(strings.Join(reqs, "\n"), want) {
			t.Errorf("no message addressed with %s in %v", want, reqs)
		}
	}
	body := strings.Join(reqs, "\n")
	for _, want := range []string{"Подбор статей", "Иван", "не разрешена"} {
		if !strings.Contains(body, want) {
			t.Errorf("the note does not mention %q: %s", want, body)
		}
	}
	if strings.Contains(body, "chat_id=-100500 |") {
		t.Errorf("something was sent into the group itself: %v", reqs)
	}
}

func TestGroupAddedNote_SaysWhetherTheGroupIsAllowed(t *testing.T) {
	allowed := groupAddedNote(-5, "Рабочая", "Иван (user_id 7)", true)
	if !strings.Contains(allowed, "уже разрешена") {
		t.Errorf("a working group's note = %q", allowed)
	}
	refused := groupAddedNote(-5, "", "Иван (user_id 7)", false)
	for _, want := range []string{"не разрешена", "chat_id -5", "gateways.max.chats"} {
		if !strings.Contains(refused, want) {
			t.Errorf("a refused group's note misses %q: %s", want, refused)
		}
	}
}

func TestGroupTitle_ReadsTheWire(t *testing.T) {
	u := Update{}
	u.wire = json.RawMessage(`{"chat":{"title":"  Дела отдела  "}}`)
	if got := groupTitle(u); got != "Дела отдела" {
		t.Errorf("groupTitle = %q", got)
	}
	u.wire = json.RawMessage(`{"message":{"recipient":{"title":"Из получателя"}}}`)
	if got := groupTitle(u); got != "Из получателя" {
		t.Errorf("groupTitle from the recipient = %q", got)
	}
	u.wire = json.RawMessage(`{}`)
	if got := groupTitle(u); got != "" {
		t.Errorf("groupTitle without a title = %q, want empty", got)
	}
}

func TestPersonName(t *testing.T) {
	cases := []struct {
		user User
		want string
	}{
		{User{UserID: 7, Name: "Иван"}, "Иван (user_id 7)"},
		{User{UserID: 7, FirstName: "Иван", LastName: "Петров"}, "Иван Петров (user_id 7)"},
		{User{UserID: 7, Username: "ivan"}, "@ivan (user_id 7)"},
		{User{UserID: 7}, "user_id 7"},
		{User{}, ""},
	}
	for _, tc := range cases {
		if got := personName(tc.user); got != tc.want {
			t.Errorf("personName(%+v) = %q, want %q", tc.user, got, tc.want)
		}
	}
}

// The two tests below exercise a group chat, so they live in the build that has
// groups: a group here is configured with a path and an access level first, and
// the admin-only rule is what they check behind that gate.
func TestHandleModelAction_RefusesNonAdminInGroup(t *testing.T) {
	f := newSendServer(t)
	r := newModelRunner("openai/gpt-4o", "rpa/qwen3.6-35b-a3b")
	b := testModelBot(t, r)
	b.setClient(f.client(t))
	// A group works in a real directory: the turn is refused when it is gone, and
	// this test is about the admin-only rule, not about a broken path.
	b.cfg.Chats = []config.GatewayChatConfig{{ChatID: 20, Workspace: t.TempDir(), Access: config.AccessAll}}

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
	// A group works in a real directory: the turn is refused when it is gone, and
	// this test is about the admin-only rule, not about a broken path.
	b.cfg.Chats = []config.GatewayChatConfig{{ChatID: 20, Workspace: t.TempDir(), Access: config.AccessAll}}

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

// groupCfgWith is a configuration with one group chat working in dir.
func groupCfgWith(chatID int64, dir string) *config.MaxGatewayConfig {
	cfg := testCfg()
	cfg.Chats = []config.GatewayChatConfig{{
		ChatID:    chatID,
		Workspace: dir,
		Access:    config.AccessAdmins,
	}}

	return cfg
}

// groupUpdate is a message MAX sends for a group chat.
func groupUpdate(chatID, userID int64, mid, text string) Update {
	body, _ := json.Marshal(map[string]any{
		"sender":    map[string]any{"user_id": userID, "name": "Alice"},
		"recipient": map[string]any{"chat_id": chatID, "chat_type": "chat"},
		"body":      map[string]any{"mid": mid, "text": text},
	})

	return Update{UpdateType: "message_created", Message: body}
}

// cwdOf is the directory the session was started in.
func (r *stubRunner) cwdOf(id string) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if st, ok := r.live[id]; ok {
		return st.GetCWD()
	}

	return ""
}

// lastSession is the session the last turn ran in.
func (r *stubRunner) lastSession() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.sessions) == 0 {
		return ""
	}

	return r.sessions[len(r.sessions)-1]
}

func TestGroupWorkspace_IsResolvedAndChecked(t *testing.T) {
	dir := t.TempDir()
	real, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatalf("EvalSymlinks: %v", err)
	}
	gate := newGroupGate(groupCfgWith(-7, dir), nil)
	got, notice, err := gate.Workspace(-7)
	if err != nil || notice != "" {
		t.Fatalf("Workspace = %q, %q, %v", got, notice, err)
	}
	if got != real {
		t.Errorf("Workspace = %q, want the real path %q", got, real)
	}

	// A file where a directory is expected is not a workspace.
	file := filepath.Join(dir, "файл.txt")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if _, _, err := newGroupGate(groupCfgWith(-7, file), nil).Workspace(-7); err == nil {
		t.Error("a file was accepted as a group directory")
	}
}

func TestGroupWorkspace_FollowsASymlink(t *testing.T) {
	real := t.TempDir()
	link := filepath.Join(t.TempDir(), "ссылка")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("симлинки недоступны: %v", err)
	}
	want, err := filepath.EvalSymlinks(real)
	if err != nil {
		t.Fatalf("EvalSymlinks: %v", err)
	}
	got, _, err := newGroupGate(groupCfgWith(-7, link), nil).Workspace(-7)
	if err != nil {
		t.Fatalf("Workspace: %v", err)
	}
	if got != want {
		t.Errorf("Workspace = %q, want the resolved %q", got, want)
	}
}

func TestGroupWorkspace_TellsAdminsOnceAnHour(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "нет-такого")
	gate := newGroupGate(groupCfgWith(-7, missing), nil)

	_, notice, err := gate.Workspace(-7)
	if err == nil {
		t.Fatal("a missing directory was accepted")
	}
	if notice == "" {
		t.Fatal("the admins were not told the first time")
	}
	for _, want := range []string{"chat_id -7", missing, "молчу"} {
		if !strings.Contains(notice, want) {
			t.Errorf("the notice misses %q: %s", want, notice)
		}
	}
	if _, again, _ := gate.Workspace(-7); again != "" {
		t.Error("the admins were told twice within the hour")
	}
}

func TestGroupTurn_SessionLivesInTheGroupDirectory(t *testing.T) {
	dir := t.TempDir()
	r := newStubRunner()
	b := New(groupCfgWith(-7, dir), r, "/общий/каталог", slog.New(slog.DiscardHandler), "", "", nil)
	b.setClient(quietAPI())
	b.burstWindow = time.Millisecond

	if err := b.Handle(context.Background(), groupUpdate(-7, 1, "m1", "привет")); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if got := r.waitTurn(t); got != "привет" {
		t.Fatalf("prompt = %q", got)
	}
	want, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatalf("EvalSymlinks: %v", err)
	}
	if got := r.cwdOf(r.lastSession()); got != want {
		t.Errorf("the session works in %q, want the group directory %q", got, want)
	}
}

func TestGroupTurn_WithoutADirectoryIsSilentAndTellsAdmins(t *testing.T) {
	cfg := groupCfgWith(-7, filepath.Join(t.TempDir(), "нет-такого"))
	cfg.Admins = []int64{1}
	r := newStubRunner()
	b := New(cfg, r, "/общий/каталог", slog.New(slog.DiscardHandler), "", "", nil)
	c, rec := recordingClient(t)
	b.setClient(c)
	b.burstWindow = time.Millisecond

	if err := b.Handle(context.Background(), groupUpdate(-7, 1, "m1", "привет")); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	select {
	case got := <-r.turns:
		t.Fatalf("a turn ran without a group directory: %q", got)
	case <-time.After(150 * time.Millisecond):
	}
	if reqs := rec.all(); len(reqs) != 1 {
		t.Fatalf("messages to the admins = %d, want 1: %v", len(reqs), reqs)
	}
}

func TestEnsureSession_MovesToTheNewDirectory(t *testing.T) {
	first, second := t.TempDir(), t.TempDir()
	r := newStubRunner()
	b := New(testCfg(), r, "/общий/каталог", slog.New(slog.DiscardHandler), "", "", nil)
	b.setClient(quietAPI())
	ctx := context.Background()
	key := "max:chat:-7"

	st, err := b.ensureSession(ctx, key, first)
	if err != nil {
		t.Fatalf("ensureSession: %v", err)
	}
	oldID := st.ID
	st, err = b.ensureSession(ctx, key, second)
	if err != nil {
		t.Fatalf("ensureSession: %v", err)
	}
	if st.ID == oldID {
		t.Fatal("the session kept its old directory")
	}
	if got := r.cwdOf(st.ID); got != second {
		t.Errorf("the new session works in %q, want %q", got, second)
	}
	if b.store.Get(key) != st.ID {
		t.Errorf("the chat is bound to %q, want the new session %q", b.store.Get(key), st.ID)
	}
}
