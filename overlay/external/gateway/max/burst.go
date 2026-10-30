//go:build gateway || gateway.max

package max

// A person writes a line and then sends the screenshot it is about. MAX makes
// each its own message, a second or two apart, so answering them as two turns
// would split one question: the text would be answered before the picture
// arrived, and the picture would land afterwards as a separate question. The
// messages of a chat are therefore held for a moment and run as one turn.

import (
	"context"
	"sync"
	"time"
)

// defaultBurstWindow is how long a message waits for the rest of its burst.
// MAX delivered the text and the picture of one question 2.1 s apart, so the
// window has to outlast that. A turn of its own takes far longer than the wait.
const defaultBurstWindow = 3 * time.Second

// burstBuffer holds the messages of each chat until its burst is complete.
type burstBuffer struct {
	mu      sync.Mutex
	window  time.Duration
	flush   func(ctx context.Context, in inbound, key string)
	pending map[string]*burstEntry
}

// burstEntry is one open burst: what arrived so far and the clock that ends it.
type burstEntry struct {
	ctx   context.Context
	in    inbound
	timer *time.Timer
}

func newBurstBuffer(window time.Duration, flush func(context.Context, inbound, string)) *burstBuffer {
	if window <= 0 {
		window = defaultBurstWindow
	}
	return &burstBuffer{window: window, flush: flush, pending: make(map[string]*burstEntry)}
}

// add takes one message of a chat into its burst and restarts the clock that
// ends it, so a person writing several messages is answered once.
func (bb *burstBuffer) add(ctx context.Context, in inbound, key string) {
	bb.mu.Lock()
	defer bb.mu.Unlock()
	if e, ok := bb.pending[key]; ok {
		e.in = mergeInbound(e.in, in)
		e.timer.Reset(bb.window)
		return
	}
	e := &burstEntry{ctx: ctx, in: in}
	bb.pending[key] = e
	e.timer = time.AfterFunc(bb.window, func() { bb.fire(key) })
}

// fire delivers the completed burst of one chat.
func (bb *burstBuffer) fire(key string) {
	bb.mu.Lock()
	e, ok := bb.pending[key]
	if !ok {
		bb.mu.Unlock()
		return
	}
	delete(bb.pending, key)
	bb.mu.Unlock()
	bb.flush(e.ctx, e.in, key)
}

// flushKey delivers what a chat still holds, for a command that must not wait
// behind the burst it interrupts.
func (bb *burstBuffer) flushKey(key string) {
	bb.mu.Lock()
	e, ok := bb.pending[key]
	if !ok {
		bb.mu.Unlock()
		return
	}
	delete(bb.pending, key)
	e.timer.Stop()
	bb.mu.Unlock()
	bb.flush(e.ctx, e.in, key)
}

// flushAll delivers every held burst: what is held here is already past the
// cursor, so a stop that dropped it would lose the message for good.
func (bb *burstBuffer) flushAll() {
	bb.mu.Lock()
	held := bb.pending
	bb.pending = make(map[string]*burstEntry)
	bb.mu.Unlock()
	for key, e := range held {
		e.timer.Stop()
		bb.flush(e.ctx, e.in, key)
	}
}

// mergeInbound folds the next message of a burst into the one before it: the
// texts follow each other and the pictures add up.
func mergeInbound(a, b inbound) inbound {
	switch {
	case b.text == "":
	case a.text == "":
		a.text = b.text
	default:
		a.text += "\n\n" + b.text
	}
	a.images = append(a.images, b.images...)
	a.files = append(a.files, b.files...)
	if b.command != "" {
		a.command, a.args = b.command, b.args
	}
	a.parts += b.parts
	return a
}
