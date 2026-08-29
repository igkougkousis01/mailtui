package tui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/igkougkousis01/mailtui/internal/message"
	"github.com/igkougkousis01/mailtui/internal/store"
)

// sized renders the model at the given terminal size.
func sized(m Model, w, h int) string {
	m.width, m.height = w, h
	return m.render()
}

// assertExactly is the layout invariant the whole renderer rests on: whatever
// the terminal size and whatever the mail contains, the frame is exactly as
// many rows as the terminal has, and every row is exactly as wide. A frame
// that breaks it is a frame with wrapped rows, drifting borders, or a pane
// drawn at a negative width.
func assertExactly(t *testing.T, out string, w, h int) {
	t.Helper()

	lines := strings.Split(out, "\n")
	if len(lines) != h {
		t.Errorf("rendered %d rows, want %d", len(lines), h)
	}
	for i, line := range lines {
		if got := ansi.StringWidth(line); got != w {
			t.Errorf("row %d is %d cells wide, want %d: %q", i, got, w, line)
		}
	}
}

// A terminal can be dragged to any size, including sizes nothing was designed
// for. None of them may panic, and none may produce a misshapen frame.
func TestRenderAtEverySize(t *testing.T) {
	empty, _ := newModel(t)

	full, st := newModel(t, "first", "second", "third")
	addMalformed(t, st)
	full.msgs = st.List()

	models := map[string]Model{"empty": empty, "populated": full}

	sizes := []struct{ w, h int }{
		{0, 0},    // before the first WindowSizeMsg
		{1, 1},    // absurd, but reachable while dragging
		{10, 3},   // below every minimum
		{24, 8},   // exactly the minimum
		{40, 12},  // narrow: the stacked layout
		{63, 20},  // one cell below the two-pane threshold
		{64, 20},  // exactly at it
		{80, 24},  // the usual terminal
		{200, 60}, // wide
		{100, 9},  // wide but very short
		{30, 100}, // narrow but very tall
	}

	for name, m := range models {
		for _, size := range sizes {
			t.Run(fmt.Sprintf("%s/%dx%d", name, size.w, size.h), func(t *testing.T) {
				out := sized(m, size.w, size.h)
				if size.w <= 0 || size.h <= 0 {
					if out != "" {
						t.Errorf("%dx%d rendered %q, want empty", size.w, size.h, out)
					}
					return
				}
				assertExactly(t, out, size.w, size.h)
			})
		}
	}
}

// The layout switches shape rather than squeezing two unreadable panes side by
// side, and both shapes still show the selected message.
func TestNarrowTerminalStacksThePanes(t *testing.T) {
	m, _ := newModel(t, "only message")

	wide := sized(m, 90, 24)
	narrow := sized(m, 40, 24)

	for _, out := range []string{wide, narrow} {
		for _, want := range []string{"Inbox", "Message", "only message"} {
			if !strings.Contains(out, want) {
				t.Errorf("frame does not mention %q:\n%s", want, out)
			}
		}
	}

	// Side by side, both titles share a row; stacked, they cannot.
	if !hasRowWithAll(wide, "Inbox", "Message") {
		t.Error("wide layout does not put the two panes on the same row")
	}
	if hasRowWithAll(narrow, "Inbox", "Message") {
		t.Error("narrow layout still puts the two panes on the same row")
	}
}

func hasRowWithAll(out string, words ...string) bool {
	for _, line := range strings.Split(out, "\n") {
		all := true
		for _, w := range words {
			if !strings.Contains(line, w) {
				all = false
				break
			}
		}
		if all {
			return true
		}
	}
	return false
}

// The preview is the whole point of the second pane.
func TestPreviewShowsHeadersEnvelopeAndBody(t *testing.T) {
	st := store.New()
	st.Add(message.Capture(
		message.Envelope{From: "bounce@mailer.test", To: []string{"queue@relay.test"}},
		[]byte("From: auth@example.dev\r\n"+
			"To: john@example.test\r\n"+
			"Subject: Reset your password\r\n"+
			"\r\n"+
			"Your reset code is 483921.\r\n"),
		receivedAt,
	))

	out := sized(New(st, testAddr, nil), 120, 30)

	for _, want := range []string{
		"Reset your password",
		"auth@example.dev",
		"john@example.test",
		"bounce@mailer.test",
		"queue@relay.test",
		"Your reset code is 483921.",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("preview does not show %q:\n%s", want, out)
		}
	}
}

// An HTML-only message is acknowledged, not rendered.
func TestPreviewPlaceholderForHTMLOnlyMessage(t *testing.T) {
	st := store.New()
	st.Add(message.Capture(
		message.Envelope{From: "a@example.test", To: []string{"b@example.test"}},
		[]byte("From: a@example.test\r\n"+
			"To: b@example.test\r\n"+
			"Subject: Fancy\r\n"+
			"Content-Type: text/html; charset=utf-8\r\n"+
			"\r\n"+
			"<p>hello</p>\r\n"),
		receivedAt,
	))

	m := New(st, testAddr, nil)
	if msg, _ := m.selected(); msg.HTMLBody == "" || msg.TextBody != "" {
		t.Fatalf("precondition: text=%q html=%q, want html only", msg.TextBody, msg.HTMLBody)
	}

	out := sized(m, 120, 30)
	if !strings.Contains(out, "text preview unavailable") {
		t.Errorf("no HTML placeholder in the preview:\n%s", out)
	}
	if strings.Contains(out, "<p>") {
		t.Errorf("preview rendered raw markup:\n%s", out)
	}
}

// A message that could not be parsed is the one a developer most wants to look
// at, so the preview must survive it and say both what went wrong and that
// nothing was thrown away.
func TestPreviewOfMalformedMessage(t *testing.T) {
	st := store.New()
	addMalformed(t, st)

	m := New(st, testAddr, nil)

	for _, size := range [][2]int{{120, 30}, {40, 20}, {24, 8}} {
		out := sized(m, size[0], size[1])
		assertExactly(t, out, size[0], size[1])
	}

	out := sized(m, 120, 30)
	if !strings.Contains(out, "could not be parsed") {
		t.Errorf("no parse warning in the preview:\n%s", out)
	}
	if !strings.Contains(out, "retained") {
		t.Errorf("preview does not say the raw message was kept:\n%s", out)
	}
	// The envelope survives a parse failure and is all we know about where the
	// message went, so it has to be on screen.
	if !strings.Contains(out, "broken@example.test") {
		t.Errorf("preview does not show the envelope sender:\n%s", out)
	}
}

// The list flags a malformed message so it can be spotted without opening it.
func TestListFlagsMalformedMessage(t *testing.T) {
	st := store.New()
	add(t, st, "fine")
	addMalformed(t, st)

	out := sized(New(st, testAddr, nil), 100, 30)

	if !strings.Contains(out, "> ! ") {
		t.Errorf("malformed entry is not flagged in the list:\n%s", out)
	}
}

func TestNoSubjectFallback(t *testing.T) {
	st := store.New()
	st.Add(message.Capture(
		message.Envelope{From: "a@example.test", To: []string{"b@example.test"}},
		[]byte("From: a@example.test\r\nTo: b@example.test\r\n\r\nbody\r\n"),
		receivedAt,
	))

	out := sized(New(st, testAddr, nil), 100, 30)
	if !strings.Contains(out, "(no subject)") {
		t.Errorf("no subject fallback in the frame:\n%s", out)
	}
}

// Mail is whatever the developer's application emitted, which may include
// escape sequences. Drawing those verbatim would let a captured message move
// the cursor around the screen.
func TestControlSequencesInMailDoNotEscapeThePane(t *testing.T) {
	st := store.New()
	st.Add(message.Capture(
		message.Envelope{From: "a@example.test", To: []string{"b@example.test"}},
		[]byte("From: a@example.test\r\n"+
			"To: b@example.test\r\n"+
			"Subject: hi \x1b[31mred\x1b[0m \x07\r\n"+
			"\r\n"+
			"body \x1b[2Jcleared\ttabbed\r\n"),
		receivedAt,
	))

	out := sized(New(st, testAddr, nil), 100, 30)

	assertExactly(t, out, 100, 30)
	if strings.Contains(out, "\x1b[2J") || strings.Contains(out, "\x07") {
		t.Errorf("a control sequence from the message reached the frame:\n%q", out)
	}
}

// The cursor stays visible however far down the list it goes.
func TestListScrollsToKeepTheCursorVisible(t *testing.T) {
	st := store.New()
	for _, s := range []string{"one", "two", "three", "four", "five", "six", "seven", "eight"} {
		add(t, st, s)
	}

	m := New(st, testAddr, nil)
	m.width, m.height = 100, 16

	for i := range m.msgs {
		m.cursor = i
		out := m.render()
		assertExactly(t, out, 100, 16)
		if !strings.Contains(out, m.msgs[i].Subject) {
			t.Errorf("cursor at %d (%q) is off screen:\n%s", i, m.msgs[i].Subject, out)
		}
	}
}
