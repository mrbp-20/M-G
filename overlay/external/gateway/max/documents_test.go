//go:build gateway || gateway.max

package max

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// fastRetries makes the pauses between attempts short for a test and puts the
// real ones back afterwards. It swaps a package variable, so a test that uses it
// must not run in parallel with another one that does: none does today.
func fastRetries(t *testing.T) {
	t.Helper()
	saved := documentRetryPauses
	documentRetryPauses = []time.Duration{time.Millisecond, time.Millisecond, time.Millisecond}
	t.Cleanup(func() { documentRetryPauses = saved })
}

// testPauses is what a test passes to sendFileMessage directly: the pauses are
// an argument now, so a video's longer waits do not slow a test down.
var testPauses = []time.Duration{time.Millisecond, time.Millisecond, time.Millisecond}

func fileSender(t *testing.T, u *uploadServer) *Sender {
	t.Helper()
	return testBot(t).newSender(context.Background(), clientOf(u), Target{UserID: 7})
}

// TestSendFileMessage_RetriesTheMessageOnly pins the measured behaviour: an
// attachment that is still being processed answers 400 attachment.not.ready,
// and a later attempt lands.
func TestSendFileMessage_RetriesTheMessageOnly(t *testing.T) {
	fastRetries(t)
	u := newUploadServer(t, `{"token":"file-token"}`)
	attempts := 0
	u.onMessage = func(w http.ResponseWriter, r *http.Request) bool {
		attempts++
		if attempts == 1 {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = io.WriteString(w, `{"code":"attachment.not.ready","message":"not processed"}`)
			return true
		}
		return false
	}

	mid, tries, err := fileSender(t, u).sendFileMessage(context.Background(), Outgoing{
		Target: Target{UserID: 7}, Text: "документ", FileToken: "file-token",
	}, testPauses)
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	if tries != 2 {
		t.Errorf("attempts = %d, want 2", tries)
	}
	if mid != "mid.1" {
		t.Errorf("mid = %q", mid)
	}
	if attempts != 2 {
		t.Errorf("the platform saw %d message calls, want 2", attempts)
	}
}

// TestSendFileMessage_GivesUpAfterFourAttempts bounds the wait: four attempts
// and then the document is reported, not retried into the turn's budget.
func TestSendFileMessage_GivesUpAfterFourAttempts(t *testing.T) {
	fastRetries(t)
	u := newUploadServer(t, `{}`)
	u.onMessage = func(w http.ResponseWriter, r *http.Request) bool {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"code":"attachment.not.ready","message":"still not processed"}`)
		return true
	}

	_, tries, err := fileSender(t, u).sendFileMessage(context.Background(), Outgoing{
		Target: Target{UserID: 7}, Text: "документ", FileToken: "file-token",
	}, testPauses)
	if err == nil {
		t.Fatal("a document the platform never accepts must fail")
	}
	if tries != 4 {
		t.Errorf("attempts = %d, want the limit of 4", tries)
	}
	if len(u.messages) != 4 {
		t.Errorf("the platform saw %d message calls, want 4", len(u.messages))
	}
}

// TestPostDocument_SendsTheArtefact walks the whole path for one document: a
// session artefact goes out as the bytes of the upload, and the message carries
// the file attachment captioned with the name and what the client wrote.
func TestPostDocument_SendsTheArtefact(t *testing.T) {
	u := newUploadServer(t, `{"token":"file-token"}`)
	sessionDir := t.TempDir()
	workspace := t.TempDir()
	data := []byte("%PDF-1.4 artefact bytes")
	if err := os.WriteFile(filepath.Join(workspace, "report.pdf"), data, 0o644); err != nil {
		t.Fatalf("write the workspace file: %v", err)
	}
	artefact, err := session.CaptureArtifact(sessionDir, workspace, "report.pdf")
	if err != nil {
		t.Fatalf("capture: %v", err)
	}

	fileSender(t, u).postDocument(sessionDir, session.ToolFile{
		ID:       artefact.ID,
		Name:     "Отчёт.pdf",
		MIMEType: "application/pdf",
		Caption:  "готово, проверьте",
	})

	if u.filename != "Отчёт.pdf" {
		t.Errorf("uploaded filename = %q", u.filename)
	}
	if !strings.HasPrefix(u.mimeType, "application/pdf") {
		t.Errorf("uploaded media type = %q", u.mimeType)
	}
	if string(u.body) != string(data) {
		t.Errorf("uploaded %d bytes, want the artefact's %d", len(u.body), len(data))
	}
	if len(u.messages) != 1 {
		t.Fatalf("messages = %d, want 1", len(u.messages))
	}
	want := quietLinks("Отчёт.pdf\n\nготово, проверьте")
	if got := u.messages[0]["text"]; got != want {
		t.Errorf("caption = %v, want %v", got, want)
	}
	list, _ := u.messages[0]["attachments"].([]any)
	if len(list) != 1 {
		t.Fatalf("attachments = %v", u.messages[0]["attachments"])
	}
	first, _ := list[0].(map[string]any)
	payload, _ := first["payload"].(map[string]any)
	if first["type"] != "file" || payload["token"] != "file-token" {
		t.Errorf("attachment = %v, want a file carrying the token", first)
	}
}

func TestSendFiles_DeliversWhenTheChatWasSwitched(t *testing.T) {
	// The field case: /resume moved the chat to another conversation while the
	// turn ran, the update carried the turn's own session, and the guard on the
	// identity dropped the file the person had asked for without a word.
	u := newUploadServer(t, `{"token":"file-token"}`)
	sessionDir := t.TempDir()
	workspace := t.TempDir()
	data := []byte("%PDF-1.4 artefact of a turn whose chat moved on")
	if err := os.WriteFile(filepath.Join(workspace, "doc.pdf"), data, 0o644); err != nil {
		t.Fatalf("write the workspace file: %v", err)
	}
	artefact, err := session.CaptureArtifact(sessionDir, workspace, "doc.pdf")
	if err != nil {
		t.Fatalf("capture: %v", err)
	}

	s := fileSender(t, u)
	s.files = stubFiles{id: "sess-turn", dir: sessionDir, cwd: workspace}

	s.sendFiles("sess-other", []session.ToolFile{{
		ID:       artefact.ID,
		Name:     "doc.pdf",
		MIMEType: "application/pdf",
	}})

	if len(u.messages) != 1 {
		t.Fatalf("messages = %d, want the document delivered anyway", len(u.messages))
	}
}

func TestSendFiles_AQuietTurnStillSendsItsDocument(t *testing.T) {
	// A detached turn says nothing into the chat, but the file was promised
	// here before the switch, so it still arrives.
	u := newUploadServer(t, `{"token":"file-token"}`)
	sessionDir := t.TempDir()
	workspace := t.TempDir()
	data := []byte("%PDF-1.4 delivered after the switch")
	if err := os.WriteFile(filepath.Join(workspace, "late.pdf"), data, 0o644); err != nil {
		t.Fatalf("write the workspace file: %v", err)
	}
	artefact, err := session.CaptureArtifact(sessionDir, workspace, "late.pdf")
	if err != nil {
		t.Fatalf("capture: %v", err)
	}

	s := fileSender(t, u)
	s.files = stubFiles{id: "sess-turn", dir: sessionDir, cwd: workspace}
	s.Detach()

	meta := session.ToolFilesMeta(nil, []session.ToolFile{{
		ID:       artefact.ID,
		Name:     "late.pdf",
		MIMEType: "application/pdf",
	}})
	if err := s.SendSessionUpdate("sess-turn", acp.ToolCallStatusUpdate{
		ToolCallID: "call-1",
		Status:     "completed",
		Meta:       meta,
	}); err != nil {
		t.Fatalf("update: %v", err)
	}

	if len(u.messages) != 1 {
		t.Fatalf("messages = %d, want the document of a detached turn delivered", len(u.messages))
	}
}

// TestPostDocument_UnknownArtefactSendsNothing keeps the session's own store as
// the only source: an id it does not hold is not a document.
func TestPostDocument_UnknownArtefactSendsNothing(t *testing.T) {
	u := newUploadServer(t, `{"token":"file-token"}`)
	fileSender(t, u).postDocument(t.TempDir(), session.ToolFile{ID: "art_nothing", Name: "x.pdf"})
	if len(u.messages) != 0 || u.filename != "" {
		t.Errorf("something was sent for an unknown artefact: messages=%d upload=%q", len(u.messages), u.filename)
	}
}

// TestDocumentCaption is the agreed shape: the name first, then what the client
// wrote, cut at the message budget with an ellipsis.
func TestDocumentCaption(t *testing.T) {
	if got := documentCaption("Счет.pdf", ""); got != "Счет.pdf" {
		t.Errorf("caption without text = %q", got)
	}
	if got := documentCaption("Счет.pdf", "  готово  "); got != "Счет.pdf\n\nготово" {
		t.Errorf("caption = %q", got)
	}
	long := strings.Repeat("я", maxTextRunes+500)
	got := documentCaption("Счет.pdf", long)
	if n := utf8.RuneCountInString(got); n != maxTextRunes {
		t.Errorf("cut caption is %d runes, want %d", n, maxTextRunes)
	}
	if !strings.HasPrefix(got, "Счет.pdf\n\n") {
		t.Errorf("the name must survive the cut: %q", got[:20])
	}
	if !strings.HasSuffix(got, "…") {
		t.Error("a cut caption must say so")
	}
}

// TestFileRefusal pins the limits this gateway keeps: the platform's size for
// each kind, and the kinds it sends at all. MAX enumerates no video or sound
// formats, so those lists are ours too.
func TestFileRefusal(t *testing.T) {
	cases := []struct {
		name string
		size int64
		want string
	}{
		{"Счет.pdf", 1024, ""},
		{"выгрузка.XLSX", 1024, ""},
		{"архив.zip", 1024, ""},
		{"клип.mp4", 1024, ""},
		{"запись.m4a", 1024, ""},
		{"huge.pdf", maxDocumentBytes + 1, "он больше 50 МБ"},
		{"huge.mp4", maxVideoBytes + 1, "он больше 250 МБ"},
		{"huge.mp3", maxAudioBytes + 1, "он больше 256 МБ"},
		{"payload.exe", 1024, "тип «exe» не в списке отправляемых"},
		{"без-расширения", 1024, "у него нет расширения"},
	}
	for _, c := range cases {
		got := fileRefusal(c.name, outgoingKind(c.name), c.size)
		if c.want == "" && got != "" {
			t.Errorf("%s: refused with %q, want it sent", c.name, got)
		}
		if c.want != "" && !strings.Contains(got, c.want) {
			t.Errorf("%s: reason = %q, want %q", c.name, got, c.want)
		}
	}
}

// TestPostDocument_RefusesAKindAndSaysSo: a refused document is not uploaded at
// all - no token is created for a file that cannot leave - and the chat is told
// why in the chat's own language.
func TestPostDocument_RefusesAKindAndSaysSo(t *testing.T) {
	u := newUploadServer(t, `{"token":"file-token"}`)
	sessionDir := t.TempDir()
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "payload.exe"), []byte("MZ"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	artefact, err := session.CaptureArtifact(sessionDir, workspace, "payload.exe")
	if err != nil {
		t.Fatalf("capture: %v", err)
	}

	fileSender(t, u).postDocument(sessionDir, session.ToolFile{ID: artefact.ID, Name: "payload.exe"})

	if u.filename != "" || string(u.body) != "" {
		t.Errorf("a refused document was uploaded: %q, %d bytes", u.filename, len(u.body))
	}
	if len(u.messages) != 1 {
		t.Fatalf("messages = %d, want one refusal", len(u.messages))
	}
	text, _ := u.messages[0]["text"].(string)
	if !strings.Contains(text, "Не отправляю документ") || !strings.Contains(text, "exe") {
		t.Errorf("refusal = %q, want the reason in Russian", text)
	}
}

// TestCleanDocumentName keeps a name a chat can show: no directories, no
// control characters, nothing unbounded.
func TestCleanDocumentName(t *testing.T) {
	if got := cleanDocumentName("../../etc/passwd", "fallback.pdf"); got != "passwd" {
		t.Errorf("name = %q, want the base only", got)
	}
	if got := cleanDocumentName("Счет\r\n№1.pdf", "fallback.pdf"); got != "Счет№1.pdf" {
		t.Errorf("control characters survived: %q", got)
	}
	if got := cleanDocumentName("  ", "fallback.pdf"); got != "fallback.pdf" {
		t.Errorf("empty name = %q, want the fallback", got)
	}
	long := strings.Repeat("я", 300) + ".pdf"
	if got := cleanDocumentName(long, "fallback.pdf"); utf8.RuneCountInString(got) != maxDocumentNameRunes {
		t.Errorf("long name kept %d runes, want %d", utf8.RuneCountInString(got), maxDocumentNameRunes)
	}
}

// stubFiles is the chat session as the document guard sees it.
type stubFiles struct {
	id, dir, cwd string
	restriction  *session.TurnRestriction
}

func (s stubFiles) GetID() string                                { return s.id }
func (s stubFiles) GetPersistedSessionDir() string               { return s.dir }
func (s stubFiles) GetCWD() string                               { return s.cwd }
func (s stubFiles) GetTurnRestriction() *session.TurnRestriction { return s.restriction }

// TestPostDocument_RefusesATamperedCopy: the store records a digest, and a copy
// that changed since is not the session's document.
func TestPostDocument_RefusesATamperedCopy(t *testing.T) {
	u := newUploadServer(t, `{"token":"file-token"}`)
	sessionDir := t.TempDir()
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "report.pdf"), []byte("original bytes"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	artefact, err := session.CaptureArtifact(sessionDir, workspace, "report.pdf")
	if err != nil {
		t.Fatalf("capture: %v", err)
	}
	snapshot := session.ArtifactPath(sessionDir, artefact.SHA256)
	// The store makes its copy read-only, so a tamper has to lift that first -
	// which is itself worth knowing: the protection is there by default.
	if err := os.Chmod(snapshot, 0o600); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	if err := os.WriteFile(snapshot, []byte("tampered bytes"), 0o600); err != nil {
		t.Fatalf("tamper: %v", err)
	}

	fileSender(t, u).postDocument(sessionDir, session.ToolFile{ID: artefact.ID, Name: "report.pdf"})

	if u.filename != "" || string(u.body) != "" {
		t.Errorf("a tampered copy was uploaded: %q, %d bytes", u.filename, len(u.body))
	}
	if len(u.messages) != 1 {
		t.Fatalf("messages = %d, want one refusal", len(u.messages))
	}
	if text, _ := u.messages[0]["text"].(string); !strings.Contains(text, "изменилась") {
		t.Errorf("refusal = %q, want it to name the changed copy", text)
	}
}

// TestPostDocument_ConfinedTurnRefusesOutsideWorkspace: a restricted turn may
// send only what was captured inside its own working directory.
func TestPostDocument_ConfinedTurnRefusesOutsideWorkspace(t *testing.T) {
	u := newUploadServer(t, `{"token":"file-token"}`)
	sessionDir := t.TempDir()
	capturedIn := t.TempDir()
	otherWorkspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(capturedIn, "report.pdf"), []byte("%PDF"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	artefact, err := session.CaptureArtifact(sessionDir, capturedIn, "report.pdf")
	if err != nil {
		t.Fatalf("capture: %v", err)
	}

	s := fileSender(t, u)
	s.files = stubFiles{
		id: "sess_1", dir: sessionDir, cwd: otherWorkspace,
		restriction: &session.TurnRestriction{ConfineToWorkspace: true},
	}
	s.postDocument(sessionDir, session.ToolFile{ID: artefact.ID, Name: "report.pdf"})

	if u.filename != "" {
		t.Errorf("a confined turn uploaded a document from outside its workspace: %q", u.filename)
	}
	if len(u.messages) != 1 {
		t.Fatalf("messages = %d, want one refusal", len(u.messages))
	}
	if text, _ := u.messages[0]["text"].(string); !strings.Contains(text, "вне рабочей папки") {
		t.Errorf("refusal = %q", text)
	}
}

// TestPostDocument_ConfinedTurnAllowsItsOwnWorkspace is the other side: the
// same restriction and a document captured where the turn works goes out.
func TestPostDocument_ConfinedTurnAllowsItsOwnWorkspace(t *testing.T) {
	u := newUploadServer(t, `{"token":"file-token"}`)
	sessionDir := t.TempDir()
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "report.pdf"), []byte("%PDF ok"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	artefact, err := session.CaptureArtifact(sessionDir, workspace, "report.pdf")
	if err != nil {
		t.Fatalf("capture: %v", err)
	}

	s := fileSender(t, u)
	s.files = stubFiles{
		id: "sess_1", dir: sessionDir, cwd: workspace,
		restriction: &session.TurnRestriction{ConfineToWorkspace: true},
	}
	s.postDocument(sessionDir, session.ToolFile{ID: artefact.ID, Name: "report.pdf"})

	if u.filename != "report.pdf" {
		t.Fatalf("the document of the turn's own workspace did not leave: %q (messages=%d)", u.filename, len(u.messages))
	}
}

// TestSendFileMessage_DoesNotRetryOtherRefusals: only attachment.not.ready says
// "the upload is still being processed". Anything else is final after one try,
// or a document would be retried into the turn's budget for no reason.
func TestSendFileMessage_DoesNotRetryOtherRefusals(t *testing.T) {
	fastRetries(t)
	u := newUploadServer(t, `{}`)
	u.onMessage = func(w http.ResponseWriter, r *http.Request) bool {
		w.WriteHeader(http.StatusForbidden)
		_, _ = io.WriteString(w, `{"code":"attachment.not.processed","message":"forbidden"}`)
		return true
	}

	_, tries, err := fileSender(t, u).sendFileMessage(context.Background(), Outgoing{
		Target: Target{UserID: 7}, Text: "документ", FileToken: "file-token",
	}, testPauses)
	if err == nil {
		t.Fatal("a refusal must be reported")
	}
	if tries != 1 {
		t.Errorf("attempts = %d, want 1: only attachment.not.ready is retried", tries)
	}
	if len(u.messages) != 1 {
		t.Errorf("the platform saw %d message calls, want 1", len(u.messages))
	}
}

func TestOutgoingKind_ByExtension(t *testing.T) {
	cases := map[string]string{
		"клип.mp4":       "video",
		"клип.m4v":       "video",
		"клип.f4v":       "video",
		"клип.avi":       "video",
		"ролик.MOV":      "video",
		"запись.mkv":     "video",
		"ролик.webm":     "video",
		"голос.mp3":      "audio",
		"голос.wav":      "audio",
		"голос.m4a":      "audio",
		"голос.ogg":      "audio",
		"голос.flac":     "audio",
		"отчёт.pdf":      "file",
		"таблица.xlsx":   "file",
		"снимок.png":     "file",
		"payload.exe":    "",
		"без-расширения": "",
	}
	for name, want := range cases {
		if got := outgoingKind(name); got != want {
			t.Errorf("outgoingKind(%q) = %q, хотим %q", name, got, want)
		}
	}
}

// TestMessage_InboundFiles_KeepsTheDurationAsSent: the platform's number is not
// a length yet - the units differ between the attachment field and the video
// details answer - so parsing keeps it as it came and the size-based reading
// happens once the file is on disk (inboundDuration).
func TestMessage_InboundFiles_KeepsTheDurationAsSent(t *testing.T) {
	m := Message{Body: Text{Attachments: []json.RawMessage{
		[]byte(`{"type":"video","payload":{"url":"https://i/v"},"duration":4000}`),
		[]byte(`{"type":"audio","payload":{"url":"https://i/a"},"duration":66500}`),
	}}}
	files, err := m.InboundFiles()
	if err != nil {
		t.Fatalf("InboundFiles: %v", err)
	}
	if files[0].Duration != 4000 {
		t.Errorf("длительность видео %d, хотим как прислали — 4000", files[0].Duration)
	}
	if files[1].Duration != 66500 {
		t.Errorf("длительность записи %d, хотим как прислали — 66500", files[1].Duration)
	}
}

func TestKindWord(t *testing.T) {
	cases := map[string]string{"video": "видео", "audio": "запись", "file": "документ", "": "документ"}
	for kind, want := range cases {
		if got := kindWord(kind); got != want {
			t.Errorf("kindWord(%q) = %q, хотим %q", kind, got, want)
		}
	}
}

// TestMediaRetryPauses: the platform transcodes a video or a recording after
// the upload, so the message may be refused for longer than a document's. The
// waits grow and there is one attempt more than for a document.
func TestMediaRetryPauses(t *testing.T) {
	if len(mediaRetryPauses) != len(documentRetryPauses)+1 {
		t.Fatalf("пауз %d, хотим на одну больше, чем у документа (%d)",
			len(mediaRetryPauses), len(documentRetryPauses))
	}
	for i := range documentRetryPauses {
		if mediaRetryPauses[i] <= documentRetryPauses[i] {
			t.Errorf("пауза %d: %v не длиннее документальной %v",
				i+1, mediaRetryPauses[i], documentRetryPauses[i])
		}
	}
}

// A video leaves as a plain file: the video upload answers XML and issues its
// token with the address, while the file upload is a plain JSON answer that has
// never failed. The recipient gets a file the chat keeps, not a player.
func TestPostDocument_SendsAVideoAsAFile(t *testing.T) {
	u := newUploadServer(t, `{"token":"video-token"}`)
	sessionDir, workspace := t.TempDir(), t.TempDir()
	data := []byte("\x00\x00\x00\x18ftypmp42 ролик")
	if err := os.WriteFile(filepath.Join(workspace, "клип.mp4"), data, 0o644); err != nil {
		t.Fatalf("файл в рабочем каталоге: %v", err)
	}
	artefact, err := session.CaptureArtifact(sessionDir, workspace, "клип.mp4")
	if err != nil {
		t.Fatalf("capture: %v", err)
	}

	fileSender(t, u).postDocument(sessionDir, session.ToolFile{
		ID: artefact.ID, Name: "клип.mp4", Caption: "готово",
	})

	if u.uploadType != "file" {
		t.Errorf("загрузка пошла как %q, хотим file", u.uploadType)
	}
	if string(u.body) != string(data) {
		t.Errorf("ушло %d байт, хотим %d", len(u.body), len(data))
	}
	if len(u.messages) != 1 {
		t.Fatalf("сообщений %d, хотим одно", len(u.messages))
	}
	list, _ := u.messages[0]["attachments"].([]any)
	if len(list) != 1 {
		t.Fatalf("вложений %v", u.messages[0]["attachments"])
	}
	first, _ := list[0].(map[string]any)
	payload, _ := first["payload"].(map[string]any)
	if first["type"] != "file" || payload["token"] != "video-token" {
		t.Errorf("вложение %v, хотим файл с токеном", first)
	}
}

func TestPostDocument_SendsARecordingAsAFile(t *testing.T) {
	u := newUploadServer(t, `{"token":"audio-token"}`)
	sessionDir, workspace := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "голос.mp3"), []byte("ID3 запись"), 0o644); err != nil {
		t.Fatalf("файл в рабочем каталоге: %v", err)
	}
	artefact, err := session.CaptureArtifact(sessionDir, workspace, "голос.mp3")
	if err != nil {
		t.Fatalf("capture: %v", err)
	}

	fileSender(t, u).postDocument(sessionDir, session.ToolFile{ID: artefact.ID, Name: "голос.mp3"})

	if u.uploadType != "file" {
		t.Errorf("загрузка пошла как %q, хотим file", u.uploadType)
	}
	list, _ := u.messages[0]["attachments"].([]any)
	if len(list) != 1 {
		t.Fatalf("вложений %v", u.messages[0]["attachments"])
	}
	first, _ := list[0].(map[string]any)
	if first["type"] != "file" {
		t.Errorf("вложение %v, хотим файл", first)
	}
}

// TestPostDocument_RefusesAnUnsupportedKind: the refusal names the kind of file
// it is about, so a person knows what did not leave.
func TestPostDocument_RefusesAnUnsupportedKind(t *testing.T) {
	u := newUploadServer(t, `{"token":"t"}`)
	sessionDir, workspace := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "payload.exe"), []byte("MZ"), 0o644); err != nil {
		t.Fatalf("файл: %v", err)
	}
	artefact, err := session.CaptureArtifact(sessionDir, workspace, "payload.exe")
	if err != nil {
		t.Fatalf("capture: %v", err)
	}

	fileSender(t, u).postDocument(sessionDir, session.ToolFile{ID: artefact.ID, Name: "payload.exe"})

	if len(u.messages) != 1 {
		t.Fatalf("сообщений %d, хотим одно с отказом", len(u.messages))
	}
	text, _ := u.messages[0]["text"].(string)
	if !strings.Contains(text, "Не отправляю документ") || !strings.Contains(text, "exe") {
		t.Errorf("отказ = %q, хотим в нём вид и причину", text)
	}
}
