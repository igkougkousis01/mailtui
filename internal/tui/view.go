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

	// maxDisplayBytes bounds how much of a decoded text body one rebuild turns
	// into wrapped lines.
	//
	// It does not apply to Raw, which is windowed instead and stays navigable
	// to its last byte however large it is — see rawview.go. A text body is
	// different: it is a decoded part rather than the whole payload, it is
	// rarely large, and the message says on screen when it was cut. Nothing is
	// dropped from what is stored either way.
	maxDisplayBytes = 256 << 10
)

// Colours are ANSI palette indices rather than hex, so the terminal's own
// theme decides what they actually look like and the UI stays legible on a
// light background as well as a dark one.
var (
	// borderColour is shared by the pane borders and the rule inside the
	// preview, so the two never drift apart. focusColour marks the pane the
	// movement keys are pointed at.
	borderColour = lipgloss.Color("8")
	focusColour  = lipgloss.Color("6")

	styleAppName   = lipgloss.NewStyle().Bold(true)
	styleDim       = lipgloss.NewStyle().Foreground(borderColour)
	styleBorder    = lipgloss.NewStyle().Foreground(borderColour)
	stylePaneTitle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("4"))
	styleSelected  = lipgloss.NewStyle().Bold(true).Foreground(focusColour)
	styleOnline    = lipgloss.NewStyle().Foreground(lipgloss.Color("2"))
	styleWarn      = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("1"))
	styleLabel     = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("4"))

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

// renderFooter is the whole key map on one row: which view of the message is
// showing and how to reach the others, then what the movement keys do right
// now. It is where the active mode is stated in words.
//
// Both halves are dropped in order of how much they are needed as the terminal
// narrows, rather than being truncated mid-word.
func (m Model) renderFooter(w int) string {
	if len(m.msgs) == 0 {
		return styleDim.Render(fitLine("q  quit", w))
	}

	left := m.renderModeBar()
	right := styleDim.Render(m.navHint())

	if gap := w - ansi.StringWidth(left) - ansi.StringWidth(right); gap >= 2 {
		return left + strings.Repeat(" ", gap) + right
	}
	if ansi.StringWidth(left) <= w {
		return fitLine(left, w)
	}
	// Too narrow even for the key list. Which view is on screen is the part
	// that cannot be guessed from looking at it, so that is what survives.
	return styleSelected.Render(fitLine(m.mode.String(), w))
}

// renderModeBar lists the four views and their keys, with the active one
// picked out. Naming the key next to the view is the whole instruction manual
// for switching between them.
func (m Model) renderModeBar() string {
	parts := make([]string, 0, len(modes))
	for _, mode := range modes {
		label := mode.key + " " + mode.name
		if mode.mode == m.mode {
			parts = append(parts, styleSelected.Render(label))
			continue
		}
		parts = append(parts, styleDim.Render(label))
	}
	return strings.Join(parts, styleDim.Render(" · "))
}

// navHint says what j and k do, which depends on where tab last left the
// focus. Stating it beats expecting anyone to remember.
func (m Model) navHint() string {
	if m.focus == focusInspect {
		return "tab inbox · j/k scroll · q quit"
	}
	return "tab view · j/k messages · q quit"
}

// renderBody draws the panes into exactly w by h cells.
func (m Model) renderBody(w, h int) string {
	if len(m.msgs) == 0 {
		// One full-width pane rather than two: an empty inbox has nothing to
		// put on either side of a divider.
		return paneSpec{title: "Inbox", content: m.emptyStateLines()}.render(w, h)
	}

	if w < narrowWidth {
		return m.renderStacked(w, h)
	}

	listW := listWidth(w)
	list := m.listPane().render(listW, h)
	preview := m.inspectPane().render(w-listW, h)

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
	listH := stackedListHeight(h)

	previewH := h - listH
	if previewH < 5 {
		// Not enough room for two boxes. The message is the one worth keeping:
		// the list is navigation, and navigation still works blind.
		return m.inspectPane().render(w, h)
	}

	list := m.listPane().render(w, listH)
	preview := m.inspectPane().render(w, previewH)

	return lipgloss.JoinVertical(lipgloss.Left, list, preview)
}

// stackedListHeight is the inbox pane's share of a stacked layout: about a
// third, but never so little that the cursor has nowhere to move nor so much
// that the message is squeezed.
func stackedListHeight(h int) int {
	listH := h/3 + paneHeaderRows
	if listH < 5 {
		listH = 5
	}
	if listH > 10 {
		listH = 10
	}
	return listH
}

// listPane is the inbox pane, sized by whichever layout is drawing it.
func (m Model) listPane() paneSpec {
	rowHeight := listRowHeight
	if m.width < narrowWidth {
		rowHeight = compactRowHeight
	}

	return paneSpec{
		title:   "Inbox",
		note:    fmt.Sprintf("%d/%d", m.cursor+1, len(m.msgs)),
		focused: m.focus == focusInbox,
		layout: func(inner, rows int) []string {
			return m.listLines(inner, rows, rowHeight)
		},
	}
}

// inspectPane is the message pane: the current mode's lines, scrolled to where
// the reader left them.
//
// The scroll position goes in the pane's note, because a pane showing rows 40
// to 60 of a raw message is otherwise indistinguishable from one showing the
// whole thing.
func (m Model) inspectPane() paneSpec {
	content := m.inspectContent()

	note := ""
	if _, rows := m.inspectSize(); rows > 0 && content.len() > rows {
		note = fmt.Sprintf("%d-%d/%d", m.scroll+1, min(m.scroll+rows, content.len()), content.len())
	}

	return paneSpec{
		title:   "Message · " + m.mode.String(),
		note:    note,
		focused: m.focus == focusInspect,
		layout: func(inner, rows int) []string {
			return content.window(m.scroll, rows)
		},
	}
}

// inspectSize is the inner width and row count of the inspection pane at the
// current terminal size — the same numbers renderBody hands to a paneSpec.
//
// Update needs them to know how far the content may be scrolled, and View
// needs them to draw it. Deriving both from one function is what stops the
// scroll offset and the visible window from disagreeing.
func (m Model) inspectSize() (w, rows int) {
	if m.width < minWidth || m.height < minHeight || len(m.msgs) == 0 {
		return 0, 0
	}

	h := m.height - 2 // the title bar and the footer

	if m.width < narrowWidth {
		previewH := h - stackedListHeight(h)
		if previewH < 5 {
			previewH = h
		}
		return m.width - paneChrome, max(paneRows(previewH), 0)
	}

	return m.width - listWidth(m.width) - paneChrome, max(paneRows(h), 0)
}

// inspectContent is what the inspection pane has to show, and the only thing
// the scroll offset is measured against.
//
// Every mode but Raw renders its lines up front: a parsed message is small, it
// is already in memory, and slicing a []string is the cheapest possible frame.
// Raw cannot, because the payload may be tens of megabytes, so it carries an
// index instead and produces rows on demand. Both answer the same two
// questions, which is all the pane and the scroll arithmetic ever ask.
type inspectContent struct {
	// lines is the whole content for every mode but Raw, where it is the
	// banner above the payload.
	lines []string

	// raw supplies the rows below lines. It is nil except in Raw mode.
	raw *rawView
}

// len is how many rows the content has in total.
func (c inspectContent) len() int {
	if c.raw == nil {
		return len(c.lines)
	}
	return len(c.lines) + c.raw.rows
}

// window returns rows [offset, offset+count) — the rows the pane is about to
// draw, and in Raw mode the only ones that are rendered at all.
func (c inspectContent) window(offset, count int) []string {
	if count <= 0 {
		return nil
	}
	if offset < 0 {
		offset = 0
	}

	head := c.lines[min(offset, len(c.lines)):min(offset+count, len(c.lines))]
	if c.raw == nil {
		return head
	}

	tail := c.raw.lines(max(offset-len(c.lines), 0), count-len(head))
	if len(head) == 0 {
		return tail
	}
	// A fresh slice rather than appending to head, which is a view into
	// c.lines and would have the banner's spare capacity written over.
	return append(append(make([]string, 0, len(head)+len(tail)), head...), tail...)
}

// inspectContent returns the pane's content, using what Update already built
// when it still describes this model and building it here when it does not.
// See Model.syncContent.
func (m Model) inspectContent() inspectContent {
	if m.currentContentKey() == m.contentKey {
		return m.content
	}
	w, _ := m.inspectSize()
	return m.buildInspect(w)
}

// buildInspect prepares the selected message in the current mode for a pane w
// cells wide.
func (m Model) buildInspect(w int) inspectContent {
	if w <= 0 {
		return inspectContent{}
	}

	msg, ok := m.selected()
	if !ok {
		return inspectContent{lines: m.emptyStateLines()}
	}

	switch m.mode {
	case modeHeaders:
		return inspectContent{lines: headerModeLines(msg, w)}
	case modeRaw:
		return rawModeContent(msg, w)
	case modeAttachments:
		return inspectContent{lines: attachmentModeLines(msg, w)}
	default:
		return inspectContent{lines: bodyModeLines(msg, w)}
	}
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
		} else if len(msg.Attachments) > 0 {
			flag = "@ "
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

// bodyModeLines renders the selected message the way a mail client would: the
// parse warning if there is one, then the headers a reader cares about, the
// envelope, and the body.
//
// This is the one view that mixes the SMTP envelope in with the message's own
// headers, and it does so with the two labelled apart. Headers mode shows the
// header block alone, because that block is a record of what was sent and the
// envelope was never part of it.
func bodyModeLines(msg message.Message, w int) []string {
	var lines []string

	if msg.ParseError != nil {
		lines = append(lines, parseWarningLines(msg, w)...)
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

// parseWarningLines is the banner a message that failed to parse carries: what
// went wrong, and that nothing was thrown away because of it.
func parseWarningLines(msg message.Message, w int) []string {
	lines := []string{styleWarn.Render(fitLine("! This message could not be parsed.", w))}
	for _, l := range wrapLines(sanitizeLine(msg.ParseError.Error()), w) {
		lines = append(lines, styleWarn.Render(l))
	}
	for _, l := range wrapLines("The raw bytes were captured in full and are retained. Press r to read them.", w) {
		lines = append(lines, styleDim.Render(l))
	}
	return append(lines, "")
}

// headerModeLines renders the message's own header block, in the order it was
// written, with repeats intact. Nothing is reconstructed: these are the fields
// the parser read, not a rendering of the handful of them Message names.
func headerModeLines(msg message.Message, w int) []string {
	if len(msg.Headers) == 0 {
		var lines []string
		if msg.ParseError != nil {
			lines = append(lines, parseWarningLines(msg, w)...)
		}
		return append(lines, wrapLines(styleDim.Render("No headers could be read."), w)...)
	}

	lines := []string{
		styleDim.Render(fitLine(plural(len(msg.Headers), "header field", "header fields"), w)),
		"",
	}
	for _, h := range msg.Headers {
		lines = append(lines, headerFieldLines(h, w)...)
	}
	return lines
}

// headerFieldLines renders one header field. A field that fits goes on one
// row; one that does not puts its name on a row of its own and indents the
// value below it, which keeps a long Received line readable as a block instead
// of as a paragraph with a ragged first line.
func headerFieldLines(h message.Header, w int) []string {
	key := sanitizeLine(h.Key)
	value := sanitizeLine(h.Value)

	if value == "" {
		return []string{fitLine(styleLabel.Render(key+":")+" "+styleDim.Render("(empty)"), w)}
	}

	if len(key)+2+ansi.StringWidth(value) <= w {
		return []string{fitLine(styleLabel.Render(key+":")+" "+value, w)}
	}

	lines := []string{fitLine(styleLabel.Render(key+":"), w)}
	for _, l := range wrapLines(value, w-2) {
		lines = append(lines, "  "+l)
	}
	return lines
}

// rawModeContent prepares the captured bytes for display.
//
// The bytes themselves are never regenerated and never re-encoded: this is
// Message.Raw, the DATA payload exactly as it arrived, and the whole of it stays
// reachable however large it is. What happens here is display normalisation and
// nothing else — a carriage return never reaches the terminal, control
// characters are dropped so the message cannot drive the cursor, and long lines
// are cut to the pane so it keeps its shape. All of that happens a screenful at
// a time, in rawView; the stored message is untouched by any of it.
func rawModeContent(msg message.Message, w int) inspectContent {
	banner := []string{
		styleWarn.Render(fitLine("RAW  "+formatBytes(int64(len(msg.Raw)))+"  exactly as captured", w)),
		"",
	}

	if len(msg.Raw) == 0 {
		return inspectContent{lines: append(banner, wrapLines(styleDim.Render("(no bytes were captured)"), w)...)}
	}
	return inspectContent{lines: banner, raw: newRawView(msg.Raw, w)}
}

// attachmentModeLines lists what the message carried besides its body: one
// entry per part, with what a developer checking their mail-sending code needs
// to confirm — that the file is there, that it is the type they meant, that it
// is not empty, and that an embedded image kept the Content-ID the HTML points
// at.
func attachmentModeLines(msg message.Message, w int) []string {
	if len(msg.Attachments) == 0 {
		lines := []string{styleDim.Render(fitLine("No attachments", w))}
		if msg.ParseError != nil {
			lines = append(lines, "")
			lines = append(lines, wrapLines(styleDim.Render(
				"This message could not be parsed, so its parts were never read. Press r for the raw bytes."), w)...)
		}
		return lines
	}

	lines := []string{
		styleDim.Render(fitLine(plural(len(msg.Attachments), "attachment", "attachments"), w)),
		"",
	}
	for i, att := range msg.Attachments {
		lines = append(lines, attachmentLines(i+1, att, w)...)
	}
	return lines
}

// attachmentLines is one entry: its name on a row, then its metadata on a
// dimmed row under it.
func attachmentLines(n int, att message.Attachment, w int) []string {
	name := sanitizeLine(att.Filename)
	if strings.TrimSpace(name) == "" {
		name = "(no filename)"
	}

	facts := []string{sanitizeLine(att.ContentType), formatBytes(att.Size)}
	if att.Disposition != "" {
		facts = append(facts, sanitizeLine(att.Disposition))
	}
	if att.ContentID != "" {
		facts = append(facts, "cid: "+sanitizeLine(att.ContentID))
	}

	lines := []string{fitLine(fmt.Sprintf("%2d. ", n)+styleLabel.Render(name), w)}
	for _, l := range wrapLines(strings.Join(facts, " · "), max(w-4, 1)) {
		lines = append(lines, "    "+styleDim.Render(l))
	}
	return append(lines, "")
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
// acknowledged rather than rendered: the Body view deliberately shows the
// parsed plain-text alternative rather than interpreting markup.
func bodyLines(msg message.Message, w int) []string {
	clamped, dropped := clampForDisplay(msg.TextBody)

	text := strings.TrimSpace(sanitizeText(clamped))
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

	if dropped > 0 {
		lines = append(lines, "")
		lines = append(lines, wrapLines(styleDim.Render(fmt.Sprintf(
			"… %s more not shown here. The message is stored in full.", formatBytes(int64(dropped)))), w)...)
	}
	return lines
}

// clampForDisplay cuts a decoded text body down to what one rebuild is willing
// to wrap and reports how many bytes were left out, so the view can say so. It
// cuts on a rune boundary, so the result is still valid text.
func clampForDisplay(s string) (string, int) {
	if len(s) <= maxDisplayBytes {
		return s, 0
	}

	cut := maxDisplayBytes
	for cut > 0 && !utf8Start(s[cut]) {
		cut--
	}
	return s[:cut], len(s) - cut
}

// formatBytes renders a byte count for a human. Powers of ten rather than two,
// because this number exists to be compared with what a file manager shows.
func formatBytes(n int64) string {
	switch {
	case n < 1000:
		return fmt.Sprintf("%d B", n)
	case n < 1000*1000:
		return fmt.Sprintf("%.1f kB", float64(n)/1000)
	default:
		return fmt.Sprintf("%.1f MB", float64(n)/1e6)
	}
}

// plural renders a count with the right noun, because "1 attachments" reads as
// a bug in the program rather than as a fact about the mail.
func plural(n int, one, many string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, one)
	}
	return fmt.Sprintf("%d %s", n, many)
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

// paneSpec is one bordered box: what it is called, what goes in it, and
// whether the movement keys are pointed at it.
//
// The content arrives as a function of the box's inner size rather than as
// lines, because neither pane knows how big it is until the layout has decided
// — and the inbox chooses how many entries to draw from exactly that number.
type paneSpec struct {
	title string

	// note is drawn right-aligned on the title row: a position, a count, the
	// small print that says what part of the whole is on screen.
	note string

	// content is used when layout is nil, for a pane whose lines do not depend
	// on its size.
	content []string
	layout  func(inner, rows int) []string

	focused bool
}

// render draws the pane into exactly w by h cells. Anything too small to hold
// a border and a row of content renders as nothing rather than as garbage.
func (p paneSpec) render(w, h int) string {
	inner := w - paneChrome
	rows := h - 2
	if inner < 1 || rows < 1 {
		return ""
	}

	content := p.content
	if p.layout != nil {
		content = p.layout(inner, max(rows-paneHeaderRows, 0))
	}

	lines := make([]string, 0, rows)
	lines = append(lines, p.titleRow(inner))
	if rows > 1 {
		lines = append(lines, "")
	}
	lines = append(lines, content...)

	body := strings.Join(fitBlock(lines, inner, rows), "\n")

	colour := borderColour
	if p.focused {
		colour = focusColour
	}

	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(colour).
		Padding(0, 1).
		Render(body)
}

// titleRow puts the title on the left and the note on the right, dropping the
// note rather than letting the two collide.
func (p paneSpec) titleRow(inner int) string {
	title := stylePaneTitle.Render(p.title)
	if p.note == "" {
		return fitLine(title, inner)
	}

	note := styleDim.Render(p.note)
	gap := inner - ansi.StringWidth(title) - ansi.StringWidth(note)
	if gap < 1 {
		return fitLine(title, inner)
	}
	return title + strings.Repeat(" ", gap) + note
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
