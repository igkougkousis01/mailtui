package smtp

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
)

// rawMessage is CRLF-terminated like a real DATA payload. Its From and To
// headers deliberately differ from the envelope used in the tests, so a value
// in the output can be traced to the one it came from.
const rawMessage = "From: Header Sender <header-from@example.test>\r\n" +
	"To: Header Recipient <header-to@example.test>\r\n" +
	"Subject: hello\r\n" +
	"\r\n" +
	"body line one\r\nbody line two\r\n"

func TestSessionReportsEnvelopeAndParsedMessage(t *testing.T) {
	var out bytes.Buffer
	s := &session{out: &out}

	if err := s.Mail("app@example.test", nil); err != nil {
		t.Fatalf("Mail: %v", err)
	}
	for _, to := range []string{"alice@example.test", "bob@example.test"} {
		if err := s.Rcpt(to, nil); err != nil {
			t.Fatalf("Rcpt(%q): %v", to, err)
		}
	}

	// Only complete messages are reported; the envelope alone prints nothing.
	if out.Len() != 0 {
		t.Fatalf("wrote output before DATA:\n%s", out.String())
	}

	if err := s.Data(strings.NewReader(rawMessage)); err != nil {
		t.Fatalf("Data: %v", err)
	}

	// Each field is asserted on its own so these checks survive a change to
	// the surrounding output format.
	got := out.String()
	for _, want := range []string{
		"app@example.test",
		"alice@example.test",
		"bob@example.test",
		"Header Sender <header-from@example.test>",
		"Header Recipient <header-to@example.test>",
		"hello",
		"body line one",
		"body line two",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q\noutput:\n%s", want, got)
		}
	}

	// A message that parses cleanly must not be flagged.
	if strings.Contains(got, "warning") {
		t.Errorf("output warns about a well-formed message\noutput:\n%s", got)
	}
}

// TestSessionKeepsEnvelopeApartFromHeaders is the reason the session hands the
// envelope to the parser instead of letting the parser infer it: the report has
// to show both, labelled, when they disagree.
func TestSessionKeepsEnvelopeApartFromHeaders(t *testing.T) {
	var out bytes.Buffer
	s := &session{out: &out}

	if err := s.Mail("app@example.test", nil); err != nil {
		t.Fatalf("Mail: %v", err)
	}
	if err := s.Rcpt("alice@example.test", nil); err != nil {
		t.Fatalf("Rcpt: %v", err)
	}
	if err := s.Data(strings.NewReader(rawMessage)); err != nil {
		t.Fatalf("Data: %v", err)
	}

	got := out.String()
	for _, want := range []string{
		"envelope from: app@example.test",
		"envelope to:   alice@example.test",
		"header from:   Header Sender <header-from@example.test>",
		"header to:     Header Recipient <header-to@example.test>",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q\noutput:\n%s", want, got)
		}
	}
}

// TestSessionAcceptsUnparsableData pins the catcher's whole point: a message we
// cannot parse is still accepted and reported, because it is often the artifact
// the developer is trying to inspect. Only a broken SMTP transaction fails.
func TestSessionAcceptsUnparsableData(t *testing.T) {
	const malformed = "this is not a mail message\r\nnor is this\r\n"

	var out bytes.Buffer
	s := &session{out: &out}

	if err := s.Mail("app@example.test", nil); err != nil {
		t.Fatalf("Mail: %v", err)
	}
	for _, to := range []string{"alice@example.test", "bob@example.test"} {
		if err := s.Rcpt(to, nil); err != nil {
			t.Fatalf("Rcpt(%q): %v", to, err)
		}
	}

	if err := s.Data(strings.NewReader(malformed)); err != nil {
		t.Fatalf("Data rejected a malformed message: %v", err)
	}

	got := out.String()

	// The failure is reported rather than swallowed, and it says what broke.
	if !strings.Contains(got, "warning: captured but not fully parsed") {
		t.Errorf("output does not report the parse failure\noutput:\n%s", got)
	}
	if !strings.Contains(got, "malformed MIME header line") {
		t.Errorf("output does not say what could not be parsed\noutput:\n%s", got)
	}
	// The parser quotes the offending line with its CRLF still attached, which
	// would split the warning across lines and leave a stray blank one. The
	// warning must occupy exactly one line, with the report resuming below it.
	lines := strings.Split(got, "\n")
	warned := -1
	for i, line := range lines {
		if strings.HasPrefix(line, "warning:") {
			warned = i
			break
		}
	}
	if warned == -1 {
		t.Fatalf("no warning line in output:\n%s", got)
	}
	if strings.ContainsAny(lines[warned], "\r") {
		t.Errorf("warning line contains a carriage return: %q", lines[warned])
	}
	if next := lines[warned+1]; !strings.HasPrefix(next, "envelope from:") {
		t.Errorf("line after the warning = %q, want the report to resume at %q", next, "envelope from:")
	}

	// The envelope is still reported, and the payload is still accounted for.
	for _, want := range []string{
		"envelope from: app@example.test",
		"envelope to:   alice@example.test, bob@example.test",
		fmt.Sprintf("(%d raw bytes)", len(malformed)),
	} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q\noutput:\n%s", want, got)
		}
	}
}

// TestSessionResetDiscardsEnvelope covers the case Reset exists for: a client
// that abandons a message with RSET and sends another on the same connection
// must not inherit the abandoned sender or recipients.
func TestSessionResetDiscardsEnvelope(t *testing.T) {
	var out bytes.Buffer
	s := &session{out: &out}

	if err := s.Mail("stale@example.test", nil); err != nil {
		t.Fatalf("Mail: %v", err)
	}
	if err := s.Rcpt("discarded@example.test", nil); err != nil {
		t.Fatalf("Rcpt: %v", err)
	}

	s.Reset()

	if s.from != "" {
		t.Errorf("from = %q after Reset, want empty", s.from)
	}
	if len(s.to) != 0 {
		t.Errorf("to = %v after Reset, want empty", s.to)
	}

	if err := s.Mail("second@example.test", nil); err != nil {
		t.Fatalf("Mail after Reset: %v", err)
	}
	if err := s.Rcpt("kept@example.test", nil); err != nil {
		t.Fatalf("Rcpt after Reset: %v", err)
	}
	if err := s.Data(strings.NewReader(rawMessage)); err != nil {
		t.Fatalf("Data after Reset: %v", err)
	}

	got := out.String()
	for _, want := range []string{"second@example.test", "kept@example.test"} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q\noutput:\n%s", want, got)
		}
	}
	for _, unwanted := range []string{"stale@example.test", "discarded@example.test"} {
		if strings.Contains(got, unwanted) {
			t.Errorf("output leaked discarded envelope %q\noutput:\n%s", unwanted, got)
		}
	}
}
