package store

import (
	"strings"
	"testing"

	"github.com/igkougkousis01/mailtui/internal/message"
)

// The tests in this file are the ones that would fail if the Store went back to
// handing out pointers into its own state. Each one takes something the Store
// gave it, or gave to the Store, writes through every mutable field, and then
// asks the Store what it holds. Shallow copying is not enough to pass them:
// Raw, EnvelopeTo and HeaderTo alias their backing arrays unless each is
// cloned, so these fail on a plain struct copy just as surely as on a pointer.

// scribble writes through every mutable field of msg. It is what a careless or
// merely curious consumer might do, and none of it may reach the Store.
func scribble(msg *message.Message) {
	msg.ID = "hijacked"
	msg.Subject = "mutated subject"
	msg.EnvelopeFrom = "mutated@example.test"

	for i := range msg.Raw {
		msg.Raw[i] = 'X'
	}
	for i := range msg.EnvelopeTo {
		msg.EnvelopeTo[i] = "mutated-envelope@example.test"
	}
	for i := range msg.HeaderTo {
		msg.HeaderTo[i] = "mutated-header@example.test"
	}
	for i := range msg.Headers {
		msg.Headers[i] = message.Header{Key: "X-Mutated", Value: "mutated"}
	}
	for i := range msg.Attachments {
		msg.Attachments[i] = message.Attachment{Filename: "mutated.bin"}
	}
}

// checkIntact asserts that a snapshot still says what was captured, with no
// trace of scribble.
func checkIntact(t *testing.T, context string, msg message.Message, wantID, wantSubject string) {
	t.Helper()

	if msg.ID != wantID {
		t.Errorf("%s: ID = %q, want %q", context, msg.ID, wantID)
	}
	if msg.Subject != wantSubject {
		t.Errorf("%s: Subject = %q, want %q", context, msg.Subject, wantSubject)
	}
	if want := "envelope@example.test"; msg.EnvelopeFrom != want {
		t.Errorf("%s: EnvelopeFrom = %q, want %q", context, msg.EnvelopeFrom, want)
	}
	if want := "rcpt@example.test"; strings.Join(msg.EnvelopeTo, ",") != want {
		t.Errorf("%s: EnvelopeTo = %v, want [%s]", context, msg.EnvelopeTo, want)
	}
	if want := "recipient@example.test"; strings.Join(msg.HeaderTo, ",") != want {
		t.Errorf("%s: HeaderTo = %v, want [%s]", context, msg.HeaderTo, want)
	}
	if strings.Contains(string(msg.Raw), "X") {
		t.Errorf("%s: Raw was overwritten: %q", context, msg.Raw)
	}
	if !strings.Contains(string(msg.Raw), "Subject: "+wantSubject) {
		t.Errorf("%s: Raw no longer contains the captured message: %q", context, msg.Raw)
	}

	// The two slice fields inspection added are as much the Store's own as
	// Raw is, and are the ones a new consumer is most likely to edit in place.
	if len(msg.Headers) == 0 {
		t.Errorf("%s: Headers is empty; the fixture no longer proves anything", context)
	}
	for _, h := range msg.Headers {
		if h.Key == "X-Mutated" {
			t.Errorf("%s: Headers was overwritten: %+v", context, msg.Headers)
			break
		}
	}
	if want := "Trace-Id: " + wantSubject; !strings.Contains(headerLine(msg), want) {
		t.Errorf("%s: Headers no longer contains %q: %+v", context, want, msg.Headers)
	}

	if len(msg.Attachments) != 1 {
		t.Errorf("%s: Attachments = %+v, want the one the fixture sends", context, msg.Attachments)
	} else if want := "data.csv"; msg.Attachments[0].Filename != want {
		t.Errorf("%s: Attachments[0].Filename = %q, want %q", context, msg.Attachments[0].Filename, want)
	}
}

// headerLine flattens the header block so a test can ask whether one field
// survived without caring where in the block it sits.
func headerLine(msg message.Message) string {
	var b strings.Builder
	for _, h := range msg.Headers {
		b.WriteString(h.Key)
		b.WriteString(": ")
		b.WriteString(h.Value)
		b.WriteString("\n")
	}
	return b.String()
}

// TestAddCopiesItsArgument covers the producer side. The SMTP session builds a
// Message and hands it over; whatever it does with its own copy afterwards, and
// whatever a future caller might reuse a Message for, must not reach the Store.
func TestAddCopiesItsArgument(t *testing.T) {
	s := New()

	msg := capture("original")
	id := s.Add(msg)

	scribble(msg)

	got, ok := s.Get(id)
	if !ok {
		t.Fatalf("Get(%q): not found", id)
	}
	checkIntact(t, "after mutating the message passed to Add", got, id, "original")
}

// TestGetReturnsAnIndependentSnapshot covers the consumer side: a snapshot is
// the caller's to edit, and editing it is not a way to write into the Store.
func TestGetReturnsAnIndependentSnapshot(t *testing.T) {
	s := New()

	id := add(t, s, "original")

	first, ok := s.Get(id)
	if !ok {
		t.Fatalf("Get(%q): not found", id)
	}
	scribble(&first)

	second, ok := s.Get(id)
	if !ok {
		t.Fatalf("Get(%q) after mutating an earlier snapshot: not found", id)
	}
	checkIntact(t, "after mutating a snapshot from Get", second, id, "original")

	// Two snapshots of the same message are independent of each other too, not
	// just of the Store.
	if strings.Contains(string(second.Raw), "X") {
		t.Error("two snapshots of one message share a Raw array")
	}
}

// TestListReturnsIndependentSnapshots is the same guarantee for the bulk read
// the TUI will use on every redraw.
func TestListReturnsIndependentSnapshots(t *testing.T) {
	s := New()

	id := add(t, s, "original")

	list := s.List()
	if len(list) != 1 {
		t.Fatalf("List() has %d messages, want 1", len(list))
	}
	scribble(&list[0])

	checkIntact(t, "after mutating a message from List", s.List()[0], id, "original")

	got, ok := s.Get(id)
	if !ok {
		t.Fatalf("Get(%q): not found", id)
	}
	checkIntact(t, "Get after mutating a message from List", got, id, "original")
}

// TestSubscriptionCarriesOnlyAnID pins the reason the channel changed shape: a
// subscriber gets a handle, not a pointer into the Store, and the snapshot it
// then fetches is as isolated as any other.
func TestSubscriptionCarriesOnlyAnID(t *testing.T) {
	s := New()

	ch, cancel := s.Subscribe()
	defer cancel()

	want := add(t, s, "original")

	id := recv(t, ch)
	if id != want {
		t.Fatalf("notified ID = %q, want %q", id, want)
	}

	msg, ok := s.Get(id)
	if !ok {
		t.Fatalf("Get(%q): a notified message is not in the store", id)
	}
	checkIntact(t, "snapshot fetched via a notification", msg, id, "original")

	scribble(&msg)

	again, ok := s.Get(id)
	if !ok {
		t.Fatalf("Get(%q) after mutating the notified snapshot: not found", id)
	}
	checkIntact(t, "after mutating a snapshot fetched via a notification", again, id, "original")
}

// TestCloneKeepsAbsentFieldsAbsent guards the detail that makes the copies
// faithful rather than merely safe: a nil slice must not come back as an empty
// one, or a consumer checking for absence would see the wrong answer.
func TestCloneKeepsAbsentFieldsAbsent(t *testing.T) {
	s := New()

	// A message with no To header and no recipients, so the fields that Clone
	// copies are nil going in.
	msg := message.Capture(
		message.Envelope{From: "envelope@example.test"},
		[]byte("From: sender@example.test\r\nSubject: no recipients\r\n\r\nbody\r\n"),
		receivedAt,
	)
	if msg.EnvelopeTo != nil || msg.HeaderTo != nil {
		t.Fatalf("fixture is not what the test needs: EnvelopeTo = %v, HeaderTo = %v", msg.EnvelopeTo, msg.HeaderTo)
	}

	got, ok := s.Get(s.Add(msg))
	if !ok {
		t.Fatal("Get: not found")
	}
	if got.EnvelopeTo != nil {
		t.Errorf("EnvelopeTo = %v, want nil", got.EnvelopeTo)
	}
	if got.HeaderTo != nil {
		t.Errorf("HeaderTo = %v, want nil", got.HeaderTo)
	}
}

// TestStoredMessageSurvivesParseError checks that the fields a malformed
// message does have are copied like any other, since that message is the one a
// developer most often wants to look at.
func TestStoredMessageSurvivesParseError(t *testing.T) {
	const malformed = "this is not a mail message\r\n"

	s := New()

	msg := message.Capture(
		message.Envelope{From: "envelope@example.test", To: []string{"rcpt@example.test"}},
		[]byte(malformed),
		receivedAt,
	)
	if msg.ParseError == nil {
		t.Fatal("fixture parsed cleanly; it is no longer malformed")
	}

	id := s.Add(msg)
	scribble(msg)

	got, ok := s.Get(id)
	if !ok {
		t.Fatalf("Get(%q): not found", id)
	}
	if got.ParseError == nil {
		t.Error("ParseError was lost in the copy")
	}
	if string(got.Raw) != malformed {
		t.Errorf("Raw = %q, want %q", got.Raw, malformed)
	}
	if want := "envelope@example.test"; got.EnvelopeFrom != want {
		t.Errorf("EnvelopeFrom = %q, want %q", got.EnvelopeFrom, want)
	}
	if want := "rcpt@example.test"; strings.Join(got.EnvelopeTo, ",") != want {
		t.Errorf("EnvelopeTo = %v, want [%s]", got.EnvelopeTo, want)
	}
}
