//go:build gateway || gateway.max

package max

// The turn's output in one MAX chat.
//
// A turn reads the way a person works through it: first what the agent did -
// tool cards, pictures, documents - and last what it has to say. MAX has no way
// to append to a message and no way to move one down, so the answer is not
// streamed: the sender keeps the text of the whole turn and posts it as the
// last messages of the turn (Flush). A preview posted with the first text would
// stay where it appeared, above every card the turn produced after it.
//
// An answer longer than one message continues in further messages. MAX caps a
// message at 4000 characters (schema.yaml, NewMessageBody.text maxLength) and
// refuses more, so a longer answer is cut into pieces that each fit and none
// of it is dropped; the pieces go out in order as the closing messages.
//
// Buttons are attachments here, not text: a permission request and a question
// with options go out as one inline_keyboard attachment, and a press comes
// back as a message_callback update carrying the payload of the button. A
// press is answered with POST /answers - a button nobody confirms keeps
// spinning for the person who pressed it, whatever the bot decided - and the
// choice is handed to the prompt that waits for it (Bot.HandleCallback).
//
// The rate is the transport's business, not this file's: every write of the
// client is held at two a second by the pacer in client.go.

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/EvilFreelancer/coddy-agent/external/gateway/access"
	"github.com/EvilFreelancer/coddy-agent/external/gateway/sessionstore"
	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

const (
	// maxTextRunes is the text budget of one MAX message: the maxLength of
	// NewMessageBody.text. A longer answer is cut into pieces of this size,
	// never truncated.
	maxTextRunes = 4000
	// flushTimeout bounds the delivery of the final answer.
	flushTimeout = 2 * time.Minute
	// settleTimeout bounds one edit that is not the answer: a prompt being
	// concluded, a refusal being written. A slow API may delay those, never
	// the turn.
	settleTimeout = 5 * time.Second
	// maxButtonsPerRow is how many buttons go into one row of a keyboard.
	maxButtonsPerRow = 3
	// actionAsk is the verb every button payload starts with. One registry
	// serves both kinds of prompt - a permission request and a question with
	// options - and the token that follows tells them apart.
	actionAsk = "ask"
)

// Sender implements acp.UpdateSender for one turn in one chat.
type Sender struct {
	b      *Bot
	ctx    context.Context
	c      *Client
	target Target

	// refuseApprovals is set for the turn of somebody who is not the bot's
	// admin: every permission request is refused, the agent's own included.
	refuseApprovals bool

	// pictures is the chat's session, whose saved copies of the pictures its
	// agent was shown are sent into the chat (pictures.go); nil sends none.
	pictures pictureSession
	// files is the same session, asked what a document needs before it leaves:
	// the working directory, and whether this turn is confined to it
	// (documents.go). nil sends no document.
	files fileSession

	mu          sync.Mutex
	responseBuf strings.Builder
	// detailTokens maps a tool call's id to the token its details button
	// carries, so the output that arrives later lands on the stored call.
	detailTokens map[string]string
	// detailMessages maps a tool call's id to the message the call was posted
	// in, so the command arriving with the call's own update renames that
	// message instead of adding a second one for the same call.
	detailMessages map[string]string
	// afterTool is set when a tool call has run since the last text, so the
	// text that follows starts a paragraph of its own in the closing answer
	// instead of running into the sentence before the call.
	afterTool bool
	// detached is set when the chat this turn writes into is switched to another
	// conversation (/resume): the turn keeps running in its own session, which
	// the web UI still shows, but it stops writing into the chat - no text, no
	// cards, no pictures. A document the person asked for is the exception: it
	// was promised in this chat and still arrives (documents.go).
	detached atomic.Bool
}

// Detach stops the turn from writing into its chat. The turn itself is not
// cancelled: it belongs to its own session, and the session keeps the answer.
func (s *Sender) Detach() { s.detached.Store(true) }

// isDetached reports whether the chat of this turn now belongs to another
// conversation.
func (s *Sender) isDetached() bool { return s.detached.Load() }

// sessionID names the session of this turn, for the log.
func (s *Sender) sessionID() string {
	if s.files == nil {
		return ""
	}

	return s.files.GetID()
}

var _ acp.UpdateSender = (*Sender)(nil)

// newSender is the sender of one turn in one chat. Step 5 hands it to the
// session manager; nothing calls it from Start.
func (b *Bot) newSender(ctx context.Context, c *Client, target Target) *Sender {
	return &Sender{b: b, ctx: ctx, c: c, target: target}
}

// SendSessionUpdate handles the agent's streaming events.
func (s *Sender) SendSessionUpdate(sessionID string, update interface{}) error {
	if s.isDetached() {
		// The chat was switched to another conversation while this turn ran
		// (/resume). Everything the turn says belongs to its own session now,
		// where the web UI shows it; only a document still reaches this chat,
		// because it was asked for here (documents.go).
		if u, ok := update.(acp.ToolCallStatusUpdate); ok && u.Status == "completed" {
			s.sendFiles(sessionID, session.ToolFilesFromMeta(u.Meta))
		}

		return nil
	}

	switch u := update.(type) {
	case acp.BackgroundWakeUpdate:
		// A turn nobody typed opens with what woke the agent.
		s.post("🔔 " + session.BackgroundWakeNote(u))
	case acp.GoalTurnUpdate:
		// The supervisor starts another turn of the goal. The note is what the
		// chat hears about it; the text this turn has written goes out with
		// the answer, below the note, when the turn ends.
		s.post("🎯 " + session.GoalTurnNote(u))
	case acp.SessionGoalUpdate:
		// How the goal ended, or where it stands, said once per change.
		if note := session.GoalEndNote(u); note != "" {
			s.post("🎯 " + note)
		}
	case acp.MessageChunkUpdate:
		if u.Content.Type != acp.ContentTypeText || u.SessionUpdate == acp.UpdateTypeUserMessageChunk {
			return nil
		}
		s.mu.Lock()
		if s.afterTool && s.responseBuf.Len() > 0 {
			// The text that follows a tool call opens a paragraph of its own:
			// the whole turn is delivered as one closing answer, and without
			// the break the sentences around the call would run together.
			s.responseBuf.WriteString("\n\n")
		}
		s.afterTool = false
		s.responseBuf.WriteString(u.Content.Text)
		s.mu.Unlock()
	case acp.ToolCallUpdate:
		title := u.Title
		if title == "" {
			title = u.Kind
		}
		if title == "" {
			return nil
		}
		token := s.b.details.put(toolDetail{Title: title})
		s.mu.Lock()
		if s.detailTokens == nil {
			s.detailTokens = make(map[string]string)
		}
		s.detailTokens[u.ToolCallID] = token
		s.afterTool = true
		s.mu.Unlock()
		// The call gets a message of its own with the button that opens it: the
		// closing answer would take the button with it.
		if mid := s.postDetails(token, title); mid != "" {
			s.mu.Lock()
			if s.detailMessages == nil {
				s.detailMessages = make(map[string]string)
			}
			s.detailMessages[u.ToolCallID] = mid
			s.mu.Unlock()
		}
	case acp.ToolCallStatusUpdate:
		s.mu.Lock()
		token := s.detailTokens[u.ToolCallID]
		mid := s.detailMessages[u.ToolCallID]
		s.mu.Unlock()
		// The update that starts a call carries its arguments: that is what the
		// call is called by, and it is not the call's output.
		if u.Status == "in_progress" {
			if command, ok := callArguments(u.Content); ok && s.b.details.setCommand(token, command) {
				s.renameDetails(token, mid)
			}
		} else if body := toolCallBody(u.Content); body != "" {
			// What the call returned goes into the entry its button opens. The
			// output arrives here and not with the call itself, which is why the
			// message is posted first and filled in later.
			s.b.details.setBody(token, body)
		}
		// A finished call that showed the model pictures puts them in the
		// chat as it happened, not with the answer (pictures.go). The
		// pictures travel in the update's meta, which is why this case
		// carries them and the one above does not.
		if u.Status == "completed" {
			s.sendPictures(sessionID, session.ToolImagesFromMeta(u.Meta))
			// A finished call that produced a document sends it the same way,
			// by the artefact id in the same _meta (documents.go).
			s.sendFiles(sessionID, session.ToolFilesFromMeta(u.Meta))
		}
	}

	return nil
}

// outgoingAnswer is answer text as a chat can show it: the placement markers of
// shared files exist for the web UI, which draws the artifact card where the
// marker stands, and no messenger renders them. The strip runs on the whole text
// at once, never on each accumulated chunk - a strip trims whitespace, and doing
// it per chunk would eat the spaces and newlines between chunks and glue words
// together. The stored message keeps its markers: only the way out is stripped.
func outgoingAnswer(text string) string {
	return session.StripAssistantContent(text, false, false)
}

// post sends a message of its own: a note that does not belong to the answer.
func (s *Sender) post(text string) {
	if s.isDetached() {
		return
	}

	if _, err := s.c.Send(s.ctx, Outgoing{Target: s.target, Text: text}); err != nil {
		s.b.log.Warn("max: message not delivered", "err", err, "target", s.target)
	}
}

// Flush posts the answer of the turn as its closing messages, continued in
// further messages when it does not fit in one. Everything the turn did went
// out before this, so the answer is the last thing the chat receives.
func (s *Sender) Flush() {
	s.mu.Lock()
	text := outgoingAnswer(s.responseBuf.String())
	s.responseBuf.Reset()
	s.afterTool = false
	s.mu.Unlock()

	if s.isDetached() {
		// The answer stays in the session: the chat belongs to another
		// conversation now, and writing it here would mix two of them.
		if strings.TrimSpace(text) != "" {
			s.b.log.Info("max: answer kept in its session, the chat moved on",
				"session", s.sessionID(), "runes", len([]rune(text)))
		}

		return
	}

	// The answer is delivered even when the turn's own context ended - a turn
	// cut off by its timeout still owes the chat what it wrote.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(s.ctx), flushTimeout)
	defer cancel()
	if strings.TrimSpace(text) == "" {
		return
	}
	for _, chunk := range splitText(text, maxTextRunes) {
		s.deliver(ctx, chunk)
	}
}

// deliver posts one piece of the answer. The pieces go out in order, one after
// another, so the closing answer reads the way the turn wrote it.
func (s *Sender) deliver(ctx context.Context, text string) {
	if _, err := s.c.Send(ctx, Outgoing{Target: s.target, Text: text, Format: "markdown"}); err != nil {
		s.b.log.Warn("max: answer not delivered", "err", err, "target", s.target)
	}
}

// RequestPermission asks the chat about a tool call of a subagent, with one
// button per option, and waits for the press. What the chat's own agent asks is
// allowed without asking: the operator who configured the bot decided that,
// which is what the mode stamped on the request says.
func (s *Sender) RequestPermission(ctx context.Context, params acp.PermissionRequestParams) (*acp.PermissionResult, error) {
	if s.refuseApprovals {
		return &acp.PermissionResult{Outcome: "cancelled", OptionID: "reject"}, nil
	}
	if mode := strings.TrimSpace(params.EffectivePermissionMode); mode == "" || mode == config.PermModeBypass {
		return &acp.PermissionResult{Outcome: "allow", OptionID: "allow"}, nil
	}
	refuse := &acp.PermissionResult{Outcome: "cancelled", OptionID: "reject"}
	if len(params.Options) == 0 {
		return refuse, nil
	}
	labels := make([]string, len(params.Options))
	for i, opt := range params.Options {
		labels[i] = optionText(opt.Name, opt.OptionID, i)
	}
	chosen, ok := s.askOptions(ctx, permissionText(params), labels)
	if !ok {
		return refuse, nil
	}

	return &acp.PermissionResult{Outcome: "selected", OptionID: params.Options[chosen].OptionID}, nil
}

// RequestQuestion posts the question and, when it offers options, waits for one
// of them to be pressed. A question without options has no form here: it goes
// out as text and the person answers with their next message, which is why such
// a question contributes no answer.
func (s *Sender) RequestQuestion(ctx context.Context, params acp.QuestionRequestParams) (*acp.QuestionResult, error) {
	if len(params.Questions) == 0 {
		return &acp.QuestionResult{}, nil
	}
	answers := make([][]string, 0, len(params.Questions))
	for _, q := range params.Questions {
		text := questionText(q)
		if len(q.Options) == 0 {
			s.post(text)
			answers = append(answers, nil)
			continue
		}
		names := make([]string, len(q.Options))
		for i, opt := range q.Options {
			names[i] = optionText(opt.Label, "", i)
		}
		// The options are listed in the message and the buttons carry their
		// letters: a platform cuts a button's label down to a word, so the text
		// of an option has to live where nothing cuts it.
		got, ok := s.askChoice(ctx, listedQuestion(text, names), optionLetters(len(names)), names, true)
		if !ok {
			answers = append(answers, nil)
			continue
		}
		if got.text != "" {
			answers = append(answers, []string{got.text})
			continue
		}
		answers = append(answers, []string{names[got.index]})
	}

	return &acp.QuestionResult{Answers: answers}, nil
}

// askOptions posts text with one button per option and waits for a press. It
// answers with the place of the pressed option and true, or -1 and false when
// nobody pressed anything before the turn ended. The message is settled either
// way: it says how the prompt ended and loses its keyboard, so no button of a
// finished prompt can be pressed again.
func (s *Sender) askOptions(ctx context.Context, text string, options []string) (int, bool) {
	got, ok := s.askChoice(ctx, text, options, options, false)
	if !ok || got.ask || got.text != "" {
		return -1, false
	}

	return got.index, true
}

// askChoice is askOptions for a prompt whose buttons are shorter than the options
// they stand for: the settled message names the chosen option in full, so the
// note does not say "✅ B". With written set it also offers to take the answer as
// a written message: the browser has a field for that, and the chat must not be
// poorer than the browser.
func (s *Sender) askChoice(ctx context.Context, text string, buttons, spoken []string, written bool) (pressAnswer, bool) {
	// A prompt that is already out of date is not posted at all: the answer
	// arrived before it went out, so there is nothing left to ask.
	if ctx.Err() != nil {
		return pressAnswer{}, false
	}
	token, answers := s.b.asked.offer(s.target, buttons)
	defer s.b.asked.drop(token)
	// The prompt itself goes out on a context of its own: the answer may arrive
	// while the message is still on the wire, and a send cut in the middle may
	// well reach the chat - leaving a keyboard nobody can take away, because the
	// id that would take it never came back.
	sendCtx, sendCancel := context.WithTimeout(context.WithoutCancel(s.ctx), settleTimeout)
	defer sendCancel()
	rows := optionRows(token, buttons)
	if written {
		rows = append(rows, customAnswerRow(token))
	}
	mid, err := s.c.Send(sendCtx, Outgoing{Target: s.target, Text: text, Buttons: rows})
	if err != nil {
		s.b.log.Warn("max: prompt not delivered", "err", err, "target", s.target)

		return pressAnswer{}, false
	}
	for {
		select {
		case got := <-answers:
			switch {
			case got.drop:
				s.settle(mid, text, endedNote(ctx))

				return pressAnswer{}, false
			case got.ask:
				// The person took the card, not an option: the prompt stays and
				// says what is expected, and their next text is the answer.
				s.settle(mid, text, customAnswerNote)
			case got.text != "":
				s.settle(mid, text, "✅ "+shortenRunes(got.text, answerNoteRunes))

				return got, true
			default:
				s.settle(mid, text, "✅ "+spoken[got.index])

				return got, true
			}
		case <-ctx.Done():
			s.settle(mid, text, endedNote(ctx))

			return pressAnswer{}, false
		case <-s.ctx.Done():
			s.settle(mid, text, endedNote(ctx))

			return pressAnswer{}, false
		}
	}
}

// The button that takes the answer as a written message, what the card says while
// it waits for one, and the payload that names the button among the options.
const (
	customAnswerButton = "✍️ Свой ответ"
	customAnswerNote   = "✍️ Свой ответ: напишите его следующим сообщением."
	customAnswerChoice = "custom"
	// answerNoteRunes caps what the settled card repeats of a written answer: the
	// card already shows the question, and MAX refuses a message of over 4000.
	answerNoteRunes = 400
)

// customAnswerRow is the row under the options that turns the prompt into one
// waiting for a written answer.
func customAnswerRow(token string) []Button {
	return []Button{{Text: customAnswerButton, Payload: actionAsk + ":" + token + ":" + customAnswerChoice}}
}

// listedQuestion puts every option under the question, so the whole text of an
// option is readable where the platform cuts a button's own label down to a word.
func listedQuestion(text string, options []string) string {
	letters := optionLetters(len(options))
	lines := make([]string, len(options))
	for i, option := range options {
		lines[i] = letters[i] + " — " + option
	}

	return shortenRunes(text+"\n\n"+strings.Join(lines, "\n"), maxTextRunes)
}

// optionLetters names the options A, B, C ... and numbers them past the alphabet,
// so a prompt of any size still has a button per option.
func optionLetters(n int) []string {
	letters := make([]string, n)
	for i := range letters {
		if i < 26 {
			letters[i] = string(rune('A' + i))
			continue
		}
		letters[i] = strconv.Itoa(i + 1)
	}

	return letters
}

// endedNote says how a prompt nobody pressed ended. A prompt withdrawn because
// the same question was answered on another surface says so: "Больше не жду"
// would read as if the person had let it pass.
func endedNote(ctx context.Context) string {
	if errors.Is(context.Cause(ctx), session.ErrAnsweredElsewhere) {
		return "✅ Ответ дан в другом окне."
	}

	return "Больше не жду."
}

// settle ends a prompt: the message keeps its text, says how it ended and
// loses its keyboard (an empty attachments list is how an edit takes one away).
func (s *Sender) settle(messageID, text, note string) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(s.ctx), settleTimeout)
	defer cancel()
	if err := s.c.EditMessage(ctx, messageID, text+"\n\n"+note, [][]Button{}); err != nil {
		s.b.log.Debug("max: prompt message not settled", "err", err, "message_id", messageID)
	}
}

// HandleCallback answers a button press: it confirms the press, so the button
// stops spinning for whoever pressed it, and hands the choice to the prompt
// that waits for it. A model-keyboard press changes the session's model and
// edits the menu in place. A press that belongs to no prompt and no model
// menu - a stale message whose turn has ended, a payload this bot never wrote
// - is confirmed too: a button the bot will not act on must not keep the
// keyboard of the messenger waiting.
//
// Step 5 calls this from the poll loop; nothing calls it from Start.
func (b *Bot) HandleCallback(ctx context.Context, c *Client, u Update) error {
	cb, err := u.Pressed()
	if err != nil {
		return err
	}
	if strings.TrimSpace(cb.CallbackID) == "" {
		return fmt.Errorf("max: %s update carries no callback_id", u.UpdateType)
	}
	// Access is checked again here, not only where the message came in: a
	// keyboard outlives the turn that drew it, and a person whose access was
	// withdrawn in between must not go on pressing yesterday's buttons. The
	// press is still confirmed, so the messenger's keyboard stops spinning -
	// it is only not acted on.
	if in := b.callbackInbound(u, cb); !b.allowed(in.chatID, in.userID, in.isGroup) {
		return c.AnswerCallback(ctx, cb.CallbackID, "")
	}

	action, payload, ok := strings.Cut(cb.Payload, ":")
	if ok && action == callbackActionModel && payload != "" {
		note, err := b.handleModelAction(ctx, c, u, cb)
		if err != nil {
			b.log.Debug("max: model action failed", "err", err)
		}
		return c.AnswerCallback(ctx, cb.CallbackID, note)
	}
	// A resume tap redraws the menu or switches the chat to another session,
	// and the message it edits says what happened: the press is confirmed
	// without a notification over it.
	if ok && action == callbackActionResume && payload != "" {
		if err := b.handleResumeAction(ctx, c, u, payload); err != nil {
			b.log.Debug("max: resume action failed", "err", err)
		}
		return c.AnswerCallback(ctx, cb.CallbackID, "")
	}

	// A details press opens a tool call in the message that carries it, or
	// turns its pages: the message itself says what happened, so the press is
	// confirmed without a notification over it.
	if ok && action == callbackActionDetails && payload != "" {
		if err := b.handleDetailsAction(ctx, c, u, payload); err != nil {
			b.log.Debug("max: details action failed", "err", err)
		}

		return c.AnswerCallback(ctx, cb.CallbackID, "")
	}

	note, _ := b.resolvePress(u, cb)

	return c.AnswerCallback(ctx, cb.CallbackID, note)
}

// handleDetailsAction opens a tool call into the message that carries it, turns
// its pages, or folds it back to one line. The message is edited in place, so
// the chat keeps one message per call instead of one per page it was read on.
func (b *Bot) handleDetailsAction(ctx context.Context, c *Client, u Update, payload string) error {
	token, pagePart, _ := strings.Cut(payload, ":")
	page := 0
	if pagePart != "" {
		n, err := strconv.Atoi(pagePart)
		if err != nil {
			return fmt.Errorf("max: details payload %q carries no page: %w", payload, err)
		}
		page = n
	}
	d, ok := b.details.get(token)
	if !ok {
		return fmt.Errorf("max: details %q are gone", token)
	}
	mid := u.MessageKey()
	if strings.TrimSpace(mid) == "" {
		return fmt.Errorf("max: details press carries no message id")
	}
	text, buttons := detailsView(token, d, page)

	return c.EditMessage(ctx, mid, text, buttons)
}

// handleModelAction resolves and applies a model-button press. It returns the
// notification shown to the presser, or an error if the update is incomplete.
func (b *Bot) handleModelAction(ctx context.Context, c *Client, u Update, cb Callback) (string, error) {
	from := pressedBy(u, cb)
	if from.UserID == 0 {
		return "", fmt.Errorf("max: model callback has no user")
	}

	chatID := u.ChatID
	if chatID == 0 {
		m, err := u.Body()
		if err == nil {
			chatID = m.Recipient.ChatID
		}
	}

	// Load the message body to learn whether the chat is a group. A callback
	// update may carry the original message; if it does not, treat it as a
	// direct chat - the message_id still lets us edit the menu.
	isGroup := false
	if m, err := u.Body(); err == nil && m.Recipient.ChatType != "dialog" {
		isGroup = true
	}

	// The model and the session are the admins' to change in a group - and the
	// model is theirs in a personal chat too: what the session runs on decides
	// what every later turn costs, and the chat of the plain user was the one
	// place where the menu still opened (29.10.2026).
	if !b.cfg.IsAdmin(from.UserID) {
		b.log.Debug("max: callback refused", "reason", "admin-only", "action", callbackActionModel, "user", from.UserID, "chat", chatID)
		return adminOnlyNote, nil
	}

	isolation := access.EffectiveIsolation(chatID, b.cfg)
	key := sessionstore.SessionKey(adapterName, chatID, from.UserID, isolation, isGroup)
	st, err := b.ensureSession(ctx, key)
	if err != nil {
		b.log.Warn("max: callback session", "err", err, "user", from.UserID, "chat", chatID)
		return "❌ Session error: " + err.Error(), nil
	}

	b.log.Debug("max: callback",
		"action", callbackActionModel,
		"payload", cb.Payload,
		"session", st.GetID(),
		"user", from.UserID,
		"chat", chatID,
	)

	note, _, err := b.handleModelCallback(ctx, c, u, cb, st)
	return note, err
}

// resolvePress gives a press to the prompt behind its payload and answers with
// the notification the presser sees: the label of the button that was pressed,
// or "" when the press belongs to no prompt that still waits.
func (b *Bot) resolvePress(u Update, cb Callback) (string, bool) {
	from := pressedBy(u, cb).UserID
	action, rest, ok := strings.Cut(cb.Payload, ":")
	if !ok || action != actionAsk {
		b.log.Debug("max: press from no prompt", "payload", cb.Payload, "user", from)

		return "", false
	}
	token, digits, ok := strings.Cut(rest, ":")
	if !ok {
		return "", false
	}
	if digits == customAnswerChoice {
		if !b.asked.arm(token, from) {
			b.log.Debug("max: press refused", "payload", cb.Payload, "user", from)

			return "", false
		}
		b.log.Debug("max: prompt waits for a written answer", "user", from)

		return customAnswerButton, true
	}
	index, err := strconv.Atoi(digits)
	if err != nil {
		return "", false
	}
	label, ok := b.asked.resolve(token, index, from)
	if !ok {
		b.log.Debug("max: press refused", "payload", cb.Payload, "user", from)

		return "", false
	}
	b.log.Debug("max: press answered", "user", from, "choice", label)

	return "✅ " + label, true
}

// pressedBy is the person who pressed a button: MAX puts them in the callback
// itself. The update's own user is the fallback, and the message the keyboard
// sits on is no help at all - the bot wrote it, so a press read from the
// message came back as the bot and every press was refused as somebody else's.
func pressedBy(u Update, cb Callback) User {
	if cb.User.UserID != 0 {
		return cb.User
	}

	return u.From()
}

// presses holds the prompts waiting for a button press, keyed by the token
// their buttons carry. The zero value is ready to use, so a Bot carries one
// without a constructor.
type presses struct {
	mu      sync.Mutex
	pending map[string]*press
}

// pressAnswer is what a prompt got: the place of the pressed option, a request
// for a written answer, the written answer itself, or the end of the wait when
// nobody will answer any more.
type pressAnswer struct {
	index int    // the pressed option, -1 when nobody pressed one
	text  string // what the person wrote, when they wrote it
	ask   bool   // the person asked to write the answer instead of pressing
	drop  bool   // the wait is over: a command took the person away from it
}

// press is one prompt waiting for its answer: who it was asked of, the options
// it offered, whether it now waits for a written answer, and the channel its
// answer arrives on.
type press struct {
	target  Target
	options []string
	written bool
	answer  chan pressAnswer
}

// offer registers a prompt under a fresh token and returns the token and the
// channel the answer arrives on.
func (p *presses) offer(target Target, options []string) (string, chan pressAnswer) {
	token := newPromptToken()
	answers := make(chan pressAnswer, 1)
	p.mu.Lock()
	if p.pending == nil {
		p.pending = make(map[string]*press)
	}
	p.pending[token] = &press{target: target, options: options, answer: answers}
	p.mu.Unlock()

	return token, answers
}

// drop withdraws a prompt nobody waits for any more.
func (p *presses) drop(token string) {
	p.mu.Lock()
	delete(p.pending, token)
	p.mu.Unlock()
}

// resolve hands a press to the prompt behind token and answers with the label
// of the option it chose. It refuses a token nobody waits for, an option the
// prompt never offered, a prompt already answered, and a press from somebody
// other than the person a private prompt was asked of; a press in a shared
// chat is anybody's, since the session there is shared.
func (p *presses) resolve(token string, index int, userID int64) (string, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	e := p.pending[token]
	if e == nil || index < 0 || index >= len(e.options) {
		return "", false
	}
	if e.target.UserID != 0 && e.target.UserID != userID {
		return "", false
	}
	select {
	case e.answer <- pressAnswer{index: index}:
	default:
		return "", false
	}
	delete(p.pending, token)

	return e.options[index], true
}

// arm turns a prompt into one waiting for a written answer: the person pressed
// the button that says they will write it, and the prompt stays behind to take
// that message.
func (p *presses) arm(token string, userID int64) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	e := p.pending[token]
	if e == nil || e.written {
		return false
	}
	if e.target.UserID != 0 && e.target.UserID != userID {
		return false
	}
	select {
	case e.answer <- pressAnswer{ask: true}:
	default:
		return false
	}
	e.written = true

	return true
}

// armed reports whether this person was told to write the answer to a prompt.
func (p *presses) armed(userID int64) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, e := range p.pending {
		if e.written && (e.target.UserID == 0 || e.target.UserID == userID) {
			return true
		}
	}

	return false
}

// disarm withdraws the wait for a written answer: a command is never an answer,
// and the prompt a person walked away from must not swallow their next question.
func (p *presses) disarm(userID int64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for token, e := range p.pending {
		if e.written && (e.target.UserID == 0 || e.target.UserID == userID) {
			// The question has to end with the wait: nothing else would ever
			// arrive on its channel, and the turn would stand there until it
			// timed out.
			select {
			case e.answer <- pressAnswer{drop: true}:
			default:
			}
			delete(p.pending, token)
		}
	}
}

// write hands the message a person typed to the prompt that waits for it. It
// answers false when no prompt of this person waits for a written answer, and
// the message goes on to be an ordinary one.
func (p *presses) write(userID int64, text string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	for token, e := range p.pending {
		if !e.written {
			continue
		}
		if e.target.UserID != 0 && e.target.UserID != userID {
			continue
		}
		select {
		case e.answer <- pressAnswer{text: text}:
		default:
			return false
		}
		delete(p.pending, token)

		return true
	}

	return false
}

// newPromptToken is the unguessable key of one prompt. A press carries it back
// and a token nobody waits for is refused, so it is drawn from the system
// random source rather than counted.
func newPromptToken() string {
	var b [8]byte
	_, _ = rand.Read(b[:])

	return hex.EncodeToString(b[:])
}

// optionRows lays options out as an inline keyboard, one button per option and
// at most maxButtonsPerRow in a row. Every payload names the prompt and the
// place of the option, which is what a press is resolved with.
func optionRows(token string, options []string) [][]Button {
	var rows [][]Button
	var row []Button
	for i, label := range options {
		row = append(row, Button{Text: label, Payload: actionAsk + ":" + token + ":" + strconv.Itoa(i)})
		if len(row) == maxButtonsPerRow {
			rows = append(rows, row)
			row = nil
		}
	}
	if len(row) > 0 {
		rows = append(rows, row)
	}

	return rows
}

// optionText names one option: its own name when it has one, the fallback
// otherwise. A button cannot be empty - MAX would refuse the keyboard - and an
// option nobody named is better pressed by its place than not at all.
func optionText(name, fallback string, index int) string {
	if text := strings.TrimSpace(name); text != "" {
		return text
	}
	if text := strings.TrimSpace(fallback); text != "" {
		return text
	}

	return fmt.Sprintf("Вариант %d", index+1)
}

// permissionText is what the chat is asked about.
func permissionText(params acp.PermissionRequestParams) string {
	title := strings.TrimSpace(params.ToolCall.Title)
	if title == "" {
		title = "Субагент просит разрешение"
	}
	text := "🔐 " + title
	for _, c := range params.ToolCall.Content {
		if body := strings.TrimSpace(c.Content.Text); body != "" {
			if r := []rune(body); len(r) > permissionBodyRunes {
				body = string(r[:permissionBodyRunes]) + "…"
			}
			text += "\n\n" + body
			break
		}
	}

	return text
}

// permissionBodyRunes is how much of a tool call's body the prompt quotes, so
// the message has room for its own text and for the keyboard below it.
const permissionBodyRunes = 3000

// questionText is what a question says: its header, when it has one, above the
// question itself.
func questionText(q acp.QuestionPrompt) string {
	text := strings.TrimSpace(q.Question)
	if header := strings.TrimSpace(q.Header); header != "" {
		text = header + "\n" + text
	}

	return text
}

// previewText is the tail of the answer, as the live message shows it.
func previewText(text string, limit int) string {
	text = strings.TrimSpace(text)
	if utf8.RuneCountInString(text) <= limit {
		return text
	}
	r := []rune(text)

	return "…" + string(r[len(r)-limit:])
}

// splitText cuts text into pieces of at most limit runes - the size one MAX
// message takes - preferring a paragraph break, then a line break, then a
// space, and never cutting a rune in two. The pieces together hold the whole
// text: an answer that does not fit in one message continues in the next
// instead of being truncated.
func splitText(text string, limit int) []string {
	if limit <= 0 {
		limit = maxTextRunes
	}
	if utf8.RuneCountInString(text) <= limit {
		return []string{text}
	}
	var pieces []string
	rest := text
	for utf8.RuneCountInString(rest) > limit {
		r := []rune(rest)
		cut := lastBreak(r[:limit])
		head := strings.TrimRight(string(r[:cut]), " \t\n")
		tail := strings.TrimLeft(string(r[cut:]), "\n")
		if head == "" {
			// A run of text with no break in it at all: cut at the limit.
			head, tail = string(r[:limit]), string(r[limit:])
		}
		pieces = append(pieces, head)
		rest = tail
	}
	if rest != "" {
		pieces = append(pieces, rest)
	}

	return pieces
}

// lastBreak is where a piece ends: the last paragraph break, line break or
// space in the run up to the limit - never before its middle, so a piece is
// not cut shorter than it has to be - or the limit itself when the text has no
// break in it.
func lastBreak(r []rune) int {
	s := string(r)
	for _, sep := range []string{"\n\n", "\n", " "} {
		if i := strings.LastIndex(s, sep); i > len(s)/2 {
			return utf8.RuneCountInString(s[:i])
		}
	}

	return len(r)
}
