package tui

import (
	"fmt"
	"strings"
	"unicode"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/igkougkousis01/mailtui/internal/message"
)

const (
	// minWidth and minHeight are the smallest terminal the two-line header,
	// footer and a bordered pane can occupy without overlapping. Below either,
	// the UI says so instead of drawing something broken.
	minWidth  = 24
	minHeight = 8

	// narrowWidth is where two side-by-side panes stop being readable and the
	// layout stacks them instead. Below it, a list pane wide enough to show a
	// subject leaves a preview pane too narrow to show a header line.
	narrowWidth = 64

	// listRowHeight is the lines one inbox entry occupies: subject, sender,
	// and a blank separator. compactRowHeight drops the sender and the
	// separator, for a stacked layout where every row is contested.
	listRowHeight    = 3
	compactRowHeight = 1

	// paneHeaderRows is what a pane spends on its own title and the blank
	// under it, above whatever it was given to show.
	paneHeaderRows = 2

	// labelWidth is the preview's label column, sized to the longest label
	// plus its colon and a space.
	labelWidth = len("Envelope from: ")

	// paneChrome is the horizontal cells a pane spends on itself: a border
	// column and a padding column on each side.
	paneChrome = 4
)

// Colours are ANSI palette indices rather than hex, so the terminal's own
// theme decides what they actually look like and the UI stays legible on a
// light background as well as a dark one.
var (
	// borderColour is shared by the pane borders and the rule inside the
	// preview, so the two never drift apart.
	borderColour = lipgloss.Color("8")

	styleAppName   = lipgloss.NewStyle().Bold(true)
	styleDim       = lipgloss.NewStyle().Foreground(borderColour)
	styleBorder    = lipgloss.NewStyle().Foreground(borderColour)
	stylePaneTitle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("4"))
	styleSelected  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("6"))
	styleOnline    = lipgloss.NewStyle().Foreground(lipgloss.Color("2"))
	styleWarn      = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("1"))

	// styleSelectedWarn is the cursor sitting on a message that failed to
	// parse: the warning colour wins, the cursor arrow carries the selection.
	styleSelectedWarn = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("9"))

	stylePlain = lipgloss.NewStyle()
)

// View draws the whole screen. Bubble Tea calls it after every Update, so it
// is a pure function of the model: it reads no clocks and touches no store.
func (m Model) View() tea.View {
	v := tea.NewView(m.render())
	v.AltScreen = true
	return v
}

func (m Model) render() string {
	w, h := m.width, m.height
	if w <= 0 || h <= 0 {
		// Before the first WindowSizeMsg there is nothing to lay out against.
		// Bubble Tea sends one immediately, so this frame is never seen.
		return ""
	}
	if w < minWidth || h < minHeight {
		return strings.Join(fitBlock([]string{"mailtui", "terminal too small"}, w, h), "\n")
	}

	header := m.renderHeader(w)
	footer := m.renderFooter(w)
	body := m.renderBody(w, h-2)

	return header + "\n" + body + "\n" + footer
}

// renderHeader is the title bar: who we are on the left, where we are
// listening on the right. The right side is dropped rather than wrapped when
// the terminal is too narrow for both.
func (m Model) renderHeader(w int) string {
	left := styleAppName.Render("mailtui")

	status := fmt.Sprintf("SMTP %s ", m.smtpAddr)
	if n := len(m.msgs); n == 1 {
		status = "1 message  " + status
	} else if n > 1 {
		status = fmt.Sprintf("%d messages  ", n) + status
	}
	right := styleDim.Render(status) + styleOnline.Render("●")

	gap := w - ansi.StringWidth(left) - ansi.StringWidth(right)
	if gap < 2 {
		return fitLine(left, w)
	}
	return left + strings.Repeat(" ", gap) + right
}

func (m Model) renderFooter(w int) string {
	return styleDim.Render(fitLine("↑/↓ · j/k  navigate     q  quit", w))
}

// renderBody draws the panes into exactly w by h cells.
func (m Model) renderBody(w, h int) string {
	if len(m.msgs) == 0 {
		// One full-width pane rather than two: an empty inbox has nothing to
		// put on either side of a divider.
		return pane("Inbox", m.emptyStateLines(), w, h)
	}

	if w < narrowWidth {
		return m.renderStacked(w, h)
	}

	listW := listWidth(w)
	list := pane("Inbox", m.listLines(listW-paneChrome, paneRows(h), listRowHeight), listW, h)
	preview := pane("Message", m.previewLines(w-listW-paneChrome), w-listW, h)

	return lipgloss.JoinHorizontal(lipgloss.Top, list, preview)
}

// renderStacked is the narrow-terminal layout: the same two panes, one above
// the other.
//
// Entries lose their sender line here. Vertical space is what is scarce in
// this layout, and the sender of the selected message is on screen anyway, two
// panes down — so spending a row per entry to repeat it costs more than it
// gives.
func (m Model) renderStacked(w, h int) string {
	listH := h/3 + paneHeaderRows
	if listH < 5 {
		listH = 5
	}
	if listH > 10 {
		listH = 10
	}

	previewH := h - listH
	if previewH < 5 {
		// Not enough room for two boxes. The message is the one worth keeping:
		// the list is navigation, and navigation still works blind.
		return pane("Message", m.previewLines(w-paneChrome), w, h)
	}

	list := pane("Inbox", m.listLines(w-paneChrome, paneRows(listH), compactRowHeight), w, listH)
	preview := pane("Message", m.previewLines(w-paneChrome), w, previewH)

	return lipgloss.JoinVertical(lipgloss.Left, list, preview)
}

// paneRows is how many rows of content a pane of total height h can show,
// after its borders and its own title.
func paneRows(h int) int {
	return h - 2 - paneHeaderRows
}

// listWidth splits the terminal between the two panes. The list gets a little
// under half, clamped so it neither squeezes subjects to nothing on a small
// terminal nor sprawls across a very wide one.
func listWidth(total int) int {
	w := total * 2 / 5
	if w < 26 {
		w = 26
	}
	if w > 44 {
		w = 44
	}
	if limit := total - 30; w > limit {
		w = limit
	}
	return w
}

// emptyStateLines is what the inbox says before any mail arrives: what it is
// waiting for, and where.
func (m Model) emptyStateLines() []string {
	return []string{
		"",
		styleAppName.Render("No messages yet."),
		"",
		styleDim.Render("SMTP listening on " + m.smtpAddr),
		styleDim.Render("Waiting for mail..."),
	}
}

// listLines renders the inbox entries, newest first, as a window around the
// cursor. Each entry is a subject line and a dimmed sender line; a message
// that failed to parse is flagged in the gutter so it can be picked out
// without opening it.
func (m Model) listLines(w, h, rowHeight int) []string {
	start, end := m.listWindow(h, rowHeight)

	lines := make([]string, 0, (end-start)*rowHeight)
	for i := start; i < end; i++ {
		msg := m.msgs[i]

		cursor := "  "
		if i == m.cursor {
			cursor = "> "
		}

		flag := "  "
		if msg.ParseError != nil {
			flag = "! "
		}

		subject := fitLine(cursor+flag+sanitizeLine(subjectOf(msg)), w)
		sender := fitLine(strings.Repeat(" ", 4)+sanitizeLine(senderOf(msg)), w)

		// One style for the whole row: nesting two of them would leave the
		// inner reset ending the outer colour halfway along the line.
		style := stylePlain
		switch {
		case i == m.cursor && msg.ParseError != nil:
			style = styleSelectedWarn
		case i == m.cursor:
			style = styleSelected
		case msg.ParseError != nil:
			style = styleWarn
		}

		lines = append(lines, style.Render(subject))
		if rowHeight >= listRowHeight {
			lines = append(lines, styleDim.Render(sender), "")
		}
	}
	return lines
}

// listWindow returns the half-open range of entries to draw so that the cursor
// is always on screen.
func (m Model) listWindow(h, rowHeight int) (start, end int) {
	visible := h / rowHeight
	if visible < 1 {
		visible = 1
	}

	start = 0
	if m.cursor >= visible {
		start = m.cursor - visible + 1
	}

	end = start + visible
	if end > len(m.msgs) {
		end = len(m.msgs)
	}
	return start, end
}

// previewLines renders the selected message: the parse warning if there is
// one, then the headers, the envelope, and the body.
func (m Model) previewLines(w int) []string {
	msg, ok := m.selected()
	if !ok {
		return m.emptyStateLines()
	}

	var lines []string

	if msg.ParseError != nil {
		// First, and loud: everything below it is partial, and knowing that
		// changes how it should be read.
		lines = append(lines, styleWarn.Render(fitLine("! This message could not be parsed.", w)))
		for _, l := range wrapLines(sanitizeLine(msg.ParseError.Error()), w) {
			lines = append(lines, styleWarn.Render(l))
		}
		for _, l := range wrapLines("The raw bytes were captured in full and are retained.", w) {
			lines = append(lines, styleDim.Render(l))
		}
		lines = append(lines, "")
	}

	lines = append(lines, fieldLines("Subject", subjectOf(msg), w)...)
	lines = append(lines, fieldLines("From", msg.HeaderFrom, w)...)
	lines = append(lines, fieldLines("To", strings.Join(msg.HeaderTo, ", "), w)...)
	lines = append(lines, fieldLines("Envelope from", msg.EnvelopeFrom, w)...)
	lines = append(lines, fieldLines("Envelope to", strings.Join(msg.EnvelopeTo, ", "), w)...)
	lines = append(lines, "", styleBorder.Render(strings.Repeat("─", max(w, 0))), "")
	lines = append(lines, bodyLines(msg, w)...)

	return lines
}

// fieldLines renders one labelled header field, wrapping a long value under a
// hanging indent. On a pane too narrow for a label column the value goes on
// its own lines instead, which is ugly but still readable.
func fieldLines(label, value string, w int) []string {
	if strings.TrimSpace(value) == "" {
		value = "(none)"
	}
	value = sanitizeLine(value)

	head := styleDim.Render(fitLine(label+":", labelWidth))

	valueW := w - labelWidth
	if valueW < 8 {
		return append([]string{head}, wrapLines(value, w)...)
	}

	wrapped := wrapLines(value, valueW)
	lines := make([]string, 0, len(wrapped))
	for i, l := range wrapped {
		if i == 0 {
			lines = append(lines, head+l)
			continue
		}
		lines = append(lines, strings.Repeat(" ", labelWidth)+l)
	}
	return lines
}

// bodyLines renders the text body, or says why there isn't one. HTML is
// acknowledged rather than rendered: turning markup into terminal text is a
// job of its own and not this milestone's.
func bodyLines(msg message.Message, w int) []string {
	text := strings.TrimSpace(sanitizeText(msg.TextBody))
	if text == "" {
		switch {
		case strings.TrimSpace(msg.HTMLBody) != "":
			return wrapLines(styleDim.Render("[HTML message — text preview unavailable]"), w)
		case msg.ParseError != nil:
			return wrapLines(styleDim.Render("(no body could be read)"), w)
		default:
			return wrapLines(styleDim.Render("(no body)"), w)
		}
	}

	var lines []string
	for _, l := range strings.Split(text, "\n") {
		lines = append(lines, wrapLines(l, w)...)
	}
	return lines
}

// subjectOf is the subject a human should see. A message with no Subject
// header is normal enough — a bare test mail has none — that it gets a
// placeholder rather than an empty row.
func subjectOf(msg message.Message) string {
	if s := strings.TrimSpace(msg.Subject); s != "" {
		return s
	}
	return "(no subject)"
}

// senderOf prefers the From header, because that is what the developer wrote
// in their application, and falls back to the envelope sender when the header
// is missing or unparsable.
func senderOf(msg message.Message) string {
	if s := strings.TrimSpace(msg.HeaderFrom); s != "" {
		return s
	}
	if s := strings.TrimSpace(msg.EnvelopeFrom); s != "" {
		return s
	}
	return "(unknown sender)"
}

// pane draws content inside a bordered box occupying exactly w by h cells,
// with the title on its first inner row. Anything too small to hold a border
// and a row of content renders as nothing rather than as garbage.
func pane(title string, content []string, w, h int) string {
	inner := w - paneChrome
	rows := h - 2
	if inner < 1 || rows < 1 {
		return ""
	}

	lines := make([]string, 0, rows)
	lines = append(lines, stylePaneTitle.Render(fitLine(title, inner)))
	if rows > 1 {
		lines = append(lines, "")
	}
	lines = append(lines, content...)

	body := strings.Join(fitBlock(lines, inner, rows), "\n")

	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(borderColour).
		Padding(0, 1).
		Render(body)
}

// fitBlock forces lines into exactly h rows of exactly w cells, padding with
// blanks and marking a truncation so a body that runs off the pane does not
// look like the whole message.
func fitBlock(lines []string, w, h int) []string {
	out := make([]string, 0, h)
	blank := strings.Repeat(" ", max(w, 0))

	for i := 0; i < h; i++ {
		switch {
		case i >= len(lines):
			out = append(out, blank)
		case i == h-1 && len(lines) > h:
			out = append(out, styleDim.Render(fitLine("… continues", w)))
		default:
			out = append(out, fitLine(lines[i], w))
		}
	}
	return out
}

// fitLine trims s to w cells, marking a trim with an ellipsis, and pads it
// back out to exactly w so the pane borders line up whatever is in the mail.
//
// It measures display width rather than bytes or runes, so a CJK subject or an
// emoji does not push the right-hand border out, and it is ANSI-aware, so a
// line that has already been styled survives it.
func fitLine(s string, w int) string {
	if w <= 0 {
		return ""
	}
	s = ansi.Truncate(s, w, "…")
	if pad := w - ansi.StringWidth(s); pad > 0 {
		s += strings.Repeat(" ", pad)
	}
	return s
}

// wrapLines word-wraps s to w cells, breaking inside a word only when the word
// itself is too long to fit.
func wrapLines(s string, w int) []string {
	if w <= 0 {
		return nil
	}
	if s == "" {
		return []string{""}
	}
	return strings.Split(ansi.Wrap(s, w, "-"), "\n")
}

// sanitizeLine makes a header value safe to draw on one row. Mail arrives from
// whatever the developer's application emitted, so it may contain tabs, stray
// carriage returns or escape sequences; rendering those verbatim would let a
// captured message move the cursor around the screen.
func sanitizeLine(s string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r == '\t', r == '\n', r == '\r':
			return ' '
		case unicode.IsControl(r):
			return -1
		default:
			return r
		}
	}, s)
}

// sanitizeText is sanitizeLine for a body, where line breaks are content and
// are kept.
func sanitizeText(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	return strings.Map(func(r rune) rune {
		switch {
		case r == '\n':
			return r
		case r == '\t':
			return ' '
		case unicode.IsControl(r):
			return -1
		default:
			return r
		}
	}, s)
}
