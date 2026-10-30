//go:build gateway || gateway.max

package max

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// Update is one event from GET /updates. The message and callback bodies stay
// raw on purpose: the adapter parses what it needs when it needs it, so an odd
// field inside a body cannot fail the whole page (see client.go), and a poll
// pays only for the fields it reads.
type Update struct {
	UpdateType string          `json:"update_type"`
	Timestamp  int64           `json:"timestamp"`
	ChatID     int64           `json:"chat_id"`
	UserID     int64           `json:"user_id"`
	MessageID  string          `json:"message_id"`
	User       *User           `json:"user"`
	Message    json.RawMessage `json:"message"`
	Callback   json.RawMessage `json:"callback"`
}

// Body parses the message of a message_created or message_edited update.
func (u Update) Body() (Message, error) {
	var m Message

	err := json.Unmarshal(u.Message, &m)

	return m, err
}

// Pressed parses the callback of a message_callback update: Payload is what the
// button carried, CallbackID answers the press.
func (u Update) Pressed() (Callback, error) {
	var cb Callback

	err := json.Unmarshal(u.Callback, &cb)

	return cb, err
}

// From is the person an update came from: the update's own user when it carries
// one, the message's sender otherwise.
func (u Update) From() User {
	if u.User != nil {
		return *u.User
	}

	m, err := u.Body()
	if err != nil {
		return User{}
	}

	return m.Sender
}

// MessageKey is the identity of the message an update carries. MAX sends the id
// of a created message inside message.body.mid and keeps the update's own
// message_id for other types, so the two are read in that order: a created
// message keyed on the update's field alone would key on an empty string and be
// dropped by the de-duplication that reads it.
func (u Update) MessageKey() string {
	if u.MessageID != "" {
		return u.MessageID
	}

	m, err := u.Body()
	if err != nil {
		return ""
	}

	return m.Body.Mid
}

// Message is the part of a MAX message the bot reads.
type Message struct {
	Sender    User      `json:"sender"`
	Recipient Recipient `json:"recipient"`
	Timestamp int64     `json:"timestamp"`
	Body      Text      `json:"body"`
}

// Text is a message's text body and any attachments it carries.
type Text struct {
	Mid         string            `json:"mid"`
	Seq         int64             `json:"seq"`
	Text        string            `json:"text"`
	Attachments []json.RawMessage `json:"attachments"`
}

// Attachment is one entry in message.body.attachments. The payload stays raw
// so an odd attachment cannot fail the whole message body. Name, size, duration
// and transcription sit beside the payload, not inside it: MAX puts them at the
// attachment level.
type Attachment struct {
	Type          string          `json:"type"`
	Payload       json.RawMessage `json:"payload"`
	Filename      string          `json:"filename"`
	Size          int64           `json:"size"`
	Duration      int64           `json:"duration"`
	Transcription string          `json:"transcription"`
}

// InboundFile is one attachment of an incoming message, ready to be saved: what
// it is, where to fetch it and how it was called. The gateway saves whatever a
// person sends - picture, document, sound or video - so the agent works with
// the file itself instead of being told about it.
type InboundFile struct {
	Kind          string // "image", "video", "audio" or "file"
	Name          string
	URL           string
	Token         string
	Size          int64
	Duration      int64
	Transcription string
}

// mediaKinds are the attachments that belong in the media folder: a picture, a
// recording or a video is something the agent looks at or listens to, while a
// document is read.
var mediaKinds = map[string]bool{"image": true, "video": true, "audio": true}

// InboundFiles lists every attachment of the message the gateway can fetch. A
// malformed one is reported through ErrBadUpdate, so the caller logs the count
// and carries on with the attachments that parsed.
func (m Message) InboundFiles() ([]InboundFile, error) {
	var out []InboundFile
	bad := 0
	for _, raw := range m.Body.Attachments {
		var a Attachment
		if err := json.Unmarshal(raw, &a); err != nil {
			bad++
			continue
		}
		switch a.Type {
		case "image", "video", "audio", "file":
		default:
			continue
		}
		var payload struct {
			Token   string `json:"token"`
			URL     string `json:"url"`
			PhotoID int64  `json:"photo_id"`
		}
		if err := json.Unmarshal(a.Payload, &payload); err != nil {
			bad++
			continue
		}
		if payload.URL == "" && payload.Token == "" {
			continue
		}
		name := a.Filename
		if name == "" && a.Type == "image" {
			name = "photo_" + strconv.FormatInt(payload.PhotoID, 10)
		}
		out = append(out, InboundFile{
			Kind:          a.Type,
			Name:          name,
			URL:           payload.URL,
			Token:         payload.Token,
			Size:          a.Size,
			Duration:      a.Duration,
			Transcription: a.Transcription,
		})
	}
	if bad > 0 {
		return out, fmt.Errorf("%w: %d of %d attachments skipped", ErrBadUpdate, bad, len(m.Body.Attachments))
	}

	return out, nil
}

// ImageAttachment is the payload of an attachment whose type is "image".
type ImageAttachment struct {
	PhotoID int64  `json:"photo_id"`
	Token   string `json:"token"`
	URL     string `json:"url"`
}

// ImageAttachments lists image attachments of the message. A malformed
// attachment returns the images that parsed together with ErrBadUpdate, so
// the caller can log the count and carry on rather than drop the message.
func (m Message) ImageAttachments() ([]ImageAttachment, error) {
	var out []ImageAttachment
	bad := 0
	for _, raw := range m.Body.Attachments {
		var a Attachment
		if err := json.Unmarshal(raw, &a); err != nil {
			bad++
			continue
		}
		if a.Type != "image" {
			continue
		}
		var img ImageAttachment
		if err := json.Unmarshal(a.Payload, &img); err != nil {
			bad++
			continue
		}
		if img.URL == "" {
			continue
		}
		out = append(out, img)
	}
	if bad > 0 {
		return out, fmt.Errorf("%w: %d of %d attachments skipped", ErrBadUpdate, bad, len(m.Body.Attachments))
	}
	return out, nil
}

// Recipient names where a message arrived. MAX fills the update's own ChatID
// and UserID for some update types only: for the rest the address is here.
type Recipient struct {
	UserID   int64  `json:"user_id"`
	ChatID   int64  `json:"chat_id"`
	ChatType string `json:"chat_type"`
}

// Callback is a button press.
type Callback struct {
	CallbackID string `json:"callback_id"`
	Payload    string `json:"payload"`
	Timestamp  int64  `json:"timestamp"`
	User       User   `json:"user"`
}

// User is the part of a MAX user card the bot reads.
type User struct {
	UserID    int64  `json:"user_id"`
	FirstName string `json:"first_name"`
	LastName  string `json:"last_name"`
	Username  string `json:"username"`
	Name      string `json:"name"`
	IsBot     bool   `json:"is_bot"`
}

// DisplayName names a person in a quote or a log, preferring the card's own
// name and falling back to the handle.
func (u User) DisplayName() string {
	if u.Name != "" {
		return u.Name
	}
	if name := strings.TrimSpace(u.FirstName + " " + u.LastName); name != "" {
		return name
	}
	if u.Username != "" {
		return "@" + strings.TrimPrefix(u.Username, "@")
	}

	return "MAX"
}

// Target addresses one place: a person's chat with the bot, or a chat. MAX
// takes exactly one of the two.
type Target struct {
	UserID int64
	ChatID int64
}

// Button is one key of an inline keyboard. A press of it comes back as a
// message_callback update carrying Payload verbatim and the CallbackID that
// answers it. Type is always "callback" - the one kind a bot's own keyboard
// uses here - and attachments() fills it in when it is left empty.
type Button struct {
	Type    string `json:"type"`
	Text    string `json:"text"`
	Payload string `json:"payload"`
}

// Outgoing is what the bot posts.
type Outgoing struct {
	Target Target
	Text   string
	Format string // "" for plain text, "markdown" or "html" for formatted
	// Buttons is the inline keyboard under the message, row by row. nil posts
	// no keyboard at all; on an edit nil leaves the attachments as they are
	// and an empty non-nil slice takes them away (the API reads an empty
	// attachments list as "delete", a missing one as "leave alone").
	Buttons [][]Button
	// ImageToken is a photo stored by UploadImage, posted as the message's
	// image attachment. It and Buttons may travel together: the API takes a
	// list of attachments.
	ImageToken string
	// FileToken is a document stored by UploadFile, posted as the message's
	// file attachment. One message carries one file: a second document is a
	// second message.
	FileToken string
}
