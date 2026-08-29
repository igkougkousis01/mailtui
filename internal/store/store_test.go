package store

import (
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/igkougkousis01/mailtui/internal/message"
)

var receivedAt = time.Date(2026, 8, 29, 10, 0, 0, 0, time.UTC)

// add captures and stores a message whose subject names it, so a message can be
// identified in an assertion without depending on the ID it happens to get. It
// returns the assigned ID, which is the only handle the Store hands out.
func add(t *testing.T, s *Store, subject string) string {
	t.Helper()

	msg := capture(subject)
	if msg.ParseError != nil {
		t.Fatalf("Capture(%q): ParseError = %v, want nil", subject, msg.ParseError)
	}

	id := s.Add(msg)
	if id == "" {
		t.Fatalf("Add(%q) returned an empty ID", subject)
	}
	// Add reads its argument and nothing else: the caller's Message is not
	// stamped with the ID, or otherwise touched.
	if msg.ID != "" {
		t.Errorf("Add wrote ID %q back into the caller's message", msg.ID)
	}
	return id
}

// capture builds a well-formed message whose subject names it.
//
// It carries an attachment and a custom header so that every slice field on
// Message is non-empty, which is what the ownership tests need in order to
// prove anything about them. Nothing in it contains a literal X: the ownership
// tests scribble that letter over Raw and then check it is not there.
func capture(subject string) *message.Message {
	raw := "From: sender@example.test\r\n" +
		"To: recipient@example.test\r\n" +
		"Subject: " + subject + "\r\n" +
		"Trace-Id: " + subject + "\r\n" +
		"MIME-Version: 1.0\r\n" +
		"Content-Type: multipart/mixed; boundary=\"b\"\r\n" +
		"\r\n" +
		"--b\r\n" +
		"Content-Type: text/plain; charset=utf-8\r\n" +
		"\r\n" +
		"body\r\n" +
		"--b\r\n" +
		"Content-Type: text/csv; charset=utf-8\r\n" +
		"Content-Disposition: attachment; filename=\"data.csv\"\r\n" +
		"\r\n" +
		"a,b\r\n" +
		"--b--\r\n"

	return message.Capture(
		message.Envelope{From: "envelope@example.test", To: []string{"rcpt@example.test"}},
		[]byte(raw),
		receivedAt,
	)
}

// addQuiet stores a message without touching *testing.T, so it is safe to call
// from the goroutines in the concurrency test, where FailNow must not be used.
func addQuiet(s *Store) string {
	return s.Add(capture("concurrent"))
}

// subjects names the messages a listing contains, in order, which is what the
// ordering assertions are really about.
func subjects(msgs []message.Message) []string {
	out := make([]string, len(msgs))
	for i, m := range msgs {
		out[i] = m.Subject
	}
	return out
}

func TestAddStoresMessage(t *testing.T) {
	s := New()

	id := add(t, s, "first")

	list := s.List()
	if len(list) != 1 {
		t.Fatalf("List() has %d messages, want 1", len(list))
	}
	// The copy the store made carries everything it was handed.
	got := list[0]
	if got.ID != id {
		t.Errorf("List()[0].ID = %q, want %q", got.ID, id)
	}
	if got.Subject != "first" {
		t.Errorf("Subject = %q, want %q", got.Subject, "first")
	}
	if len(got.Raw) == 0 {
		t.Error("stored message has no raw bytes")
	}
	if strings.Join(got.EnvelopeTo, ",") != "rcpt@example.test" {
		t.Errorf("EnvelopeTo = %v, want [rcpt@example.test]", got.EnvelopeTo)
	}
}

func TestAddAssignsNonEmptyUniqueIDs(t *testing.T) {
	s := New()

	seen := make(map[string]bool)
	for i := 0; i < 50; i++ {
		id := add(t, s, "subject")
		if seen[id] {
			t.Fatalf("Add reused ID %q", id)
		}
		seen[id] = true
	}
}

// TestAddIDsAreUniqueAcrossStores is why the counter is package-level: two
// stores in one process must not both start handing out the same ID.
func TestAddIDsAreUniqueAcrossStores(t *testing.T) {
	first, second := New(), New()

	a := add(t, first, "a")
	b := add(t, second, "b")

	if a == b {
		t.Errorf("two stores issued the same ID %q", a)
	}
}

// TestAddKeepsIDStable pins the promise the TUI and CLI rely on: an ID names
// the same message for as long as it is stored.
func TestAddKeepsIDStable(t *testing.T) {
	s := New()

	id := add(t, s, "first")

	add(t, s, "second")
	add(t, s, "third")

	got, ok := s.Get(id)
	if !ok {
		t.Fatalf("Get(%q) after later adds: not found", id)
	}
	if got.Subject != "first" {
		t.Errorf("Get(%q).Subject = %q, want %q", id, got.Subject, "first")
	}
	if got.ID != id {
		t.Errorf("stored message ID changed to %q, want %q", got.ID, id)
	}
}

func TestListIsNewestFirst(t *testing.T) {
	s := New()

	add(t, s, "first")
	add(t, s, "second")
	add(t, s, "third")

	want := []string{"third", "second", "first"}
	got := subjects(s.List())
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("List() = %v, want %v", got, want)
	}
}

func TestListOfEmptyStore(t *testing.T) {
	if got := New().List(); len(got) != 0 {
		t.Errorf("List() on an empty store = %v, want empty", got)
	}
}

// TestListReturnsIndependentSlice is the reason List copies the slice: a
// consumer that sorts or truncates its listing must not be editing the store.
// Mutation of the messages themselves is covered in ownership_test.go.
func TestListReturnsIndependentSlice(t *testing.T) {
	s := New()

	add(t, s, "first")
	add(t, s, "second")

	list := s.List()
	list[0] = message.Message{Subject: "clobbered"}
	list = list[:1]
	_ = list

	got := subjects(s.List())
	want := []string{"second", "first"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("List() = %v after the previous listing was modified, want %v", got, want)
	}
}

func TestGet(t *testing.T) {
	s := New()

	first := add(t, s, "first")
	second := add(t, s, "second")

	for wantSubject, id := range map[string]string{"first": first, "second": second} {
		got, ok := s.Get(id)
		if !ok {
			t.Fatalf("Get(%q): not found", id)
		}
		if got.Subject != wantSubject {
			t.Errorf("Get(%q).Subject = %q, want %q", id, got.Subject, wantSubject)
		}
		if got.ID != id {
			t.Errorf("Get(%q).ID = %q, want %q", id, got.ID, id)
		}
	}

	// A miss returns the zero Message, not a half-filled one.
	if got, ok := s.Get("no-such-id"); ok || got.ID != "" || got.Raw != nil {
		t.Errorf("Get of an unknown ID = (%+v, %v), want (zero, false)", got, ok)
	}

}

func TestDelete(t *testing.T) {
	s := New()

	first := add(t, s, "first")
	second := add(t, s, "second")
	add(t, s, "third")

	if !s.Delete(second) {
		t.Fatalf("Delete(%q) = false, want true", second)
	}

	// Gone from both the listing and the lookup, and the survivors keep their
	// order.
	if _, ok := s.Get(second); ok {
		t.Errorf("Get(%q) still finds a deleted message", second)
	}
	want := []string{"third", "first"}
	if got := subjects(s.List()); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("List() = %v after delete, want %v", got, want)
	}

	// Deleting twice reports the second attempt honestly rather than panicking.
	if s.Delete(second) {
		t.Errorf("Delete(%q) = true on an already deleted message", second)
	}
	if s.Delete("no-such-id") {
		t.Error("Delete of an unknown ID = true, want false")
	}

	if _, ok := s.Get(first); !ok {
		t.Errorf("Get(%q): deleting one message lost another", first)
	}
}

func TestClear(t *testing.T) {
	s := New()

	first := add(t, s, "first")
	add(t, s, "second")

	s.Clear()

	if got := s.List(); len(got) != 0 {
		t.Errorf("List() = %v after Clear, want empty", got)
	}
	if _, ok := s.Get(first); ok {
		t.Errorf("Get(%q) still finds a message after Clear", first)
	}

	// The store stays usable, and a cleared ID is not reissued.
	next := add(t, s, "third")
	if next == first {
		t.Errorf("ID %q was reissued after Clear", next)
	}
	if got := subjects(s.List()); strings.Join(got, ",") != "third" {
		t.Errorf("List() = %v after adding to a cleared store, want [third]", got)
	}
}

// TestConcurrentAddAndList is the test that earns the mutex. Run under -race it
// covers writers and readers hitting the store at once, which is exactly what
// several SMTP connections plus a UI will do.
func TestConcurrentAddAndList(t *testing.T) {
	s := New()

	const writers, perWriter = 8, 50

	var wg sync.WaitGroup
	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < perWriter; j++ {
				addQuiet(s)
			}
		}()
	}
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < perWriter; j++ {
				for _, msg := range s.List() {
					_ = msg.ID
				}
				s.Get("1")
			}
		}()
	}
	wg.Wait()

	list := s.List()
	if len(list) != writers*perWriter {
		t.Fatalf("List() has %d messages, want %d", len(list), writers*perWriter)
	}

	// Every concurrent Add still produced a distinct ID and a working lookup.
	seen := make(map[string]bool, len(list))
	for _, msg := range list {
		if msg.ID == "" {
			t.Fatal("a concurrently added message has no ID")
		}
		if seen[msg.ID] {
			t.Fatalf("ID %q was issued twice", msg.ID)
		}
		seen[msg.ID] = true

		if _, ok := s.Get(msg.ID); !ok {
			t.Fatalf("Get(%q): a listed message is not indexed", msg.ID)
		}
	}
}
