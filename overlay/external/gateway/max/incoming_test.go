//go:build gateway || gateway.max

package max

// What a person sends to the bot has to reach the disk. These tests pin the
// shape of that: two folders, names kept as sent when they are usable, nothing
// overwritten, and a refusal said out loud instead of silence.

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// inboxServer answers every download with the same bytes.
func inboxServer(t *testing.T, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)

	return srv
}

func inboxBot(t *testing.T, srv *httptest.Server) *Bot {
	t.Helper()

	return &Bot{httpClient: srv.Client(), log: discardLog()}
}

func TestReceiveFiles_SavesADocumentIntoDocs(t *testing.T) {
	srv := inboxServer(t, "%PDF-1.4 договор")
	b := inboxBot(t, srv)
	cwd := t.TempDir()

	saved, refusals := b.receiveFiles(context.Background(), cwd, []InboundFile{{
		Kind: "file",
		Name: "договор.pdf",
		URL:  srv.URL + "/doc",
	}})
	if len(refusals) != 0 {
		t.Fatalf("отказы: %v", refusals)
	}
	if len(saved) != 1 {
		t.Fatalf("сохранено %d файлов, хотим один", len(saved))
	}
	want := filepath.Join(cwd, "Max-in", "Docs", "договор.pdf")
	if saved[0].Path != want {
		t.Fatalf("путь %q, хотим %q", saved[0].Path, want)
	}
	data, err := os.ReadFile(want)
	if err != nil {
		t.Fatalf("чтение файла: %v", err)
	}
	if string(data) != "%PDF-1.4 договор" {
		t.Fatalf("в файле %q", data)
	}
	if saved[0].Size != int64(len(data)) {
		t.Errorf("размер %d, хотим %d", saved[0].Size, len(data))
	}
}

func TestReceiveFiles_PutsVideoAndSoundIntoMedia(t *testing.T) {
	// The sizes are what a real file of that length weighs: a 45-second video at
	// about 20 KB/s and a 2-second recording - the length is judged against them
	// (inboundDuration), so toy bodies would read as no length at all.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		size := 900682
		if r.URL.Path == "/a" {
			size = 20480
		}
		_, _ = w.Write(bytes.Repeat([]byte("x"), size))
	}))
	defer srv.Close()
	b := inboxBot(t, srv)
	cwd := t.TempDir()

	saved, refusals := b.receiveFiles(context.Background(), cwd, []InboundFile{
		{Kind: "video", Name: "клип.mp4", URL: srv.URL + "/v", Duration: 45000},
		{Kind: "audio", Name: "запись.mp3", URL: srv.URL + "/a", Duration: 2000},
	})
	if len(refusals) != 0 {
		t.Fatalf("отказы: %v", refusals)
	}
	if len(saved) != 2 {
		t.Fatalf("сохранено %d файлов, хотим два", len(saved))
	}
	for _, f := range saved {
		if dir := filepath.Base(filepath.Dir(f.Path)); dir != "Media" {
			t.Errorf("%s легло в %q, хотим Media", f.Name, dir)
		}
	}
	if saved[0].Duration != 45 {
		t.Errorf("длительность видео %d, хотим 45", saved[0].Duration)
	}
	if saved[1].Duration != 2 {
		t.Errorf("длительность записи %d, хотим 2", saved[1].Duration)
	}
}

func TestReceiveFiles_NeverOverwrites(t *testing.T) {
	srv := inboxServer(t, "первый")
	b := inboxBot(t, srv)
	cwd := t.TempDir()
	file := InboundFile{Kind: "file", Name: "отчёт.txt", URL: srv.URL + "/1"}

	first, _ := b.receiveFiles(context.Background(), cwd, []InboundFile{file})
	srv2 := inboxServer(t, "второй")
	b.httpClient = srv2.Client()
	file.URL = srv2.URL + "/2"
	second, _ := b.receiveFiles(context.Background(), cwd, []InboundFile{file})
	if len(first) != 1 || len(second) != 1 {
		t.Fatalf("сохранено %d и %d", len(first), len(second))
	}
	if first[0].Path == second[0].Path {
		t.Fatalf("второй файл затёр первый: %q", first[0].Path)
	}
	if got := second[0].Name; got != "отчёт (2).txt" {
		t.Errorf("имя второго файла %q, хотим «отчёт (2).txt»", got)
	}
	if data, _ := os.ReadFile(first[0].Path); string(data) != "первый" {
		t.Errorf("первый файл испорчен: %q", data)
	}
}

func TestReceiveFiles_SaysSoWhenTheFileDoesNotCome(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "нет", http.StatusNotFound)
	}))
	defer srv.Close()
	b := inboxBot(t, srv)

	saved, refusals := b.receiveFiles(context.Background(), t.TempDir(), []InboundFile{{
		Kind: "video", Name: "клип.mp4", URL: srv.URL + "/v",
	}})
	if len(saved) != 0 {
		t.Fatalf("сохранено %d файлов, хотим ноль", len(saved))
	}
	if len(refusals) != 1 {
		t.Fatalf("отказов %d, хотим одну строку для чата: %v", len(refusals), refusals)
	}
	if !strings.Contains(refusals[0], "клип.mp4") || !strings.Contains(refusals[0], "404") {
		t.Errorf("в отказе нет ни имени, ни причины: %q", refusals[0])
	}
}

func TestDownloadIncoming_RefusesAFileOverTheLimit(t *testing.T) {
	srv := inboxServer(t, strings.Repeat("x", 100))
	b := inboxBot(t, srv)
	path := filepath.Join(t.TempDir(), "big.bin")

	if _, err := b.downloadIncoming(context.Background(), srv.URL, path, 10); err == nil {
		t.Fatal("файл сверх предела скачался")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("недокачанный файл остался на диске")
	}
}

func TestSaveIncoming_WritesAPicture(t *testing.T) {
	b := &Bot{log: discardLog()}
	cwd := t.TempDir()

	got, err := b.saveIncoming(cwd, "image", "photo_7", []byte("\x89PNG\r\n\x1a\n и данные"))
	if err != nil {
		t.Fatalf("сохранение снимка: %v", err)
	}
	want := filepath.Join(cwd, "Max-in", "Media", "photo_7.png")
	if got.Path != want {
		t.Fatalf("путь %q, хотим %q", got.Path, want)
	}
	if _, err := os.Stat(want); err != nil {
		t.Fatalf("файла нет на диске: %v", err)
	}
}

func TestSafeInboxName(t *testing.T) {
	cases := []struct {
		name string
		ext  string
		want string
	}{
		{"договор.pdf", ".pdf", "договор.pdf"},
		{"photo_7", ".png", "photo_7.png"},
		{"../../etc/passwd", "", "passwd"},
		{`C:\Windows\system32\evil.dll`, "", "evil.dll"},
		{"имя:с:двоеточиями.txt", "", "имя_с_двоеточиями.txt"},
		{"CON", ".txt", "_CON.txt"},
		{"", ".mp4", "file.mp4"},
		{"   ", "", "file"},
		{"...", "", "file"},
	}
	for _, c := range cases {
		if got := safeInboxName(c.name, c.ext); got != c.want {
			t.Errorf("safeInboxName(%q, %q) = %q, хотим %q", c.name, c.ext, got, c.want)
		}
	}
}

// TestSafeInboxName_LongExtensionIsCutNotPanicked: the name arrives from
// whoever writes to the bot, and MAX puts no limit on its length. A name is cut
// by its stem, never by its extension, and the bound of that cut must stay
// positive - a negative one panics the turn that received the file instead of
// saving it under a shorter name.
func TestSafeInboxName_LongExtensionIsCutNotPanicked(t *testing.T) {
	cases := []struct{ name, ext string }{
		{"договор." + strings.Repeat("a", 200), ""},
		{strings.Repeat("a", 200), ""},
		{strings.Repeat("a", 130) + ".pdf", ""},
		{"файл." + strings.Repeat("b", 60), ""},
		{"файл." + strings.Repeat("b", 119), ".txt"},
		{strings.Repeat("я", 120), ".mp4"},
		{strings.Repeat("я", 121), ".mp4"},
	}
	for _, c := range cases {
		got := safeInboxName(c.name, c.ext) // a panic here fails the test
		if got == "" {
			t.Fatalf("safeInboxName(%q) = %q, want a usable name", c.name, got)
		}
		if n := len([]rune(got)); n > inboxNameRunes {
			t.Errorf("safeInboxName(%q) is %d runes long, want at most %d", c.name, n, inboxNameRunes)
		}
	}
	// A real extension survives the cut whole, however long the stem is.
	if got := safeInboxName(strings.Repeat("a", 130)+".pdf", ""); !strings.HasSuffix(got, ".pdf") {
		t.Errorf("safeInboxName kept %q, want the .pdf tail", got)
	}
}

func TestIncomingBlock_TellsTheAgentWhereTheFilesAre(t *testing.T) {
	note := incomingBlock([]savedFile{
		{Kind: "video", Name: "клип.mp4", Path: "/w/Max-in/Media/клип.mp4", Size: 12918456, Duration: 95},
		{Kind: "audio", Name: "запись.mp3", Path: "/w/Max-in/Media/запись.mp3", Size: 2048,
			Transcription: "привет, это проверка"},
	})
	for _, want := range []string{
		"видео «клип.mp4»", "12,3 МБ", "1 мин 35 с", "/w/Max-in/Media/клип.mp4",
		"запись «запись.mp3»", "2,0 КБ", "расшифровка от платформы: привет, это проверка",
	} {
		if !strings.Contains(note, want) {
			t.Errorf("в подсказке модели нет %q:\n%s", want, note)
		}
	}
	if incomingBlock(nil) != "" {
		t.Error("без файлов подсказка должна быть пустой")
	}
}

func TestMessage_InboundFiles_ReadsEveryKind(t *testing.T) {
	m := Message{Body: Text{Attachments: []json.RawMessage{
		[]byte(`{"type":"image","payload":{"photo_id":5,"url":"https://i/5"}}`),
		[]byte(`{"type":"video","payload":{"token":"vt","url":"https://i/v"},"duration":30000}`),
		[]byte(`{"type":"audio","payload":{"url":"https://i/a"},"transcription":"речь"}`),
		[]byte(`{"type":"file","payload":{"url":"https://i/f"},"filename":"акт.pdf","size":4096}`),
		[]byte(`{"type":"sticker","payload":{"url":"https://i/s"}}`),
	}}}

	files, err := m.InboundFiles()
	if err != nil {
		t.Fatalf("InboundFiles: %v", err)
	}
	if len(files) != 4 {
		t.Fatalf("вложений %d, хотим четыре (наклейку пропускаем): %+v", len(files), files)
	}
	if files[0].Kind != "image" || files[0].Name != "photo_5" {
		t.Errorf("снимок разобран как %+v", files[0])
	}
	// The length stays as the platform sent it: the units differ between sources,
	// so it is read against the file's size once the file is on disk.
	if files[1].Duration != 30000 || files[1].Token != "vt" {
		t.Errorf("видео разобрано как %+v", files[1])
	}
	if files[2].Transcription != "речь" {
		t.Errorf("у записи нет расшифровки: %+v", files[2])
	}
	if files[3].Name != "акт.pdf" || files[3].Size != 4096 {
		t.Errorf("документ разобран как %+v", files[3])
	}
}

func TestClient_VideoFile_PrefersTheBestLink(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/videos/vt" {
			t.Errorf("запрос ушёл на %q", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"token":"vt","duration":91,"urls":{"mp4_1080":null,"mp4_720":"https://cdn/720.mp4"}}`))
	}))
	defer srv.Close()
	c := NewClient("t", WithHTTPClient(srv.Client()), WithBaseURL(srv.URL))

	link, duration, err := c.VideoFile(context.Background(), "vt")
	if err != nil {
		t.Fatalf("VideoFile: %v", err)
	}
	if link != "https://cdn/720.mp4" {
		t.Errorf("ссылка %q", link)
	}
	if duration != 91 {
		t.Errorf("длительность %d, хотим 91", duration)
	}
}

// TestIncoming_WithoutAWorkingDirectoryWritesNothing: a conversation with no
// working directory has nowhere to keep a file. Writing beside the gateway put
// pictures into the program's own tree during a test run, so the save refuses
// instead of picking a directory by itself.
func TestIncoming_WithoutAWorkingDirectoryWritesNothing(t *testing.T) {
	srv := inboxServer(t, "данные")
	b := inboxBot(t, srv)

	if _, err := b.saveIncoming("", "image", "photo_3", []byte("\x89PNG данные")); err == nil {
		t.Error("снимок сохранился без рабочего каталога")
	}
	if _, err := b.saveIncoming("   ", "image", "photo_3", []byte("данные")); err == nil {
		t.Error("снимок сохранился, когда каталог — пробелы")
	}
	saved, refusals := b.receiveFiles(context.Background(), "", []InboundFile{{
		Kind: "video", Name: "клип.mp4", URL: srv.URL + "/v",
	}})
	if len(saved) != 0 {
		t.Errorf("сохранено %d файлов без рабочего каталога", len(saved))
	}
	if len(refusals) != 1 {
		t.Fatalf("отказов %d, хотим один: %v", len(refusals), refusals)
	}
	if !strings.Contains(refusals[0], "рабочего каталога") {
		t.Errorf("отказ не называет причину: %q", refusals[0])
	}
	if _, err := os.Stat("Max-in"); !os.IsNotExist(err) {
		t.Fatalf("рядом с пакетом появился каталог Max-in: %v", err)
	}
}

// TestInboundDuration_ChoosesTheUnit: the platform's number comes in different
// units - the attachment field in milliseconds, the video details answer in
// microseconds - and only the reading that agrees with the file's own size is
// believed.
func TestInboundDuration_ChoosesTheUnit(t *testing.T) {
	cases := []struct {
		name   string
		kind   string
		size   int64
		raws   []int64
		want   int64
		reason string
	}{
		{"видео, поле вложения в миллисекундах", "video", 831905, []int64{4000}, 4, "4 с при 812 КБ"},
		{"видео, ответ /videos в микросекундах", "video", 2466226, []int64{11000000}, 11, "11 с при 2,4 МБ"},
		{"оба источника, микросекунды первыми", "video", 2466226, []int64{11000000, 11000000}, 11, "тот же файл"},
		{"звук в миллисекундах", "audio", 20480, []int64{2000}, 2, "10 КБ/с"},
		{"звук в микросекундах", "audio", 20480, []int64{4000000}, 4, "5 КБ/с"},
		{"ни одно чтение не сходится", "video", 2466226, []int64{1}, 0, "длительность не печатаем"},
		{"нулевая длительность", "video", 1000, []int64{0}, 0, "нечего показывать"},
		{"нет размера — не гадаем", "video", 0, []int64{11000000}, 0, "сверять не с чем"},
	}
	for _, c := range cases {
		if got := inboundDuration(c.kind, c.size, c.raws...); got != c.want {
			t.Errorf("%s: inboundDuration = %d с, хотим %d (%s)", c.name, got, c.want, c.reason)
		}
	}
}

// TestReceiveFiles_VideoDurationFromTheDetailsAnswer is the field case: the file
// of a video comes from GET /videos/{token}, and that answer carries the length
// in microseconds. 11 000 000 must become 11 seconds, not "183 мин 20 с".
func TestReceiveFiles_VideoDurationFromTheDetailsAnswer(t *testing.T) {
	const size = 1 << 20
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/videos/vt" {
			_, _ = io.WriteString(w, `{"token":"vt","duration":11000000,"urls":{"mp4_720":"http://`+r.Host+`/file"}}`)
			return
		}
		_, _ = w.Write(bytes.Repeat([]byte("x"), size))
	}))
	defer srv.Close()

	b := inboxBot(t, srv)
	b.setClient(NewClient("t", WithHTTPClient(srv.Client()), WithBaseURL(srv.URL)))
	saved, refusals := b.receiveFiles(context.Background(), t.TempDir(), []InboundFile{{
		Kind: "video", Name: "клип.mp4", URL: srv.URL + "/player", Token: "vt",
	}})
	if len(refusals) != 0 {
		t.Fatalf("отказы: %v", refusals)
	}
	if len(saved) != 1 {
		t.Fatalf("сохранено %d файлов, хотим один", len(saved))
	}
	if saved[0].Duration != 11 {
		t.Errorf("длительность %d с, хотим 11", saved[0].Duration)
	}
	if saved[0].Size != size {
		t.Errorf("размер %d, хотим %d", saved[0].Size, size)
	}
}
