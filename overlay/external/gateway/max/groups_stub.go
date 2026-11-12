//go:build (gateway || gateway.max) && !gateway.max.groups

package max

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
)

// errGroupsNotBuilt is what a build without group support answers when a group
// asks it anything. The bot works in direct chats only, and says why.
var errGroupsNotBuilt = errors.New("эта сборка собрана без поддержки групп: пересоберите с тегом gateway.max.groups")

// groupGate is the group policy of a build without group support: it refuses
// every group, and it refuses to start when the configuration expects groups to
// work. The type mirrors groups.go, so the rest of the adapter has one call in
// one place and never asks which build it is in.
type groupGate struct {
	cfg *config.MaxGatewayConfig
	log *slog.Logger

	mu       sync.Mutex
	reported map[int64]bool
}

func newGroupGate(cfg *config.MaxGatewayConfig, log *slog.Logger) *groupGate {
	if log == nil {
		log = slog.Default()
	}

	return &groupGate{cfg: cfg, log: log, reported: make(map[int64]bool)}
}

// Enabled reports whether this build carries group support at all.
func (g *groupGate) Enabled() bool { return false }

// Note is the line /help shows about groups.
func (g *groupGate) Note() string {
	return "Группы: выключены в этой сборке (сборка без тега gateway.max.groups)."
}

// ConfigProblems names the entries this build cannot honour. Silently ignoring
// them would leave a person believing the bot works in a group it cannot even
// see; stopping the gateway over them would take the personal chats down, so the
// entries are reported once at startup and stay unanswered.
func (g *groupGate) ConfigProblems() []string {
	var out []string
	for i := range g.cfg.Chats {
		c := &g.cfg.Chats[i]
		if c.ChatID >= 0 && strings.TrimSpace(c.Workspace) == "" {
			continue // a direct chat override: nothing group-specific here
		}
		out = append(out, fmt.Sprintf("чат %d настроен для работы в группе, но эта сборка собрана без "+
			"поддержки групп: пересоберите её с тегом gateway.max.groups или уберите этот чат из конфига", c.ChatID))
	}

	return out
}

// Workspace refuses too: a build without group support has no group directory,
// because it has no group.
func (g *groupGate) Workspace(int64) (string, string, error) { return "", "", errGroupsNotBuilt }

// Admit refuses every group, listed or not.
func (g *groupGate) Admit(int64) error { return errGroupsNotBuilt }

// reportOnce says whether this chat's refusal has not been logged yet.
func (g *groupGate) reportOnce(chatID int64) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.reported[chatID] {
		return false
	}
	g.reported[chatID] = true

	return true
}

// handleGroupEvent records a membership change and does nothing else: a build
// without group support has no admission rules to announce, and telling the
// admins about a chat the bot cannot work in would be noise.
func (b *Bot) handleGroupEvent(_ context.Context, u Update, kind string) error {
	b.log.Info("max: group event in a build without group support", "kind", kind,
		"chat", u.ChatID, "raw", rawUpdate(u))

	return nil
}
