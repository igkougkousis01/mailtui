package extract

import (
	"errors"
	"testing"

	"github.com/igkougkousis01/mailtui/internal/message"
)

func TestLinkFromTextBody(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{"a plain URL", "Confirm at https://example.test/verify/abc", "https://example.test/verify/abc"},
		{"http as well as https", "Open http://example.test/x", "http://example.test/x"},
		{"the first of several", "See https://one.test/a then https://two.test/b", "https://one.test/a"},
		{"a query string", "Go to https://example.test/v?token=abc&next=/home now", "https://example.test/v?token=abc&next=/home"},
		{"a trailing full stop", "Visit https://example.test/verify.", "https://example.test/verify"},
		{"wrapped in angle brackets", "Visit <https://example.test/verify> today", "https://example.test/verify"},
		{"in parentheses", "Visit (https://example.test/verify) today", "https://example.test/verify"},
		{"at the very end", "Visit https://example.test/verify", "https://example.test/verify"},
		{"an uppercase scheme", "Visit HTTPS://example.test/verify", "HTTPS://example.test/verify"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := Link(text(tt.body))
			if !ok {
				t.Fatalf("Link(%q) found nothing, want %q", tt.body, tt.want)
			}
			if got != tt.want {
				t.Fatalf("Link(%q) = %q, want %q", tt.body, got, tt.want)
			}
		})
	}
}

// TestLinkIgnoresNonFetchableSchemes is the list of things that look like
// links and are not the link: a script cannot follow any of them.
func TestLinkIgnoresNonFetchableSchemes(t *testing.T) {
	tests := []struct {
		name string
		html string
	}{
		{"mailto", `<a href="mailto:support@example.test">Email us</a>`},
		{"cid", `<img src="cid:logo@example.test"><a href="cid:logo@example.test">logo</a>`},
		{"javascript", `<a href="javascript:alert('hi')">Click</a>`},
		{"tel", `<a href="tel:+15551234567">Call</a>`},
		{"data", `<a href="data:text/html,<b>hi</b>">Open</a>`},
		{"an anchor", `<a href="#top">Back to top</a>`},
		{"a relative path", `<a href="/verify/abc">Verify</a>`},
		{"a scheme with no host", `<a href="https://">Verify</a>`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msg := message.Message{HTMLBody: tt.html}
			if got, ok := Link(msg); ok {
				t.Fatalf("Link(%q) = %q, want nothing", tt.html, got)
			}
		})
	}
}

// TestLinkSkipsPastUnusableHrefsToTheRealOne is the shape of a real mail
// footer, where the first href is an unsubscribe mailto and the link that
// matters comes after it.
func TestLinkSkipsPastUnusableHrefsToTheRealOne(t *testing.T) {
	msg := message.Message{
		HTMLBody: `<a href="#top">Skip</a>` +
			`<a href="mailto:support@example.test">Support</a>` +
			`<a href="https://example.test/verify/abc">Verify your account</a>`,
	}

	got, ok := Link(msg)
	if !ok {
		t.Fatal("Link found nothing past the hrefs it cannot use")
	}
	if got != "https://example.test/verify/abc" {
		t.Fatalf("Link = %q, want %q", got, "https://example.test/verify/abc")
	}
}

func TestLinkFromHTMLHrefs(t *testing.T) {
	tests := []struct {
		name string
		html string
		want string
	}{
		{
			"a quoted href",
			`<p><a href="https://example.test/verify/abc">Verify</a></p>`,
			"https://example.test/verify/abc",
		},
		{
			"a single-quoted href",
			`<a href='https://example.test/verify/abc'>Verify</a>`,
			"https://example.test/verify/abc",
		},
		{
			"an unquoted href",
			`<a href=https://example.test/verify/abc>Verify</a>`,
			"https://example.test/verify/abc",
		},
		{
			"spaces around the equals sign",
			`<a href = "https://example.test/verify/abc">Verify</a>`,
			"https://example.test/verify/abc",
		},
		{
			"an uppercase attribute",
			`<a HREF="https://example.test/verify/abc">Verify</a>`,
			"https://example.test/verify/abc",
		},
		{
			"character references in a query string",
			`<a href="https://example.test/v?token=abc&amp;next=%2Fhome">Verify</a>`,
			"https://example.test/v?token=abc&next=%2Fhome",
		},
		{
			"document order, not tag order",
			`<a href="https://one.test/a">One</a><a href="https://two.test/b">Two</a>`,
			"https://one.test/a",
		},
		{
			"a URL written as text with no anchor",
			`<p>Copy this: https://example.test/verify/abc</p>`,
			"https://example.test/verify/abc",
		},
		{
			"hreflang is not an href",
			`<a hreflang="en" href="https://example.test/verify/abc">Verify</a>`,
			"https://example.test/verify/abc",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := Link(message.Message{HTMLBody: tt.html})
			if !ok {
				t.Fatalf("Link(%q) found nothing, want %q", tt.html, tt.want)
			}
			if got != tt.want {
				t.Fatalf("Link(%q) = %q, want %q", tt.html, got, tt.want)
			}
		})
	}
}

// TestLinkPrefersTheTextBody pins the order the bodies are read in, using a
// message whose two parts disagree.
func TestLinkPrefersTheTextBody(t *testing.T) {
	msg := message.Message{
		TextBody: "Confirm at https://text.test/verify",
		HTMLBody: `<a href="https://html.test/verify">Verify</a>`,
	}

	got, ok := Link(msg)
	if !ok {
		t.Fatal("Link found nothing")
	}
	if got != "https://text.test/verify" {
		t.Fatalf("Link = %q, want the text body's %q", got, "https://text.test/verify")
	}
}

// TestLinkFallsBackToHTMLWhenTheTextBodyHasNoURL covers the common multipart
// mail whose text part is a one-line "view this in your browser" apology.
func TestLinkFallsBackToHTMLWhenTheTextBodyHasNoURL(t *testing.T) {
	msg := message.Message{
		TextBody: "This message requires an HTML-capable mail reader.",
		HTMLBody: `<a href="https://example.test/verify/abc">Verify</a>`,
	}

	got, ok := Link(msg)
	if !ok {
		t.Fatal("Link found nothing in the HTML body of a message whose text body has no URL")
	}
	if got != "https://example.test/verify/abc" {
		t.Fatalf("Link = %q, want %q", got, "https://example.test/verify/abc")
	}
}

func TestLinkWhenThereIsNone(t *testing.T) {
	tests := []struct {
		name string
		msg  message.Message
	}{
		{"an empty message", message.Message{}},
		{"prose with no URL", text("Welcome aboard. Your account is ready.")},
		{"a bare domain is not a link", text("Visit example.test/verify to continue.")},
		{"markup with no href", message.Message{HTMLBody: `<p>Welcome aboard.</p>`}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got, ok := Link(tt.msg); ok {
				t.Fatalf("Link = %q, want nothing", got)
			}
		})
	}
}

// TestLinkFromMalformedMessage is the fallback: a message that failed to parse
// has no bodies, and the raw payload is the only text there is.
func TestLinkFromMalformedMessage(t *testing.T) {
	msg := message.Message{
		Raw:        []byte("Subject broken header\r\nConfirm at https://example.test/verify/abc\r\n"),
		ParseError: errors.New("parse mail: malformed MIME header line"),
	}

	got, ok := Link(msg)
	if !ok {
		t.Fatal("Link found nothing in a malformed message whose raw payload holds a URL")
	}
	if got != "https://example.test/verify/abc" {
		t.Fatalf("Link = %q, want %q", got, "https://example.test/verify/abc")
	}
}

// TestScriptContentsAreNotSearched keeps a URL that only ever existed inside a
// tracking script out of the answer.
func TestScriptContentsAreNotSearched(t *testing.T) {
	msg := message.Message{
		HTMLBody: `<script>var beacon = "https://tracker.test/pixel";</script>` +
			`<a href="https://example.test/verify/abc">Verify</a>`,
	}

	got, ok := Link(msg)
	if !ok {
		t.Fatal("Link found nothing")
	}
	if got != "https://example.test/verify/abc" {
		t.Fatalf("Link = %q, want %q", got, "https://example.test/verify/abc")
	}
}
