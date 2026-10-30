//go:build gateway || gateway.max

package max

// The pictures a turn looked at.
//
// The model reads an image file during a call, and the copy the session kept of
// that picture is what the chat is shown: not the file in the workspace, which
// may have changed since, and not the bytes the model got, which are already
// gone. The pictures of a call arrive in the update's meta
// (session.ToolImagesFromMeta), so a chat follows the turn as it reads - the
// same arrangement the Telegram gateway makes, and the reason this file looks
// the way it does.
//
// Two things differ on this surface. MAX has no document fallback: a picture it
// refuses is skipped rather than posted another way, and the answer still
// comes. And a message here is sent, never edited into existence, so a picture
// is a message of its own with the file's name as its text.

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// pictureTimeout bounds one picture on the wire: the upload and the message
// that carries it. A turn waits for neither, so a picture that is slow is
// dropped instead of holding the answer.
const pictureTimeout = 60 * time.Second

// pictureSession is the session a chat turn runs in, as far as sending its
// pictures goes: which session it is, and where its bundle keeps the copies.
type pictureSession interface {
	GetID() string
	GetPersistedSessionDir() string
}

// sendPictures posts the pictures a finished call of the chat's own session
// showed the model (read on an image file), one message each, captioned with
// the file's name, so the chat sees what the agent looked at while the turn
// runs.
//
// The asset is a bare name inside the session's assets directory; any other
// spelling is not a copy this session made, and is not read. The bytes decide,
// not the name: a file that is no picture (replaced since the call, damaged) is
// not uploaded. A picture the messenger refuses is left out, and the answer
// still comes. A subagent's calls belong to another session and are not the
// chat's to see.
func (s *Sender) sendPictures(sessionID string, images []session.ToolImage) {
	if len(images) == 0 {
		return
	}
	if s.pictures == nil {
		s.b.log.Warn("max: pictures dropped, the turn has no chat session",
			"session", sessionID, "pictures", len(images))

		return
	}
	if sessionID != s.pictures.GetID() {
		// A picture is part of the answer, not a file somebody asked for, so a
		// turn that has been switched away from does not push it into the chat
		// that moved on.
		s.b.log.Warn("max: pictures of another session are not shown here",
			"turn", sessionID, "chat_session", s.pictures.GetID(), "pictures", len(images))

		return
	}
	dir := strings.TrimSpace(s.pictures.GetPersistedSessionDir())
	if dir == "" {
		s.b.log.Warn("max: pictures dropped, the session has no directory on disk",
			"session", sessionID, "pictures", len(images))

		return
	}
	assets := session.AssetsPath(dir)
	for _, img := range images {
		if img.Asset == "" || filepath.Base(img.Asset) != img.Asset || img.Asset == "." || img.Asset == ".." {
			continue
		}
		path := filepath.Join(assets, img.Asset)
		info, err := os.Lstat(path)
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		data, err := os.ReadFile(path)
		if err != nil {
			s.b.log.Warn("max: read picture", "asset", img.Asset, "err", err)
			continue
		}
		if !strings.HasPrefix(http.DetectContentType(data), "image/") {
			s.b.log.Warn("max: the saved picture is not an image", "asset", img.Asset)
			continue
		}
		name := strings.TrimSpace(img.Name)
		if name == "" {
			name = img.Asset
		}
		if err = s.postPicture(name, data); err != nil {
			s.b.log.Warn("max: send picture", "name", name, "err", err)
		}
	}
}

// postPicture stores one picture and sends it as a message of its own,
// captioned with its name. The timeout is per picture, so a slow upload costs
// one picture and not the turn.
func (s *Sender) postPicture(name string, data []byte) error {
	ctx, cancel := context.WithTimeout(s.ctx, pictureTimeout)
	defer cancel()
	token, err := s.c.UploadImage(ctx, name, data)
	if err != nil {
		return err
	}
	_, err = s.c.Send(ctx, Outgoing{Target: s.target, Text: name, ImageToken: token})
	return err
}
