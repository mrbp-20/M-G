//go:build gateway || gateway.max

package max

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
)

// TestDetailStore_KeepsTheLastCalls: the store is bounded, because a bot that
// runs for weeks must not grow with the number of calls it has seen.
func TestDetailStore_KeepsTheLastCalls(t *testing.T) {
	var s detailStore
	first := s.put(toolDetail{Title: "первый"})
	for i := 0; i < detailKeep; i++ {
		s.put(toolDetail{Title: "ещё"})
	}
	if _, ok := s.get(first); ok {
		t.Fatal("the oldest call is still stored, want it dropped once the store is full")
	}
	if len(s.items) != detailKeep {
		t.Fatalf("%d calls stored, want %d", len(s.items), detailKeep)
	}
}

// TestDetailStore_SetBodyLandsOnTheStoredCall: the output arrives after the
// call, and it has to reach the entry the button already points at.
func TestDetailStore_SetBodyLandsOnTheStoredCall(t *testing.T) {
	var s detailStore
	token := s.put(toolDetail{Title: "grep x"})
	s.setBody(token, "строка вывода")
	got, ok := s.get(token)
	if !ok {
		t.Fatal("the stored call is gone")
	}
	if got.Title != "grep x" || got.Body != "строка вывода" {
		t.Fatalf("stored %q / %q, want the command and its output", got.Title, got.Body)
	}
	// A token that was dropped is left alone: its button is stale by then.
	s.setBody("нет-такого", "что-то")
	if _, ok := s.get("нет-такого"); ok {
		t.Fatal("a body was stored under an unknown token")
	}
}

// TestDetailPages_EachFitsOneMessage: no page is over the budget, none is lost,
// and a page ends on a line break when there is one.
func TestDetailPages_EachFitsOneMessage(t *testing.T) {
	var sb strings.Builder
	for i := 0; i < 400; i++ {
		sb.WriteString("строка вывода номер ")
		sb.WriteString(strings.Repeat("x", 40))
		sb.WriteString("\n")
	}
	body := sb.String()
	pages := detailPages(body)
	if len(pages) < 2 {
		t.Fatalf("%d pages for %d runes, want the output cut", len(pages), len(body))
	}
	var joined strings.Builder
	for i, page := range pages {
		if n := len([]rune(page)); n > detailPageRunes {
			t.Fatalf("page %d holds %d runes, over the %d page budget", i+1, n, detailPageRunes)
		}
		joined.WriteString(page)
	}
	if strings.TrimRight(joined.String(), "\n") != strings.TrimRight(body, "\n") {
		t.Fatal("the pages do not add up to the output")
	}
}

// TestDetailsView_CollapsedThenExpanded: the message starts as one line with the
// button, opens into command and output, and folds back again.
func TestDetailsView_CollapsedThenExpanded(t *testing.T) {
	d := toolDetail{Title: "grep -rn \"портянка\" .", Body: "dispatch.go:220: new session started"}

	text, buttons := detailsView("d1", d, pageCollapsed)
	if text != "⚙️ "+d.Title {
		t.Fatalf("collapsed text = %q, want the command on one line", text)
	}
	if len(buttons) != 1 || len(buttons[0]) != 1 {
		t.Fatalf("collapsed buttons = %v, want the one that opens the call", buttons)
	}
	if got, want := buttons[0][0].Payload, "det:d1"; got != want {
		t.Fatalf("open payload = %q, want %q", got, want)
	}
	if buttons[0][0].Text != detailsButtonText {
		t.Fatalf("button text = %q, want %q", buttons[0][0].Text, detailsButtonText)
	}

	text, buttons = detailsView("d1", d, 0)
	if !strings.Contains(text, d.Title) || !strings.Contains(text, d.Body) {
		t.Fatalf("expanded view = %q, want the command and its output", text)
	}
	if len(buttons) != 1 || len(buttons[0]) != 1 || buttons[0][0].Text != detailsCollapseText {
		t.Fatalf("expanded buttons = %v, want the one that folds the call", buttons)
	}
	if got, want := buttons[0][0].Payload, "det:d1:-1"; got != want {
		t.Fatalf("collapse payload = %q, want %q", got, want)
	}
}

// TestDetailsView_TurnsPages: an output longer than a message offers the next
// page, and the pages are what the buttons name.
func TestDetailsView_TurnsPages(t *testing.T) {
	body := strings.Repeat("x", detailPageRunes*2+10)
	d := toolDetail{Title: "cat big.txt", Body: body}

	text, buttons := detailsView("d2", d, 0)
	if !strings.Contains(text, "Страница 1 из 3") {
		t.Fatalf("first page = %q, want it to say where it is", text[max(0, len(text)-60):])
	}
	if len(buttons[0]) != 2 {
		t.Fatalf("first page has %d buttons, want only the way forward and the fold", len(buttons[0]))
	}
	for _, b := range buttons[0] {
		if b.Payload == "det:d2:1" && b.Text != "▶" {
			t.Fatalf("the forward button = %q, want ▶", b.Text)
		}
	}

	_, buttons = detailsView("d2", d, 1)
	if len(buttons[0]) != 3 {
		t.Fatalf("a middle page has %d buttons, want back, forward and the fold", len(buttons[0]))
	}

	last, _ := detailsView("d2", d, 2)
	if !strings.Contains(last, "Страница 3 из 3") {
		t.Fatal("the last page does not say it is the last")
	}
}

// TestDetailsView_EmptyOutput: a call that printed nothing still opens, and says
// so instead of showing an empty message.
func TestDetailsView_EmptyOutput(t *testing.T) {
	text, _ := detailsView("d3", toolDetail{Title: "true"}, 0)
	if !strings.Contains(text, "вывода нет") {
		t.Fatalf("view = %q, want it to say the call printed nothing", text)
	}
}

// TestToolCallBody_JoinsWhatCame: the output arrives as items, and each one's
// text is what the button shows.
func TestToolCallBody_JoinsWhatCame(t *testing.T) {
	items := []acp.ToolCallResultItem{
		{Type: "content", Content: acp.ContentBlock{Type: "text", Text: "первая строка"}},
		{Type: "content", Content: acp.ContentBlock{Type: "text", Text: ""}},
		{Type: "content", Content: acp.ContentBlock{Type: "text", Text: "вторая строка"}},
	}
	if got, want := toolCallBody(items), "первая строка\nвторая строка"; got != want {
		t.Fatalf("body = %q, want %q", got, want)
	}
}

// TestSender_AToolCallCarriesTheDetailsButton: the call reaches the chat as a
// message of its own with the button, because the live message becomes the
// answer and would take the button with it.
func TestSender_AToolCallCarriesTheDetailsButton(t *testing.T) {
	f := newSendServer(t)
	b := testBot(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := b.newSender(ctx, f.client(t), Target{UserID: 7})

	if err := s.SendSessionUpdate("sess", acp.ToolCallUpdate{Title: "grep -rn портянка ."}); err != nil {
		t.Fatalf("tool call: %v", err)
	}
	deadline := time.Now().Add(5 * time.Second)
	var found bool
	for time.Now().Before(deadline) && !found {
		for _, call := range f.sends() {
			if strings.HasPrefix(call.body.Text, "⚙️ grep -rn портянка") &&
				strings.Contains(string(call.body.Attachments), `"det:`) {
				found = true
				break
			}
		}
		if !found {
			time.Sleep(10 * time.Millisecond)
		}
	}
	if !found {
		t.Fatalf("no message carried the call and its details button; sent: %+v", f.sends())
	}
}

// TestCallArguments_ReadsWhatTheCallRuns: the agent reports a call's arguments
// in the update that starts it, and that text is what names the call.
func TestCallArguments_ReadsWhatTheCallRuns(t *testing.T) {
	for _, tc := range []struct {
		name string
		raw  string
		want string
		ok   bool
	}{
		{"command", `{"command":"ls -la /mnt"}`, "ls -la /mnt", true},
		{"cmd", `{"cmd":"df -h"}`, "df -h", true},
		{"path", `{"path":"/tmp/x.go"}`, "/tmp/x.go", true},
		{"empty command falls through", `{"command":"  ","path":"/a"}`, "/a", true},
		{"not arguments", `просто вывод инструмента`, "", false},
		{"json without arguments", `{"result":"ok"}`, "", false},
		{"broken json", `{"command":}`, "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			items := []acp.ToolCallResultItem{
				{Type: "content", Content: acp.ContentBlock{Type: "text", Text: tc.raw}},
			}
			got, ok := callArguments(items)
			if ok != tc.ok || got != tc.want {
				t.Fatalf("callArguments(%q) = (%q, %v), want (%q, %v)", tc.raw, got, ok, tc.want, tc.ok)
			}
		})
	}
}

// TestDetailsView_ANamedCallShowsItsCommand: the collapsed message names the
// call by what it ran, and the opened one keeps the command in full.
func TestDetailsView_ANamedCallShowsItsCommand(t *testing.T) {
	collapsed, _ := detailsView("d1", toolDetail{Title: "run_command", Command: "ls -la /mnt"}, pageCollapsed)
	if want := "⚙️ ls -la /mnt"; collapsed != want {
		t.Fatalf("collapsed = %q, want %q", collapsed, want)
	}
	long := strings.Repeat("cd /tmp && ", 20) + "make build"
	opened, _ := detailsView("d2", toolDetail{Title: "run_command", Command: long, Body: "готово"}, 0)
	if !strings.Contains(opened, "Команда:") {
		t.Fatalf("opened view does not show the whole command: %q", opened)
	}
	if !strings.Contains(opened, "готово") {
		t.Fatalf("opened view does not show the output: %q", opened)
	}
	// A short command is already the title, so it is not printed twice.
	short, _ := detailsView("d3", toolDetail{Title: "run_command", Command: "true", Body: "ok"}, 0)
	if strings.Contains(short, "Команда:") {
		t.Fatalf("a command equal to the title is shown twice: %q", short)
	}
}

// TestDetailStore_SetCommandLandsOnce: the call reports its arguments once, and
// a second report must not rename a message the chat already shows.
func TestDetailStore_SetCommandLandsOnce(t *testing.T) {
	var s detailStore
	token := s.put(toolDetail{Title: "run_command"})
	if !s.setCommand(token, "ls -la") {
		t.Fatal("the first arguments did not name the call")
	}
	if s.setCommand(token, "rm -rf /") {
		t.Fatal("a second report renamed the call, want the first one kept")
	}
	got, _ := s.get(token)
	if got.Command != "ls -la" {
		t.Fatalf("command = %q, want %q", got.Command, "ls -la")
	}
}

// TestSender_TheCallIsRenamedToItsCommand: the call is posted under the tool's
// name and renamed in place once the arguments arrive - no second message.
func TestSender_TheCallIsRenamedToItsCommand(t *testing.T) {
	f := newSendServer(t)
	b := testBot(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := b.newSender(ctx, f.client(t), Target{UserID: 7})

	if err := s.SendSessionUpdate("sess", acp.ToolCallUpdate{ToolCallID: "c1", Title: "run_command", Kind: "run_command"}); err != nil {
		t.Fatalf("tool call: %v", err)
	}
	args := `{"command":"ls -la /mnt"}`
	if err := s.SendSessionUpdate("sess", acp.ToolCallStatusUpdate{
		ToolCallID: "c1",
		Status:     "in_progress",
		Content:    []acp.ToolCallResultItem{{Type: "content", Content: acp.ContentBlock{Type: "text", Text: args}}},
	}); err != nil {
		t.Fatalf("status: %v", err)
	}

	waitFor(t, func() bool { return len(f.editCalls()) > 0 }, "the call renamed to its command")
	edits := f.editCalls()
	got := edits[len(edits)-1].body.Text
	if !strings.HasPrefix(got, "⚙️ ls -la /mnt") {
		t.Fatalf("renamed message = %q, want it to name the command", got)
	}
	// The call has one message of its own; the live indicator under it is not a
	// second call. A rename that posted a message instead of editing would show
	// up here as two messages carrying the details button.
	var calls int
	for _, sent := range f.sends() {
		if strings.HasPrefix(sent.body.Text, "⚙️ ") && strings.Contains(string(sent.body.Attachments), `"det:`) {
			calls++
		}
	}
	if calls != 1 {
		t.Fatalf("%d messages carry the details button, want 1: the rename must edit, not add", calls)
	}
	// The arguments are not the call's output.
	d, ok := b.details.get(s.detailTokens["c1"])
	if !ok {
		t.Fatal("the call is gone from the store")
	}
	if d.Body != "" {
		t.Fatalf("body = %q, want the output slot to stay empty", d.Body)
	}
}
