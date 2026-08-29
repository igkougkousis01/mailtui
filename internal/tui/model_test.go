package tui

import (
	"fmt"
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

// --- inspection modes ----------------------------------------------------

// addRaw stores whatever bytes it is given, for the tests that care about the
// exact shape of a message rather than about it merely existing.
func addRaw(t *testing.T, st *store.Store, raw string) string {
	t.Helper()

	return st.Add(message.Capture(
		message.Envelope{From: "envelope@example.test", To: []string{"rcpt@example.test"}},
		[]byte(raw),
		receivedAt,
	))
}

// addLong stores a message whose body is long enough that no pane can show all
// of it, which is what makes scrolling observable.
func addLong(t *testing.T, st *store.Store, subject string, lines int) string {
	t.Helper()

	var b strings.Builder
	b.WriteString("From: long@example.test\r\nTo: dev@example.test\r\nSubject: " + subject + "\r\n\r\n")
	for i := range lines {
		fmt.Fprintf(&b, "line %03d of %s\r\n", i, subject)
	}
	return addRaw(t, st, b.String())
}

// sizedModel returns a model at a usable terminal size, with the inspection
// content already built, which is what every arriving frame would have.
func sizedModel(t *testing.T, m Model, w, h int) Model {
	t.Helper()

	m, _ = send(t, m, tea.WindowSizeMsg{Width: w, Height: h})
	return m
}

func TestModeKeysSwitchTheInspectionMode(t *testing.T) {
	base, _ := newModel(t, "only")

	if base.mode != modeBody {
		t.Fatalf("initial mode = %v, want Body: the body is what a mail client shows first", base.mode)
	}

	tests := []struct {
		key  rune
		want inspectMode
		name string
	}{
		{'h', modeHeaders, "Headers"},
		{'r', modeRaw, "Raw"},
		{'a', modeAttachments, "Attachments"},
		{'b', modeBody, "Body"},
	}

	m := base
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m, _ = send(t, m, key(tt.key))
			if m.mode != tt.want {
				t.Errorf("after %q, mode = %v, want %v", tt.key, m.mode, tt.want)
			}
			if got := m.mode.String(); got != tt.name {
				t.Errorf("mode names itself %q, want %q", got, tt.name)
			}
		})
	}
}

// The mode is a standing preference about how to read mail, not a property of
// one message, so moving through the inbox must not undo it.
func TestModeSurvivesSelectionAndArrival(t *testing.T) {
	m, st := newModel(t, "first", "second")
	m = sizedModel(t, m, 100, 30)

	m, _ = send(t, m, key('r'))
	m, _ = send(t, m, key('j'))
	if m.mode != modeRaw {
		t.Errorf("mode = %v after moving to another message, want Raw", m.mode)
	}

	add(t, st, "third")
	m, _ = send(t, m, mailArrivedMsg{})
	if m.mode != modeRaw {
		t.Errorf("mode = %v after an arrival, want Raw", m.mode)
	}
}

// --- focus and scrolling -------------------------------------------------

func TestTabSwitchesFocus(t *testing.T) {
	m, _ := newModel(t, "only")

	if m.focus != focusInbox {
		t.Fatalf("initial focus = %v, want the inbox", m.focus)
	}

	m, _ = send(t, m, special(tea.KeyTab))
	if m.focus != focusInspect {
		t.Errorf("focus = %v after tab, want the inspection pane", m.focus)
	}

	m, _ = send(t, m, special(tea.KeyTab))
	if m.focus != focusInbox {
		t.Errorf("focus = %v after a second tab, want the inbox again", m.focus)
	}
}

// j and k mean "move" in whichever pane is listening. With the inbox focused
// they must not scroll the message, and with the message focused they must not
// walk the inbox out from under the reader.
func TestFocusDecidesWhatMovementKeysMove(t *testing.T) {
	m, st := newModel(t)
	addLong(t, st, "long", 200)
	add(t, st, "newer")
	m, _ = send(t, m, mailArrivedMsg{})
	m = sizedModel(t, m, 100, 30)

	// Inbox focused: the cursor moves and the content stays where it is.
	m, _ = send(t, m, key('j'))
	if m.cursor != 1 {
		t.Fatalf("cursor = %d after j with the inbox focused, want 1", m.cursor)
	}
	if m.scroll != 0 {
		t.Errorf("scroll = %d, want 0: j moved the cursor, not the content", m.scroll)
	}

	// Message focused: the content moves and the cursor stays where it is.
	m, _ = send(t, m, special(tea.KeyTab))
	m, _ = send(t, m, key('j'))
	m, _ = send(t, m, key('j'))
	if m.cursor != 1 {
		t.Errorf("cursor = %d after j with the message focused, want it left alone at 1", m.cursor)
	}
	if m.scroll != 2 {
		t.Errorf("scroll = %d, want 2", m.scroll)
	}

	m, _ = send(t, m, key('k'))
	if m.scroll != 1 {
		t.Errorf("scroll = %d after k, want 1", m.scroll)
	}
}

// Scrolling stops at both ends. Past the top there is nothing, and past the
// bottom the pane would show blank rows below a message that has ended.
func TestScrollingStopsAtBothEnds(t *testing.T) {
	m, st := newModel(t)
	addLong(t, st, "long", 200)
	m, _ = send(t, m, mailArrivedMsg{})
	m = sizedModel(t, m, 100, 30)
	m, _ = send(t, m, special(tea.KeyTab))

	for range 5 {
		m, _ = send(t, m, key('k'))
	}
	if m.scroll != 0 {
		t.Errorf("scroll = %d after k at the top, want 0", m.scroll)
	}

	limit := m.maxScroll()
	if limit <= 0 {
		t.Fatalf("maxScroll = %d, so the fixture does not overflow the pane", limit)
	}
	for range limit + 20 {
		m, _ = send(t, m, key('j'))
	}
	if m.scroll != limit {
		t.Errorf("scroll = %d after running j past the end, want %d", m.scroll, limit)
	}

	// The last row of the content is on screen and no row past it is.
	_, rows := m.inspectSize()
	if got := len(m.content.window(m.scroll, rows)); got != rows {
		t.Errorf("the pane shows %d rows at the bottom, want %d full rows", got, rows)
	}
}

// A message short enough to fit does not scroll at all: j would otherwise
// slide the only screenful of text off the top.
func TestShortMessageDoesNotScroll(t *testing.T) {
	m, _ := newModel(t, "short")
	m = sizedModel(t, m, 100, 40)
	m, _ = send(t, m, special(tea.KeyTab))

	if got := m.maxScroll(); got != 0 {
		t.Fatalf("maxScroll = %d for a message that fits, want 0", got)
	}

	m, _ = send(t, m, key('j'))
	if m.scroll != 0 {
		t.Errorf("scroll = %d, want 0", m.scroll)
	}
}

// Reading position belongs to the message being read, so moving to another one
// starts at its top rather than partway down it.
func TestSwitchingMessageResetsScroll(t *testing.T) {
	m, st := newModel(t)
	addLong(t, st, "older", 200)
	addLong(t, st, "newer", 200)
	m, _ = send(t, m, mailArrivedMsg{})
	m = sizedModel(t, m, 100, 30)

	m, _ = send(t, m, special(tea.KeyTab))
	for range 10 {
		m, _ = send(t, m, key('j'))
	}
	if m.scroll == 0 {
		t.Fatal("precondition: scroll = 0, so nothing was scrolled")
	}

	m, _ = send(t, m, special(tea.KeyTab))
	m, _ = send(t, m, key('j'))

	if got := selectedSubject(t, m); got != "older" {
		t.Fatalf("selected = %q, want %q", got, "older")
	}
	if m.scroll != 0 {
		t.Errorf("scroll = %d on a newly selected message, want 0", m.scroll)
	}
}

// Each mode is a different amount of text about the same message, so an offset
// carried across from another one would land somewhere arbitrary.
func TestSwitchingModeResetsScroll(t *testing.T) {
	m, st := newModel(t)
	addLong(t, st, "long", 200)
	m, _ = send(t, m, mailArrivedMsg{})
	m = sizedModel(t, m, 100, 30)

	m, _ = send(t, m, special(tea.KeyTab))
	for range 10 {
		m, _ = send(t, m, key('j'))
	}
	if m.scroll == 0 {
		t.Fatal("precondition: scroll = 0, so nothing was scrolled")
	}

	m, _ = send(t, m, key('r'))
	if m.scroll != 0 {
		t.Errorf("scroll = %d after switching to Raw, want 0", m.scroll)
	}
}

// Resizing is not a change of position. Someone who has scrolled to the middle
// of a raw message and widens their terminal is still reading the middle of it.
func TestResizeKeepsScroll(t *testing.T) {
	m, st := newModel(t)
	addLong(t, st, "long", 500)
	m, _ = send(t, m, mailArrivedMsg{})
	m = sizedModel(t, m, 100, 30)

	m, _ = send(t, m, special(tea.KeyTab))
	for range 10 {
		m, _ = send(t, m, key('j'))
	}

	m = sizedModel(t, m, 140, 40)
	if m.scroll != 10 {
		t.Errorf("scroll = %d after a resize, want it left at 10", m.scroll)
	}
}

// Shrinking the terminal until the content fits has to pull the offset back,
// or the pane would be scrolled past the end of what it holds.
func TestScrollIsClampedWhenContentShrinks(t *testing.T) {
	m, st := newModel(t)
	addLong(t, st, "long", 30)
	m, _ = send(t, m, mailArrivedMsg{})
	m = sizedModel(t, m, 100, 20)

	m, _ = send(t, m, special(tea.KeyTab))
	for range 100 {
		m, _ = send(t, m, key('j'))
	}
	if m.scroll == 0 {
		t.Fatal("precondition: scroll = 0, so nothing was scrolled")
	}

	m = sizedModel(t, m, 100, 100)
	if m.scroll > m.maxScroll() {
		t.Errorf("scroll = %d, past the limit of %d for the new size", m.scroll, m.maxScroll())
	}
}

// New mail must not move the content of the message being read, any more than
// it moves the selection.
func TestArrivalDoesNotDisturbScroll(t *testing.T) {
	m, st := newModel(t)
	addLong(t, st, "older", 200)
	add(t, st, "newer")
	m, _ = send(t, m, mailArrivedMsg{})
	m = sizedModel(t, m, 100, 30)

	m, _ = send(t, m, key('j')) // onto "older"
	m, _ = send(t, m, special(tea.KeyTab))
	for range 7 {
		m, _ = send(t, m, key('j'))
	}
	if m.scroll != 7 {
		t.Fatalf("precondition: scroll = %d, want 7", m.scroll)
	}

	add(t, st, "newest")
	m, _ = send(t, m, mailArrivedMsg{})

	if got := selectedSubject(t, m); got != "older" {
		t.Fatalf("selected = %q, want %q", got, "older")
	}
	if m.scroll != 7 {
		t.Errorf("scroll = %d after an arrival, want it left at 7", m.scroll)
	}
}

// An empty inbox has nothing to scroll and no size to scroll it in. Pressing
// the movement keys anyway must not put the model into a state the renderer
// cannot draw.
func TestScrollingAnEmptyInboxIsHarmless(t *testing.T) {
	m, _ := newModel(t)
	m = sizedModel(t, m, 100, 30)

	m, _ = send(t, m, special(tea.KeyTab))
	for range 5 {
		m, _ = send(t, m, key('j'))
	}
	if m.scroll != 0 {
		t.Errorf("scroll = %d with no messages, want 0", m.scroll)
	}
	if out := m.render(); out == "" {
		t.Error("the empty inbox rendered nothing")
	}
}
