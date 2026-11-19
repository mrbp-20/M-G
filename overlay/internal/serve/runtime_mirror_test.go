package serve

import (
	"context"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
)

// senderProbe is an acp.UpdateSender that only records how many updates it
// got; what a mirror's wrapper does with it is the mirror's business.
type senderProbe struct{ updates int }

func (s *senderProbe) SendSessionUpdate(string, interface{}) error {
	s.updates++
	return nil
}
func (s *senderProbe) RequestPermission(context.Context, acp.PermissionRequestParams) (*acp.PermissionResult, error) {
	return nil, nil
}
func (s *senderProbe) RequestQuestion(context.Context, acp.QuestionRequestParams) (*acp.QuestionResult, error) {
	return nil, nil
}

// More than one surface can mirror a turn - the desktop's window server and
// its public HTTP server share the sessions, so both have to see them. The
// mirrors are a set, not a slot: each one registered gets the wrap, and each
// one's stop ends only its own mirroring.
// chatMirrorProbe stands in for a messenger bot: asked for a turn of a session
// it serves, it hands back a sender of its own, which is what the MAX adapter
// does with the chat the session is bound to.
type chatMirrorProbe struct {
	probe *senderProbe
	said  []string
}

func (m *chatMirrorProbe) MirrorTurn(string, acp.UpdateSender) (acp.UpdateSender, func()) {
	return m.probe, func() {}
}

func (m *chatMirrorProbe) Say(_ string, text string) { m.said = append(m.said, text) }

// The MAX adapter registers itself through AddChatMirror, and that registration
// has to land in the very set the turn of another surface looks in.
//
// It stopped doing so on 19.11.2026: the method wrote into a second map while
// TurnToChats and SayToChats read the first. The bot registered where nobody
// looked, so a turn started in the browser was answered there and never reached
// its chat - and because a message typed in the chat itself took the bot's own
// road, the fault looked like it was the platform's.
func TestAChatMirrorIsTheSetTheTurnLooksIn(t *testing.T) {
	rt := &Runtime{}
	m := &chatMirrorProbe{probe: &senderProbe{}}
	withdraw := rt.AddChatMirror(m)

	sender, stop := rt.TurnToChats("sess_1")
	if sender == nil {
		t.Fatal("TurnToChats found nothing: the chat mirror is registered where nobody looks")
	}
	if err := sender.SendSessionUpdate("sess_1", nil); err != nil {
		t.Fatal(err)
	}
	if m.probe.updates != 1 {
		t.Fatalf("the chat sender got %d updates, want 1", m.probe.updates)
	}
	stop()

	rt.SayToChats("sess_1", "привет")
	if len(m.said) != 1 || m.said[0] != "привет" {
		t.Fatalf("SayToChats reached the chat mirror with %v", m.said)
	}

	withdraw()
	if sender, _ := rt.TurnToChats("sess_2"); sender != nil {
		t.Fatal("a withdrawn chat mirror was still offered")
	}

	// The other name for the same set: a surface that offered itself through
	// AddTurnMirror has to be found by the chat road as well, or the two names
	// have drifted apart again.
	other := &Runtime{}
	otherProbe := &chatMirrorProbe{probe: &senderProbe{}}
	other.AddTurnMirror(otherProbe)
	if sender, _ := other.TurnToChats("sess_9"); sender == nil {
		t.Fatal("a mirror registered through AddTurnMirror is invisible to TurnToChats")
	}
}

func TestEveryRegisteredMirrorSeesTheTurn(t *testing.T) {
	rt := &Runtime{}
	first, second := &recordingMirror{}, &recordingMirror{}
	withdrawFirst := rt.AddTurnMirror(first)
	withdrawSecond := rt.AddTurnMirror(second)

	sender, stop := rt.MirrorTurn("sess_1", &senderProbe{})
	if err := sender.SendSessionUpdate("sess_1", nil); err != nil {
		t.Fatal(err)
	}
	if len(first.seen()) != 1 || len(second.seen()) != 1 {
		t.Fatalf("first saw %v, second saw %v", first.seen(), second.seen())
	}

	// Withdrawing one surface's mirror leaves the other in place.
	withdrawFirst()
	_, stop2 := rt.MirrorTurn("sess_2", &senderProbe{})
	if len(first.seen()) != 1 {
		t.Fatalf("the withdrawn mirror still saw %v", first.seen())
	}
	if len(second.seen()) != 2 {
		t.Fatalf("the live mirror saw %v", second.seen())
	}

	withdrawSecond()
	stop()
	stop2()

	// With nothing registered the primary sender comes back untouched and the
	// stop is a no-op.
	primary := &senderProbe{}
	got, stop3 := rt.MirrorTurn("sess_3", primary)
	same, ok := got.(*senderProbe)
	if !ok || same != primary {
		t.Fatal("an empty mirror set did not hand the primary sender back")
	}
	if err := got.SendSessionUpdate("sess_3", nil); err != nil {
		t.Fatal(err)
	}
	stop3()
	if primary.updates != 1 {
		t.Fatalf("the primary sender got %d updates", primary.updates)
	}
}

// SetTurnMirror keeps its single-surface semantics for the callers that have
// one surface: it replaces the whole set, and nil clears it.
func TestSetTurnMirrorReplacesTheWholeSet(t *testing.T) {
	rt := &Runtime{}
	old := &recordingMirror{}
	rt.AddTurnMirror(old)
	fresh := &recordingMirror{}
	rt.SetTurnMirror(fresh)

	if _, stop := rt.MirrorTurn("sess_1", &senderProbe{}); stop == nil {
		t.Fatal("no stop")
	}
	if len(old.seen()) != 0 || len(fresh.seen()) != 1 {
		t.Fatalf("old saw %v, fresh saw %v", old.seen(), fresh.seen())
	}

	rt.SetTurnMirror(nil)
	primary := &senderProbe{}
	got, stop := rt.MirrorTurn("sess_2", primary)
	same, ok := got.(*senderProbe)
	if !ok || same != primary {
		t.Fatal("a cleared set did not hand the primary sender back")
	}
	stop()
}
