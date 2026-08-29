package wait

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/igkougkousis01/mailtui/internal/match"
	"github.com/igkougkousis01/mailtui/internal/message"
	"github.com/igkougkousis01/mailtui/internal/store"
)

// waitTimeout is generous: these tests assert that something is found, and a
// loaded machine must not turn that into a flake.
const waitTimeout = 2 * time.Second

// msg builds a captured message addressed to john with the given subject,
// which is all the criteria under test look at.
func msg(subject string) *message.Message {
	return &message.Message{
		EnvelopeFrom: "app@example.test",
		EnvelopeTo:   []string{"john@example.test"},
		Subject:      subject,
		TextBody:     "body of " + subject,
	}
}

// waitFor runs Message with a deadline, so a waiter that never returns fails
// the test instead of hanging it.
func waitFor(t *testing.T, st *store.Store, c match.Criteria) (message.Message, error) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), waitTimeout)
	defer cancel()

	return Message(ctx, st, c)
}

func TestExistingMessageMatchesImmediately(t *testing.T) {
	st := store.New()
	st.Add(msg("Reset password"))

	got, err := waitFor(t, st, match.Criteria{Subject: "Reset"})
	if err != nil {
		t.Fatalf("Message: %v", err)
	}
	if got.Subject != "Reset password" {
		t.Fatalf("matched subject %q, want %q", got.Subject, "Reset password")
	}
}

// TestExistingMessageMatchesEvenPastTheDeadline pins the order of operations:
// the store is scanned before the context is consulted, so mail that is
// already there is never lost to a deadline that has already passed.
func TestExistingMessageMatchesEvenPastTheDeadline(t *testing.T) {
	st := store.New()
	st.Add(msg("Reset password"))

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	got, err := Message(ctx, st, match.Criteria{Subject: "Reset"})
	if err != nil {
		t.Fatalf("Message on a cancelled context with a matching message already stored: %v", err)
	}
	if got.Subject != "Reset password" {
		t.Fatalf("matched subject %q, want %q", got.Subject, "Reset password")
	}
}

func TestFutureMessageMatches(t *testing.T) {
	st := store.New()

	done := make(chan struct{})
	var got message.Message
	var err error
	go func() {
		defer close(done)
		got, err = waitFor(t, st, match.Criteria{Subject: "Welcome"})
	}()

	st.Add(msg("Welcome aboard"))

	<-done
	if err != nil {
		t.Fatalf("Message: %v", err)
	}
	if got.Subject != "Welcome aboard" {
		t.Fatalf("matched subject %q, want %q", got.Subject, "Welcome aboard")
	}
}

func TestTimeoutReportsDeadlineExceeded(t *testing.T) {
	st := store.New()
	st.Add(msg("Something else"))

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	_, err := Message(ctx, st, match.Criteria{Subject: "Welcome"})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Message returned %v, want context.DeadlineExceeded", err)
	}
}

// TestCancellationIsDistinguishableFromTimeout matters at the command line:
// Ctrl-C and a timeout are different exit codes, and the difference is carried
// by this error.
func TestCancellationIsDistinguishableFromTimeout(t *testing.T) {
	st := store.New()

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() {
		_, err := Message(ctx, st, match.Criteria{Subject: "Welcome"})
		done <- err
	}()

	cancel()

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Message returned %v, want context.Canceled", err)
		}
	case <-time.After(waitTimeout):
		t.Fatal("Message did not return after its context was cancelled")
	}
}

// TestNonMatchingArrivalsDoNotEndTheWait covers the ordinary case of a test
// script waiting for one email while an application sends several: the waiter
// wakes for each, rejects it, and keeps waiting.
func TestNonMatchingArrivalsDoNotEndTheWait(t *testing.T) {
	st := store.New()

	done := make(chan message.Message, 1)
	go func() {
		got, err := waitFor(t, st, match.Criteria{Subject: "Welcome"})
		if err != nil {
			t.Errorf("Message: %v", err)
		}
		done <- got
	}()

	for i := range 5 {
		st.Add(msg(fmt.Sprintf("Newsletter %d", i)))
		time.Sleep(time.Millisecond)
	}

	select {
	case got := <-done:
		t.Fatalf("Message returned %q before the message it was waiting for arrived", got.Subject)
	default:
	}

	st.Add(msg("Welcome aboard"))

	select {
	case got := <-done:
		if got.Subject != "Welcome aboard" {
			t.Fatalf("matched subject %q, want %q", got.Subject, "Welcome aboard")
		}
	case <-time.After(waitTimeout):
		t.Fatal("Message did not return after the matching message arrived")
	}
}

// TestEarliestMatchWins fixes which message a waiter returns when more than
// one qualifies: the one that arrived first, because that is the one the
// script's action caused first.
func TestEarliestMatchWins(t *testing.T) {
	st := store.New()
	st.Add(msg("Welcome first"))
	st.Add(msg("Welcome second"))

	got, err := waitFor(t, st, match.Criteria{Subject: "Welcome"})
	if err != nil {
		t.Fatalf("Message: %v", err)
	}
	if got.Subject != "Welcome first" {
		t.Fatalf("matched %q, want the earlier %q", got.Subject, "Welcome first")
	}
}

// TestNoArrivalIsMissedBetweenTheScanAndTheSubscription is the race the
// waiter is built around: a message that lands in the window between looking
// at the store and asking to be told about new ones would otherwise be
// announced to nobody and found by nobody.
//
// It is a race, so it is run many times and under -race; a waiter that
// subscribed after its first scan fails this quickly.
func TestNoArrivalIsMissedBetweenTheScanAndTheSubscription(t *testing.T) {
	for i := range 200 {
		st := store.New()

		// A message already present, so the initial scan has work to do and
		// the window between it and the subscription is a real one.
		st.Add(msg("Newsletter"))

		start := make(chan struct{})
		var wg sync.WaitGroup

		wg.Add(1)
		var got message.Message
		var err error
		go func() {
			defer wg.Done()
			<-start
			got, err = waitFor(t, st, match.Criteria{Subject: "Welcome"})
		}()

		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			st.Add(msg("Welcome aboard"))
		}()

		close(start)
		wg.Wait()

		if err != nil {
			t.Fatalf("run %d: Message: %v", i, err)
		}
		if got.Subject != "Welcome aboard" {
			t.Fatalf("run %d: matched %q, want %q", i, got.Subject, "Welcome aboard")
		}
	}
}

// TestABurstOfArrivalsDoesNotLoseTheMatch covers the other half of the
// subscription contract: notifications are dropped rather than allowed to
// stall the SMTP session producing them, so a burst larger than the store's
// buffer will drop some. The waiter rescans on every wake-up precisely so that
// a dropped notification costs nothing.
func TestABurstOfArrivalsDoesNotLoseTheMatch(t *testing.T) {
	st := store.New()

	done := make(chan message.Message, 1)
	go func() {
		got, err := waitFor(t, st, match.Criteria{Subject: "Welcome"})
		if err != nil {
			t.Errorf("Message: %v", err)
		}
		done <- got
	}()

	// Comfortably more than the store's per-subscriber buffer, sent as fast as
	// the store will take them.
	for i := range 200 {
		st.Add(msg(fmt.Sprintf("Newsletter %d", i)))
	}
	st.Add(msg("Welcome aboard"))

	select {
	case got := <-done:
		if got.Subject != "Welcome aboard" {
			t.Fatalf("matched %q, want %q", got.Subject, "Welcome aboard")
		}
	case <-time.After(waitTimeout):
		t.Fatal("the matching message was lost in a burst of arrivals")
	}
}
