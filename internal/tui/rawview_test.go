package tui

import (
	"bytes"
	"fmt"
	"runtime"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/igkougkousis01/mailtui/internal/store"
)

// rawRowsOf renders every display row of a payload the slow way, by walking it
// from the first byte. It is the reference the windowed reader has to match:
// whatever rawView returns for a window has to be what a reader who had
// rendered the whole thing would have found there.
func rawRowsOf(raw []byte, width int) []string {
	var out []string
	for off := 0; off < len(raw); {
		end, next := rawLineAt(raw, off)
		line := raw[off:end]
		rawRows(line, width, func(from, to int) {
			out = append(out, sanitizeText(string(line[from:to])))
		})
		off = next
	}
	return out
}

// bigRawMessage builds a payload past the size at which rendering it whole
// stops being an option, with markers at three places a reader has to be able
// to get to.
//
// The markers are deliberately spaced around the old 256 KB display limit: one
// before it, one just past it, and one on the very last line.
const (
	startMarker = "MARKER-FIRST-LINE"
	pastMarker  = "MARKER-PAST-THE-OLD-LIMIT"
	endMarker   = "MARKER-FINAL-LINE"
)

func bigRawMessage(tb testing.TB, headers string) []byte {
	tb.Helper()

	var b strings.Builder
	b.WriteString(headers)
	b.WriteString(startMarker + "\r\n")

	// Past 256 KB, with room either side, in lines the width of a real one.
	const lines = 12000
	past := lines / 2
	for i := range lines {
		switch i {
		case past:
			fmt.Fprintf(&b, "%s\r\n", pastMarker)
		default:
			fmt.Fprintf(&b, "line %06d of a payload nobody wants to render at once\r\n", i)
		}
	}
	b.WriteString(endMarker + "\r\n")

	raw := []byte(b.String())
	if len(raw) <= 256<<10 {
		tb.Fatalf("fixture is %d bytes, want more than the 256 KB it is meant to be past", len(raw))
	}
	// The marker has to land past the old limit or the test proves nothing.
	if i := strings.Index(b.String(), pastMarker); i <= 256<<10 {
		tb.Fatalf("%s sits at byte %d, want it past 256 KB", pastMarker, i)
	}
	return raw
}

const wellFormedHeaders = "From: big@example.test\r\n" +
	"To: dev@example.test\r\n" +
	"Subject: A very large message\r\n" +
	"MIME-Version: 1.0\r\n" +
	"Content-Type: text/plain; charset=utf-8\r\n" +
	"\r\n"

// rawModel returns a model showing raw, focused on the message pane, at the
// given size — the state someone is in when they are reading a payload.
func rawModel(t *testing.T, raw []byte, w, h int) Model {
	t.Helper()

	st := store.New()
	addRaw(t, st, string(raw))

	m := New(st, testAddr, nil)
	m, _ = send(t, m, tea.WindowSizeMsg{Width: w, Height: h})
	m, _ = send(t, m, key('r'))
	m, _ = send(t, m, special(tea.KeyTab))

	if m.mode != modeRaw || m.focus != focusInspect {
		t.Fatalf("mode = %v, focus = %v, want Raw with the message pane focused", m.mode, m.focus)
	}
	return m
}

// scrollBy presses j or k the given number of times, which is what a reader
// does and the only way the offset is ever supposed to move.
func scrollBy(t *testing.T, m Model, n int) Model {
	t.Helper()

	k := key('j')
	if n < 0 {
		k, n = key('k'), -n
	}
	for range n {
		m, _ = send(t, m, k)
	}
	return m
}

// The whole point: a payload far larger than any screenful is navigable from
// its first line to its last, and back.
func TestRawViewNavigatesAWholeLargePayload(t *testing.T) {
	raw := bigRawMessage(t, wellFormedHeaders)
	m := rawModel(t, raw, 100, 30)

	if !strings.Contains(m.render(), startMarker) {
		t.Errorf("the first line of the payload is not on screen at the top:\n%s", m.render())
	}

	limit := m.maxScroll()
	if limit < 10000 {
		t.Fatalf("maxScroll = %d, which is not the size of payload this test is about", limit)
	}

	// Somewhere past where a 256 KB display limit used to end the message.
	m = scrollBy(t, m, limit)
	if m.scroll != limit {
		t.Fatalf("scroll = %d after running j to the end, want %d", m.scroll, limit)
	}
	if !strings.Contains(m.render(), endMarker) {
		t.Errorf("the last line of the payload is not on screen at the bottom:\n%s", m.render())
	}

	// Running past the end changes nothing, and the pane stays full.
	m = scrollBy(t, m, 200)
	if m.scroll != limit {
		t.Errorf("scroll = %d past the end, want %d", m.scroll, limit)
	}
	_, rows := m.inspectSize()
	if got := len(m.content.window(m.scroll, rows)); got != rows {
		t.Errorf("the pane shows %d rows at the bottom, want %d", got, rows)
	}

	// And all the way back.
	m = scrollBy(t, m, -(limit + 200))
	if m.scroll != 0 {
		t.Errorf("scroll = %d back at the top, want 0", m.scroll)
	}
	if !strings.Contains(m.render(), startMarker) {
		t.Errorf("the first line is not back on screen:\n%s", m.render())
	}
}

// Every row of the payload has to be reachable, not just its two ends. Paging
// through it must show each marker, including the one placed past the old
// 256 KB boundary.
func TestRawViewReachesEveryPartOfThePayload(t *testing.T) {
	raw := bigRawMessage(t, wellFormedHeaders)
	m := rawModel(t, raw, 100, 30)

	_, rows := m.inspectSize()
	if rows <= 0 {
		t.Fatal("the inspection pane has no rows")
	}

	seen := map[string]bool{}
	for offset := 0; offset < m.content.len(); offset += rows {
		for _, line := range m.content.window(offset, rows) {
			for _, marker := range []string{startMarker, pastMarker, endMarker} {
				if strings.Contains(line, marker) {
					seen[marker] = true
				}
			}
		}
	}

	for _, marker := range []string{startMarker, pastMarker, endMarker} {
		if !seen[marker] {
			t.Errorf("paging through the payload never showed %s", marker)
		}
	}
}

// The windowed reader has to agree with a reader that had walked the payload
// from the beginning. If the index and the renderer disagree, the offset points
// somewhere other than where the pane draws, and scrolling quietly skips or
// repeats rows.
func TestRawViewWindowMatchesASequentialWalk(t *testing.T) {
	raw := bigRawMessage(t, wellFormedHeaders)

	for _, width := range []int{20, 37, 100} {
		t.Run(fmt.Sprintf("width%d", width), func(t *testing.T) {
			v := newRawView(raw, width)
			want := rawRowsOf(raw, width)

			if v.rows != len(want) {
				t.Fatalf("rows = %d, want %d", v.rows, len(want))
			}

			// Offsets chosen to land inside a checkpoint's span, exactly on one,
			// and at both ends, since those are where an off-by-one hides.
			offsets := []int{0, 1, rawIndexStride - 1, rawIndexStride, rawIndexStride + 1,
				len(want) / 3, len(want) / 2, len(want) - 30, len(want) - 1}

			for _, offset := range offsets {
				if offset < 0 || offset >= len(want) {
					continue
				}
				count := min(30, len(want)-offset)

				got := v.lines(offset, count)
				if len(got) != count {
					t.Fatalf("lines(%d, %d) returned %d rows", offset, count, len(got))
				}
				for i := range got {
					if got[i] != want[offset+i] {
						t.Fatalf("lines(%d, %d)[%d] = %q, want %q", offset, count, i, got[i], want[offset+i])
					}
				}
			}

			// Past the end is empty rather than an error or a panic.
			if got := v.lines(len(want), 10); len(got) != 0 {
				t.Errorf("lines past the end returned %d rows, want none", len(got))
			}
		})
	}
}

// A frame must cost a screenful of work, not a payload of it. Rendering the
// same large message repeatedly may not allocate in proportion to its size.
func TestRawViewRenderingIsBounded(t *testing.T) {
	// Deliberately far bigger than the fixture the other tests use: the point
	// is that the number below does not follow this one.
	raw := append(bigRawMessage(t, wellFormedHeaders), bytes.Repeat([]byte("padding padding padding padding padding padding\r\n"), 100000)...)

	m := rawModel(t, raw, 100, 30)

	// Warm up, so the index is built and out of the measurement.
	m.render()

	const frames = 50

	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	for range frames {
		m = scrollBy(t, m, 1)
		m.render()
	}
	runtime.ReadMemStats(&after)

	perFrame := (after.TotalAlloc - before.TotalAlloc) / frames

	// Generous: a frame is a hundred cells by thirty rows plus the inbox beside
	// it. What it may not be is anywhere near the payload, which is what
	// rendering the message whole every time would cost.
	const budget = 128 << 10
	if perFrame > budget {
		t.Errorf("a frame allocates %d bytes, want under %d for a %d byte payload",
			perFrame, budget, len(raw))
	}
	t.Logf("%d bytes per frame for a %d byte payload", perFrame, len(raw))
}

// The message that never parsed is the one raw inspection exists for, and it
// can be just as large as any other.
func TestRawViewOfALargeMalformedPayload(t *testing.T) {
	raw := bigRawMessage(t, "")
	if !strings.HasPrefix(string(raw), startMarker) {
		t.Fatalf("fixture has a header block after all: %q", raw[:40])
	}

	st := store.New()
	id := addRaw(t, st, string(raw))

	stored, _ := st.Get(id)
	if stored.ParseError == nil {
		t.Fatal("the payload parsed cleanly, so this is not the malformed case")
	}

	m := rawModel(t, raw, 100, 30)

	out := m.render()
	assertExactly(t, out, 100, 30)
	if !strings.Contains(out, startMarker) {
		t.Errorf("the payload is not readable at the top:\n%s", out)
	}

	m = scrollBy(t, m, m.maxScroll())
	out = m.render()
	assertExactly(t, out, 100, 30)
	if !strings.Contains(out, endMarker) {
		t.Errorf("the end of the payload is not reachable:\n%s", out)
	}
}

// Sanitisation is not something the first screenful gets and the rest does not:
// every row is rendered by the same code, and an escape sequence buried a
// quarter of a megabyte in has to be as inert as one in the subject.
func TestRawViewSanitizesBeyondTheFirstScreenful(t *testing.T) {
	var b strings.Builder
	b.WriteString(wellFormedHeaders)
	for i := range 12000 {
		if i == 9000 {
			b.WriteString("buried \x1b[2J\x1b[H clear \x07 bell \x1b]0;retitle\x07 here\r\n")
			continue
		}
		fmt.Fprintf(&b, "line %06d padding padding padding padding padding\r\n", i)
	}
	raw := []byte(b.String())
	if len(raw) <= 256<<10 {
		t.Fatalf("fixture is only %d bytes", len(raw))
	}

	m := rawModel(t, raw, 100, 30)

	_, rows := m.inspectSize()
	found := false
	for offset := 0; offset < m.content.len(); offset += rows {
		for _, line := range m.content.window(offset, rows) {
			if strings.Contains(line, "buried") {
				found = true
			}
			for _, escape := range []string{"\x1b[2J", "\x1b[H", "\x1b]0;", "\x07"} {
				if strings.Contains(line, escape) {
					t.Fatalf("a control sequence %q survived at offset %d: %q", escape, offset, line)
				}
			}
		}
	}
	if !found {
		t.Error("the line carrying the escape sequences was never shown, so nothing was proven")
	}

	// And the frame that actually draws it is still a frame.
	for _, offset := range []int{0, 8990, m.content.len() - 1} {
		mm := m
		mm.scroll = offset
		mm.clampScroll()
		assertExactly(t, mm.render(), 100, 30)
	}
}

// A payload with no line breaks at all is not real mail, but it is exactly the
// sort of thing that gets captured when something has gone wrong. It must not
// take the pane, or the process, with it.
func TestRawViewOfOneEnormousLine(t *testing.T) {
	raw := []byte(startMarker + strings.Repeat("x", 1<<20) + endMarker)

	m := rawModel(t, raw, 100, 30)

	out := m.render()
	assertExactly(t, out, 100, 30)
	if !strings.Contains(out, startMarker) {
		t.Errorf("the beginning of the line is not on screen:\n%s", out)
	}

	limit := m.maxScroll()
	if limit <= 0 {
		t.Fatalf("maxScroll = %d, so a megabyte on one line is not scrollable", limit)
	}

	m = scrollBy(t, m, limit)
	out = m.render()
	assertExactly(t, out, 100, 30)
	if !strings.Contains(out, endMarker) {
		t.Errorf("the end of the line is not reachable:\n%s", out)
	}
}

// A large payload has to survive the sizes a dragged window corner produces,
// including the ones with no room to draw anything.
func TestRawViewAtSmallTerminalSizes(t *testing.T) {
	raw := bigRawMessage(t, wellFormedHeaders)

	for _, size := range [][2]int{{1, 1}, {10, 3}, {24, 8}, {30, 9}, {40, 12}, {63, 20}, {64, 20}} {
		t.Run(fmt.Sprintf("%dx%d", size[0], size[1]), func(t *testing.T) {
			m := rawModel(t, raw, size[0], size[1])

			assertExactly(t, m.render(), size[0], size[1])

			m = scrollBy(t, m, 50)
			assertExactly(t, m.render(), size[0], size[1])

			m = scrollBy(t, m, -100)
			if m.scroll != 0 {
				t.Errorf("scroll = %d back at the top, want 0", m.scroll)
			}
			assertExactly(t, m.render(), size[0], size[1])
		})
	}
}

// An empty payload has no rows, and asking for some must not walk off either
// end of it.
func TestRawViewOfNothing(t *testing.T) {
	v := newRawView(nil, 40)
	if v.rows != 0 {
		t.Errorf("rows = %d for an empty payload, want 0", v.rows)
	}
	if got := v.lines(0, 10); got != nil {
		t.Errorf("lines = %q, want none", got)
	}

	// A pane with no width to draw in indexes nothing and returns nothing.
	if got := newRawView([]byte("hello"), 0); got.rows != 0 || got.lines(0, 5) != nil {
		t.Errorf("a zero-width view produced %d rows", got.rows)
	}
}

// rawRows is the one definition of how a line becomes rows, so both the index
// and the renderer inherit whatever it gets wrong. These are the cases where an
// off-by-one would cost a column of every row, or invent a blank one.
func TestRawRowsCutsAtThePaneEdge(t *testing.T) {
	tests := []struct {
		name  string
		line  string
		width int
		want  []string
	}{
		{"empty line is one blank row", "", 8, []string{""}},
		{"short line is one row", "abc", 8, []string{"abc"}},
		{"a line exactly as wide as the pane is one row", "abcdefgh", 8, []string{"abcdefgh"}},
		{"one cell over is two rows", "abcdefghi", 8, []string{"abcdefgh", "i"}},
		{"twice the width is two rows, not three", "abcdefghijklmnop", 8, []string{"abcdefgh", "ijklmnop"}},
		{"a trailing carriage return costs no cells", "abcdefgh\r", 8, []string{"abcdefgh"}},
		{"control characters are dropped rather than wrapped", "ab\x00\x01cd", 4, []string{"abcd"}},
		{"a tab is one cell", "ab\tcd", 4, []string{"ab c", "d"}},
		{"cuts by display width, not by bytes", "日本語です", 4, []string{"日本", "語で", "す"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got []string
			line := []byte(tt.line)
			rawRows(line, tt.width, func(from, to int) {
				got = append(got, sanitizeText(string(line[from:to])))
			})

			if len(got) != len(tt.want) {
				t.Fatalf("rows = %q, want %q", got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("row %d = %q, want %q", i, got[i], tt.want[i])
				}
			}

			// The count the index is built from has to be the same number.
			if n := rawRowCount(line, tt.width); n != len(tt.want) {
				t.Errorf("rawRowCount = %d, want %d", n, len(tt.want))
			}
		})
	}
}

// A payload with no trailing newline still ends in a line, and one that ends
// with a newline does not gain an empty one.
func TestRawViewCountsFinalLines(t *testing.T) {
	tests := []struct {
		raw  string
		want int
	}{
		{"one", 1},
		{"one\n", 1},
		{"one\ntwo", 2},
		{"one\ntwo\n", 2},
		{"one\r\ntwo\r\n", 2},
		{"\n", 1},
		{"\n\n", 2},
	}

	for _, tt := range tests {
		t.Run(fmt.Sprintf("%q", tt.raw), func(t *testing.T) {
			if got := newRawView([]byte(tt.raw), 40).rows; got != tt.want {
				t.Errorf("rows = %d, want %d", got, tt.want)
			}
		})
	}
}
