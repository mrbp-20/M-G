//go:build gateway || gateway.max

package max

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// outboxMaxEntries bounds the parked answers. A chat that stays unreachable for
// a day must not grow a state file without end, and an answer older than the
// oldest entry here is no longer an answer to anything.
const outboxMaxEntries = 50

// outboxMaxAge is how long a parked answer is still worth sending. A reply found
// a day later arrives after the person has long given up on it.
const outboxMaxAge = 24 * time.Hour

// outboxEntry is one answer this bot owed a chat and could not deliver.
type outboxEntry struct {
	Target Target    `json:"target"`
	Text   string    `json:"text"`
	At     time.Time `json:"at"`
}

// outbox keeps the answers that MAX refused to take.
//
// It exists because of 19.11.2026: the platform stopped answering for fifteen
// seconds, the turn's single POST timed out and the answer died with the socket.
// The journal said "answer not delivered" while the person saw silence and had
// to ask whether the answer existed at all. A network blip must cost a delay,
// not the answer, so an undelivered text is written down here and sent as soon
// as the bot is connected again.
//
// The file is a plain JSON list in Coddy's state directory. A missing or
// unreadable file is not an error to start with: an empty outbox is exactly what
// a bot that never lost an answer has.
type outbox struct {
	mu      sync.Mutex
	path    string
	entries []outboxEntry
}

// newOutbox loads the parked answers of an earlier run. An empty path keeps the
// outbox in memory only, which is how a test runs it.
func newOutbox(path string) *outbox {
	o := &outbox{path: path}
	if path == "" {
		return o
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return o
	}
	var entries []outboxEntry
	if err := json.Unmarshal(raw, &entries); err != nil {
		// A corrupt file is dropped rather than kept: it holds answers nobody
		// can reconstruct anyway, and refusing to start over it would take the
		// whole gateway down for a detail.
		return o
	}
	now := time.Now()
	for _, e := range entries {
		if e.Text == "" || now.Sub(e.At) > outboxMaxAge {
			continue
		}
		o.entries = append(o.entries, e)
	}
	return o
}

// add parks one answer and writes the file.
func (o *outbox) add(target Target, text string) {
	if o == nil || text == "" {
		return
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	now := time.Now()
	fresh := o.entries[:0]
	for _, e := range o.entries {
		if now.Sub(e.At) <= outboxMaxAge {
			fresh = append(fresh, e)
		}
	}
	fresh = append(fresh, outboxEntry{Target: target, Text: text, At: now})
	if len(fresh) > outboxMaxEntries {
		fresh = fresh[len(fresh)-outboxMaxEntries:]
	}
	o.entries = fresh
	o.saveLocked()
}

// take hands back what is waiting, oldest first.
func (o *outbox) take() []outboxEntry {
	if o == nil {
		return nil
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	out := make([]outboxEntry, len(o.entries))
	copy(out, o.entries)
	return out
}

// drop forgets the first n entries, which are the ones just delivered.
func (o *outbox) drop(n int) {
	if o == nil || n <= 0 {
		return
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if n >= len(o.entries) {
		o.entries = nil
	} else {
		o.entries = o.entries[n:]
	}
	o.saveLocked()
}

// length is what the journal reports when answers are parked or freed.
func (o *outbox) length() int {
	if o == nil {
		return 0
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	return len(o.entries)
}

// saveLocked writes the file through a temporary one and a rename, so a crash
// in the middle leaves the previous list rather than half of both.
func (o *outbox) saveLocked() {
	if o.path == "" {
		return
	}
	raw, err := json.Marshal(o.entries)
	if err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(o.path), 0o755); err != nil {
		return
	}
	tmp := o.path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return
	}
	_ = os.Rename(tmp, o.path)
}
