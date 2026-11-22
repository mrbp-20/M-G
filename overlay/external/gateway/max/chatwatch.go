//go:build gateway || gateway.max

package max

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/llm"
)

// A turn can start without a message from a chat: a background task finishing
// wakes the agent, a scheduler job runs, another window opens the conversation
// and works in it. handleMessage wraps only the turns a chat asked for, so the
// answer of such a turn used to stay inside the conversation - visible in the
// web UI, silent in MAX, and the person who had asked never learned that the
// task was over (20.11.2026: two answers never reached the chat that way).
//
// The watcher below holds every conversation some chat is bound to. It reads the
// conversation through the transcript hook (history.go: SetTranscript) and posts
// each assistant message of a turn this bot did not start into the chats that
// hold the conversation at the moment of delivery - not at the moment the turn
// started, because /resume may move the binding while the turn runs.
const (
	// selfPostedKeep is how long an answer this bot delivered itself stays in
	// the memory below.
	selfPostedKeep = 10 * time.Minute
	// selfPostedMax bounds that memory; the oldest entries go first.
	selfPostedMax = 512
)

// chatWatchPause is how often a watched conversation is read for answers. A bot
// carries its own pace in watchPause, so a test reads on a faster beat without
// touching a variable the running watchers share.
const chatWatchPause = 2 * time.Second

// chatWatchGrace is how long a watcher stays quiet after a turn of the same
// conversation stopped running.
//
// A turn this bot wraps delivers its answer itself and only then tells the
// watcher where to start (mirror.go, markTurnEnd). Between the running flag
// clearing and that mark there is a window of a few hundred milliseconds, and a
// tick landing inside it used to post the very answer the mirror had just
// delivered: on 22.11.2026 Merlen's chat got one block from the mirror and the
// same text again 0.5 s later (101 + 3 542 runes), while my own runs of the same
// build never hit it. A float is a race, so the grace closes the window by
// construction instead of hoping the ticks fall elsewhere.
const chatWatchGrace = 3 * time.Second

// postedTexts remembers what this bot delivered on its own, so the watcher does
// not post the same answer a second time. The window is ten minutes rather than
// the three seconds of dupGuard: the watcher reads the conversation on a two
// second beat and the turn that wrote the answer may already be over.
type postedTexts struct {
	mu    sync.Mutex
	items map[string]time.Time
}

func newPostedTexts() *postedTexts {
	return &postedTexts{items: make(map[string]time.Time)}
}

func postedKey(sessionID, text string) string {
	sum := sha256.Sum256([]byte(text))

	return sessionID + "\x00" + hex.EncodeToString(sum[:8])
}

func (p *postedTexts) mark(sessionID, text string) {
	if p == nil || sessionID == "" || strings.TrimSpace(text) == "" {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.items == nil {
		p.items = make(map[string]time.Time)
	}
	now := time.Now()
	for key, at := range p.items {
		if now.Sub(at) > selfPostedKeep {
			delete(p.items, key)
		}
	}
	if len(p.items) >= selfPostedMax {
		for key := range p.items {
			delete(p.items, key)

			break
		}
	}
	p.items[postedKey(sessionID, text)] = now
}

func (p *postedTexts) has(sessionID, text string) bool {
	if p == nil || sessionID == "" {
		return false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	at, ok := p.items[postedKey(sessionID, text)]

	return ok && time.Since(at) <= selfPostedKeep
}

// turnMarks remembers how far into a conversation this bot has already posted on
// its own.
//
// The transcript keeps a turn's assistant text as one message, so a turn that
// wrote four short lines sits there as a single block. Comparing texts does not
// catch that: the block is not equal to any line that was sent. Without the mark
// the watcher repeated the whole block as one long message right after the chat
// had received the same words one by one (20.11.2026, 895 characters at 15:34).
type turnMarks struct {
	mu sync.Mutex
	at map[string]int
	// ended is when a turn of that conversation last recorded its end. The
	// watcher uses it to tell "a turn may still be finishing its own delivery"
	// from "nobody delivered this", and writes it down when an answer slips
	// through anyway (chatwatch.go, postToChats).
	ended map[string]time.Time
}

func newTurnMarks() *turnMarks {
	return &turnMarks{at: make(map[string]int), ended: make(map[string]time.Time)}
}

func (t *turnMarks) remember(sessionID string, at int) {
	if t == nil || sessionID == "" {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.at == nil {
		t.at = make(map[string]int)
	}
	if t.ended == nil {
		t.ended = make(map[string]time.Time)
	}
	if at > t.at[sessionID] {
		t.at[sessionID] = at
	}
	t.ended[sessionID] = time.Now()
}

// endedAgo reports how long ago a turn of this conversation recorded its end. It
// is negative when no turn of this bot has ever marked one.
func (t *turnMarks) endedAgo(sessionID string) time.Duration {
	if t == nil || sessionID == "" {
		return -1
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	at, ok := t.ended[sessionID]
	if !ok {
		return -1
	}

	return time.Since(at)
}

func (t *turnMarks) floor(sessionID string) int {
	if t == nil {
		return 0
	}
	t.mu.Lock()
	defer t.mu.Unlock()

	return t.at[sessionID]
}

// markTurnEnd records that everything the conversation holds right now was
// delivered by this bot's own turn. Called where the turn ends (dispatch.go), so
// the watcher afterwards only sees what appeared later - the answer of a turn no
// chat started.
func (b *Bot) markTurnEnd(sessionID string) {
	if sessionID == "" || b.transcript == nil {
		return
	}
	if msgs, known := b.transcript(sessionID); known {
		b.turnEnd.remember(sessionID, len(msgs))
	}
}

// armWatchers starts a watcher for every conversation a chat holds. It runs once
// at Start, so a bot that restarts keeps watching the conversations people are
// bound to instead of waiting for them to write again.
func (b *Bot) armWatchers(ctx context.Context) {
	if b.transcript == nil || b.store == nil {
		return
	}
	for _, id := range b.store.KnownIDs() {
		b.armWatcher(ctx, id)
	}
}

// armWatcher starts watching one conversation, once. The watcher lives as long
// as the bot unless nobody holds the conversation any more.
func (b *Bot) armWatcher(ctx context.Context, sessionID string) {
	if sessionID == "" || b.transcript == nil {
		return
	}
	b.watchMu.Lock()
	if b.watching == nil {
		b.watching = make(map[string]context.CancelFunc)
	}
	if _, ok := b.watching[sessionID]; ok {
		b.watchMu.Unlock()

		return
	}
	// Where the conversation stands right now, taken before the goroutine runs:
	// counting inside it would let an answer written between arming and the
	// first read be mistaken for history and never posted.
	msgs, known := b.transcript(sessionID)
	seen := len(msgs)
	// A conversation Coddy has not loaded yet answers with nothing, and its
	// length at that moment says nothing about where it stands. Taking that
	// zero as the starting point is what pushed two hundred old answers into a
	// chat at once on 20.11.2026, right after a restart.
	settled := known
	wctx, cancel := context.WithCancel(ctx)
	b.watching[sessionID] = cancel
	b.watchMu.Unlock()
	go b.watchSession(wctx, sessionID, seen, settled)
}

// disarmWatcher stops watching a conversation nobody holds any more.
func (b *Bot) disarmWatcher(sessionID string) {
	b.watchMu.Lock()
	cancel := b.watching[sessionID]
	delete(b.watching, sessionID)
	b.watchMu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// watchSession posts into the chats everything the conversation gains that this
// bot did not write.
func (b *Bot) watchSession(ctx context.Context, sessionID string, seen int, settled bool) {
	pause := b.watchPause
	if pause <= 0 {
		pause = chatWatchPause
	}
	ticker := time.NewTicker(pause)
	defer ticker.Stop()

	// lastActive is when this conversation last ran a turn. It outlives a single
	// tick on purpose: the grace below counts from it, not from the tick.
	var lastActive time.Time

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			keys := b.store.KeysFor(sessionID)
			if len(keys) == 0 {
				b.log.Debug("max: nobody holds the conversation, the watcher stops",
					"session", sessionID)
				b.disarmWatcher(sessionID)

				return
			}
			msgs, known := b.transcript(sessionID)
			if !settled {
				if !known {
					continue
				}
				// The conversation has now been loaded. Everything it holds was
				// written before this watcher existed, so it is history and is
				// never delivered: the person already saw those answers, in the
				// window they were written in.
				seen = len(msgs)
				settled = true
				b.log.Debug("max: the conversation was not loaded when the watcher armed, its history is not replayed",
					"session", sessionID, "messages", len(msgs))

				continue
			}
			if len(msgs) < seen {
				// The conversation was rewritten under us (a compaction or an
				// edit): read it again from its end instead of posting a tail
				// that no longer is one.
				seen = len(msgs)

				continue
			}
			// Our own turns delivered everything up to their end, one line at a
			// time. The transcript keeps that same text as one block, so the
			// watcher starts after the mark and never repeats it.
			if floor := b.turnEnd.floor(sessionID); seen < floor {
				seen = floor
			}
			// A turn that is running, or that stopped running a moment ago,
			// delivers its own answer and only then records where this watcher
			// must start (mirror.go, markTurnEnd). A tick landing between the
			// running flag clearing and that mark used to post the very answer
			// the mirror had just sent: on 22.11.2026 the chat got one block from
			// the mirror and the same text again 0.5 s later. The grace makes the
			// window close by construction instead of hoping the ticks fall
			// elsewhere. The pointer is left where it stands, so nothing is lost -
			// the same block is looked at again once the grace is over.
			grace := b.watchGrace
			if grace <= 0 {
				grace = chatWatchGrace
			}
			if b.turnActive(sessionID) {
				lastActive = time.Now()

				continue
			}
			if !lastActive.IsZero() && time.Since(lastActive) < grace {
				continue
			}
			// A turn of this chat delivers itself; recognising it here is what
			// keeps the answer from arriving twice.
			ours := b.anyOurs(keys)
			for i := seen; i < len(msgs); i++ {
				m := msgs[i]
				if m.Role != llm.RoleAssistant {
					seen = i + 1

					continue
				}
				text := strings.TrimSpace(m.Content)
				if text == "" || ours {
					seen = i + 1

					continue
				}
				if b.selfPosted.has(sessionID, text) {
					seen = i + 1

					continue
				}
				// A turn still running in this conversation delivers this very
				// answer through the mirror (mirror.go), and it wins the race
				// whenever it posts first: on 22.11.2026 both roads posted the same
				// answer 0.47 s apart and the person read it twice. Let the pointer
				// stand and look again at the next tick - after the turn ends, a
				// text the mirror delivered carries its mark, and a text nobody
				// delivered is posted here, which is the case of 20.11.2026.
				if b.turnActive(sessionID) {
					break
				}
				b.postToChats(ctx, sessionID, keys, text)
				seen = i + 1
			}
		}
	}
}

// anyOurs reports whether one of the chats holding the conversation runs a turn
// this bot started.
func (b *Bot) anyOurs(keys []string) bool {
	for _, key := range keys {
		if b.turnOurs(key) {
			return true
		}
	}

	return false
}

// postToChats delivers one answer of a background turn into every chat that
// holds the conversation at this moment.
func (b *Bot) postToChats(ctx context.Context, sessionID string, keys []string, text string) {
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}
	if len(keys) > 1 {
		b.log.Warn("max: one conversation is held by more than one chat",
			"session", sessionID, "keys", strings.Join(keys, " "),
			"note", "every one of them gets the answer")
	}
	c := b.connectedClient()
	if c == nil {
		b.log.Warn("max: answer of a background turn not posted",
			"reason", "no connected client", "session", sessionID)

		return
	}
	for _, key := range keys {
		if b.store.Peek(key) != sessionID {
			b.log.Debug("max: the chat moved before the answer arrived",
				"session", sessionID, "key", key)

			continue
		}
		target, ok := targetOfKey(key)
		if !ok {
			continue
		}
		// The answer takes the road of every other message out (sender.go post):
		// split at the platform's limit, markdown formatting, a retry while the
		// platform is quiet and the outbox when the refusal will stand. It used to
		// be clipped into one message and sent raw, so a long answer arrived cut -
		// on 22.11.2026 a 3 980-unit stub stood in the chat next to the whole
		// answer, its last paragraphs gone and its "**" showing through.
		if b.newSender(ctx, c, target).post(text) {
			b.log.Info("max: answer of a background turn reached the chat",
				"session", sessionID, "user", target.UserID, "chat", target.ChatID,
				"runes", len([]rune(text)))
			// A turn of this conversation recorded its end moments ago: either the
			// grace above has just run out and this is the answer the mirror could
			// not deliver, or - what we want to see - a mark landed too late and
			// the chat got the text twice. Writing it down is how a float stops
			// being invisible: on 22.11.2026 the only trace was two identical
			// blocks in a phone screenshot.
			if ago := b.turnEnd.endedAgo(sessionID); ago >= 0 && ago < chatWatchGrace {
				b.log.Info("max: the answer arrived moments after a turn of this conversation ended",
					"session", sessionID, "runes", len([]rune(text)),
					"ago", ago.Round(time.Millisecond),
					"note", "if the chat shows it twice, the end mark landed after this tick")
			}
		} else {
			b.log.Warn("max: answer of a background turn is waiting for another attempt",
				"session", sessionID, "user", target.UserID, "chat", target.ChatID,
				"runes", len([]rune(text)))
		}
		b.selfPosted.mark(sessionID, text)
	}
}

// targetOfKey turns a chat key back into the address MAX understands. A private
// conversation is keyed by the person (sessionstore.SessionKey) and MAX writes
// to a person by user_id; a group is keyed by the chat and written by chat_id.
func targetOfKey(key string) (Target, bool) {
	parts := strings.Split(key, ":")
	if len(parts) < 3 {
		return Target{}, false
	}
	id, err := strconv.ParseInt(parts[2], 10, 64)
	if err != nil || id == 0 {
		return Target{}, false
	}
	switch parts[1] {
	case "user":
		return Target{UserID: id}, true
	case "chat":
		return Target{ChatID: id}, true
	default:
		// Anything else is a key of some other shape; addressing it would send
		// an answer nowhere and look like success.
		return Target{}, false
	}
}
