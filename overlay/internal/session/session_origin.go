package session

import (
	"strings"
)

// SessionOrigin reports where a session was started: "gateway:max",
// "gateway:telegram", "print", or "" for a local one. It reads the bundle
// metadata only, so a menu of a dozen sessions does not load a dozen once.
//
// A session this process already holds answers from memory, which is the same
// value the bundle carries - the origin is written once, by the surface that
// created the session. An unreadable or missing bundle answers "": a surface
// that cannot tell where a session came from shows it unmarked rather than
// refusing the whole listing.
func (m *Manager) SessionOrigin(sessionID string) string {
	if m == nil {
		return ""
	}
	id := strings.TrimSpace(sessionID)
	if id == "" {
		return ""
	}
	m.mu.RLock()
	st := m.sessions[id]
	m.mu.RUnlock()
	if st != nil {
		return strings.TrimSpace(st.GetOrigin())
	}
	store := m.store
	if store == nil || store.Root == "" {
		return ""
	}
	meta, err := store.ReadMeta(id)
	if err != nil {
		if m.log != nil {
			m.log.Debug("session origin: metadata not read", "session", id, "err", err)
		}
		return ""
	}
	return strings.TrimSpace(meta.Origin)
}
