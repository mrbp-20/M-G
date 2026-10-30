//go:build gateway || gateway.max

// Package max is the MAX messenger gateway: the bot's own HTTP client and the
// adapter that turns incoming messages into Coddy sessions.
//
// The client is written by hand rather than on the official library, and every
// rule below exists for a measured reason: an audit of
// max-bot-api-client-go/v2@v2.4.4 against a fake server reproduced each of the
// failures these rules avoid.
//
//   - The HTTP status stays on the error. The official client drops it, so a
//     429 and a 500 with the same body are indistinguishable and a caller
//     cannot tell a rate limit from a refusal.
//   - A 429 is waited out, up to maxRetryAfter, and only a 429: MAX refuses the
//     request before doing the work, so a repeated POST cannot post a second
//     message. Nothing else is retried for a write, because a 5xx or a broken
//     connection may well have been processed.
//   - Response bodies are read through a limit. Without one, a hostile or
//     misconfigured server makes the process allocate without bound: the audit
//     read a 100 MiB body into a JSON string and the heap grew by ~484 MiB.
//   - The update page is decoded in two steps. One malformed element must not
//     drop the page: the official client fails the whole response on a single
//     bad update, the cursor does not move, and the same page is re-fetched
//     forever.
//   - The cursor belongs to us and only a successful poll moves it. The
//     official client answers a failed poll with marker 0, so the loop its own
//     documentation shows loses the position and replays everything.
//   - Polls are not paced here. The pause between them belongs to the caller:
//     the official WithPollingPause is dead code, and against instant empty
//     answers the client spins at roughly 2000 requests a second.
//   - Writes are paced here, and only writes. The API refuses a bot that sends
//     faster than two requests a second (the 429 of the schema), and the
//     refusal costs a whole round trip to discover; a pacer in the transport
//     holds every POST, PUT and DELETE at that rate whatever calls it, while a
//     GET - the long poll above all - stays as fast as its caller asks.
package max

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	// DefaultBaseURL is the MAX Bot API root (the v2 host).
	DefaultBaseURL = "https://platform-api2.max.ru"
	// maxUpdates is the page size one poll asks for.
	maxUpdates = 50
	// maxBody caps how much of a response is read into memory.
	maxBody = 8 << 20
	// maxRetryAfter is the longest pause a 429 is waited out. A longer one is a
	// limit to report, not a pause to sit through.
	maxRetryAfter = 60 * time.Second
	// defaultPoll is how long the server may hold one poll open.
	defaultPoll = 30 * time.Second
	// writeGap is the shortest pause between two writes of this bot: the API
	// tolerates two requests a second, and this is the code that keeps the bot
	// at it (see pacer).
	writeGap = 500 * time.Millisecond
)

// ErrBadUpdate marks a page where some elements did not parse. The updates that
// did parse are returned alongside it, so the caller can log the count and
// carry on rather than stall on one bad element.
var ErrBadUpdate = errors.New("max: malformed update")

// Client talks to MAX with one bot token.
type Client struct {
	base  string
	token string
	http  *http.Client
	poll  time.Duration

	// writes spaces the bot's writes. It is shared by every caller of this
	// client - every sender of the bot - because the rate is the token's, not
	// one chat's.
	writes pacer

	mu         sync.Mutex
	cursorPath string
	cursor     int64
}

// Option configures a Client.
type Option func(*Client)

// WithHTTPClient sets the HTTP client. It is the one that carries the MinCifry
// root certificate, the proxy and the timeout, and its timeout must exceed the
// poll timeout: the official client ships 30 s for both, and a held poll then
// reads as a timeout after 93 s of retries.
func WithHTTPClient(hc *http.Client) Option { return func(c *Client) { c.http = hc } }

// WithBaseURL points the client at another API root, for tests.
func WithBaseURL(base string) Option {
	return func(c *Client) { c.base = strings.TrimRight(strings.TrimSpace(base), "/") }
}

// WithCursor keeps the poll marker in path, so a restart resumes where the bot
// stopped.
func WithCursor(path string) Option { return func(c *Client) { c.cursorPath = path } }

// WithPollTimeout sets how long the server may hold one poll open.
func WithPollTimeout(d time.Duration) Option { return func(c *Client) { c.poll = d } }

// NewClient returns a client for token. The token travels in the Authorization
// header as MAX wants it: the raw value, with no "Bearer " prefix.
func NewClient(token string, opts ...Option) *Client {
	c := &Client{
		base:  DefaultBaseURL,
		token: token,
		http:  &http.Client{Timeout: defaultPoll + 15*time.Second},
		poll:  defaultPoll,
	}
	for _, o := range opts {
		o(c)
	}
	c.loadCursor()
	return c
}

// Cursor is the marker of the last successful poll.
func (c *Client) Cursor() int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.cursor
}

func (c *Client) loadCursor() {
	if c.cursorPath == "" {
		return
	}
	b, err := os.ReadFile(c.cursorPath)
	if err != nil {
		return
	}
	if v, err := strconv.ParseInt(strings.TrimSpace(string(b)), 10, 64); err == nil {
		c.cursor = v
	}
}

// saveCursor records a marker, in memory and on disk. A write to a temporary
// file plus a rename keeps a reader from ever seeing half a number.
func (c *Client) saveCursor(v int64) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.cursor = v
	if c.cursorPath == "" {
		return nil
	}
	tmp := c.cursorPath + ".tmp"
	if err := os.WriteFile(tmp, []byte(strconv.FormatInt(v, 10)), 0o600); err != nil {
		return fmt.Errorf("max: save cursor: %w", err)
	}
	if err := os.Rename(tmp, c.cursorPath); err != nil {
		return fmt.Errorf("max: save cursor: %w", err)
	}
	return nil
}

// Updates makes one long poll: GET /updates. It returns the page and advances
// the cursor when the poll succeeded and the server sent a new non-zero marker.
// A page with a malformed element returns the updates that parsed together with
// ErrBadUpdate. It is the convenience of a caller with no handler to run between
// the page and the next poll; the poll loop uses fetchUpdates instead, so the
// cursor advances only after the page is handled.
func (c *Client) Updates(ctx context.Context) ([]Update, error) {
	updates, marker, err := c.fetchUpdates(ctx)
	if err != nil && !errors.Is(err, ErrBadUpdate) {
		return nil, err
	}
	if marker > 0 && marker != c.Cursor() {
		if serr := c.saveCursor(marker); serr != nil {
			return nil, serr
		}
	}
	return updates, err
}

// fetchUpdates makes one long poll and returns the page beside the marker the
// server answered. It does not advance the cursor: the caller decides when the
// page is handled, so a stop in the middle of it replays the page on the next
// process instead of losing the unhandled rest.
func (c *Client) fetchUpdates(ctx context.Context) ([]Update, int64, error) {
	q := url.Values{"limit": {strconv.Itoa(maxUpdates)}}
	if secs := int(c.poll.Seconds()); secs > 0 {
		q.Set("timeout", strconv.Itoa(secs))
	}
	marker := c.Cursor()
	if marker > 0 {
		q.Set("marker", strconv.FormatInt(marker, 10))
	}
	var page struct {
		Updates []json.RawMessage `json:"updates"`
		Marker  int64             `json:"marker"`
	}
	if err := c.do(ctx, http.MethodGet, "/updates", q, nil, &page); err != nil {
		return nil, 0, err
	}
	out := make([]Update, 0, len(page.Updates))
	bad := 0
	for _, raw := range page.Updates {
		var u Update
		if err := json.Unmarshal(raw, &u); err != nil {
			bad++
			continue
		}
		out = append(out, u)
	}
	if bad > 0 {
		return out, page.Marker, fmt.Errorf("%w: %d of %d skipped", ErrBadUpdate, bad, len(page.Updates))
	}
	return out, page.Marker, nil
}

// Me reads the bot's own card: GET /me. A revoked or mistyped token is refused
// here, which is what makes this the cheapest call to probe a token with.
func (c *Client) Me(ctx context.Context) (User, error) {
	var me User

	err := c.do(ctx, http.MethodGet, "/me", nil, nil, &me)

	return me, err
}

// Send posts an outgoing message and returns the id MAX gave it.
func (c *Client) Send(ctx context.Context, out Outgoing) (string, error) {
	q := url.Values{}
	switch {
	case out.Target.UserID > 0:
		q.Set("user_id", strconv.FormatInt(out.Target.UserID, 10))
	case out.Target.ChatID != 0:
		q.Set("chat_id", strconv.FormatInt(out.Target.ChatID, 10))
	default:
		return "", errors.New("max: a target is required")
	}
	// MAX reads any word of the shape name.ext as a host name, fetches it itself
	// and attaches the preview card it gets back: a tool call carrying a file
	// name filled the chat with cards for parked domains. The switch is the query
	// parameter below, and it has to be a query parameter - the same name in the
	// body is dropped without a word, which is how a day was spent on a guard
	// that never had to exist.
	q.Set("disable_link_preview", "true")
	body := map[string]any{"text": quietLinks(out.Text)}
	if out.Format != "" {
		body["format"] = out.Format
	}
	if out.Buttons != nil || out.ImageToken != "" || out.FileToken != "" {
		list := make([]any, 0, 3)
		if out.Buttons != nil {
			list = append(list, attachments(out.Buttons)...)
		}
		if out.ImageToken != "" {
			list = append(list, imageAttachment(out.ImageToken))
		}
		if out.FileToken != "" {
			list = append(list, fileAttachment(out.FileToken))
		}
		body["attachments"] = list
	}
	var res struct {
		Message struct {
			Body struct {
				Mid string `json:"mid"`
			} `json:"body"`
		} `json:"message"`
	}
	if err := c.do(ctx, http.MethodPost, "/messages", q, body, &res); err != nil {
		return "", err
	}
	return res.Message.Body.Mid, nil
}

// SetCommands replaces the hint list the messenger shows when a person types
// "/" in a dialog with the bot or in a group: PATCH /me/commands. The list
// travels in the order given, and an empty one takes the hints away instead of
// sending a null the API does not read as a list. It is a write, so it waits
// its turn in the client's pacer (two a second, the rate MAX tolerates) like
// every other non-GET request.
func (c *Client) SetCommands(ctx context.Context, cmds []BotCommand) error {
	if cmds == nil {
		cmds = []BotCommand{}
	}
	body := struct {
		Commands []BotCommand `json:"commands"`
	}{Commands: cmds}

	return c.do(ctx, http.MethodPatch, "/me/commands", nil, body, nil)
}

// The chat actions MAX takes on POST /chats/{chatId}/actions (SenderAction in
// the platform schema). Two of the six matter to a gateway: typing_on is the
// "typing…" hint a person sees while the bot works, and mark_seen is how a bot
// says it has read the chat - which is what turns the single tick on a
// person's own message into two.
const (
	// ActionTyping is the "typing…" hint. MAX shows it for a few seconds, so a
	// long turn repeats it.
	ActionTyping = "typing_on"
	// ActionMarkSeen marks the chat read for the bot.
	ActionMarkSeen = "mark_seen"
)

// SendAction posts one chat action. Unlike the message routes the chat travels
// in the path, and the action is the whole body: the schema's
// ActionRequestBody has one required field and carries no message ids, so
// mark_seen reads the chat rather than a list of messages. It is a write, so
// it waits its turn in the client's pacer like every other non-GET request.
func (c *Client) SendAction(ctx context.Context, chatID int64, action string) error {
	if chatID == 0 {
		return errors.New("max: a chat id is required")
	}
	if action == "" {
		return errors.New("max: an action is required")
	}
	body := struct {
		Action string `json:"action"`
	}{Action: action}
	path := "/chats/" + strconv.FormatInt(chatID, 10) + "/actions"

	return c.do(ctx, http.MethodPost, path, nil, body, nil)
}

// imageAttachment renders a stored photo in the shape the API takes it: an
// image attachment carrying the token UploadImage returned.
func imageAttachment(token string) map[string]any {
	return map[string]any{"type": "image", "payload": map[string]any{"token": token}}
}

// fileAttachment renders a stored document in the shape the API takes it: a
// file attachment carrying the token UploadFile returned.
func fileAttachment(token string) map[string]any {
	return map[string]any{"type": "file", "payload": map[string]any{"token": token}}
}

// UploadImage stores an image in MAX and returns the token an outgoing message
// carries as an image attachment.
func (c *Client) UploadImage(ctx context.Context, name string, data []byte) (string, error) {
	return c.uploadMedia(ctx, "image", name, "", int64(len(data)), bytes.NewReader(data))
}

// UploadFile stores a document in MAX and returns the token an outgoing message
// carries as a file attachment. size is how many bytes r must yield: the upload is
// a stream, so the request declares its length up front. A reader that yields less
// fails at the transport; one that yields more is cut at size, so a caller whose
// content may grow takes its size from the same read that verified the content.
func (c *Client) UploadFile(ctx context.Context, name, mime string, size int64, r io.Reader) (string, error) {
	return c.uploadMedia(ctx, "file", name, mime, size, r)
}

// videoDetails is the answer of GET /videos/{token}: the direct links of the
// file by resolution, and its duration.
type videoDetails struct {
	Token    string `json:"token"`
	Duration int    `json:"duration"`
	URLs     struct {
		MP41080 string `json:"mp4_1080"`
		MP4720  string `json:"mp4_720"`
		MP4480  string `json:"mp4_480"`
		MP4360  string `json:"mp4_360"`
		MP4240  string `json:"mp4_240"`
		MP4144  string `json:"mp4_144"`
	} `json:"urls"`
}

// VideoFile asks the platform where the file of a video attachment is. The
// payload of a video carries a player page, not the file, so the bytes come
// from here: the best resolution the platform offers, or "" when it offers
// none (a video still being processed answers that way).
func (c *Client) VideoFile(ctx context.Context, token string) (string, int, error) {
	var d videoDetails
	if err := c.do(ctx, http.MethodGet, "/videos/"+url.PathEscape(token), nil, nil, &d); err != nil {
		return "", 0, err
	}
	for _, candidate := range []string{
		d.URLs.MP41080, d.URLs.MP4720, d.URLs.MP4480,
		d.URLs.MP4360, d.URLs.MP4240, d.URLs.MP4144,
	} {
		if candidate != "" {
			return candidate, d.Duration, nil
		}
	}

	return "", d.Duration, nil
}

// uploadMedia stores media in MAX and returns the token an outgoing message
// carries as an attachment of that kind.
//
// The API takes media in two calls: POST /uploads?type=<kind> answers an
// address of its own, and the bytes go there as multipart/form-data in a field
// named "data", with no authorization at all; that answer names the stored
// media by a token - the first answer carries no token at all. Both calls are
// writes, so both are paced like writes. Neither is retried: a repeated upload
// leaves a second copy in the messenger's storage, and a copy nobody sends is
// nobody's to take away.
//
// The body is streamed, never held: a document may be tens of megabytes, and a
// buffer that size would be the whole process's problem instead of one file's.
// The multipart envelope around the stream is therefore built by hand, which
// keeps the request's length known - the API is given no chunked body.
func (c *Client) uploadMedia(ctx context.Context, kind, name, mime string, size int64, r io.Reader) (string, error) {
	var slot struct {
		URL string `json:"url"`
	}
	if err := c.do(ctx, http.MethodPost, "/uploads", url.Values{"type": {kind}}, nil, &slot); err != nil {
		return "", err
	}
	address := strings.TrimSpace(slot.URL)
	if address == "" {
		return "", fmt.Errorf("max: upload %s: the uploads answer carried no address", name)
	}

	head, tail, contentType, err := uploadEnvelope(name, mime)
	if err != nil {
		return "", fmt.Errorf("max: upload %s: %w", name, err)
	}
	// The limit keeps the declared length and the sent bytes equal: a file that
	// grew or shrank since its size was taken fails here instead of hanging.
	body := io.MultiReader(bytes.NewReader(head), io.LimitReader(r, size), bytes.NewReader(tail))
	if err = c.writes.wait(ctx); err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, address, body)
	if err != nil {
		return "", fmt.Errorf("max: upload %s: %w", name, err)
	}
	req.Header.Set("Content-Type", contentType)
	req.ContentLength = int64(len(head)) + size + int64(len(tail))
	resp, err := c.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("max: upload %s: %w", name, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return "", fmt.Errorf("max: upload %s: %w", name, parseAPIError(resp))
	}
	var stored struct {
		Token  string `json:"token"`
		Photos map[string]struct {
			Token string `json:"token"`
		} `json:"photos"`
	}
	if err = json.NewDecoder(io.LimitReader(resp.Body, maxBody)).Decode(&stored); err != nil {
		return "", fmt.Errorf("max: upload %s: %w", name, err)
	}
	// A file reports its token at the top; a picture reports it under a photo
	// id, which is a key and not a position: any token in that answer will do.
	if token := strings.TrimSpace(stored.Token); token != "" {
		return token, nil
	}
	for _, photo := range stored.Photos {
		if token := strings.TrimSpace(photo.Token); token != "" {
			return token, nil
		}
	}
	return "", fmt.Errorf("max: upload %s: the upload answer carried no token", name)
}

// uploadEnvelope is the multipart form-data around the file itself: the part
// header before it and the closing boundary after it. Built by hand rather than
// by multipart.Writer so that the three pieces can be counted before the first
// byte leaves, and the request still carries a length.
func uploadEnvelope(name, mime string) (head, tail []byte, contentType string, err error) {
	var raw [16]byte
	if _, err = rand.Read(raw[:]); err != nil {
		return nil, nil, "", err
	}
	boundary := "max" + hex.EncodeToString(raw[:])
	if strings.TrimSpace(mime) == "" {
		// The shape a picture has always had here, and a document whose type
		// nobody knows: the field the API reads is the bytes, not the label.
		mime = "application/octet-stream"
	}
	var buf bytes.Buffer
	fmt.Fprintf(&buf, "--%s\r\n", boundary)
	fmt.Fprintf(&buf, "Content-Disposition: form-data; name=\"data\"; filename=\"%s\"\r\n", escapeFilename(name))
	fmt.Fprintf(&buf, "Content-Type: %s\r\n\r\n", mime)
	return buf.Bytes(), []byte("\r\n--" + boundary + "--\r\n"),
		"multipart/form-data; boundary=" + boundary, nil
}

// escapeFilename keeps a name inside the part header it is written into: a
// quote ends the value early and a newline starts a header of its own.
func escapeFilename(name string) string {
	return strings.NewReplacer("\\", "\\\\", `"`, `\"`, "\r", " ", "\n", " ").Replace(name)
}

// EditMessage replaces the text of a message the bot sent and, when buttons is
// not nil, its inline keyboard: nil leaves the attachments as they are, an
// empty non-nil slice takes them away. PUT /messages reads an empty
// attachments list as "delete them" and a missing one as "leave them alone",
// which is exactly the distinction the two forms of buttons carry.
func (c *Client) EditMessage(ctx context.Context, messageID, text string, buttons [][]Button) error {
	// disable_link_preview is not in the reference for PUT /messages, but the
	// API takes it there: an edit without it put the card back on a message
	// that was sent without one.
	q := url.Values{"message_id": {messageID}, "disable_link_preview": {"true"}}
	body := map[string]any{"text": quietLinks(text)}
	if buttons != nil {
		body["attachments"] = attachments(buttons)
	}
	return c.do(ctx, http.MethodPut, "/messages", q, body, nil)
}

// EditText replaces the text of a message the bot sent, leaving its
// attachments as they are: PUT /messages.
func (c *Client) EditText(ctx context.Context, messageID, text string) error {
	return c.EditMessage(ctx, messageID, text, nil)
}

// EditMarkdown replaces the text of a message and asks MAX to render it as
// markdown. The agent writes markdown, and the client draws bold, code and
// headings from it - the same shape the web shows, instead of the markers.
func (c *Client) EditMarkdown(ctx context.Context, messageID, text string) error {
	q := url.Values{"message_id": {messageID}, "disable_link_preview": {"true"}}
	body := map[string]any{"text": quietLinks(text), "format": "markdown"}

	return c.do(ctx, http.MethodPut, "/messages", q, body, nil)
}

// attachments renders an inline keyboard in the shape the API takes it
// (schema.yaml, InlineKeyboardAttachmentRequest):
//
//	[{"type":"inline_keyboard","payload":{"buttons":[[{"type":"callback",
//	  "text":"Approve","payload":"ask:9f3c:0"}]]}}]
//
// An empty non-nil slice renders as an empty list, which is how an edit takes
// a keyboard away. Buttons without an explicit type are callback buttons, the
// only kind this bot posts.
func attachments(buttons [][]Button) []any {
	rows := make([]any, 0, len(buttons))
	for _, row := range buttons {
		if len(row) == 0 {
			// The API refuses an empty row (minItems 1 on its items).
			continue
		}
		keys := make([]Button, len(row))
		copy(keys, row)
		for i := range keys {
			if keys[i].Type == "" {
				keys[i].Type = ButtonTypeCallback
			}
		}
		rows = append(rows, keys)
	}
	if len(rows) == 0 {
		return []any{}
	}

	return []any{map[string]any{
		"type":    "inline_keyboard",
		"payload": map[string]any{"buttons": rows},
	}}
}

// ButtonTypeCallback is the only button kind the bot posts: it comes back as a
// message_callback update instead of opening a link or starting a chat.
const ButtonTypeCallback = "callback"

// AnswerCallback answers a button press: POST /answers. The notification is
// what the presser sees.
func (c *Client) AnswerCallback(ctx context.Context, callbackID, notification string) error {
	q := url.Values{"callback_id": {callbackID}}
	return c.do(ctx, http.MethodPost, "/answers", q, map[string]string{"notification": notification}, nil)
}

// APIError is a refusal from MAX, carrying the status the API answered.
type APIError struct {
	Status     int
	Code       string
	Message    string
	RetryAfter time.Duration
}

func (e *APIError) Error() string {
	msg := strings.TrimSpace(e.Message)
	if msg == "" {
		msg = http.StatusText(e.Status)
	}
	code := strings.TrimSpace(e.Code)
	if code == "" {
		return fmt.Sprintf("max: HTTP %d: %s", e.Status, msg)
	}
	return fmt.Sprintf("max: HTTP %d %s: %s", e.Status, code, msg)
}

// TooManyRequests reports whether the refusal is a rate limit.
func (e *APIError) TooManyRequests() bool { return e.Status == http.StatusTooManyRequests }

// IsStatus reports whether err is a MAX refusal with this HTTP status.
func IsStatus(err error, status int) bool {
	var ae *APIError
	return errors.As(err, &ae) && ae.Status == status
}

// IsTransient reports whether err may well not happen again: the network, a
// server error or a rate limit, as opposed to a refusal that will stand (a bad
// token, a message that does not exist).
func IsTransient(err error) bool {
	if err == nil {
		return false
	}
	var ae *APIError
	if !errors.As(err, &ae) {
		return true
	}
	return ae.TooManyRequests() || ae.Status >= 500
}

// pacer spaces a client's writes at writeGap apart - two a second, the rate
// MAX tolerates. It is a plain reservation rather than a token bucket: every
// waiter takes the next free instant and the call after it is pushed one gap
// further, so a burst of callers leaves in the order they arrived, whatever
// they do between the reservation and the request. Waiting for a slot honors
// the context, so a cancelled turn does not sit out the gap.
type pacer struct {
	mu   sync.Mutex
	next time.Time
}

// wait blocks until this client's next write may go out.
func (p *pacer) wait(ctx context.Context) error {
	p.mu.Lock()
	now := time.Now()
	if p.next.Before(now) {
		p.next = now
	}
	at := p.next
	p.next = at.Add(writeGap)
	p.mu.Unlock()
	d := time.Until(at)
	if d <= 0 {
		return nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// do sends one request and waits out a 429.
func (c *Client) do(ctx context.Context, method, path string, q url.Values, in, out any) error {
	var body []byte
	if in != nil {
		var err error
		if body, err = json.Marshal(in); err != nil {
			return fmt.Errorf("max: marshal %s %s: %w", method, path, err)
		}
	}
	for attempt := 0; ; attempt++ {
		// A retry is a request too: it waits its turn like the first attempt,
		// which is why the pace stands inside the loop rather than in front
		// of it.
		if method != http.MethodGet {
			if err := c.writes.wait(ctx); err != nil {
				return err
			}
		}
		err := c.once(ctx, method, path, q, body, out)
		var ae *APIError
		if !errors.As(err, &ae) || !ae.TooManyRequests() || ae.RetryAfter > maxRetryAfter {
			return err
		}
		pause := ae.RetryAfter
		if pause <= 0 {
			pause = time.Second << uint(attempt)
		}
		t := time.NewTimer(pause)
		select {
		case <-ctx.Done():
			t.Stop()
			return ctx.Err()
		case <-t.C:
		}
	}
}

// once performs a single request.
func (c *Client) once(ctx context.Context, method, path string, q url.Values, body []byte, out any) error {
	u := c.base + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	var r io.Reader
	if body != nil {
		r = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, u, r)
	if err != nil {
		return fmt.Errorf("max: %s %s: %w", method, path, err)
	}
	req.Header.Set("Authorization", c.token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("max: %s %s: %w", method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return parseAPIError(resp)
	}
	if out == nil {
		_, err = io.Copy(io.Discard, resp.Body)
		return err
	}
	return json.NewDecoder(io.LimitReader(resp.Body, maxBody)).Decode(out)
}

// parseAPIError reads the error body ({"code","error","message"} per the API
// schema) and the pause a 429 asked for.
func parseAPIError(resp *http.Response) error {
	e := &APIError{Status: resp.StatusCode}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("max: read error body: %w", err)
	}
	var body struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	}
	if json.Unmarshal(raw, &body) == nil {
		e.Code, e.Message = body.Code, body.Message
	} else if text := strings.TrimSpace(string(raw)); text != "" {
		e.Message = text
	}
	if s := strings.TrimSpace(resp.Header.Get("Retry-After")); s != "" {
		if secs, err := strconv.Atoi(s); err == nil && secs >= 0 {
			e.RetryAfter = time.Duration(secs) * time.Second
		}
	}
	return e
}
