//go:build gateway || gateway.max

package max

// Godog harness for features/gateway_max.feature: runs Bot.Start end to end
// against a fake MAX Bot API on httptest, with a scripted agent behind the
// session runner. No LLM and no network beyond the local server.

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cucumber/godog"

	"github.com/EvilFreelancer/coddy-agent/external/gateway/sessionstore"
	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/logger"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

const (
	bddToken   = "max-test-token"
	bddSettle  = 5 * time.Second
	bddPollFor = 20 * time.Millisecond
)

// scriptRunner answers every prompt with a scripted text.
type scriptRunner struct {
	mu         sync.Mutex
	cfg        *config.Config
	live       map[string]*session.State
	answer     string
	prompts    []string
	imageParts [][]acp.ImagePartRef
	// picture is an asset name the fake agent writes into the session's
	// assets and reports as an image it read, the way the real agent does.
	picture string
	// document is a workspace file the fake agent captures as an artefact and
	// reports the way the real agent does after share_file.
	document string
}

func newScriptRunner() *scriptRunner {
	return &scriptRunner{
		cfg: &config.Config{
			Models: []config.ModelEntry{
				{Model: "openai/gpt-4o", Multimodal: true},
				{Model: "rpa/qwen3.6-35b-a3b"},
			},
			Agent: config.Agent{Model: "openai/gpt-4o"},
		},
		live:   map[string]*session.State{},
		answer: "plain answer",
	}
}

// HandleSessionList answers the way a server without other conversations
// does: /resume still has to say something a person can act on.
func (r *scriptRunner) HandleSessionList(_ context.Context, _ acp.SessionListParams) (*acp.SessionListResult, error) {
	return &acp.SessionListResult{}, nil
}

func (r *scriptRunner) EnsureHTTPSession(_ context.Context, sessionID, cwd string) (*session.State, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if strings.TrimSpace(sessionID) == "" {
		return nil, fmt.Errorf("empty session id")
	}
	if st, ok := r.live[sessionID]; ok {
		return st, nil
	}
	st := &session.State{
		ID:         sessionID,
		CWD:        cwd,
		Mode:       session.ModeAgent,
		SessionDir: filepath.Join(cwd, "sessions", sessionID),
	}
	r.live[sessionID] = st
	return st, nil
}

func (r *scriptRunner) HandleSessionPromptWithSender(_ context.Context, params acp.SessionPromptParams, sender acp.UpdateSender, _ *session.PromptRunOpts) (*acp.SessionPromptResult, error) {
	r.mu.Lock()
	var sb strings.Builder
	for _, blk := range params.Prompt {
		sb.WriteString(blk.Text)
	}
	r.prompts = append(r.prompts, sb.String())
	r.imageParts = append(r.imageParts, append([]acp.ImagePartRef(nil), params.ImageParts...))
	r.mu.Unlock()

	r.showPicture(params.SessionID, sender)
	r.shareDocument(params.SessionID, sender)

	_ = sender.SendSessionUpdate(params.SessionID, acp.MessageChunkUpdate{
		SessionUpdate: acp.UpdateTypeAgentMessageChunk,
		Content:       acp.ContentBlock{Type: acp.ContentTypeText, Text: r.answer},
	})
	return &acp.SessionPromptResult{StopReason: acp.StopReasonEndTurn}, nil
}

// showPicture writes a real PNG into the session's assets and reports it the
// way the agent does after reading a picture: a completed tool call whose meta
// carries the image.
func (r *scriptRunner) showPicture(sessionID string, sender acp.UpdateSender) {
	r.mu.Lock()
	name := r.picture
	st := r.live[sessionID]
	r.mu.Unlock()
	if name == "" || st == nil {
		return
	}
	sessionDir := strings.TrimSpace(st.GetPersistedSessionDir())
	if sessionDir == "" {
		return
	}
	png, err := base64.StdEncoding.DecodeString(tinyPNG)
	if err != nil {
		return
	}
	dir := session.AssetsPath(sessionDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return
	}
	if err := os.WriteFile(filepath.Join(dir, name), png, 0o644); err != nil {
		return
	}
	meta := session.ToolImagesMeta(nil, []session.ToolImage{{Name: name, MIMEType: "image/png", Asset: name}})
	_ = sender.SendSessionUpdate(sessionID, acp.ToolCallStatusUpdate{
		SessionUpdate: "tool_call_update",
		ToolCallID:    "read-picture",
		Status:        "completed",
		Meta:          meta,
	})
}

// shareDocument writes a file into the session's workspace, captures it as an
// artefact and reports it the way the agent does after share_file: a completed
// tool call whose meta carries the document by id.
func (r *scriptRunner) shareDocument(sessionID string, sender acp.UpdateSender) {
	r.mu.Lock()
	name := r.document
	st := r.live[sessionID]
	r.mu.Unlock()
	if name == "" || st == nil {
		return
	}
	cwd := strings.TrimSpace(st.GetCWD())
	dir := strings.TrimSpace(st.GetPersistedSessionDir())
	if cwd == "" || dir == "" {
		return
	}
	path := filepath.Join(cwd, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}
	if err := os.WriteFile(path, []byte("%PDF-1.4\n% a document the agent produced\n"), 0o644); err != nil {
		return
	}
	artefact, err := session.CaptureArtifact(dir, cwd, name)
	if err != nil {
		return
	}
	meta := session.ToolFilesMeta(nil, []session.ToolFile{{
		ID:       artefact.ID,
		Name:     name,
		MIMEType: "application/pdf",
		Size:     artefact.Size,
	}})
	_ = sender.SendSessionUpdate(sessionID, acp.ToolCallStatusUpdate{
		SessionUpdate: "tool_call_update",
		ToolCallID:    "share-document",
		Status:        "completed",
		Meta:          meta,
	})
}

func (r *scriptRunner) ForgetLiveSession(id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.live, id)
}

func (r *scriptRunner) HandleSessionCancel(acp.SessionCancelParams) {}

func (r *scriptRunner) HandleSessionSetConfigOption(_ context.Context, params acp.SessionSetConfigOptionParams) (*acp.SessionSetConfigOptionResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	st := r.live[params.SessionID]
	if st == nil {
		return nil, fmt.Errorf("session not found: %s", params.SessionID)
	}
	if params.ConfigID != "model" || r.cfg.FindModelEntry(params.Value) == nil {
		return nil, fmt.Errorf("unknown option %s=%s", params.ConfigID, params.Value)
	}
	st.SetSelectedModelID(params.Value)
	return &acp.SessionSetConfigOptionResult{}, nil
}

func (r *scriptRunner) Cfg() *config.Config {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.cfg
}

func (r *scriptRunner) lastImageParts() []acp.ImagePartRef {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.imageParts) == 0 {
		return nil
	}
	return append([]acp.ImagePartRef(nil), r.imageParts[len(r.imageParts)-1]...)
}

func (r *scriptRunner) promptCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.prompts)
}

// fakeServer is a stand-in MAX Bot API for the BDD scenarios.
type fakeServer struct {
	srv *httptest.Server

	mu      sync.Mutex
	botID   int64
	botName string

	updates []Update
	marker  int64

	calls map[string]int

	messagesByUser map[int64][]string
	messagesByChat map[int64][]string
	nextMid        int

	// lastMessageByUser/Chat holds the most recent bot message, including its
	// mid and attachments, so button-tap steps can resolve the payload that
	// was sent for a given model.
	lastMessageByUser map[int64]*sentMessage
	lastMessageByChat map[int64]*sentMessage
	// sentByUser keeps every message posted to a user in order, so a step can
	// look past the last one for the picture that came before the answer.
	sentByUser map[int64][]*sentMessage
	// editedMessages records messages that were edited, keyed by mid.
	editedMessages map[string]*sentMessage

	// uploads counts the /uploads calls, and uploaded records what arrived at
	// the address it answered: the picture's name, its size and the type its
	// own bytes give.
	uploads     int
	uploaded    []uploadedFile
	uploadFails bool

	// commands is the last hint list the bot published through
	// PATCH /me/commands, and commandsCalls counts how often it did.
	commands      []BotCommand
	commandsCalls int
}

// uploadedFile is one picture the fake server received through the upload
// address, the way the API would have stored it.
type uploadedFile struct {
	name string
	size int
	mime string
}

// sentMessage is one bot message as seen by the fake API.
type sentMessage struct {
	mid         string
	text        string
	attachments []json.RawMessage
}

func newFakeServer(botName string) *fakeServer {
	fs := &fakeServer{
		botID:             1,
		botName:           botName,
		calls:             map[string]int{},
		messagesByUser:    map[int64][]string{},
		messagesByChat:    map[int64][]string{},
		lastMessageByUser: map[int64]*sentMessage{},
		lastMessageByChat: map[int64]*sentMessage{},
		editedMessages:    map[string]*sentMessage{},
		sentByUser:        map[int64][]*sentMessage{},
	}
	fs.srv = httptest.NewServer(http.HandlerFunc(fs.handle))
	return fs
}

func (fs *fakeServer) URL() string { return fs.srv.URL }

func (fs *fakeServer) Close() { fs.srv.Close() }

func (fs *fakeServer) queueUpdate(u Update) {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	fs.updates = append(fs.updates, u)
}

func (fs *fakeServer) pollCount() int {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	return fs.calls["GET /updates"]
}

func (fs *fakeServer) messagesForUser(userID int64) []string {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	return append([]string(nil), fs.messagesByUser[userID]...)
}

func (fs *fakeServer) lastMessageForUser(userID int64) *sentMessage {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	return fs.lastMessageByUser[userID]
}

// sentMessagesForUser returns every message posted to a user, oldest first.
func (fs *fakeServer) sentMessagesForUser(userID int64) []*sentMessage {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	return append([]*sentMessage(nil), fs.sentByUser[userID]...)
}

func (fs *fakeServer) editedMessage(mid string) *sentMessage {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	return fs.editedMessages[mid]
}

func (fs *fakeServer) nextMarker() int64 {
	fs.marker++
	return fs.marker
}

func (fs *fakeServer) handle(w http.ResponseWriter, r *http.Request) {
	fs.mu.Lock()
	fs.calls[r.Method+" "+r.URL.Path]++
	fs.mu.Unlock()

	switch r.URL.Path {
	case "/me":
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(User{UserID: fs.botID, Name: fs.botName})
	case "/me/commands":
		body, _ := io.ReadAll(r.Body)
		var payload struct {
			Commands []BotCommand `json:"commands"`
		}
		_ = json.Unmarshal(body, &payload)

		fs.mu.Lock()
		fs.commandsCalls++
		fs.commands = payload.Commands
		fs.mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":true}`))
	case "/updates":
		fs.mu.Lock()
		upds := make([]Update, len(fs.updates))
		copy(upds, fs.updates)
		fs.updates = fs.updates[:0]
		marker := fs.nextMarker()
		fs.mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(struct {
			Updates []Update `json:"updates"`
			Marker  int64    `json:"marker"`
		}{Updates: upds, Marker: marker})
	case "/messages":
		body, _ := io.ReadAll(r.Body)
		var payload struct {
			Text        string            `json:"text"`
			Attachments []json.RawMessage `json:"attachments"`
		}
		_ = json.Unmarshal(body, &payload)

		fs.mu.Lock()
		switch r.Method {
		case http.MethodPost:
			fs.nextMid++
			mid := fmt.Sprintf("m%d", fs.nextMid)
			msg := &sentMessage{mid: mid, text: payload.Text, attachments: payload.Attachments}
			if uid := r.URL.Query().Get("user_id"); uid != "" {
				if id, err := strconv.ParseInt(uid, 10, 64); err == nil {
					fs.messagesByUser[id] = append(fs.messagesByUser[id], payload.Text)
					fs.lastMessageByUser[id] = msg
					fs.sentByUser[id] = append(fs.sentByUser[id], msg)
				}
			} else if cid := r.URL.Query().Get("chat_id"); cid != "" {
				if id, err := strconv.ParseInt(cid, 10, 64); err == nil {
					fs.messagesByChat[id] = append(fs.messagesByChat[id], payload.Text)
					fs.lastMessageByChat[id] = msg
				}
			}
			fs.mu.Unlock()

			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"message": map[string]any{
					"body": map[string]any{"mid": mid},
				},
			})
		case http.MethodPut:
			mid := r.URL.Query().Get("message_id")
			fs.editedMessages[mid] = &sentMessage{mid: mid, text: payload.Text, attachments: payload.Attachments}
			fs.mu.Unlock()
			w.WriteHeader(http.StatusOK)
		default:
			fs.mu.Unlock()
			http.NotFound(w, r)
		}
	case "/answers":
		w.WriteHeader(http.StatusOK)
	case "/uploads":
		// MAX answers an address of its own for a picture, and the bytes go
		// there without any authorization; that answer names the stored photo
		// by a token.
		if r.Method != http.MethodPost {
			http.NotFound(w, r)
			return
		}
		fs.mu.Lock()
		fs.uploads++
		fs.mu.Unlock()
		address := fs.srv.URL + "/upload-image"
		if r.URL.Query().Get("type") == "file" {
			address = fs.srv.URL + "/upload-file"
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"url": address})
	case "/upload-image":
		fs.mu.Lock()
		fails := fs.uploadFails
		fs.mu.Unlock()
		if fails {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		if err := r.ParseMultipartForm(8 << 20); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		file, header, err := r.FormFile("data")
		if err != nil {
			http.Error(w, "no data field", http.StatusBadRequest)
			return
		}
		defer func() { _ = file.Close() }()
		data, err := io.ReadAll(file)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		fs.mu.Lock()
		fs.uploaded = append(fs.uploaded, uploadedFile{name: header.Filename, size: len(data), mime: http.DetectContentType(data)})
		token := fmt.Sprintf("photo-%d", len(fs.uploaded))
		fs.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"photos": map[string]any{"0": map[string]any{"token": token}},
		})
	case "/upload-file":
		// A document reports its token at the top of the upload answer, not
		// under a photo id - the difference the live probe found.
		if err := r.ParseMultipartForm(8 << 20); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		file, header, err := r.FormFile("data")
		if err != nil {
			http.Error(w, "no data field", http.StatusBadRequest)
			return
		}
		defer func() { _ = file.Close() }()
		data, err := io.ReadAll(file)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		fs.mu.Lock()
		fs.uploaded = append(fs.uploaded, uploadedFile{name: header.Filename, size: len(data), mime: header.Header.Get("Content-Type")})
		token := fmt.Sprintf("file-%d", len(fs.uploaded))
		fs.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"token": token})

	default:
		http.NotFound(w, r)
	}
}

// uploadedFiles is what the fake server received through the upload address.
func (fs *fakeServer) uploadedFiles() []uploadedFile {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	return append([]uploadedFile(nil), fs.uploaded...)
}

// maxWorld holds the fake server, the bot and the run of Start.
type maxWorld struct {
	fake     *fakeServer
	imageSrv *httptest.Server
	runner   *scriptRunner
	bot      *Bot
	dir      string

	cancel context.CancelFunc
	done   chan error
	envSet bool

	people   map[string]int64
	userChat map[string]int64
	nextUser int64
	nextChat int64

	lastPromptCount int
	pollsAtPost     int
}

func newMaxWorld() *maxWorld {
	return &maxWorld{
		people:   map[string]int64{},
		userChat: map[string]int64{},
		nextUser: 100,
		nextChat: 1000,
	}
}

func (w *maxWorld) fakeMAXAPI(botName string) error {
	w.fake = newFakeServer(botName)
	return nil
}

func (w *maxWorld) gatewayPointedAtIt() error {
	dir, err := os.MkdirTemp("", "coddy-max-bdd-")
	if err != nil {
		return err
	}
	w.dir = dir

	png, _ := base64.StdEncoding.DecodeString(tinyPNG)
	w.imageSrv = httptest.NewServer(http.HandlerFunc(func(wr http.ResponseWriter, r *http.Request) {
		wr.Header().Set("Content-Type", "image/png")
		_, _ = wr.Write(png)
	}))

	if err := os.Setenv(config.MaxAPIBaseEnv, w.fake.URL()); err != nil {
		return err
	}
	w.envSet = true

	base, _, _ := logger.New(config.Logger{
		Level:   config.LogLevelError,
		Format:  config.LogFormatText,
		Outputs: []string{config.LogOutputStderr},
	})

	w.runner = newScriptRunner()
	w.bot = New(&config.MaxGatewayConfig{
		Enabled:          true,
		Token:            bddToken,
		Proxy:            "none",
		Admins:           []int64{1},
		DefaultAccess:    config.AccessAll,
		DefaultIsolation: config.IsolationIndividual,
	}, w.runner, w.dir, logger.Component(base, logger.ComponentGatewayMax),
		filepath.Join(w.dir, "sessions.json"),
		filepath.Join(w.dir, "cursor.json"),
		nil,
	)
	w.bot.pollEvery = bddPollFor
	return nil
}

func (w *maxWorld) start() error {
	ctx, cancel := context.WithCancel(context.Background())
	w.cancel = cancel
	w.done = make(chan error, 1)
	go func() { w.done <- w.bot.Start(ctx) }()

	return waitUntil(func() bool {
		return w.bot.connectedClient() != nil
	}, "the bot to connect")
}

func (w *maxWorld) stop() error {
	if w.cancel == nil {
		return nil
	}
	w.cancel()
	w.cancel = nil
	select {
	case err := <-w.done:
		return err
	case <-time.After(10 * time.Second):
		return fmt.Errorf("Start did not return after the stop")
	}
}

func (w *maxWorld) cleanup() {
	_ = w.stop()
	if w.envSet {
		_ = os.Unsetenv(config.MaxAPIBaseEnv)
	}
	if w.fake != nil {
		w.fake.Close()
	}
	if w.imageSrv != nil {
		w.imageSrv.Close()
	}
	if w.dir != "" {
		_ = os.RemoveAll(w.dir)
	}
}

func waitUntil(cond func() bool, what string) error {
	deadline := time.Now().Add(bddSettle)
	for time.Now().Before(deadline) {
		if cond() {
			return nil
		}
		time.Sleep(10 * time.Millisecond)
	}
	return fmt.Errorf("timed out waiting for %s", what)
}

func (w *maxWorld) person(name string) int64 {
	if id, ok := w.people[name]; ok {
		return id
	}
	w.nextUser++
	id := w.nextUser
	w.people[name] = id
	return id
}

func (w *maxWorld) personalChat(userID int64) int64 {
	// A direct chat gets a dedicated chat id, just like a group.
	for name, id := range w.people {
		if id == userID {
			if cid, ok := w.userChat[name]; ok {
				return cid
			}
			w.nextChat++
			w.userChat[name] = w.nextChat
			return w.nextChat
		}
	}
	w.nextChat++
	return w.nextChat
}

func (w *maxWorld) messageUpdate(mid string, sender User, chatID int64, text string) Update {
	msg := Message{
		Sender:    sender,
		Recipient: Recipient{ChatID: chatID, ChatType: "dialog"},
		Body:      Text{Mid: mid, Text: text},
	}
	raw, _ := json.Marshal(msg)
	// The shape MAX sends: the id lives in message.body.mid, and the update
	// carries no message_id, chat_id or user_id of its own.
	return Update{
		UpdateType: "message_created",
		Message:    raw,
	}
}

func (w *maxWorld) photoUpdate(mid string, sender User, chatID int64, text, imageURL string) Update {
	msg := Message{
		Sender:    sender,
		Recipient: Recipient{ChatID: chatID, ChatType: "dialog"},
		Body: Text{
			Mid:  mid,
			Text: text,
			Attachments: []json.RawMessage{
				json.RawMessage(fmt.Sprintf(`{"type":"image","payload":{"photo_id":1,"token":"tok","url":%q}}`, imageURL)),
			},
		},
	}
	raw, _ := json.Marshal(msg)
	return Update{
		UpdateType: "message_created",
		Message:    raw,
	}
}

func (w *maxWorld) writesDirect(name, text string) error {
	uid := w.person(name)
	chatID := w.personalChat(uid)

	w.lastPromptCount = w.runner.promptCount()
	w.pollsAtPost = w.fake.pollCount()

	mid := fmt.Sprintf("%s-%d", name, w.fake.nextMid)
	w.fake.nextMid++
	w.fake.queueUpdate(w.messageUpdate(mid, User{UserID: uid, Name: name}, chatID, text))
	return nil
}

func (w *maxWorld) sendsPhotoDirect(name, text string) error {
	uid := w.person(name)
	chatID := w.personalChat(uid)

	w.lastPromptCount = w.runner.promptCount()
	w.pollsAtPost = w.fake.pollCount()

	mid := fmt.Sprintf("photo-%d", w.fake.nextMid)
	w.fake.nextMid++
	w.fake.queueUpdate(w.photoUpdate(mid, User{UserID: uid, Name: name}, chatID, text, w.imageSrv.URL+"/pic"))
	return nil
}

func (w *maxWorld) botMessageInDirectChat(name string) error {
	uid := w.person(name)
	chatID := w.personalChat(uid)

	w.lastPromptCount = w.runner.promptCount()
	w.pollsAtPost = w.fake.pollCount()

	mid := fmt.Sprintf("bot-%d", w.fake.nextMid)
	w.fake.nextMid++
	w.fake.queueUpdate(w.messageUpdate(mid, User{UserID: w.fake.botID, Name: w.fake.botName, IsBot: true}, chatID, "bot message"))
	return nil
}

func (w *maxWorld) directShows(name, text string) error {
	uid := w.person(name)
	return waitUntil(func() bool {
		for _, m := range w.fake.messagesForUser(uid) {
			if m == text {
				return true
			}
		}
		return false
	}, fmt.Sprintf("bot message %q in direct chat with %s", text, name))
}

func (w *maxWorld) sessionKey(name string) string {
	uid := w.person(name)
	chatID := w.personalChat(uid)
	return sessionstore.SessionKey(adapterName, chatID, uid, config.IsolationIndividual, false)
}

// findModelButton searches an inline keyboard attachment for the button whose
// visible text matches model (with or without the current-model tick) and
// returns its callback payload.
func findModelButton(attachments []json.RawMessage, model string) (string, error) {
	for _, raw := range attachments {
		var a struct {
			Type    string `json:"type"`
			Payload struct {
				Buttons [][]Button `json:"buttons"`
			} `json:"payload"`
		}
		if err := json.Unmarshal(raw, &a); err != nil {
			continue
		}
		if a.Type != "inline_keyboard" {
			continue
		}
		for _, row := range a.Payload.Buttons {
			for _, btn := range row {
				if btn.Text == model || btn.Text == "✓ "+model {
					return btn.Payload, nil
				}
			}
		}
	}
	return "", fmt.Errorf("no button for model %q", model)
}

func (w *maxWorld) offersModelMenu(name string) error {
	return w.writesDirect(name, "/model")
}

// publishedCommandCount waits for the hint list a start publishes: the client
// offers exactly these when somebody types "/". The publish happens after the
// bot reports itself connected, so a scenario may reach this step first.
func (w *maxWorld) publishedCommandCount(n int) error {
	deadline := time.Now().Add(5 * time.Second)
	for {
		w.fake.mu.Lock()
		got := len(w.fake.commands)
		names := make([]string, 0, got)
		for _, cmd := range w.fake.commands {
			names = append(names, cmd.Name)
		}
		w.fake.mu.Unlock()
		if got == n {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("the bot published %d commands, want %d: %v", got, n, names)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func (w *maxWorld) firstPublishedCommandIs(name string) error {
	w.fake.mu.Lock()
	defer w.fake.mu.Unlock()
	if len(w.fake.commands) == 0 {
		return fmt.Errorf("the bot published no commands")
	}
	if got := w.fake.commands[0].Name; got != name {
		return fmt.Errorf("the first published command is %q, want %q", got, name)
	}
	return nil
}

func (w *maxWorld) tapsModelButton(name, model string) error {
	uid := w.person(name)
	chatID := w.personalChat(uid)
	msg := w.fake.lastMessageForUser(uid)
	if msg == nil {
		return fmt.Errorf("no bot message in direct chat with %s", name)
	}
	payload, err := findModelButton(msg.attachments, model)
	if err != nil {
		return err
	}

	cb, _ := json.Marshal(Callback{
		CallbackID: fmt.Sprintf("cb-%s-%s", name, model),
		Payload:    payload,
		User:       User{UserID: uid, Name: name},
	})
	// The callback update carries the original message so the bot can avoid
	// editing the menu when nothing visible changed.
	orig, _ := json.Marshal(Message{
		Sender:    User{UserID: w.fake.botID, IsBot: true},
		Recipient: Recipient{ChatID: chatID, ChatType: "dialog"},
		Body:      Text{Mid: msg.mid, Text: msg.text, Attachments: msg.attachments},
	})
	w.fake.queueUpdate(Update{
		UpdateType: "message_callback",
		ChatID:     chatID,
		UserID:     uid,
		MessageID:  msg.mid,
		Message:    orig,
		Callback:   cb,
	})
	return nil
}

func (w *maxWorld) sessionModelIs(name, model string) error {
	key := w.sessionKey(name)
	store := sessionstore.NewPersisted(filepath.Join(w.dir, "sessions.json"))
	return waitUntil(func() bool {
		w.runner.mu.Lock()
		defer w.runner.mu.Unlock()
		sessionID := store.Peek(key)
		if sessionID == "" {
			return false
		}
		st := w.runner.live[sessionID]
		if st == nil {
			return false
		}
		return st.GetSelectedModelID() == model
	}, fmt.Sprintf("session model for %s to become %q", name, model))
}

func (w *maxWorld) directChatKeyboardHasTick(name, model string) error {
	uid := w.person(name)
	return waitUntil(func() bool {
		msg := w.fake.lastMessageForUser(uid)
		if msg == nil {
			return false
		}
		_, err := findModelButton(msg.attachments, "✓ "+model)
		return err == nil
	}, fmt.Sprintf("keyboard for %s to tick %q", name, model))
}

func (w *maxWorld) directChatEditedKeyboardHasTick(name, model string) error {
	uid := w.person(name)
	return waitUntil(func() bool {
		msg := w.fake.lastMessageForUser(uid)
		if msg == nil {
			return false
		}
		edited := w.fake.editedMessage(msg.mid)
		if edited == nil {
			return false
		}
		_, err := findModelButton(edited.attachments, "✓ "+model)
		return err == nil
	}, fmt.Sprintf("edited keyboard for %s to tick %q", name, model))
}

func (w *maxWorld) agentAsked(text string) error {
	return waitUntil(func() bool {
		w.runner.mu.Lock()
		defer w.runner.mu.Unlock()
		for _, p := range w.runner.prompts {
			// A message with an attachment carries what the gateway says about
			// the saved file after the question, so the question is the start of
			// the prompt, not the whole of it.
			if p == text || strings.HasPrefix(p, text+"\n") {
				return true
			}
		}
		return false
	}, fmt.Sprintf("the agent to be asked %q", text))
}

func (w *maxWorld) agentAskedNothing() error {
	if err := w.waitForPollAfterPost(); err != nil {
		return err
	}
	if n := w.runner.promptCount(); n != 0 {
		return fmt.Errorf("the agent was asked %d time(s), want 0", n)
	}
	return nil
}

func (w *maxWorld) agentAskedNothingMore() error {
	if err := w.waitForPollAfterPost(); err != nil {
		return err
	}
	if got := w.runner.promptCount(); got != w.lastPromptCount {
		return fmt.Errorf("the agent was asked %d time(s), want %d", got, w.lastPromptCount)
	}
	return nil
}

func (w *maxWorld) agentReceivedImagePart(prefix string) error {
	parts := w.runner.lastImageParts()
	if len(parts) == 0 {
		return fmt.Errorf("agent received no image parts")
	}
	if !strings.HasPrefix(parts[len(parts)-1].DataURL, prefix) {
		return fmt.Errorf("last image part data URL %q does not start with %q", parts[len(parts)-1].DataURL, prefix)
	}
	return nil
}

// agentWillShowPicture makes the fake agent read a picture during its next
// turn: it writes the image into the session's assets and reports it in a
// completed tool call, exactly as the real agent does.
func (w *maxWorld) agentWillShowPicture(name string) error {
	w.runner.mu.Lock()
	w.runner.picture = name
	w.runner.mu.Unlock()
	return nil
}

// receivesPhoto checks that the direct chat got the picture the agent read,
// captioned with the asset name and carrying an image attachment. It waits for
// the turn to post it, like directShows does for the answer.
func (w *maxWorld) receivesPhoto(name, caption string) error {
	userID := w.person(name)
	if err := waitUntil(func() bool {
		for _, msg := range w.fake.sentMessagesForUser(userID) {
			// The caption leaves the gateway defused, like every other text.
			if msg.text == quietLinks(caption) {
				return true
			}
		}
		return false
	}, fmt.Sprintf("a photo captioned %q in the direct chat with %s", caption, name)); err != nil {
		return fmt.Errorf("%w, got %v", err, w.fake.messagesForUser(userID))
	}
	for _, msg := range w.fake.sentMessagesForUser(userID) {
		if msg.text != quietLinks(caption) {
			continue
		}
		for _, raw := range msg.attachments {
			var probe struct {
				Type string `json:"type"`
			}
			if json.Unmarshal(raw, &probe) == nil && probe.Type == "image" {
				return nil
			}
		}
		return fmt.Errorf("the message %q carried no image attachment", caption)
	}
	return fmt.Errorf("no message %q in the direct chat with %q", caption, name)
}

// agentWillShareDocument makes the fake agent produce a document during its next
// turn: it captures a workspace file as an artefact and reports it in a completed
// tool call, exactly as the real agent does after share_file.
func (w *maxWorld) agentWillShareDocument(name string) error {
	w.runner.mu.Lock()
	w.runner.document = name
	w.runner.mu.Unlock()
	return nil
}

// receivesDocument checks that the direct chat got the document the agent left,
// captioned with its name, carrying a file attachment, and that the bytes went
// through the upload address first.
func (w *maxWorld) receivesDocument(person, name string) error {
	userID := w.person(person)
	if err := waitUntil(func() bool {
		for _, msg := range w.fake.sentMessagesForUser(userID) {
			if msg.text == quietLinks(name) {
				return true
			}
		}
		return false
	}, fmt.Sprintf("a document named %q in the direct chat with %s", name, person)); err != nil {
		return fmt.Errorf("%w, got %v", err, w.fake.messagesForUser(userID))
	}
	uploaded := false
	for _, f := range w.fake.uploadedFiles() {
		if f.name == name {
			uploaded = true
			break
		}
	}
	if !uploaded {
		return fmt.Errorf("no upload carried %q", name)
	}
	for _, msg := range w.fake.sentMessagesForUser(userID) {
		if msg.text != quietLinks(name) {
			continue
		}
		for _, raw := range msg.attachments {
			var probe struct {
				Type string `json:"type"`
			}
			if json.Unmarshal(raw, &probe) == nil && probe.Type == "file" {
				return nil
			}
		}
		return fmt.Errorf("the message %q carried no file attachment", name)
	}
	return fmt.Errorf("no message %q in the direct chat with %q", name, person)
}

func (w *maxWorld) waitForPollAfterPost() error {
	return waitUntil(func() bool {
		return w.fake.pollCount() > w.pollsAtPost
	}, "the bot to poll after the message")
}

func (w *maxWorld) wroteDirectAndGotAnswer(name, text string) error {
	before := w.runner.promptCount()
	if err := w.writesDirect(name, text); err != nil {
		return err
	}
	if err := waitUntil(func() bool { return w.runner.promptCount() > before }, "the first turn"); err != nil {
		return err
	}
	return w.directShows(name, w.runner.answer)
}

func initializeMAXScenario(sc *godog.ScenarioContext) {
	w := newMaxWorld()
	sc.After(func(ctx context.Context, _ *godog.Scenario, err error) (context.Context, error) {
		w.cleanup()
		return ctx, nil
	})
	sc.Given(`^a fake MAX Bot API whose bot is "([^"]*)"$`, w.fakeMAXAPI)
	sc.Given(`^a max gateway over a scripted agent pointed at it$`, w.gatewayPointedAtIt)
	sc.Given(`^the bot is started$`, w.start)
	sc.Given(`^the bot counts "([^"]*)" among its admins$`, w.countsAmongAdmins)
	sc.Step(`^the person "([^"]*)" writes "([^"]*)" in a direct chat$`, w.writesDirect)
	sc.Step(`^the direct chat with "([^"]*)" shows a bot message "([^"]*)"$`, w.directShows)
	sc.Step(`^the agent was asked "([^"]*)"$`, w.agentAsked)
	sc.Step(`^a message from the bot itself arrives in the direct chat with "([^"]*)"$`, w.botMessageInDirectChat)
	sc.Step(`^the agent was asked nothing$`, w.agentAskedNothing)
	sc.Step(`^the person "([^"]*)" wrote "([^"]*)" in a direct chat and got an answer$`, w.wroteDirectAndGotAnswer)
	sc.Step(`^the agent was asked nothing more$`, w.agentAskedNothingMore)
	sc.Step(`^the person "([^"]*)" sends a photo with text "([^"]*)" in a direct chat$`, w.sendsPhotoDirect)
	sc.Step(`^the agent received an image part starting with "([^"]*)"$`, w.agentReceivedImagePart)
	sc.Step(`^the person "([^"]*)" opens the model menu$`, w.offersModelMenu)
	sc.Step(`^the person "([^"]*)" opens the model menu and is refused$`, w.modelMenuRefused)
	sc.Step(`^the bot published (\d+) commands$`, w.publishedCommandCount)
	sc.Step(`^the first published command is "([^"]*)"$`, w.firstPublishedCommandIs)
	sc.Step(`^the person "([^"]*)" taps the model button "([^"]*)"$`, w.tapsModelButton)
	sc.Step(`^the session model for "([^"]*)" is "([^"]*)"$`, w.sessionModelIs)
	sc.Step(`^the direct chat with "([^"]*)" shows a keyboard that ticks "([^"]*)"$`, w.directChatKeyboardHasTick)
	sc.Step(`^the direct chat with "([^"]*)" shows an edited keyboard that ticks "([^"]*)"$`, w.directChatEditedKeyboardHasTick)
	sc.Step(`^the agent will show the picture "([^"]*)"$`, w.agentWillShowPicture)
	sc.Step(`^the direct chat with "([^"]*)" receives a photo captioned "([^"]*)"$`, w.receivesPhoto)
	sc.Step(`^the agent will share the document "([^"]*)"$`, w.agentWillShareDocument)
	sc.Step(`^the direct chat with "([^"]*)" receives a document named "([^"]*)"$`, w.receivesDocument)
}

func TestGatewayMaxFeatures(t *testing.T) {
	suite := godog.TestSuite{
		Name:                "gateway max",
		ScenarioInitializer: initializeMAXScenario,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"../../../features/gateway_max.feature"},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("gateway max feature suite failed")
	}
}

// countsAmongAdmins makes a person one of the bot's admins: the commands that
// change or replace the conversation belong to them alone (commands.go), and
// /resume is one of those (dispatch.go).
func (w *maxWorld) countsAmongAdmins(name string) error {
	w.bot.cfg.Admins = append(w.bot.cfg.Admins, w.person(name))

	return nil
}

// modelMenuRefused opens the model menu as somebody who is not an admin: the
// chat gets the admin-only note instead of a keyboard. What a session runs on
// is the admins' in every chat, a personal one included (commands.go).
func (w *maxWorld) modelMenuRefused(name string) error {
	if err := w.writesDirect(name, "/model"); err != nil {
		return err
	}
	uid := w.person(name)

	return waitUntil(func() bool {
		for _, m := range w.fake.messagesForUser(uid) {
			if strings.Contains(m, adminOnlyNote) {
				return true
			}
		}

		return false
	}, fmt.Sprintf("the admin-only note in the direct chat with %s", name))
}
