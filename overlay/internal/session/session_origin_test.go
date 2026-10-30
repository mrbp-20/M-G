package session

import (
	"context"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
)

// SessionOrigin answers where a session was started from the bundle metadata
// alone: a listing that marks a dozen sessions does not load a dozen of them.
func TestSessionOriginReadsTheBundleWithoutLoadingIt(t *testing.T) {
	root := t.TempDir()
	fs := &FileStore{Root: root}
	mgr := newLoadTestManager(t, root, fs)
	writeOriginBundle(t, fs, "sess_tg", GatewayOrigin("telegram"), "", root)
	writeOriginBundle(t, fs, "sess_print", PrintOrigin, "", root)
	writeOriginBundle(t, fs, "sess_local", "", "", root)

	for _, tc := range []struct{ id, want string }{
		{"sess_tg", "gateway:telegram"},
		{"sess_print", PrintOrigin},
		{"sess_local", ""},
		// A bundle that is not there - or not readable - is unmarked rather
		// than an error: the listing it feeds must still come out.
		{"sess_missing", ""},
		{"", ""},
	} {
		if got := mgr.SessionOrigin(tc.id); got != tc.want {
			t.Errorf("SessionOrigin(%q) = %q, want %q", tc.id, got, tc.want)
		}
		if _, live := mgr.sessions[tc.id]; live {
			t.Errorf("SessionOrigin(%q) loaded the session, want the metadata read only", tc.id)
		}
	}

	var nilManager *Manager
	if got := nilManager.SessionOrigin("sess_tg"); got != "" {
		t.Errorf("SessionOrigin on a nil manager = %q, want empty", got)
	}
}

// TestSessionOriginAnswersForALiveSession: a session this process holds answers
// from memory, which carries what the bundle does.
func TestSessionOriginAnswersForALiveSession(t *testing.T) {
	root := t.TempDir()
	fs := &FileStore{Root: root}
	mgr := newLoadTestManager(t, root, fs)

	res, err := mgr.HandleSessionNew(context.Background(), acp.SessionNewParams{CWD: root})
	if err != nil {
		t.Fatalf("session/new: %v", err)
	}
	mgr.SessionByID(res.SessionID).SetOriginWithoutPersist(GatewayOrigin("max"))

	if got, want := mgr.SessionOrigin(res.SessionID), "gateway:max"; got != want {
		t.Fatalf("SessionOrigin = %q, want %q", got, want)
	}
}
