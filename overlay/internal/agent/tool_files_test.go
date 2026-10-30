package agent

import (
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/llm"
)

// TestToolFilesForSurfaces pins the contract a surface sends by: an artefact id,
// a name, a media type and a size - and never a path. A picture is left to the
// picture route.
func TestToolFilesForSurfaces(t *testing.T) {
	files := toolFilesForSurfaces([]llm.Artifact{
		{ID: "art_pdf", Name: "Отчёт.pdf", Size: 1024},
		{ID: "art_zip", Name: "archive.ZIP", Size: 2048},
		{ID: "art_png", Name: "picture.png", Size: 512},
		{ID: "art_nameless", SourcePath: "/workspace/notes.txt", Size: 3},
		{Name: "ghost.pdf", Size: 10},
	})
	if len(files) != 4 {
		t.Fatalf("files = %d, want 4 (an id-less entry is the only one dropped): %+v", len(files), files)
	}
	byID := map[string]string{}
	for _, f := range files {
		byID[f.ID] = f.MIMEType
	}
	if got := byID["art_png"]; got != "image/png" {
		t.Errorf("a shared picture must travel as a document, media type = %q", got)
	}
	if _, ok := byID[""]; ok {
		t.Error("an artefact without an id cannot be sent")
	}
	if got := byID["art_pdf"]; got != "application/pdf" {
		t.Errorf("pdf media type = %q", got)
	}
	if got := byID["art_zip"]; got != "application/zip" {
		t.Errorf("zip media type = %q", got)
	}
	if got := byID["art_nameless"]; !strings.HasPrefix(got, "text/plain") {
		t.Errorf("nameless artefact took its name from the source path, media type = %q", got)
	}
	for _, f := range files {
		if f.Size == 0 {
			t.Errorf("%s lost its size", f.ID)
		}
	}
}
