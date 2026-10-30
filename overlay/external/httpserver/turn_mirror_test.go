//go:build http

package httpserver

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// chatWatch stands in for the chat a browser turn is mirrored into: it answers at
// once, or waits for the turn to end and records why its prompt was withdrawn.
type chatWatch struct {
	question *acp.QuestionResult
	perm     *acp.PermissionResult
	wait     bool

	mu    sync.Mutex
	cause error
}

func (c *chatWatch) SendSessionUpdate(string, interface{}) error { return nil }

func (c *chatWatch) RequestQuestion(ctx context.Context, _ acp.QuestionRequestParams) (*acp.QuestionResult, error) {
	c.hold(ctx)

	return c.question, nil
}

func (c *chatWatch) RequestPermission(ctx context.Context, _ acp.PermissionRequestParams) (*acp.PermissionResult, error) {
	c.hold(ctx)

	return c.perm, nil
}

func (c *chatWatch) hold(ctx context.Context) {
	if !c.wait {
		return
	}
	<-ctx.Done()
	c.mu.Lock()
	c.cause = context.Cause(ctx)
	c.mu.Unlock()
}

func (c *chatWatch) withdrawal() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.cause
}

// waitForEvent waits until the composer stream carries the frame.
func waitForEvent(t *testing.T, stream *syncBuffer, event string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(stream.String(), event) {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("the composer never got %q", event)
}

// waitForWithdrawal waits until the chat's prompt records how it ended.
func waitForWithdrawal(t *testing.T, c *chatWatch) error {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cause := c.withdrawal(); cause != nil {
			return cause
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatal("the chat's prompt was left waiting after the page answered")

	return nil
}

// TestRequestQuestion_AsksTheChatTheTurnReadsIn is the report from the chat: a
// turn started in the browser showed its tool cards there and never asked the
// question, so the person reading the chat had nothing to answer.
func TestRequestQuestion_AsksTheChatTheTurnReadsIn(t *testing.T) {
	stream := &syncBuffer{}
	sender := NewSender(&config.Config{}, stream, true, "agent-model")
	sender.extra = &chatWatch{question: &acp.QuestionResult{Answers: [][]string{{"из чата"}}}}

	res, err := sender.RequestQuestion(context.Background(), acp.QuestionRequestParams{
		SessionID: "s-chat",
		RequestID: "r-chat",
		Questions: []acp.QuestionPrompt{{Question: "Что делаем?", Options: []acp.QuestionOption{{Label: "Первое"}, {Label: "Второе"}}}},
	})
	if err != nil {
		t.Fatalf("RequestQuestion: %v", err)
	}
	if res == nil || len(res.Answers) != 1 {
		t.Fatalf("the question came back as %#v", res)
	}
	if len(res.Answers[0]) != 1 || res.Answers[0][0] != "из чата" {
		t.Fatalf("the answer was %#v, want the chat's", res.Answers)
	}
	if QuestionPending("s-chat") {
		t.Fatal("the page's prompt was left waiting after the chat answered")
	}
}

// TestRequestQuestion_ThePageAnswerWithdrawsTheChatPrompt: when the person
// answers in the browser, the chat says so instead of keeping a keyboard that
// does nothing.
func TestRequestQuestion_ThePageAnswerWithdrawsTheChatPrompt(t *testing.T) {
	stream := &syncBuffer{}
	sender := NewSender(&config.Config{}, stream, true, "agent-model")
	watch := &chatWatch{wait: true}
	sender.extra = watch

	done := make(chan *acp.QuestionResult, 1)
	go func() {
		res, err := sender.RequestQuestion(context.Background(), acp.QuestionRequestParams{
			SessionID: "s-page",
			RequestID: "r-page",
			Questions: []acp.QuestionPrompt{{Question: "Что делаем?", Options: []acp.QuestionOption{{Label: "Первое"}}}},
		})
		if err != nil {
			t.Errorf("RequestQuestion: %v", err)
		}
		done <- res
	}()
	waitForEvent(t, stream, "event: question")
	if ok := CompleteQuestionAnswer("s-page", "r-page", &acp.QuestionResult{Answers: [][]string{{"из браузера"}}}); !ok {
		t.Fatal("CompleteQuestionAnswer failed")
	}
	select {
	case res := <-done:
		if res == nil || len(res.Answers) != 1 || res.Answers[0][0] != "из браузера" {
			t.Fatalf("the answer was %#v, want the page's", res)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the turn did not continue after the page answered")
	}
	if cause := waitForWithdrawal(t, watch); !errors.Is(cause, session.ErrAnsweredElsewhere) {
		t.Fatalf("the chat's prompt ended with %v, want it withdrawn as answered elsewhere", cause)
	}
}

// TestRequestPermission_AsksTheChatToo: a tool call of a subagent stops the whole
// turn, so the chat has to be able to allow it even when the turn was started in
// the browser.
func TestRequestPermission_AsksTheChatToo(t *testing.T) {
	stream := &syncBuffer{}
	sender := NewSender(&config.Config{}, stream, true, "agent-model")
	sender.extra = &chatWatch{perm: &acp.PermissionResult{Outcome: "selected", OptionID: "allow"}}

	res, err := sender.RequestPermission(context.Background(), acp.PermissionRequestParams{
		SessionID: "s-perm",
		ToolCall:  acp.PermissionToolCall{ToolCallID: "call_1", Title: "Пишет файл"},
		Options: []acp.PermissionOption{
			{OptionID: "allow", Name: "Разрешить", Kind: "allow_once"},
			{OptionID: "reject", Name: "Отклонить", Kind: "reject_once"},
		},
	})
	if err != nil {
		t.Fatalf("RequestPermission: %v", err)
	}
	if res == nil || res.Outcome != "selected" || res.OptionID != "allow" {
		t.Fatalf("the prompt answered %+v, want the chat's allow", res)
	}
}
