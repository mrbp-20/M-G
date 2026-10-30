//go:build gateway || gateway.max

package max

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// tinyPNG is a 1x1 transparent PNG.
const tinyPNG = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII="

func TestFileTypePrefersPictureByMagic(t *testing.T) {
	png, err := base64.StdEncoding.DecodeString(tinyPNG)
	if err != nil {
		t.Fatalf("decode tiny png: %v", err)
	}
	if got := fileType("application/octet-stream", png); got != "image/png" {
		t.Errorf("fileType(octet-stream, png) = %q, want image/png", got)
	}
}

func TestFileTypeTrustsDeclaredNonImage(t *testing.T) {
	if got := fileType("text/plain", []byte("hello")); got != "text/plain" {
		t.Errorf("fileType(text/plain, hello) = %q, want text/plain", got)
	}
}

func TestMultimodalNoteNamesModel(t *testing.T) {
	if got := multimodalNote("m/model"); !strings.Contains(got, "m/model") {
		t.Errorf("multimodalNote does not name the model: %q", got)
	}
}

func TestDownloadImageOK(t *testing.T) {
	png, _ := base64.StdEncoding.DecodeString(tinyPNG)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(png)
	}))
	defer srv.Close()

	b := &Bot{httpClient: srv.Client(), log: discardLog()}
	data, err := b.downloadImage(context.Background(), srv.URL+"/photo")
	if err != nil {
		t.Fatalf("downloadImage: %v", err)
	}
	if string(data) != string(png) {
		t.Errorf("downloaded bytes differ")
	}
}

func TestDownloadImageBadStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "gone", http.StatusNotFound)
	}))
	defer srv.Close()

	b := &Bot{httpClient: srv.Client(), log: discardLog()}
	if _, err := b.downloadImage(context.Background(), srv.URL+"/missing"); err == nil {
		t.Fatal("expected error for 404")
	}
}

func TestDownloadImageSizeLimit(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		for i := 0; i < maxInboundFileBytes+2; i++ {
			_, _ = w.Write([]byte{0})
		}
	}))
	defer srv.Close()

	b := &Bot{httpClient: srv.Client(), log: discardLog()}
	if _, err := b.downloadImage(context.Background(), srv.URL+"/huge"); err == nil {
		t.Fatal("expected error for oversized response")
	}
}

func TestDownloadImagesProducesDataURI(t *testing.T) {
	png, _ := base64.StdEncoding.DecodeString(tinyPNG)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(png)
	}))
	defer srv.Close()

	b := &Bot{httpClient: srv.Client(), log: discardLog()}
	parts, failed, err := b.downloadImages(context.Background(), []ImageAttachment{
		{PhotoID: 42, URL: srv.URL + "/photo.png"},
	})
	if err != nil {
		t.Fatalf("downloadImages: %v (failed %q)", err, failed)
	}
	if len(parts) != 1 {
		t.Fatalf("got %d parts, want 1", len(parts))
	}
}

func TestImageAttachmentsFiltersImages(t *testing.T) {
	m := Message{
		Body: Text{
			Attachments: []json.RawMessage{
				[]byte(`{"type":"image","payload":{"photo_id":1,"url":"https://i.oneme.ru/1"}}`),
				[]byte(`{"type":"file","payload":{"file_id":2}}`),
			},
		},
	}
	imgs, err := m.ImageAttachments()
	if err != nil {
		t.Fatalf("ImageAttachments: %v", err)
	}
	if len(imgs) != 1 || imgs[0].PhotoID != 1 {
		t.Errorf("got %+v, want one image with photo_id 1", imgs)
	}
}

func TestImageAttachmentsSkipsBrokenOnes(t *testing.T) {
	m := Message{
		Body: Text{
			Attachments: []json.RawMessage{
				[]byte(`{"type":"image","payload":{"photo_id":1,"url":"https://i.oneme.ru/1"}}`),
				[]byte(`not json`),
			},
		},
	}
	imgs, err := m.ImageAttachments()
	if err == nil {
		t.Fatal("expected ErrBadUpdate from malformed attachment")
	}
	if len(imgs) != 1 {
		t.Errorf("got %d images, want 1 despite the bad attachment", len(imgs))
	}
}

func discardLog() *slog.Logger { return slog.New(slog.DiscardHandler) }
