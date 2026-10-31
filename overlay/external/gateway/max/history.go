//go:build gateway || gateway.max

package max

// A switch of the conversation is invisible in a messenger. The chat keeps every
// message it had, and the session behind it becomes another one, so after
// /resume a person has no way to tell where they landed without asking. The card
// below answers that in one message: the title of the conversation now in use,
// how much is in it, the last lines of it, and a button that pages through the
// whole transcript.
//
// The transcript comes from the session, not from the chat: MAX stores what it
// rendered, without the markdown markers, so a read-back would lose the shape of
// every answer. The bot is handed a reader at startup (SetTranscript), because a
// gateway adapter has no business opening the session store itself.

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode/utf16"

	"github.com/EvilFreelancer/coddy-agent/internal/llm"
)

const (
	// historyTurns is how many of the last messages the card shows: enough to
	// recognise the conversation, few enough to read without scrolling.
	historyTurns = 30
	// historyLineRunes is how much of one message a line keeps.
	historyLineRunes = 120
	// historyButton opens the whole transcript.
	historyButton = "📜 Развернуть"
	// historyTitle names the entry in the details store.
	historyTitle = "История беседы"
	// historySlack is the room kept under the platform limit. The card is built
	// with a margin so a prefix or a note added later cannot push it over, and so
	// a card that fits by our count is not refused by MAX's.
	historySlack = 20
	// historyFailedNote is what a person reads when the platform refuses the card
	// itself: the switch happened, and the history they were promised is missing.
	historyFailedNote = "Историю беседы не удалось отправить: MAX отказал по размеру " +
		"сообщения. Она целиком есть в веб-интерфейсе."
)

// SetTranscript hands the bot the reader it asks for a session's messages. A nil
// reader, or one that knows nothing about a session, leaves /resume as it was:
// the confirmation alone.
func (b *Bot) SetTranscript(read transcriptReader) {
	b.transcript = read
}

// transcriptReader hands the bot the messages of a session it did not author.
// The gateway adapter never opens the session store itself: the process wires
// this at startup, so a bot built in a test has no reader and posts no card.
type transcriptReader func(sessionID string) []llm.Message

// historyCard is the message that opens a switched conversation: the divider,
// then one line per message of the tail. The card is a single message, and MAX
// refuses a text longer than maxTextRunes, so the oldest lines give way rather
// than the whole card: a note counts what stayed behind and names the button that
// holds the transcript in full.
func historyCard(title string, msgs []llm.Message) string {
	head := fmt.Sprintf("── беседа «%s» · %s ──", historyName(title), historyCount(len(msgs)))
	if len(msgs) == 0 {
		return head + "\nПока пусто: здесь ещё не было ни одного сообщения."
	}

	budget := maxTextRunes - historySlack
	all := historyLines(msgs, historyTurns)
	shown := all
	for {
		note := ""
		if dropped := len(all) - len(shown); dropped > 0 {
			note = "\n" + historyNote(dropped)
		}
		text := head + "\n" + strings.Join(shown, "\n") + note
		if textUnits(text) <= budget {
			return text
		}
		if len(shown) == 0 {
			// The head alone does not fit, which only a very long title makes
			// possible: it is the one part the card cannot give up.
			return clipUnits(head, budget)
		}
		shown = shown[:len(shown)-1]
	}
}

// historyNote counts the lines that did not fit the card, so a short card reads
// as a budget rather than as a conversation that had little in it.
func historyNote(dropped int) string {
	word := "строк"
	switch {
	case dropped%10 == 1 && dropped%100 != 11:
		word = "строка"
	case dropped%10 >= 2 && dropped%10 <= 4 && (dropped%100 < 12 || dropped%100 > 14):
		word = "строки"
	}

	return fmt.Sprintf("…и ещё %d %s — «%s»", dropped, word, historyButton)
}

// historyName is the title the card shows: a session without one is called by
// what it is rather than by an empty pair of quotes.
func historyName(title string) string {
	title = strings.TrimSpace(title)
	if title == "" {
		return "без названия"
	}

	return title
}

// historyCount is the Russian count of messages: 1 сообщение, 2 сообщения,
// 5 сообщений.
func historyCount(n int) string {
	word := "сообщений"
	switch {
	case n%10 == 1 && n%100 != 11:
		word = "сообщение"
	case n%10 >= 2 && n%10 <= 4 && (n%100 < 12 || n%100 > 14):
		word = "сообщения"
	}

	return fmt.Sprintf("%d %s", n, word)
}

// historyLines renders the tail of a transcript, newest first, one line each: the
// operator reads a card from the end of the conversation back. A tool step and a
// system message say nothing a person would recognise, so they are skipped; an
// assistant answer that is empty (a call without text) is skipped with them.
func historyLines(msgs []llm.Message, limit int) []string {
	lines := make([]string, 0, limit)
	for i := len(msgs) - 1; i >= 0 && len(lines) < limit; i-- {
		line := historyLine(msgs[i], historyLineRunes)
		if line == "" {
			continue
		}
		lines = append(lines, line)
	}
	return lines
}

// historyLine is one message as a single line: when it was said, who said it and
// what was said, shortened to limit runes.
func historyLine(m llm.Message, limit int) string {
	var who string
	switch m.Role {
	case llm.RoleUser:
		who = "👤 "
	case llm.RoleAssistant:
		who = "🤖 "
	default:
		return ""
	}
	text := strings.TrimSpace(oneLine(m.Content))
	if text == "" {
		return ""
	}
	if stamp := historyStamp(m.CreatedAt); stamp != "" {
		who = stamp + " " + who
	}

	return who + shortenRunes(text, limit)
}

// historyStamp renders when a message was appended, in the shape the operator
// asked for: DD.MM.YY-hh:mm:ss, in the machine's own zone so it matches the clock
// on the wall rather than UTC. A message stored before the field existed has no
// time, and its line simply starts with the speaker.
func historyStamp(createdAt string) string {
	at := strings.TrimSpace(createdAt)
	if at == "" {
		return ""
	}
	t, err := time.Parse(time.RFC3339, at)
	if err != nil {
		return ""
	}

	return t.Local().Format("02.01.06-15:04:05")
}

// historyTranscript is the whole conversation, for the button: every message with
// its speaker, in full, so a page can be read like the chat it came from.
//
// Newest first, exactly like the card it opens from. The card reads from the end
// of the conversation back, and opening it used to turn the conversation round:
// the same messages arrived oldest first, so the last thing said appeared below
// everything else and the reader had to scroll to the end of a long page to find
// out where they were. The order is the card's order; the button only shows more
// of it.
func historyTranscript(msgs []llm.Message) string {
	var sb strings.Builder
	for i := len(msgs) - 1; i >= 0; i-- {
		m := msgs[i]
		var who string
		switch m.Role {
		case llm.RoleUser:
			who = "👤 Человек"
		case llm.RoleAssistant:
			who = "🤖 Агент"
		default:
			continue
		}
		text := strings.TrimSpace(m.Content)
		if text == "" {
			continue
		}
		if sb.Len() > 0 {
			sb.WriteString("\n\n")
		}
		sb.WriteString(who)
		if stamp := historyStamp(m.CreatedAt); stamp != "" {
			sb.WriteString(" · ")
			sb.WriteString(stamp)
		}
		sb.WriteString(":\n")
		sb.WriteString(text)
	}

	return sb.String()
}

// postHistoryCard puts the card of the conversation the chat was just switched to
// into that chat. It says nothing when no reader is wired: a switch then reads as
// it did before.
func (b *Bot) postHistoryCard(ctx context.Context, c *Client, in inbound, id, title string) {
	if b.transcript == nil || id == "" {
		return
	}
	msgs := b.transcript(id)
	text := historyCard(title, msgs)
	out := Outgoing{Target: b.answerTarget(in), Text: text}
	if len(msgs) > 0 {
		token := b.details.put(toolDetail{
			Title:      historyTitle + " «" + historyName(title) + "»",
			Body:       historyTranscript(msgs),
			Icon:       "📜",
			ButtonText: historyButton,
			Collapsed:  text,
		})
		out.Buttons = [][]Button{{{Text: historyButton, Payload: detailsCallback(token, 0)}}}
	}
	if _, err := c.Send(ctx, out); err != nil {
		// A refusal of the platform leaves the person without the card they were
		// promised, so it belongs in the log at a level somebody watches.
		b.log.Warn("max: history card not delivered", "err", err, "session", id)
		// Silence is what the reader cannot act on: the switch is confirmed and
		// the history is simply absent. Say it in one line instead.
		note := Outgoing{Target: b.answerTarget(in), Text: historyFailedNote}
		if _, noteErr := c.Send(ctx, note); noteErr != nil {
			b.log.Warn("max: history card refusal not told", "err", noteErr, "session", id)
		}

		return
	}
	b.log.Debug("max: history card posted", "session", id, "messages", len(msgs))
}

// textUnits is the length MAX itself measures: a text is refused when its UTF-16
// code units pass the platform limit, not when its characters do. Every emoji a
// line carries (🤖 for the agent, 👤 for the person) lies outside the basic
// plane and costs two units, so thirty lines measured in runes fit while the same
// thirty lines measured in units do not - and the card, promised to the reader,
// was refused as too long.
func textUnits(text string) int {
	return len(utf16.Encode([]rune(text)))
}

// clipUnits cuts text until it fits the budget in the units the platform counts,
// marking the cut so a reader knows there was more.
func clipUnits(text string, budget int) string {
	if textUnits(text) <= budget {
		return text
	}
	runes := []rune(text)
	for len(runes) > 0 {
		runes = runes[:len(runes)-1]
		if cut := string(runes) + "…"; textUnits(cut) <= budget {
			return cut
		}
	}

	return "…"
}

// shortenRunes cuts text to limit runes, marking the cut so a reader knows there
// was more.
func shortenRunes(text string, limit int) string {
	runes := []rune(text)
	if len(runes) <= limit {
		return text
	}
	if limit <= 1 {
		return "…"
	}

	return string(runes[:limit-1]) + "…"
}
