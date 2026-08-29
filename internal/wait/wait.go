// Package wait blocks until a captured message matches, or until the caller
// gives up.
//
// It is the one place that knows how to watch a store without missing
// anything, so wait, assert and the extract commands all get the same
// guarantee from the same code.
package wait

import (
	"context"

	"github.com/igkougkousis01/mailtui/internal/match"
	"github.com/igkougkousis01/mailtui/internal/message"
	"github.com/igkougkousis01/mailtui/internal/store"
)

// Message returns the first message in st that satisfies c, waiting for one to
// arrive if none has yet. It returns ctx.Err() if ctx ends first, so a caller
// can tell a timeout (DeadlineExceeded) from a Ctrl-C (Canceled).
//
// "First" is arrival order, not the store's newest-first listing: the message
// a script is waiting for is the next one its application sends, and if two
// match, the earlier one is the one it caused first. Messages already in the
// store when Message is called count, which is what makes it safe to call
// after the mail may already have landed.
//
// Nothing is left behind when it returns. It starts no goroutines, and the
// subscription it takes is cancelled on every path out.
func Message(ctx context.Context, st *store.Store, c match.Criteria) (message.Message, error) {
	// Subscribe before the first scan, not after it. The other order has a
	// hole in it: a message that arrives between the scan and the subscription
	// is in neither, and nothing would ever mention it again. Subscribing
	// first can only cause a notification about a message the scan has already
	// examined, and a re-examination costs nothing.
	events, unsubscribe := st.Subscribe()
	defer unsubscribe()

	// The IDs already examined. Matching is a pure function of a snapshot, so
	// re-checking is harmless; this only keeps a store that has accumulated
	// messages from being re-matched in full on every arrival.
	seen := make(map[string]struct{})

	for {
		if msg, ok := scan(st, c, seen); ok {
			return msg, nil
		}

		select {
		case <-events:
			// The ID is deliberately ignored. A store notification is best
			// effort — it is dropped rather than allowed to stall the SMTP
			// session that produced it — so acting on the ID alone would mean
			// missing a message under load. Treating the receive as "something
			// happened" and rescanning cannot: a drop only happens when the
			// buffer is full, a full buffer is a notification still to come,
			// and every notification leads back here to a scan of everything.
		case <-ctx.Done():
			// One last scan before giving up. A message can arrive a moment
			// before the deadline and leave both this channel and ctx.Done
			// ready at once, and select picks between ready cases at random —
			// which would turn mail that did arrive in time into a coin-flip
			// timeout.
			if msg, ok := scan(st, c, seen); ok {
				return msg, nil
			}
			return message.Message{}, ctx.Err()
		}
	}
}

// scan checks every message not yet examined, oldest first, and records what
// it looked at.
func scan(st *store.Store, c match.Criteria, seen map[string]struct{}) (message.Message, bool) {
	// List is newest-first and always complete, which is the property this
	// relies on; walking it backwards puts it back into arrival order.
	list := st.List()
	for i := len(list) - 1; i >= 0; i-- {
		msg := list[i]
		if _, done := seen[msg.ID]; done {
			continue
		}
		seen[msg.ID] = struct{}{}

		if c.Match(msg) {
			return msg, true
		}
	}
	return message.Message{}, false
}
