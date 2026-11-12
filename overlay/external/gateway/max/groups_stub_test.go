//go:build (gateway || gateway.max) && !gateway.max.groups

package max

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
)

// stubGroupCfg is a configuration that expects groups, used to prove a build
// without group support refuses to start on it rather than ignoring it.
func stubGroupCfg() *config.MaxGatewayConfig {
	cfg := testCfg()
	cfg.Chats = []config.GatewayChatConfig{{
		ChatID:    -100500,
		Workspace: "/srv/groups/podborka",
		Access:    config.AccessAdmins,
	}}

	return cfg
}

func TestGroupStub_Note(t *testing.T) {
	gate := newGroupGate(testCfg(), nil)
	if gate.Enabled() {
		t.Error("Enabled() = true in a build without group support")
	}
	if !strings.Contains(gate.Note(), "gateway.max.groups") {
		t.Errorf("Note() = %q, want it to name the tag", gate.Note())
	}
}

func TestGroupStub_RefusesEveryGroup(t *testing.T) {
	gate := newGroupGate(stubGroupCfg(), nil)
	for _, chat := range []int64{-100500, -777, 0} {
		if err := gate.Admit(chat); err != errGroupsNotBuilt {
			t.Errorf("Admit(%d) = %v, want %v", chat, err, errGroupsNotBuilt)
		}
	}
}

func TestGroupStub_ReportsGroupsInItsConfiguration(t *testing.T) {
	problems := newGroupGate(stubGroupCfg(), nil).ConfigProblems()
	if len(problems) != 1 {
		t.Fatalf("ConfigProblems = %v, want one problem", problems)
	}
	for _, want := range []string{"gateway.max.groups", "-100500"} {
		if !strings.Contains(problems[0], want) {
			t.Errorf("the problem misses %q: %s", want, problems[0])
		}
	}
}

func TestGroupStub_ADirectChatOverrideHasNoProblems(t *testing.T) {
	cfg := testCfg()
	cfg.Chats = []config.GatewayChatConfig{{ChatID: 42, Isolation: config.IsolationShared}}
	if problems := newGroupGate(cfg, nil).ConfigProblems(); len(problems) != 0 {
		t.Errorf("ConfigProblems = %v, want none", problems)
	}
}

// TestGroupStub_TheGatewayStillAnswers: a build without group support reports a
// group in its configuration and keeps serving personal chats.
func TestGroupStub_TheGatewayStillAnswers(t *testing.T) {
	r := newStubRunner()
	b := New(stubGroupCfg(), r, "", slog.New(slog.DiscardHandler), "", "", nil)
	b.setClient(quietAPI())
	b.burstWindow = time.Millisecond

	if problems := b.groups.ConfigProblems(); len(problems) != 1 {
		t.Fatalf("ConfigProblems = %v, want one", problems)
	}
	if err := b.Handle(context.Background(), inboundMsg("m1", "привет")); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if got := r.waitTurn(t); got != "привет" {
		t.Errorf("the personal chat stopped working: prompt = %q", got)
	}
}

func TestGroupStub_GroupIsNotAnsweredEvenWhenListed(t *testing.T) {
	b := New(stubGroupCfg(), newStubRunner(), "", slog.New(slog.DiscardHandler), "", "", nil)
	b.setClient(quietAPI())

	if b.allowed(-100500, 1, true) {
		t.Error("a group must never be answered by a build without group support")
	}
	if !b.allowed(42, 1, false) {
		t.Error("a direct chat is unaffected")
	}
}

func TestGroupStub_EventIsRecordedAndQuiet(t *testing.T) {
	b := New(testCfg(), newStubRunner(), "", slog.New(slog.DiscardHandler), "", "", nil)
	b.setClient(quietAPI())

	u := Update{UpdateType: "bot_added", ChatID: -100500, User: &User{UserID: 7, Name: "Иван"}}
	u.wire = json.RawMessage(`{"update_type":"bot_added","chat_id":-100500}`)
	if err := b.Handle(context.Background(), u); err != nil {
		t.Fatalf("Handle: %v", err)
	}
}
