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

// botCommands is the menu published at Start: the short list a person meets
// first, the same shape the Telegram side publishes. It holds the commands
// processMessage answers itself (start, help, model, context, clear) and the
// three modes (agent, plan, ask), with goal closing it.
//
// The rest of the settings catalogue (reasoning, think, nothink, toolset, plus
// the aliases effort and no_think) works and is handed to the session, but it
// stays out of the hints: it is what somebody reaches for after the short list,
// not what a first "/" should offer. Adding one is a one-line change here, and
// commands_menu_test.go holds the platform bounds when that happens.
//
// Names carry no slash, since the client adds it.
func botCommands() []BotCommand {
	return []BotCommand{
		{Name: "start", Description: "Приветствие и краткое знакомство"},
		{Name: "help", Description: "Список команд и легенда"},
		{Name: "model", Description: "Сменить модель"},
		{Name: "stop", Description: "Остановить текущую работу"},
		{Name: "agent", Description: "Режим работы: все инструменты"},
		{Name: "plan", Description: "Режим плана: только чтение и план"},
		{Name: "ask", Description: "Режим вопросов: ответы без правок"},
		{Name: "context", Description: "Сколько занято в контексте"},
		{Name: "goal", Description: "Работать над целью до готовности"},
		{Name: "resume", Description: "Продолжить другую беседу"},
		{Name: "clear", Description: "Начать новую беседу"},
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
