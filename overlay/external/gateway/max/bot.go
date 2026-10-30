//go:build gateway || gateway.max

package max

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/EvilFreelancer/coddy-agent/external/gateway/proxyutil"
	"github.com/EvilFreelancer/coddy-agent/external/gateway/sessionstore"
	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

const (
	adapterName    = "max"
	workerQueueCap = 32 // max queued messages per session
)

// SessionRunner is what the bot needs from the session manager;
// session.Manager satisfies it.
type SessionRunner interface {
	EnsureHTTPSession(ctx context.Context, sessionID string, defaultCWD string) (*session.State, error)
	HandleSessionPromptWithSender(ctx context.Context, params acp.SessionPromptParams, sender acp.UpdateSender, opts *session.PromptRunOpts) (*acp.SessionPromptResult, error)
	ForgetLiveSession(sessionID string)
	HandleSessionSetConfigOption(ctx context.Context, params acp.SessionSetConfigOptionParams) (*acp.SessionSetConfigOptionResult, error)
	// HandleSessionCancel is the cancel the web UI and the CLI use: the session
	// marks the turn as the person's own stop, so the core ends it quietly
	// instead of reporting a generation that was interrupted (stop.go).
	HandleSessionCancel(params acp.SessionCancelParams)
	Cfg() *config.Config
}

// Bot is the MAX gateway adapter: the bot's settings, the connection it makes
// at Start, and the dispatch of incoming updates into Coddy sessions. Name
// and Start are the whole of gateway.Adapter, satisfied structurally -
// importing external/gateway here would be a cycle, since that package builds
// this one in serve_max.go.
type Bot struct {
	cfg    *config.MaxGatewayConfig
	runner SessionRunner
	cwd    string
	log    *slog.Logger
	store  *sessionstore.Store
	mirror session.TurnMirror

	// cursorPath is the file the poll marker is kept in, so a restart resumes
	// where the bot stopped. It is a path in Coddy's state directory and never
	// the working directory or a temporary one: serve_max.go builds it from
	// the sessions root. Empty keeps the marker in memory only.
	cursorPath string
	// pollEvery, when set, replaces the configured poll interval; tests poll
	// faster than the one-second floor of the configuration.
	pollEvery time.Duration
	// asked holds the prompts waiting for a button press, keyed by the token
	// their buttons carry (sender.go). Its zero value is ready to use.
	asked presses
	// details keeps the tool calls a chat may open (tooldetails.go), so a press
	// after the turn ended still finds what the command was and what it said.
	// Its zero value is ready to use.
	details detailStore
	// transcript reads a session's messages for the history card a switch posts
	// (history.go). Nil leaves a switch as it was: the confirmation alone.
	transcript transcriptReader
	// burst holds the messages of a chat for a moment, so a text and the
	// picture sent with it run one turn (burst.go). bursts builds it on first
	// use, and burstWindow is the wait, for tests.
	burst       *burstBuffer
	burstWindow time.Duration
	// typingEvery is how often the "typing…" hint is renewed while a turn
	// runs; zero takes the production interval, and a test sets it short
	// (dispatch.go, typingKeepAlive).
	typingEvery time.Duration

	// Who the bot is in the messenger, read once at Start.
	selfID int64
	name   string

	mu         sync.Mutex
	client     *Client
	httpClient *http.Client
	turnCtx    context.Context
	stopping   bool
	workers    map[string]chan workerJob

	inFlight sync.WaitGroup

	// seen remembers recently handled message ids, so a message MAX delivered
	// twice runs one turn.
	seen *seenIDs

	// active holds the senders of the turns in flight, so a chat that is
	// switched to another conversation stops receiving the turn that no longer
	// belongs to it (resume.go, sender.go).
	activeMu sync.Mutex
	active   map[*Sender]Target

	// running holds the cancellation of the turns in flight, by session key, so
	// /stop ends one the moment it arrives instead of waiting behind it
	// (stop.go).
	runningMu sync.Mutex
	running   map[string]context.CancelFunc
	// stoppedKeys names the chats whose turn the person ended with /stop: the
	// core still returns an error when a turn is cut before its first token, and
	// that error is nobody's business but the log's (stop.go, dispatch.go).
	stoppedKeys map[string]bool
}

// trackSender remembers a turn in flight.
func (b *Bot) trackSender(s *Sender) {
	b.activeMu.Lock()
	if b.active == nil {
		b.active = make(map[*Sender]Target)
	}
	b.active[s] = s.target
	b.activeMu.Unlock()
}

// untrackSender forgets a turn that ended.
func (b *Bot) untrackSender(s *Sender) {
	b.activeMu.Lock()
	delete(b.active, s)
	b.activeMu.Unlock()
}

// detachChat stops the turns of a chat that no longer owns them: after a switch
// to another conversation they keep running in their own sessions, where the web
// UI still shows them, but they stop writing into the chat. Only a document
// still goes out, because it was asked for here (documents.go).
func (b *Bot) detachChat(chat Target, keep string) int {
	b.activeMu.Lock()
	defer b.activeMu.Unlock()

	stopped := 0
	for s, target := range b.active {
		if target != chat {
			continue
		}
		if s.files != nil && s.files.GetID() == keep {
			continue
		}
		s.Detach()
		stopped++
	}

	return stopped
}

type workerJob struct {
	in  inbound
	key string
}

// New returns a MAX adapter reading cfg, keeping its poll marker in cursorPath
// and the chat-to-session map in storePath; "" for either keeps it in memory
// only. The logger is expected to be tagged with the gateway.max component,
// which serve_max.go does.
func New(cfg *config.MaxGatewayConfig, runner SessionRunner, cwd string, log *slog.Logger, storePath, cursorPath string, mirror session.TurnMirror) *Bot {
	if mirror == nil {
		mirror = session.NopTurnMirror{}
	}
	return &Bot{
		cfg:        cfg,
		runner:     runner,
		cwd:        cwd,
		log:        log,
		store:      sessionstore.NewPersisted(storePath),
		mirror:     mirror,
		cursorPath: cursorPath,
		workers:    make(map[string]chan workerJob),
		seen:       newSeenIDs(4096),
	}
}

// Name satisfies gateway.Adapter.
func (b *Bot) Name() string { return "max" }

// apiBaseURL is the API root this bot talks to: CODDY_MAX_API_BASE when set,
// the v2 host otherwise.
func (b *Bot) apiBaseURL() string {
	base := strings.TrimRight(strings.TrimSpace(os.Getenv(config.MaxAPIBaseEnv)), "/")
	if base == "" {
		return config.DefaultMaxAPIBase
	}
	return base
}

// Start connects to the MAX Bot API and runs the update loop until ctx is
// cancelled. The loop hands every update to the bot itself, which turns a
// message into a session turn and a callback into the answer of a waiting
// prompt.
func (b *Bot) Start(ctx context.Context) error {
	token := b.cfg.EffectiveToken()
	if token == "" {
		return fmt.Errorf("max: no bot token; set gateways.max.token or the %s environment variable", config.MaxBotTokenEnvVar)
	}
	hc, err := proxyutil.BuildHTTPClient(b.cfg.Proxy)
	if err != nil {
		return fmt.Errorf("max: %w", err)
	}
	if b.cfg.CAFile != "" {
		if hc, err = withRootCA(hc, b.cfg.CAFile); err != nil {
			return fmt.Errorf("max: %w", err)
		}
	}
	// The poll client must have a deadline of its own. The one proxyutil builds
	// carries none - it either hands back http.DefaultClient or a bare
	// &http.Client{} - and a client without a Timeout waits for the operating
	// system to give up on a dead connection, which is minutes of silence for
	// a bot that should answer in seconds. WithHTTPClient replaces the safe
	// default NewClient sets, so the deadline is put back here, above the poll
	// timeout the server is allowed to hold the request open for.
	hc = withFiniteTimeout(hc, b.cfg.PollTimeout()+15*time.Second)
	b.httpClient = hc
	base := b.apiBaseURL()
	if base != config.DefaultMaxAPIBase {
		b.log.Info("max: api base override", "base", base)
	}
	client := NewClient(token, WithHTTPClient(hc), WithBaseURL(base),
		WithPollTimeout(b.cfg.PollTimeout()), WithCursor(b.cursorPath))

	me, err := client.Me(ctx)
	if err != nil {
		return fmt.Errorf("max: token check: %w", err)
	}
	b.selfID = me.UserID
	b.name = me.DisplayName()
	b.log.Info("max bot connected", "name", b.name, "user_id", b.selfID)

	// The hub starts this bot again after an error: workers of the previous
	// run ended with its context, and its permission prompts were withdrawn,
	// so both start afresh.
	b.mu.Lock()
	b.stopping = false
	b.workers = make(map[string]chan workerJob)
	b.mu.Unlock()
	b.setClient(client)
	defer b.setClient(nil)

	// The client shows these when somebody types "/". Best effort: a bot that
	// cannot publish its hints still answers people, and the next Start tries
	// again.
	b.publishCommands(ctx)

	// Turns run under a context of their own, so that stopping the bot stops
	// intake first and generation second: a token rotation must not cut an
	// answer off half-written.
	turnCtx, cancelTurns := context.WithCancel(context.WithoutCancel(ctx))
	defer cancelTurns()
	b.mu.Lock()
	b.turnCtx = turnCtx
	b.mu.Unlock()

	err = b.PollLoop(ctx, client, b)

	// What is still held here is already past the cursor: a burst waiting for
	// its clock when the loop ends would be lost with the process, so it goes
	// out now, and the drain below waits for its turn.
	b.bursts().flushAll()

	b.mu.Lock()
	b.stopping = true
	b.mu.Unlock()
	b.drain()
	if ctx.Err() != nil {
		return nil
	}
	return err
}

// setClient records the client of a running bot; nil means the bot stopped.
func (b *Bot) setClient(c *Client) {
	b.mu.Lock()
	b.client = c
	b.mu.Unlock()
}

// connectedClient is the client of a running bot, nil when stopped.
func (b *Bot) connectedClient() *Client {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.client
}

// bursts is the buffer of open message bursts, built on first use (burst.go).
func (b *Bot) bursts() *burstBuffer {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.burst == nil {
		b.burst = newBurstBuffer(b.burstWindow, b.deliver)
	}
	return b.burst
}

// turnContext is the context turns run under: the start's context without its
// cancellation, so a stop ends intake before it ends generation.
func (b *Bot) turnContext(ctx context.Context) context.Context {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.turnCtx != nil {
		return b.turnCtx
	}
	return ctx
}

// drainTimeout bounds the wait for turns still running when the bot stops.
const drainTimeout = 20 * time.Second

func (b *Bot) drain() {
	done := make(chan struct{})
	go func() {
		b.inFlight.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(drainTimeout):
		b.log.Warn("max: turns still running at stop", "waited", drainTimeout)
	}
}

// withRootCA returns a client that trusts the certificates of the PEM bundle
// at path in addition to the system store - a national root is an addition to
// it, not a replacement. The client given is copied, never modified in place:
// BuildHTTPClient answers "inherit" with http.DefaultClient itself, and a
// gateway setting must not reach every other request of the process.
func withRootCA(hc *http.Client, path string) (*http.Client, error) {
	pem, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("ca_file: %w", err)
	}
	pool, err := x509.SystemCertPool()
	if err != nil || pool == nil {
		pool = x509.NewCertPool()
	}
	if !pool.AppendCertsFromPEM(pem) {
		return nil, fmt.Errorf("ca_file %q contains no certificate", path)
	}
	tr, ok := hc.Transport.(*http.Transport)
	if !ok || tr == nil {
		base, ok := http.DefaultTransport.(*http.Transport)
		if !ok {
			return nil, fmt.Errorf("default transport is not *http.Transport")
		}
		tr = base
	}
	tr = tr.Clone()
	cfg := tr.TLSClientConfig
	if cfg == nil {
		cfg = &tls.Config{MinVersion: tls.VersionTLS12}
	} else {
		cfg = cfg.Clone()
	}
	cfg.RootCAs = pool
	tr.TLSClientConfig = cfg
	out := *hc
	out.Transport = tr

	return &out, nil
}

// withFiniteTimeout returns a client that gives up on a dead connection of its
// own accord. The client is copied, never changed in place: proxyutil may hand
// back http.DefaultClient, and a deadline written into that one would reach
// every other part of the program. A deadline the caller already set is kept
// when it is shorter than the one asked for here.
func withFiniteTimeout(hc *http.Client, atLeast time.Duration) *http.Client {
	if hc == nil || atLeast <= 0 {
		return hc
	}
	if hc.Timeout > 0 && hc.Timeout <= atLeast {
		return hc
	}
	out := *hc
	out.Timeout = atLeast

	return &out
}
