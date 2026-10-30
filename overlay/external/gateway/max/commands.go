//go:build gateway || gateway.max

package max

// The bot's slash commands: /start, /help, /clear, /model, /context. The
// settings commands (/agent, /plan, /ask, /think, ...) are the session's own:
// they pass through to the turn, and the session manager reads them off the
// start of the text. /model <id> does the same, so the remembered model is the
// gateway's pick for every fresh conversation.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// parseCommand splits "/word rest" into its command word and the rest.
func parseCommand(text string) (string, string) {
	if !strings.HasPrefix(text, "/") {
		return "", ""
	}
	word, rest, _ := strings.Cut(text[1:], " ")
	word = strings.ToLower(strings.TrimSpace(word))
	if word == "" || strings.ContainsAny(word, "/\n\t") {
		return "", ""
	}
	return word, strings.TrimSpace(rest)
}

// knownCommand reports whether cmd is a built-in command or a settings
// command the bot hands to the session. Anything else that starts with a
// slash is somebody else's command and is ignored.
func knownCommand(cmd string) bool {
	switch cmd {
	case "start", "help", "clear", "model", "context", "goal", "resume", "stop":
		return true
	}
	return isSettingsCommand(cmd)
}

// changesSettings reports whether cmd changes the session's settings or
// replaces the conversation.
func changesSettings(cmd string) bool {
	return cmd == "model" || cmd == "clear" || isSettingsCommand(cmd)
}

// adminEverywhere reports the settings that belong to the admins in every chat,
// not only where the conversation is shared. Moving a chat to another model and
// throwing a conversation away are not the same as picking a mode for a turn:
// the first decides what every later turn costs, the second destroys history.
// A group has always been guarded; a personal chat is where the plain user
// lives, and the model menu there was the one door left open (29.10.2026).
func adminEverywhere(cmd string) bool {
	return cmd == "model" || cmd == "clear"
}

func isSettingsCommand(cmd string) bool {
	sc, ok := session.LookupSettingsCommand(cmd)
	return ok && sc.Setting != session.SettingPermissionMode
}

// adminOnlyNote answers a group member who is not an admin and tried to change
// the settings.
const adminOnlyNote = "Менять настройки в этом чате могут только админы бота."

func (b *Bot) helpText() string {
	var sb strings.Builder
	sb.WriteString("**Команды**\n\n")
	sb.WriteString("/model — сменить модель (/model <id> задаёт напрямую)\n")
	sb.WriteString("/agent, /plan, /ask — режим работы беседы\n")
	sb.WriteString("/context — сколько занято в контексте\n")
	sb.WriteString("/goal <цель> — работать над целью до готовности\n")
	sb.WriteString("/clear — начать новую беседу\n")
	sb.WriteString("/resume — продолжить другую беседу (выбрать из списка или назвать)\n")
	sb.WriteString("/help — эта справка\n\n")
	sb.WriteString("**Пиктограммы**\n")
	sb.WriteString("▶️ текущая · ✈️ из Телеграма · 🌐 из веба или консоли · 💬 другой шлюз · без пометки — эта")
	sb.WriteString("\n\n")
	sb.WriteString("Ответьте на сообщение, чтобы спросить о нём.")
	return sb.String()
}

// handleModelCommand opens the model menu when no id is given. A direct
// "/model <id>" is handled by the session turn and changes the model in text.
func (b *Bot) handleModelCommand(ctx context.Context, in inbound, key string) {
	cfg := b.runner.Cfg()
	if len(cfg.Models) == 0 {
		b.reply(ctx, in, "⚠️ Модели не настроены.")
		return
	}
	st, err := b.ensureSession(ctx, key)
	if err != nil {
		b.reply(ctx, in, "Ошибка сессии: "+err.Error())
		return
	}
	current := st.EffectiveModelID(cfg)
	kb := buildModelKeyboard(cfg.Models, current)
	b.log.Debug("max: model menu",
		"session", st.GetID(),
		"current", current,
		"models", len(cfg.Models),
	)
	c := b.connectedClient()
	if c == nil {
		return
	}
	if _, err := c.Send(ctx, Outgoing{
		Target:  b.answerTarget(in),
		Text:    modelMenuText(current),
		Format:  "markdown",
		Buttons: kb,
	}); err != nil {
		b.log.Warn("max: send model menu", "err", err)
	}
}

// modelMenuText is the text above the model keyboard.
func modelMenuText(current string) string {
	return fmt.Sprintf("**Модель**\n\nСейчас: `%s`\n\nВыберите модель:", current)
}

// buildModelKeyboard returns one button per configured model, with a tick on
// the current one. Every payload carries the model action prefix so the tap
// is recognised in HandleCallback.
func buildModelKeyboard(models []config.ModelEntry, current string) [][]Button {
	rows := make([][]Button, 0, len(models))
	for _, m := range models {
		if m.Model == "" {
			continue
		}
		label := m.Model
		if m.Model == current {
			label = "✓ " + label
		}
		rows = append(rows, []Button{{
			Text:    label,
			Payload: callbackActionModel + ":" + modelCallbackValue(m.Model),
		}})
	}
	return rows
}

// ── Callback payloads ────────────────────────────────────────────────────────

const (
	// callbackActionModel is the verb carried by every model button.
	callbackActionModel = "model"

	// maxCallbackPayload is the MAX Bot API limit on a callback button
	// payload (schema.yaml, CallbackButton.payload maxLength).
	maxCallbackPayload = 1024
	// callbackDigestMarker prefixes the short form used for a value that does
	// not fit that limit. A model id cannot carry it (config validation admits
	// letters, digits, slash, dot, underscore and hyphen), so the two forms
	// never collide.
	callbackDigestMarker = "#"
)

// callbackValue returns the payload a button carries for value, next to a
// prefix of prefixLen bytes. A value that fits travels as itself; a longer one
// travels as a digest, because truncating it would send back a name nothing is
// stored under - the tap would be rejected and the keyboard would look broken.
func callbackValue(prefixLen int, value string) string {
	if prefixLen+len(value) <= maxCallbackPayload {
		return value
	}
	sum := sha256.Sum256([]byte(value))
	return callbackDigestMarker + hex.EncodeToString(sum[:8])
}

// modelCallbackValue returns the payload carried by one model button.
func modelCallbackValue(model string) string {
	return callbackValue(len(callbackActionModel)+1, model)
}

// resolveModelCallback maps a payload back to a configured model id. A model
// dropped from the configuration since the keyboard was sent resolves to
// nothing, which is better than applying a name that is gone.
func resolveModelCallback(models []config.ModelEntry, payload string) (string, bool) {
	for i := range models {
		if models[i].Model == payload {
			return payload, true
		}
	}
	if !strings.HasPrefix(payload, callbackDigestMarker) {
		return "", false
	}
	for i := range models {
		if modelCallbackValue(models[i].Model) == payload {
			return models[i].Model, true
		}
	}
	return "", false
}

// handleContextCommand reports the session's context breakdown.
func (b *Bot) handleContextCommand(ctx context.Context, in inbound, key string) {
	st, err := b.ensureSession(ctx, key)
	if err != nil {
		b.reply(ctx, in, "Ошибка сессии: "+err.Error())
		return
	}
	bd := st.GetLastContextBreakdown()
	if bd == nil {
		b.reply(ctx, in, "Контекст\n\nПока пусто: сначала пришлите сообщение.")
		return
	}
	b.reply(ctx, in, formatContextBreakdown(bd, b.contextHeader(st)))
}

// contextHeader names what a breakdown belongs to. The sizes alone say nothing
// about which model they are measured against or which conversation they
// describe, so /context carries the conversation's human name, the model id and
// the window that model runs with (0 when no window is known).
type contextHeader struct {
	sessionID string
	title     string
	model     string
	window    int
}

// contextHeader builds the header for st. The model and the window come from
// the same resolution every other reader uses, so /context cannot disagree with
// the composer or with usage_update.
func (b *Bot) contextHeader(st *session.State) contextHeader {
	head := contextHeader{sessionID: st.GetID(), title: st.ConversationTitle()}
	if cfg := b.runner.Cfg(); cfg != nil {
		head.model = st.EffectiveModelID(cfg)
		head.window, _ = st.ContextWindow(cfg)
	}
	return head
}

// handleModelCallback applies a model chosen from the inline keyboard and
// edits the menu message so the tick moves to the new model. It returns the
// notification to show the presser and whether the menu was edited.
func (b *Bot) handleModelCallback(ctx context.Context, c *Client, u Update, cb Callback, st *session.State) (string, bool, error) {
	cfg := b.runner.Cfg()
	newModel, ok := resolveModelCallback(cfg.Models, cb.Payload[len(callbackActionModel)+1:])
	if !ok {
		b.log.Warn("max: callback model unknown", "payload", cb.Payload, "session", st.GetID())
		return "❌ That model is no longer configured.", false, nil
	}

	_, err := b.runner.HandleSessionSetConfigOption(ctx, acp.SessionSetConfigOptionParams{
		SessionID: st.GetID(),
		ConfigID:  "model",
		Value:     newModel,
	})
	if err != nil {
		b.log.Warn("max: set model", "err", err, "session", st.GetID(), "model", newModel)
		return "❌ " + err.Error(), false, nil
	}
	b.log.Info("max: model applied", "session", st.GetID(), "model", newModel)

	// The gateway is a surface of its own: the model an admin picked here is
	// what the next fresh chat session starts on. Anybody else's pick stays
	// in their own session.
	if b.cfg.IsAdmin(cb.User.UserID) {
		b.store.SetLastModel(newModel)
	}

	// Editing with the same keyboard is a waste and may be refused as
	// "message is not modified", so skip it when nothing visible changed.
	if sameModelKeyboard(u, cfg.Models, newModel) {
		b.log.Debug("max: model menu already current", "session", st.GetID(), "model", newModel)
		return "✓ " + newModel, false, nil
	}

	// The id of the message the button belongs to is what MAX puts in
	// message.body.mid of a callback update, not in the update's own
	// message_id: MessageKey reads whichever the update carries. An edit with
	// an empty id is refused by the API ("Missing required parameter"), so a
	// press that carries no message leaves the menu alone.
	mid := u.MessageKey()
	if mid == "" {
		b.log.Debug("max: model menu not editable", "reason", "no message id", "session", st.GetID())
		return "✓ " + newModel, false, nil
	}

	if err := c.EditMessage(ctx, mid, modelMenuText(newModel), buildModelKeyboard(cfg.Models, newModel)); err != nil {
		b.log.Debug("max: edit model message", "err", err)
		return "✓ " + newModel, false, err
	}
	return "✓ " + newModel, true, nil
}

// sameModelKeyboard reports whether the message the callback came from already
// shows the keyboard for newModel. It reads the inline keyboard from the
// message body carried by the callback update, if any.
func sameModelKeyboard(u Update, models []config.ModelEntry, newModel string) bool {
	m, err := u.Body()
	if err != nil {
		return false
	}
	want := buildModelKeyboard(models, newModel)
	return sameButtonKeyboard(m.Body.Attachments, want)
}

// sameButtonKeyboard reports whether the attachments already show the wanted
// keyboard, row by row and button by button.
func sameButtonKeyboard(attachments []json.RawMessage, want [][]Button) bool {
	var got [][]Button
	for _, raw := range attachments {
		var a struct {
			Type    string `json:"type"`
			Payload struct {
				Buttons [][]Button `json:"buttons"`
			} `json:"payload"`
		}
		if err := json.Unmarshal(raw, &a); err != nil {
			continue
		}
		if a.Type == "inline_keyboard" {
			got = a.Payload.Buttons
			break
		}
	}
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if len(got[i]) != len(want[i]) {
			return false
		}
		for j := range got[i] {
			if got[i][j].Text != want[i][j].Text || got[i][j].Payload != want[i][j].Payload {
				return false
			}
		}
	}
	return true
}

// formatContextBreakdown renders a session's context breakdown for /context:
// what the numbers belong to (conversation, model, its window), the sizes
// themselves, and how much of the window the estimate takes.
//
// It is plain text on purpose: MAX does not render markdown, so a "**bold**"
// or a fenced block would reach the chat as literal asterisks and backticks.
// The sizes are one per line without column padding for the same reason - a
// proportional font in the chat would not line them up anyway.
func formatContextBreakdown(bd *session.ContextBreakdown, head contextHeader) string {
	rows := []struct {
		label string
		val   int
	}{
		{"Беседа", bd.Conversation},
		{"Системный промпт", bd.SystemPrompt},
		{"Инструменты", bd.ToolDefinitions},
		{"Правила", bd.Rules},
		{"Навыки", bd.Skills},
		{"MCP", bd.MCP},
		{"Субагенты", bd.Subagents},
	}
	var sb strings.Builder
	sb.WriteString("Контекст\n\n")
	if title := strings.TrimSpace(head.title); title != "" {
		sb.WriteString("Беседа: «" + shortenRunes(title, contextTitleRunes) + "»\n")
	}
	sb.WriteString("Номер: " + head.sessionID + "\n")
	if head.model != "" {
		sb.WriteString("Модель: " + head.model)
		if head.window > 0 {
			sb.WriteString(" — окно " + groupDigits(head.window) + " токенов")
		}
		sb.WriteString("\n")
	}
	sb.WriteString("\n")
	for _, r := range rows {
		if r.val == 0 {
			continue
		}
		sb.WriteString(r.label + ": " + groupDigits(r.val) + "\n")
	}
	sb.WriteString("\nВсего около " + groupDigits(bd.EstimatedTotal) + " токенов")
	if head.window > 0 {
		fmt.Fprintf(&sb, " — %d%% окна", bd.EstimatedTotal*100/head.window)
	}
	sb.WriteString(" (оценка: символы / 4)")
	return sb.String()
}

// contextTitleRunes is how much of the conversation's title /context shows: a
// derived title is the first message, which can run long.
const contextTitleRunes = 60

// groupDigits writes an integer with a space every three digits, so a count of
// 183289 reads as 183 289 in a chat.
func groupDigits(n int) string {
	neg := n < 0
	if neg {
		n = -n
	}
	digits := strconv.Itoa(n)
	var out strings.Builder
	if neg {
		out.WriteString("-")
	}
	for i, r := range digits {
		if i > 0 && (len(digits)-i)%3 == 0 {
			out.WriteString(" ")
		}
		out.WriteRune(r)
	}
	return out.String()
}
