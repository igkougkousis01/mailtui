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
	// Attachment payloads are metadata-only, so the CSV bytes must not end up
	// mixed into a body.
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

// TestCloneSharesNothingMutable covers Clone directly, field by field, because
// it is the whole basis of the store's ownership guarantee and a new
// slice-backed field on Message would otherwise silently escape the copy.
func TestCloneSharesNothingMutable(t *testing.T) {
	original := Capture(testEnvelope, loadFixture(t, "plaintext.eml"), receivedAt)
	if original.ParseError != nil {
		t.Fatalf("ParseError = %v, want nil", original.ParseError)
	}
	if len(original.EnvelopeTo) == 0 || len(original.HeaderTo) == 0 || len(original.Raw) == 0 {
		t.Fatal("fixture has an empty slice field; the test would prove nothing")
	}

	clone := original.Clone()

	// The copy starts out saying exactly what the original says.
	if clone.Subject != original.Subject || clone.HeaderFrom != original.HeaderFrom {
		t.Error("Clone did not carry the scalar fields over")
	}
	if string(clone.Raw) != string(original.Raw) {
		t.Error("Clone did not carry Raw over")
	}
	if !equalStrings(clone.EnvelopeTo, original.EnvelopeTo) || !equalStrings(clone.HeaderTo, original.HeaderTo) {
		t.Error("Clone did not carry the address lists over")
	}

	// Writing through every mutable field of the copy leaves the original as
	// it was, which a shallow struct copy would not.
	for i := range clone.Raw {
		clone.Raw[i] = 'X'
	}
	for i := range clone.EnvelopeTo {
		clone.EnvelopeTo[i] = "mutated"
	}
	for i := range clone.HeaderTo {
		clone.HeaderTo[i] = "mutated"
	}
	clone.Subject = "mutated"

	if strings.Contains(string(original.Raw), "X") {
		t.Errorf("Raw is shared: %q", original.Raw)
	}
	if !equalStrings(original.EnvelopeTo, testEnvelope.To) {
		t.Errorf("EnvelopeTo is shared: %v", original.EnvelopeTo)
	}
	for _, to := range original.HeaderTo {
		if to == "mutated" {
			t.Errorf("HeaderTo is shared: %v", original.HeaderTo)
		}
	}
	if original.Subject == "mutated" {
		t.Error("Subject was changed through the clone")
	}
}

// TestCloneKeepsNilSlicesNil pins the difference between a faithful copy and a
// merely safe one: absent fields must stay absent.
func TestCloneKeepsNilSlicesNil(t *testing.T) {
	var empty Message

	clone := empty.Clone()

	if clone.Raw != nil {
		t.Errorf("Raw = %v, want nil", clone.Raw)
	}
	if clone.EnvelopeTo != nil {
		t.Errorf("EnvelopeTo = %v, want nil", clone.EnvelopeTo)
	}
	if clone.HeaderTo != nil {
		t.Errorf("HeaderTo = %v, want nil", clone.HeaderTo)
	}
	if clone.Headers != nil {
		t.Errorf("Headers = %v, want nil", clone.Headers)
	}
	if clone.Attachments != nil {
		t.Errorf("Attachments = %v, want nil", clone.Attachments)
	}
}

// TestCloneDoesNotAliasHeadersOrAttachments is the ownership test for the two
// slice fields inspection added. Without a clone of each, a consumer editing
// what the store handed it would edit what the store holds.
func TestCloneDoesNotAliasHeadersOrAttachments(t *testing.T) {
	original := captureFixture(t, "attachments.eml")
	original.Headers = append(original.Headers, Header{Key: "X-Extra", Value: "kept"})

	if len(original.Headers) == 0 || len(original.Attachments) == 0 {
		t.Fatal("fixture has no headers or attachments; the test would prove nothing")
	}

	clone := original.Clone()

	if len(clone.Headers) != len(original.Headers) || len(clone.Attachments) != len(original.Attachments) {
		t.Fatalf("Clone carried over %d headers and %d attachments, want %d and %d",
			len(clone.Headers), len(clone.Attachments), len(original.Headers), len(original.Attachments))
	}
	if clone.Headers[0] != original.Headers[0] || clone.Attachments[0] != original.Attachments[0] {
		t.Error("Clone did not carry the header and attachment contents over")
	}

	// Writing through every element of the copy, and appending to it, must
	// leave the original exactly as it was.
	wantHeader := original.Headers[0]
	wantAttachment := original.Attachments[0]

	for i := range clone.Headers {
		clone.Headers[i] = Header{Key: "X-Mutated", Value: "mutated"}
	}
	for i := range clone.Attachments {
		clone.Attachments[i] = Attachment{Filename: "mutated", ContentType: "mutated", Size: -1}
	}
	clone.Headers = append(clone.Headers, Header{Key: "X-Appended"})
	clone.Attachments = append(clone.Attachments, Attachment{Filename: "appended"})

	if original.Headers[0] != wantHeader {
		t.Errorf("Headers is shared: %+v, want %+v", original.Headers[0], wantHeader)
	}
	if original.Attachments[0] != wantAttachment {
		t.Errorf("Attachments is shared: %+v, want %+v", original.Attachments[0], wantAttachment)
	}
	for _, h := range original.Headers {
		if h.Key == "X-Mutated" || h.Key == "X-Appended" {
			t.Errorf("Headers is shared: %+v", original.Headers)
			break
		}
	}
	for _, a := range original.Attachments {
		if a.Filename == "mutated" || a.Filename == "appended" {
			t.Errorf("Attachments is shared: %+v", original.Attachments)
			break
		}
	}
}

// --- header preservation -------------------------------------------------

// findHeaders returns every value stored under key, in order, so a test can ask
// about a repeated header without assuming where in the block it sits.
func findHeaders(msg *Message, key string) []string {
	var out []string
	for _, h := range msg.Headers {
		if strings.EqualFold(h.Key, key) {
			out = append(out, h.Value)
		}
	}
	return out
}

// headerKeys is the header block's shape: what was there, in what order.
func headerKeys(msg *Message) []string {
	keys := make([]string, 0, len(msg.Headers))
	for _, h := range msg.Headers {
		keys = append(keys, h.Key)
	}
	return keys
}

// The headers the structured fields do not cover are the reason Headers exists:
// a developer inspecting a captured mail is usually looking for one of these.
func TestCapturePreservesHeadersForInspection(t *testing.T) {
	msg := captureFixture(t, "headers.eml")

	for _, want := range []string{"Received", "Date", "Message-ID", "MIME-Version", "Content-Type", "Content-Transfer-Encoding", "X-Mailer"} {
		if len(findHeaders(msg, want)) == 0 {
			t.Errorf("header %q was not preserved; block is %q", want, headerKeys(msg))
		}
	}

	if got := findHeaders(msg, "Message-ID"); len(got) != 1 || got[0] != "<trace-1@app.test>" {
		t.Errorf("Message-ID = %q, want [<trace-1@app.test>]", got)
	}
	if got := findHeaders(msg, "Content-Transfer-Encoding"); len(got) != 1 || got[0] != "7bit" {
		t.Errorf("Content-Transfer-Encoding = %q, want [7bit]", got)
	}
}

// The header block is a sequence, and reading a Received trace depends on that
// sequence surviving intact.
func TestCapturePreservesHeaderOrder(t *testing.T) {
	msg := captureFixture(t, "headers.eml")

	want := []string{
		"Received", "Received", "Date", "Message-ID", "MIME-Version",
		"From", "To", "Subject", "X-Mailer", "X-Tag", "X-Tag", "X-Empty",
		"Content-Type", "Content-Transfer-Encoding",
	}
	if !equalStrings(headerKeys(msg), want) {
		t.Errorf("header keys =\n %q\nwant\n %q", headerKeys(msg), want)
	}
}

// A map-shaped representation would quietly lose one of each of these pairs,
// which is exactly the information a delivery trace is made of.
func TestCaptureKeepsRepeatedHeadersApart(t *testing.T) {
	msg := captureFixture(t, "headers.eml")

	received := findHeaders(msg, "Received")
	if len(received) != 2 {
		t.Fatalf("Received appears %d times, want 2: %q", len(received), received)
	}
	if !strings.Contains(received[0], "id 0001") {
		t.Errorf("first Received = %q, want the most recent hop", received[0])
	}
	if !strings.Contains(received[1], "from app.test") {
		t.Errorf("second Received = %q, want the earlier hop", received[1])
	}

	if got, want := findHeaders(msg, "X-Tag"), []string{"alpha", "beta"}; !equalStrings(got, want) {
		t.Errorf("X-Tag = %q, want %q", got, want)
	}
}

// A folded header is one field, and it has to read as one field. The exact
// bytes, folding included, stay in Raw.
func TestCaptureUnfoldsHeaderValues(t *testing.T) {
	msg := captureFixture(t, "headers.eml")

	got := findHeaders(msg, "Received")[0]
	if strings.ContainsAny(got, "\r\n") {
		t.Errorf("Received still contains a line break: %q", got)
	}
	for _, want := range []string{"from client.test", "by catcher.test", "Sat, 29 Aug 2026 09:58:00 +0000"} {
		if !strings.Contains(got, want) {
			t.Errorf("Received = %q, want it to contain %q", got, want)
		}
	}

	// The folding itself must still be recoverable from the captured bytes.
	if !strings.Contains(string(msg.Raw), "id 0001;\r\n\t") {
		t.Error("Raw no longer shows the original folding")
	}
}

// Canonicalising the key would show a developer a spelling their mail library
// never emitted, which defeats the point of an inspector.
func TestCaptureKeepsOriginalHeaderCasing(t *testing.T) {
	msg := captureFixture(t, "headers.eml")

	for _, want := range []string{"MIME-Version", "Message-ID"} {
		found := false
		for _, h := range msg.Headers {
			if h.Key == want {
				found = true
			}
		}
		if !found {
			t.Errorf("no header spelled %q; block is %q", want, headerKeys(msg))
		}
	}
}

// A valueless header is still a header, and dropping it would misrepresent what
// was sent.
func TestCaptureKeepsEmptyHeaderValue(t *testing.T) {
	msg := captureFixture(t, "headers.eml")

	got := findHeaders(msg, "X-Empty")
	if len(got) != 1 {
		t.Fatalf("X-Empty appears %d times, want 1", len(got))
	}
	if got[0] != "" {
		t.Errorf("X-Empty = %q, want an empty value", got[0])
	}
}

// A message we cannot parse has no header block to show, and must say so by
// being empty rather than by panicking somewhere downstream.
func TestCaptureMalformedHasNoHeadersOrAttachments(t *testing.T) {
	msg := Capture(testEnvelope, loadFixture(t, "malformed.eml"), receivedAt)

	if msg.ParseError == nil {
		t.Fatal("precondition: ParseError = nil, want a failure")
	}
	if msg.Headers != nil {
		t.Errorf("Headers = %q, want nil", msg.Headers)
	}
	if msg.Attachments != nil {
		t.Errorf("Attachments = %v, want nil", msg.Attachments)
	}
}

// --- attachment metadata -------------------------------------------------

func TestCaptureAttachmentMetadata(t *testing.T) {
	msg := captureFixture(t, "attachments.eml")

	if len(msg.Attachments) != 2 {
		t.Fatalf("Attachments = %d, want 2: %+v", len(msg.Attachments), msg.Attachments)
	}

	pdf := msg.Attachments[0]
	// The filename is an RFC 2047 encoded-word in the fixture; showing it
	// undecoded would be worse than showing nothing.
	if want := "rapport café.pdf"; pdf.Filename != want {
		t.Errorf("Attachments[0].Filename = %q, want %q", pdf.Filename, want)
	}
	if want := "application/pdf"; pdf.ContentType != want {
		t.Errorf("Attachments[0].ContentType = %q, want %q", pdf.ContentType, want)
	}
	if want := "attachment"; pdf.Disposition != want {
		t.Errorf("Attachments[0].Disposition = %q, want %q", pdf.Disposition, want)
	}
	// Size is the decoded payload, not the base64 that carried it: the fixture
	// sends 12 bytes on the wire for 9 bytes of PDF.
	if want := int64(len("%PDF-1.4\n")); pdf.Size != want {
		t.Errorf("Attachments[0].Size = %d, want %d (the decoded length)", pdf.Size, want)
	}

	bin := msg.Attachments[1]
	// No filename in the disposition, so the discouraged Content-Type name is
	// the only thing the sender gave us and is better than nothing.
	if want := "notes.bin"; bin.Filename != want {
		t.Errorf("Attachments[1].Filename = %q, want %q", bin.Filename, want)
	}
	if want := "application/octet-stream"; bin.ContentType != want {
		t.Errorf("Attachments[1].ContentType = %q, want %q", bin.ContentType, want)
	}
	if want := int64(len("0123456789")); bin.Size != want {
		t.Errorf("Attachments[1].Size = %d, want %d", bin.Size, want)
	}

	// The body must still be a body.
	if want := "Both files are attached."; !strings.Contains(msg.TextBody, want) {
		t.Errorf("TextBody = %q, want it to contain %q", msg.TextBody, want)
	}
	// And no payload may have been kept anywhere it does not belong.
	if strings.Contains(msg.TextBody, "0123456789") || strings.Contains(msg.HTMLBody, "0123456789") {
		t.Errorf("attachment payload leaked into a body:\ntext: %q\nhtml: %q", msg.TextBody, msg.HTMLBody)
	}
}

// The common real-world shape: an alternative pair nested inside a mixed part
// alongside one attachment.
func TestCaptureMultipartMixedAttachmentMetadata(t *testing.T) {
	msg := captureFixture(t, "mixed.eml")

	if len(msg.Attachments) != 1 {
		t.Fatalf("Attachments = %d, want 1: %+v", len(msg.Attachments), msg.Attachments)
	}
	att := msg.Attachments[0]
	if att.Filename != "report.csv" || att.ContentType != "text/csv" || att.Disposition != "attachment" {
		t.Errorf("Attachments[0] = %+v, want report.csv / text/csv / attachment", att)
	}
	if att.Size <= 0 {
		t.Errorf("Attachments[0].Size = %d, want the size of the CSV", att.Size)
	}
	if att.ContentID != "" {
		t.Errorf("Attachments[0].ContentID = %q, want empty: the part has no Content-ID", att.ContentID)
	}
}

// An embedded image is dispositioned inline, which the MIME reader treats the
// same way it treats body text. It is neither: putting it in TextBody would
// splice binary into the preview, and calling it a plain attachment would hide
// that the HTML refers to it.
func TestCaptureInlinePartIsNotBodyText(t *testing.T) {
	msg := captureFixture(t, "inline.eml")

	if want := `<img src="cid:logo@app.test">`; !strings.Contains(msg.HTMLBody, want) {
		t.Errorf("HTMLBody = %q, want it to contain %q", msg.HTMLBody, want)
	}
	if msg.TextBody != "" {
		t.Errorf("TextBody = %q, want empty: the image is not body text", msg.TextBody)
	}

	if len(msg.Attachments) != 1 {
		t.Fatalf("Attachments = %d, want the inline image: %+v", len(msg.Attachments), msg.Attachments)
	}
	got := msg.Attachments[0]
	if got.Filename != "logo.png" || got.ContentType != "image/png" {
		t.Errorf("Attachments[0] = %+v, want logo.png / image/png", got)
	}
	// Recorded as inline, not as an attachment: a viewer has to be able to tell
	// a cid: image apart from a file the sender meant to send.
	if want := "inline"; got.Disposition != want {
		t.Errorf("Attachments[0].Disposition = %q, want %q", got.Disposition, want)
	}
	// The Content-ID is what ties it to the cid: URL in the HTML, and is kept
	// without its angle brackets so the two compare directly.
	if want := "logo@app.test"; got.ContentID != want {
		t.Errorf("Attachments[0].ContentID = %q, want %q", got.ContentID, want)
	}
	if want := int64(len("hello-png")); got.Size != want {
		t.Errorf("Attachments[0].Size = %d, want %d", got.Size, want)
	}
}

// A plain text or HTML part is body text even though it carries the same MIME
// metadata an attachment does. Nothing here may become an attachment.
func TestCaptureTextPartsAreNeverAttachments(t *testing.T) {
	for _, name := range []string{"plaintext.eml", "html.eml", "alternative.eml", "encoded.eml", "headers.eml"} {
		t.Run(name, func(t *testing.T) {
			msg := captureFixture(t, name)
			if len(msg.Attachments) != 0 {
				t.Errorf("Attachments = %+v, want none", msg.Attachments)
			}
		})
	}
}
