package smtp

import (
	"bytes"
	"strings"
	"testing"
)

// rawMessage is CRLF-terminated like a real DATA payload. It deliberately
// contains no addresses, so any address found in the output must have come
// from the envelope rather than from the body.
const rawMessage = "Subject: hello\r\n\r\nbody line one\r\nbody line two\r\n"

func TestSessionWritesEnvelopeAndBody(t *testing.T) {
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
		rawMessage,
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
