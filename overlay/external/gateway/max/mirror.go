//go:build gateway || gateway.max

package max

// The gateway is transparent both ways: what is written in a chat runs in the
// session the chat is bound to, and a turn somebody starts on another surface -
// the browser, the console - has to read in that chat too. The bot therefore
// offers itself to the process (Runtime.AddTurnMirror) as a mirror of the chats
// it owns: a turn nobody's chat started is published into the chat its session
// is bound to, and the person's own line is shown there as well.

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/EvilFreelancer/coddy-agent/external/gateway/sessionstore"
	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// jobHeaderRunes keeps the header one readable line above the report it marks.
const jobHeaderRunes = 80

// jobHeader marks a message a scheduled job sends into the conversation: the
// person must see at once that this is not a reply in the dialogue, but a task
// that fired on its own, and which one it was.
func jobHeader(description string, at time.Time) string {
	text := strings.TrimSpace(description)
	if text == "" {
		text = "задание"
	}

	return "⏰ Задание: " + shortenRunes(text, jobHeaderRunes) + " · " + at.Format("02.01, 15:04")
}

// reportSession is the conversation a scheduled job reports into: the chat of an
// administrator of this gateway, read from the binding that chat already holds.
// Nothing is minted here - a gateway whose administrator has never written has
// nowhere to report, and the report then stays in the job's own session.
func (b *Bot) reportSession() (string, bool) {
	if b.store == nil || b.cfg == nil {
		return "", false
	}
	for _, admin := range b.cfg.Admins {
		if admin == 0 {
			continue
		}
		if id := strings.TrimSpace(b.store.Peek(reportKey(admin))); id != "" {
			return id, true
		}
	}

	return "", false
}

// reportKey is the binding key of an administrator's private chat: the same one
// a message typed there is filed under, so a report follows the conversation
// rather than a number kept beside the job.
func reportKey(admin int64) string {
	return sessionstore.SessionKey(adapterName, 0, admin, config.IsolationIndividual, false)
}

// SayJobReport shows a scheduled job's report in the conversation of an
// administrator, headed as the job's own message rather than as a line of the
// dialogue it lands in. It refuses to invent a conversation: a gateway whose
// administrator has never written reports that it has nowhere to report, and
// the caller keeps the text in the job's own session instead of dropping it.
func (b *Bot) SayJobReport(description string, at time.Time, text string) bool {
	text = strings.TrimSpace(text)
	if text == "" {
		return false
	}
	sessionID, ok := b.reportSession()
	if !ok {
		return false
	}
	chat, release := b.chatSender(sessionID)
	if chat == nil {
		return false
	}
	defer release()
	chat.post(jobHeader(description, at) + "\n\n" + text)

	return true
}

// MirrorTurn publishes a turn this bot did not start into the chat its session
// is bound to, and hands the turn back untouched when the chat owns it already:
// a message typed in MAX is answered there by the turn itself.
func (b *Bot) MirrorTurn(sessionID string, primary acp.UpdateSender) (acp.UpdateSender, func()) {
	if _, own := primary.(*Sender); own {
		return primary, func() {}
	}
	chat, release := b.chatSender(sessionID)
	if chat == nil {
		return primary, func() {}
	}
	if primary == nil {
		return chat, release
	}

	return &mirroredTurn{primary: primary, chat: chat}, release
}

// Say shows in the bound chat a line written on another surface, so a
// conversation opened in the browser reads in MAX as it does there.
func (b *Bot) Say(sessionID, text string) {
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}
	chat, release := b.chatSender(sessionID)
	if chat == nil {
		return
	}
	defer release()
	chat.post("🌐 " + text)
}

// chatSender builds the sender of the chat the session is bound to, with nil
// when this bot serves no such chat or is not connected right now.
func (b *Bot) chatSender(sessionID string) (*Sender, func()) {
	target, ok := b.boundChat(sessionID)
	if !ok {
		return nil, func() {}
	}
	c := b.connectedClient()
	if c == nil {
		return nil, func() {}
	}
	ctx := b.turnContext(context.Background())
	chat := b.newSender(ctx, c, target)
	// The chat's own session, so the pictures its agent reads are shown there
	// (pictures.go) - the same as the turn of a message typed in MAX.
	if st, err := b.runner.EnsureHTTPSession(ctx, sessionID, b.cwd); err == nil {
		chat.pictures = st
	}

	return chat, chat.Flush
}

// boundChat is the chat a session is bound to, read from the map of the chats
// this bot serves: a direct chat is keyed by its person, a group by its chat.
func (b *Bot) boundChat(sessionID string) (Target, bool) {
	key, ok := b.store.KeyFor(sessionID)
	if !ok || !strings.HasPrefix(key, adapterName+":") {
		return Target{}, false
	}
	rest := strings.TrimPrefix(key, adapterName+":")
	if v, ok := strings.CutPrefix(rest, "user:"); ok {
		id, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
		if err != nil || id == 0 {
			return Target{}, false
		}

		return Target{UserID: id}, true
	}
	if v, ok := strings.CutPrefix(rest, "chat:"); ok {
		idPart, _, _ := strings.Cut(v, ":")
		id, err := strconv.ParseInt(strings.TrimSpace(idPart), 10, 64)
		if err != nil || id == 0 {
			return Target{}, false
		}

		return Target{ChatID: id}, true
	}

	return Target{}, false
}

// mirroredTurn fans a turn out to the chat that belongs to it and to the
// surface that started it. A question or a permission prompt is asked on both:
// the person may be reading the page or the chat, and a turn that waits for an
// answer nobody can give stalls where it stands.
type mirroredTurn struct {
	primary acp.UpdateSender
	chat    *Sender
}

func (m *mirroredTurn) SendSessionUpdate(sessionID string, update interface{}) error {
	if m.chat != nil {
		_ = m.chat.SendSessionUpdate(sessionID, update)
	}
	if m.primary == nil {
		return nil
	}

	return m.primary.SendSessionUpdate(sessionID, update)
}

func (m *mirroredTurn) RequestPermission(ctx context.Context, params acp.PermissionRequestParams) (*acp.PermissionResult, error) {
	refuse := &acp.PermissionResult{Outcome: "cancelled", OptionID: "reject"}
	asks := make([]func(context.Context) (*acp.PermissionResult, error), 0, 2)
	if m.primary != nil {
		asks = append(asks, func(ctx context.Context) (*acp.PermissionResult, error) {
			return m.primary.RequestPermission(ctx, params)
		})
	}
	if m.chat != nil {
		asks = append(asks, func(ctx context.Context) (*acp.PermissionResult, error) {
			return m.chat.RequestPermission(ctx, params)
		})
	}
	res, err := firstAnswer(ctx, session.PressedPermission, asks...)
	if res != nil {
		return res, nil
	}
	if err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
		return refuse, err
	}

	return refuse, nil
}

func (m *mirroredTurn) RequestQuestion(ctx context.Context, params acp.QuestionRequestParams) (*acp.QuestionResult, error) {
	asks := make([]func(context.Context) (*acp.QuestionResult, error), 0, 2)
	if m.primary != nil {
		asks = append(asks, func(ctx context.Context) (*acp.QuestionResult, error) {
			return m.primary.RequestQuestion(ctx, params)
		})
	}
	if m.chat != nil {
		asks = append(asks, func(ctx context.Context) (*acp.QuestionResult, error) {
			return m.chat.RequestQuestion(ctx, params)
		})
	}
	res, err := firstAnswer(ctx, session.AnsweredQuestion, asks...)
	if res != nil {
		return res, nil
	}
	if err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
		return &acp.QuestionResult{}, err
	}

	return &acp.QuestionResult{}, nil
}

// firstAnswer runs the same prompt on every surface that can show it and keeps
// the first answer somebody gave. The prompts the answer did not come from are
// withdrawn through the cause of the shared context.
func firstAnswer[T any](ctx context.Context, real func(T) bool, asks ...func(context.Context) (T, error)) (T, error) {
	var zero T
	switch len(asks) {
	case 0:
		return zero, nil
	case 1:
		return asks[0](ctx)
	}
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	type reply struct {
		value T
		err   error
	}
	replies := make(chan reply, len(asks))
	for _, ask := range asks {
		go func(ask func(context.Context) (T, error)) {
			value, err := ask(ctx)
			replies <- reply{value, err}
		}(ask)
	}
	var failure error
	for range asks {
		select {
		case got := <-replies:
			if real(got.value) {
				cancel(session.ErrAnsweredElsewhere)

				return got.value, got.err
			}
			if got.err != nil {
				failure = got.err
			}
		case <-ctx.Done():
			return zero, context.Cause(ctx)
		}
	}

	return zero, failure
}

var _ acp.UpdateSender = (*mirroredTurn)(nil)
