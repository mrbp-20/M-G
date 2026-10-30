//go:build gateway || gateway.max

package max

// /stop and the case of commands. The command word is lowercased when it is
// parsed (commands.go), so "/STOP", "/Stop" and "/stop" are one command; these
// tests hold that for every command the bot answers itself and for the settings
// commands it hands to the session.

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/external/gateway/sessionstore"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
)

func TestCommands_AnyCaseIsTheSameCommand(t *testing.T) {
	names := make([]string, 0, len(botCommands())+8)
	for _, c := range botCommands() {
		names = append(names, c.Name)
	}
	names = append(names, "agent", "plan", "ask", "think", "nothink", "reasoning", "toolset", "goal")

	for _, name := range names {
		for _, spelled := range []string{name, strings.ToUpper(name), capitalised(name)} {
			cmd, _ := parseCommand("/" + spelled)
			if cmd != name {
				t.Errorf("parseCommand(%q) = %q, want %q", "/"+spelled, cmd, name)
			}
			if !knownCommand(cmd) {
				t.Errorf("knownCommand(%q) = false, want the bot to answer it", cmd)
			}
		}
	}
}

func TestParseCommand_StopIsKnownInEveryCase(t *testing.T) {
	for _, text := range []string{"/stop", "/STOP", "/Stop", "/sToP"} {
		cmd, args := parseCommand(text)
		if cmd != "stop" || args != "" {
			t.Fatalf("parseCommand(%q) = %q/%q, want stop with no arguments", text, cmd, args)
		}
	}
	if cmd, args := parseCommand("/STOP всё"); cmd != "stop" || args != "всё" {
		t.Fatalf("parseCommand(\"/STOP всё\") = %q/%q, want stop with arguments", cmd, args)
	}
}

func stopKey(t *testing.T, b *Bot) string {
	t.Helper()

	return sessionstore.SessionKey(adapterName, 10, 1, config.IsolationIndividual, false)
}

func TestStop_CancelsTheRunningTurn(t *testing.T) {
	f := newSendServer(t)
	b := testBot(t)
	b.setClient(f.client(t))
	key := stopKey(t, b)

	cancelled := false
	b.registerTurn(key, func() { cancelled = true })

	b.handleStop(context.Background(), inbound{chatID: 10, userID: 1}, key)

	if !cancelled {
		t.Fatal("the turn was not cancelled")
	}
	f.waitSends(t, 1, 5*time.Second)
	if got := f.sends()[0].body.Text; !strings.Contains(got, "Останавливаю") {
		t.Fatalf("reply = %q, want it to say the turn is stopping", got)
	}
	if b.cancelTurn(key) {
		t.Fatal("a stopped turn stayed on record")
	}
}

func TestStop_ReportsWhenNothingRuns(t *testing.T) {
	f := newSendServer(t)
	b := testBot(t)
	b.setClient(f.client(t))

	b.handleStop(context.Background(), inbound{chatID: 10, userID: 1}, stopKey(t, b))

	f.waitSends(t, 1, 5*time.Second)
	if got := f.sends()[0].body.Text; !strings.Contains(got, "ничего не выполняется") {
		t.Fatalf("reply = %q, want it to say there is nothing to stop", got)
	}
}

func TestStop_DropsWhatWaitsInTheQueue(t *testing.T) {
	b := testBot(t)
	key := stopKey(t, b)
	b.mu.Lock()
	b.workers[key] = make(chan workerJob, 4)
	b.mu.Unlock()
	b.inFlight.Add(2)
	b.workers[key] <- workerJob{key: key}
	b.workers[key] <- workerJob{key: key}

	if got := b.dropQueued(key); got != 2 {
		t.Fatalf("dropped = %d, want the two messages waiting behind the turn", got)
	}
	if got := b.dropQueued(key); got != 0 {
		t.Fatalf("dropped = %d on an empty queue, want 0", got)
	}
}

func TestStop_AGroupMemberDoesNotStopTheSharedTurn(t *testing.T) {
	f := newSendServer(t)
	b := testBot(t)
	b.setClient(f.client(t))
	key := stopKey(t, b)

	cancelled := false
	b.registerTurn(key, func() { cancelled = true })

	b.handleStop(context.Background(), inbound{chatID: 10, userID: 1, isGroup: true}, key)

	if cancelled {
		t.Fatal("a non-admin stopped the shared conversation")
	}
	f.waitSends(t, 1, 5*time.Second)
	if got := f.sends()[0].body.Text; got != adminOnlyNote {
		t.Fatalf("reply = %q, want the admin-only note", got)
	}
}

// capitalised spells a command the way somebody might type it: first letter
// upper, the rest as they are.
func capitalised(word string) string {
	if word == "" {
		return word
	}

	return strings.ToUpper(word[:1]) + word[1:]
}

func TestStop_CancelsThroughTheSessionLikeTheWebAndTheCLI(t *testing.T) {
	// The cancel the web UI and the CLI use: the session marks the turn as the
	// person's own stop, so the core ends it quietly instead of reporting a
	// generation that was interrupted before its first token.
	r := newStubRunner()
	b := newTestBot(r)
	key := stopKey(t, b)
	b.store.Bind(key, "sess-1")
	b.registerTurn(key, func() {})

	b.handleStop(context.Background(), inbound{chatID: 10, userID: 1}, key)

	if got := r.cancelledIDs(); len(got) != 1 || got[0] != "sess-1" {
		t.Fatalf("sessions cancelled = %v, want the chat's own session", got)
	}
}

func TestStop_TheInterruptedTurnIsNotAnAgentError(t *testing.T) {
	b := testBot(t)
	key := stopKey(t, b)

	if b.personStopped(key) {
		t.Fatal("a turn nobody stopped reads as stopped")
	}
	b.markStopped(key)
	if !b.personStopped(key) {
		t.Fatal("the stop of the person was forgotten before the turn ended")
	}
	if b.personStopped(key) {
		t.Fatal("one stop silences the next turn of the chat as well")
	}
}
