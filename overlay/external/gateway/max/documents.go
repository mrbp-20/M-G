//go:build gateway || gateway.max

package max

// Documents in a MAX chat.
//
// A document is three calls, measured on the live API before this file existed:
// POST /uploads?type=file answers an address and no token, the bytes posted to
// that address are answered with the token, and POST /messages carries the token
// as a file attachment. The bytes are an artefact of the chat's own session, so
// a surface sends only what the session captured - never a path a model named.
//
// One message carries one document. The caption is the file's own name first and
// whatever its client wrote after it, cut at the message budget: a name alone is
// what a chat needs to know what arrived.
//
// The message is retried and the upload never is. A repeated upload leaves a
// second copy in the messenger's storage, and a copy nobody sends is nobody's to
// take away - so when the message fails for good, the token is written to the log
// and left where it is.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"mime"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// fileSession is the chat's own session as the document guard needs it: the id
// and the bundle it keeps artefacts in, the working directory its sources are
// confined to, and the restriction this turn runs under.
type fileSession interface {
	GetID() string
	GetPersistedSessionDir() string
	GetCWD() string
	GetTurnRestriction() *session.TurnRestriction
}

const (
	// documentTimeout bounds one document on the wire, the upload and the
	// message together. It is longer than a picture's minute because a document
	// may be tens of megabytes, and it stays inside the turn's own budget.
	documentTimeout = 5 * time.Minute
	// mediaTimeout bounds one video or recording. They are bigger, and the
	// platform still processes them after the upload - the message is refused
	// for a while (attachment.not.ready) - so the wait is longer than a
	// document's. It stays inside the turn's ten minutes.
	mediaTimeout = 8 * time.Minute
	// maxDocumentBytes is the most this gateway sends in one file. The platform
	// takes up to 4 GB, but a turn lasts ten minutes and the upload inside it
	// five: 50 MB at 1 MB/s is 50 s, and anything larger is a promise the turn
	// cannot keep. Raised only after a measured throughput.
	maxDocumentBytes = 50 << 20
	// maxVideoBytes and maxAudioBytes are the platform's own limits: a video up
	// to 250 MB, a recording up to 256 MB and no longer than an hour. A file the
	// platform itself would take is not refused here.
	maxVideoBytes = 250 << 20
	maxAudioBytes = 256 << 20
	// maxDocumentNameRunes keeps a name that a model wrote from becoming a
	// paragraph in the chat.
	maxDocumentNameRunes = 120
)

// documentExtensions are the kinds this gateway sends. MAX itself has no list -
// "common formats (for example TXT, DOC and others)" is all its reference says -
// so this is our own guard, and widening it is one line.
var documentExtensions = map[string]bool{
	"pdf": true, "doc": true, "docx": true, "xls": true, "xlsx": true,
	"csv": true, "txt": true, "zip": true, "md": true,
	// A picture the agent shared is a document here: the photo route carries
	// only what the model read, and the web would show a card the chat never gets.
	"png": true, "jpg": true, "jpeg": true, "gif": true, "webp": true,
}

// videoExtensions and audioExtensions are sent like any other file, but they are
// named apart because they are bigger and the platform prepares them after the
// upload: the kind decides the limit and the patience, not the attachment type.
// The list follows what the gateway receives, so a file it took from a person can
// always be returned - f4v and m4v are the same MP4 container under another name.
var videoExtensions = map[string]bool{
	"mp4": true, "m4v": true, "f4v": true, "mov": true, "mkv": true,
	"webm": true, "avi": true,
}

var audioExtensions = map[string]bool{
	"mp3": true, "wav": true, "m4a": true, "ogg": true, "aac": true,
	"flac": true, "opus": true,
}

// documentRetryPauses are the waits before each further attempt at the message:
// four attempts at most, so a document costs a bounded wait and not the turn.
// The live probe met attachment.not.ready on its first try and landed on the
// second, three seconds later; the platform asks for a pause after an upload
// rather than an immediate send.
var documentRetryPauses = []time.Duration{3 * time.Second, 6 * time.Second, 9 * time.Second}

// mediaRetryPauses are the waits for a video or a recording: the platform
// transcodes it after the upload, which takes longer than storing a document,
// so the pauses are longer and there is one attempt more.
var mediaRetryPauses = []time.Duration{5 * time.Second, 10 * time.Second, 15 * time.Second, 20 * time.Second}

// outgoingKind says how a file is sent: "" when this gateway does not send that
// kind at all, else "video", "audio" or "file" - the three attachment types MAX
// keeps apart.
func outgoingKind(name string) string {
	ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(name), "."))
	switch {
	case videoExtensions[ext]:
		return "video"
	case audioExtensions[ext]:
		return "audio"
	case documentExtensions[ext]:
		return "file"
	}

	return ""
}

// fileLimit is the largest file of this kind that may go out: the platform's
// own limits for video and sound, our measured one for documents.
func fileLimit(kind string) int64 {
	switch kind {
	case "video":
		return maxVideoBytes
	case "audio":
		return maxAudioBytes
	default:
		return maxDocumentBytes
	}
}

// sendFiles posts the documents a finished call of the chat's own session left
// for it, one message each, captioned with the name and what the client wrote.
//
// Only artefacts of this chat's session are sent: a subagent's call belongs to
// another session and is not the chat's to see, and an id the session does not
// hold resolves to nothing. A document the messenger refuses is left out and the
// answer still comes.
func (s *Sender) sendFiles(sessionID string, files []session.ToolFile) {
	if len(files) == 0 {
		return
	}
	if s.files == nil {
		s.b.log.Warn("max: document dropped, the turn has no chat session",
			"session", sessionID, "files", len(files))

		return
	}
	if sessionID != s.files.GetID() {
		// The chat was switched to another conversation while this turn ran
		// (/resume). The file was asked for in this chat and the artefact lives
		// in the turn's own session, so it is delivered here: a file that was
		// promised and never arrived is worse than one that comes after a switch.
		s.b.log.Warn("max: document of a switched-away turn still reaches the chat",
			"turn", sessionID, "chat_session", s.files.GetID(), "files", len(files))
	}
	dir := strings.TrimSpace(s.files.GetPersistedSessionDir())
	if dir == "" {
		s.b.log.Warn("max: document dropped, the session has no directory on disk",
			"session", sessionID, "files", len(files))

		return
	}
	// One document is waited out here, in the turn's own goroutine: a 50 MB file
	// at a slow link holds the answer back for up to documentTimeout. Pictures do
	// the same for a minute, and a document is worth the longer wait.
	for _, file := range files {
		s.postDocument(dir, file)
	}
}

// postDocument resolves one artefact and sends it, logging what it cost. Every
// failure is one document's own: the turn goes on.
func (s *Sender) postDocument(sessionDir string, file session.ToolFile) {
	artifact, path, err := session.ReadArtifact(sessionDir, strings.TrimSpace(file.ID))
	if err != nil {
		s.b.log.Warn("max: document artefact is not in this session", "id", file.ID, "err", err)
		return
	}
	// ReadArtifact already refused anything but a regular file in this session's
	// own store, so the open below is the whole of the check.
	handle, err := os.Open(path)
	if err != nil {
		s.b.log.Warn("max: open document", "id", file.ID, "err", err)
		return
	}
	defer func() { _ = handle.Close() }()

	name := cleanDocumentName(documentName(file, artifact, path), filepath.Base(path))
	kind := outgoingKind(name)
	if reason := fileRefusal(name, kind, artifact.Size); reason != "" {
		s.refuseFile(name, kind, reason)
		return
	}
	// The store records a digest of what it captured: a copy that changed since
	// is no longer the session's document, and does not leave.
	if err = verifySnapshot(handle, artifact.SHA256); err != nil {
		s.refuseFile(name, kind, err.Error())
		return
	}
	if reason := s.confinedRefusal(artifact); reason != "" {
		s.refuseFile(name, kind, reason)
		return
	}
	mimeType := strings.TrimSpace(file.MIMEType)
	if mimeType == "" {
		mimeType = mime.TypeByExtension(filepath.Ext(name))
	}
	// A video and a recording are bigger and are prepared by the platform after
	// the upload, so they get the longer wait and the longer pauses.
	timeout, pauses := documentTimeout, documentRetryPauses
	if kind != "file" {
		timeout, pauses = mediaTimeout, mediaRetryPauses
	}
	ctx, cancel := context.WithTimeout(s.ctx, timeout)
	defer cancel()

	start := time.Now()
	// Everything leaves as a file, video and sound included: the video upload
	// answers the platform's answer in XML rather than JSON and issues its token
	// together with the address, while the file upload is a plain answer whose
	// token has never failed. A file lands as a download the chat keeps, which
	// is what a returned recording is wanted for.
	token, err := s.c.UploadFile(ctx, name, mimeType, artifact.Size, handle)
	if err != nil {
		s.b.log.Warn("max: send file", "kind", kind, "name", name, "size", artifact.Size, "err", err)
		return
	}
	mid, attempts, err := s.sendFileMessage(ctx, Outgoing{
		Target:    s.target,
		Text:      documentCaption(name, file.Caption),
		FileToken: token,
	}, pauses)
	if err != nil {
		// The copy stays in the messenger's storage with nobody to send it and
		// no call to delete it: the log is where an orphan is recorded.
		s.b.log.Warn("max: upload token unused", "kind", kind, "name", name, "size", artifact.Size, "err", err)
		return
	}
	s.b.log.Info("max: file sent", "kind", kind, "name", name, "size", artifact.Size, "mid", mid,
		"attempts", attempts, "took", time.Since(start).Round(time.Millisecond))
}

// sendFileMessage posts a message carrying a document and retries only the
// message: attachment.not.ready says the upload is still being processed and
// answers a later attempt, while a repeated upload would leave a second copy.
func (s *Sender) sendFileMessage(ctx context.Context, out Outgoing, pauses []time.Duration) (string, int, error) {
	var last error
	for attempt := 0; ; attempt++ {
		mid, err := s.c.Send(ctx, out)
		if err == nil {
			return mid, attempt + 1, nil
		}
		last = err
		var api *APIError
		if !errors.As(err, &api) || api.Code != "attachment.not.ready" || attempt >= len(pauses) {
			return "", attempt + 1, last
		}
		pause := pauses[attempt]
		s.b.log.Info("max: attachment not ready", "retry_in", pause.String(), "attempt", attempt+1)
		select {
		case <-ctx.Done():
			return "", attempt + 1, ctx.Err()
		case <-time.After(pause):
		}
	}
}

// documentCaption is the name first and what the client wrote after it, cut at
// the message budget with an ellipsis so the API never refuses the length.
func documentCaption(name, written string) string {
	caption := name
	if text := strings.TrimSpace(written); text != "" {
		caption += "\n\n" + text
	}
	return clipRunes(caption, maxTextRunes)
}

// documentName is what the chat calls the file: the name the client gave, the
// name the store recorded, or the last resort of the snapshot's own name.
func documentName(file session.ToolFile, artifact session.Artifact, path string) string {
	name := strings.TrimSpace(file.Name)
	if name == "" {
		name = strings.TrimSpace(artifact.Name)
	}
	if name == "" {
		name = filepath.Base(path)
	}
	return name
}

// verifySnapshot reads the copy once and puts it back at its start: the digest
// the manifest recorded is what the session captured, and a copy that differs
// is refused rather than sent. The gap between this read and the upload is safe
// because the store writes its copies read-only (0444, a tamper has to chmod
// first) and nothing in this process writes them.
func verifySnapshot(handle *os.File, want string) error {
	want = strings.TrimSpace(want)
	if want == "" {
		return nil
	}
	sum := sha256.New()
	if _, err := io.Copy(sum, handle); err != nil {
		return fmt.Errorf("чтение копии не удалось: %w", err)
	}
	if _, err := handle.Seek(0, io.SeekStart); err != nil {
		return fmt.Errorf("копия не читается с начала: %w", err)
	}
	if got := hex.EncodeToString(sum.Sum(nil)); !strings.EqualFold(got, want) {
		return errors.New("копия изменилась после того, как её сняли")
	}
	return nil
}

// confinedRefusal is why a confined turn may not send this artefact, or "": the
// source it was captured from has to sit inside the session's working directory
// and outside the agent's home, the same rule a read obeys.
func (s *Sender) confinedRefusal(artifact session.Artifact) string {
	if s.files == nil {
		return ""
	}
	restriction := s.files.GetTurnRestriction()
	if restriction == nil || !restriction.ConfineToWorkspace {
		return ""
	}
	home := ""
	if s.b != nil && s.b.runner != nil {
		if cfg := s.b.runner.Cfg(); cfg != nil {
			home = cfg.Paths.Home
		}
	}
	// A chat turn carries no additional roots: the conversation's working
	// directory is the whole workspace it may read, and the agent's home stays
	// out of it.
	if !session.PathInWorkspace(artifact.SourcePath, s.files.GetCWD(), home, nil) {
		return "он лежит вне рабочей папки этой беседы"
	}
	return ""
}

// fileRefusal is why a file is not sent, or "" when it is: the size the platform
// keeps to for that kind, and the kinds this gateway sends at all. Both checks
// run before the upload, because an upload that is then refused leaves a copy in
// the messenger's storage that nothing can delete.
// fileRefusal says why a file cannot go out; an empty answer means it may.
func fileRefusal(name, kind string, size int64) string {
	if limit := fileLimit(kind); size > limit {
		return fmt.Sprintf("он больше %d МБ", limit>>20)
	}
	ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(name), "."))
	if ext == "" {
		return "у него нет расширения"
	}
	if kind == "" {
		return fmt.Sprintf("тип «%s» не в списке отправляемых", ext)
	}

	return ""
}

// kindWord names an attachment kind in Russian, for a sentence to the person.
func kindWord(kind string) string {
	switch kind {
	case "video":
		return "видео"
	case "audio":
		return "запись"
	default:
		return "документ"
	}
}

// refuseFile says in the chat why a file did not arrive, naming what it was. The
// turn goes on: one file's refusal is not the answer's failure.
func (s *Sender) refuseFile(name, kind, reason string) {
	s.b.log.Warn("max: file refused", "kind", kind, "name", name, "reason", reason)
	ctx, cancel := context.WithTimeout(s.ctx, settleTimeout)
	defer cancel()
	text := "Не отправляю " + kindWord(kind) + " «" + name + "»: " + reason + "."
	if _, err := s.c.Send(ctx, Outgoing{Target: s.target, Text: text}); err != nil {
		s.b.log.Warn("max: refuse file", "name", name, "err", err)
	}
}

// cleanDocumentName is a name a chat can show: the base of whatever came in,
// with control characters dropped and the length bounded.
func cleanDocumentName(name, fallback string) string {
	name = filepath.Base(strings.TrimSpace(name))
	name = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, name)
	name = strings.TrimSpace(name)
	if name == "" || name == "." || name == ".." || name == string(filepath.Separator) {
		name = filepath.Base(strings.TrimSpace(fallback))
	}

	return clipRunes(name, maxDocumentNameRunes)
}

// clipRunes keeps at most n runes of s, marking the cut.
func clipRunes(s string, n int) string {
	if n <= 0 || utf8.RuneCountInString(s) <= n {
		return s
	}
	out := make([]rune, 0, n)
	for _, r := range s {
		if len(out) == n-1 {
			break
		}
		out = append(out, r)
	}
	return string(out) + "…"
}
