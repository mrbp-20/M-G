//go:build gateway || gateway.max

package max

// Incoming pictures from MAX: attachments of type "image" are downloaded from
// their URL, turned into data URIs and passed to the session as ImageParts.
// The same size limit, timeout and MIME detection are used as for Telegram.

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
)

const (
	// maxInboundFileBytes is the most a bot downloads for one incoming file.
	maxInboundFileBytes = 20 << 20
	// fileDownloadTimeout bounds the download of one image.
	fileDownloadTimeout = 2 * time.Minute
)

// pictureExtensions names a downloaded photo after its detected type, since
// MAX sends images without a file name.
var pictureExtensions = map[string]string{
	"image/jpeg": ".jpg",
	"image/png":  ".png",
	"image/gif":  ".gif",
	"image/webp": ".webp",
	"image/bmp":  ".bmp",
}

// errImageDownload is a download that failed; its text names neither the
// URL nor any token it may carry.
var errImageDownload = errors.New("download failed")

// multimodalNote answers a picture sent to a session whose model does not
// read images.
func multimodalNote(model string) string {
	return "Модель этой беседы (" + model + ") не читает картинки. Выберите другую через /model или пришлите вопрос текстом."
}

// inboundImages lists the image attachments of a message. A malformed
// attachment is reported through ErrBadUpdate so the caller can log it and
// carry on with the attachments that parsed.
func (b *Bot) inboundImages(m Message) ([]ImageAttachment, error) {
	return m.ImageAttachments()
}

// receivedImage is one picture of an incoming message: the part the model reads
// and the bytes themselves, which are saved to the conversation's media folder
// without a second download.
type receivedImage struct {
	Part acp.ImagePartRef
	Data []byte
	Name string
}

// downloadImages fetches every image URL with the bot's own HTTP client, so
// the configured proxy and root certificates apply, and returns them as the
// session takes attachments. The first failure stops it and names the image.
func (b *Bot) downloadImages(ctx context.Context, images []ImageAttachment) ([]receivedImage, string, error) {
	out := make([]receivedImage, 0, len(images))
	for _, img := range images {
		data, err := b.downloadImage(ctx, img.URL)
		if err != nil {
			return nil, "photo_" + strconv.FormatInt(img.PhotoID, 10), err
		}
		mediaType := fileType("", data)
		name := "photo_" + strconv.FormatInt(img.PhotoID, 10)
		if ext := pictureExtensions[mediaType]; ext != "" && path.Ext(name) == "" {
			name += ext
		}
		out = append(out, receivedImage{
			Part: acp.ImagePartRef{
				DataURL: "data:" + mediaType + ";base64," + base64.StdEncoding.EncodeToString(data),
				Name:    name,
			},
			Data: data,
			Name: name,
		})
	}
	return out, "", nil
}

// downloadImage fetches one image URL and returns its bytes. The download is
// bounded by size and timeout, and no authentication header is sent: MAX
// image URLs carry their own token in the query string.
func (b *Bot) downloadImage(ctx context.Context, imageURL string) ([]byte, error) {
	if b.httpClient == nil {
		return nil, fmt.Errorf("%w: no HTTP client", errImageDownload)
	}
	ctx, cancel := context.WithTimeout(ctx, fileDownloadTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, imageURL, nil)
	if err != nil {
		return nil, fmt.Errorf("%w: %s", errImageDownload, withoutAddress(err))
	}
	resp, err := b.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: %s", errImageDownload, withoutAddress(err))
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%w: HTTP %d", errImageDownload, resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxInboundFileBytes+1))
	if err != nil {
		return nil, fmt.Errorf("%w: %s", errImageDownload, withoutAddress(err))
	}
	if len(data) > maxInboundFileBytes {
		return nil, fmt.Errorf("%w: the file is over %d bytes", errImageDownload, maxInboundFileBytes)
	}
	return data, nil
}

// fileType is the media type a downloaded file goes to the session with:
// a picture whatever the server declared, since a picture sent as a file is
// still one for the model; else the declared type, when it is one; else what
// the bytes look like.
func fileType(declared string, data []byte) string {
	sniffed, _, _ := strings.Cut(http.DetectContentType(data), ";")
	if strings.HasPrefix(sniffed, "image/") {
		return sniffed
	}
	if declared, _, err := mime.ParseMediaType(declared); err == nil && declared != "" && declared != "application/octet-stream" && !strings.HasPrefix(declared, "image/") {
		return declared
	}
	return sniffed
}

// withoutAddress is the text of a request's error without the URL it went to,
// which may carry a token.
func withoutAddress(err error) string {
	var ue *url.Error
	if errors.As(err, &ue) {
		return ue.Op + ": " + ue.Err.Error()
	}
	return err.Error()
}
