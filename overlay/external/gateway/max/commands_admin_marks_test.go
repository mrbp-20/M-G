//go:build gateway || gateway.max

package max

import (
	"bytes"
	"testing"
)

// TestBotCommands_SayWhichOnesAreAdminOnly is the operator's decision of 13.11:
// the hint list shows every command a person may type, and the row of a command
// only the bot's admins can run says so - a menu that stays silent about it
// promises a command the bot refuses.
func TestBotCommands_SayWhichOnesAreAdminOnly(t *testing.T) {
	adminOnly := map[string]bool{
		"model":  true,
		"agent":  true,
		"plan":   true,
		"ask":    true,
		"resume": true,
		"clear":  true,
	}
	seen := make(map[string]bool, len(adminOnly))
	for _, c := range botCommands() {
		if !adminOnly[c.Name] {
			continue
		}
		seen[c.Name] = true
		if !bytes.Contains([]byte(c.Description), []byte("админ")) {
			t.Errorf("botCommands row %q says %q, want it to say the command is for admins", c.Name, c.Description)
		}
	}
	for name := range adminOnly {
		if !seen[name] {
			t.Errorf("botCommands does not list %q, which is an admin-only command", name)
		}
	}
}

// TestHelpText_SeparatesAdminCommands keeps the help page and the menu together:
// the page says which commands are for everyone and which are for admins, so a
// person who meets a refusal has been told about it in advance.
func TestHelpText_SeparatesAdminCommands(t *testing.T) {
	b := newTestBot(&stubRunner{})
	text := b.helpText()
	for _, want := range []string{"Всем:", "Только админам бота:", "/context", "/goal", "/stop", "/help", "/model", "/agent, /plan, /ask", "/resume", "/clear"} {
		if !bytes.Contains([]byte(text), []byte(want)) {
			t.Errorf("helpText is missing %q:\n%s", want, text)
		}
	}
}
