//go:build gateway || gateway.max

package max

// Unit tests of the history card a switch of the conversation posts: what a line
// of it looks like, how much of the transcript it keeps, and what it does when
// there is nothing to show or nobody to ask.

import (
	"context"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/llm"
)

func TestHistoryLines_MarkWhoSpokeAndSkipToolSteps(t *testing.T) {
	msgs := []llm.Message{
		{Role: llm.RoleUser, Content: "посмотри на gateway"},
		{Role: llm.RoleAssistant, Content: "смотрю"},
		{Role: llm.RoleTool, Content: "grep: 12 совпадений"},
		{Role: llm.RoleAssistant, Content: "   "},
		{Role: llm.RoleUser, Content: "и что?"},
	}

	lines := historyLines(msgs, historyTurns)
	// Newest first: the card is read from the end of the conversation back.
	want := []string{"👤 и что?", "🤖 смотрю", "👤 посмотри на gateway"}
	if len(lines) != len(want) {
		t.Fatalf("lines = %d %v, want %d", len(lines), lines, len(want))
	}
	for i := range want {
		if lines[i] != want[i] {
			t.Fatalf("line %d = %q, want %q", i, lines[i], want[i])
		}
	}
}

func TestHistoryLines_KeepTheTailNewestFirst(t *testing.T) {
	msgs := make([]llm.Message, 0, 40)
	for i := 1; i <= 40; i++ {
		role := llm.RoleUser
		if i%2 == 0 {
			role = llm.RoleAssistant
		}
		msgs = append(msgs, llm.Message{Role: role, Content: "сообщение " + itoa(i)})
	}

	lines := historyLines(msgs, historyTurns)
	if len(lines) != historyTurns {
		t.Fatalf("lines = %d, want %d", len(lines), historyTurns)
	}
	// The card is read from the end of the conversation back, so the newest
	// message stands first and the oldest of the kept tail closes it.
	if !strings.HasSuffix(lines[0], "сообщение 40") {
		t.Fatalf("first line = %q, want the newest message at the top", lines[0])
	}
	if !strings.HasSuffix(lines[len(lines)-1], "сообщение 11") {
		t.Fatalf("last line = %q, want the kept tail to end at message 11", lines[len(lines)-1])
	}
}

func TestHistoryStamp_IsDayMonthYearAndTheLocalClock(t *testing.T) {
	at := time.Date(2026, 10, 18, 6, 13, 32, 0, time.UTC)
	got := historyStamp(at.Format(time.RFC3339))
	if want := at.Local().Format("02.01.06-15:04:05"); got != want {
		t.Fatalf("historyStamp = %q, want %q", got, want)
	}
	if !regexp.MustCompile(`^\d{2}\.\d{2}\.\d{2}-\d{2}:\d{2}:\d{2}$`).MatchString(got) {
		t.Fatalf("historyStamp = %q, want DD.MM.YY-hh:mm:ss", got)
	}
}

func TestHistoryStamp_SaysNothingWithoutATime(t *testing.T) {
	for _, in := range []string{"", "   ", "вчера", "18.10.2026 09:13"} {
		if got := historyStamp(in); got != "" {
			t.Fatalf("historyStamp(%q) = %q, want nothing", in, got)
		}
	}
}

func TestHistoryLine_OpensWithTheTimeThenTheSpeaker(t *testing.T) {
	at := time.Date(2026, 10, 18, 6, 13, 32, 0, time.UTC)
	line := historyLine(llm.Message{Role: llm.RoleAssistant, Content: "готово", CreatedAt: at.Format(time.RFC3339)}, historyLineRunes)
	want := at.Local().Format("02.01.06-15:04:05") + " 🤖 готово"
	if line != want {
		t.Fatalf("line = %q, want %q", line, want)
	}
}

func TestHistoryTranscript_StampsBothSpeakers(t *testing.T) {
	at := time.Date(2026, 10, 18, 6, 13, 32, 0, time.UTC)
	body := historyTranscript([]llm.Message{
		{Role: llm.RoleUser, Content: "вопрос", CreatedAt: at.Format(time.RFC3339)},
		{Role: llm.RoleAssistant, Content: "ответ", CreatedAt: at.Format(time.RFC3339)},
	})
	if got := strings.Count(body, at.Local().Format("02.01.06-15:04:05")); got != 2 {
		t.Fatalf("transcript = %q, want both headers stamped", body)
	}
}

func TestHistoryLine_ShortensLongTextWithoutBreakingRunes(t *testing.T) {
	long := strings.Repeat("я", historyLineRunes+50)
	line := historyLine(llm.Message{Role: llm.RoleUser, Content: long}, historyLineRunes)
	body := strings.TrimPrefix(line, "👤 ")
	if !strings.HasSuffix(body, "…") {
		t.Fatalf("line %q, want it marked as cut", body)
	}
	if got := len([]rune(body)); got != historyLineRunes {
		t.Fatalf("line is %d runes, want %d", got, historyLineRunes)
	}
	if strings.ContainsRune(body, '\ufffd') {
		t.Fatalf("line %q holds a broken rune", body)
	}
}

func TestHistoryCount_CountsInRussian(t *testing.T) {
	cases := map[int]string{1: "1 сообщение", 2: "2 сообщения", 5: "5 сообщений", 11: "11 сообщений", 21: "21 сообщение", 104: "104 сообщения"}
	for n, want := range cases {
		if got := historyCount(n); got != want {
			t.Fatalf("historyCount(%d) = %q, want %q", n, got, want)
		}
	}
}

func TestHistoryCard_OpensWithWhereTheChatLanded(t *testing.T) {
	msgs := []llm.Message{
		{Role: llm.RoleUser, Content: "переключись"},
		{Role: llm.RoleAssistant, Content: "переключился"},
	}
	card := historyCard("Плагины МАКСа", msgs)
	if !strings.Contains(card, "── беседа «Плагины МАКСа» · 2 сообщения ──") {
		t.Fatalf("card = %q, want the divider with the title and the count", card)
	}
	if !strings.Contains(card, "👤 переключись") || !strings.Contains(card, "🤖 переключился") {
		t.Fatalf("card = %q, want both lines under the divider", card)
	}
}

func TestHistoryCard_KeepsTheCardInsideOneMessage(t *testing.T) {
	// A long conversation is the case that broke the card: thirty lines of a
	// hundred-odd runes each ran past the four thousand MAX accepts, the platform
	// refused the whole message, and after a switch the person saw the
	// confirmation alone with no history under it.
	msgs := make([]llm.Message, 0, historyTurns)
	for i := 0; i < historyTurns; i++ {
		msgs = append(msgs, llm.Message{
			Role:      llm.RoleAssistant,
			Content:   strings.Repeat("длинная реплика ассистента ", 20),
			CreatedAt: "2026-10-25T20:30:00+03:00",
		})
	}

	card := historyCard("Проект Max-Gate", msgs)

	if got := textUnits(card); got > maxTextRunes {
		t.Fatalf("card = %d UTF-16 units, want at most %d: MAX counts units, not characters", got, maxTextRunes)
	}
	if !strings.Contains(card, "🤖") {
		t.Fatalf("card = %q, want the newest line kept", card)
	}
	if !strings.Contains(card, "…и ещё") || !strings.Contains(card, historyButton) {
		t.Fatalf("card = %q, want a note counting the lines left out", card)
	}
}

func TestHistoryCard_FitsTheUnitsThePlatformCounts(t *testing.T) {
	// The case from the field: thirty lines of a hundred-odd characters each fit
	// our own ruler, and MAX still refused the card, because it counts UTF-16 code
	// units and every 🤖/👤 marker costs two. A card that passes here must pass
	// there, with the slack the builder keeps under the limit.
	msgs := make([]llm.Message, 0, historyTurns)
	for i := 0; i < historyTurns; i++ {
		msgs = append(msgs, llm.Message{
			Role:      llm.RoleAssistant,
			Content:   strings.Repeat("я", 200),
			CreatedAt: "2026-10-31T20:30:00+03:00",
		})
	}

	card := historyCard("Вёрста, у нас периодически возникают проблемы с поиском", msgs)

	if got := textUnits(card); got > maxTextRunes {
		t.Fatalf("card = %d UTF-16 units, want at most %d", got, maxTextRunes)
	}
	if runes := len([]rune(card)); textUnits(card) <= runes {
		t.Fatalf("card = %d runes and %d units: the emoji markers are not being counted as units", runes, textUnits(card))
	}
	if !strings.Contains(card, "── беседа") {
		t.Fatalf("card = %q, want the divider kept", card)
	}
}

func TestClipUnits_CountsUnitsNotRunes(t *testing.T) {
	// Sixty 🤖 are sixty characters and a hundred and twenty units: cutting by
	// characters would leave a text the platform refuses.
	text := strings.Repeat("🤖", 60)
	got := clipUnits(text, 100)
	if n := textUnits(got); n > 100 {
		t.Fatalf("clipUnits = %d units, want at most 100", n)
	}
	if !strings.HasSuffix(got, "…") {
		t.Fatalf("clipUnits = %q, want a marked cut", got)
	}
}

func TestHistoryCard_AnEmptySessionSaysSo(t *testing.T) {
	card := historyCard("", nil)
	if !strings.Contains(card, "«без названия»") {
		t.Fatalf("card = %q, want a name for a session without a title", card)
	}
	if !strings.Contains(card, "Пока пусто") {
		t.Fatalf("card = %q, want it to say the session has nothing", card)
	}
}

func TestPostHistoryCard_PostsTheTailWithTheButton(t *testing.T) {
	f := newSendServer(t)
	b := newTestBot(&stubRunner{})
	c := f.client(t)
	b.SetTranscript(func(string) []llm.Message {
		return []llm.Message{
			{Role: llm.RoleUser, Content: "первый вопрос"},
			{Role: llm.RoleAssistant, Content: "первый ответ"},
		}
	})

	b.postHistoryCard(context.Background(), c, inbound{chatID: 10, userID: 1}, "s1", "Черновик")

	sent := f.sends()
	if len(sent) != 1 {
		t.Fatalf("sends = %d, want exactly one card", len(sent))
	}
	text := sent[0].body.Text
	if !strings.Contains(text, "── беседа «Черновик» · 2 сообщения ──") {
		t.Fatalf("text = %q, want the divider", text)
	}
	if !strings.Contains(text, "👤 первый вопрос") {
		t.Fatalf("text = %q, want the tail", text)
	}
	if !strings.Contains(string(sent[0].body.Attachments), "det:") {
		t.Fatalf("attachments = %s, want a details button carrying the transcript", sent[0].body.Attachments)
	}
	if len(b.details.order) != 1 {
		t.Fatalf("details entries = %d, want the transcript kept for the button", len(b.details.order))
	}
	d, ok := b.details.get(b.details.order[0])
	if !ok || !strings.Contains(d.Body, "👤 Человек:\nпервый вопрос") {
		t.Fatalf("stored transcript = %+v, want every message in full", d)
	}
	if d.Icon != "📜" || d.ButtonText != historyButton {
		t.Fatalf("stored entry = %+v, want the transcript icon and button", d)
	}
}

func TestPostHistoryCard_AnEmptySessionGetsNoButton(t *testing.T) {
	f := newSendServer(t)
	b := newTestBot(&stubRunner{})
	c := f.client(t)
	b.SetTranscript(func(string) []llm.Message { return nil })

	b.postHistoryCard(context.Background(), c, inbound{chatID: 10, userID: 1}, "s1", "Пустая")

	sent := f.sends()
	if len(sent) != 1 {
		t.Fatalf("sends = %d, want the card of an empty session", len(sent))
	}
	if strings.Contains(string(sent[0].body.Attachments), "det:") {
		t.Fatalf("attachments = %s, want no button over an empty transcript", sent[0].body.Attachments)
	}
}

func TestPostHistoryCard_WithoutAReaderSaysNothing(t *testing.T) {
	f := newSendServer(t)
	b := newTestBot(&stubRunner{})
	c := f.client(t)

	b.postHistoryCard(context.Background(), c, inbound{chatID: 10, userID: 1}, "s1", "Черновик")

	if got := len(f.sends()); got != 0 {
		t.Fatalf("sends = %d, want none: the bot was never handed a reader", got)
	}
}

// itoa keeps the test free of a fmt dependency it would otherwise need once.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var digits []byte
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}

	return string(digits)
}

// TestHistoryTranscript_KeepsTheCardOrder is the report from the operator: the
// card reads from the end of the conversation back, and opening it used to turn
// the conversation round - the oldest message came first, so the reader had to
// scroll to the bottom to find where they were.
func TestHistoryTranscript_KeepsTheCardOrder(t *testing.T) {
	msgs := []llm.Message{
		{Role: llm.RoleUser, Content: "первое"},
		{Role: llm.RoleAssistant, Content: "второе"},
		{Role: llm.RoleUser, Content: "третье"},
	}

	got := historyTranscript(msgs)

	newest := strings.Index(got, "третье")
	oldest := strings.Index(got, "первое")
	if newest < 0 || oldest < 0 {
		t.Fatalf("transcript lost a message: %q", got)
	}
	if newest > oldest {
		t.Fatalf("the expanded view starts with the oldest message:\n%s", got)
	}
}
