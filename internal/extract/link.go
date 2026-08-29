package extract

import (
	"html"
	"net/url"
	"strings"

	"github.com/igkougkousis01/mailtui/internal/message"
)

// Link returns one URL from m, and whether one was found.
//
// Which URL, exactly: the first http or https URL written in the text body;
// failing that, the first http or https href in the HTML body, in document
// order; failing that, the first one written as plain text in the HTML; and
// for a message that failed to parse and so has no bodies, the first one in
// the raw payload. The text body comes first because when a message has both,
// the two carry the same links and the text one has no markup to get wrong.
//
// Only http and https, and only with a host. A mailto:, cid:, tel:, data: or
// javascript: URL is not what "the link in the email" means, and following one
// is not something a test script can do. Nothing here opens anything: the URL
// is printed, and what happens to it is the caller's business.
//
// Trailing sentence punctuation is trimmed, so "visit https://x.test/a." gives
// up the full stop. A URL that genuinely ends in punctuation loses it, which
// is the accepted cost of the far more common case.
func Link(m message.Message) (string, bool) {
	if u, ok := firstURL(m.TextBody); ok {
		return u, true
	}
	if u, ok := firstHref(m.HTMLBody); ok {
		return u, true
	}
	if u, ok := firstURL(tagText(m.HTMLBody)); ok {
		return u, true
	}
	if m.TextBody == "" && m.HTMLBody == "" && m.ParseError != nil {
		return firstURL(string(m.Raw))
	}
	return "", false
}

// urlPrefixes are what a URL in running text starts with. Nothing else counts:
// a bare "example.test/path" is not distinguishable from prose.
var urlPrefixes = []string{"http://", "https://"}

// firstURL returns the first http(s) URL written in plain text.
func firstURL(text string) (string, bool) {
	runes := []rune(text)
	for _, s := range urlSpans(runes) {
		if u := string(runes[s.start:s.end]); isHTTPURL(u) {
			return u, true
		}
	}
	return "", false
}

// urlSpans returns the spans of the http(s) URLs written in text, in order.
//
// It is shared with the OTP heuristic, which uses it to ignore digits that are
// part of a link rather than a code.
func urlSpans(text []rune) []span {
	lower := foldRunes(text)

	var out []span
	for i := 0; i < len(text); {
		prefix := urlPrefixAt(lower, i)
		if prefix == 0 {
			i++
			continue
		}

		end := i + prefix
		for end < len(text) && !urlTerminator(text[end]) {
			end++
		}
		end = trimURLEnd(text, i, end)

		out = append(out, span{i, end})
		i = end
	}
	return out
}

// urlPrefixAt returns the length of the URL scheme starting at i, or 0.
func urlPrefixAt(lower []rune, i int) int {
	for _, prefix := range urlPrefixes {
		p := []rune(prefix)
		if i+len(p) > len(lower) {
			continue
		}
		if string(lower[i:i+len(p)]) == prefix {
			return len(p)
		}
	}
	return 0
}

// urlTerminator reports whether r ends a URL written in text. Whitespace ends
// it by definition; the rest are the characters that wrap a URL rather than
// belong to it — angle brackets in a plain-text mail, quotes in markup.
func urlTerminator(r rune) bool {
	switch r {
	case ' ', '\t', '\n', '\r', '<', '>', '"', '\'', '`':
		return true
	}
	return false
}

// trimURLEnd walks back over punctuation that ends a sentence rather than a
// URL. It never trims past the scheme, so the result is still a URL to parse.
func trimURLEnd(text []rune, start, end int) int {
	for end > start {
		switch text[end-1] {
		case '.', ',', ';', ':', '!', '?', ')', ']', '}':
			end--
		default:
			return end
		}
	}
	return end
}

// firstHref returns the first http(s) href in an HTML body.
//
// Attributes are read by scanning, not by parsing: this looks for href, an
// equals sign, and a value, and resolves the character references in what it
// finds so that a query string written with &amp; comes back usable. Anything
// it cannot read it skips, because the next href is usually just as good.
func firstHref(body string) (string, bool) {
	for i := 0; ; {
		at := indexFold(body, "href", i)
		if at < 0 {
			return "", false
		}
		i = at + len("href")

		value, next, ok := attrValue(body, i)
		if !ok {
			continue
		}
		i = next

		if isHTTPURL(value) {
			return value, true
		}
	}
}

// attrValue reads the value of an attribute whose name ends at i, returning it
// and where to carry on from. It reports false when what follows is not an
// attribute value at all, which is how href inside a word or a bare hreflang
// attribute gets skipped.
func attrValue(s string, i int) (value string, next int, ok bool) {
	for i < len(s) && asciiSpace(s[i]) {
		i++
	}
	if i >= len(s) || s[i] != '=' {
		return "", i, false
	}
	i++
	for i < len(s) && asciiSpace(s[i]) {
		i++
	}
	if i >= len(s) {
		return "", i, false
	}

	if quote := s[i]; quote == '"' || quote == '\'' {
		i++
		end := strings.IndexByte(s[i:], quote)
		if end < 0 {
			return "", len(s), false
		}
		return unescapeAttr(s[i : i+end]), i + end + 1, true
	}

	end := i
	for end < len(s) && !asciiSpace(s[end]) && s[end] != '>' {
		end++
	}
	return unescapeAttr(s[i:end]), end, true
}

// unescapeAttr resolves character references and trims the whitespace a
// wrapped href picks up from the markup around it.
func unescapeAttr(s string) string {
	return strings.TrimSpace(html.UnescapeString(s))
}

// isHTTPURL reports whether s is a URL a script could actually fetch.
func isHTTPURL(s string) bool {
	u, err := url.Parse(s)
	if err != nil {
		return false
	}
	switch strings.ToLower(u.Scheme) {
	case "http", "https":
		return u.Host != ""
	}
	return false
}
