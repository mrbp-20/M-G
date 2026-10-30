package serve

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"log/slog"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/agent"
	"github.com/EvilFreelancer/coddy-agent/internal/bgtask"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/permission"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// Runtime is the state every subsystem of one `coddy serve` process shares:
// the resolved paths, the logger, and - for the surfaces that run agent turns -
// the single session manager that makes a conversation started in a chat the
// same conversation a browser opens.
type Runtime struct {
	// Paths holds the resolved home, workspace and config file locations.
	Paths config.Paths
	// Log is the process logger.
	Log *slog.Logger
	// Store is the session bundle store, nil when no enabled subsystem runs turns.
	Store *session.FileStore
	// Mgr owns sessions, nil when no enabled subsystem runs turns.
	Mgr *session.Manager

	mirrorMu sync.RWMutex
	mirror   session.TurnMirror

	// chatMirrors are the surfaces that publish a turn started elsewhere into
	// the chats they own (AddTurnMirror): the way back from the browser into a
	// messenger chat.
	chatMirrorsMu sync.RWMutex
	chatMirrors   map[uint64]session.TurnMirror
	chatMirrorSeq uint64

	// approvers are the surfaces that offered to show a detached subagent's
	// permission prompt, keyed by the offer so withdrawing one never takes
	// another with it.
	approversMu sync.RWMutex
	approvers   map[uint64]agent.DetachedPermissionBroker
	approverSeq uint64

	// wakes are the surfaces that offered to run the turn a finished
	// background task starts (wake.go).
	wakes wakeSurfaces

	// live holds the configuration while no manager exists, and every reload
	// of it (liveconfig.go). Once a manager exists it owns the live pointer,
	// because every reload path replaces it there.
	live liveConfig

	// sup is the supervisor whose reading of the live configuration Status
	// reports (SetSupervisor).
	sup atomic.Pointer[Supervisor]

	// metrics reads the exporter's block for the status document, nil while
	// no exporter is registered (SetMetricsSource).
	metricsMu sync.Mutex
	metrics   func() *MetricsBlock

	// process is how this process was started, read once by Init: coddy serve
	// clears the role variable for the processes it starts, so nothing could
	// read it later. Its ConfigFile is the path as the process loaded it.
	process ProcessDoc

	// diskFree reads the free and total bytes of a folder's disk, nil for
	// platform.DiskFree. A test lowers the figures through it.
	diskFree func(path string) (available, size uint64, err error)
}

// Cfg returns the live configuration.
func (r *Runtime) Cfg() *config.Config {
	if r.Mgr != nil {
		return r.Mgr.Cfg()
	}
	return r.live.load()
}

// SetTurnMirror installs (or with nil clears) the surface that mirrors turns
// started elsewhere.
//
// It is a slot rather than a constructor argument because the surfaces come up
// in whatever order the configuration lists them, and because one of them can
// be restarted under a settings change while another keeps running. A gateway
// that started first still ends up mirroring into an HTTP server that started
// second.
func (r *Runtime) SetTurnMirror(m session.TurnMirror) {
	r.mirrorMu.Lock()
	r.mirror = m
	r.mirrorMu.Unlock()
}

// MirrorTurn implements session.TurnMirror by delegating to the installed
// mirror, or handing the sender straight back when nothing is watching.
func (r *Runtime) MirrorTurn(sessionID string, primary acp.UpdateSender) (acp.UpdateSender, func()) {
	r.mirrorMu.RLock()
	m := r.mirror
	r.mirrorMu.RUnlock()
	if m == nil {
		return primary, func() {}
	}
	return m.MirrorTurn(sessionID, primary)
}

var _ session.TurnMirror = (*Runtime)(nil)

// AddTurnMirror offers a surface that publishes a turn started on some other
// surface into the chats it owns - a conversation opened in the browser reads
// the same in a messenger chat - and returns the function that withdraws
// exactly this offer.
//
// An offer rather than a constructor argument for the same reason as
// SetTurnMirror: the surfaces come up, and are rebuilt under a settings change,
// in whatever order the configuration lists them.
func (r *Runtime) AddTurnMirror(m session.TurnMirror) (withdraw func()) {
	if m == nil {
		return func() {}
	}
	r.chatMirrorsMu.Lock()
	r.chatMirrorSeq++
	id := r.chatMirrorSeq
	if r.chatMirrors == nil {
		r.chatMirrors = make(map[uint64]session.TurnMirror)
	}
	r.chatMirrors[id] = m
	r.chatMirrorsMu.Unlock()

	var once sync.Once
	return func() {
		once.Do(func() {
			r.chatMirrorsMu.Lock()
			delete(r.chatMirrors, id)
			r.chatMirrorsMu.Unlock()
		})
	}
}

// TurnToChats hands back a sender that publishes a turn this surface started
// into the chats bound to the session, and the function that ends it. It is the
// way back: without it a conversation is transparent from a chat into the
// browser and not from the browser into the chat.
//
// The turn keeps every question that needs an answer: the browser answers for
// the turn it started, and a chat only follows.
func (r *Runtime) TurnToChats(sessionID string) (acp.UpdateSender, func()) {
	var sender acp.UpdateSender
	var releases []func()
	for _, m := range r.chatMirrorList() {
		s, done := m.MirrorTurn(sessionID, sender)
		if s != nil {
			sender = s
		}
		if done != nil {
			releases = append(releases, done)
		}
	}

	return sender, func() {
		for _, done := range releases {
			done()
		}
	}
}

// SayToChats shows in the chats bound to the session a message written on
// another surface, so the conversation reads there as it does where it was
// written.
func (r *Runtime) SayToChats(sessionID, text string) {
	if strings.TrimSpace(text) == "" {
		return
	}
	for _, m := range r.chatMirrorList() {
		if s, ok := m.(interface{ Say(sessionID, text string) }); ok {
			s.Say(sessionID, text)
		}
	}
}

// chatMirrorList is the offers that stand right now, copied so a surface that
// withdraws while a turn is being set up cannot cut the list under it.
func (r *Runtime) chatMirrorList() []session.TurnMirror {
	r.chatMirrorsMu.RLock()
	defer r.chatMirrorsMu.RUnlock()
	out := make([]session.TurnMirror, 0, len(r.chatMirrors))
	for _, m := range r.chatMirrors {
		out = append(out, m)
	}

	return out
}

// SayJobReport shows a scheduled job's report in the conversation its gateway
// reports into - the administrator's chat - headed so the person sees at once
// that a task fired rather than somebody answering. A report is published by
// the first surface that takes it; a process with no messenger takes none, and
// the caller then keeps the text in the job's own session.
func (r *Runtime) SayJobReport(description string, at time.Time, text string) bool {
	if strings.TrimSpace(text) == "" {
		return false
	}
	for _, m := range r.chatMirrorList() {
		if s, ok := m.(interface {
			SayJobReport(description string, at time.Time, text string) bool
		}); ok && s.SayJobReport(description, at, text) {
			return true
		}
	}

	return false
}

// WatchJobReports shows the report of a finished run of a scheduled job in the
// conversation its gateway reports into, so a task the person scheduled reads
// where the conversation does. A run is told apart by the job id its session
// carries (SchedulerRunMeta), so a subagent of an ordinary turn is left alone.
// The description comes from the caller, which owns the job store.
func (r *Runtime) WatchJobReports(pool *bgtask.Pool, description func(jobID string) string) {
	if r == nil || pool == nil {
		return
	}
	var mu sync.Mutex
	seen := make(map[string]bool)
	pool.Subscribe(func(s bgtask.Snapshot) {
		if !jobRunFinished(s) {
			return
		}
		mu.Lock()
		done := seen[s.ID]
		seen[s.ID] = true
		mu.Unlock()
		if done {
			return
		}
		r.reportJobRun(s, description)
	})
}

// jobRunFinished reports whether a snapshot is a scheduled run that ended: a run
// that failed is a report of its own, so both outcomes are carried.
func jobRunFinished(s bgtask.Snapshot) bool {
	if s.Kind != bgtask.KindAgent || s.Agent == nil || strings.TrimSpace(s.Agent.SessionID) == "" {
		return false
	}

	return s.Status == bgtask.StatusSucceeded || s.Status == bgtask.StatusFailed
}

// reportJobRun carries one finished run's last answer to the conversation.
func (r *Runtime) reportJobRun(s bgtask.Snapshot, description func(jobID string) string) {
	jobID, text := r.jobRunAnswer(s)
	if jobID == "" {
		// An ordinary turn leaves the same trace as a scheduled run and is the
		// common case: there is no report to lose, so there is nothing to say.
		// Warning here wrote a line on every finished turn of every chat - 70
		// of them in one day - and a warning that fires constantly is one
		// nobody reads when it finally means something (29.10.2026).
		return
	}
	if s.Status == bgtask.StatusFailed {
		text = strings.TrimSpace(failureText(s) + "\n\n" + strings.TrimSpace(text))
	}
	if text == "" {
		r.logReportSkipped(s, "the run left no answer")

		return
	}
	desc := ""
	if description != nil {
		desc = description(jobID)
	}
	if r.SayJobReport(desc, s.StartedAt, text) {
		return
	}
	r.logReportSkipped(s, "no administrator conversation to report into")
}

// jobRunAnswer is the job a finished run belongs to and the answer it ended
// with: read from the live session while it is still there, and from the run's
// own bundle otherwise. A run is a child session, retired as soon as the run
// ends, so the live lookup alone found nothing and every report of a scheduled
// job vanished without a word (27.10.2026).
func (r *Runtime) jobRunAnswer(s bgtask.Snapshot) (string, string) {
	var jobID, text string
	if r.Mgr != nil {
		if st := r.Mgr.SessionByID(s.Agent.SessionID); st != nil {
			jobID = strings.TrimSpace(st.GetSchedulerJobID())
			text = lastReportText(st.GetMessages())
		}
	}
	dir := r.runBundleDir(s)
	if dir == "" || (jobID != "" && text != "") {
		return jobID, text
	}
	if jobID == "" {
		jobID = readRunJobID(filepath.Join(dir, "session.json"))
	}
	if text == "" {
		text = readRunAnswer(filepath.Join(dir, "messages.json"))
	}

	return jobID, text
}

// runBundleDir is where a run keeps its own bundle: under the job session it
// belongs to, in subagents/<run>.
func (r *Runtime) runBundleDir(s bgtask.Snapshot) string {
	cfg := r.Cfg()
	if cfg == nil || s.Agent == nil {
		return ""
	}
	root := strings.TrimSpace(cfg.ResolvedSessionsRoot())
	if root == "" || strings.TrimSpace(s.SessionID) == "" || strings.TrimSpace(s.Agent.SessionID) == "" {
		return ""
	}

	return filepath.Join(root, s.SessionID, "subagents", s.Agent.SessionID)
}

// logReportSkipped says why a finished job's report did not reach the chat: a
// report that disappears without a line in the log is the defect this exists to
// prevent.
func (r *Runtime) logReportSkipped(s bgtask.Snapshot, reason string) {
	if r.Log == nil {
		return
	}
	r.Log.Warn("scheduled report not delivered", "reason", reason, "task", s.ID, "job_session", s.SessionID)
}

// failureText is the line a failed run's report starts with.
func failureText(s bgtask.Snapshot) string {
	why := strings.TrimSpace(s.Error)
	if why == "" {
		why = "причина не записана"
	}

	return "Не выполнено: " + why
}

// readRunJobID is the job a run's own bundle names itself with.
func readRunJobID(path string) string {
	raw, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	var meta struct {
		JobID string `json:"schedulerJobId"`
	}
	if err := json.Unmarshal(raw, &meta); err != nil {
		return ""
	}

	return strings.TrimSpace(meta.JobID)
}

// readRunAnswer is the last answer of a run whose session no longer exists.
func readRunAnswer(path string) string {
	raw, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	var bundle struct {
		Messages []runMessage `json:"messages"`
	}
	if err := json.Unmarshal(raw, &bundle); err != nil {
		return ""
	}
	msgs := make([]llm.Message, 0, len(bundle.Messages))
	for _, m := range bundle.Messages {
		msgs = append(msgs, llm.Message{Role: llm.Role(m.Role), Content: m.text()})
	}

	return lastReportText(msgs)
}

// runMessage is one persisted message: its content is a plain string or the
// parts a provider streamed.
type runMessage struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

// text is the message's text, whichever shape its content came in.
func (m runMessage) text() string {
	var plain string
	if err := json.Unmarshal(m.Content, &plain); err == nil {
		return plain
	}
	var parts []struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(m.Content, &parts); err != nil {
		return ""
	}
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		out = append(out, p.Text)
	}

	return strings.Join(out, "")
}

// lastReportText is the answer a child run ends with, read the way the
// scheduler reads it: the last assistant message with text, the parts of an
// answer one cut continued joined back, and the file placement markers the web
// UI needs left out of a message a person reads in a chat.
func lastReportText(msgs []llm.Message) string {
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role != llm.RoleAssistant {
			continue
		}
		if s := strings.TrimSpace(msgs[i].Content); s != "" {
			return session.StripArtifactMarkers(session.ContinuedAnswer(msgs, i))
		}
	}

	return ""
}

// AddDetachedPermissionApprover offers a surface that can show the permission
// prompt of a subagent whose parent turn has ended, and returns the function
// that withdraws exactly this offer.
//
// An offer rather than a constructor argument for the same reason as
// SetTurnMirror: every turn on the shared manager is handed the runtime as its
// broker when it starts, while the surfaces that can actually show the prompt -
// the HTTP server, a messenger bot - come up, or are restarted, on their own
// schedule. A detached child asks at whatever moment it needs to, so the
// question is settled then, not when its parent's turn began. Withdrawing is
// keyed by the offer because a restarted surface can come up before the old one
// has finished stopping.
func (r *Runtime) AddDetachedPermissionApprover(b agent.DetachedPermissionBroker) (withdraw func()) {
	if b == nil {
		return func() {}
	}
	r.approversMu.Lock()
	r.approverSeq++
	id := r.approverSeq
	if r.approvers == nil {
		r.approvers = make(map[uint64]agent.DetachedPermissionBroker)
	}
	r.approvers[id] = b
	r.approversMu.Unlock()

	var once sync.Once
	return func() {
		once.Do(func() {
			r.approversMu.Lock()
			delete(r.approvers, id)
			r.approversMu.Unlock()
		})
	}
}

// RequestDetachedPermission implements agent.DetachedPermissionBroker by
// offering the prompt to every surface at once: the person reading that
// conversation may be in a browser, in a console attached to this server or in
// a messenger chat, and the runtime cannot tell which. The first answer settles
// it; the context of every other surface is cancelled, so each takes its copy
// of the prompt down, and the call returns only once they all have.
//
// A surface that cannot show this prompt answers agent.ErrNoDetachedApprover (a
// failure to deliver it counts the same). When every surface did - or none was
// offered - nobody could be asked, and the relay turns that into a refusal that
// says so.
func (r *Runtime) RequestDetachedPermission(ctx context.Context, req agent.DetachedPermissionRequest) (*acp.PermissionResult, error) {
	r.approversMu.RLock()
	surfaces := make([]agent.DetachedPermissionBroker, 0, len(r.approvers))
	for _, b := range r.approvers {
		surfaces = append(surfaces, b)
	}
	r.approversMu.RUnlock()
	if len(surfaces) == 0 {
		return nil, agent.ErrNoDetachedApprover
	}

	askCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	type answer struct {
		res *acp.PermissionResult
		err error
	}
	answers := make(chan answer, len(surfaces))
	for _, b := range surfaces {
		go func() {
			res, err := b.RequestDetachedPermission(askCtx, req)
			answers <- answer{res: res, err: err}
		}()
	}

	var settled *acp.PermissionResult
	cannotShow := 0
	for range surfaces {
		a := <-answers
		switch {
		case settled != nil:
			// Answered elsewhere first; this surface was only taking its copy down.
		case a.err == nil && a.res != nil:
			settled = a.res
			cancel()
		case a.err != nil:
			cannotShow++
		}
	}
	if settled != nil {
		return settled, nil
	}
	if cannotShow == len(surfaces) {
		return nil, agent.ErrNoDetachedApprover
	}
	return nil, nil
}

var _ agent.DetachedPermissionBroker = (*Runtime)(nil)

// Options are the process-level inputs a `coddy serve` invocation resolved from
// its flags and environment.
type Options struct {
	// CLI carries the --config / --home / --cwd overrides.
	CLI config.CLIPaths
	// SessionsRoot overrides sessions.dir when non-empty.
	SessionsRoot string
	// PreferredSessionID names the folder a newly created session claims.
	PreferredSessionID string
	// NeedsSessions is false for a process whose enabled subsystems run no
	// agent turns - a bare swarm relay - so it opens no store and builds no
	// manager.
	NeedsSessions bool
	// Log is the already-built process logger.
	Log *slog.Logger
	// Cfg is the already-loaded configuration.
	Cfg *config.Config
	// AdjustConfig reapplies process flags and environment values to a new
	// configuration before an in-process writer installs it.
	AdjustConfig func(*config.Config) error
}

// Init fills the runtime in place.
//
// It is two-phase on purpose: the subsystem descriptors close over the runtime,
// and which subsystems are enabled decides whether a session store is opened at
// all. The caller builds the empty runtime, describes the surfaces against it,
// resolves them, and only then comes back here.
func (r *Runtime) Init(opts Options) error {
	if opts.Cfg == nil {
		return fmt.Errorf("serve: configuration is required")
	}
	log := opts.Log
	if log == nil {
		log = slog.Default()
	}
	paths, err := config.Resolve(opts.CLI)
	if err != nil {
		return err
	}
	r.Paths = paths
	r.Log = log
	// Before the early return below: a runtime without sessions reports it too.
	r.process.StartedAt = time.Now()
	r.process.StartedBy = startedByRole(Role())
	r.process.ConfigFile = paths.ConfigPath
	r.live.replace(opts.Cfg)
	if !opts.NeedsSessions {
		return nil
	}

	store, err := OpenSessionStore(opts.SessionsRoot, opts.Cfg)
	if err != nil {
		return err
	}
	r.Store = store
	log.Info("session persistence enabled", "root", store.Root)

	var mgr *session.Manager
	live := func() *config.Config {
		if mgr != nil {
			return mgr.Cfg()
		}
		return opts.Cfg
	}
	runner := func(ctx context.Context, st *session.State, prompt []acp.ContentBlock, snd acp.UpdateSender) (string, error) {
		loop := agent.NewAgent(live(), st, snd, log)
		loop.SetWorkspaceSwitcher(func(ctx context.Context, dir string) error {
			return mgr.SetSessionWorkspaceDuringTurn(ctx, st, dir)
		})
		loop.SetConfigReloader(func(ctx context.Context) (*config.Config, []string, error) {
			return mgr.ReloadConfigForSession(ctx, st)
		})
		loop.SetSubagentRuntime(mgr)
		// A detached child outlives this turn, so its permission prompts need
		// somewhere to go once the turn's stream is gone: the runtime offers
		// them to every surface that is up when the child asks.
		loop.SetDetachedPermissionBroker(r)
		return loop.Run(ctx, prompt)
	}
	mgr = session.NewManager(opts.Cfg, &defaultSender{live: live}, runner, log, paths.CWD, store)
	mgr.SetConfigAdjuster(opts.AdjustConfig)
	// A session this process creates returns before its MCP servers answer,
	// and its first turn waits for them after announcing itself (issue #357):
	// a cold server used to hold the first message of a web chat, without a
	// byte on the wire, for as long as it took to start. A stored session
	// still starts its servers only for its first turn.
	mgr.SetNewSessionsBackgroundMCP(true)
	// A met goal is confirmed against the workspace by the goal verifier.
	mgr.SetGoalVerifier(agent.NewGoalVerifier(mgr, log))
	// A met goal with a follow-up file hands the next task to a new session,
	// whose first turn this process runs (goal_chain.go).
	mgr.SetGoalChainStarter(r.runGoalChainTurn)
	if pid := strings.TrimSpace(opts.PreferredSessionID); pid != "" {
		if err := session.ValidateFolderSessionID(pid); err != nil {
			return fmt.Errorf("--session-id: %w", err)
		}
		mgr.SetPreferredSessionID(pid)
	}
	r.Mgr = mgr
	// A task started with a wake (notify_on_finish) wakes the agent whichever surfaces are
	// enabled; they offer to run the turn as they come up (AddWakeSurface).
	r.attachWaker()
	return nil
}

// defaultSender is the manager's fallback surface: what answers when a turn
// runs with nobody attached to it.
//
// It refuses rather than approves. A daemon that starts turns of its own -
// a scheduled job, a background wake - has no human to ask, and the safe
// reading of "nobody is here" is no, not yes. Only an operator who set the
// permission mode to bypass has said otherwise, and a subagent's own narrowed
// mode overrides even that.
type defaultSender struct {
	live func() *config.Config
}

func (d *defaultSender) SendSessionUpdate(string, interface{}) error { return nil }

func (d *defaultSender) RequestPermission(_ context.Context, params acp.PermissionRequestParams) (*acp.PermissionResult, error) {
	cfgMode := ""
	if cfg := d.live(); cfg != nil {
		cfgMode = cfg.Tools.ResolvedPermMode()
	}
	if permission.AutoApproves(params, cfgMode) {
		return &acp.PermissionResult{Outcome: "allow", OptionID: "allow"}, nil
	}
	return &acp.PermissionResult{Outcome: "cancelled", OptionID: "reject"}, nil
}

func (d *defaultSender) RequestQuestion(context.Context, acp.QuestionRequestParams) (*acp.QuestionResult, error) {
	return &acp.QuestionResult{}, nil
}

// EnsureHomeLayout creates the directories every surface expects under the
// agent home.
func EnsureHomeLayout(home string) error {
	if strings.TrimSpace(home) == "" {
		return nil
	}
	for _, name := range []string{"sessions", "skills", "scheduler"} {
		p := filepath.Join(home, name)
		if err := os.MkdirAll(p, 0o755); err != nil {
			return fmt.Errorf("mkdir %s: %w", p, err)
		}
	}
	return nil
}

// OpenSessionStore resolves the session bundle root, preferring an explicit
// override over sessions.dir.
func OpenSessionStore(flagValue string, cfg *config.Config) (*session.FileStore, error) {
	if raw := strings.TrimSpace(flagValue); raw != "" {
		root, err := filepath.Abs(raw)
		if err != nil {
			return nil, fmt.Errorf("sessions-dir: %w", err)
		}
		if err := os.MkdirAll(root, 0o755); err != nil {
			return nil, fmt.Errorf("sessions-dir mkdir: %w", err)
		}
		return &session.FileStore{Root: root}, nil
	}
	root := cfg.ResolvedSessionsRoot()
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, fmt.Errorf("sessions root mkdir: %w", err)
	}
	return &session.FileStore{Root: root}, nil
}
