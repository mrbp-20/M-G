package session

import "encoding/json"

// ToolFile is a document a finished tool call left for the surfaces to send: an
// artefact of the chat's own session, named by id.
//
// It carries no filesystem path, and that is the point: the bytes are read from
// the session's artefact store, which captured them under its own rules (no
// symlinks, nothing above the real workspace, a sha256 of the copy). A surface
// can therefore send only what the session itself captured, and a model that
// names /etc/passwd has nothing to name it to.
type ToolFile struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	MIMEType string `json:"mime_type,omitempty"`
	Size     int64  `json:"size,omitempty"`
	// Caption is what the client wrote about this file, if anything: the text
	// the chat shows beside the document. A surface puts the name first and
	// this after it.
	Caption string `json:"caption,omitempty"`
}

// ToolFilesMeta returns meta with the documents of a tool call under
// coddy.files, the _meta of its final tool_call_update. Whatever meta already
// carries stays; with no documents meta is returned as it was.
func ToolFilesMeta(meta map[string]interface{}, files []ToolFile) map[string]interface{} {
	if len(files) == 0 {
		return meta
	}
	if meta == nil {
		meta = map[string]interface{}{}
	}
	coddy, _ := meta["coddy"].(map[string]interface{})
	if coddy == nil {
		coddy = map[string]interface{}{}
		meta["coddy"] = coddy
	}
	coddy["files"] = append([]ToolFile(nil), files...)

	return meta
}

// ToolFilesFromMeta reads the documents of a tool_call_update's _meta, both the
// value the agent published in this process and its JSON on the wire.
func ToolFilesFromMeta(meta map[string]interface{}) []ToolFile {
	coddy, _ := meta["coddy"].(map[string]interface{})
	raw, ok := coddy["files"]
	if !ok || raw == nil {
		return nil
	}
	if files, ok := raw.([]ToolFile); ok {
		return append([]ToolFile(nil), files...)
	}
	data, err := json.Marshal(raw)
	if err != nil {
		return nil
	}
	var files []ToolFile
	if json.Unmarshal(data, &files) != nil || len(files) == 0 {
		return nil
	}

	return files
}
