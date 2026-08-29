// Package match decides whether a captured message is the one a script asked
// for.
//
// It is the shared vocabulary of the script-mode commands: wait selects with
// it, assert both selects and checks with it, and the extract commands select
// with it before reading the message they were given. There is one matching
// rule for all of them, so `--subject "Welcome"` means the same thing wherever
// it is typed.
//
// Deliberately not a query language. Every field is a case-insensitive
// substring test, the fields are ANDed, and an empty field is not a
// constraint. That is enough to name the one message a test just triggered,
// and small enough that its behaviour fits in a paragraph of --help.
package match

import (
	"slices"
	"strings"

	"github.com/igkougkousis01/mailtui/internal/message"
)

// Criteria is a set of substring tests over a message. The zero value matches
// everything, which is what makes "wait for any mail at all" the natural
// default rather than a special case.
//
// Addressing comes in three flavours because mailtui models the difference
// between the SMTP envelope and the message header, and a test that cares
// about that difference has to be able to say so. To and From are the ones to
// reach for: they match either side, because an application under test knows
// the address it sent to and rarely knows or cares which of the two carried
// it. The Envelope* and Header* fields pin the check to one side for the
// occasions when that is the point of the test.
type Criteria struct {
	// To matches if the substring is in any envelope recipient (RCPT TO) or
	// any address in the To header.
	To string
	// EnvelopeTo matches only against the RCPT TO recipients.
	EnvelopeTo string
	// HeaderTo matches only against the To header.
	HeaderTo string

	// From matches if the substring is in the envelope sender (MAIL FROM) or
	// in the From header.
	From string
	// EnvelopeFrom matches only against the MAIL FROM sender.
	EnvelopeFrom string
	// HeaderFrom matches only against the From header.
	HeaderFrom string

	// Subject matches against the decoded Subject header.
	Subject string

	// Contains matches against the subject and the message bodies — text and
	// HTML both, so an assertion does not fail merely because the application
	// sent HTML only. It does not search Raw: Raw holds the transfer encoding
	// and the header block, so a hit there could be a fragment of base64 that
	// means nothing to whoever wrote the assertion. The exception is a message
	// that failed to parse and therefore has no bodies at all, where Raw is
	// the only text there is.
	Contains string
}

// Failure is one criterion the message did not satisfy.
//
// It carries what was asked for and what the message had, because that is what
// assert has to print: "want X, got Y" is the difference between a report a
// developer can act on and one that just says no.
type Failure struct {
	// Field is the flag name the criterion came from, such as "subject".
	Field string
	// Want is the substring that was looked for.
	Want string
	// Got is what the message holds for that field, empty when the searched
	// text is a whole message body and too big to quote.
	Got string
}

// Empty reports whether c constrains anything.
func (c Criteria) Empty() bool { return c == Criteria{} }

// Match reports whether m satisfies every criterion.
func (c Criteria) Match(m message.Message) bool { return len(c.Failures(m)) == 0 }

// Fields returns the names of the criteria that are set, in flag order.
//
// It is what lets a caller talk about a criterion no message satisfied, which
// is not something Failures can say: a criterion that every message failed and
// one that was never asked for both look like silence there.
func (c Criteria) Fields() []string {
	// The zero Message is enough: which criteria are set is a property of c,
	// not of what they are compared against.
	set := c.checks(message.Message{})

	names := make([]string, len(set))
	for i, ch := range set {
		names[i] = ch.field
	}
	return names
}

// Failures returns the criteria m does not satisfy, in flag order. An empty
// result means the message matches; Match is this, counted.
//
// One implementation for both questions is the point: a report of why a
// message did not match can never disagree with the decision that it did not,
// because they are the same code.
func (c Criteria) Failures(m message.Message) []Failure {
	var failures []Failure
	for _, ch := range c.checks(m) {
		if contains(ch.got, ch.want) {
			continue
		}

		failure := Failure{Field: ch.field, Want: ch.want}
		if ch.showGot {
			failure.Got = ch.got
		}
		failures = append(failures, failure)
	}
	return failures
}

// check is one criterion that was set, paired with the text it is tested
// against.
type check struct {
	field string
	want  string
	got   string
	// showGot is false for a criterion whose text is the whole message, which
	// a failure report must not quote back into someone's terminal.
	showGot bool
}

// checks returns the criteria that are set, in flag order, each paired with
// what m has for it.
//
// It is the single list of what this package knows how to compare. Everything
// else here — matching, reporting, naming the criteria in play — reads it,
// so a new criterion is one entry rather than three.
func (c Criteria) checks(m message.Message) []check {
	envelopeTo := strings.Join(m.EnvelopeTo, ", ")
	headerTo := strings.Join(m.HeaderTo, ", ")
	// slices.Concat rather than append, which would be free to write into the
	// spare capacity of the caller's EnvelopeTo.
	anyTo := strings.Join(slices.Concat(m.EnvelopeTo, m.HeaderTo), ", ")
	anyFrom := strings.TrimSpace(m.EnvelopeFrom + " " + m.HeaderFrom)

	set := make([]check, 0, 8)
	add := func(field, want, got string, showGot bool) {
		if want == "" {
			return
		}
		set = append(set, check{field: field, want: want, got: got, showGot: showGot})
	}

	add("to", c.To, anyTo, true)
	add("envelope-to", c.EnvelopeTo, envelopeTo, true)
	add("header-to", c.HeaderTo, headerTo, true)
	add("from", c.From, anyFrom, true)
	add("envelope-from", c.EnvelopeFrom, m.EnvelopeFrom, true)
	add("header-from", c.HeaderFrom, m.HeaderFrom, true)
	add("subject", c.Subject, m.Subject, true)
	add("contains", c.Contains, searchableText(m), false)

	return set
}

// searchableText is what Contains looks through: the subject and both bodies,
// or the raw payload when there was nothing to parse them out of.
func searchableText(m message.Message) string {
	if m.TextBody == "" && m.HTMLBody == "" && m.ParseError != nil {
		return m.Subject + "\n" + string(m.Raw)
	}
	return m.Subject + "\n" + m.TextBody + "\n" + m.HTMLBody
}

// contains is the one comparison this package makes.
//
// Case-insensitive, everywhere, without an option to make it otherwise. Mail
// addresses vary in case for reasons nobody testing an application cares
// about, subjects get retyped into a test with different capitalisation, and a
// tool whose answer depends on which of those happened is a tool that produces
// confusing red builds. The cost is that a test cannot assert on casing; that
// has not been worth a flag.
func contains(got, want string) bool {
	return strings.Contains(strings.ToLower(got), strings.ToLower(want))
}
