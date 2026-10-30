//go:build http

package httpserver

import (
	"strings"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
)

// SetChatMirrors installs the process's owner of the chats a session is bound
// to. It is called once, as the server comes up, before any turn runs.
func (s *Server) SetChatMirrors(m ChatMirrors) {
	if s == nil {
		return
	}
	s.chatMirrors = m
}

// mirrorToChats wires the sender of a turn this server started to the chats
// bound to the session. It returns the function that ends those mirrors, which
// the caller defers; a turn whose session no chat owns is left as it was.
func (s *Server) mirrorToChats(sessionID string, bridge *Sender) func() {
	if s == nil || s.chatMirrors == nil || bridge == nil {
		return func() {}
	}
	extra, release := s.chatMirrors.TurnToChats(sessionID)
	if extra == nil {
		return func() {}
	}
	bridge.extra = extra

	return release
}

// sayToChats shows the person's own message in the chats bound to the session:
// an answer without the question it replies to reads as a monologue there.
func (s *Server) sayToChats(sessionID string, blocks []acp.ContentBlock) {
	if s == nil || s.chatMirrors == nil {
		return
	}
	var sb strings.Builder
	for _, b := range blocks {
		text := strings.TrimSpace(b.Text)
		if text == "" {
			continue
		}
		if sb.Len() > 0 {
			sb.WriteString("\n")
		}
		sb.WriteString(text)
	}
	s.chatMirrors.SayToChats(sessionID, sb.String())
}
