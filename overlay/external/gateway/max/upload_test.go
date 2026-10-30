//go:build gateway || gateway.max

package max

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// uploadServer stands in for the two calls one upload makes: the API answers an
// address, and the upload host receives the bytes and answers a token. It keeps
// what the second call carried, so a test can read the part the API would.
type uploadServer struct {
	srv     *httptest.Server
	answer  string
	started chan struct{}

	length   int64
	field    string
	filename string
	mimeType string
	body     []byte
	// uploadType is the type the address call asked for: image, file, video or
	// audio.
	uploadType string

	messages  []map[string]any
	lastQuery string

	// onMessage scripts the message call when a test needs it to answer
	// something other than 200 with a mid: attachment.not.ready, a refusal.
	onMessage func(w http.ResponseWriter, r *http.Request) bool
	// uploadsAnswer, when set, is what the address call answers instead of a
	// usable address.
	uploadsAnswer string
}

func newUploadServer(t *testing.T, answer string) *uploadServer {
	t.Helper()
	u := &uploadServer{answer: answer, started: make(chan struct{})}
	var once sync.Once
	u.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/messages" {
			u.lastQuery = r.URL.RawQuery
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			u.messages = append(u.messages, body)
			if u.onMessage != nil && u.onMessage(w, r) {
				return
			}
			_, _ = io.WriteString(w, `{"message":{"body":{"mid":"mid.1"}}}`)
			return
		}
		if r.URL.Path == "/uploads" {
			// The first answer names an address and nothing else: a token here
			// would mean the flow differs from the one the probe measured.
			if r.URL.Query().Get("type") == "" {
				t.Errorf("the address call must name the media type, got %q", r.URL.RawQuery)
			}
			u.uploadType = r.URL.Query().Get("type")
			if u.uploadsAnswer != "" {
				_, _ = io.WriteString(w, u.uploadsAnswer)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]string{"url": u.srv.URL + "/store"})
			return
		}
		once.Do(func() { close(u.started) })
		u.length = r.ContentLength
		mediaType, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if err != nil || mediaType != "multipart/form-data" {
			t.Errorf("the upload must be multipart, got %q", r.Header.Get("Content-Type"))
			return
		}
		part, err := multipart.NewReader(r.Body, params["boundary"]).NextPart()
		if err != nil {
			t.Errorf("read the part: %v", err)
			return
		}
		u.field = part.FormName()
		u.filename = part.FileName()
		u.mimeType = part.Header.Get("Content-Type")
		u.body, _ = io.ReadAll(part)
		_, _ = io.WriteString(w, u.answer)
	}))
	t.Cleanup(u.srv.Close)
	return u
}

func clientOf(u *uploadServer) *Client {
	return NewClient("t", WithHTTPClient(u.srv.Client()), WithBaseURL(u.srv.URL))
}

// TestUploadFile_TokenFromUploadAnswer pins the flow the live probe measured:
// the address call carries no token, and the token arrives with the bytes.
func TestUploadFile_TokenFromUploadAnswer(t *testing.T) {
	u := newUploadServer(t, `{"token":"file-token"}`)
	data := []byte("%PDF-1.4 probe")
	token, err := clientOf(u).UploadFile(context.Background(), "Счет №1.pdf", "application/pdf", int64(len(data)), bytes.NewReader(data))
	if err != nil {
		t.Fatalf("upload: %v", err)
	}
	if token != "file-token" {
		t.Errorf("token = %q, want the one from the upload answer", token)
	}
	if u.field != "data" {
		t.Errorf("form field = %q, want \"data\"", u.field)
	}
	if u.filename != "Счет №1.pdf" {
		t.Errorf("filename = %q, want it passed through unchanged", u.filename)
	}
	if u.mimeType != "application/pdf" {
		t.Errorf("part media type = %q, want application/pdf", u.mimeType)
	}
	if !bytes.Equal(u.body, data) {
		t.Errorf("body = %q, want the bytes the caller gave", u.body)
	}
	if u.length <= int64(len(data)) {
		t.Errorf("the request declared %d bytes, which cannot include the multipart envelope", u.length)
	}
}

// TestUploadImage_PhotoTokenStillWorks guards the shape pictures have always
// used: a token under a photo id, and the generic media type.
func TestUploadImage_PhotoTokenStillWorks(t *testing.T) {
	u := newUploadServer(t, `{"photos":{"42":{"token":"photo-token"}}}`)
	token, err := clientOf(u).UploadImage(context.Background(), "pic.png", pngBytes(t))
	if err != nil {
		t.Fatalf("upload: %v", err)
	}
	if token != "photo-token" {
		t.Errorf("token = %q, want the photo token", token)
	}
	if u.field != "data" {
		t.Errorf("form field = %q, want \"data\"", u.field)
	}
	if u.mimeType != "application/octet-stream" {
		t.Errorf("part media type = %q, want the generic one pictures carried until now", u.mimeType)
	}
}

// gatedReader produces a byte only once the server has begun reading, which it
// can only do after the request went out. An implementation that builds the
// body in memory reads it before sending and fails here instead of passing.
type gatedReader struct {
	gate  chan struct{}
	data  []byte
	off   int
	wait  time.Duration
	armed bool
}

func (g *gatedReader) Read(p []byte) (int, error) {
	if !g.armed {
		g.armed = true
		select {
		case <-g.gate:
		case <-time.After(g.wait):
			return 0, errors.New("the body was read before the request was sent: the upload buffers the file")
		}
	}
	if g.off >= len(g.data) {
		return 0, io.EOF
	}
	n := copy(p, g.data[g.off:])
	g.off += n
	return n, nil
}

func TestUploadFile_StreamsTheBody(t *testing.T) {
	u := newUploadServer(t, `{"token":"streamed"}`)
	data := bytes.Repeat([]byte("a"), 4<<20)
	r := &gatedReader{gate: u.started, data: data, wait: 3 * time.Second}
	token, err := clientOf(u).UploadFile(context.Background(), "big.pdf", "application/pdf", int64(len(data)), r)
	if err != nil {
		t.Fatalf("upload: %v", err)
	}
	if token != "streamed" {
		t.Errorf("token = %q", token)
	}
	if len(u.body) != len(data) {
		t.Errorf("the server received %d bytes, want %d", len(u.body), len(data))
	}
}

// TestUploadFile_ShortReaderIsAnError keeps the declared length honest: a file
// that shrank between its size being taken and the upload must fail loudly.
func TestUploadFile_ShortReaderIsAnError(t *testing.T) {
	u := newUploadServer(t, `{"token":"never"}`)
	_, err := clientOf(u).UploadFile(context.Background(), "short.pdf", "application/pdf", 1024, bytes.NewReader([]byte("only a few")))
	if err == nil {
		t.Fatal("a body shorter than its declared length must fail")
	}
}

// TestSend_FileAttachment pins the third call: the document travels as a file
// attachment carrying the upload token, and the caption is the text it was
// given - the name plus whatever the client wrote.
func TestSend_FileAttachment(t *testing.T) {
	u := newUploadServer(t, `{}`)
	mid, err := clientOf(u).Send(context.Background(), Outgoing{
		Target:    Target{UserID: 7},
		Text:      "Счет.pdf\n\nготово, проверьте",
		FileToken: "file-token",
	})
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	if mid != "mid.1" {
		t.Errorf("mid = %q", mid)
	}
	if len(u.messages) != 1 {
		t.Fatalf("messages = %d, want 1", len(u.messages))
	}
	// The caption goes through the same guard as any text: a word shaped like
	// a host name is broken, so the chat is not filled with preview cards.
	want := quietLinks("Счет.pdf\n\nготово, проверьте")
	if got := u.messages[0]["text"]; got != want {
		t.Errorf("text = %v, want %v", got, want)
	}
	list, _ := u.messages[0]["attachments"].([]any)
	if len(list) != 1 {
		t.Fatalf("attachments = %v, want one", u.messages[0]["attachments"])
	}
	first, _ := list[0].(map[string]any)
	if first["type"] != "file" {
		t.Errorf("attachment type = %v, want file", first["type"])
	}
	payload, _ := first["payload"].(map[string]any)
	if payload["token"] != "file-token" {
		t.Errorf("payload token = %v", payload["token"])
	}
	if !strings.Contains(u.lastQuery, "disable_link_preview=true") {
		t.Errorf("query = %q, want the preview switch", u.lastQuery)
	}
	if !strings.Contains(u.lastQuery, "user_id=7") {
		t.Errorf("query = %q, want the target", u.lastQuery)
	}
}

// TestUploadFile_AddressWithoutURL: the first answer has to name an address, and
// a client that got none says so instead of posting to nowhere.
func TestUploadFile_AddressWithoutURL(t *testing.T) {
	u := newUploadServer(t, `{"token":"file-token"}`)
	u.uploadsAnswer = `{}`
	_, err := clientOf(u).UploadFile(context.Background(), "a.pdf", "application/pdf", 3, bytes.NewReader([]byte("abc")))
	if err == nil {
		t.Fatal("an address answer without a url must fail")
	}
	if !strings.Contains(err.Error(), "no address") {
		t.Errorf("error = %q, want it to name the missing address", err)
	}
}

// TestUploadFile_UploadWithoutToken: the token comes with the bytes, and its
// absence is an error rather than an empty attachment.
func TestUploadFile_UploadWithoutToken(t *testing.T) {
	u := newUploadServer(t, `{}`)
	_, err := clientOf(u).UploadFile(context.Background(), "a.pdf", "application/pdf", 3, bytes.NewReader([]byte("abc")))
	if err == nil {
		t.Fatal("an upload answer without a token must fail")
	}
	if !strings.Contains(err.Error(), "no token") {
		t.Errorf("error = %q, want it to name the missing token", err)
	}
}

// TestUploadFile_LongerReaderIsCutAtTheDeclaredSize pins the documented edge: the
// request declares its length from size, so a reader that yields more loses the
// excess instead of corrupting the request. A caller whose content may grow takes
// the size from the same read that verified the content - the gateway's snapshot
// is verified by its digest right before, and the store keeps copies read-only.
func TestUploadFile_LongerReaderIsCutAtTheDeclaredSize(t *testing.T) {
	u := newUploadServer(t, `{"token":"file-token"}`)
	_, err := clientOf(u).UploadFile(context.Background(), "a.pdf", "application/pdf", 16, bytes.NewReader(bytes.Repeat([]byte("a"), 64)))
	if err != nil {
		t.Fatalf("upload: %v", err)
	}
	if len(u.body) != 16 {
		t.Errorf("the upload carried %d bytes, want the declared 16", len(u.body))
	}
}

// TestUploadFile_EmptyDocument: a zero-byte document is a document.
func TestUploadFile_EmptyDocument(t *testing.T) {
	u := newUploadServer(t, `{"token":"file-token"}`)
	token, err := clientOf(u).UploadFile(context.Background(), "empty.csv", "text/csv", 0, bytes.NewReader(nil))
	if err != nil {
		t.Fatalf("upload: %v", err)
	}
	if token != "file-token" {
		t.Errorf("token = %q", token)
	}
	if len(u.body) != 0 {
		t.Errorf("the server received %d bytes, want none", len(u.body))
	}
}
