//go:build gateway || gateway.max

package max

// Polling the update stream.
//
// MAX hands updates out over a long poll: GET /updates with the marker of the
// last page read, answered as soon as something happens or when the hold of
// the request runs out. The loop below calls it once per iteration and gives
// every update to the handler on its own, never the page as a whole: what the
// handler runs is a session turn, and one slow turn must not hold up the rest
// of the page, nor make every caller think in pages.
//
// The pause between two polls is ours, not the client's. The reference client
// of the messenger carries a WithPollingPause option that nothing reads, and
// against a server that answers at once it spins at roughly 2000 requests a
// second (the audit behind client.go). The pause is taken after an answer has
// arrived, never on top of the poll hold: while the server holds an idle
// connection open the loop is already waiting on it, and a pause there would
// delay the first update after it by the interval for nothing. When the server
// does answer at once - an error, or a burst of updates - the pause is the
// only thing keeping the request rate at the level the settings ask for. It is
// skipped in no case: even a page of updates is followed by it, so a busy bot
// polls no faster than an idle one.
//
// The position in the stream is not the loop's business. The cursor lives in
// the client (client.go), in memory and, when the bot was given a file, on
// disk, and only a successful poll moves it. That is why a failed poll is
// returned to the caller as it is: the answer is the caller's to act on, and
// the marker still stands where the last good page left it, so the next call -
// or the next process - resumes there instead of replaying the stream.

import (
	"context"
	"errors"
	"log/slog"
	"time"
)

// Handler takes one update. The loop of a bot hands updates over one at a time
// and in the order the server sent them.
type Handler interface {
	// Handle processes one update. An error is logged and the loop carries
	// on: one update the bot could not handle must not stop it, and must not
	// keep the rest of the page from being handled either.
	Handle(ctx context.Context, u Update) error
}

// PollLoop reads the update stream until ctx is cancelled, handing every
// update to h on its own.
//
// It returns nil when ctx ends and the error of a failed poll otherwise; a
// page whose elements did not all parse is delivered and logged rather than
// returned, and is never fetched twice. The client keeps the position, so
// neither outcome loses it.
//
// The adapter does not call this yet: GET /updates moves the marker as soon as
// it succeeds, so a poll with nobody to hand the updates to would move it past
// messages the bot never read and lose them. Start says the same where it
// waits; the loop arrives in it together with the session handling it serves.
func (b *Bot) PollLoop(ctx context.Context, c *Client, h Handler) error {
	return pollLoop(ctx, c, h, b.pollInterval(), b.log)
}

// pollInterval is the pause the loop keeps between two polls: the configured
// interval, or the one a test set to poll faster than the one-second floor of
// the configuration.
func (b *Bot) pollInterval() time.Duration {
	if b.pollEvery > 0 {
		return b.pollEvery
	}

	return b.cfg.PollInterval()
}

// pollLoop is PollLoop over an interval given outright, so a test can poll
// faster than the configuration allows.
func pollLoop(ctx context.Context, c *Client, h Handler, interval time.Duration, log *slog.Logger) error {
	if log == nil {
		log = slog.Default()
	}
	if interval < 0 {
		interval = 0
	}
	for {
		updates, marker, err := c.fetchUpdates(ctx)
		// Whatever the page held is handed over first, error or not: a page
		// with a malformed element comes back with the elements that did parse
		// beside ErrBadUpdate, and those must not be lost to a log line.
		for _, u := range updates {
			if ctx.Err() != nil {
				// Stopping: the cursor still stands before this page, so the
				// next process replays it, and the handler's de-dup by
				// message_id skips what already ran.
				return nil
			}
			if herr := h.Handle(ctx, u); herr != nil {
				log.Warn("max: update not handled", "err", herr,
					"type", u.UpdateType, "chat_id", u.ChatID, "message_id", u.MessageID)
			}
		}
		// The cursor moves only after the whole page is handled. A stop in the
		// middle of it leaves the marker where the last good page ended, so the
		// page is replayed rather than its unhandled rest lost.
		if marker > 0 {
			if serr := c.saveCursor(marker); serr != nil {
				return serr
			}
		}
		switch {
		case err == nil:
		case errors.Is(err, ErrBadUpdate):
			// Part of the page did not parse. What did parse was delivered
			// above and the client moved the marker past the whole page, so the
			// loop carries on: one bad element neither loses the rest of the
			// page nor spins on the same page forever.
			log.Warn("max: page with a malformed update", "err", err, "delivered", len(updates))
		case ctx.Err() != nil:
			// The poll failed because the bot is stopping. That is the exit
			// the caller asked for, not a failure to report.
			return nil
		default:
			// A failed poll goes to the caller: it knows whether to back off,
			// to log or to stop, and the cursor of the client still stands
			// where the last good page ended.
			return err
		}
		if ctx.Err() != nil {
			return nil
		}
		// The pause after the answer, never over the hold of the next poll:
		// see the file comment.
		t := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			t.Stop()
			return nil
		case <-t.C:
		}
	}
}
