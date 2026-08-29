package extract

import (
	"errors"
	"testing"

	"github.com/igkougkousis01/mailtui/internal/message"
)

// text returns a message whose only content is a plain-text body.
func text(body string) message.Message {
	return message.Message{Subject: "Your code", TextBody: body}
}

func TestOTPLengths(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{"four digits", "Your code is 4839.", "4839"},
		{"five digits", "Your code is 48392.", "48392"},
		{"six digits", "Your code is 483921.", "483921"},
		{"eight digits", "Your code is 48392175.", "48392175"},
		{"code on its own line", "Your login code\n\n    483921\n\nIt expires in 10 minutes.", "483921"},
		{"code before the keyword", "483921 is your verification code.", "483921"},
		{"colon and no space", "Code:483921", "483921"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := OTP(text(tt.body))
			if !ok {
				t.Fatalf("OTP(%q) found nothing, want %q", tt.body, tt.want)
			}
			if got != tt.want {
				t.Fatalf("OTP(%q) = %q, want %q", tt.body, got, tt.want)
			}
		})
	}
}

// TestOTPRejections covers everything the heuristic is supposed to refuse. Each
// case is a number a naive scan would happily return.
func TestOTPRejections(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{"no digits at all", "Welcome aboard. Your account is ready."},
		{"too few digits", "Only 12 seats left."},
		{"seven digits is not a code length", "Reference 1234567 for support."},
		{"eleven digits", "Call 12345678901 for support."},
		{"a year in a footer", "Thanks for signing up.\n\n(c) 2026 Example Inc."},
		{"a date", "Your trial ends on 2026-08-29."},
		{"a decimal", "Your total is 12.3456 units."},
		{"a phone number", "Call us on 555-1234."},
		{"a time", "The window closes at 12:3045."},
		{"digits inside a word", "Your reference is id_123456."},
		{"digits with a unit", "The banner is 1234px wide."},
		{"digits inside a URL", "Confirm at https://example.test/verify/483921 to continue."},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got, ok := OTP(text(tt.body)); ok {
				t.Fatalf("OTP(%q) = %q, want nothing", tt.body, got)
			}
		})
	}
}

// TestOTPSelectionIsDeterministic is the part of the heuristic most likely to
// be wrong in a real email, where the code shares the page with an order
// number, a year and a support line.
func TestOTPSelectionIsDeterministic(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{
			"the keyword decides, not the position",
			"Order #12345 shipped. Your code is 483921.",
			"483921",
		},
		{
			"the nearest keyword wins",
			"Your code is 483921. Another number 998877 sits further along in the same sentence.",
			"483921",
		},
		{
			"with no keyword the first candidate wins",
			"Reference 1234 and reference 5678.",
			"1234",
		},
		{
			"a real-looking mail",
			"Hi John,\n\n" +
				"Someone asked to sign in to your account on 2026-08-29.\n\n" +
				"Verification code: 483921\n\n" +
				"It expires in 10 minutes. Ticket 55123 if you need us.\n\n" +
				"(c) 2026 Example Inc.\n",
			"483921",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := OTP(text(tt.body))
			if !ok {
				t.Fatalf("OTP found nothing, want %q", tt.want)
			}
			if got != tt.want {
				t.Fatalf("OTP = %q, want %q", got, tt.want)
			}

			// Deterministic means the same answer every time, not merely a
			// plausible one: nothing here may depend on map iteration order.
			for range 20 {
				if again, _ := OTP(text(tt.body)); again != got {
					t.Fatalf("OTP returned %q and then %q for the same message", got, again)
				}
			}
		})
	}
}

// TestOTPFromHTMLOnlyBody covers the mail that has no text part at all, which
// is much of transactional mail: the markup is flattened first, and what was
// only ever styling is not read as content.
func TestOTPFromHTMLOnlyBody(t *testing.T) {
	msg := message.Message{
		HTMLBody: `<html><head><style>.banner{width:483921}</style></head>` +
			`<body><p>Your verification code is <b>558214</b></p></body></html>`,
	}

	got, ok := OTP(msg)
	if !ok {
		t.Fatal("OTP found nothing in an HTML-only message")
	}
	if got != "558214" {
		t.Fatalf("OTP = %q, want %q; a number inside a style block is not a code", got, "558214")
	}
}

// TestOTPDoesNotRejoinACodeSplitAcrossTags records a limitation rather than a
// guarantee. Tags flatten to a space, so <b>48</b><b>3921</b> reads as two
// numbers and the heuristic answers with a fragment.
//
// The alternative — flattening tags to nothing — would glue the cells of a
// table into numbers that were never written, which is the worse failure of
// the two. A code cut in half by markup is rare; adjacent table cells are not.
func TestOTPDoesNotRejoinACodeSplitAcrossTags(t *testing.T) {
	msg := message.Message{HTMLBody: `<p>Your code is <b>48</b><b>3921</b></p>`}

	got, ok := OTP(msg)
	if !ok || got != "3921" {
		t.Fatalf("OTP = %q, %v; the documented behaviour is the fragment %q", got, ok, "3921")
	}
}

// TestOTPPrefersTheTextBody pins the order the bodies are read in, using a
// message whose two parts disagree.
func TestOTPPrefersTheTextBody(t *testing.T) {
	msg := message.Message{
		TextBody: "Your code is 111111.",
		HTMLBody: "<p>Your code is 222222.</p>",
	}

	got, ok := OTP(msg)
	if !ok {
		t.Fatal("OTP found nothing")
	}
	if got != "111111" {
		t.Fatalf("OTP = %q, want the text body's %q", got, "111111")
	}
}

// TestOTPFromMalformedMessage is the fallback: a message that failed to parse
// has no bodies, and the raw payload is the only text there is. It must not
// panic on it, and it should still find the code.
func TestOTPFromMalformedMessage(t *testing.T) {
	msg := message.Message{
		Raw:        []byte("Subject broken header\r\nYour verification code is 483921\r\n"),
		ParseError: errors.New("parse mail: malformed MIME header line"),
	}

	got, ok := OTP(msg)
	if !ok {
		t.Fatal("OTP found nothing in a malformed message whose raw payload holds a code")
	}
	if got != "483921" {
		t.Fatalf("OTP = %q, want %q", got, "483921")
	}
}

// TestOTPOfAnEmptyMessage is the degenerate input: no bodies, no raw, no parse
// error. It has to answer no rather than reach past the end of an empty text.
func TestOTPOfAnEmptyMessage(t *testing.T) {
	if got, ok := OTP(message.Message{}); ok {
		t.Fatalf("OTP of the zero Message = %q, want nothing", got)
	}
}
