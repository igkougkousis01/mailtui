package tui

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/igkougkousis01/mailtui/internal/message"
	"github.com/igkougkousis01/mailtui/internal/store"
)

const testAddr = "127.0.0.1:1025"

var receivedAt = time.Date(2026, 8, 29, 10, 0, 0, 0, time.UTC)

// newModel builds a model over a store already holding the named messages, in
// the order given, so "oldest first" in the argument list reads the same way
// arrivals do.
func newModel(t *testing.T, subjects ...string) (Model, *store.Store) {
	t.Helper()

	st := store.New()
	for _, s := range subjects {
		add(t, st, s)
	}
	return New(st, testAddr, nil), st
}

// add stores a well-formed message whose subject names it.
func add(t *testing.T, st *store.Store, subject string) string {
	t.Helper()

	raw := "From: " + strings.ReplaceAll(subject, " ", "-") + "@example.test\r\n" +
		"To: dev@example.test\r\n" +
		"Subject: " + subject + "\r\n" +
		"\r\n" +
		"body of " + subject + "\r\n"

	msg := message.Capture(
		message.Envelope{From: "envelope@example.test", To: []string{"rcpt@example.test"}},
		[]byte(raw),
		receivedAt,
	)
	if msg.ParseError != nil {
		t.Fatalf("Capture(%q): ParseError = %v, want nil", subject, msg.ParseError)
	}
	return st.Add(msg)
}

// addMalformed stores something the parser cannot read, which is the case the
// preview has to survive.
func addMalformed(t *testing.T, st *store.Store) string {
	t.Helper()

	msg := message.Capture(
		message.Envelope{From: "broken@example.test", To: []string{"dev@example.test"}},
		[]byte("this is not a mail message at all"),
		receivedAt,
	)
	if msg.ParseError == nil {
		t.Fatal("addMalformed: payload parsed cleanly, want a ParseError")
	}
	return st.Add(msg)
}

// key builds the key press for a single printable character.
func key(r rune) tea.KeyPressMsg {
	return tea.KeyPressMsg{Code: r, Text: string(r)}
}

// special builds the key press for a named key such as [tea.KeyDown].
func special(code rune) tea.KeyPressMsg {
	return tea.KeyPressMsg{Code: code}
}

// send applies one message and returns the model, which Update hands back as
// an interface.
func send(t *testing.T, m Model, msg tea.Msg) (Model, tea.Cmd) {
	t.Helper()

	next, cmd := m.Update(msg)
	got, ok := next.(Model)
	if !ok {
		t.Fatalf("Update returned %T, want tui.Model", next)
	}
	return got, cmd
}

// selectedSubject names the message under the cursor, which is what every
// navigation assertion is really about.
func selectedSubject(t *testing.T, m Model) string {
	t.Helper()

	msg, ok := m.selected()
	if !ok {
		t.Fatal("no message selected")
	}
	return msg.Subject
}

func TestNewEmptyStore(t *testing.T) {
	m, _ := newModel(t)

	if len(m.msgs) != 0 {
		t.Errorf("msgs = %d, want 0", len(m.msgs))
	}
	if _, ok := m.selected(); ok {
		t.Error("selected() reported a message in an empty inbox")
	}
}

// The empty state has to say what is happening and where, because it is the
// screen a developer stares at while wondering whether the port is right.
func TestEmptyStateMentionsAddress(t *testing.T) {
	m, _ := newModel(t)
	m.width, m.height = 100, 30

	out := m.render()
	for _, want := range []string{"No messages yet.", testAddr, "Waiting for mail"} {
		if !strings.Contains(out, want) {
			t.Errorf("empty state does not mention %q:\n%s", want, out)
		}
	}
}

func TestInitialSelectionIsNewest(t *testing.T) {
	m, _ := newModel(t, "oldest", "middle", "newest")

	if m.cursor != 0 {
		t.Errorf("cursor = %d, want 0", m.cursor)
	}
	if got := selectedSubject(t, m); got != "newest" {
		t.Errorf("selected = %q, want %q", got, "newest")
	}
}

func TestNavigation(t *testing.T) {
	// Stored oldest first, so the list reads newest, middle, oldest.
	base, _ := newModel(t, "oldest", "middle", "newest")

	tests := []struct {
		name string
		keys []tea.KeyPressMsg
		want string
	}{
		{"down moves to the older message", []tea.KeyPressMsg{key('j')}, "middle"},
		{"down arrow does the same", []tea.KeyPressMsg{special(tea.KeyDown)}, "middle"},
		{"twice reaches the oldest", []tea.KeyPressMsg{key('j'), key('j')}, "oldest"},
		{"stops at the end of the list", []tea.KeyPressMsg{key('j'), key('j'), key('j'), key('j')}, "oldest"},
		{"up returns", []tea.KeyPressMsg{key('j'), key('k')}, "newest"},
		{"up arrow does the same", []tea.KeyPressMsg{special(tea.KeyDown), special(tea.KeyUp)}, "newest"},
		{"stops at the top of the list", []tea.KeyPressMsg{key('k'), key('k')}, "newest"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := base
			for _, k := range tt.keys {
				m, _ = send(t, m, k)
			}
			if got := selectedSubject(t, m); got != tt.want {
				t.Errorf("selected = %q, want %q", got, tt.want)
			}
		})
	}
}

// Someone watching the newest message is watching the newest message, not one
// particular mail, so an arrival should carry them along.
func TestArrivalFollowsNewestWhenOnNewest(t *testing.T) {
	m, st := newModel(t, "first", "second")

	if got := selectedSubject(t, m); got != "second" {
		t.Fatalf("precondition: selected = %q, want %q", got, "second")
	}

	add(t, st, "third")
	m, _ = send(t, m, mailArrivedMsg{})

	if m.cursor != 0 {
		t.Errorf("cursor = %d, want 0", m.cursor)
	}
	if got := selectedSubject(t, m); got != "third" {
		t.Errorf("selected = %q, want %q", got, "third")
	}
}

// Having navigated away, the reader is reading something. New mail must not
// take it off the screen.
func TestArrivalKeepsSelectionWhenReadingOlder(t *testing.T) {
	m, st := newModel(t, "first", "second")

	m, _ = send(t, m, key('j'))
	if got := selectedSubject(t, m); got != "first" {
		t.Fatalf("precondition: selected = %q, want %q", got, "first")
	}

	add(t, st, "third")
	m, _ = send(t, m, mailArrivedMsg{})

	if got := selectedSubject(t, m); got != "first" {
		t.Errorf("selected = %q, want %q", got, "first")
	}
	// The list grew above the selection, so staying on the same message means
	// moving down an index.
	if m.cursor != 2 {
		t.Errorf("cursor = %d, want 2", m.cursor)
	}
	if len(m.msgs) != 3 {
		t.Errorf("msgs = %d, want 3", len(m.msgs))
	}
}

// The first arrival into an empty inbox has nothing to steal focus from.
func TestFirstArrivalSelectsIt(t *testing.T) {
	m, st := newModel(t)

	add(t, st, "hello")
	m, _ = send(t, m, mailArrivedMsg{})

	if got := selectedSubject(t, m); got != "hello" {
		t.Errorf("selected = %q, want %q", got, "hello")
	}
}

// Notifications are best effort, so a refresh has to pick up everything that
// arrived, not just the one that woke it.
func TestArrivalPicksUpEveryMessage(t *testing.T) {
	m, st := newModel(t, "first")

	add(t, st, "second")
	add(t, st, "third")
	m, _ = send(t, m, mailArrivedMsg{})

	if len(m.msgs) != 3 {
		t.Errorf("msgs = %d, want 3", len(m.msgs))
	}
	if got := selectedSubject(t, m); got != "third" {
		t.Errorf("selected = %q, want %q", got, "third")
	}
}

// Nothing in this milestone deletes a message, but the store can, and a stale
// cursor would index past the end of the snapshot.
func TestSelectionSurvivesTheMessageDisappearing(t *testing.T) {
	m, st := newModel(t, "first", "second", "third")

	m, _ = send(t, m, key('j'))
	selected := m.msgs[m.cursor].ID

	if !st.Delete(selected) {
		t.Fatalf("Delete(%q) = false, want true", selected)
	}
	m, _ = send(t, m, mailArrivedMsg{})

	if m.cursor < 0 || m.cursor >= len(m.msgs) {
		t.Fatalf("cursor = %d, out of range for %d messages", m.cursor, len(m.msgs))
	}
	if _, ok := m.selected(); !ok {
		t.Error("selected() reported nothing with messages still in the store")
	}
}

func TestQuitKeys(t *testing.T) {
	base, _ := newModel(t, "only")

	tests := []struct {
		name string
		key  tea.KeyPressMsg
	}{
		{"q", key('q')},
		{"ctrl+c", tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.key.String(); got != tt.name {
				t.Fatalf("test key renders as %q, want %q", got, tt.name)
			}

			_, cmd := send(t, base, tt.key)
			if cmd == nil {
				t.Fatal("no command returned, want tea.Quit")
			}
			if _, ok := cmd().(tea.QuitMsg); !ok {
				t.Errorf("command produced %T, want tea.QuitMsg", cmd())
			}
		})
	}
}

// An unbound key does nothing at all: no movement, no command.
func TestUnboundKeyIsIgnored(t *testing.T) {
	m, _ := newModel(t, "first", "second")

	next, cmd := send(t, m, key('x'))
	if cmd != nil {
		t.Errorf("command = %v, want nil", cmd())
	}
	if next.cursor != m.cursor {
		t.Errorf("cursor = %d, want %d", next.cursor, m.cursor)
	}
}

func TestResizeIsRecorded(t *testing.T) {
	m, _ := newModel(t, "first")

	m, _ = send(t, m, tea.WindowSizeMsg{Width: 120, Height: 40})
	if m.width != 120 || m.height != 40 {
		t.Errorf("size = %dx%d, want 120x40", m.width, m.height)
	}
}

// A live subscription is what the model actually runs on, so exercise the real
// path once: subscribe, store a message, and let the command deliver it.
func TestSubscriptionDeliversArrivals(t *testing.T) {
	st := store.New()
	events, unsubscribe := st.Subscribe()
	defer unsubscribe()

	m := New(st, testAddr, events)

	cmd := m.Init()
	if cmd == nil {
		t.Fatal("Init returned no command, want a wait on the subscription")
	}

	add(t, st, "live")

	if _, ok := cmd().(mailArrivedMsg); !ok {
		t.Fatalf("command produced %T, want mailArrivedMsg", cmd())
	}

	m, next := send(t, m, mailArrivedMsg{})
	if got := selectedSubject(t, m); got != "live" {
		t.Errorf("selected = %q, want %q", got, "live")
	}
	if next == nil {
		t.Error("no follow-up command, so the next arrival would never be seen")
	}
}

// Cancelling the subscription closes the channel; the waiting command has to
// end rather than spin or block forever.
func TestCancelledSubscriptionEndsTheWait(t *testing.T) {
	st := store.New()
	events, unsubscribe := st.Subscribe()

	cmd := waitForMail(events)
	unsubscribe()

	if _, ok := cmd().(subscriptionClosedMsg); !ok {
		t.Errorf("command produced %T, want subscriptionClosedMsg", cmd())
	}
}

// A model built without a subscription must not hand back a command that would
// block on a nil channel for the life of the process.
func TestNilSubscriptionYieldsNoCommand(t *testing.T) {
	m, _ := newModel(t)

	if cmd := m.Init(); cmd != nil {
		t.Errorf("Init returned a command with no subscription: %T", cmd())
	}
}
