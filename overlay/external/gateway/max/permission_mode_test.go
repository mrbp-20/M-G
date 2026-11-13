//go:build gateway || gateway.max

package max

import (
	"context"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
)

// TestSender_PermissionWithoutAModeStampIsAskedNotAllowed guards the defect the
// operator found on 13.11.2026 in the desktop window's «Спрашивать» mode.
//
// The window waited on a permission card while the chat answered it silently:
// the request reached the gateway without a mode stamp, the guard read the empty
// stamp as "no questions" and returned allow at once. An answer that nobody
// pressed is not a person's answer, so the bridge ignored it and the window kept
// waiting - a stuck turn and an approval the person never saw.
//
// An empty stamp means the caller did not say; the caller asks the chat only
// after deciding by itself that a person has to answer, so the chat asks too.
func TestSender_PermissionWithoutAModeStampIsAskedNotAllowed(t *testing.T) {
	f := newSendServer(t)
	b := testBot(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := b.newSender(ctx, f.client(t), Target{UserID: 7})

	done := make(chan *acp.PermissionResult, 1)
	go func() {
		res, err := s.RequestPermission(ctx, acp.PermissionRequestParams{
			SessionID: "sess",
			// No EffectivePermissionMode: the caller did not say.
			ToolCall: acp.PermissionToolCall{
				Title:   "Run: http_request",
				Content: []acp.ToolCallResultItem{{Content: acp.ContentBlock{Type: acp.ContentTypeText, Text: "https://wttr.in/Krasnodar"}}},
			},
			Options: []acp.PermissionOption{
				{OptionID: "allow", Name: "Разрешить", Kind: "allow_once"},
				{OptionID: "reject", Name: "Отклонить", Kind: "reject_once"},
			},
		})
		if err != nil {
			t.Errorf("RequestPermission: %v", err)
		}
		done <- res
	}()

	// A card has to go out: the person is the only one who may answer.
	f.waitSends(t, 1, 5*time.Second)
	select {
	case res := <-done:
		t.Fatalf("the request was answered without asking the person: %+v", res)
	default:
	}

	// The person refuses, and the waiting call ends with that refusal.
	rows := buttons(t, f.sends()[0].body.Attachments)
	if len(rows) == 0 || len(rows[0]) < 2 {
		t.Fatalf("the keyboard was %+v, want a button per option", rows)
	}
	if err := b.HandleCallback(ctx, f.client(t), pressUpdate(t, Callback{
		CallbackID: "cb-1", Payload: rows[0][1].Payload, User: User{UserID: 7},
	})); err != nil {
		t.Fatalf("HandleCallback: %v", err)
	}
	select {
	case res := <-done:
		if res == nil || res.Outcome != "selected" || res.OptionID != "reject" {
			t.Fatalf("the press answered %+v, want the reject option selected", res)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("RequestPermission did not return after the press")
	}
}
