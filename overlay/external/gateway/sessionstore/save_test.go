//go:build gateway || gateway.telegram || gateway.pachca || gateway.max

package sessionstore_test

import (
	"bytes"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/external/gateway/sessionstore"
)

// TestSaveFailure_IsLoggedAndRemembered covers the report from the audit: a save
// that could not happen used to return silently, so a chat-to-session link was
// lost with no line in the log and no way to notice before a restart.
func TestSaveFailure_IsLoggedAndRemembered(t *testing.T) {
	dir := t.TempDir()
	blocker := filepath.Join(dir, "blocker")
	if err := os.WriteFile(blocker, []byte("not a directory"), 0o644); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	lg := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	st := sessionstore.NewPersisted(filepath.Join(blocker, "sessions.json")).WithLogger(lg)

	st.Bind("max:user:1", "sess-1")

	if err := st.LastSaveError(); err == nil {
		t.Fatal("a save that could not happen was reported as done")
	}
	if !strings.Contains(buf.String(), "session store not saved") {
		t.Fatalf("the failed save left no line in the log, log = %q", buf.String())
	}
}

// TestSaveSuccess_ClearsTheRememberedError is the other half, and it has to make
// the transition it claims: a failed save, then the cause removed, then a save
// that works. Creating a healthy store and binding once would only prove that a
// store which never failed reports nothing.
func TestSaveSuccess_ClearsTheRememberedError(t *testing.T) {
	dir := t.TempDir()
	blocker := filepath.Join(dir, "blocker")
	if err := os.WriteFile(blocker, []byte("not a directory"), 0o644); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(blocker, "sessions.json")
	st := sessionstore.NewPersisted(path)

	st.Bind("max:user:1", "sess-1")
	if err := st.LastSaveError(); err == nil {
		t.Fatal("the first save could not work, yet nothing was remembered")
	}

	// Убираем причину: на месте файла появляется каталог, и путь становится годным.
	if err := os.Remove(blocker); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}

	st.Bind("max:user:2", "sess-2")

	if err := st.LastSaveError(); err != nil {
		t.Fatalf("a save that worked still reports %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("the store wrote nothing: %v", err)
	}
	if !strings.Contains(string(data), "sess-2") {
		t.Fatalf("the file holds %q, want the second binding", data)
	}
}
