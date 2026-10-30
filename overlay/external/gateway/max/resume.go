//go:build gateway || gateway.max

package max

// /resume: the list of conversations a chat can switch to, and the switch
// itself.
//
// A chat is bound to one session, and /clear replaces it with a fresh one: the
// session it left stays on disk, out of the chat's reach. /resume is the way
// back. Alone it offers the sessions the server keeps as a keyboard of
// titles, newest first; with a query it resumes the one the words name - by
// id, by a prefix of it or by a fragment of the title - and offers the
// keyboard when several do. The choice rewrites the same mapping /clear
// writes, so it survives a restart of the gateway.
//
// The session left behind stays loaded: /resume is a switch, not an ending.
// /clear is the command that says the conversation is over.

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/EvilFreelancer/coddy-agent/external/gateway/access"
	"github.com/EvilFreelancer/coddy-agent/external/gateway/sessionstore"
	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

const (
	callbackActionResume = "resume"

	// resumePickMarker and resumePageMarker tell a tap on a session from a
	// tap on the navigation row inside the callback payload.
	resumePickMarker = "s"
	resumePageMarker = "p"

	// resumePageSize is how many sessions one keyboard page carries: about
	// what a phone screen shows without scrolling.
	resumePageSize = 8

	// resumeLabelMaxRunes caps a button label. MAX cuts a longer one itself,
	// at a width that depends on the device; cutting here ends the label with
	// an ellipsis instead of mid-word.
	resumeLabelMaxRunes = 32

	// noteTurnBusy is the line a resume adds when the session it opens is
	// running a turn: the switch itself is safe, the message sent next waits.
	noteTurnBusy = "в беседе идёт работа — продолжит, когда закончит"
)

// resumePickPrefix precedes the session in the payload of a session button.
const resumePickPrefix = callbackActionResume + ":" + resumePickMarker + ":"

// sessionLister is the part of the session manager /resume needs. It is
// claimed by a type assertion rather than added to SessionRunner, so a runner
// that only runs turns keeps working and the menu says it cannot list.
type sessionLister interface {
	HandleSessionList(context.Context, acp.SessionListParams) (*acp.SessionListResult, error)
}

// sessionOriginReader reports where a session was started without loading it
// (session.Manager.SessionOrigin).
type sessionOriginReader interface {
	SessionOrigin(string) string
}

// turnActiveChecker reports whether a session runs a turn in this process
// (session.Manager.SessionTurnActiveInProcess).
type turnActiveChecker interface {
	SessionTurnActiveInProcess(string) bool
}

// errSessionListUnavailable says the runner cannot list sessions at all, which
// is a different answer from a listing that failed.
var errSessionListUnavailable = errors.New("session list unavailable")

// handleResume lists the conversations to switch to, or switches to the one a
// query names.
func (b *Bot) handleResume(ctx context.Context, in inbound, key, args string) {
	c := b.connectedClient()
	if c == nil {
		return
	}
	rows, err := b.listSessions(ctx)
	switch {
	case errors.Is(err, errSessionListUnavailable):
		b.log.Debug("max: resume unavailable", "user", in.userID, "chat", in.chatID)
		b.resumeReply(ctx, c, in, "Список бесед недоступен: этот шлюз не умеет их перечислять.")
		return
	case err != nil:
		b.log.Warn("max: resume list", "err", err, "user", in.userID, "chat", in.chatID)
		b.resumeReply(ctx, c, in, "❌ Не удалось получить список бесед: "+err.Error())
		return
	case len(rows) == 0:
		b.resumeReply(ctx, c, in, "Бесед пока нет.")
		return
	}

	current := b.store.Peek(key)
	query := strings.TrimSpace(args)
	if query == "" {
		b.log.Debug("max: resume menu", "session", current, "sessions", len(rows), "page", 0)
		b.sendResumeMenu(ctx, c, in, b.resumeMenu(rows, current, 0, ""))
		return
	}

	matches := matchResumeSessions(rows, query)
	b.log.Debug("max: resume query", "query", query, "matches", len(matches), "session", current)
	switch len(matches) {
	case 0:
		b.resumeReply(ctx, c, in, "🔍 Не нашёл «"+query+"». Пришлите /resume без слов, чтобы выбрать из списка.")
	case 1:
		text, _ := b.resumeSessionRow(ctx, in, matches[0])
		b.resumeReply(ctx, c, in, text)
	default:
		b.sendResumeMenu(ctx, c, in, b.resumeMenu(matches, current, 0, query))
	}
}

// handleResumeAction answers a tap on the resume keyboard: a page turn redraws
// the menu in place, a session pick binds the chat and replaces the menu with
// the confirmation, so the keyboard does not outlive the choice.
func (b *Bot) handleResumeAction(ctx context.Context, c *Client, u Update, rest string) error {
	kind, value, ok := strings.Cut(rest, ":")
	if !ok || value == "" {
		b.log.Debug("max: resume callback ignored", "reason", "unrecognised payload", "payload", rest)
		return nil
	}
	cb, err := u.Pressed()
	if err != nil {
		return err
	}
	in := b.callbackInbound(u, cb)
	key := b.sessionKey(in)

	// The resume menu lists every session the bot has and binds the chat to
	// the one that is pressed, so the buttons are the admins' on every surface,
	// not only in a group: a member who presses one would move the chat to
	// somebody else's history from the next turn on.
	if !b.cfg.IsAdmin(in.userID) {
		b.log.Debug("max: resume callback refused", "reason", "resume is admin-only", "user", in.userID, "chat", in.chatID)
		b.resumeReply(ctx, c, in, adminOnlyNote)
		return nil
	}

	switch kind {
	case resumePageMarker:
		page, err := strconv.Atoi(value)
		if err != nil {
			b.log.Debug("max: resume callback ignored", "reason", "unrecognised page", "payload", rest)
			return nil
		}
		rows, err := b.listSessions(ctx)
		if err != nil {
			b.log.Warn("max: resume list", "err", err, "user", in.userID, "chat", in.chatID)
			b.resumeReply(ctx, c, in, "❌ Не удалось получить список бесед: "+err.Error())
			return nil
		}
		menu := b.resumeMenu(rows, b.store.Peek(key), page, "")
		b.log.Debug("max: resume menu", "session", b.store.Peek(key), "sessions", len(rows), "page", page)
		mid := u.MessageKey()
		if mid == "" {
			b.log.Debug("max: resume menu not editable", "reason", "no message id")
			return nil
		}
		if err := c.EditMessage(ctx, mid, menu.text, menu.buttons); err != nil {
			b.log.Debug("max: edit resume menu", "err", err)
			return err
		}
		return nil

	case resumePickMarker:
		rows, err := b.listSessions(ctx)
		if err == nil {
			if row, ok := resolveResumeCallback(rows, value); ok {
				b.log.Debug("max: resume callback",
					"action", callbackActionResume,
					"value", row.SessionID,
					"user", in.userID,
					"chat", in.chatID,
				)
				// The confirmation replaces the menu, keyboard included, so
				// nothing under it keeps offering a choice already made.
				b.confirmResume(ctx, c, u, in, row)
				return nil
			}
		}
		// A digest payload names a session the listing no longer holds; an id
		// travels as itself, and switching to it is still worth trying.
		if strings.HasPrefix(value, callbackDigestMarker) {
			b.log.Warn("max: resume callback unknown", "payload", value, "user", in.userID, "chat", in.chatID)
			b.resumeReply(ctx, c, in, "❌ Такой беседы больше нет.")
			return nil
		}
		b.resumeSession(ctx, c, in, value)
		return nil
	}

	b.log.Debug("max: resume callback ignored", "reason", "unrecognised payload", "payload", rest)
	return nil
}

// confirmResume writes the confirmation of a switch into the message the tap
// came from, so the menu and the choice do not linger as two messages. A press
// that carries no message to rewrite is answered with a message of its own.
func (b *Bot) confirmResume(ctx context.Context, c *Client, u Update, in inbound, row acp.SessionListInfo) {
	text, buttons := b.resumeSessionRow(ctx, in, row)
	if text == "" {
		return
	}
	mid := u.MessageKey()
	if mid == "" {
		b.resumeReply(ctx, c, in, text)
		return
	}
	if err := c.EditMessage(ctx, mid, text, buttons); err != nil {
		b.log.Debug("max: edit resume confirmation", "err", err)
		b.resumeReply(ctx, c, in, text)
	}
	if b.store.Peek(b.sessionKey(in)) == row.SessionID {
		b.postHistoryCard(ctx, c, in, row.SessionID, b.resumeLabel(row))
	}
}

// resumeSession switches the chat of in to id, when only the id is known.
func (b *Bot) resumeSession(ctx context.Context, c *Client, in inbound, id string) {
	row := acp.SessionListInfo{SessionID: strings.TrimSpace(id)}
	// The label carries the title, which an id alone does not: look the row up
	// when the listing can be read, and switch to the id without it otherwise.
	if rows, err := b.listSessions(ctx); err == nil {
		for _, r := range rows {
			if r.SessionID == row.SessionID {
				row = r
				break
			}
		}
	}
	text, _ := b.resumeSessionRow(ctx, in, row)
	if text != "" {
		b.resumeReply(ctx, c, in, text)
	}
	// The chat keeps every message it had, so the card below is the only thing
	// that says which conversation is now on the other side of it.
	if b.store.Peek(b.sessionKey(in)) == row.SessionID {
		b.postHistoryCard(ctx, c, in, row.SessionID, b.resumeLabel(row))
	}
}

// resumeSessionRow switches the chat of in to row and returns what the chat is
// now on, with a keyboard that takes the menu away. It posts nothing: the
// caller sends the text as a message of its own or writes it into the message
// the choice came from, so a tap on a menu leaves one message behind and not
// two.
//
// The session is loaded first: a bundle that cannot be read is reported here,
// and the mapping is left as it was, so the chat keeps the conversation it
// had.
func (b *Bot) resumeSessionRow(ctx context.Context, in inbound, row acp.SessionListInfo) (string, [][]Button) {
	id := row.SessionID
	if id == "" {
		return "❌ Беседа не названа.", nil
	}
	key := b.sessionKey(in)
	old := b.store.Peek(key)
	if old == id {
		return "▶️ Эта беседа уже открыта: " + b.resumeLabel(row) + "\n" + id, [][]Button{}
	}

	st, err := b.runner.EnsureHTTPSession(ctx, id, b.cwd)
	if err != nil {
		b.log.Warn("max: resume session", "err", err, "session", id, "user", in.userID, "chat", in.chatID)
		text := "❌ Не удалось открыть беседу: " + err.Error()
		if errors.Is(err, session.ErrSessionTurnBusy) {
			text += "\n" + noteTurnBusy
		}
		return text, [][]Button{}
	}

	b.store.Bind(key, id)
	b.log.Info("max: session resumed", "old", old, "new", id, "user", in.userID, "chat", in.chatID)
	// A turn still running for the conversation just left belongs to that
	// conversation: it keeps going in its own session, but it stops writing
	// here, otherwise two dialogues answer into one chat (bot.go: detachChat).
	if stopped := b.detachChat(b.answerTarget(in), id); stopped > 0 {
		b.log.Info("max: the running turn left the chat with the switch",
			"new", id, "user", in.userID, "chat", in.chatID, "turns", stopped)
	}

	text := "▶️ Продолжаю: " + b.resumeLabel(row) + "\n" + id
	if line := resumeSettingsLine(b.runner.Cfg(), st); line != "" {
		text += "\n" + line
	}
	if b.turnActive(id) {
		text += "\n" + noteTurnBusy
	}
	return text, [][]Button{}
}

// resumeMenu is one page of the keyboard with the text above it.
type resumeMenu struct {
	text    string
	buttons [][]Button
}

// resumeMenu lays out one page of the keyboard over rows: a button per
// session, the chat's current one marked, and a navigation row when there is
// more than one page. With a query the rows are the matches and there is no
// navigation - the query does not fit the callback payload, so a second page
// could not be asked for - and the text says how many matched instead; a
// narrower query is the way to the rest.
func (b *Bot) resumeMenu(rows []acp.SessionListInfo, current string, page int, query string) resumeMenu {
	pages := (len(rows) + resumePageSize - 1) / resumePageSize
	if pages < 1 || query != "" {
		pages = 1
	}
	page = max(0, min(page, pages-1))
	start := page * resumePageSize
	end := min(start+resumePageSize, len(rows))

	buttons := make([][]Button, 0, resumePageSize+1)
	for _, row := range rows[start:end] {
		buttons = append(buttons, []Button{{
			Text:    b.resumeButtonLabel(row, row.SessionID == current),
			Payload: resumePickPrefix + callbackValue(len(resumePickPrefix), row.SessionID),
		}})
	}
	var nav []Button
	if page > 0 {
		nav = append(nav, Button{Text: "◀", Payload: resumePageCallback(page - 1)})
	}
	if page < pages-1 {
		nav = append(nav, Button{Text: "▶", Payload: resumePageCallback(page + 1)})
	}
	if len(nav) > 0 {
		buttons = append(buttons, nav)
	}

	var sb strings.Builder
	sb.WriteString("Беседы")
	switch {
	case query != "" && end < len(rows):
		fmt.Fprintf(&sb, " · нашлось %d, первые %d", len(rows), end)
	case query != "":
		fmt.Fprintf(&sb, " · нашлось %d", len(rows))
	default:
		fmt.Fprintf(&sb, " · стр. %d/%d", page+1, pages)
	}
	return resumeMenu{text: sb.String(), buttons: buttons}
}

// resumePageCallback is the payload of one navigation arrow.
func resumePageCallback(page int) string {
	return callbackActionResume + ":" + resumePageMarker + ":" + strconv.Itoa(page)
}

// listSessions asks the server for the sessions it keeps, newest first. No
// folder narrows the list: a chat has no working directory of its own, so it
// sees what a console connected to this server would see.
func (b *Bot) listSessions(ctx context.Context) ([]acp.SessionListInfo, error) {
	lister, ok := b.runner.(sessionLister)
	if !ok {
		return nil, errSessionListUnavailable
	}
	res, err := lister.HandleSessionList(ctx, acp.SessionListParams{})
	if err != nil {
		return nil, err
	}
	if res == nil {
		return nil, nil
	}
	return res.Sessions, nil
}

// sendResumeMenu posts the menu into the chat of in.
func (b *Bot) sendResumeMenu(ctx context.Context, c *Client, in inbound, menu resumeMenu) {
	if _, err := c.Send(ctx, Outgoing{
		Target:  b.answerTarget(in),
		Text:    menu.text,
		Format:  "markdown",
		Buttons: menu.buttons,
	}); err != nil {
		b.log.Warn("max: send resume menu", "err", err, "target", b.answerTarget(in))
	}
}

// resumeReply posts one plain message into the chat of in. A confirmation is
// read on a phone: it carries no markup, so nothing is left unrendered there.
func (b *Bot) resumeReply(ctx context.Context, c *Client, in inbound, text string) {
	if _, err := c.Send(ctx, Outgoing{Target: b.answerTarget(in), Text: text}); err != nil {
		b.log.Warn("max: reply not delivered", "err", err, "target", b.answerTarget(in))
	}
}

// callbackInbound reads the chat, the person and the kind of chat out of a
// callback update: a press carries the message it was made on, and MAX puts
// the ids in different places for a message and for a press.
func (b *Bot) callbackInbound(u Update, cb Callback) inbound {
	from := cb.User
	if from.UserID == 0 {
		from = u.From()
	}
	chatID := u.ChatID
	isGroup := false
	if m, err := u.Body(); err == nil {
		if chatID == 0 {
			chatID = m.Recipient.ChatID
		}
		isGroup = m.Recipient.ChatType != "dialog"
	}
	if chatID == 0 {
		chatID = from.UserID
	}
	return inbound{chatID: chatID, userID: from.UserID, isGroup: isGroup}
}

// sessionKey is the key the chat-to-session map holds for in.
func (b *Bot) sessionKey(in inbound) string {
	isolation := access.EffectiveIsolation(in.chatID, b.cfg)
	return sessionstore.SessionKey(adapterName, in.chatID, in.userID, isolation, in.isGroup)
}

// matchResumeSessions returns the sessions query names: the one whose id it
// is, or otherwise every one whose id starts with it or whose title contains
// it, case-insensitively. A query is what a person types from memory, so it is
// read loosely; the exact id is checked first, byte for byte, because an id
// spelled in full can be nothing else.
func matchResumeSessions(rows []acp.SessionListInfo, query string) []acp.SessionListInfo {
	exact := strings.TrimSpace(query)
	q := strings.ToLower(exact)
	if q == "" {
		return nil
	}
	for _, row := range rows {
		if row.SessionID == exact {
			return []acp.SessionListInfo{row}
		}
	}
	var out []acp.SessionListInfo
	for _, row := range rows {
		if strings.HasPrefix(strings.ToLower(row.SessionID), q) || strings.Contains(strings.ToLower(rowTitle(row)), q) {
			out = append(out, row)
		}
	}
	return out
}

// resolveResumeCallback maps the payload of a session button back to a row of
// the current listing. A session deleted since the keyboard was sent resolves
// to nothing, which is a better answer than binding the chat to a bundle that
// is gone.
func resolveResumeCallback(rows []acp.SessionListInfo, payload string) (acp.SessionListInfo, bool) {
	for _, row := range rows {
		if row.SessionID == payload {
			return row, true
		}
	}
	if !strings.HasPrefix(payload, callbackDigestMarker) {
		return acp.SessionListInfo{}, false
	}
	for _, row := range rows {
		if callbackValue(len(resumePickPrefix), row.SessionID) == payload {
			return row, true
		}
	}
	return acp.SessionListInfo{}, false
}

// resumeButtonLabel is what a session button shows: the mark of the chat's
// current conversation, the sign of the surface the session came from, and its
// title - or the id of a session that has none yet.
func (b *Bot) resumeButtonLabel(row acp.SessionListInfo, current bool) string {
	label := b.resumeLabel(row)
	if current {
		label = "▶️ " + label
	}
	return truncateRunes(label, resumeLabelMaxRunes)
}

// resumeLabel names a session in prose and on a button: its title, or the id
// of a session that has none yet, with the sign of where it came from.
func (b *Bot) resumeLabel(row acp.SessionListInfo) string {
	label := rowTitle(row)
	if label == "" {
		label = shortSessionID(row.SessionID)
	}
	if mark := b.originMark(row.SessionID); mark != "" {
		label = mark + " " + label
	}
	return label
}

// originMark is the sign of the surface a session came from: a plane for a
// conversation held in Telegram, a globe for one opened here or in the web UI,
// a speech bubble for any other messenger. A conversation of this gateway
// carries no sign - the menu around it already says where it is - and a
// session whose origin nothing can report stays unmarked rather than being
// guessed at.
func (b *Bot) originMark(sessionID string) string {
	reader, ok := b.runner.(sessionOriginReader)
	if !ok {
		return ""
	}
	origin := strings.ToLower(strings.TrimSpace(reader.SessionOrigin(sessionID)))
	switch {
	case origin == session.GatewayOrigin(adapterName):
		return ""
	case origin == session.GatewayOrigin("telegram"):
		return "✈️"
	case origin == "" || session.IsPrintOrigin(origin):
		return "🌐"
	default:
		return "💬"
	}
}

// turnActive reports whether the session runs a turn in this process.
func (b *Bot) turnActive(sessionID string) bool {
	checker, ok := b.runner.(turnActiveChecker)
	return ok && checker.SessionTurnActiveInProcess(sessionID)
}

// resumeSettingsLine names what a resumed session runs on, as it keeps it: its
// model and its mode. It is the session's own, never the model last picked on
// this bot, which only a fresh chat starts on.
func resumeSettingsLine(cfg *config.Config, st *session.State) string {
	if cfg == nil || st == nil {
		return ""
	}
	model := st.SessionModelID(cfg)
	if model == "" {
		return ""
	}
	mode := st.GetMode()
	if mode == "" {
		mode = string(session.ModeAgent)
	}
	return "Модель: " + model + " · режим: " + mode
}

// resumeTitle names a session in prose: its title, or a short form of the id
// of one that has none yet.
func rowTitle(row acp.SessionListInfo) string {
	if row.Title == nil {
		return ""
	}
	return strings.TrimSpace(*row.Title)
}

// shortSessionID is the readable head of an id, for a session with no title
// yet: enough to recognise it, short enough for a button.
func shortSessionID(id string) string {
	const keep = 9
	rs := []rune(id)
	if len(rs) <= keep {
		return id
	}
	return string(rs[:keep]) + "…"
}

// truncateRunes cuts s to at most n runes, ending it with an ellipsis when it
// was longer. Counting runes, not bytes, keeps a Cyrillic title from being cut
// inside a character.
func truncateRunes(s string, n int) string {
	rs := []rune(s)
	if len(rs) <= n {
		return s
	}
	if n < 1 {
		return "…"
	}
	return string(rs[:n-1]) + "…"
}
