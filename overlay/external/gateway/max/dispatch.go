//go:build gateway || gateway.max

package max

// From an update to a turn: what the update is, whether it is addressed to the
// bot, who may talk to it, which session it belongs to. The bot itself is the
// Handler the poll loop hands updates to, so Start calls PollLoop(ctx, c, b).

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/EvilFreelancer/coddy-agent/external/gateway/access"
	"github.com/EvilFreelancer/coddy-agent/external/gateway/sessionstore"
	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

const (
	// turnTimeout bounds one turn of a chat message.
	turnTimeout = 10 * time.Minute
	// actionTimeout bounds one chat action. A message left unread, or a missing
	// "typing…", is a worse chat and never a failed turn: the call is given a
	// short rope and its refusal only reaches the log.
	actionTimeout = 5 * time.Second
	// typingInterval is how often the "typing…" hint is renewed. MAX shows it
	// for a few seconds, so a turn longer than that repeats it; the Telegram
	// adapter settled on the same four seconds.
	typingInterval = 4 * time.Second
	// noTextNote answers a message with nothing written in it.
	noTextNote = "Здесь я читаю только текст: сообщение без текста не передаётся. Напишите вопрос текстом."
)

// inbound is a message the bot will answer.
type inbound struct {
	chatID  int64
	userID  int64
	isGroup bool
	text    string
	command string
	args    string
	images  []ImageAttachment
	// files are the attachments that are not pictures: documents, recordings
	// and videos. They are saved to the conversation's Max-in folder, so the
	// agent works with the file itself (incoming.go).
	files []InboundFile
	// parts counts the messages of MAX folded into this one: a person who
	// writes a line and then sends the picture it is about sends two, and they
	// run as one turn (burst.go).
	parts int
}

// Handle routes one update. It satisfies the Handler the poll loop hands
// updates to: a callback is answered, a created message becomes a session
// turn, everything else - an edit, a reaction - is ignored. The bot never
// answers itself: its own messages arrive back as updates, and are told apart
// by the is_bot flag of their sender.
func (b *Bot) Handle(ctx context.Context, u Update) error {
	switch u.UpdateType {
	case "message_callback":
		c := b.connectedClient()
		if c == nil {
			return nil
		}
		return b.HandleCallback(ctx, c, u)
	case "message_created":
		// De-duplicate by message id: MAX may deliver a message again after
		// a restart that stopped between reading the page and handling it,
		// and one message runs one turn. The id of a created message lives in
		// message.body.mid, which MessageKey reads, not in the update's own
		// message_id that MAX leaves for other update types.
		key := u.MessageKey()
		if key == "" {
			b.log.Debug("max: update ignored", "reason", "no message id")
			return nil
		}
		if !b.seen.add(key) {
			return nil
		}
		return b.handleMessage(ctx, u)
	default:
		return nil
	}
}

// handleMessage turns one created message into an enqueued turn, or ignores it.
func (b *Bot) handleMessage(ctx context.Context, u Update) error {
	m, err := u.Body()
	if err != nil {
		return err
	}
	sender := u.From()
	if sender.IsBot {
		// Our own messages and other bots' are never answered.
		return nil
	}
	chatID := u.ChatID
	if chatID == 0 {
		chatID = m.Recipient.ChatID
	}
	userID := u.UserID
	if userID == 0 {
		userID = m.Sender.UserID
	}
	// MAX calls a direct chat "dialog"; anything else is a group or a channel,
	// where the bot answers only when it is meant to.
	isGroup := m.Recipient.ChatType != "dialog"

	if !b.allowed(chatID, userID, isGroup) {
		return nil
	}
	// The second tick on the person's own message. MAX shows it only when the
	// bot says it has read the chat, and the bot never said so: every message
	// to it stayed single-ticked, which reads as "delivered and forgotten". It
	// goes out before the burst wait, so "дошло" is answered at once rather
	// than three seconds later - and in the background, because the client
	// paces its writes and a hint must never hold up the intake.
	go b.markSeen(context.WithoutCancel(ctx), chatID)
	b.log.Debug("max: update",
		"kind", "message",
		"user", userID,
		"chat", chatID,
		"is_group", isGroup,
		"text_len", len(m.Body.Text),
	)

	text := strings.TrimSpace(m.Body.Text)
	images, imgErr := m.ImageAttachments()
	if imgErr != nil {
		b.log.Debug("max: update has malformed attachments", "err", imgErr,
			"user", userID, "chat", chatID)
	}
	files, fileErr := m.InboundFiles()
	if fileErr != nil {
		b.log.Debug("max: update has malformed attachments", "err", fileErr,
			"user", userID, "chat", chatID)
	}
	in := inbound{chatID: chatID, userID: userID, isGroup: isGroup, text: text, images: images, files: files, parts: 1}
	in.command, in.args = parseCommand(text)
	if in.command != "" && !knownCommand(in.command) {
		b.log.Debug("max: update ignored", "reason", "unknown command", "command", in.command)
		return nil
	}

	isolation := access.EffectiveIsolation(chatID, b.cfg)
	key := sessionstore.SessionKey(adapterName, chatID, userID, isolation, isGroup)
	// A prompt that asked for a written answer takes the next text of this chat:
	// the person pressed "Свой ответ" and the card says so. A command is never an
	// answer - it runs as itself and the wait ends - and a message that carries no
	// text leaves the wait standing and says again what is expected.
	if b.asked.armed(userID) {
		switch {
		case in.command != "":
			b.asked.disarm(userID)
		case text == "":
			b.log.Warn("max: written answer still awaited", "user", userID, "chat", chatID)
			b.reply(ctx, in, customAnswerNote)

			return nil
		case b.asked.write(userID, text):
			b.log.Debug("max: written answer taken", "user", userID, "chat", chatID, "len", len(text))

			return nil
		}
	}
	// /stop is answered on intake, before the burst wait and before the queue:
	// it ends the turn in flight, and joining that queue would defeat it
	// (stop.go).
	if in.command == "stop" {
		b.bursts().flushKey(key)
		b.handleStop(ctx, in, key)

		return nil
	}
	// A command is answered at once - the person expects the menu, not a wait -
	// but whatever the chat held so far goes first, so the order of what was
	// written stays the order of what is answered.
	if in.command != "" {
		b.bursts().flushKey(key)
		b.deliver(ctx, in, key)
		return nil
	}
	// Everything else waits a moment for the rest of its burst: a line and the
	// picture it is about are two messages of MAX, and one question (burst.go).
	b.bursts().add(ctx, in, key)
	return nil
}

// deliver hands one message, or the burst that completed it, to the worker of
// its session, and tells the person when the queue is full.
func (b *Bot) deliver(ctx context.Context, in inbound, key string) {
	if in.parts > 1 {
		b.log.Debug("max: burst merged", "parts", in.parts, "images", len(in.images), "key", key)
	}
	accepted, queued := b.enqueue(b.turnContext(ctx), in, key)
	if !accepted {
		// The bot is stopping: the message stays in the stream for the next
		// process, since the cursor still stands before it.
		return
	}
	if !queued {
		b.log.Debug("max: update rejected", "reason", "worker queue full", "key", key, "cap", workerQueueCap)
		b.reply(ctx, in, "Слишком много сообщений ждут ответа; это я не взял. Пришлите ещё раз, когда отвечу.")
	}
}

// allowed applies the access level and the admin-only isolation of the chat.
func (b *Bot) allowed(chatID, userID int64, isGroup bool) bool {
	level := access.EffectiveAccess(chatID, b.cfg)
	if !access.CanAccess(userID, level, b.cfg) {
		b.log.Debug("max: update ignored", "reason", "access denied", "user", userID, "chat", chatID)
		return false
	}
	if isGroup && access.EffectiveIsolation(chatID, b.cfg) == config.IsolationAdmin && !b.cfg.IsAdmin(userID) {
		b.log.Debug("max: update ignored", "reason", "admin-only chat", "user", userID, "chat", chatID)
		return false
	}
	return true
}

// markSeen tells MAX the chat has been read, and the person's own message turns
// from one tick into two - the only "your message reached me" the platform
// shows without a line of its own in the chat. Best effort: a chat left unread
// is a worse chat, never a failed turn.
func (b *Bot) markSeen(ctx context.Context, chatID int64) {
	if c := b.connectedClient(); c != nil {
		b.sendAction(ctx, c, chatID, ActionMarkSeen)
	}
}

// typingKeepAlive renews the "typing…" hint until the returned stop is called
// or the turn's context ends: MAX shows the action for a few seconds and a turn
// lasts minutes. The first action goes out at once, so the chat answers the
// person's message before the first word of the answer exists.
func (b *Bot) typingKeepAlive(ctx context.Context, chatID int64) (stop func()) {
	c := b.connectedClient()
	if c == nil {
		return func() {}
	}
	// The hint's own context, so stopping cancels a send that is already
	// waiting its turn in the client's pacer instead of landing after the turn.
	tctx, cancel := context.WithCancel(ctx)
	b.sendAction(tctx, c, chatID, ActionTyping)
	every := b.typingEvery
	if every <= 0 {
		every = typingInterval
	}
	go func() {
		ticker := time.NewTicker(every)
		defer ticker.Stop()
		for {
			select {
			case <-tctx.Done():
				return
			case <-ticker.C:
				b.sendAction(tctx, c, chatID, ActionTyping)
			}
		}
	}()
	var once sync.Once

	return func() { once.Do(cancel) }
}

// sendAction posts one chat action, best effort and bounded: a refused hint is
// a line in the log, and nobody loses an answer over it.
func (b *Bot) sendAction(ctx context.Context, c *Client, chatID int64, action string) {
	actx, cancel := context.WithTimeout(ctx, actionTimeout)
	defer cancel()
	if err := c.SendAction(actx, chatID, action); err != nil {
		b.log.Debug("max: chat action", "action", action, "chat", chatID, "err", err)
	}
}

// enqueue hands a message to the worker of its session key, so one session
// runs one turn at a time. It reports false when the bot is stopping: the
// message then stays in the stream for the next process. A queued message
// counts as in flight from here.
func (b *Bot) enqueue(ctx context.Context, in inbound, key string) (accepted, queued bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.stopping {
		return false, false
	}
	ch, ok := b.workers[key]
	if !ok {
		ch = make(chan workerJob, workerQueueCap)
		b.workers[key] = ch
		go b.sessionWorker(ctx, ch)
	}
	b.inFlight.Add(1)
	select {
	case ch <- workerJob{in: in, key: key}:
		return true, true
	default:
		b.inFlight.Done()
		return true, false
	}
}

func (b *Bot) sessionWorker(ctx context.Context, ch chan workerJob) {
	for {
		select {
		case job := <-ch:
			b.processMessage(ctx, job.in, job.key)
			b.inFlight.Done()
		case <-ctx.Done():
			// The stop waited as long as it could: what is still queued is
			// let go, and no longer counted.
			for {
				select {
				case <-ch:
					b.inFlight.Done()
				default:
					return
				}
			}
		}
	}
}

// processMessage runs the turn of one message in the worker of its session key.
func (b *Bot) processMessage(ctx context.Context, in inbound, key string) {
	c := b.connectedClient()
	if c == nil {
		return
	}
	text := in.text
	hasImages := len(in.images) > 0
	if text == "" && !hasImages && len(in.files) == 0 {
		b.reply(ctx, in, noTextNote)
		return
	}
	// A group shares the bot with many people: what changes the session's
	// settings, or replaces the conversation, is the admins' to do. Two of
	// those are the admins' in a personal chat as well - the model a session
	// runs on and the act of throwing a conversation away - because the plain
	// user's own chat was the one door left open (29.10.2026).
	if changesSettings(in.command) && !b.cfg.IsAdmin(in.userID) && (in.isGroup || adminEverywhere(in.command)) {
		b.log.Debug("max: update refused", "reason", "settings are admin-only", "command", in.command, "user", in.userID, "chat", in.chatID)
		b.reply(ctx, in, adminOnlyNote)
		return
	}
	switch in.command {
	case "clear":
		oldID := b.store.Get(key)
		newID := b.store.Reset(key)
		b.runner.ForgetLiveSession(oldID)
		b.reply(ctx, in, "Начата новая беседа.")
		b.log.Info("max: session cleared", "old", oldID, "new", newID, "user", in.userID)
		return
	case "start":
		b.reply(ctx, in, "Привет! Пришлите вопрос или задачу; /help покажет команды.")
		return
	case "help":
		b.reply(ctx, in, b.helpText())
		return
	case "model":
		if in.args == "" {
			b.handleModelCommand(ctx, in, key)
			return
		}
	case "context":
		b.handleContextCommand(ctx, in, key)
		return
	case "resume":
		// Reading the list of the bot's other conversations and moving the
		// chat to one of them is the admins' call on every surface. A direct
		// chat is not a safe place for it either: the list holds every session
		// the bot has, listed by its title, and a chat bound to somebody
		// else's session shows their history from the next turn on. Until the
		// list is filtered by its owner, the command stays with the admins.
		if !b.cfg.IsAdmin(in.userID) {
			b.reply(ctx, in, adminOnlyNote)
			return
		}
		b.handleResume(ctx, in, key, in.args)
		return
	}
	// A typed "/model <id>" is a session-scoped pick on this surface: the
	// gateway remembers it, since the remembered model is what every fresh
	// conversation of the bot starts on.
	if line, err := session.ParseSettingsCommands(text); err == nil && line.Session.Model != nil && b.cfg.IsAdmin(in.userID) {
		if id := strings.TrimSpace(*line.Session.Model); b.runner.Cfg().FindModelEntry(id) != nil {
			b.store.SetLastModel(id)
		}
	}

	tctx, cancel := context.WithTimeout(ctx, turnTimeout)
	defer cancel()
	// The turn is on record by its session key, so /stop ends it from the intake
	// path of another goroutine instead of waiting behind it (stop.go).
	b.registerTurn(key, cancel)
	defer b.unregisterTurn(key)
	// "typing…" while this turn runs, renewed for as long as it takes: without
	// it the chat is silent from the person's message to the first word of the
	// answer, which is most of a slow turn.
	stopTyping := b.typingKeepAlive(tctx, in.chatID)
	defer stopTyping()
	st, err := b.ensureSession(tctx, key)
	if err != nil {
		b.log.Warn("max: ensure session", "err", err)
		b.reply(ctx, in, "Не удалось начать беседу: "+err.Error())
		return
	}

	// Pictures need a multimodal model. If the current model is not one,
	// tell the person and do not run a turn.
	if hasImages {
		cfg := b.runner.Cfg()
		model := st.EffectiveModelID(cfg)
		entry := cfg.FindModelEntry(model)
		if entry == nil || !entry.Multimodal {
			b.log.Debug("max: image rejected by model", "model", model, "user", in.userID, "chat", in.chatID)
			b.reply(ctx, in, multimodalNote(model))
			return
		}
	}

	sender := b.newSender(tctx, c, b.answerTarget(in))
	// A turn in flight is on record, so a switch of the chat can stop it from
	// writing where it no longer belongs (resume.go, bot.go: detachChat).
	b.trackSender(sender)
	defer b.untrackSender(sender)
	// The chat's own session, so the pictures its agent reads are shown here
	// (pictures.go); a subagent's calls belong to another session.
	sender.pictures = st
	sender.files = st
	// Somebody who is not the bot's admin gets the conversation and nothing
	// that changes the agent: the turn refuses the tools that would, and the
	// chat approves nothing it is asked about.
	var restriction *session.TurnRestriction
	if !b.cfg.IsAdmin(in.userID) {
		restriction = access.NonAdminTurn()
		sender.refuseApprovals = true
	}
	// Anything else in this process that can show a session follows along;
	// permission prompts and questions stay with the chat.
	mirrored, releaseMirror := session.Mirror(b.mirror, st.GetID(), sender)
	defer releaseMirror()

	cwd := st.GetCWD()
	if cwd == "" {
		cwd = b.cwd
	}
	// Every attachment a person sent lands in this conversation's Max-in folder.
	// A picture is saved from the bytes the model gets, so it is not fetched
	// twice; documents, recordings and videos are streamed to disk
	// (incoming.go). The agent then works with the file, not with a promise.
	var imageParts []acp.ImagePartRef
	var saved []savedFile
	if hasImages {
		images, failed, err := b.downloadImages(tctx, in.images)
		if err != nil {
			b.log.Warn("max: image download failed", "file", failed, "err", err, "session", st.GetID(), "user", in.userID, "chat", in.chatID)
			b.reply(ctx, in, "Не удалось скачать "+failed+" из МАКСа; пришлите ещё раз.")
			return
		}
		for _, img := range images {
			imageParts = append(imageParts, img.Part)
			got, saveErr := b.saveIncoming(cwd, "image", img.Name, img.Data)
			if saveErr != nil {
				b.log.Warn("max: incoming picture not saved", "name", img.Name, "err", saveErr)
				b.reply(ctx, in, "Снимок «"+img.Name+"» не сохранён: "+saveErr.Error()+".")
				continue
			}
			saved = append(saved, got)
		}
	}
	if len(in.files) > 0 && cwd != "" {
		got, refusals := b.receiveFiles(tctx, cwd, in.files)
		saved = append(saved, got...)
		for _, note := range refusals {
			b.reply(ctx, in, "⚠️ "+note)
		}
	}
	if cwd == "" && (hasImages || len(in.files) > 0) {
		// Nothing to attach the file to: a conversation without a working
		// directory has nowhere to keep it, and writing beside the gateway
		// itself would leave it in the program's own tree.
		b.log.Warn("max: incoming files not saved, the session has no working directory",
			"session", st.GetID(), "images", len(in.images), "files", len(in.files))
	}
	text += incomingBlock(saved)

	params := acp.SessionPromptParams{
		SessionID:  st.GetID(),
		Prompt:     []acp.ContentBlock{{Type: "text", Text: text}},
		ImageParts: imageParts,
	}
	opts := &session.PromptRunOpts{
		SkipUsagePublish:    true,
		SurfaceSystemPrompt: surfaceSystemPrompt(),
		Restriction:         restriction,
	}
	result, err := b.runTurn(tctx, params, mirrored, opts)
	sender.Flush()

	if err != nil {
		// A session whose previous turn is still running is not an error the
		// person caused: saying so in English internals is not an answer, so it
		// is told to send the message again once the turn is over.
		if errors.Is(err, session.ErrSessionTurnBusy) {
			b.log.Warn("max: the previous turn still holds the session", "session", st.GetID(), "user", in.userID)
			b.reply(ctx, in, busyNote)
			return
		}
		stopReason := ""
		if result != nil {
			stopReason = string(result.StopReason)
		}
		if b.personStopped(key) {
			// The person ended this turn with /stop, so an interrupted generation
			// is exactly what they asked for: the core's wording for it belongs in
			// the log, while the chat already has the stop confirmation (stop.go).
			b.log.Info("max: turn ended by the person", "err", err, "session", st.GetID(), "stop_reason", stopReason)

			return
		}
		b.log.Warn("max: agent error", "err", err, "session", st.GetID(), "stop_reason", stopReason)
		b.reply(ctx, in, "Ошибка агента: "+err.Error())
		return
	}
	b.log.Debug("max: agent turn done", "session", st.GetID())
	if result != nil && result.StopNotice != "" {
		b.reply(ctx, in, result.StopNotice)
	}
}

// The turns of one session are serialized, so a message that arrives while the
// previous turn is still running waits for its place in the queue instead of
// failing at once. Only a wait that runs out is reported to the person, and in
// Russian: the internal error is not an answer.
var (
	// turnBusyWait bounds how long a message waits for the previous turn.
	turnBusyWait = 30 * time.Second
	// turnBusyPause is the gap between two attempts.
	turnBusyPause = time.Second
)

// busyNote is what a person is told when their message could not be taken.
const busyNote = "В беседе идёт работа — пришлите сообщение ещё раз, когда я закончу."

// runTurn runs one turn, waiting out a session whose previous turn has not
// finished yet.
func (b *Bot) runTurn(ctx context.Context, params acp.SessionPromptParams, sender acp.UpdateSender, opts *session.PromptRunOpts) (*acp.SessionPromptResult, error) {
	deadline := time.Now().Add(turnBusyWait)
	for {
		result, err := b.runner.HandleSessionPromptWithSender(ctx, params, sender, opts)
		if err == nil || !errors.Is(err, session.ErrSessionTurnBusy) {
			return result, err
		}
		if !time.Now().Before(deadline) {
			return result, err
		}
		b.log.Debug("max: turn waits for the previous one", "session", params.SessionID)
		select {
		case <-ctx.Done():
			return result, ctx.Err()
		case <-time.After(turnBusyPause):
		}
	}
}

// answerTarget is where the answer to in goes.
func (b *Bot) answerTarget(in inbound) Target {
	if in.isGroup {
		return Target{ChatID: in.chatID}
	}
	return Target{UserID: in.userID}
}

// reply posts a short service message into the chat the message came from.
func (b *Bot) reply(ctx context.Context, in inbound, text string) {
	c := b.connectedClient()
	if c == nil {
		return
	}
	if _, err := c.Send(ctx, Outgoing{Target: b.answerTarget(in), Text: text}); err != nil {
		b.log.Warn("max: reply not delivered", "err", err, "target", b.answerTarget(in))
	}
}

// ensureSession gets or creates the session for key. A conversation this
// gateway starts is stamped with where it came from.
func (b *Bot) ensureSession(ctx context.Context, key string) (*session.State, error) {
	fresh := b.store.Peek(key) == ""
	st, err := b.runner.EnsureHTTPSession(ctx, b.store.Get(key), b.cwd)
	if err != nil {
		return nil, err
	}
	if fresh {
		st.SetOrigin(session.GatewayOrigin(adapterName))
	}
	b.applyInitialModel(ctx, st)
	return st, nil
}

// applyInitialModel stamps the gateway's own model on a session that is just
// beginning: the model last picked on this surface, or the alphabetically
// first configured one. A session that already chose keeps its model.
func (b *Bot) applyInitialModel(ctx context.Context, st *session.State) {
	if st == nil || st.GetSelectedModelID() != "" || len(st.GetMessages()) != 0 {
		return
	}
	cfg := b.runner.Cfg()
	initial := cfg.FirstModelID()
	if last := b.store.LastModel(); cfg.FindModelEntry(last) != nil {
		initial = last
	}
	if initial == "" || initial == st.EffectiveModelID(cfg) {
		return
	}
	if _, err := b.runner.HandleSessionSetConfigOption(ctx, acp.SessionSetConfigOptionParams{
		SessionID: st.GetID(), ConfigID: "model", Value: initial,
	}); err != nil {
		b.log.Warn("max: initial model", "err", err, "session", st.GetID(), "model", initial)
	}
}

// seenIDs remembers recently handled message ids, so a message MAX delivered
// twice runs one turn.
type seenIDs struct {
	mu    sync.Mutex
	limit int
	set   map[string]bool
	order []string
}

func newSeenIDs(limit int) *seenIDs {
	return &seenIDs{limit: limit, set: make(map[string]bool)}
}

// add records id and reports whether it was new.
func (s *seenIDs) add(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.set[id] {
		return false
	}
	s.set[id] = true
	s.order = append(s.order, id)
	if len(s.order) > s.limit {
		delete(s.set, s.order[0])
		s.order = s.order[1:]
	}
	return true
}

// What this adapter tells the model about answering into a MAX chat: a system
// prompt block for the length of one turn (session.PromptRunOpts
// .SurfaceSystemPrompt), never a line added to what the person wrote, so the
// transcript reads the same from every surface.
const maxGuidance = "## Answering in a MAX chat\n\n" +
	"This conversation reaches the person through MAX, a messenger that shows plain text. Write the answer for a chat.\n\n" +
	"- a chat is a narrow column: answer in a few short paragraphs and put the thing that was asked for first;\n" +
	"- plain text renders as sent: keep lists short and code blocks small.\n\n" +
	"None of this is visible to the person, and nothing about it belongs in the answer itself."

// surfaceSystemPrompt is what the gateway hands the session for one turn.
func surfaceSystemPrompt() string { return maxGuidance }
