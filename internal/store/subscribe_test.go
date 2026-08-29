package store

import (
	"testing"
	"time"
)

// recvTimeout is generous: these tests assert that something arrives, and a
// loaded machine must not turn that into a flake.
const recvTimeout = 2 * time.Second

// recv waits for one notification, failing the test if none comes.
func recv(t *testing.T, ch <-chan string) string {
	t.Helper()

	select {
	case id, ok := <-ch:
		if !ok {
			t.Fatal("subscription channel closed while waiting for a notification")
		}
		return id
	case <-time.After(recvTimeout):
		t.Fatal("timed out waiting for a notification")
		return ""
	}
}

// TestSubscriberReceivesAddedMessageIDs covers the whole subscription contract
// from a consumer's side: the ID arrives, and it is a usable handle for
// fetching a snapshot of the message that caused it.
func TestSubscriberReceivesAddedMessageIDs(t *testing.T) {
	s := New()

	ch, cancel := s.Subscribe()
	defer cancel()

	// Only messages added after subscribing are announced; the backlog is what
	// List is for.
	want := add(t, s, "first")

	got := recv(t, ch)
	if got != want {
		t.Fatalf("notified ID = %q, want %q", got, want)
	}

	// The ID is the handle: the message is already in the store by the time
	// the notification arrives, so a consumer can fetch it straight away.
	msg, ok := s.Get(got)
	if !ok {
		t.Fatalf("Get(%q): a notified message is not in the store", got)
	}
	if msg.Subject != "first" {
		t.Errorf("Get(%q).Subject = %q, want %q", got, msg.Subject, "first")
	}
	if msg.ID != got {
		t.Errorf("Get(%q).ID = %q, want %q", got, msg.ID, got)
	}
}

func TestMultipleSubscribersEachReceive(t *testing.T) {
	s := New()

	const subscribers = 3
	chans := make([]<-chan string, subscribers)
	for i := range chans {
		ch, cancel := s.Subscribe()
		defer cancel()
		chans[i] = ch
	}

	want := add(t, s, "broadcast")

	for i, ch := range chans {
		if got := recv(t, ch); got != want {
			t.Errorf("subscriber %d was notified of %q, want %q", i, got, want)
		}
	}
}

// TestSlowSubscriberDoesNotBlockAdd is the requirement the buffer-and-drop
// design exists for: an SMTP session must never wait on a consumer that has
// stopped reading.
func TestSlowSubscriberDoesNotBlockAdd(t *testing.T) {
	s := New()

	// Subscribed and then never read from, which is the worst case: a TUI that
	// is busy redrawing, or a consumer that has wandered off.
	_, cancel := s.Subscribe()
	defer cancel()

	done := make(chan struct{})
	go func() {
		defer close(done)
		// Comfortably more than the buffer, so the drop path is what is being
		// exercised rather than the buffer's slack.
		for i := 0; i < subscriberBuffer*4; i++ {
			addQuiet(s)
		}
	}()

	select {
	case <-done:
	case <-time.After(recvTimeout):
		t.Fatal("Add blocked on a subscriber that never reads")
	}

	// Dropped notifications cost nothing but the notification: every message
	// is still stored, which is why a subscriber may always fall back to List.
	if got := len(s.List()); got != subscriberBuffer*4 {
		t.Errorf("List() has %d messages, want %d", got, subscriberBuffer*4)
	}
}

// TestCancelStopsNotificationsAndClosesChannel covers the documented lifecycle:
// cancelling is what ends a range over the channel, and it is the only thing
// that closes it.
func TestCancelStopsNotificationsAndClosesChannel(t *testing.T) {
	s := New()

	ch, cancel := s.Subscribe()
	add(t, s, "before cancel")
	recv(t, ch)

	cancel()

	// The channel is closed, so a consumer ranging over it terminates instead
	// of blocking forever.
	select {
	case _, ok := <-ch:
		if ok {
			t.Fatal("received a message after cancel")
		}
	case <-time.After(recvTimeout):
		t.Fatal("channel was not closed by cancel")
	}

	// Adding after cancel must not send on the closed channel, which would
	// panic and take the SMTP session down with it.
	add(t, s, "after cancel")

	// Cancelling twice is safe, so a consumer may defer it and also call it on
	// a shutdown path.
	cancel()
}

// TestCancelOneSubscriberLeavesOthers pins that unsubscribing is per
// subscription, not a teardown of the store's notifications.
func TestCancelOneSubscriberLeavesOthers(t *testing.T) {
	s := New()

	_, cancelFirst := s.Subscribe()
	second, cancelSecond := s.Subscribe()
	defer cancelSecond()

	cancelFirst()

	want := add(t, s, "still listening")
	if got := recv(t, second); got != want {
		t.Errorf("surviving subscriber was notified of %q, want %q", got, want)
	}
}

// TestSubscribeDuringAdd runs subscribe, cancel and Add against each other so
// -race can check the lock that lets Add send on a channel another goroutine
// may be closing.
func TestSubscribeDuringAdd(t *testing.T) {
	s := New()

	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 200; i++ {
			addQuiet(s)
		}
	}()

	for i := 0; i < 200; i++ {
		ch, cancel := s.Subscribe()
		select {
		case <-ch:
		default:
		}
		cancel()
	}

	<-done
}
