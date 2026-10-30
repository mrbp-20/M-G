//go:build gateway || gateway.max

package max

// Unit tests of the poll loop, against a fake Bot API of our own rather than
// the real one: the pause between two polls, the exit on cancellation, the
// cursor a failed poll leaves alone, a page with a malformed element and the
// delivery of a page one update at a time. TestLiveMe is the one check that
// needs the real API.

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
)

const testToken = "test-token"

// testInterval is the pause the tests run the loop at. 200 ms is below the
// one-second floor of the configuration, which is why the loop is reachable
// through pollLoop and pollEvery at all.
const testInterval = 200 * time.Millisecond

// pollServer is a stand-in Bot API for the poll loop: GET /updates is answered
// from a scripted list of pages, and every poll that arrived is recorded with
// the marker it carried and when it came in.
type pollServer struct {
	srv   *httptest.Server
	polls chan struct{}

	mu    sync.Mutex
	pages []pollPage
	seen  []pollArrival
	// hold is how long an answer is held back, standing for the long poll of
	// MAX: a request waits until something happens or the client gives up.
	hold time.Duration
}

// pollPage is one scripted answer. A page beyond the script is an empty one.
type pollPage struct {
	// updates are raw JSON elements; an element that is not an object is one
	// the client cannot parse, which is how ErrBadUpdate is reproduced.
	updates []string
	marker  int64
	// status, when not 2xx, is the refusal the poll is answered with.
	status int
}

// pollArrival is one poll that reached the server.
type pollArrival struct {
	marker string
	at     time.Time
}

func newPollServer(t *testing.T, pages ...pollPage) *pollServer {
	t.Helper()
	s := &pollServer{pages: pages, polls: make(chan struct{}, 64)}
	s.srv = httptest.NewServer(http.HandlerFunc(s.handle))
	t.Cleanup(s.srv.Close)

	return s
}

func (s *pollServer) handle(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/updates" {
		http.NotFound(w, r)
		return
	}
	s.mu.Lock()
	n := len(s.seen)
	page := pollPage{}
	if n < len(s.pages) {
		page = s.pages[n]
	}
	s.seen = append(s.seen, pollArrival{marker: r.URL.Query().Get("marker"), at: time.Now()})
	hold := s.hold
	s.mu.Unlock()
	select {
	case s.polls <- struct{}{}:
	default:
	}
	if hold > 0 {
		select {
		case <-time.After(hold):
		case <-r.Context().Done():
			return
		}
	}
	if page.status != 0 {
		w.WriteHeader(page.status)
		_, _ = w.Write([]byte(`{"code":"internal","message":"boom"}`))
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = fmt.Fprintf(w, `{"updates":[%s],"marker":%d}`, strings.Join(page.updates, ","), page.marker)
}

// client talks to the fake with a cursor file in a directory of the test.
func (s *pollServer) client(t *testing.T, cursorPath string) *Client {
	t.Helper()

	return NewClient(testToken, WithHTTPClient(s.srv.Client()), WithBaseURL(s.srv.URL),
		WithPollTimeout(5*time.Second), WithCursor(cursorPath))
}

func (s *pollServer) arrivals() []pollArrival {
	s.mu.Lock()
	defer s.mu.Unlock()

	return slices.Clone(s.seen)
}

// waitPolls waits until n polls have reached the server, or fails the test with
// what it was waiting for.
func (s *pollServer) waitPolls(t *testing.T, n int, within time.Duration, what string) {
	t.Helper()
	deadline := time.After(within)
	for {
		if len(s.arrivals()) >= n {
			return
		}
		select {
		case <-s.polls:
		case <-deadline:
			t.Fatalf("%s: %d polls within %v, want %d", what, len(s.arrivals()), within, n)
		}
	}
}

// messageUpdate is one message_created update in the shape the API sends it:
// the id of the message sits in its body.
func messageUpdate(mid string, userID int64, text string) string {
	return fmt.Sprintf(`{"update_type":"message_created","timestamp":1,`+
		`"message":{"sender":{"user_id":%d,"name":"Tester"},"body":{"mid":%q,"seq":1,"text":%q}}}`,
		userID, mid, text)
}

// recorder is a Handler that remembers what it was given.
type recorder struct {
	mu    sync.Mutex
	got   []Update
	sizes []int // how many updates each call carried
	// failAt is the 1-based number of an update the handler refuses, 0 for
	// none: the loop must log it and carry on.
	failAt int
}

func (r *recorder) Handle(_ context.Context, u Update) error {
	r.mu.Lock()
	r.got = append(r.got, u)
	n := len(r.got)
	r.sizes = append(r.sizes, 1)
	r.mu.Unlock()
	if r.failAt == n {
		return fmt.Errorf("max: the test handler refuses update %d", n)
	}

	return nil
}

func (r *recorder) updates() []Update {
	r.mu.Lock()
	defer r.mu.Unlock()

	return slices.Clone(r.got)
}

// mids is the id of every message the handler was given, in the order it saw
// them.
func (r *recorder) mids(t *testing.T) []string {
	t.Helper()
	out := []string{}
	for _, u := range r.updates() {
		m, err := u.Body()
		if err != nil {
			t.Fatalf("update %+v carries no message: %v", u, err)
		}
		out = append(out, m.Body.Mid)
	}

	return out
}

// waitUpdates waits until the handler has seen n updates.
func (r *recorder) waitUpdates(t *testing.T, n int, within time.Duration) {
	t.Helper()
	deadline := time.Now().Add(within)
	for {
		if len(r.updates()) >= n {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("the handler saw %d updates within %v, want %d", len(r.updates()), within, n)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

// logs is a logger a test reads back.
type logs struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (l *logs) logger() *slog.Logger { return slog.New(slog.NewTextHandler(l, nil)) }

func (l *logs) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	return l.buf.Write(p)
}

func (l *logs) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()

	return l.buf.String()
}

// run starts the loop in the background and answers with what it returned.
func run(ctx context.Context, c *Client, h Handler, interval time.Duration, log *slog.Logger) <-chan error {
	done := make(chan error, 1)
	go func() { done <- pollLoop(ctx, c, h, interval, log) }()

	return done
}

// stop cancels ctx and waits for the loop, failing the test when it does not
// come back at once.
func stop(t *testing.T, cancel context.CancelFunc, done <-chan error, within time.Duration) error {
	t.Helper()
	cancel()
	select {
	case err := <-done:
		return err
	case <-time.After(within):
		t.Fatalf("the loop did not stop within %v of the cancel", within)

		return nil
	}
}

// TestPollLoop_PausesBetweenPolls measures the pause in the terms the settings
// ask for. With the interval at 200 ms the first poll is on its way at once -
// the pause goes after an answer, never in front of a request, so a server that
// holds a connection open does not have the interval added to its latency - and
// the second one is no sooner than the interval after the first. Without that
// pause the client spins at roughly 2000 requests a second against a server
// that answers at once (the audit behind client.go).
func TestPollLoop_PausesBetweenPolls(t *testing.T) {
	s := newPollServer(t) // an empty page, answered at once
	c := s.client(t, filepath.Join(t.TempDir(), "gateway_max_cursor"))
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := run(ctx, c, &recorder{}, testInterval, nil)

	s.waitPolls(t, 1, testInterval/2, "the first poll, before any pause")
	s.waitPolls(t, 2, 3*testInterval, "a second poll")
	polls := s.arrivals()
	if gap := polls[1].at.Sub(polls[0].at); gap < testInterval {
		t.Fatalf("two polls %v apart, less than the %v pause: the loop is not pacing itself", gap, testInterval)
	}
	if err := stop(t, cancel, done, time.Second); err != nil {
		t.Fatalf("a cancelled loop returned %v", err)
	}
}

// TestPollLoop_CancellingStopsItAtOnce: the loop ends on ctx, whether the cancel
// lands between two polls or inside one. A poll is cancelled with the context
// too, so a stop never waits out the hold of the server.
func TestPollLoop_CancellingStopsItAtOnce(t *testing.T) {
	t.Run("between two polls", func(t *testing.T) {
		s := newPollServer(t)
		c := s.client(t, filepath.Join(t.TempDir(), "gateway_max_cursor"))
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		// An interval longer than the test: the cancel lands inside the pause.
		done := run(ctx, c, &recorder{}, time.Hour, nil)

		s.waitPolls(t, 1, 5*time.Second, "the first poll")
		if err := stop(t, cancel, done, time.Second); err != nil {
			t.Fatalf("a cancelled loop returned %v", err)
		}
	})

	t.Run("inside a poll", func(t *testing.T) {
		s := newPollServer(t)
		s.hold = time.Hour // the answer waits for the client to give up
		c := s.client(t, filepath.Join(t.TempDir(), "gateway_max_cursor"))
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		done := run(ctx, c, &recorder{}, testInterval, nil)

		s.waitPolls(t, 1, 5*time.Second, "the first poll")
		if err := stop(t, cancel, done, 2*time.Second); err != nil {
			t.Fatalf("a loop cancelled inside a poll returned %v", err)
		}
	})
}

// TestPollLoop_AFailedPollKeepsTheCursor: a failed poll is returned to the
// caller and the marker still stands where the last good page left it - in the
// client and in its file - so the next call resumes there instead of replaying
// the stream.
func TestPollLoop_AFailedPollKeepsTheCursor(t *testing.T) {
	cursor := filepath.Join(t.TempDir(), "gateway_max_cursor")
	s := newPollServer(t,
		pollPage{updates: []string{messageUpdate("m1", 1, "hello")}, marker: 100},
		pollPage{status: http.StatusInternalServerError},
	)
	c := s.client(t, cursor)

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := run(ctx, c, &recorder{}, 10*time.Millisecond, nil)
	var err error
	select {
	case err = <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the loop did not return the failed poll")
	}
	if !IsStatus(err, http.StatusInternalServerError) {
		t.Fatalf("the failed poll returned %v, want the 500 of the API", err)
	}
	if got := readCursorFile(t, cursor); got != "100" {
		t.Fatalf("the cursor file holds %q after the failure, want 100", got)
	}

	// Polling on: the request must carry the marker of the last good page.
	ctx2, cancel2 := context.WithCancel(t.Context())
	defer cancel2()
	done2 := run(ctx2, c, &recorder{}, 10*time.Millisecond, nil)
	s.waitPolls(t, 3, 5*time.Second, "the poll after the failure")
	if err := stop(t, cancel2, done2, time.Second); err != nil {
		t.Fatalf("a cancelled loop returned %v", err)
	}
	polls := s.arrivals()
	if polls[0].marker != "" {
		t.Fatalf("the first poll carried marker %q, want none", polls[0].marker)
	}
	if polls[2].marker != "100" {
		t.Fatalf("the poll after the failure carried marker %q, want 100", polls[2].marker)
	}

	// And a restart reads the same position back from the file.
	restarted := NewClient(testToken, WithBaseURL(s.srv.URL), WithCursor(cursor))
	if got := restarted.Cursor(); got != 100 {
		t.Fatalf("a restarted client began at marker %d, want 100", got)
	}
}

// TestUpdates_MalformedElementIsErrBadUpdate pins the contract the loop keys on:
// a page with an element that does not parse comes back as the elements that did
// parse beside ErrBadUpdate, never as a bare error - which is what lets the loop
// deliver the page and carry on rather than losing it.
func TestUpdates_MalformedElementIsErrBadUpdate(t *testing.T) {
	s := newPollServer(t, pollPage{
		updates: []string{messageUpdate("m1", 1, "one"), `"broken"`},
		marker:  4,
	})
	c := s.client(t, "")

	updates, err := c.Updates(t.Context())
	if !errors.Is(err, ErrBadUpdate) {
		t.Fatalf("a page with a malformed element returned %v, want ErrBadUpdate", err)
	}
	if len(updates) != 1 {
		t.Fatalf("the page came back with %d updates, want the one that parsed", len(updates))
	}
}

// TestPollLoop_APageWithAMalformedUpdateIsDelivered is the key case: a page whose
// element does not parse comes back as the elements that did parse beside
// ErrBadUpdate. The loop delivers them, logs the error and polls on, so one bad
// element neither loses the rest of the page nor spins on the same page forever.
func TestPollLoop_APageWithAMalformedUpdateIsDelivered(t *testing.T) {
	log := &logs{}
	s := newPollServer(t, pollPage{
		updates: []string{messageUpdate("m1", 1, "one"), `"broken"`, messageUpdate("m2", 2, "two")},
		marker:  7,
	})
	c := s.client(t, filepath.Join(t.TempDir(), "gateway_max_cursor"))
	h := &recorder{}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := run(ctx, c, h, 10*time.Millisecond, log.logger())

	// A second poll proves the loop carried on rather than returning the error.
	s.waitPolls(t, 2, 5*time.Second, "a poll after the malformed page")
	if err := stop(t, cancel, done, time.Second); err != nil {
		t.Fatalf("a page with a malformed update ended the loop: %v", err)
	}
	if got, want := h.mids(t), []string{"m1", "m2"}; !slices.Equal(got, want) {
		t.Fatalf("the page delivered %v, want %v", got, want)
	}
	if line := log.String(); !strings.Contains(line, "malformed") {
		t.Fatalf("the malformed element was not reported: %q", line)
	}
}

// TestPollLoop_UpdatesAreHandedOverOneAtATime: a page arrives as one poll but
// reaches the handler update by update, in the order the server sent them, so a
// slow turn cannot hold up the rest of the page.
func TestPollLoop_UpdatesAreHandedOverOneAtATime(t *testing.T) {
	s := newPollServer(t, pollPage{
		updates: []string{
			messageUpdate("m1", 1, "one"),
			messageUpdate("m2", 2, "two"),
			messageUpdate("m3", 3, "three"),
		},
		marker: 5,
	})
	c := s.client(t, filepath.Join(t.TempDir(), "gateway_max_cursor"))
	h := &recorder{}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := run(ctx, c, h, 10*time.Millisecond, nil)

	h.waitUpdates(t, 3, 5*time.Second)
	s.waitPolls(t, 2, 5*time.Second, "a poll after the page")
	if err := stop(t, cancel, done, time.Second); err != nil {
		t.Fatalf("a cancelled loop returned %v", err)
	}
	if got, want := h.mids(t), []string{"m1", "m2", "m3"}; !slices.Equal(got, want) {
		t.Fatalf("the page delivered %v, want %v", got, want)
	}
	h.mu.Lock()
	sizes := slices.Clone(h.sizes)
	h.mu.Unlock()
	for i, n := range sizes {
		if n != 1 {
			t.Fatalf("call %d was handed %d updates, want one", i+1, n)
		}
	}
}

// TestPollLoop_AHandlerErrorDoesNotStopTheLoop: an update the handler refuses is
// logged and the rest of the page still goes out, and the loop keeps polling: one
// update the bot cannot handle must not stop it.
func TestPollLoop_AHandlerErrorDoesNotStopTheLoop(t *testing.T) {
	log := &logs{}
	s := newPollServer(t, pollPage{
		updates: []string{
			messageUpdate("m1", 1, "one"),
			messageUpdate("m2", 2, "two"),
			messageUpdate("m3", 3, "three"),
		},
		marker: 9,
	})
	c := s.client(t, filepath.Join(t.TempDir(), "gateway_max_cursor"))
	h := &recorder{failAt: 2}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := run(ctx, c, h, 10*time.Millisecond, log.logger())

	h.waitUpdates(t, 3, 5*time.Second)
	s.waitPolls(t, 2, 5*time.Second, "a poll after the refused update")
	if err := stop(t, cancel, done, time.Second); err != nil {
		t.Fatalf("an update the handler refused ended the loop: %v", err)
	}
	if got, want := h.mids(t), []string{"m1", "m2", "m3"}; !slices.Equal(got, want) {
		t.Fatalf("the page delivered %v, want %v: the rest of the page was dropped", got, want)
	}
	if line := log.String(); !strings.Contains(line, "update not handled") {
		t.Fatalf("the refused update was not logged: %q", line)
	}
}

// TestBot_PollInterval: the loop of the adapter takes its pause from the
// settings, and pollEvery stands in for them only in a test, since the
// configuration floor is one second.
func TestBot_PollInterval(t *testing.T) {
	cfg := &config.MaxGatewayConfig{}
	cfg.ApplyDefaults()
	b := New(cfg, nil, "", (&logs{}).logger(), "", filepath.Join(t.TempDir(), "gateway_max_cursor"), nil)
	if got := b.pollInterval(); got != time.Second {
		t.Fatalf("the default interval is %v, want 1s", got)
	}
	slow := &config.MaxGatewayConfig{PollIntervalSeconds: 25}
	if got := New(slow, nil, "", b.log, "", b.cursorPath, nil).pollInterval(); got != 25*time.Second {
		t.Fatalf("the configured interval is %v, want 25s", got)
	}
	b.pollEvery = testInterval
	if got := b.pollInterval(); got != testInterval {
		t.Fatalf("pollEvery gives %v, want %v", got, testInterval)
	}
}

// TestBotPollLoop is the loop the adapter runs: the client, the handler and the
// interval as the adapter resolves them.
func TestBotPollLoop(t *testing.T) {
	cfg := &config.MaxGatewayConfig{}
	cfg.ApplyDefaults()
	b := New(cfg, nil, "", (&logs{}).logger(), "", filepath.Join(t.TempDir(), "gateway_max_cursor"), nil)
	b.pollEvery = 10 * time.Millisecond
	s := newPollServer(t, pollPage{updates: []string{messageUpdate("m1", 1, "hello")}, marker: 3})
	c := s.client(t, b.cursorPath)
	h := &recorder{}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- b.PollLoop(ctx, c, h) }()

	h.waitUpdates(t, 1, 5*time.Second)
	if err := stop(t, cancel, done, time.Second); err != nil {
		t.Fatalf("a cancelled loop returned %v", err)
	}
	if got, want := h.mids(t), []string{"m1"}; !slices.Equal(got, want) {
		t.Fatalf("the loop delivered %v, want %v", got, want)
	}
	if got := readCursorFile(t, b.cursorPath); got != "3" {
		t.Fatalf("the cursor file holds %q, want 3", got)
	}
}

// readCursorFile is what the poll marker file holds.
func readCursorFile(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read the cursor file: %v", err)
	}

	return strings.TrimSpace(string(raw))
}
