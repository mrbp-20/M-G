//go:build gateway || gateway.max

package max

// The performance group of the package: what one poll of GET /updates costs in
// time and memory, against a fake Bot API of the same kind the tests drive, and
// what the loop around it costs in goroutines.
//
// The figure the audit behind client.go asks for is one page of maxUpdates (50)
// updates, read and parsed by our own transport; a -benchtime=60s run of the
// same benchmark reads the same cost carried over a minute of continuous
// polling. The fake server is built here instead of reusing poll_test.go's: the
// helper there takes a *testing.T and renders its answer on every request,
// while a benchmark needs a *testing.B and a body prepared once - `go test`
// reads the process-wide allocation counters, so the allocations of a server
// goroutine during the measured loop are counted with the client's, and a
// handler that composed its page per request would be measured instead of us.
//
// TestPollLoop_Goroutines is the one plain test here, for the third number of
// the audit: the loop is one goroutine, not one per poll and not one per update.

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"testing"
	"time"
)

// benchMarker is the marker the fake API answers with. It never changes, which
// is the steady state of a poll: the first answer saves it, and no later one
// touches the cursor again, so the benchmark pays the bookkeeping of the first
// iteration only.
const benchMarker = 170000

// benchScript is how many pages of updates TestPollLoop_Goroutines feeds the
// loop: enough updates for a goroutine per update to be impossible to miss.
const benchScript = 40

// benchText is the body of one update: a line a person would send in a chat,
// about 120 characters, which is what a turn of a real conversation costs.
func benchText(i int) string {
	return fmt.Sprintf("update %d: %s", i, strings.Repeat("a sentence of a chat message. ", 4))
}

// benchUpdate is one message_created update in the shape MAX sends it: the ids
// of the message sit in its body, the sender card and the recipient beside it.
func benchUpdate(i int) string {
	return fmt.Sprintf(`{"update_type":"message_created","timestamp":%d,"chat_id":%d,"user_id":%d,`+
		`"user":{"user_id":%d,"first_name":"Tester","last_name":"Number %d","username":"tester%d","is_bot":false},`+
		`"message":{"sender":{"user_id":%d,"first_name":"Tester","last_name":"Number %d","username":"tester%d"},`+
		`"recipient":{"chat_id":%d,"chat_type":"dialog"},"timestamp":%d,`+
		`"body":{"mid":"mid-%d","seq":%d,"text":%q}}}`,
		1700000000+int64(i), 1000+int64(i), 2000+int64(i),
		2000+int64(i), i, i,
		2000+int64(i), i, i,
		1000+int64(i), 1700000000+int64(i),
		i, i+1, benchText(i))
}

// benchUpdates renders n updates, the elements of one page.
func benchUpdates(n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = benchUpdate(i)
	}

	return out
}

// benchPage renders one answer of GET /updates: n updates and the marker that
// goes with them.
//
// A page of maxUpdates of these measures 27 KB, 546 bytes per update, and the
// size is part of the figure rather than a detail of it: most of what one poll
// allocates is the JSON decoder's buffer, which doubles as the page arrives, and
// the copies it makes of the raw elements, so both follow the page and not the
// number of polls. A comparison against another harness is only fair when the
// two pages are of a size.
func benchPage(n int, marker int64) []byte {
	return []byte(fmt.Sprintf(`{"updates":[%s],"marker":%d}`, strings.Join(benchUpdates(n), ","), marker))
}

// fakeAPI is the fake Bot API of the benchmarks: GET /updates answers the one
// prepared body, and everything else is a 404. The page is written as it is,
// with nothing composed and nothing encoded on the way, so what the numbers
// carry is the client.
func fakeAPI(b *testing.B, body []byte) *httptest.Server {
	b.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/updates" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	}))
	b.Cleanup(srv.Close)

	return srv
}

// benchClient is a client of the fake API whose marker stays in memory: a
// benchmark must not pay a disk write per poll, which is why the cursor file is
// not given here (WithCursor).
func benchClient(b *testing.B, srv *httptest.Server) *Client {
	b.Helper()

	return NewClient(testToken, WithHTTPClient(srv.Client()), WithBaseURL(srv.URL),
		WithPollTimeout(5*time.Second))
}

// pollPageOf50 measures one round trip: b.N polls of the same client, each of
// which must come back with the whole page.
func pollPageOf50(b *testing.B, c *Client, body []byte) {
	b.Helper()
	ctx := context.Background()
	b.ReportAllocs()
	b.SetBytes(int64(len(body)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		updates, err := c.Updates(ctx)
		if err != nil {
			b.Fatalf("poll %d: %v", i, err)
		}
		if len(updates) != maxUpdates {
			b.Fatalf("poll %d came back with %d updates, want %d", i, len(updates), maxUpdates)
		}
	}
}

// cannedTransport answers every request with one prepared response. A benchmark
// over it carries our own code and nothing else: no socket, no server, and none
// of the goroutines net/http keeps on either side of one.
type cannedTransport struct{ body []byte }

func (t *cannedTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	return &http.Response{
		Status:     "200 OK",
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": {"application/json"}},
		Body:       io.NopCloser(bytes.NewReader(t.body)),
		Request:    req,
	}, nil
}

// BenchmarkUpdates_PageOf50 is the headline figure: one poll that arrives with
// a full page of 50 updates, read over HTTP and parsed by our own transport.
//
// The two halves of it are apart on purpose. fake_api is the standalone
// arrangement the audit used - a fake Bot API in front of a real socket - and
// canned_response is the same call with the response handed over directly, so
// the difference between the two is what the standard library's client and
// server halves cost and what is ours.
func BenchmarkUpdates_PageOf50(b *testing.B) {
	body := benchPage(maxUpdates, benchMarker)

	b.Run("fake_api", func(b *testing.B) {
		pollPageOf50(b, benchClient(b, fakeAPI(b, body)), body)
	})
	b.Run("canned_response", func(b *testing.B) {
		c := NewClient(testToken, WithBaseURL("http://max.invalid"),
			WithHTTPClient(&http.Client{Transport: &cannedTransport{body: body}}))
		pollPageOf50(b, c, body)
	})
}

// BenchmarkUpdates_EmptyPage is the floor of one poll: the same round trip with
// nothing in it. The difference to BenchmarkUpdates_PageOf50/fake_api is what
// the page itself costs - the decoder's buffer and the copies it makes of the
// 50 elements - and it is also the price of a poll that found nothing, which is
// the common case of a long poll that ran out of its hold.
func BenchmarkUpdates_EmptyPage(b *testing.B) {
	body := []byte(fmt.Sprintf(`{"updates":[],"marker":%d}`, benchMarker))
	c := benchClient(b, fakeAPI(b, body))
	ctx := context.Background()
	b.ReportAllocs()
	b.SetBytes(int64(len(body)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := c.Updates(ctx); err != nil {
			b.Fatalf("poll %d: %v", i, err)
		}
	}
}

// TestPollLoop_Goroutines pins what the loop costs in goroutines: one, not one
// per poll and not one per update.
//
// socketGoroutines is what one HTTP connection costs on top of the loop's own
// goroutine: a read pump and a write pump in the client's transport. A count that
// rises by this much is a connection, not a leak.
const socketGoroutines = 2

// The absolute count is not ours to assert. httptest keeps a listener and a
// goroutine per connection, and the client's transport keeps two more for the
// same socket, so the test measures the differences around them: the loop's own
// contribution is bounded and does not follow the traffic, and once the loop is
// cancelled and the server is closed the count comes back to what it was before
// anything started.
func TestPollLoop_Goroutines(t *testing.T) {
	// One script of pages of 50 updates: the loop hands thousands of them over
	// while the test watches the count.
	page := benchUpdates(maxUpdates)
	pages := make([]pollPage, benchScript)
	for i := range pages {
		pages[i] = pollPage{updates: page, marker: int64(100 + i)}
	}

	base := runtime.NumGoroutine()
	s := newPollServer(t, pages...)
	serverUp := runtime.NumGoroutine()
	c := s.client(t, "") // the marker stays in memory: not what this test is about
	h := &recorder{}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := run(ctx, c, h, 5*time.Millisecond, nil)

	// The first page proves the loop is up and has handed a whole page over.
	h.waitUpdates(t, maxUpdates, 10*time.Second)
	// The loop's own goroutine is not necessarily in the count the instant the
	// first page lands: a slow runner schedules it a moment later. So it is
	// waited for, like the updates above, instead of being read once - CI read
	// the instant and saw no loop at all.
	running := runtime.NumGoroutine()
	for deadline := time.Now().Add(10 * time.Second); running <= serverUp && time.Now().Before(deadline); {
		time.Sleep(5 * time.Millisecond)
		running = runtime.NumGoroutine()
	}

	// The rest of the script: the count must not follow the traffic.
	s.waitPolls(t, benchScript, 30*time.Second, "the whole script of pages")
	h.waitUpdates(t, benchScript*maxUpdates, 10*time.Second)
	// One socket costs two goroutines that belong to the transport, not to the loop:
	// a read pump and a write pump. Under load the client may hold one connection
	// more than it did at the first page, and a slow runner needs a moment to retire
	// the previous one, so the count is waited for before it is judged - the same way
	// every other number here is. A loop that leaked per update would sit thousands
	// above, not two: CI once read the instant and failed on exactly this allowance.
	after := runtime.NumGoroutine()
	for deadline := time.Now().Add(2 * time.Second); after > running+socketGoroutines && time.Now().Before(deadline); {
		time.Sleep(5 * time.Millisecond)
		after = runtime.NumGoroutine()
	}

	t.Logf("goroutines: %d before the test, %d with the fake server up, %d while the loop polls, %d after %d updates",
		base, serverUp, running, after, benchScript*maxUpdates)
	if running <= serverUp {
		t.Fatalf("the loop is polling but holds no goroutine of its own: %d against %d with only the server up",
			running, serverUp)
	}
	if after > running+socketGoroutines {
		t.Fatalf("%d updates took the count from %d to %d: the loop grows with the traffic",
			benchScript*maxUpdates, running, after)
	}

	if err := stop(t, cancel, done, time.Second); err != nil {
		t.Fatalf("a cancelled loop returned %v", err)
	}
	// Closing the server takes its listener and its connections with it, and
	// drops the idle ones at once; the client's transport then ends the read and
	// write goroutines it kept for the socket, which is a moment later rather
	// than now. So the count is waited for, not read once.
	s.srv.Close()
	deadline := time.Now().Add(10 * time.Second)
	for {
		got := runtime.NumGoroutine()
		if got <= base {
			t.Logf("goroutines back to %d, the count of the test's start", got)
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("the count settled at %d, above the %d of the test's start: the loop left goroutines behind",
				got, base)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
