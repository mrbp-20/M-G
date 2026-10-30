//go:build gateway || gateway.max

package max

import (
	"context"
	"net/http"
	"os"
	"testing"
	"time"
)

// TestLiveMe is the one check a fake server cannot make: a real call to the MAX
// Bot API with the real token, which proves the two things the stands could only
// take from the schema - that MAX takes the token raw in Authorization, and that
// the MinCifry root in ca_file is enough to reach platform-api2.max.ru.
//
// It is opt-in and silent by default: CODDY_MAX_LIVE turns it on and a set
// MAX_BOT_TOKEN gives it a token, so a host whose environment carries the token
// for the running service does not make the suite reach the network.
// CODDY_MAX_CA names the PEM bundle, CODDY_MAX_API_BASE overrides the host (a
// stand).
func TestLiveMe(t *testing.T) {
	if os.Getenv("CODDY_MAX_LIVE") == "" {
		t.Skip("CODDY_MAX_LIVE is not set: the live check is opt-in")
	}
	token := os.Getenv("MAX_BOT_TOKEN")
	if token == "" {
		t.Skip("MAX_BOT_TOKEN is not set: the live check needs a token")
	}
	hc := &http.Client{Timeout: 30 * time.Second}
	if ca := os.Getenv("CODDY_MAX_CA"); ca != "" {
		var err error
		if hc, err = withRootCA(hc, ca); err != nil {
			t.Fatalf("ca_file: %v", err)
		}
	}
	opts := []Option{WithHTTPClient(hc)}
	base := os.Getenv("CODDY_MAX_API_BASE")
	if base != "" {
		opts = append(opts, WithBaseURL(base))
	}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()

	me, err := NewClient(token, opts...).Me(ctx)
	if err != nil {
		t.Fatalf("Me: %v", err)
	}
	t.Logf("connected to %q as %q (user_id %d, bot=%v)", base, me.DisplayName(), me.UserID, me.IsBot)
}
