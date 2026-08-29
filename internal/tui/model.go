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

// inspectMode is which view of the selected message the right-hand pane shows.
//
// Four flat modes with a key each, rather than a menu: a developer looking at a
// captured mail wants to flip between the body and the raw bytes repeatedly and
// quickly, and anything that takes two keystrokes to do that is in the way.
type inspectMode int

const (
	modeBody inspectMode = iota
	modeHeaders
	modeRaw
	modeAttachments
)

// modes is every mode in the order the footer lists them, with the key that
// selects it. It is the one place the key map and the footer agree.
var modes = []struct {
	mode inspectMode
	key  string
	name string
}{
	{modeBody, "b", "Body"},
	{modeHeaders, "h", "Headers"},
	{modeRaw, "r", "Raw"},
	{modeAttachments, "a", "Attachments"},
}

// String names the mode for the pane title and the footer.
func (mode inspectMode) String() string {
	for _, m := range modes {
		if m.mode == mode {
			return m.name
		}
	}
	return "Body"
}

// focusArea is which pane the movement keys act on.
//
// The panes share j/k because those are the movement keys, and a mode where
// they mean different things in different places is easier to hold in the head
// than two sets of movement keys, one of which is always the wrong one. Tab
// says which pane is listening; the focused pane's border says it back.
type focusArea int

const (
	focusInbox focusArea = iota
	focusInspect
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

	// mode is the view of the selected message the inspection pane shows, and
	// focus is the pane j/k moves in.
	mode  inspectMode
	focus focusArea

	// scroll is how many rendered lines of the inspection pane are above its
	// first visible row.
	scroll int

	// content is what the inspection pane shows, and contentKey is what it was
	// prepared from. See syncContent.
	content    inspectContent
	contentKey contentKey

	width, height int
}

// contentKey is everything the inspection pane's lines depend on. When it is
// unchanged, lines rendered earlier still describe the screen.
type contentKey struct {
	id    string
	mode  inspectMode
	width int
}

// New returns a Model showing the messages already in st.
//
// events is the channel from st.Subscribe; the caller owns the subscription
// and must cancel it. A nil channel gives a model that never sees new mail,
// which is what a test that drives arrivals by hand wants.
func New(st *store.Store, smtpAddr string, events <-chan string) Model {
	m := Model{
		store:    st,
		smtpAddr: smtpAddr,
		events:   events,
		msgs:     st.List(),
	}
	m.syncContent()
	return m
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
//
// Everything that could change what the inspection pane shows goes through
// here, so syncContent is applied once, at the end, rather than remembered at
// each of the places that move the cursor or the mode.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	next, cmd := m.update(msg)
	next.syncContent()
	return next, cmd
}

func (m Model) update(msg tea.Msg) (Model, tea.Cmd) {
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

// handleKey applies the key map: pick a message, pick a view of it, move
// within it, and leave.
func (m Model) handleKey(msg tea.KeyPressMsg) (Model, tea.Cmd) {
	switch pressed := msg.String(); pressed {
	case "q", "ctrl+c":
		return m, tea.Quit

	case "tab":
		if m.focus == focusInbox {
			m.focus = focusInspect
		} else {
			m.focus = focusInbox
		}

	case "j", "down":
		m.move(1)
	case "k", "up":
		m.move(-1)

	default:
		for _, mode := range modes {
			if pressed == mode.key {
				m.mode = mode.mode
			}
		}
	}
	return m, nil
}

// move applies one step of j/k to whichever pane has focus. Both directions
// stop at their end rather than wrapping: wrapping in a mail list means an
// extra keypress lands you at the other end of the inbox.
func (m *Model) move(delta int) {
	if m.focus == focusInspect {
		m.scroll += delta
		m.clampScroll()
		return
	}

	m.cursor += delta
	m.clampCursor()
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
//
// The inspection mode is not touched either way: which view of a message
// someone is using is a standing preference, not something an arrival revises.
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

	// The message being read is gone. The current UI does not remove messages,
	// but the store can, and a cursor past the end would panic the preview
	// rather than merely look wrong.
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

// clampScroll keeps the offset inside the content: never above the first line,
// and never so far down that the pane is showing blank rows below the end.
func (m *Model) clampScroll() {
	if m.scroll > m.maxScroll() {
		m.scroll = m.maxScroll()
	}
	if m.scroll < 0 {
		m.scroll = 0
	}
}

// maxScroll is the largest offset that still fills the pane. It is zero when
// the content fits, which is what makes j a no-op on a short message.
func (m Model) maxScroll() int {
	_, rows := m.inspectSize()
	if over := m.content.len() - rows; over > 0 {
		return over
	}
	return 0
}

// syncContent rebuilds the inspection pane's lines when what they are made of
// has changed, and keeps the scroll offset inside them.
//
// The content is prepared here rather than in View because View runs on every
// frame, and neither wrapping a message nor indexing a raw payload is work to
// repeat sixty times a second. View falls back to preparing it itself when the
// key does not match, so a Model whose fields were set directly is never drawn
// stale — the cache is an optimisation, not a correctness requirement.
//
// It is also the one place scroll is reset. A different message and a different
// mode both mean "the key changed in a way that makes the old offset
// meaningless", and doing it here means no handler has to remember. A resize
// is deliberately not a reset: the reader has not moved, so neither should
// their position.
func (m *Model) syncContent() {
	key := m.currentContentKey()

	if key.id != m.contentKey.id || key.mode != m.contentKey.mode {
		m.scroll = 0
	}
	if key != m.contentKey {
		m.contentKey = key
		m.content = m.buildInspect(key.width)
	}

	m.clampScroll()
}

func (m Model) currentContentKey() contentKey {
	var id string
	if msg, ok := m.selected(); ok {
		id = msg.ID
	}
	w, _ := m.inspectSize()
	return contentKey{id: id, mode: m.mode, width: w}
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
