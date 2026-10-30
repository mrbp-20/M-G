//go:build gateway || gateway.max

package max

import (
	"regexp"
	"strings"
)

// MAX does two things to a word that reads like a host name, and they need two
// different cures.
//
// The first is a preview card: the server fetches the address and attaches a
// "share" attachment. A tool call carrying README.md was enough - the card showed
// a parked domain serving ad spam - and `burst.go` was read as a host as well. The
// cure is the query parameter disable_link_preview (see client.go), which the
// reference documents on POST /messages and which the API also honours on PUT.
//
// The second is the card's twin, and the flag does not touch it: the client still
// draws the word as a hyperlink, blue and underlined. A guard against the card
// alone therefore left a chat full of addresses that look clickable, which is what
// the operator caught on 2026-10-18 with a file name in a history card.
//
// What defeats both is a zero-width space after the dot: a reader cannot see it,
// MAX no longer reads the word as a host, so neither the card nor the link
// appears, and a real URL keeps working because the words inside one are left
// alone - somebody who wrote a link meant a link.
var (
	// linkSpan is what a person meant as a link, kept exactly as written.
	linkSpan = regexp.MustCompile(`https?://[^\s]+`)
	// hostish is a word of the shape name.ext that MAX reads as a host. The
	// extension is letters only on purpose: a version such as 1.3.17 or a
	// numbered range is not a host and must stay readable.
	hostish = regexp.MustCompile(`([\p{L}\p{N}][\p{L}\p{N}_-]*)\.([\p{L}]{2,})(\b|/)`)
)

// zeroWidth is the separator that breaks the host reading without showing.
const zeroWidth = "\u200b"

// quietLinks defuses the host-like words of text so MAX stops attaching preview
// cards to them, and leaves genuine http(s) links alone.
func quietLinks(text string) string {
	if !strings.Contains(text, ".") {
		return text
	}
	var b strings.Builder
	b.Grow(len(text) + 8)
	last := 0
	for _, span := range linkSpan.FindAllStringIndex(text, -1) {
		b.WriteString(defuseHosts(text[last:span[0]]))
		b.WriteString(text[span[0]:span[1]])
		last = span[1]
	}
	b.WriteString(defuseHosts(text[last:]))

	return b.String()
}

// defuseHosts breaks every host-like word of a span that carries no link. A name
// can hold more than one dot (README.ru.md), and breaking the first one leaves
// ru.md behind, which MAX reads as a host just as readily, so the pass repeats
// until nothing host-like is left.
func defuseHosts(part string) string {
	if !strings.Contains(part, ".") {
		return part
	}
	for {
		out := hostish.ReplaceAllString(part, "${1}."+zeroWidth+"${2}${3}")
		if out == part {
			return part
		}
		part = out
	}
}
