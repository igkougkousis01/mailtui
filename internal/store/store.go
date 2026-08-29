// Package store keeps captured messages in memory for the rest of mailtui to
// read.
//
// It is deliberately concrete: one process-local store, no persistence, no
// interface. mailtui is a development tool whose messages live and die with the
// process, so a map and a slice behind a mutex is the whole design.
//
// Ownership: the Store is the sole owner of the messages it holds. It copies
// what it is given and copies what it hands back, so nothing it stores is
// reachable, let alone writable, from outside. Add may be handed a Message the
// caller goes on using; List and Get return snapshots that a consumer is free
// to modify. Correctness does not depend on anyone agreeing to leave a shared
// pointer alone.
//
// The price is a copy of Raw per message per read, which for a developer's
// inbox is a rounding error next to the cost of getting aliasing wrong.
package store

import (
	"slices"
	"strconv"
	"sync"
	"sync/atomic"

	"github.com/igkougkousis01/mailtui/internal/message"
)

// subscriberBuffer is how many notifications a subscriber may fall behind by
// before it starts missing them. It is small on purpose: the channel is a
// wake-up signal, not a queue to be drained at leisure.
const subscriberBuffer = 16

// nextID is process-wide rather than per-Store so that two stores in the same
// process (as in tests) never hand out the same ID.
var nextID atomic.Uint64

// Store holds captured messages in memory and notifies subscribers as they
// arrive. The zero value is not usable; call New.
type Store struct {
	mu sync.RWMutex

	// msgs and byID point at the same store-owned Messages, which no code
	// outside this file ever sees. msgs is kept in arrival order and reversed
	// by List; the pointers are what let the slice and the index stay in step
	// without storing an index that Delete would invalidate.
	msgs []*message.Message
	byID map[string]*message.Message

	subs map[chan string]struct{}
}

// New returns an empty Store.
func New() *Store {
	return &Store{
		byID: make(map[string]*message.Message),
		subs: make(map[chan string]struct{}),
	}
}

// Add stores a copy of msg under a newly assigned ID, notifies subscribers,
// and returns the ID.
//
// msg is only read. The caller keeps it and may modify or discard it
// afterwards without affecting what was stored; the ID is returned rather than
// written back to msg, so Add leaves the caller's Message untouched.
//
// Add never blocks on a subscriber; see Subscribe.
func (s *Store) Add(msg *message.Message) string {
	id := strconv.FormatUint(nextID.Add(1), 10)

	// Cloned before the lock: the copy is the Store's, and doing it here keeps
	// the work off the critical section.
	stored := msg.Clone()
	stored.ID = id

	s.mu.Lock()
	defer s.mu.Unlock()

	s.msgs = append(s.msgs, &stored)
	s.byID[id] = &stored

	// Sent under the same lock that Subscribe and the unsubscribe function
	// take, so a channel can never be closed while this loop is sending on it.
	// The sends are non-blocking, so holding the lock here is cheap.
	for ch := range s.subs {
		select {
		case ch <- id:
		default:
			// The subscriber is behind. Dropping is the only option that keeps
			// SMTP handling responsive; see Subscribe for what a subscriber is
			// expected to do about it.
		}
	}

	return id
}

// List returns a snapshot of the stored messages, newest first.
//
// Newest-first is what every consumer wants: the TUI opens on the message that
// just arrived, and a CLI listing shows the latest mail without scrolling. It
// is done here rather than at each call site so there is one answer to the
// question.
//
// Both the slice and the messages in it are the caller's own. Reordering the
// slice, or writing to a message's Raw or recipients, changes nothing in the
// Store.
func (s *Store) List() []message.Message {
	s.mu.RLock()
	defer s.mu.RUnlock()

	list := make([]message.Message, len(s.msgs))
	for i, msg := range s.msgs {
		list[len(s.msgs)-1-i] = msg.Clone()
	}
	return list
}

// Get returns a snapshot of the message with the given ID. The second result
// reports whether it was found; when it is false the Message is the zero value.
//
// As with List, the returned Message belongs to the caller and may be modified
// freely.
func (s *Store) Get(id string) (message.Message, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	msg, ok := s.byID[id]
	if !ok {
		return message.Message{}, false
	}
	return msg.Clone(), true
}

// Delete removes the message with the given ID and reports whether it existed.
func (s *Store) Delete(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.byID[id]; !ok {
		return false
	}
	delete(s.byID, id)

	// slices.Delete rather than a hand-rolled append, so the vacated tail is
	// zeroed and the removed message is actually released.
	for i, msg := range s.msgs {
		if msg.ID == id {
			s.msgs = slices.Delete(s.msgs, i, i+1)
			break
		}
	}
	return true
}

// Clear removes every stored message. Subscribers are not notified: the
// notification channel reports arrivals, and a clear is always something the
// consumer just did itself.
func (s *Store) Clear() {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.msgs = nil
	s.byID = make(map[string]*message.Message)
}

// Subscribe returns a channel that receives the ID of each message as it is
// added, and a function that cancels the subscription.
//
// An ID rather than the message itself, for two reasons. The Store stays the
// only owner of its messages, so a subscription cannot become a way around the
// snapshot boundary that List and Get maintain. And a notification stays a few
// bytes whether the mail was a one-line receipt or a 20 MB one with a PDF
// attached, which matters when the buffer below holds several of them. A
// consumer that wants the message calls Get.
//
// Lifecycle: the caller owns the subscription and must call the returned
// function when it is done, which removes the subscription and closes the
// channel, ending any range over it. It is safe to call more than once, and it
// is the only thing that closes the channel. Nothing else needs cleaning up:
// the Store runs no goroutines, so an abandoned subscription leaks nothing but
// the buffer until it is cancelled.
//
// Delivery is best effort. If a subscriber does not keep up, notifications are
// dropped rather than allowed to stall the SMTP session that is producing them.
// A subscriber that must not miss anything should treat a receive as "something
// arrived" and call List, which is always complete.
//
// Any number of subscribers may exist; each gets its own channel and its own
// buffer.
func (s *Store) Subscribe() (<-chan string, func()) {
	ch := make(chan string, subscriberBuffer)

	s.mu.Lock()
	s.subs[ch] = struct{}{}
	s.mu.Unlock()

	var once sync.Once
	cancel := func() {
		once.Do(func() {
			s.mu.Lock()
			defer s.mu.Unlock()

			delete(s.subs, ch)
			close(ch)
		})
	}

	return ch, cancel
}
