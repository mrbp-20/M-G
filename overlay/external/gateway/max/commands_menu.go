//go:build gateway || gateway.max

package max

// The hint list the MAX client shows when a person types "/": the same commands
// the bot dispatches (commands.go, dispatch.go) and the settings commands it
// hands to the session, so the menu never promises something the bot ignores.
// The descriptions are Russian, like the rest of the bot's texts.
//
// The platform bounds the list (schema of PATCH /me/commands): at most 32
// commands, a name of 1-64 characters and a description of 1-128.
// commands_menu_test.go holds those bounds, so a longer text fails the build's
// tests instead of the publish at runtime.

import (
	"context"
)

// Limits MAX puts on the published command list.
const (
	maxMenuCommands = 32
	maxMenuNameLen  = 64
	maxMenuDescLen  = 128
)

// BotCommand is one row of the hint list: the word typed after "/" and the
// one-line description shown beside it.
type BotCommand struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

func botCommands() []BotCommand {
	return []BotCommand{
		{Name: "start", Description: "Приветствие и краткое знакомство"},
		{Name: "help", Description: "Список команд и легенда"},
		{Name: "stop", Description: "Остановить текущую работу"},
		{Name: "context", Description: "Сколько занято в контексте"},
		{Name: "goal", Description: "Работать над целью до готовности"},
	}
}

// publishCommands replaces the messenger's hint list with botCommands. It is
// best effort on purpose: a bot that cannot publish its hints still answers
// people, so the failure is logged and never returned. A stopped bot has no
// client to publish through, and nothing to publish for.
func (b *Bot) publishCommands(ctx context.Context) {
	c := b.connectedClient()
	if c == nil {
		b.log.Debug("max: publish commands", "reason", "no connected client")
		return
	}
	if err := c.SetCommands(ctx, botCommands()); err != nil {
		b.log.Warn("max: publish commands", "err", err)
		return
	}
	b.log.Debug("max: commands published", "count", len(botCommands()))
}
