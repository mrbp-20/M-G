//go:build gateway || gateway.max

package max

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
)

func TestFirstLine_IsTheFirstLineCut(t *testing.T) {
	if got := firstLine("  первая строка\nвторая\n"); got != "первая строка" {
		t.Errorf("firstLine = %q, want the first line only", got)
	}
	if got := firstLine("   \n\n"); got != "" {
		t.Errorf("firstLine of blank text = %q, want empty", got)
	}
	if n := len([]rune(firstLine(strings.Repeat("я", 400)))); n > 160 {
		t.Errorf("firstLine kept %d runes, want at most 160", n)
	}
}

func TestRefusalOfNonAdmin_IsWrittenOnlyForARestrictedTurn(t *testing.T) {
	var logs bytes.Buffer
	b := testBot(t)
	b.log = slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelInfo}))

	// A turn of an admin runs without the restriction: a failed call in it is
	// the agent's own business and must not reach the journal this way.
	admin := &Sender{b: b}
	admin.refusalOfNonAdmin("", "failed", nil)
	if logs.Len() != 0 {
		t.Fatalf("an unrestricted turn wrote %q", logs.String())
	}

	// A call that finished is not a refusal either.
	restricted := &Sender{b: b}
	restricted.refuseApprovals = true
	restricted.refusalOfNonAdmin("", "completed", nil)
	if logs.Len() != 0 {
		t.Fatalf("a completed call was written as a refusal: %q", logs.String())
	}

	restricted.refusalOfNonAdmin("", "failed", nil)
	out := logs.String()
	if !strings.Contains(out, "refusal to a non-admin") {
		t.Fatalf("no refusal line for a restricted turn: %q", out)
	}
	if !strings.Contains(out, "status=failed") {
		t.Errorf("the line names no status: %q", out)
	}
	if !strings.Contains(out, "the core stopped the call") {
		t.Errorf("the line gives no reason when the core sent none: %q", out)
	}
}
