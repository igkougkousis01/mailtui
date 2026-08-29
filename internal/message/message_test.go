package message

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// testEnvelope deliberately shares no address with any fixture's From/To
// headers, so a value appearing in an envelope field cannot have leaked in from
// the headers, or the other way round.
var testEnvelope = Envelope{
	From: "envelope-sender@smtp.test",
	To:   []string{"envelope-first@smtp.test", "envelope-second@smtp.test"},
}

var receivedAt = time.Date(2026, 8, 29, 10, 0, 0, 0, time.UTC)

// loadFixture returns the fixture bytes exactly as stored, CRLF line endings
// included, so tests parse what a real DATA payload looks like.
func loadFixture(t *testing.T, name string) []byte {
	t.Helper()

	raw, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	if !strings.Contains(string(raw), "\r\n") {
		t.Fatalf("fixture %s has no CRLF line endings; it no longer resembles SMTP DATA", name)
	}
	return raw
}

// captureFixture captures a fixture that is expected to parse cleanly, so every
// test built on it also asserts that a valid message reports no parse error.
func captureFixture(t *testing.T, name string) *Message {
	t.Helper()

	msg := Capture(testEnvelope, loadFixture(t, name), receivedAt)
	if msg.ParseError != nil {
		t.Fatalf("Capture(%s): ParseError = %v, want nil", name, msg.ParseError)
	}
	return msg
}

func TestCapturePlainText(t *testing.T) {
	msg := captureFixture(t, "plaintext.eml")

	if want := "Your receipt"; msg.Subject != want {
		t.Errorf("Subject = %q, want %q", msg.Subject, want)
	}
	if want := "App Notifications <notifications@app.test>"; msg.HeaderFrom != want {
		t.Errorf("HeaderFrom = %q, want %q", msg.HeaderFrom, want)
	}
	if want := []string{"Alice <alice@example.test>"}; !equalStrings(msg.HeaderTo, want) {
		t.Errorf("HeaderTo = %q, want %q", msg.HeaderTo, want)
	}
	if !strings.Contains(msg.TextBody, "Thanks for your order.") {
		t.Errorf("TextBody missing body text: %q", msg.TextBody)
	}
	if !strings.Contains(msg.TextBody, "Total: 12.00 EUR") {
		t.Errorf("TextBody truncated before the last line: %q", msg.TextBody)
	}
	if msg.HTMLBody != "" {
		t.Errorf("HTMLBody = %q, want empty for a text-only message", msg.HTMLBody)
	}
	if !msg.ReceivedAt.Equal(receivedAt) {
		t.Errorf("ReceivedAt = %v, want %v", msg.ReceivedAt, receivedAt)
	}
}

func TestCaptureHTML(t *testing.T) {
	msg := captureFixture(t, "html.eml")

	if want := "Welcome aboard"; msg.Subject != want {
		t.Errorf("Subject = %q, want %q", msg.Subject, want)
	}
	if !strings.Contains(msg.HTMLBody, "<h1>Welcome</h1>") {
		t.Errorf("HTMLBody missing markup: %q", msg.HTMLBody)
	}
	if msg.TextBody != "" {
		t.Errorf("TextBody = %q, want empty: an HTML-only message has no text part", msg.TextBody)
	}
}

func TestCaptureMultipartAlternative(t *testing.T) {
	msg := captureFixture(t, "alternative.eml")

	// Both alternatives must survive; a viewer picks between them later.
	if want := "Reset your password: https://app.test/reset"; !strings.Contains(msg.TextBody, want) {
		t.Errorf("TextBody = %q, want it to contain %q", msg.TextBody, want)
	}
	if want := `<a href="https://app.test/reset">`; !strings.Contains(msg.HTMLBody, want) {
		t.Errorf("HTMLBody = %q, want it to contain %q", msg.HTMLBody, want)
	}
	// The HTML part must not bleed into the text part or vice versa.
	if strings.Contains(msg.TextBody, "<a href") {
		t.Errorf("TextBody contains HTML markup: %q", msg.TextBody)
	}
	if strings.Contains(msg.HTMLBody, "Reset your password: https") {
		t.Errorf("HTMLBody contains the plain text part: %q", msg.HTMLBody)
	}

	if want := []string{"Carol <carol@example.test>", "dave@example.test"}; !equalStrings(msg.HeaderTo, want) {
		t.Errorf("HeaderTo = %q, want %q", msg.HeaderTo, want)
	}
}

// TestCaptureMultipartMixed covers the common real-world shape: an alternative
// pair nested inside a mixed part alongside an attachment.
func TestCaptureMultipartMixed(t *testing.T) {
	msg := captureFixture(t, "mixed.eml")

	if want := "Your report is attached."; !strings.Contains(msg.TextBody, want) {
		t.Errorf("TextBody = %q, want it to contain %q", msg.TextBody, want)
	}
	if want := "<p>Your report is attached.</p>"; !strings.Contains(msg.HTMLBody, want) {
		t.Errorf("HTMLBody = %q, want it to contain %q", msg.HTMLBody, want)
	}
	// Attachments are out of scope for this milestone, so the CSV payload must
	// not end up mixed into a body.
	if strings.Contains(msg.TextBody, "month,total") || strings.Contains(msg.HTMLBody, "month,total") {
		t.Errorf("attachment content leaked into a body:\ntext: %q\nhtml: %q", msg.TextBody, msg.HTMLBody)
	}
}

// TestCaptureDecodesCharsetAndEncodedWords checks that a non-UTF-8,
// quoted-printable message is decoded rather than shown as mojibake.
func TestCaptureDecodesCharsetAndEncodedWords(t *testing.T) {
	msg := captureFixture(t, "encoded.eml")

	if want := "Café réservé"; msg.Subject != want {
		t.Errorf("Subject = %q, want %q", msg.Subject, want)
	}
	if want := "Café App <cafe@app.test>"; msg.HeaderFrom != want {
		t.Errorf("HeaderFrom = %q, want %q", msg.HeaderFrom, want)
	}
	if want := "Réservation confirmée au café."; !strings.Contains(msg.TextBody, want) {
		t.Errorf("TextBody = %q, want it to contain %q", msg.TextBody, want)
	}
}

// TestCaptureKeepsEnvelopeDistinctFromHeaders is the point of the two address
// pairs: a sender can put anything in From/To, and the envelope is what
// actually decided delivery. Neither may overwrite the other.
func TestCaptureKeepsEnvelopeDistinctFromHeaders(t *testing.T) {
	msg := captureFixture(t, "plaintext.eml")

	if msg.EnvelopeFrom != testEnvelope.From {
		t.Errorf("EnvelopeFrom = %q, want %q", msg.EnvelopeFrom, testEnvelope.From)
	}
	if !equalStrings(msg.EnvelopeTo, testEnvelope.To) {
		t.Errorf("EnvelopeTo = %q, want %q", msg.EnvelopeTo, testEnvelope.To)
	}
	if msg.HeaderFrom == msg.EnvelopeFrom {
		t.Errorf("HeaderFrom and EnvelopeFrom both = %q; the header overwrote the envelope or vice versa", msg.HeaderFrom)
	}
	if equalStrings(msg.HeaderTo, msg.EnvelopeTo) {
		t.Errorf("HeaderTo and EnvelopeTo both = %q; the header overwrote the envelope or vice versa", msg.HeaderTo)
	}
}

// TestCaptureDoesNotAliasEnvelopeRecipients guards against the parsed message
// sharing the caller's slice, which would let a later append mutate a message
// we have already handed on.
func TestCaptureDoesNotAliasEnvelopeRecipients(t *testing.T) {
	env := Envelope{From: "sender@smtp.test", To: []string{"first@smtp.test"}}

	msg := Capture(env, loadFixture(t, "plaintext.eml"), receivedAt)

	env.To[0] = "mutated@smtp.test"

	if want := "first@smtp.test"; msg.EnvelopeTo[0] != want {
		t.Errorf("EnvelopeTo[0] = %q, want %q: the message aliases the caller's slice", msg.EnvelopeTo[0], want)
	}
}

// TestCapturePreservesRawBytes is what makes the catcher trustworthy: whatever we
// display, the original payload must still be available byte for byte.
func TestCapturePreservesRawBytes(t *testing.T) {
	for _, name := range []string{"plaintext.eml", "html.eml", "alternative.eml", "mixed.eml", "encoded.eml", "malformed.eml"} {
		t.Run(name, func(t *testing.T) {
			raw := loadFixture(t, name)

			msg := Capture(testEnvelope, raw, receivedAt)
			if string(msg.Raw) != string(raw) {
				t.Errorf("Raw does not match the input\n got (%d bytes): %q\nwant (%d bytes): %q",
					len(msg.Raw), msg.Raw, len(raw), raw)
			}

			// Raw must be our own copy, not a view onto the caller's buffer.
			raw[0] = 'X'
			if msg.Raw[0] == 'X' {
				t.Error("Raw aliases the caller's buffer")
			}
		})
	}
}

func TestCaptureMalformedKeepsEnvelopeAndRaw(t *testing.T) {
	raw := loadFixture(t, "malformed.eml")

	msg := Capture(testEnvelope, raw, receivedAt)

	// Everything the SMTP transaction established survives a parse failure.
	if msg.EnvelopeFrom != testEnvelope.From {
		t.Errorf("EnvelopeFrom = %q, want %q", msg.EnvelopeFrom, testEnvelope.From)
	}
	if !equalStrings(msg.EnvelopeTo, testEnvelope.To) {
		t.Errorf("EnvelopeTo = %q, want %q", msg.EnvelopeTo, testEnvelope.To)
	}
	if string(msg.Raw) != string(raw) {
		t.Errorf("Raw = %q, want %q", msg.Raw, raw)
	}
	if !msg.ReceivedAt.Equal(receivedAt) {
		t.Errorf("ReceivedAt = %v, want %v", msg.ReceivedAt, receivedAt)
	}

	if msg.ParseError == nil {
		t.Fatal("ParseError = nil for a malformed message, so the failure is not observable")
	}

	// The error has to say what went wrong, not just that something did.
	got := msg.ParseError.Error()
	if !strings.Contains(got, "parse mail") {
		t.Errorf("ParseError %q does not name the operation that failed", got)
	}
	if !strings.Contains(got, "header") {
		t.Errorf("ParseError %q does not say what was wrong with the message", got)
	}
}

func equalStrings(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}
