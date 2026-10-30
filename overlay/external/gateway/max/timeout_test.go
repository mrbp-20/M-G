//go:build gateway || gateway.max

package max

// The bot's HTTP client must give up on a dead connection by itself. The client
// proxyutil builds carries no deadline - it hands back http.DefaultClient or a
// bare &http.Client{} - and WithHTTPClient replaces the safe default NewClient
// sets, so the deadline is put back on the way in (bot.go).

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestWithFiniteTimeout_SetsADeadlineOnABareClient(t *testing.T) {
	hc := withFiniteTimeout(&http.Client{}, 45*time.Second)
	if hc.Timeout != 45*time.Second {
		t.Fatalf("timeout = %v, want 45s", hc.Timeout)
	}
}

func TestWithFiniteTimeout_KeepsAShorterDeadline(t *testing.T) {
	short := &http.Client{Timeout: 5 * time.Second}
	if got := withFiniteTimeout(short, 45*time.Second); got.Timeout != 5*time.Second {
		t.Fatalf("timeout = %v, want the shorter 5s kept", got.Timeout)
	}
}

// TestWithFiniteTimeout_LeavesTheSharedClientAlone: proxyutil may hand back
// http.DefaultClient, and a deadline written into that one would reach every
// other part of the program.
func TestWithFiniteTimeout_LeavesTheSharedClientAlone(t *testing.T) {
	before := http.DefaultClient.Timeout
	got := withFiniteTimeout(http.DefaultClient, 45*time.Second)
	if http.DefaultClient.Timeout != before {
		t.Fatalf("http.DefaultClient.Timeout = %v, want it untouched at %v", http.DefaultClient.Timeout, before)
	}
	if got == http.DefaultClient {
		t.Fatal("the shared client was returned as is, want a copy carrying the deadline")
	}
	if got.Timeout != 45*time.Second {
		t.Fatalf("timeout = %v on the copy, want 45s", got.Timeout)
	}
}

func TestWithFiniteTimeout_IgnoresAnEmptyAsk(t *testing.T) {
	hc := &http.Client{}
	if got := withFiniteTimeout(hc, 0); got != hc {
		t.Fatal("a zero deadline changed the client, want it returned as is")
	}
	if got := withFiniteTimeout(nil, time.Second); got != nil {
		t.Fatal("a nil client came back non-nil")
	}
}

// TestAHungServer_EndsInATimeout is the integration half of the deadline: the
// helper above puts a Timeout on the client, and this proves a server that never
// answers ends the call instead of holding the turn open until the operating
// system gives up. Without it the helper could be right and still useless.
func TestAHungServer_EndsInATimeout(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	defer srv.Close()
	defer close(release)

	hc := withFiniteTimeout(&http.Client{}, 200*time.Millisecond)
	c := NewClient("token", WithHTTPClient(hc), WithBaseURL(srv.URL))

	start := time.Now()
	_, err := c.Me(context.Background())
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("a server that never answers was reported as an answer")
	}
	if elapsed > 3*time.Second {
		t.Fatalf("the call took %v, want it cut by the deadline", elapsed)
	}
}
