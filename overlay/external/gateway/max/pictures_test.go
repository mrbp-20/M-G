//go:build gateway || gateway.max

package max

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// stubPictures is the session a sender is shown the pictures of: its id, and
// where its bundle keeps the copies.
type stubPictures struct {
	id  string
	dir string
}

func (s stubPictures) GetID() string                  { return s.id }
func (s stubPictures) GetPersistedSessionDir() string { return s.dir }

// pngBytes is a real PNG: sendPictures decides by the bytes, so text written
// under a .png name would prove nothing.
func pngBytes(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	img.Set(0, 0, color.RGBA{R: 255, A: 255})
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode png: %v", err)
	}
	return buf.Bytes()
}

// pictureSender builds a sender whose chat has a session holding files in its
// assets directory, as a finished call would have left them.
func pictureSender(t *testing.T, f *fakeServer, files map[string][]byte) *Sender {
	t.Helper()
	b := testBot(t)
	dir := t.TempDir()
	assets := session.AssetsPath(dir)
	if err := os.MkdirAll(assets, 0o755); err != nil {
		t.Fatalf("mkdir assets: %v", err)
	}
	for name, data := range files {
		if err := os.WriteFile(filepath.Join(assets, name), data, 0o644); err != nil {
			t.Fatalf("write asset %s: %v", name, err)
		}
	}
	s := b.newSender(context.Background(),
		NewClient("t", WithHTTPClient(f.srv.Client()), WithBaseURL(f.URL())), Target{UserID: 7})
	s.pictures = stubPictures{id: "sess_1", dir: dir}
	return s
}

// imageAttachmentOf reads the single image attachment of a posted message.
func imageAttachmentOf(t *testing.T, msg *sentMessage) string {
	t.Helper()
	if msg == nil {
		t.Fatal("no message was posted")
	}
	for _, raw := range msg.attachments {
		var a struct {
			Type    string `json:"type"`
			Payload struct {
				Token string `json:"token"`
			} `json:"payload"`
		}
		if err := json.Unmarshal(raw, &a); err != nil {
			t.Fatalf("attachment is not JSON: %v", err)
		}
		if a.Type == "image" {
			return a.Payload.Token
		}
	}
	t.Fatalf("the message carried no image attachment: %s", msg.attachments)
	return ""
}

// TestSendPictures_UploadsThenPostsThePicture is the whole path: the copy the
// session kept goes to the upload address, and the token that comes back
// becomes an image attachment of a message captioned with the file's name.
func TestSendPictures_UploadsThenPostsThePicture(t *testing.T) {
	f := newFakeServer("bot")
	defer f.Close()
	png := pngBytes(t)
	s := pictureSender(t, f, map[string][]byte{"shot.png": png})

	s.sendPictures("sess_1", []session.ToolImage{{Name: "shot.png", Asset: "shot.png", MIMEType: "image/png"}})

	f.mu.Lock()
	uploads := f.uploads
	f.mu.Unlock()
	if uploads != 1 {
		t.Fatalf("asked for an upload address %d times, want 1", uploads)
	}
	sent := f.uploadedFiles()
	if len(sent) != 1 {
		t.Fatalf("the upload address received %d files, want 1", len(sent))
	}
	if sent[0].name != "shot.png" || sent[0].mime != "image/png" || sent[0].size != len(png) {
		t.Fatalf("uploaded %+v, want shot.png as image/png of %d bytes", sent[0], len(png))
	}
	msg := f.lastMessageForUser(7)
	// The caption is defused like any other text: a file name is a host name to
	// MAX, and the client would draw it as a link.
	if msg == nil || msg.text != quietLinks("shot.png") {
		t.Fatalf("the picture was not posted captioned with its name: %+v", msg)
	}
	if token := imageAttachmentOf(t, msg); token != "photo-1" {
		t.Fatalf("the message carried token %q, want photo-1", token)
	}
}

// TestSendPictures_LeavesOutWhatIsNotAPicture keeps the promise that a file is
// posted because of its bytes: text under a .png name never reaches the
// messenger, and nothing is posted for it.
func TestSendPictures_LeavesOutWhatIsNotAPicture(t *testing.T) {
	f := newFakeServer("bot")
	defer f.Close()
	s := pictureSender(t, f, map[string][]byte{"notes.png": []byte("this is not a picture")})

	s.sendPictures("sess_1", []session.ToolImage{{Name: "notes.png", Asset: "notes.png"}})

	f.mu.Lock()
	uploads := f.uploads
	f.mu.Unlock()
	if uploads != 0 {
		t.Fatalf("a file that is no picture was uploaded (%d uploads)", uploads)
	}
	if msg := f.lastMessageForUser(7); msg != nil {
		t.Fatalf("a message was posted for a file that is no picture: %+v", msg)
	}
}

// TestSendPictures_ReadsOnlyBareNamesInsideTheSession keeps the asset guard:
// a path is not a copy this session made, so it is never read.
func TestSendPictures_ReadsOnlyBareNamesInsideTheSession(t *testing.T) {
	f := newFakeServer("bot")
	defer f.Close()
	s := pictureSender(t, f, map[string][]byte{"shot.png": pngBytes(t)})

	for _, asset := range []string{"../shot.png", "/etc/passwd", ".", "..", "", "dir/shot.png"} {
		s.sendPictures("sess_1", []session.ToolImage{{Name: asset, Asset: asset}})
	}

	f.mu.Lock()
	uploads := f.uploads
	f.mu.Unlock()
	if uploads != 0 {
		t.Fatalf("%d uploads for names that are not this session's assets", uploads)
	}
	if msg := f.lastMessageForUser(7); msg != nil {
		t.Fatalf("a message was posted for a name outside the assets: %+v", msg)
	}
}

// TestSendPictures_AnotherSessionsPicturesAreNotTheChats: a subagent's calls
// belong to its own session, and the chat does not see them.
func TestSendPictures_AnotherSessionsPicturesAreNotTheChats(t *testing.T) {
	f := newFakeServer("bot")
	defer f.Close()
	s := pictureSender(t, f, map[string][]byte{"shot.png": pngBytes(t)})

	s.sendPictures("sess_other", []session.ToolImage{{Name: "shot.png", Asset: "shot.png"}})

	f.mu.Lock()
	uploads := f.uploads
	f.mu.Unlock()
	if uploads != 0 {
		t.Fatalf("a picture of another session was uploaded (%d uploads)", uploads)
	}
}

// TestSendPictures_AnUploadFailureCostsOnlyThatPicture: the picture is left out
// and the turn carries on - the answer is not the picture's hostage.
func TestSendPictures_AnUploadFailureCostsOnlyThatPicture(t *testing.T) {
	f := newFakeServer("bot")
	defer f.Close()
	f.mu.Lock()
	f.uploadFails = true
	f.mu.Unlock()
	s := pictureSender(t, f, map[string][]byte{
		"shot.png": pngBytes(t),
		"two.png":  pngBytes(t),
	})

	s.sendPictures("sess_1", []session.ToolImage{
		{Name: "shot.png", Asset: "shot.png"},
		{Name: "two.png", Asset: "two.png"},
	})

	if msg := f.lastMessageForUser(7); msg != nil {
		t.Fatalf("a message was posted although every upload failed: %+v", msg)
	}
}

// TestSendPictures_WithoutASessionSendsNothing: a sender wired to no session
// has nothing to show, and says so by doing nothing.
func TestSendPictures_WithoutASessionSendsNothing(t *testing.T) {
	f := newFakeServer("bot")
	defer f.Close()
	b := testBot(t)
	s := b.newSender(context.Background(),
		NewClient("t", WithHTTPClient(f.srv.Client()), WithBaseURL(f.URL())), Target{UserID: 7})

	s.sendPictures("sess_1", []session.ToolImage{{Name: "shot.png", Asset: "shot.png"}})

	f.mu.Lock()
	uploads := f.uploads
	f.mu.Unlock()
	if uploads != 0 {
		t.Fatalf("a sender with no session uploaded a picture (%d uploads)", uploads)
	}
}

// TestSendSessionUpdate_ShowsThePicturesOfAFinishedCall proves the wiring: the
// pictures a completed call reports in its meta reach the chat through the
// update the agent sends, with no caller of sendPictures in between. A call
// that is still running shows nothing.
func TestSendSessionUpdate_ShowsThePicturesOfAFinishedCall(t *testing.T) {
	f := newFakeServer("bot")
	defer f.Close()
	s := pictureSender(t, f, map[string][]byte{"shot.png": pngBytes(t)})
	meta := session.ToolImagesMeta(nil, []session.ToolImage{{Name: "shot.png", Asset: "shot.png"}})

	if err := s.SendSessionUpdate("sess_1", acp.ToolCallStatusUpdate{
		ToolCallID: "call-1", Status: "in_progress", Meta: meta,
	}); err != nil {
		t.Fatalf("SendSessionUpdate(in_progress): %v", err)
	}
	f.mu.Lock()
	uploads := f.uploads
	f.mu.Unlock()
	if uploads != 0 {
		t.Fatalf("a picture was sent for a call that is still running (%d uploads)", uploads)
	}

	if err := s.SendSessionUpdate("sess_1", acp.ToolCallStatusUpdate{
		ToolCallID: "call-1", Status: "completed", Meta: meta,
	}); err != nil {
		t.Fatalf("SendSessionUpdate(completed): %v", err)
	}
	msg := f.lastMessageForUser(7)
	if msg == nil || msg.text != quietLinks("shot.png") {
		t.Fatalf("the picture of a finished call did not reach the chat: %+v", msg)
	}
	if token := imageAttachmentOf(t, msg); token != "photo-1" {
		t.Fatalf("the message carried token %q, want photo-1", token)
	}
}
