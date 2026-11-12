//go:build gateway || gateway.max

package max

import (
	"strconv"
	"sync"
	"time"
)

// duplicateWindow is how long a line just posted is remembered. One turn of a
// conversation can reach a chat along two paths at once - the turn's own sender
// and the chat mirror that follows the session - and both end in the same call
// to the platform. The copies leave half a second apart, so a short window
// catches them without touching a genuine repeat typed or answered later.
const duplicateWindow = 3 * time.Second

// dupGuard remembers what this bot has just posted, so a second path cannot
// post the same line into the same chat again. The window is time-based on
// purpose: the two paths run in different goroutines and share no identifier.
type dupGuard struct {
	mu   sync.Mutex
	last map[string]time.Time
}

func newDupGuard() *dupGuard {
	return &dupGuard{last: make(map[string]time.Time)}
}

// repeat answers whether this exact text already went to this exact target
// inside the window, remembering it when it did not.
func (g *dupGuard) repeat(target Target, text string) bool {
	if g == nil {
		return false
	}
	key := strconv.FormatInt(target.UserID, 10) + ":" + strconv.FormatInt(target.ChatID, 10) + "\x00" + text
	now := time.Now()

	g.mu.Lock()
	defer g.mu.Unlock()
	if seen, ok := g.last[key]; ok && now.Sub(seen) < duplicateWindow {
		g.last[key] = now

		return true
	}
	if len(g.last) >= dupGuardMaxEntries {
		for k, at := range g.last {
			if now.Sub(at) >= duplicateWindow {
				delete(g.last, k)
			}
		}
	}
	g.last[key] = now

	return false
}

// dupGuardMaxEntries bounds the memory a chatty day can leave behind; the sweep
// above runs only when the map has grown past it.
const dupGuardMaxEntries = 512
