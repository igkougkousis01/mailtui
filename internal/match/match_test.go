package match

import (
	"errors"
	"testing"

	"github.com/igkougkousis01/mailtui/internal/message"
)

// sample is a message whose envelope and headers deliberately disagree, so a
// test can tell which side a match came from.
func sample() message.Message {
	return message.Message{
		ID:           "1",
		EnvelopeFrom: "bounce@envelope.test",
		EnvelopeTo:   []string{"envelope-rcpt@example.test"},
		HeaderFrom:   "Support <support@header.test>",
		HeaderTo:     []string{"John <john@header.test>"},
		Subject:      "Reset your password",
		TextBody:     "Hello John,\nUse the code 483921 to continue.\n",
		HTMLBody:     "<p>Only in the markup</p>",
	}
}

func TestEmptyCriteriaMatchEverything(t *testing.T) {
	var c Criteria
	if !c.Empty() {
		t.Fatal("the zero Criteria reports itself as constraining something")
	}
	if !c.Match(sample()) {
		t.Fatal("the zero Criteria did not match; it must, so that waiting for any message needs no special case")
	}
	if !c.Match(message.Message{}) {
		t.Fatal("the zero Criteria did not match the zero Message")
	}
}

// TestMatch covers each field on its own: what it accepts, what it rejects,
// and — for the addressing flags — which side of the message it is allowed to
// read.
func TestMatch(t *testing.T) {
	tests := []struct {
		name string
		c    Criteria
		want bool
	}{
		{"subject substring", Criteria{Subject: "password"}, true},
		{"subject whole value", Criteria{Subject: "Reset your password"}, true},
		{"subject differing case", Criteria{Subject: "RESET YOUR PASSWORD"}, true},
		{"subject absent", Criteria{Subject: "Welcome"}, false},

		{"to matches the envelope side", Criteria{To: "envelope-rcpt@example.test"}, true},
		{"to matches the header side", Criteria{To: "john@header.test"}, true},
		{"to matches neither", Criteria{To: "someone@else.test"}, false},
		{"envelope-to matches the envelope", Criteria{EnvelopeTo: "envelope-rcpt"}, true},
		{"envelope-to does not read the header", Criteria{EnvelopeTo: "john@header.test"}, false},
		{"header-to matches the header", Criteria{HeaderTo: "john@header.test"}, true},
		{"header-to does not read the envelope", Criteria{HeaderTo: "envelope-rcpt"}, false},

		{"from matches the envelope side", Criteria{From: "bounce@envelope.test"}, true},
		{"from matches the header side", Criteria{From: "support@header.test"}, true},
		{"from matches neither", Criteria{From: "nobody@else.test"}, false},
		{"envelope-from matches the envelope", Criteria{EnvelopeFrom: "bounce@"}, true},
		{"envelope-from does not read the header", Criteria{EnvelopeFrom: "support@header.test"}, false},
		{"header-from matches the header", Criteria{HeaderFrom: "support@header.test"}, true},
		{"header-from does not read the envelope", Criteria{HeaderFrom: "bounce@envelope.test"}, false},

		{"contains reads the text body", Criteria{Contains: "Use the code"}, true},
		{"contains reads the subject", Criteria{Contains: "Reset your"}, true},
		{"contains reads the HTML body", Criteria{Contains: "Only in the markup"}, true},
		{"contains ignoring case", Criteria{Contains: "USE THE CODE"}, true},
		{"contains absent", Criteria{Contains: "Verify your account"}, false},

		{"every field must hold", Criteria{To: "john@header.test", Subject: "password", Contains: "483921"}, true},
		{"one field failing fails the match", Criteria{To: "john@header.test", Subject: "Welcome"}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.c.Match(sample()); got != tt.want {
				t.Fatalf("Match() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestMatchingIsCaseInsensitiveBothWays guards the rule rather than one
// example of it: neither the criterion nor the message decides the casing.
func TestMatchingIsCaseInsensitiveBothWays(t *testing.T) {
	msg := sample()
	msg.Subject = "WELCOME ABOARD"
	msg.EnvelopeTo = []string{"John@Example.Test"}

	c := Criteria{Subject: "welcome", EnvelopeTo: "john@example.test"}
	if !c.Match(msg) {
		t.Fatal("a lower-case criterion did not match an upper-case message")
	}

	c = Criteria{Subject: "WELCOME", EnvelopeTo: "JOHN@EXAMPLE.TEST"}
	if !c.Match(msg) {
		t.Fatal("an upper-case criterion did not match; matching must not depend on which side is capitalised")
	}
}

// TestContainsFallsBackToRawForUnparseableMessages covers the one message that
// has no bodies to search: Raw is all there is, and a match on it is better
// than a message the tool refuses to see into.
func TestContainsFallsBackToRawForUnparseableMessages(t *testing.T) {
	msg := message.Message{
		EnvelopeTo: []string{"john@example.test"},
		Raw:        []byte("Subject broken\r\nthis body never parsed but the code 483921 is in it\r\n"),
		ParseError: errors.New("parse mail: malformed header"),
	}

	if !(Criteria{Contains: "483921"}).Match(msg) {
		t.Fatal("Contains did not search Raw for a message that failed to parse")
	}
	if !(Criteria{To: "john@example.test"}).Match(msg) {
		t.Fatal("a message that failed to parse must still match on its envelope")
	}
}

// TestContainsDoesNotSearchRawOfAParsedMessage is the other half of the rule:
// header names and transfer encoding are in Raw, and a match on those would
// mean nothing to whoever wrote the assertion.
func TestContainsDoesNotSearchRawOfAParsedMessage(t *testing.T) {
	msg := sample()
	msg.Raw = []byte("Content-Transfer-Encoding: quoted-printable\r\n\r\nSGVsbG8=\r\n")

	if (Criteria{Contains: "quoted-printable"}).Match(msg) {
		t.Fatal("Contains searched Raw on a parsed message")
	}
}

// TestFailuresNameTheUnmetCriteria is what assert prints from, so it has to
// report every unmet criterion, not just the first, and carry what the message
// held.
func TestFailuresNameTheUnmetCriteria(t *testing.T) {
	c := Criteria{
		To:       "john@header.test",    // holds
		Subject:  "Welcome",             // does not
		Contains: "Verify your account", // does not
	}

	failures := c.Failures(sample())
	if len(failures) != 2 {
		t.Fatalf("Failures() reported %d criteria, want 2: %+v", len(failures), failures)
	}

	if failures[0].Field != "subject" {
		t.Errorf("first failure field = %q, want \"subject\"", failures[0].Field)
	}
	if failures[0].Want != "Welcome" {
		t.Errorf("first failure Want = %q, want \"Welcome\"", failures[0].Want)
	}
	if failures[0].Got != "Reset your password" {
		t.Errorf("first failure Got = %q, want the message's subject", failures[0].Got)
	}

	if failures[1].Field != "contains" {
		t.Errorf("second failure field = %q, want \"contains\"", failures[1].Field)
	}
	if failures[1].Got != "" {
		t.Errorf("a body failure reported Got = %q; the searched text is the whole message and is not quoted back", failures[1].Got)
	}
}

// TestFailuresIsEmptyForAMatch keeps the two answers in step: Match is
// Failures, counted, and a test that trusted one over the other would be
// trusting a coincidence.
func TestFailuresIsEmptyForAMatch(t *testing.T) {
	c := Criteria{To: "john@header.test", Subject: "password"}

	if failures := c.Failures(sample()); len(failures) != 0 {
		t.Fatalf("Failures() = %+v for a message that Match accepts", failures)
	}
}

// TestFailuresDoesNotMutateTheMessage guards the one place this package could
// write through a caller's slice: joining the two recipient lists.
func TestFailuresDoesNotMutateTheMessage(t *testing.T) {
	msg := sample()
	// Spare capacity is what an append into EnvelopeTo would scribble on.
	msg.EnvelopeTo = append(make([]string, 0, 4), "envelope-rcpt@example.test")
	msg.HeaderTo = []string{"john@header.test"}

	(Criteria{To: "nothing@matches.test"}).Failures(msg)

	if got := msg.EnvelopeTo[:cap(msg.EnvelopeTo)]; got[1] != "" {
		t.Fatalf("matching wrote %q into the caller's recipient slice", got[1])
	}
}
