//go:build scheduler || http

package serve

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/bgtask"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
)

// TestJobRunFinished_OnlyAScheduledRunThatEnded: a report is carried for the
// finish of a scheduled run alone - both outcomes, since a job that failed is
// news the person asked for - so a subagent of an ordinary turn, or a run still
// going, never writes into the conversation.
func TestJobRunFinished_OnlyAScheduledRunThatEnded(t *testing.T) {
	done := bgtask.Snapshot{
		ID: "bg_1", Kind: bgtask.KindAgent, Status: bgtask.StatusSucceeded,
		Agent: &bgtask.AgentInfo{SessionID: "sess_run"},
	}
	if !jobRunFinished(done) {
		t.Fatal("a finished scheduled run was not recognised")
	}

	failed := done
	failed.Status = bgtask.StatusFailed
	failed.Error = "model was silent"
	if !jobRunFinished(failed) {
		t.Fatal("a failed scheduled run was not recognised")
	}

	running := done
	running.Status = bgtask.StatusRunning
	if jobRunFinished(running) {
		t.Fatal("a run still going was reported")
	}

	command := bgtask.Snapshot{ID: "bg_2", Kind: bgtask.KindCommand, Status: bgtask.StatusSucceeded}
	if jobRunFinished(command) {
		t.Fatal("a command was reported as a job")
	}

	noSession := done
	noSession.Agent = &bgtask.AgentInfo{}
	if jobRunFinished(noSession) {
		t.Fatal("a run without its own session was reported")
	}
}

// TestReadRunBundle_AnswersForARetiredSession: the run's own bundle answers for
// it after its session is gone - the job it belongs to and the answer it ended
// with - so a report does not depend on the live session still being there.
func TestReadRunBundle_AnswersForARetiredSession(t *testing.T) {
	dir := t.TempDir()
	meta := `{"version":1,"id":"sess_run","schedulerJobId":"procedury-na-28-10","schedulerTrigger":"cron"}`
	if err := os.WriteFile(filepath.Join(dir, "session.json"), []byte(meta), 0o600); err != nil {
		t.Fatalf("session.json: %v", err)
	}
	bundle := `{"version":1,"messages":[` +
		`{"role":"user","content":"покажи процедуры"},` +
		`{"role":"assistant","content":[{"type":"text","text":"Завтра, 28.10: ЛФК в 10:00"}]},` +
		`{"role":"assistant","content":"<coddy_file id=\"a1\" />\n\nВсего 11 записей"}]}`
	if err := os.WriteFile(filepath.Join(dir, "messages.json"), []byte(bundle), 0o600); err != nil {
		t.Fatalf("messages.json: %v", err)
	}

	if jobID := readRunJobID(filepath.Join(dir, "session.json")); jobID != "procedury-na-28-10" {
		t.Fatalf("job = %q", jobID)
	}
	if got, want := readRunAnswer(filepath.Join(dir, "messages.json")), "Всего 11 записей"; got != want {
		t.Fatalf("answer = %q, want %q", got, want)
	}
	if got := readRunAnswer(filepath.Join(dir, "нет-такого.json")); got != "" {
		t.Fatalf("a missing bundle answered %q", got)
	}
}

// TestLastReportText_IsTheAnswerTheRunEndedWith: the report is the last answer
// with text, and the file placement markers the web UI needs stay out of a
// message a person reads in a chat.
func TestLastReportText_IsTheAnswerTheRunEndedWith(t *testing.T) {
	msgs := []llm.Message{
		{Role: llm.RoleUser, Content: "проверь обновления"},
		{Role: llm.RoleAssistant, Content: "сначала проверяю"},
		{Role: llm.RoleAssistant, Content: "  "},
		{Role: llm.RoleAssistant, Content: "<coddy_file id=\"a1\" />\n\nПавел выпустил 1.3.58"},
	}
	got := lastReportText(msgs)
	want := "Павел выпустил 1.3.58"
	if got != want {
		t.Fatalf("report = %q, want %q", got, want)
	}
	if text := lastReportText([]llm.Message{{Role: llm.RoleUser, Content: "только вопрос"}}); text != "" {
		t.Fatalf("a transcript without an answer gave %q", text)
	}
}

// TestReportJobRun_WithoutASessionManagerDoesNothing: a process that runs no
// sessions has nothing to read, and a report must not be invented.
func TestReportJobRun_WithoutASessionManagerDoesNothing(t *testing.T) {
	rt := &Runtime{}
	rt.reportJobRun(bgtask.Snapshot{
		ID: "bg_3", Kind: bgtask.KindAgent, Status: bgtask.StatusSucceeded,
		StartedAt: time.Now(), Agent: &bgtask.AgentInfo{SessionID: "sess_run"},
	}, nil)
}
