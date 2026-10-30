//go:build gateway || gateway.max

package max

import (
	"encoding/json"
	"strconv"
	"strings"
	"sync"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
)

// A tool call is the part of a turn a person watching the chat most wants to
// look at: the command the agent ran and what came back. MAX has no collapsible
// blocks, so a call is a message of its own with one button, and the button
// edits that same message into the command and its output - the closest thing
// to the collapsible call the web shows.
const (
	// callbackActionDetails is the verb carried by a details button.
	callbackActionDetails = "det"
	// detailsButtonText opens a call and collapses it back.
	detailsButtonText = "🔍 Подробности"
	// detailsCollapseText is the way back to the one-line form.
	detailsCollapseText = "Свернуть"
	// detailPageRunes is how much of the output one page holds: a MAX message
	// takes 4000, and the command above the output needs room of its own.
	detailPageRunes = 3500
	// detailTitleRunes is how much of the command the collapsed message shows,
	// so a long one does not fill the chat before it is expanded.
	detailTitleRunes = 220
	// detailCommandRunes is how much of the command the opened call shows: the
	// web prints it whole in a code block, a message has 4000 runes for the
	// command and the page of output together.
	detailCommandRunes = 1200
	// detailKeep is how many calls the store keeps: a chat reads the last few,
	// and a bot that runs for weeks must not grow without a bound.
	detailKeep = 200
	// pageCollapsed is the page value that puts a call back to one line.
	pageCollapsed = -1
)

// toolDetail is one call: what was run and what came back.
type toolDetail struct {
	Title string
	Body  string
	// Command is what the call was made with - the command line of a
	// run_command, the path of a read - which the web shows above the output.
	// MAX reports it in the update that starts the call, and the collapsed
	// message takes it as its name instead of the bare tool name.
	Command string
	// Icon leads the expanded view. Empty means the gear of a tool call; a
	// transcript takes a scroll instead, so a reader can tell the two apart
	// before opening either.
	Icon string
	// ButtonText is what the collapsed message offers. Empty is the details
	// button of a tool call.
	ButtonText string
	// Collapsed replaces the one-line title when the entry is folded back:
	// a transcript folds back into the card that introduced it, not into its
	// own name.
	Collapsed string
}

// detailStore keeps the calls a chat may expand, keyed by the token their
// buttons carry. The zero value is ready to use.
type detailStore struct {
	mu    sync.Mutex
	seq   uint64
	order []string
	items map[string]toolDetail
}

// put stores a call and returns the token its button carries.
func (s *detailStore) put(d toolDetail) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.items == nil {
		s.items = make(map[string]toolDetail)
	}
	s.seq++
	token := "d" + strconv.FormatUint(s.seq, 36)
	s.items[token] = d
	s.order = append(s.order, token)
	for len(s.order) > detailKeep {
		drop := s.order[0]
		s.order = s.order[1:]
		delete(s.items, drop)
	}

	return token
}

// setBody puts what a call returned into the stored entry. A call whose token
// has already been dropped is left alone: its button is stale by then, and a
// press on it says so.
func (s *detailStore) setBody(token, body string) {
	if token == "" || body == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	d, ok := s.items[token]
	if !ok {
		return
	}
	d.Body = body
	s.items[token] = d
}

// get returns the stored call.
func (s *detailStore) get(token string) (toolDetail, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, ok := s.items[token]

	return d, ok
}

// setCommand puts what a call was made with into the stored entry, and reports
// whether the entry took it: the call reports its arguments once, and a second
// report must not rename a message that already stands in the chat.
func (s *detailStore) setCommand(token, command string) bool {
	command = strings.TrimSpace(command)
	if token == "" || command == "" {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	d, ok := s.items[token]
	if !ok || strings.TrimSpace(d.Command) != "" {
		return false
	}
	d.Command = command
	s.items[token] = d

	return true
}

// detailPages cuts a call's output into pages that each fit one message,
// preferring to end a page on a line break so a line is not split in two.
func detailPages(body string) []string {
	body = strings.TrimRight(body, "\n")
	if strings.TrimSpace(body) == "" {
		return []string{""}
	}
	runes := []rune(body)
	pages := make([]string, 0, len(runes)/detailPageRunes+1)
	for len(runes) > detailPageRunes {
		cut := detailPageRunes
		for i := detailPageRunes - 1; i > detailPageRunes/2; i-- {
			if runes[i] == '\n' {
				cut = i
				break
			}
		}
		pages = append(pages, string(runes[:cut]))
		runes = runes[cut:]
	}

	return append(pages, string(runes))
}

// detailsCallback builds the payload of one details button: page 0 opens the
// call, a positive page turns to that page, pageCollapsed folds it again.
func detailsCallback(token string, page int) string {
	if page == 0 {
		return callbackActionDetails + ":" + token
	}

	return callbackActionDetails + ":" + token + ":" + strconv.Itoa(page)
}

// detailsView renders what a details press shows: the command, one page of its
// output, where in the output the page is, and the buttons that turn the pages.
// The collapsed form is the one-line message the call started as.
// icon is what leads an entry's view: the gear of a tool call unless the entry
// named its own.
func icon(d toolDetail) string {
	if d.Icon != "" {
		return d.Icon
	}

	return "⚙️"
}

// buttonText is what a folded entry offers: the details button unless the entry
// named its own.
func buttonText(d toolDetail) string {
	if d.ButtonText != "" {
		return d.ButtonText
	}

	return detailsButtonText
}

func detailsView(token string, d toolDetail, page int) (string, [][]Button) {
	title := detailTitle(d)
	if page == pageCollapsed {
		text := d.Collapsed
		if text == "" {
			text = icon(d) + " " + title
		}
		return text, [][]Button{{{Text: buttonText(d), Payload: detailsCallback(token, 0)}}}
	}
	pages := detailPages(d.Body)
	if page < 0 || page >= len(pages) {
		page = 0
	}
	var sb strings.Builder
	sb.WriteString(icon(d) + " " + title + "\n\n")
	// The command in full, when the one-line title had to shorten it: this is
	// the half of a call the web shows above the output, and a chat would
	// otherwise lose what was run.
	if cmd := strings.TrimSpace(d.Command); cmd != "" && oneLine(cmd) != title {
		sb.WriteString("Команда:\n" + truncateRunes(cmd, detailCommandRunes) + "\n\n")
	}
	if body := strings.TrimSpace(pages[page]); body != "" {
		sb.WriteString(body)
	} else {
		sb.WriteString("(вывода нет)")
	}
	if len(pages) > 1 {
		sb.WriteString("\n\nСтраница " + strconv.Itoa(page+1) + " из " + strconv.Itoa(len(pages)))
	}
	row := make([]Button, 0, 3)
	if page > 0 {
		row = append(row, Button{Text: "◀", Payload: detailsCallback(token, page-1)})
	}
	if page < len(pages)-1 {
		row = append(row, Button{Text: "▶", Payload: detailsCallback(token, page+1)})
	}
	row = append(row, Button{Text: detailsCollapseText, Payload: detailsCallback(token, pageCollapsed)})

	return sb.String(), [][]Button{row}
}

// detailTitle is the one line a call is called by: what it ran once the
// arguments are known, the tool's name until then.
func detailTitle(d toolDetail) string {
	if cmd := strings.TrimSpace(d.Command); cmd != "" {
		return truncateRunes(oneLine(cmd), detailTitleRunes)
	}
	if title := truncateRunes(strings.TrimSpace(d.Title), detailTitleRunes); title != "" {
		return title
	}

	return "вызов инструмента"
}

// oneLine folds a command into one line, so the collapsed message stays a
// single line however the command was written.
func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// callArguments reads what a call was made with out of the update the agent
// sends when the call starts: the arguments travel as that update's text, so
// the chat can name a call the way the web does. It reports false for output
// that happens to be JSON but names none of the arguments.
func callArguments(items []acp.ToolCallResultItem) (string, bool) {
	raw := strings.TrimSpace(toolCallBody(items))
	if raw == "" || raw[0] != '{' {
		return "", false
	}
	var args map[string]any
	if err := json.Unmarshal([]byte(raw), &args); err != nil {
		return "", false
	}
	for _, key := range []string{"command", "cmd", "path", "file_path", "file", "query"} {
		if v, ok := args[key].(string); ok && strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v), true
		}
	}

	return "", false
}

// postDetails puts one call into the chat with the button that opens it.
func (s *Sender) postDetails(token, title string) string {
	text, buttons := detailsView(token, toolDetail{Title: title}, pageCollapsed)
	mid, err := s.c.Send(s.ctx, Outgoing{Target: s.target, Text: text, Buttons: buttons})
	if err != nil {
		// The call happened and its details are lost with the message: at debug
		// nobody would ever notice the chat missing a step.
		s.b.log.Warn("max: tool call not posted", "err", err)

		return ""
	}

	return mid
}

// renameDetails puts the command into the message the call already has, so the
// chat names a call by what it ran rather than by the tool that ran it, and
// gains no extra message for it.
func (s *Sender) renameDetails(token, mid string) {
	if token == "" || mid == "" {
		return
	}
	d, ok := s.b.details.get(token)
	if !ok {
		return
	}
	text, buttons := detailsView(token, d, pageCollapsed)
	if err := s.c.EditMessage(s.ctx, mid, text, buttons); err != nil {
		s.b.log.Debug("max: tool call not renamed", "err", err)
	}
}

// toolCallBody is what a tool call returned, as the ACP update carries it.
func toolCallBody(items []acp.ToolCallResultItem) string {
	var sb strings.Builder
	for _, item := range items {
		text := strings.TrimSpace(item.Content.Text)
		if text == "" {
			continue
		}
		if sb.Len() > 0 {
			sb.WriteString("\n")
		}
		sb.WriteString(text)
	}

	return sb.String()
}
