//go:build gateway || gateway.max

package gateway

import (
	"context"
	"errors"
	"path/filepath"

	"github.com/EvilFreelancer/coddy-agent/external/gateway/max"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/logger"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// MaxAvailable reports whether this binary carries the MAX adapter.
const MaxAvailable = true

// ServeMax builds the MAX bot and runs it until ctx is cancelled.
func ServeMax(ctx context.Context, opts Options) error {
	if opts.Cfg == nil || opts.Mgr == nil || opts.Log == nil {
		return errors.New("gateway: Cfg, Mgr and Log are required")
	}
	mx := &opts.Cfg.Gateways.Max
	if !mx.Enabled {
		return errors.New("gateway: the MAX bot is not enabled; set gateways.max.enable: true in config")
	}
	if mx.EffectiveToken() == "" {
		return errors.New("gateways.max.enable is true but no token was found; set gateways.max.token or the " +
			config.MaxBotTokenEnvVar + " environment variable")
	}
	// Each adapter logs under its own component so logger.levels can raise one
	// bot to debug without the rest of the process following it.
	//
	// The bot keeps its own files in the state directory of Coddy - the
	// chat-to-session map and the poll marker - never in the working directory
	// of the process: a restart resumes where the bot stopped, whatever
	// directory `coddy serve` was started from.
	root := opts.Cfg.ResolvedSessionsRoot()
	bot := max.New(mx, opts.Mgr, opts.DefaultCWD,
		logger.Component(opts.Log, logger.ComponentGatewayMax),
		filepath.Join(root, "gateway_max_sessions.json"),
		filepath.Join(root, "gateway_max_cursor"),
		opts.Mirror)
	// A switch of the conversation has to show where it landed, and the messages
	// of a session live in the manager, not in the gateway: the bot is handed a
	// reader (history.go) rather than the manager itself.
	bot.SetTranscript(func(sessionID string) []llm.Message {
		st := opts.Mgr.SessionByID(sessionID)
		if st == nil {
			return nil
		}

		return st.GetMessages()
	})
	// The gateway is transparent both ways: a turn started in the browser has to
	// read in the chat its session is bound to, so the bot offers itself as a
	// mirror of the chats it owns while it runs.
	if rt, ok := opts.Mirror.(interface {
		AddTurnMirror(session.TurnMirror) func()
	}); ok {
		withdraw := rt.AddTurnMirror(bot)
		defer withdraw()
	}
	NewHub(logger.Component(opts.Log, logger.ComponentGateway), bot).Start(ctx)
	return nil
}
