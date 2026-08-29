// Package extract pulls the two things a test script usually wants out of a
// captured message: a one-time code, and a link to follow.
//
// Both are heuristics over text, and both are documented as such — there is no
// header that says "the code is 483921", so a tool that offers to find one is
// offering a good guess. The guesses here are the ones that hold for the mail
// that transactional email libraries actually send, and each is small enough
// to explain in full; see OTP and Link.
package extract

import (
	"html"
	"strings"
	"unicode"

	"github.com/igkougkousis01/mailtui/internal/message"
)

// bodyText is the text the heuristics read.
//
// The text body first, because that is where a code or a link is written for a
// human without markup in the way. Failing that, the HTML body flattened to
// text — flattened by tagText, which is a tag stripper and not an HTML engine.
// Failing both, and only for a message we could not parse at all, the raw
// payload: a malformed message has no bodies to prefer, and the bytes are
// still text that may hold what was asked for.
func bodyText(m message.Message) string {
	if strings.TrimSpace(m.TextBody) != "" {
		return m.TextBody
	}
	if m.HTMLBody != "" {
		return tagText(m.HTMLBody)
	}
	if m.ParseError != nil {
		return string(m.Raw)
	}
	return ""
}

// tagText flattens HTML to the text a reader would see.
//
// It removes script and style elements along with their contents, drops
// everything between angle brackets, and resolves character references. That
// is all it does: no DOM, no error recovery, no opinion about what the markup
// meant. Feeding it broken HTML yields worse text rather than a wrong answer,
// which is the right trade for something whose output is then searched for a
// six-digit number.
func tagText(s string) string {
	s = stripElement(s, "script")
	s = stripElement(s, "style")

	// Tags become a space rather than nothing, so that <td>12</td><td>34</td>
	// reads as two numbers and not as one.
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); {
		if s[i] != '<' {
			b.WriteByte(s[i])
			i++
			continue
		}
		end := strings.IndexByte(s[i:], '>')
		if end < 0 {
			// An unclosed tag at the end of the document: there is no text
			// left to recover, so stop rather than emit the markup.
			break
		}
		b.WriteByte(' ')
		i += end + 1
	}

	return html.UnescapeString(b.String())
}

// stripElement removes every <name>...</name> element, contents included.
func stripElement(s, name string) string {
	open := "<" + name

	var b strings.Builder
	for {
		// Only a real tag: <scriptural> is prose, <script src=...> is not.
		start := indexFold(s, open, 0)
		for start >= 0 && !tagNameEnds(s, start+len(open)) {
			start = indexFold(s, open, start+1)
		}
		if start < 0 {
			b.WriteString(s)
			return b.String()
		}
		b.WriteString(s[:start])

		end := indexFold(s, "</"+name, start)
		if end < 0 {
			// Unclosed: everything after the opening tag is inside it.
			return b.String()
		}
		gt := strings.IndexByte(s[end:], '>')
		if gt < 0 {
			return b.String()
		}
		s = s[end+gt+1:]
	}
}

// tagNameEnds reports whether a tag name ends at i, rather than continuing
// into a longer one.
func tagNameEnds(s string, i int) bool {
	if i >= len(s) {
		return false
	}
	return s[i] == '>' || s[i] == '/' || asciiSpace(s[i])
}

// indexFold is strings.Index with ASCII letters compared without case, and a
// starting offset. Only ASCII folds, which is all that HTML tag and attribute
// names need.
func indexFold(s, sub string, start int) int {
	if start < 0 {
		start = 0
	}
	for i := start; i+len(sub) <= len(s); i++ {
		if equalFold(s[i:i+len(sub)], sub) {
			return i
		}
	}
	return -1
}

// equalFold compares two strings of equal length, ASCII case-insensitively.
func equalFold(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := 0; i < len(a); i++ {
		if lowerASCII(a[i]) != lowerASCII(b[i]) {
			return false
		}
	}
	return true
}

func lowerASCII(c byte) byte {
	if c >= 'A' && c <= 'Z' {
		return c + ('a' - 'A')
	}
	return c
}

func asciiSpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\f' || c == '\v'
}

// normalizeSpace collapses every run of whitespace to a single space and trims
// the ends.
//
// The OTP heuristic measures how far a number is from a word like "code", and
// that distance is only meaningful once the blank lines and indentation a mail
// template leaves behind stop counting as characters.
func normalizeSpace(s string) string {
	var b strings.Builder
	b.Grow(len(s))

	space := false
	for _, r := range s {
		if unicode.IsSpace(r) {
			space = true
			continue
		}
		if space && b.Len() > 0 {
			b.WriteByte(' ')
		}
		space = false
		b.WriteRune(r)
	}
	return b.String()
}
