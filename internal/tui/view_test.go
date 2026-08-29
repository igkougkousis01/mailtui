package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

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

// --- inspection modes ----------------------------------------------------

// inMode renders m at the given size with the given inspection mode selected,
// reaching the mode through its key so the key map is exercised too.
func inMode(t *testing.T, m Model, mode inspectMode, w, h int) string {
	t.Helper()

	m, _ = send(t, m, tea.WindowSizeMsg{Width: w, Height: h})
	for _, spec := range modes {
		if spec.mode == mode {
			m, _ = send(t, m, key(rune(spec.key[0])))
		}
	}
	if m.mode != mode {
		t.Fatalf("mode = %v after pressing its key, want %v", m.mode, mode)
	}
	return m.render()
}

// modelOver builds a model over a store holding exactly the given payload.
func modelOver(t *testing.T, raw string) Model {
	t.Helper()

	st := store.New()
	addRaw(t, st, raw)
	return New(st, testAddr, nil)
}

// The mode has to be legible from the screen: someone looking at a wall of
// header lines should not have to remember which key they last pressed.
func TestActiveModeIsNamedOnScreen(t *testing.T) {
	m, _ := newModel(t, "only")

	for _, spec := range modes {
		t.Run(spec.name, func(t *testing.T) {
			out := inMode(t, m, spec.mode, 120, 30)

			// Once in the pane title, saying what the pane is showing.
			if want := "Message · " + spec.name; !strings.Contains(out, want) {
				t.Errorf("the pane is not titled %q:\n%s", want, out)
			}
			// And once in the footer, next to every other mode's key, which is
			// the only place the keys are written down.
			for _, other := range modes {
				if !strings.Contains(out, other.key+" "+other.name) {
					t.Errorf("the footer does not offer %q for %s:\n%s", other.key, other.name, out)
				}
			}
		})
	}
}

// The footer says what j and k are about to do, which changes with the focus.
func TestFooterNamesWhatMovementKeysDo(t *testing.T) {
	m, _ := newModel(t, "only")
	m, _ = send(t, m, tea.WindowSizeMsg{Width: 120, Height: 30})

	if out := m.render(); !strings.Contains(out, "j/k messages") {
		t.Errorf("footer does not say j/k moves through the inbox:\n%s", out)
	}

	m, _ = send(t, m, special(tea.KeyTab))
	if out := m.render(); !strings.Contains(out, "j/k scroll") {
		t.Errorf("footer does not say j/k scrolls the message:\n%s", out)
	}
}

// --- headers mode --------------------------------------------------------

const headerRichMessage = "Received: from client.test by catcher.test; Sat, 29 Aug 2026 09:58:00 +0000\r\n" +
	"Received: from app.test by client.test; Sat, 29 Aug 2026 09:57:00 +0000\r\n" +
	"Date: Sat, 29 Aug 2026 10:00:00 +0000\r\n" +
	"Message-ID: <trace-1@app.test>\r\n" +
	"MIME-Version: 1.0\r\n" +
	"From: ops@app.test\r\n" +
	"To: ivan@example.test\r\n" +
	"Subject: Delivery trace\r\n" +
	"X-Tag: alpha\r\n" +
	"X-Tag: beta\r\n" +
	"Content-Type: text/plain; charset=utf-8\r\n" +
	"Content-Transfer-Encoding: 7bit\r\n" +
	"\r\n" +
	"Look at the headers.\r\n"

// Headers mode exists so that the fields the structured preview leaves out are
// reachable, so those are exactly what it has to show.
func TestHeadersModeShowsTheWholeHeaderBlock(t *testing.T) {
	out := inMode(t, modelOver(t, headerRichMessage), modeHeaders, 120, 40)

	for _, want := range []string{
		"Received:", "catcher.test", "Date:", "Message-ID:", "<trace-1@app.test>",
		"MIME-Version:", "Content-Type:", "Content-Transfer-Encoding:", "7bit", "X-Tag:",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("Headers mode does not show %q:\n%s", want, out)
		}
	}

	// Both values of a repeated header, not just the one a map would have kept.
	for _, want := range []string{"alpha", "beta"} {
		if !strings.Contains(out, want) {
			t.Errorf("Headers mode dropped the %q value of X-Tag:\n%s", want, out)
		}
	}
	if !strings.Contains(out, "09:58:00") || !strings.Contains(out, "09:57:00") {
		t.Errorf("Headers mode dropped one of the two Received lines:\n%s", out)
	}
}

// The header block is a record of what was sent. The SMTP envelope was never
// part of it, and mixing the two would invent a header the sender never wrote.
func TestHeadersModeExcludesTheEnvelope(t *testing.T) {
	st := store.New()
	st.Add(message.Capture(
		message.Envelope{From: "envelope-only@smtp.test", To: []string{"rcpt-only@smtp.test"}},
		[]byte(headerRichMessage),
		receivedAt,
	))

	m := New(st, testAddr, nil)

	headers := inMode(t, m, modeHeaders, 120, 40)
	for _, unwanted := range []string{"envelope-only@smtp.test", "rcpt-only@smtp.test", "Envelope from"} {
		if strings.Contains(headers, unwanted) {
			t.Errorf("Headers mode shows envelope information (%q):\n%s", unwanted, headers)
		}
	}

	// It is still on screen, in the mode that is about the message rather than
	// about its header block.
	body := inMode(t, m, modeBody, 120, 40)
	for _, want := range []string{"envelope-only@smtp.test", "rcpt-only@smtp.test"} {
		if !strings.Contains(body, want) {
			t.Errorf("Body mode does not show %q:\n%s", want, body)
		}
	}
}

// A message with no readable header block must say so rather than draw an
// empty pane that looks like a bug.
func TestHeadersModeOfMalformedMessage(t *testing.T) {
	st := store.New()
	addMalformed(t, st)

	out := inMode(t, New(st, testAddr, nil), modeHeaders, 120, 30)

	assertExactly(t, out, 120, 30)
	if !strings.Contains(out, "No headers could be read") {
		t.Errorf("Headers mode does not explain the empty block:\n%s", out)
	}
}

// --- raw mode ------------------------------------------------------------

// Raw mode shows the bytes that arrived, not a re-rendering of what we parsed
// out of them. A quoted-printable body is the difference made visible: the
// preview decodes it and the raw view must not.
func TestRawModeShowsTheStoredBytes(t *testing.T) {
	const raw = "From: cafe@app.test\r\n" +
		"To: frank@example.test\r\n" +
		"Subject: Cafe\r\n" +
		"MIME-Version: 1.0\r\n" +
		"Content-Type: text/plain; charset=utf-8\r\n" +
		"Content-Transfer-Encoding: quoted-printable\r\n" +
		"\r\n" +
		"R=C3=A9servation confirm=C3=A9e.\r\n"

	m := modelOver(t, raw)

	if msg, _ := m.selected(); !strings.Contains(msg.TextBody, "Réservation") {
		t.Fatalf("precondition: TextBody = %q, want it decoded", msg.TextBody)
	}

	out := inMode(t, m, modeRaw, 120, 40)

	if !strings.Contains(out, "RAW") {
		t.Errorf("the raw view is not labelled RAW:\n%s", out)
	}
	// The encoded bytes, and the headers that describe them, exactly as sent.
	for _, want := range []string{"R=C3=A9servation", "Content-Transfer-Encoding: quoted-printable"} {
		if !strings.Contains(out, want) {
			t.Errorf("Raw mode does not show %q:\n%s", want, out)
		}
	}
	// And nothing regenerated: the decoded form belongs to the other view.
	if strings.Contains(out, "Réservation") {
		t.Errorf("Raw mode shows decoded text, so it is not showing the stored bytes:\n%s", out)
	}
}

// The message that cannot be parsed is the one raw inspection is for, so it is
// the case that has to work without any parsed structure to lean on.
func TestRawModeOfMalformedMessage(t *testing.T) {
	st := store.New()
	addMalformed(t, st)

	m := New(st, testAddr, nil)

	for _, size := range [][2]int{{120, 30}, {40, 20}, {24, 8}} {
		out := inMode(t, m, modeRaw, size[0], size[1])
		assertExactly(t, out, size[0], size[1])
	}

	out := inMode(t, m, modeRaw, 120, 30)
	if !strings.Contains(out, "RAW") {
		t.Errorf("the raw view is not labelled RAW:\n%s", out)
	}
	if want := "this is not a mail message at all"; !strings.Contains(out, want) {
		t.Errorf("Raw mode does not show the captured bytes %q:\n%s", want, out)
	}
}

// Scrolling is what makes a raw message inspectable at all: the pane shows a
// window onto it, and moving the window has to change what is in it.
func TestRawModeScrolls(t *testing.T) {
	m, st := newModel(t)
	addLong(t, st, "long", 200)
	m, _ = send(t, m, mailArrivedMsg{})
	m, _ = send(t, m, tea.WindowSizeMsg{Width: 120, Height: 30})
	m, _ = send(t, m, key('r'))
	m, _ = send(t, m, special(tea.KeyTab))

	top := m.render()
	if !strings.Contains(top, "line 000") {
		t.Fatalf("the first line of the message is not on screen:\n%s", top)
	}

	for range 40 {
		m, _ = send(t, m, key('j'))
	}
	scrolled := m.render()

	assertExactly(t, scrolled, 120, 30)
	if strings.Contains(scrolled, "line 000 ") {
		t.Errorf("the first line is still on screen after scrolling:\n%s", scrolled)
	}
	if !strings.Contains(scrolled, "line 034 of long") {
		t.Errorf("scrolling did not land where the offset says it did:\n%s", scrolled)
	}
	// The pane says where in the message the window is, which is the only clue
	// that there is more above and below.
	if !strings.Contains(scrolled, "/") {
		t.Errorf("the pane does not report the scroll position:\n%s", scrolled)
	}
}

// --- attachments mode ----------------------------------------------------

const attachmentMessage = "From: reports@app.test\r\n" +
	"To: hana@example.test\r\n" +
	"Subject: Two files\r\n" +
	"MIME-Version: 1.0\r\n" +
	"Content-Type: multipart/mixed; boundary=\"mix\"\r\n" +
	"\r\n" +
	"--mix\r\n" +
	"Content-Type: text/plain; charset=utf-8\r\n" +
	"\r\n" +
	"Both files are attached.\r\n" +
	"--mix\r\n" +
	"Content-Type: application/pdf\r\n" +
	"Content-Disposition: attachment; filename=\"report.pdf\"\r\n" +
	"\r\n" +
	"%PDF-1.4\r\n" +
	"--mix\r\n" +
	"Content-Type: image/png; name=\"logo.png\"\r\n" +
	"Content-Disposition: inline; filename=\"logo.png\"\r\n" +
	"Content-ID: <logo@app.test>\r\n" +
	"\r\n" +
	"pngbytes\r\n" +
	"--mix--\r\n"

func TestAttachmentsModeListsMetadata(t *testing.T) {
	out := inMode(t, modelOver(t, attachmentMessage), modeAttachments, 120, 40)

	if !strings.Contains(out, "2 attachments") {
		t.Errorf("the attachment count is missing:\n%s", out)
	}
	for _, want := range []string{
		"report.pdf", "application/pdf", "attachment",
		"logo.png", "image/png", "inline", "cid: logo@app.test",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the attachment list does not show %q:\n%s", want, out)
		}
	}
	// A size, so an attachment that was silently sent empty is visible as such.
	if !strings.Contains(out, "8 B") {
		t.Errorf("the attachment list does not show a size:\n%s", out)
	}
}

// One attachment is one attachment, not "1 attachments".
func TestAttachmentsModeCountsInSingular(t *testing.T) {
	const one = "From: a@example.test\r\n" +
		"To: b@example.test\r\n" +
		"Subject: One file\r\n" +
		"MIME-Version: 1.0\r\n" +
		"Content-Type: multipart/mixed; boundary=\"one\"\r\n" +
		"\r\n" +
		"--one\r\n" +
		"Content-Type: text/plain; charset=utf-8\r\n" +
		"\r\n" +
		"see attached\r\n" +
		"--one\r\n" +
		"Content-Type: text/csv\r\n" +
		"Content-Disposition: attachment; filename=\"data.csv\"\r\n" +
		"\r\n" +
		"a,b\r\n" +
		"--one--\r\n"

	out := inMode(t, modelOver(t, one), modeAttachments, 120, 40)

	if !strings.Contains(out, "1 attachment") {
		t.Fatalf("the attachment count is missing:\n%s", out)
	}
	if strings.Contains(out, "1 attachments") {
		t.Errorf("the count does not agree with its noun:\n%s", out)
	}
}

// Rendering is a read. Whatever the raw view has to do to make bytes safe for a
// terminal, the bytes it was given must come out of it unchanged.
func TestRawModeDoesNotAlterTheStoredMessage(t *testing.T) {
	const raw = "From: a@example.test\r\nTo: b@example.test\r\nSubject: keep me\r\n\r\nline\r\n\x00\x1b[31m\r\n"

	st := store.New()
	id := addRaw(t, st, raw)

	inMode(t, New(st, testAddr, nil), modeRaw, 100, 30)

	stored, ok := st.Get(id)
	if !ok {
		t.Fatalf("Get(%q): not found", id)
	}
	if string(stored.Raw) != raw {
		t.Errorf("Raw changed while being displayed\n got: %q\nwant: %q", stored.Raw, raw)
	}
}

func TestAttachmentsModeEmptyState(t *testing.T) {
	out := inMode(t, modelOver(t, headerRichMessage), modeAttachments, 120, 30)

	assertExactly(t, out, 120, 30)
	if !strings.Contains(out, "No attachments") {
		t.Errorf("a message with no attachments does not say so:\n%s", out)
	}
}

// A message that never parsed has no parts, which is not the same as having no
// attachments, and the view should not claim otherwise without explaining.
func TestAttachmentsModeOfMalformedMessage(t *testing.T) {
	st := store.New()
	addMalformed(t, st)

	out := inMode(t, New(st, testAddr, nil), modeAttachments, 120, 30)

	assertExactly(t, out, 120, 30)
	if !strings.Contains(out, "No attachments") {
		t.Errorf("the empty state is missing:\n%s", out)
	}
	if !strings.Contains(out, "could not be parsed") {
		t.Errorf("the view does not say why there are no parts:\n%s", out)
	}
}

// --- safety --------------------------------------------------------------

// Every mode renders untrusted bytes, and Raw renders the most of them. A
// captured message must not be able to move the cursor, clear the screen or
// ring the bell from inside any of them.
func TestControlSequencesAreContainedInEveryMode(t *testing.T) {
	const hostile = "From: a@example.test\r\n" +
		"To: b@example.test\r\n" +
		"Subject: hi \x1b[31mred\x1b[0m \x07\r\n" +
		"X-Evil: \x1b[2J\x1b[H wiped\r\n" +
		"Content-Type: text/plain; charset=utf-8\r\n" +
		"\r\n" +
		"body \x1b[2Jcleared\ttabbed \x1b]0;retitled\x07\r\n"

	m := modelOver(t, hostile)

	for _, spec := range modes {
		t.Run(spec.name, func(t *testing.T) {
			out := inMode(t, m, spec.mode, 100, 30)

			assertExactly(t, out, 100, 30)
			for _, escape := range []string{"\x1b[2J", "\x1b[H", "\x1b]0;", "\x07"} {
				if strings.Contains(out, escape) {
					t.Errorf("a control sequence (%q) from the message reached the frame:\n%q", escape, out)
				}
			}
		})
	}
}

// A single line longer than any terminal is wrapped, not left to run off the
// pane and take the borders with it.
func TestVeryLongLineDoesNotBreakTheLayout(t *testing.T) {
	raw := "From: a@example.test\r\nTo: b@example.test\r\nSubject: " +
		strings.Repeat("subject-", 400) + "\r\n" +
		"X-Long: " + strings.Repeat("header-", 400) + "\r\n" +
		"\r\n" +
		strings.Repeat("body-", 4000) + "\r\n"

	m := modelOver(t, raw)

	for _, spec := range modes {
		t.Run(spec.name, func(t *testing.T) {
			for _, size := range [][2]int{{80, 24}, {24, 8}, {200, 60}} {
				out := inMode(t, m, spec.mode, size[0], size[1])
				assertExactly(t, out, size[0], size[1])
			}
		})
	}
}

// Every mode has to survive every terminal size, including the ones only a
// dragged window corner produces.
func TestEveryModeRendersAtEverySize(t *testing.T) {
	st := store.New()
	addRaw(t, st, attachmentMessage)
	addRaw(t, st, headerRichMessage)
	addMalformed(t, st)

	m := New(st, testAddr, nil)

	sizes := [][2]int{{1, 1}, {10, 3}, {24, 8}, {40, 12}, {63, 20}, {64, 20}, {80, 24}, {200, 60}, {100, 9}, {30, 100}}

	for _, spec := range modes {
		for _, size := range sizes {
			t.Run(fmt.Sprintf("%s/%dx%d", spec.name, size[0], size[1]), func(t *testing.T) {
				out := inMode(t, m, spec.mode, size[0], size[1])
				assertExactly(t, out, size[0], size[1])
			})
		}
	}
}

// The inbox marks a message that carried something, so an attachment is
// findable without opening every entry.
func TestListFlagsMessageWithAttachments(t *testing.T) {
	st := store.New()
	addRaw(t, st, attachmentMessage)

	out := sized(New(st, testAddr, nil), 100, 30)
	if !strings.Contains(out, "> @ ") {
		t.Errorf("an entry with attachments is not flagged in the list:\n%s", out)
	}
}
