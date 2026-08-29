// Package tui renders the live inbox that mailtui puts on the terminal.
//
// It is a reader of the store and nothing more. It does not parse MIME, run
// SMTP sessions, or keep message state of its own: everything it draws comes
// from store.List, and it never writes back. That keeps the interesting logic
// — capture, parsing, ownership — testable without a terminal, and keeps this
// package free to be rewritten around a different widget set later.
package tui

import (
	tea "charm.land/bubbletea/v2"

	"github.com/igkougkousis01/mailtui/internal/message"
	"github.com/igkougkousis01/mailtui/internal/store"
)

// Model is the Bubble Tea model for the inbox screen.
//
// The zero value is not usable; call New.
type Model struct {
	store    *store.Store
	smtpAddr string

	// events is the store subscription. The model only ever reads from it as
	// a wake-up signal; the IDs it carries are discarded, because refreshing
	// from store.List is the only thing that stays correct when the store
	// drops a notification to keep an SMTP session moving.
	events <-chan string

	// msgs is the snapshot currently being drawn, newest first, exactly as
	// store.List orders it. It is replaced wholesale on every arrival rather
	// than appended to.
	msgs []message.Message

	// cursor indexes msgs. Zero means the newest message.
	cursor int

	width, height int
}

// New returns a Model showing the messages already in st.
//
// events is the channel from st.Subscribe; the caller owns the subscription
// and must cancel it. A nil channel gives a model that never sees new mail,
// which is what a test that drives arrivals by hand wants.
func New(st *store.Store, smtpAddr string, events <-chan string) Model {
	return Model{
		store:    st,
		smtpAddr: smtpAddr,
		events:   events,
		msgs:     st.List(),
	}
}

// mailArrivedMsg says the store gained a message. It carries no ID: see
// Model.events.
type mailArrivedMsg struct{}

// subscriptionClosedMsg says the subscription was cancelled, so no further
// arrivals will be reported. The UI keeps working on what it already has.
type subscriptionClosedMsg struct{}

// Init starts waiting for the first arrival.
func (m Model) Init() tea.Cmd {
	return waitForMail(m.events)
}

// Update handles input, resizes and arrivals.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil

	case tea.KeyPressMsg:
		return m.handleKey(msg)

	case mailArrivedMsg:
		m.refresh()
		// Re-arm immediately: a Cmd fires once, so the wait has to be handed
		// back for the next arrival.
		return m, waitForMail(m.events)

	case subscriptionClosedMsg:
		return m, nil
	}

	return m, nil
}

// handleKey applies the key map, which is deliberately tiny: move, and leave.
func (m Model) handleKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q", "ctrl+c":
		return m, tea.Quit
	case "j", "down":
		if m.cursor < len(m.msgs)-1 {
			m.cursor++
		}
	case "k", "up":
		if m.cursor > 0 {
			m.cursor--
		}
	}
	return m, nil
}

// refresh replaces the drawn snapshot with a fresh one and decides where the
// cursor lands.
//
// The rule is about intent. A cursor on the newest message is following the
// newest message — that is what someone watching mail land is doing — so it
// stays on whatever is newest, including mail that just arrived. A cursor
// anywhere else is a deliberate choice to read something older, and new mail
// must not yank the reader out of it, so the cursor moves to wherever the
// message it was on now sits.
func (m *Model) refresh() {
	following := m.cursor == 0

	var selected string
	if m.cursor >= 0 && m.cursor < len(m.msgs) {
		selected = m.msgs[m.cursor].ID
	}

	m.msgs = m.store.List()

	if following || selected == "" {
		m.cursor = 0
		return
	}

	for i := range m.msgs {
		if m.msgs[i].ID == selected {
			m.cursor = i
			return
		}
	}

	// The message being read is gone. Nothing removes messages in this
	// milestone, but the store can, and a cursor past the end would panic the
	// preview rather than merely look wrong.
	m.clampCursor()
}

func (m *Model) clampCursor() {
	if m.cursor >= len(m.msgs) {
		m.cursor = len(m.msgs) - 1
	}
	if m.cursor < 0 {
		m.cursor = 0
	}
}

// selected returns the message under the cursor. The second result is false
// when the inbox is empty.
func (m Model) selected() (message.Message, bool) {
	if m.cursor < 0 || m.cursor >= len(m.msgs) {
		return message.Message{}, false
	}
	return m.msgs[m.cursor], true
}

// waitForMail blocks in a Cmd goroutine until the store reports an arrival.
//
// Cancelling the subscription closes the channel, which unblocks this and ends
// the goroutine, so quitting leaves nothing running.
func waitForMail(events <-chan string) tea.Cmd {
	if events == nil {
		return nil
	}
	return func() tea.Msg {
		if _, ok := <-events; !ok {
			return subscriptionClosedMsg{}
		}
		return mailArrivedMsg{}
	}
}
