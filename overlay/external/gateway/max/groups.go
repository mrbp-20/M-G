//go:build (gateway || gateway.max) && gateway.max.groups

package max

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
)

// Why a group chat is not answered. The text lands in the log beside the chat
// id, and it is what a person needs to fix the configuration.
var (
	errGroupNotAllowed  = errors.New("группа не разрешена: впишите её в gateways.max.chats с путём (workspace) и доступом (access)")
	errGroupNoWorkspace = errors.New("у группы не указан путь (workspace)")
)

// groupNoteRunes keeps a message to the admins inside one MAX message.
const groupNoteRunes = 700

// groupGate is what this build answers about a group chat: may the bot take part
// at all, and is the configuration complete enough to try.
//
// A build without group support carries the same type with the opposite answers
// (groups_stub.go), so the rest of the adapter never asks which build it is in:
// there is one call in one place.
type groupGate struct {
	cfg *config.MaxGatewayConfig
	log *slog.Logger

	mu       sync.Mutex
	reported map[int64]bool      // chats whose refusal was already said out loud
	told     map[int64]time.Time // when the admins last heard the directory is gone
}

func newGroupGate(cfg *config.MaxGatewayConfig, log *slog.Logger) *groupGate {
	if log == nil {
		log = slog.Default()
	}

	return &groupGate{cfg: cfg, log: log, reported: make(map[int64]bool), told: make(map[int64]time.Time)}
}

// Enabled reports whether this build carries group support at all.
func (g *groupGate) Enabled() bool { return true }

// Note is the line /help shows about groups.
func (g *groupGate) Note() string { return "Группы: включены в этой сборке." }

// ConfigProblems lists the group entries this build cannot work with. A group
// entry is a chat with a negative id or with a workspace, and such an entry
// needs both the path and the access level written out: guessing either is how
// a bot ends up in a chat nobody meant to open.
//
// A problem does not stop the gateway. One mistyped group must not take the
// personal chats of its owner down, and an error returned from Start would have
// Coddy restart the whole adapter every five seconds. The entries are named once
// at startup, in the log and in a letter to the admins, and only those groups
// stay unanswered.
func (g *groupGate) ConfigProblems() []string {
	var out []string
	for i := range g.cfg.Chats {
		c := &g.cfg.Chats[i]
		if c.ChatID >= 0 && c.Workspace == "" {
			continue // a direct chat override: nothing group-specific to check
		}
		switch {
		case c.Workspace == "":
			out = append(out, fmt.Sprintf("группа %d: не указан путь (workspace), с такой группой бот не работает", c.ChatID))
		case !filepath.IsAbs(c.Workspace):
			out = append(out, fmt.Sprintf("группа %d: путь %q не абсолютный, а относительный означает разные каталоги при разных запусках", c.ChatID, c.Workspace))
		case strings.TrimSpace(string(c.Access)) == "":
			out = append(out, fmt.Sprintf("группа %d: не указан доступ (access: admins, all или group:<имя>)", c.ChatID))
		}
	}

	return out
}

// Admit says whether the bot may take part in this group. A group that is absent
// from the configuration, or present without a path or without an access level,
// is a group the bot does not answer in - the same as a group nobody allowed.
func (g *groupGate) Admit(chatID int64) error {
	c := g.cfg.ChatOverride(chatID)
	if c == nil {
		return errGroupNotAllowed
	}
	if strings.TrimSpace(c.Workspace) == "" {
		return errGroupNoWorkspace
	}
	if strings.TrimSpace(string(c.Access)) == "" {
		return errGroupNotAllowed
	}

	return nil
}

// reportOnce says whether this chat's refusal has not been logged yet: a group
// the bot was dragged into says one line, not one per message. A bot's log is
// read by a person, and a warning repeated on every message stops being read.
func (g *groupGate) reportOnce(chatID int64) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.reported[chatID] {
		return false
	}
	g.reported[chatID] = true

	return true
}

// groupTitle takes the chat title out of the update when the platform sends one.
// The shape is not documented for these update types, so the probe reads the two
// places a title can sit and never fails: the admins are told the title is
// missing rather than being told nothing.
func groupTitle(u Update) string {
	var probe struct {
		Chat *struct {
			Title string `json:"title"`
		} `json:"chat"`
		Message *struct {
			Recipient struct {
				Title string `json:"title"`
			} `json:"recipient"`
		} `json:"message"`
	}
	if len(u.wire) > 0 {
		_ = json.Unmarshal(u.wire, &probe)
	}
	if probe.Chat != nil && strings.TrimSpace(probe.Chat.Title) != "" {
		return strings.TrimSpace(probe.Chat.Title)
	}
	if probe.Message != nil && strings.TrimSpace(probe.Message.Recipient.Title) != "" {
		return strings.TrimSpace(probe.Message.Recipient.Title)
	}

	return ""
}

// personName names the person an update came from, so a notification reads as
// something a human wrote rather than as an id.
func personName(u User) string {
	name := strings.TrimSpace(u.Name)
	if name == "" {
		name = strings.Join(nonEmpty(u.FirstName, u.LastName), " ")
	}
	if name == "" && u.Username != "" {
		name = "@" + u.Username
	}
	switch {
	case name == "" && u.UserID == 0:
		return ""
	case name == "":
		return fmt.Sprintf("user_id %d", u.UserID)
	default:
		return fmt.Sprintf("%s (user_id %d)", name, u.UserID)
	}
}

func nonEmpty(parts ...string) []string {
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if strings.TrimSpace(p) != "" {
			out = append(out, strings.TrimSpace(p))
		}
	}

	return out
}

// handleGroupEvent turns a group membership change into a log line and, when it
// concerns the admins, into a message to each of them. Nothing is said in the
// group itself: the bot either works there or stays silent.
func (b *Bot) handleGroupEvent(ctx context.Context, u Update, kind string) error {
	chatID := u.ChatID
	title := groupTitle(u)
	who := personName(u.From())
	allowed := b.groups.Admit(chatID) == nil

	b.log.Info("max: group event", "kind", kind, "chat", chatID, "title", title,
		"by", who, "allowed", allowed, "raw", rawUpdate(u))

	switch kind {
	case "added":
		b.notifyAdmins(ctx, groupAddedNote(chatID, title, who, allowed))
	case "removed":
		b.notifyAdmins(ctx, groupRemovedNote(chatID, title, who))
	}

	return nil
}

func groupAddedNote(chatID int64, title, who string, allowed bool) string {
	var sb strings.Builder
	sb.WriteString("Меня добавили в группу.\n")
	fmt.Fprintf(&sb, "Группа: %s\n", placeName(title, chatID))
	if who != "" {
		fmt.Fprintf(&sb, "Пригласил: %s\n", who)
	}
	if allowed {
		sb.WriteString("Эта группа уже разрешена в конфиге — работаю в ней.")
	} else {
		sb.WriteString("Эта группа не разрешена: в ней я молчу. Чтобы включить, добавьте в " +
			"gateways.max.chats запись с chat_id, workspace (абсолютный путь) и access.")
	}

	return clipRunes(sb.String(), groupNoteRunes)
}

func groupRemovedNote(chatID int64, title, who string) string {
	var sb strings.Builder
	sb.WriteString("Меня удалили из группы.\n")
	fmt.Fprintf(&sb, "Группа: %s\n", placeName(title, chatID))
	if who != "" {
		fmt.Fprintf(&sb, "Удалил: %s\n", who)
	}
	sb.WriteString("В этой беседе я больше не работаю.")

	return clipRunes(sb.String(), groupNoteRunes)
}

// placeName is the chat as an admin can recognise it: the title when the
// platform gave one, and the id always, because the id is what goes in the
// configuration.
func placeName(title string, chatID int64) string {
	if strings.TrimSpace(title) == "" {
		return fmt.Sprintf("название платформа не сообщила, chat_id %d", chatID)
	}

	return fmt.Sprintf("%s, chat_id %d", title, chatID)
}

// workspaceToldGap is how often the admins hear that a group's directory is
// gone: once an hour while it stays broken, not once per message.
const workspaceToldGap = time.Hour

// Workspace is the directory a group works in: the configured path, resolved to
// its real self. Resolving is not a formality - a path that is a symlink would
// otherwise confine the turn in name only, and the check would compare the name
// of a place with the place itself.
//
// A path that is gone, unreadable or not a directory refuses the turn and, at
// most once an hour per chat, produces a notice for the admins. The notice is
// returned rather than sent: the gate has no client, and one thing that talks
// to MAX is enough.
func (g *groupGate) Workspace(chatID int64) (dir, notice string, err error) {
	c := g.cfg.ChatOverride(chatID)
	if c == nil || strings.TrimSpace(c.Workspace) == "" {
		return "", "", errGroupNoWorkspace
	}
	real, resolveErr := resolveWorkspace(c.Workspace)
	if resolveErr != nil {
		return "", g.workspaceNotice(chatID, c.Workspace, resolveErr), resolveErr
	}

	return real, "", nil
}

// resolveWorkspace follows every symlink and checks that what is left is a
// directory the bot can open.
func resolveWorkspace(path string) (string, error) {
	real, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", fmt.Errorf("каталог не найден: %s", path)
	}
	st, err := os.Stat(real)
	if err != nil {
		return "", fmt.Errorf("каталог недоступен: %s", real)
	}
	if !st.IsDir() {
		return "", fmt.Errorf("это не каталог: %s", real)
	}

	return real, nil
}

// workspaceNotice builds the message to the admins, or returns an empty string
// when one already went out within the hour.
func (g *groupGate) workspaceNotice(chatID int64, path string, cause error) string {
	g.mu.Lock()
	last, told := g.told[chatID]
	if told && time.Since(last) < workspaceToldGap {
		g.mu.Unlock()

		return ""
	}
	g.told[chatID] = time.Now()
	g.mu.Unlock()

	var sb strings.Builder
	sb.WriteString("Не работаю в группе: её каталог недоступен.\n")
	fmt.Fprintf(&sb, "Группа: chat_id %d\n", chatID)
	fmt.Fprintf(&sb, "Путь из конфига: %s\n", path)
	fmt.Fprintf(&sb, "Причина: %v\n", cause)
	sb.WriteString("Пока каталога нет, в группе я молчу. Исправьте путь в gateways.max.chats " +
		"(workspace) или создайте каталог - работа возобновится сама, перезапуск не нужен.")

	return clipRunes(sb.String(), groupNoteRunes)
}
