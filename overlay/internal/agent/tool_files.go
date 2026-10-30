package agent

import (
	"mime"
	"path/filepath"
	"strings"

	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// toolFilesForSurfaces describes the documents of a finished call for the _meta
// of its final tool_call_update: an artefact of the session, named by id, so a
// surface reads the copy the session captured and never a path the model wrote.
//
// A picture is not filtered out: the photo route (toolImagesForSurfaces) carries
// the pictures the model *read*, and a file the agent *shared* has no other way
// to a chat - dropping it here lost it silently, the web still showing the card.
func toolFilesForSurfaces(artifacts []llm.Artifact) []session.ToolFile {
	var out []session.ToolFile
	for _, a := range artifacts {
		if strings.TrimSpace(a.ID) == "" {
			continue
		}
		name := strings.TrimSpace(a.Name)
		if name == "" {
			name = filepath.Base(strings.TrimSpace(a.SourcePath))
		}
		if name == "" {
			continue
		}
		mimeType := mime.TypeByExtension(filepath.Ext(name))
		out = append(out, session.ToolFile{
			ID:       a.ID,
			Name:     name,
			MIMEType: mimeType,
			Size:     a.Size,
		})
	}

	return out
}
