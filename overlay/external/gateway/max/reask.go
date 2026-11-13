//go:build gateway || gateway.max

package max

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// A question asked before the chat was attached: the chats a turn is mirrored
// into are asked at the moment the question is raised, so a conversation switched
// to afterwards knows nothing about it. The person then meets "the conversation is
// busy" and has no way to answer a question they cannot see, while the turn stands
// still waiting for them. The card below is that question, asked once more on this
// surface; the answer goes into the very wait the page holds, so whoever answers
// first ends it for both.
const (
	reaskTitle = "❓ Беседа ждёт ответа на вопрос"
	// reaskTimeout bounds a card nobody answers: the turn it belongs to may end by
	// its own timeout long before, and a keyboard must not outlive its question.
	reaskTimeout = 30 * time.Minute
	// reaskPoll is how often the card checks that the question still waits: an
	// answer given in the browser has to close it here, or the chat keeps offering
	// options that lead nowhere.
	reaskPoll = 3 * time.Second
	// reaskEndedNote closes a card whose question stopped waiting.
	reaskEndedNote = "✅ Вопрос больше не ждёт ответа."
	// reaskElsewhereNote closes a card answered on the other surface.
	reaskElsewhereNote = "✅ Ответ дан в другом окне."
)

// reaskQuestion asks this chat a question that is already waiting for an answer
// elsewhere. It reports nothing: the card is a courtesy of the switch, and a chat
// that cannot be asked must not fail a switch that otherwise worked.
func (b *Bot) reaskQuestion(ctx context.Context, in inbound, sessionID string) {
	q, ok := session.WaitingQuestionOf(sessionID)
	if !ok || len(q.Options) == 0 {
		return
	}
	key := fmt.Sprintf("%d|%s|%s", in.chatID, sessionID, q.RequestID)
	if b.reasks != nil && !b.reasks.add(key) {
		// The same question already has its card in this chat: a second /resume
		// must not ask it twice.
		return
	}
	c := b.connectedClient()
	if c == nil {
		return
	}
	target := b.answerTarget(in)
	names := make([]string, len(q.Options))
	for i, label := range q.Options {
		names[i] = optionText(label, "", i)
	}
	token, answers := b.asked.offer(target, names)
	defer b.asked.drop(token)
	rows := optionRows(token, optionLetters(len(names)))
	rows = append(rows, customAnswerRow(token))
	text := listedQuestion(reaskText(q), names)

	sendCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), settleTimeout)
	mid, err := c.Send(sendCtx, Outgoing{Target: target, Text: text, Buttons: rows})
	cancel()
	if err != nil {
		b.log.Warn("max: the waiting question was not re-asked", "err", err,
			"session", sessionID, "request", q.RequestID, "user", in.userID)

		return
	}
	b.log.Info("max: waiting question asked in the chat", "session", sessionID,
		"request", q.RequestID, "user", in.userID, "chat", in.chatID, "options", len(names))

	every := reaskPoll
	if b.reaskEvery > 0 {
		every = b.reaskEvery
	}
	ticker := time.NewTicker(every)
	defer ticker.Stop()
	deadline := time.After(reaskTimeout)
	for {
		select {
		case got := <-answers:
			switch {
			case got.drop:
				b.settleReask(c, mid, text, reaskEndedNote)

				return
			case got.ask:
				// The person took the card and will write the answer: the card
				// stays and says what is expected of them.
				b.settleReask(c, mid, text, customAnswerNote)
			case got.text != "":
				if !session.AnswerWaitingQuestion(sessionID, -1, got.text) {
					b.settleReask(c, mid, text, reaskElsewhereNote)

					return
				}
				b.settleReask(c, mid, text, "✅ "+shortenRunes(got.text, answerNoteRunes))

				return
			default:
				if !session.AnswerWaitingQuestion(sessionID, got.index, "") {
					b.settleReask(c, mid, text, reaskElsewhereNote)

					return
				}
				b.settleReask(c, mid, text, "✅ "+names[got.index])

				return
			}
		case <-ticker.C:
			if now, still := session.WaitingQuestionOf(sessionID); !still || now.RequestID != q.RequestID {
				b.settleReask(c, mid, text, reaskElsewhereNote)

				return
			}
		case <-deadline:
			b.settleReask(c, mid, text, reaskEndedNote)

			return
		case <-ctx.Done():
			b.settleReask(c, mid, text, reaskEndedNote)

			return
		}
	}
}

// reaskText is the question as the card shows it: the header and the words of the
// question, without the options - they are listed under it by listedQuestion.
func reaskText(q session.WaitingQuestion) string {
	text := reaskTitle
	if header := strings.TrimSpace(q.Header); header != "" {
		text += "\n" + header
	}
	if body := strings.TrimSpace(q.Text); body != "" {
		text += "\n\n" + body
	}

	return text
}

// settleReask ends the card: it says how the question ended and loses its
// keyboard, so no option of it can be pressed again.
func (b *Bot) settleReask(c *Client, messageID, text, note string) {
	ctx, cancel := context.WithTimeout(context.Background(), settleTimeout)
	defer cancel()
	if err := c.EditMessage(ctx, messageID, text+"\n\n"+note, [][]Button{}); err != nil {
		b.log.Debug("max: re-asked question not settled", "err", err, "message_id", messageID)
	}
}

// A permission prompt raised before the chat was attached is the same story as a
// question: the chats a turn is mirrored into are asked at the moment the prompt
// is raised, so a conversation switched to afterwards knows nothing about it. The
// turn then stands still on a permission the person cannot see, and only the
// window that asked is able to free it. The card below is that prompt, asked once
// more here; the answer goes into the very wait the page holds, so whoever answers
// first ends it for both.
const (
	reaskPermissionTitle = "🔐 Беседа ждёт разрешения"
	// reaskPermissionBodyRunes bounds the tool call shown under the title: the
	// arguments of a write can be kilobytes, and the card has to stay one message.
	reaskPermissionBodyRunes = 600
	// reaskPermissionEnded closes a card whose prompt stopped waiting.
	reaskPermissionEnded = "✅ Разрешение больше не ждёт ответа."
)

// reaskPermission asks this chat a permission prompt that is already waiting
// elsewhere. It reports nothing: the card is a courtesy of the switch, and a chat
// that cannot be asked must not fail a switch that otherwise worked.
func (b *Bot) reaskPermission(ctx context.Context, in inbound, sessionID string) {
	p, ok := session.WaitingPermissionOf(sessionID)
	if !ok || len(p.Options) == 0 {
		return
	}
	key := fmt.Sprintf("perm|%d|%s|%s", in.chatID, sessionID, p.ToolCallID)
	if b.reasks != nil && !b.reasks.add(key) {
		// The same prompt already has its card in this chat.
		return
	}
	c := b.connectedClient()
	if c == nil {
		return
	}
	target := b.answerTarget(in)
	names := make([]string, len(p.Options))
	for i, label := range p.Options {
		names[i] = optionText(label, "", i)
	}
	token, answers := b.asked.offer(target, names)
	defer b.asked.drop(token)
	text := listedQuestion(reaskPermissionText(p), names)

	sendCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), settleTimeout)
	mid, err := c.Send(sendCtx, Outgoing{Target: target, Text: text, Buttons: optionRows(token, optionLetters(len(names)))})
	cancel()
	if err != nil {
		b.log.Warn("max: the waiting permission was not asked", "err", err,
			"session", sessionID, "tool_call", p.ToolCallID, "user", in.userID)

		return
	}
	b.log.Info("max: waiting permission asked in the chat", "session", sessionID,
		"tool_call", p.ToolCallID, "user", in.userID, "chat", in.chatID, "options", len(names))

	every := reaskPoll
	if b.reaskEvery > 0 {
		every = b.reaskEvery
	}
	ticker := time.NewTicker(every)
	defer ticker.Stop()
	deadline := time.After(reaskTimeout)
	for {
		select {
		case got := <-answers:
			if got.drop || got.ask || got.index < 0 || got.index >= len(names) {
				b.settleReask(c, mid, text, reaskPermissionEnded)

				return
			}
			if !session.AnswerWaitingPermission(sessionID, got.index) {
				b.settleReask(c, mid, text, reaskElsewhereNote)

				return
			}
			b.settleReask(c, mid, text, "✅ "+names[got.index])

			return
		case <-ticker.C:
			if now, still := session.WaitingPermissionOf(sessionID); !still || now.ToolCallID != p.ToolCallID {
				b.settleReask(c, mid, text, reaskElsewhereNote)

				return
			}
		case <-deadline:
			b.settleReask(c, mid, text, reaskPermissionEnded)

			return
		case <-ctx.Done():
			b.settleReask(c, mid, text, reaskPermissionEnded)

			return
		}
	}
}

// reaskPermissionText is the prompt as the card shows it: what the tool call is
// going to do, without the options - they are listed under it by listedQuestion.
func reaskPermissionText(p session.WaitingPermission) string {
	text := reaskPermissionTitle
	if title := strings.TrimSpace(p.Title); title != "" {
		text += "\n" + title
	}
	if body := strings.TrimSpace(p.Text); body != "" {
		text += "\n\n" + shortenRunes(body, reaskPermissionBodyRunes)
	}

	return text
}
