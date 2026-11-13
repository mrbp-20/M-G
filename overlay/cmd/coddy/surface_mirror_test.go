package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestEveryHTTPSurfaceHasTheWayBackToTheChats guards the defect of 13.11.2026.
//
// The window's surface was built without ChatMirrors, so a message typed in the
// window reached the session and the answer reached MAX, while the person's own
// line never did: the chat showed an answer without its question, and the same
// conversation read correctly in the window and wrongly in the messenger. The
// service had the field, the two other surfaces that build an interface had not.
//
// A surface that starts turns of its own needs it, so every httpserver.Options
// literal in this package must carry it. The scan is textual on purpose: it has
// to fire at the moment the field is forgotten, before anyone runs the window.
func TestEveryHTTPSurfaceHasTheWayBackToTheChats(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("list the package: %v", err)
	}

	checked := 0
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		src, err := os.ReadFile(name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		text := string(src)
		for from := 0; ; {
			idx := strings.Index(text[from:], "httpserver.Options{")
			if idx < 0 {
				break
			}
			open := from + idx + len("httpserver.Options{") - 1
			block, ok := bracedBlock(text, open)
			if !ok {
				t.Fatalf("%s: the httpserver.Options literal at offset %d is not closed", name, open)
			}
			checked++
			if !strings.Contains(block, "ChatMirrors:") {
				line := 1 + strings.Count(text[:open], "\n")
				t.Errorf("%s:%d: httpserver.Options has no ChatMirrors, so this surface cannot show "+
					"the person's own line in the chat the session is bound to", name, line)
			}
			from = open + len(block)
		}
	}

	if checked == 0 {
		t.Fatal("no httpserver.Options literal was found: the guard would pass on an empty search")
	}
}

// bracedBlock returns the text of the composite literal that starts at start,
// which must hold an opening brace, up to and including its closing brace.
func bracedBlock(text string, start int) (string, bool) {
	depth := 0
	for i := start; i < len(text); i++ {
		switch text[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return text[start : i+1], true
			}
		}
	}

	return "", false
}
