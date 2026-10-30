//go:build gateway || gateway.max

package max

import (
	"context"
	"sync"
	"testing"
	"time"
)

// burstCapture records what the buffer delivers, so a test can tell one turn
// from two.
type burstCapture struct {
	mu    sync.Mutex
	items []inbound
	keys  []string
	done  chan struct{}
}

func newBurstCapture() *burstCapture {
	return &burstCapture{done: make(chan struct{}, 16)}
}

func (c *burstCapture) flush(_ context.Context, in inbound, key string) {
	c.mu.Lock()
	c.items = append(c.items, in)
	c.keys = append(c.keys, key)
	c.mu.Unlock()
	c.done <- struct{}{}
}

func (c *burstCapture) wait(t *testing.T) inbound {
	t.Helper()
	select {
	case <-c.done:
	case <-time.After(2 * time.Second):
		t.Fatal("the burst was never delivered")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.items[len(c.items)-1]
}

func (c *burstCapture) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.items)
}

// TestBurst_TextThenPictureRunsOneTurn is the case from the chat: a line is
// written, and the screenshot it is about follows two seconds later.
func TestBurst_TextThenPictureRunsOneTurn(t *testing.T) {
	c := newBurstCapture()
	bb := newBurstBuffer(80*time.Millisecond, c.flush)

	bb.add(context.Background(), inbound{text: "смотри, что показывает", parts: 1}, "k")
	time.Sleep(10 * time.Millisecond)
	bb.add(context.Background(), inbound{images: []ImageAttachment{{PhotoID: 47130960806}}, parts: 1}, "k")

	got := c.wait(t)
	if got.text != "смотри, что показывает" {
		t.Errorf("text = %q, want the line that was written", got.text)
	}
	if len(got.images) != 1 || got.images[0].PhotoID != 47130960806 {
		t.Errorf("images = %+v, want the screenshot of the same burst", got.images)
	}
	if got.parts != 2 {
		t.Errorf("parts = %d, want 2", got.parts)
	}
	time.Sleep(200 * time.Millisecond)
	if n := c.count(); n != 1 {
		t.Errorf("turns = %d, want 1: the picture must not run its own", n)
	}
}

// TestBurst_PictureFirstThenTextMergesToo: the order the person sends them in
// is not something the bot can rely on.
func TestBurst_PictureFirstThenTextMergesToo(t *testing.T) {
	c := newBurstCapture()
	bb := newBurstBuffer(80*time.Millisecond, c.flush)

	bb.add(context.Background(), inbound{images: []ImageAttachment{{PhotoID: 5}}, parts: 1}, "k")
	time.Sleep(10 * time.Millisecond)
	bb.add(context.Background(), inbound{text: "что тут не так?", parts: 1}, "k")

	got := c.wait(t)
	if got.text != "что тут не так?" {
		t.Errorf("text = %q, want the caption of the burst", got.text)
	}
	if len(got.images) != 1 {
		t.Errorf("images = %d, want 1", len(got.images))
	}
}

// TestBurst_OneMessageGoesAlone: a single message still runs, after the window.
func TestBurst_OneMessageGoesAlone(t *testing.T) {
	c := newBurstCapture()
	bb := newBurstBuffer(40*time.Millisecond, c.flush)

	start := time.Now()
	bb.add(context.Background(), inbound{text: "привет", parts: 1}, "k")
	got := c.wait(t)
	if got.text != "привет" {
		t.Errorf("text = %q, want %q", got.text, "привет")
	}
	if waited := time.Since(start); waited < 30*time.Millisecond {
		t.Errorf("delivered after %v, want the window to be waited out", waited)
	}
}

// TestBurst_ThreeMessagesBecomeOne: a person typing in pieces is answered once.
func TestBurst_ThreeMessagesBecomeOne(t *testing.T) {
	c := newBurstCapture()
	bb := newBurstBuffer(80*time.Millisecond, c.flush)

	bb.add(context.Background(), inbound{text: "первая", parts: 1}, "k")
	time.Sleep(5 * time.Millisecond)
	bb.add(context.Background(), inbound{text: "вторая", parts: 1}, "k")
	time.Sleep(5 * time.Millisecond)
	bb.add(context.Background(), inbound{images: []ImageAttachment{{PhotoID: 9}}, parts: 1}, "k")

	got := c.wait(t)
	if want := "первая\n\nвторая"; got.text != want {
		t.Errorf("text = %q, want %q", got.text, want)
	}
	if got.parts != 3 {
		t.Errorf("parts = %d, want 3", got.parts)
	}
}

// TestBurst_ACommandDoesNotWait: the menu is answered at once, and what the
// chat held before it goes first, so the order of what was written holds.
func TestBurst_ACommandDoesNotWait(t *testing.T) {
	c := newBurstCapture()
	bb := newBurstBuffer(5*time.Second, c.flush)

	bb.add(context.Background(), inbound{text: "пока подумай", parts: 1}, "k")
	bb.flushKey("k")

	select {
	case <-c.done:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("a command waited behind the burst it interrupts")
	}
	c.mu.Lock()
	got := c.items[len(c.items)-1]
	c.mu.Unlock()
	if got.text != "пока подумай" {
		t.Errorf("text = %q, want what the chat held", got.text)
	}
}

// TestBurst_ChatsDoNotMerge: two people writing at once are two questions.
func TestBurst_ChatsDoNotMerge(t *testing.T) {
	c := newBurstCapture()
	bb := newBurstBuffer(40*time.Millisecond, c.flush)

	bb.add(context.Background(), inbound{text: "из первого чата", parts: 1}, "k1")
	bb.add(context.Background(), inbound{text: "из второго чата", parts: 1}, "k2")

	c.wait(t)
	c.wait(t)
	if n := c.count(); n != 2 {
		t.Errorf("deliveries = %d, want 2", n)
	}
}

// TestBurst_FlushAllLosesNothingOnStop: what is held is already past the
// cursor, so a stop hands it over instead of dropping it.
func TestBurst_FlushAllLosesNothingOnStop(t *testing.T) {
	c := newBurstCapture()
	bb := newBurstBuffer(5*time.Second, c.flush)

	bb.add(context.Background(), inbound{text: "раз", parts: 1}, "k1")
	bb.add(context.Background(), inbound{text: "два", images: []ImageAttachment{{PhotoID: 3}}, parts: 1}, "k2")
	bb.flushAll()

	if n := c.count(); n != 2 {
		t.Fatalf("deliveries = %d, want 2", n)
	}
	// A second stop finds nothing left to deliver.
	bb.flushAll()
	if n := c.count(); n != 2 {
		t.Errorf("deliveries = %d after a second stop, want 2", n)
	}
}

// TestHandle_DeliversABurstAsOneTurn proves the wiring, not just the buffer.
func TestHandle_DeliversABurstAsOneTurn(t *testing.T) {
	r := newStubRunner()
	b := newTestBot(r)

	if err := b.Handle(context.Background(), inboundMsg("m1", "проверь это")); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if err := b.Handle(context.Background(), inboundMsg("m2", "и вот снимок")); err != nil {
		t.Fatalf("Handle: %v", err)
	}

	got := r.waitTurn(t)
	if want := "проверь это\n\nи вот снимок"; got != want {
		t.Errorf("prompt = %q, want %q", got, want)
	}
	time.Sleep(100 * time.Millisecond)
	if n := r.promptCount(); n != 1 {
		t.Errorf("turns = %d, want 1", n)
	}
}
