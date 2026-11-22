package config

import "testing"

// TestMaxGatewayConfig_ToolCardsHidden pins the default and the override: a
// group hides the cards of a turn's tool calls, a direct chat shows them, and a
// chat entry that says what it wants wins over both.
func TestMaxGatewayConfig_ToolCardsHidden(t *testing.T) {
	cfg := &MaxGatewayConfig{Chats: []GatewayChatConfig{
		{ChatID: -100, Workspace: "/tmp/group"},                           // a group with no word on cards
		{ChatID: -200, Workspace: "/tmp/other", ToolCards: ToolCardsShow}, // asked for them back
		{ChatID: 500, ToolCards: ToolCardsHide},                           // a direct chat that wants them gone
		{ChatID: 501},                                                     // a plain direct chat
	}}

	cases := []struct {
		name string
		chat int64
		want bool
	}{
		{"group without a word", -100, true},
		{"group asking for the cards", -200, false},
		{"an unlisted group", -300, true},
		{"direct chat hiding them", 500, true},
		{"plain direct chat", 501, false},
		{"a chat never mentioned", 777, false},
	}
	for _, c := range cases {
		if got := cfg.ToolCardsHidden(c.chat); got != c.want {
			t.Errorf("%s (chat %d): ToolCardsHidden = %v, want %v", c.name, c.chat, got, c.want)
		}
	}

	var none *MaxGatewayConfig
	if !none.ToolCardsHidden(-100) {
		t.Error("a missing configuration must still default a group to hidden cards")
	}
}

// TestChatToolCardsSurviveTheSettingsForm: the field travels through the shared
// chat shape of the JSON settings, so a save from the web form does not wipe it.
func TestChatToolCardsSurviveTheSettingsForm(t *testing.T) {
	in := []GatewayChatConfig{{ChatID: -100, Workspace: "/tmp/group", ToolCards: ToolCardsHide}}

	out := chatsFromJSON(chatsToJSON(in))
	if len(out) != 1 {
		t.Fatalf("chats = %d, want 1", len(out))
	}
	if out[0].ToolCards != ToolCardsHide {
		t.Fatalf("ToolCards = %q after the round trip, want %q", out[0].ToolCards, ToolCardsHide)
	}
	if out[0].Workspace != "/tmp/group" {
		t.Fatalf("Workspace = %q after the round trip", out[0].Workspace)
	}
}
