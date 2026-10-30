//go:build gateway || gateway.max

package max

// Files a person sends to the bot.
//
// The gateway does not try to understand them. It fetches every attachment into
// the conversation's own folder and tells the agent where the file lies: what
// to do with a video or a recording is the agent's business, and it has tools
// for that. A chat that swallowed the file silently, as the gateway used to,
// left the agent with nothing to work on.
//
// The folders are Max-in/Docs for documents and Max-in/Media for pictures,
// sound and video, inside the working directory of the conversation - so every
// conversation keeps its own. Names are kept as sent when the platform gave one
// and the operating system accepts it, and no file is ever overwritten.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	// inboxDir is the folder of incoming files inside a conversation.
	inboxDir = "Max-in"
	// inboxDocs holds documents, inboxMedia everything else.
	inboxDocs  = "Docs"
	inboxMedia = "Media"
	// incomingTimeout bounds the fetch of one file. The platform's largest video
	// takes a while on a slow link; the turn it belongs to has its own, longer
	// bound (turnTimeout).
	incomingTimeout = 10 * time.Minute
	// inboxNameRunes is the longest name a saved file keeps: the same order as
	// the names MAX itself accepts, and short enough for every file system.
	inboxNameRunes = 120
	// transcriptionRunes is how much of the platform's own transcription of a
	// recording is quoted to the agent: enough to work with, short enough not to
	// crowd out the question.
	transcriptionRunes = 800
)

// Incoming limits are the platform's own, so a file MAX accepted is never
// refused here: a video up to 250 MB, a sound file up to 256 MB, a document up
// to 4 GB and a picture up to 50 MB.
const (
	maxIncomingImage int64 = 50 << 20
	maxIncomingVideo int64 = 250 << 20
	maxIncomingAudio int64 = 256 << 20
	maxIncomingFile  int64 = 4 << 30
)

// reservedNames are names the operating systems keep for their own devices: a
// file called CON is not a file on Windows whatever the extension.
var reservedNames = map[string]bool{
	"CON": true, "PRN": true, "AUX": true, "NUL": true,
	"COM1": true, "COM2": true, "COM3": true, "COM4": true, "COM5": true,
	"COM6": true, "COM7": true, "COM8": true, "COM9": true,
	"LPT1": true, "LPT2": true, "LPT3": true, "LPT4": true, "LPT5": true,
	"LPT6": true, "LPT7": true, "LPT8": true, "LPT9": true,
}

// errNoWorkingDir is what a save answers when the conversation has no working
// directory: there is nowhere to put the file, and writing it beside the
// gateway itself would leave it in the program's own tree.
var errNoWorkingDir = errors.New("у беседы нет рабочего каталога")

// savedFile is one attachment that reached the disk.
type savedFile struct {
	Kind          string
	Name          string
	Path          string
	Size          int64
	Duration      int64
	Transcription string
}

// inboxFolder is the folder an attachment of this kind goes to: a document is
// read, while a picture, a recording and a video are looked at or listened to.
func inboxFolder(kind string) string {
	if mediaKinds[kind] {
		return inboxMedia
	}

	return inboxDocs
}

// inboxPath is the folder of one kind inside a conversation's working directory.
func inboxPath(cwd, kind string) string {
	return filepath.Join(cwd, inboxDir, inboxFolder(kind))
}

// receiveFiles fetches every attachment that is not a picture into the
// conversation's folder. Pictures arrive already downloaded (the model reads
// them), so they are saved by saveIncoming instead. Every failure comes back as
// a sentence for the chat: a person who sent a file has to learn that the bot
// does not have it.
func (b *Bot) receiveFiles(ctx context.Context, cwd string, files []InboundFile) ([]savedFile, []string) {
	var saved []savedFile
	var refusals []string
	for i, f := range files {
		if f.Kind == "image" {
			continue
		}
		got, err := b.receiveFile(ctx, cwd, f, i)
		if err != nil {
			b.log.Warn("max: incoming file not saved", "kind", f.Kind, "name", f.Name, "err", err)
			refusals = append(refusals, "Файл «"+inboxDisplayName(f)+"» не сохранён: "+err.Error()+".")
			continue
		}
		b.log.Info("max: incoming file saved", "kind", got.Kind, "name", got.Name,
			"size", got.Size, "path", got.Path, "session_dir", cwd)
		saved = append(saved, got)
	}

	return saved, refusals
}

// receiveFile downloads one attachment to its folder.
func (b *Bot) receiveFile(ctx context.Context, cwd string, f InboundFile, index int) (savedFile, error) {
	if strings.TrimSpace(cwd) == "" {
		return savedFile{}, errNoWorkingDir
	}
	dir := inboxPath(cwd, f.Kind)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return savedFile{}, errors.New("не удалось создать каталог " + filepath.Join(inboxDir, inboxFolder(f.Kind)))
	}
	source := f.URL
	// The platform reports a length in two places and in different units, so
	// both are kept as they came and read once the file's own size is known
	// (inboundDuration). The details answer goes first: it describes the file
	// that was actually served.
	rawLengths := []int64{f.Duration}
	if f.Kind == "video" && f.Token != "" {
		// The payload of a video is a player page, not the file: the direct link
		// comes from the details call. A video still being processed has none,
		// and the payload page is the best that is left.
		if link, got, err := b.videoLink(ctx, f.Token); err == nil && link != "" {
			source = link
			rawLengths = append([]int64{int64(got)}, rawLengths...)
		} else if err != nil {
			b.log.Debug("max: video details failed", "err", err)
		}
	}
	if source == "" {
		return savedFile{}, errors.New("платформа не дала ссылку на файл")
	}

	tmp := filepath.Join(dir, ".incoming-"+strconv.Itoa(index)+".part")
	size, err := b.downloadIncoming(ctx, source, tmp, inboxLimit(f.Kind))
	if err != nil {
		_ = os.Remove(tmp)

		return savedFile{}, err
	}
	name := safeInboxName(f.Name, inboxExt(tmp))
	final := freeInboxPath(dir, name)
	if err := os.Rename(tmp, final); err != nil {
		_ = os.Remove(tmp)

		return savedFile{}, errors.New("не удалось сохранить файл")
	}

	return savedFile{
		Kind:          f.Kind,
		Name:          filepath.Base(final),
		Path:          final,
		Size:          size,
		Duration:      inboundDuration(f.Kind, size, rawLengths...),
		Transcription: f.Transcription,
	}, nil
}

// saveIncoming writes a picture the turn already downloaded. The bytes are the
// ones the model gets, so a picture is not fetched twice.
func (b *Bot) saveIncoming(cwd string, kind, name string, data []byte) (savedFile, error) {
	if strings.TrimSpace(cwd) == "" {
		return savedFile{}, errNoWorkingDir
	}
	dir := inboxPath(cwd, kind)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return savedFile{}, errors.New("не удалось создать каталог " + filepath.Join(inboxDir, inboxFolder(kind)))
	}
	safe := safeInboxName(name, inboxExtFor(data))
	final := freeInboxPath(dir, safe)
	if err := os.WriteFile(final, data, 0o644); err != nil {
		return savedFile{}, errors.New("не удалось сохранить файл")
	}

	return savedFile{Kind: kind, Name: filepath.Base(final), Path: final, Size: int64(len(data))}, nil
}

// videoLink asks the platform for the file of a video attachment.
func (b *Bot) videoLink(ctx context.Context, token string) (string, int, error) {
	c := b.connectedClient()
	if c == nil {
		return "", 0, errors.New("нет связи с платформой")
	}

	return c.VideoFile(ctx, token)
}

// inboxLimit is the largest file of this kind the platform itself accepts.
func inboxLimit(kind string) int64 {
	switch kind {
	case "image":
		return maxIncomingImage
	case "video":
		return maxIncomingVideo
	case "audio":
		return maxIncomingAudio
	default:
		return maxIncomingFile
	}
}

// downloadIncoming streams one file to path, never holding it in memory: a
// video of the largest size is not something to load into the bot.
func (b *Bot) downloadIncoming(ctx context.Context, rawURL, path string, limit int64) (int64, error) {
	if b.httpClient == nil {
		return 0, errors.New("нет HTTP-клиента")
	}
	ctx, cancel := context.WithTimeout(ctx, incomingTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return 0, errors.New("ссылка на файл негодная")
	}
	resp, err := b.httpClient.Do(req)
	if err != nil {
		return 0, errors.New(withoutAddress(err))
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("платформа ответила HTTP %d", resp.StatusCode)
	}
	if resp.ContentLength > limit {
		return 0, fmt.Errorf("файл больше %s", humanBytes(limit))
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return 0, errors.New("не удалось создать файл")
	}
	written, copyErr := io.Copy(f, io.LimitReader(resp.Body, limit+1))
	closeErr := f.Close()
	if copyErr != nil {
		return 0, errors.New(withoutAddress(copyErr))
	}
	if closeErr != nil {
		return 0, errors.New("не удалось записать файл")
	}
	if written > limit {
		return 0, fmt.Errorf("файл больше %s", humanBytes(limit))
	}

	return written, nil
}

// safeInboxName is the name a saved file gets: the one it arrived with when it
// is usable on this machine, else the same name repaired. A name is a name, not
// a path - separators, traversal and the characters Windows refuses never
// survive - and a name the system keeps for a device is pushed aside.
func safeInboxName(name, ext string) string {
	name = strings.ReplaceAll(strings.TrimSpace(name), "\\", "/")
	if i := strings.LastIndex(name, "/"); i >= 0 {
		name = name[i+1:]
	}
	name = strings.Trim(strings.TrimSpace(name), ".")
	name = strings.Map(func(r rune) rune {
		switch r {
		case '<', '>', ':', '"', '|', '?', '*', '/', '\\', 0:
			return '_'
		}
		if r < 0x20 || r == 0x7f {
			return '_'
		}

		return r
	}, name)
	if name == "" {
		name = "file"
	}
	// A name over the limit loses its stem, never its extension: the tail is
	// what says how to open the file. The cut is bounded from both sides, and
	// the bound must never come out negative - a name arrives from whoever
	// writes to the bot, and a negative one would panic the whole turn. An
	// "extension" that fills half the limit is not an extension at all, it is
	// the tail of a name with a dot in it, so it goes with the rest of the name.
	if reservedNames[strings.ToUpper(strings.TrimSuffix(name, filepath.Ext(name)))] {
		name = "_" + name
	}
	if filepath.Ext(name) == "" && ext != "" {
		name += ext
	}
	// A name over the limit loses its stem, never its extension: the tail is
	// what says how to open the file. The cut is bounded from both sides, and
	// the bound must never come out negative - a name arrives from whoever
	// writes to the bot, and a negative one would panic the whole turn. An
	// "extension" that fills half the limit is not an extension at all, it is
	// the tail of a name with a dot in it, so it goes with the rest of the name.
	// The limit is applied last, to the name the file is saved under: a stem
	// that just fits and then gains ".mp4" is over the limit all the same.
	if len([]rune(name)) > inboxNameRunes {
		suffix := filepath.Ext(name)
		if len([]rune(suffix)) >= inboxNameRunes/2 {
			suffix = ""
		}
		stem := []rune(strings.TrimSuffix(name, suffix))
		if room := inboxNameRunes - len([]rune(suffix)); room < len(stem) {
			stem = stem[:room]
		}
		name = string(stem) + suffix
	}

	return name
}

// freeInboxPath is name in dir, numbered when that name is taken: an arriving
// file never replaces the one before it.
func freeInboxPath(dir, name string) string {
	path := filepath.Join(dir, name)
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return path
	}
	ext := filepath.Ext(name)
	stem := strings.TrimSuffix(name, ext)
	for i := 2; i < 1000; i++ {
		candidate := filepath.Join(dir, stem+" ("+strconv.Itoa(i)+")"+ext)
		if _, err := os.Stat(candidate); os.IsNotExist(err) {
			return candidate
		}
	}

	return filepath.Join(dir, stem+" ("+strconv.FormatInt(time.Now().UnixNano(), 10)+")"+ext)
}

// inboxExt is the extension of a downloaded file, sniffed from its own bytes: a
// video arrives without a name, and a name without an extension is a file no
// tool will open by itself.
func inboxExt(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer func() { _ = f.Close() }()
	head := make([]byte, 512)
	n, _ := io.ReadFull(f, head)
	if n <= 0 {
		return ""
	}

	return inboxExtFor(head[:n])
}

// inboxExtFor is the extension of a media type: the picture table first, since
// MAX sends pictures without a name, then what the system knows of the type.
func inboxExtFor(head []byte) string {
	mediaType := fileType("", head)
	if ext := pictureExtensions[mediaType]; ext != "" {
		return ext
	}
	if exts, err := mime.ExtensionsByType(mediaType); err == nil && len(exts) > 0 {
		return exts[0]
	}

	return ""
}

// inboxDisplayName is how a file is named in a sentence to the person.
// inboundSeconds brings a length the platform reports into seconds. MAX counts
// milliseconds for a video and a recording - a 4.08-second clip arrived as 4000,
// which read as "66 min 40 s" - so the note to the agent and the chat say
// seconds only after this.
// Bitrate windows a reported length is judged by: a video or a recording whose
// length would make the file thinner or denser than this is not a length at all.
const (
	minVideoRate = 5 << 10   // 40 kbit/s - below this no video is that thin
	maxVideoRate = 2 << 20   // 16 Mbit/s - above this nothing a person sends fits
	minAudioRate = 1 << 10   // 8 kbit/s
	maxAudioRate = 100 << 10 // 800 kbit/s
)

// inboundDuration brings a length the platform reports into seconds, or 0 when
// no reading of the number can be trusted.
//
// The units are not consistent: the attachment's field came in milliseconds (a
// 4-second clip as 4000) and the video-details answer in microseconds (an
// 11.5-second clip as 11000000, which read as three hours at 2.4 MB). So the
// number is tried as milliseconds, as microseconds and as seconds, and the first
// reading that agrees with the file's own size wins; a number no reading fits is
// dropped rather than printed as a length nobody can believe.
func inboundDuration(kind string, size int64, raws ...int64) int64 {
	if size <= 0 {
		return 0
	}
	for _, raw := range raws {
		if raw <= 0 {
			continue
		}
		for _, seconds := range []int64{raw / 1000, raw / 1_000_000, raw} {
			if plausibleLength(kind, size, seconds) {
				return seconds
			}
		}
	}

	return 0
}

// plausibleLength says whether a length fits a file of this size for this kind:
// bytes per second have to stay inside the window of a real recording.
func plausibleLength(kind string, size, seconds int64) bool {
	if size <= 0 || seconds <= 0 {
		return false
	}
	rate := size / seconds
	switch kind {
	case "video":
		return rate >= minVideoRate && rate <= maxVideoRate
	case "audio":
		return rate >= minAudioRate && rate <= maxAudioRate
	}

	return false
}

// inboxDisplayName is how a file is named in a sentence to the person.
func inboxDisplayName(f InboundFile) string {
	if name := strings.TrimSpace(f.Name); name != "" {
		return name
	}
	switch f.Kind {
	case "image":
		return "снимок"
	case "video":
		return "видео"
	case "audio":
		return "запись"
	default:
		return "файл"
	}
}

// incomingBlock is what the agent is told about the files a person sent: where
// each one lies, how big it is and how long it lasts. The gateway does not open
// them - a recording is not something a text model reads - so the note ends
// with what to do about it.
func incomingBlock(saved []savedFile) string {
	if len(saved) == 0 {
		return ""
	}
	var sb strings.Builder
	sb.WriteString("\n\nВходящие файлы из МАКСа, сохранены на диск:")
	for _, f := range saved {
		sb.WriteString("\n- " + inboxKindLabel(f.Kind) + " «" + f.Name + "», " + humanBytes(f.Size))
		if f.Duration > 0 {
			sb.WriteString(", " + durationText(f.Duration))
		}
		sb.WriteString(" — " + f.Path)
		if note := strings.TrimSpace(f.Transcription); note != "" {
			sb.WriteString("\n  расшифровка от платформы: " + shortenRunes(note, transcriptionRunes))
		}
	}
	sb.WriteString("\nПрослушать и посмотреть их можно инструментами, которые у вас есть.")

	return sb.String()
}

// inboxKindLabel names an attachment kind in Russian, for a sentence to the
// person and for the note to the agent.
func inboxKindLabel(kind string) string {
	switch kind {
	case "image":
		return "снимок"
	case "video":
		return "видео"
	case "audio":
		return "запись"
	default:
		return "документ"
	}
}

// durationText writes a length in seconds the way a person reads it.
func durationText(seconds int64) string {
	if seconds < 60 {
		return strconv.FormatInt(seconds, 10) + " с"
	}
	minutes, rest := seconds/60, seconds%60
	if rest == 0 {
		return strconv.FormatInt(minutes, 10) + " мин"
	}

	return strconv.FormatInt(minutes, 10) + " мин " + strconv.FormatInt(rest, 10) + " с"
}

// humanBytes writes a size the way a person reads it: 12,3 МБ rather than
// 12918456 bytes.
func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return strconv.FormatInt(n, 10) + " Б"
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	value := float64(n) / float64(div)
	units := []string{"КБ", "МБ", "ГБ", "ТБ"}
	if exp >= len(units) {
		exp = len(units) - 1
	}
	if value < 100 {
		// A tenth matters where a person compares file sizes, and the comma is
		// how Russian writes it.
		return strings.Replace(strconv.FormatFloat(value, 'f', 1, 64), ".", ",", 1) + " " + units[exp]
	}

	return strconv.FormatFloat(value, 'f', 0, 64) + " " + units[exp]
}
