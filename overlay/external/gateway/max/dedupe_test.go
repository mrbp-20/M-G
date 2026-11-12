//go:build gateway || gateway.max

package max

import (
	"testing"
	"time"
)

func TestDupGuard_DropsTheSameLineTwice(t *testing.T) {
	g := newDupGuard()
	target := Target{ChatID: 100}

	if g.repeat(target, "готово") {
		t.Fatal("the first line is never a repeat")
	}
	if !g.repeat(target, "готово") {
		t.Fatal("the same line to the same chat must be recognised as a repeat")
	}
}

func TestDupGuard_LetsADifferentLineThrough(t *testing.T) {
	g := newDupGuard()
	target := Target{ChatID: 100}

	g.repeat(target, "готово")
	if g.repeat(target, "готово, проверяю") {
		t.Fatal("a different line is not a repeat")
	}
}

func TestDupGuard_KeepsTargetsApart(t *testing.T) {
	g := newDupGuard()

	g.repeat(Target{UserID: 7}, "готово")
	if g.repeat(Target{ChatID: 100}, "готово") {
		t.Fatal("the same text in another chat is not a repeat")
	}
}

func TestDupGuard_ForgetsAfterTheWindow(t *testing.T) {
	g := newDupGuard()
	target := Target{ChatID: 100}

	g.repeat(target, "готово")
	// Age every remembered line past the window: a repeat an hour later is a
	// new message, not a copy of the same one.
	g.mu.Lock()
	for k := range g.last {
		g.last[k] = time.Now().Add(-duplicateWindow - time.Second)
	}
	g.mu.Unlock()

	if g.repeat(target, "готово") {
		t.Fatal("a line repeated long after the window must go out")
	}
}

func TestDupGuard_WithoutAStoreSaysNo(t *testing.T) {
	var g *dupGuard
	if g.repeat(Target{ChatID: 100}, "готово") {
		t.Fatal("a missing guard never reports a repeat")
	}
}
