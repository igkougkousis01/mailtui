package tui

import (
	"bytes"
	"sort"
	"unicode/utf8"

	"github.com/charmbracelet/x/ansi"
)

// A captured payload can be tens of megabytes — a base64 attachment is all in
// Message.Raw — and Raw mode is the view that has to survive anyway, because it
// is where a developer ends up when parsing has failed. So it cannot be
// rendered the way the other modes are, which turn the whole message into
// []string once and scroll a window over it.
//
// rawView is that view instead. It never holds the payload as lines. It walks
// the bytes once to record where the display rows are, and then renders only
// the rows the pane is about to draw. The cost of the walk is paid when the
// message or the pane width changes; the cost of a frame is a binary search and
// a screenful of strings.

const (
	// rawIndexStride is how many source lines one checkpoint covers.
	//
	// It trades index size against the forward scan needed to land exactly:
	// bigger means fewer checkpoints and a longer scan. 256 keeps both small —
	// a few hundred checkpoints for a megabyte of ordinary mail, and a scan
	// over at most 256 lines to place the top of the pane.
	rawIndexStride = 256

	// rawLineLimit is the longest run of bytes treated as a single source line.
	//
	// Real mail lines are short; RFC 5322 caps them at 998 octets. A payload
	// that arrives as one unbroken megabyte is not real mail, but it is exactly
	// the sort of thing a developer captures mailtui to look at. Cutting it
	// here means every line the index and the renderer handle is small enough
	// to measure and wrap without allocating anything unbounded.
	rawLineLimit = 4096
)

// rawView renders a payload as scrollable text.
//
// It borrows the byte slice rather than copying it: the Model holds a snapshot
// of the message that nothing mutates, and copying 25 MB to display it would
// undo the point of the exercise.
type rawView struct {
	raw   []byte
	width int

	// marks is one checkpoint every rawIndexStride source lines. Finding the
	// line that a display row falls in is a binary search here followed by a
	// walk of at most that many lines.
	marks []rawMark

	// rows is how many display rows the whole payload occupies.
	rows int
}

// rawMark ties a source line's byte offset to the display row it starts at.
type rawMark struct {
	offset int
	row    int
}

// newRawView indexes raw for display at the given pane width.
//
// This is the one pass over the payload. It allocates nothing per line: the
// walk measures each line in display cells and keeps a running row total,
// recording a checkpoint every so often.
func newRawView(raw []byte, width int) *rawView {
	v := &rawView{raw: raw, width: width}
	if width <= 0 {
		return v
	}

	for off, line := 0, 0; off < len(raw); line++ {
		if line%rawIndexStride == 0 {
			v.marks = append(v.marks, rawMark{offset: off, row: v.rows})
		}

		end, next := rawLineAt(raw, off)
		v.rows += rawRowCount(raw[off:end], width)
		off = next
	}
	return v
}

// lines returns display rows [offset, offset+count) of the payload.
func (v *rawView) lines(offset, count int) []string {
	if v == nil || v.width <= 0 || count <= 0 || offset >= v.rows {
		return nil
	}
	if offset < 0 {
		offset = 0
	}

	off, row := v.seek(offset)

	out := make([]string, 0, count)
	for off < len(v.raw) && len(out) < count {
		end, next := rawLineAt(v.raw, off)
		line := v.raw[off:end]

		rawRows(line, v.width, func(from, to int) {
			if row >= offset && len(out) < count {
				out = append(out, sanitizeText(string(line[from:to])))
			}
			row++
		})

		off = next
	}
	return out
}

// seek returns the byte offset of the source line that display row target
// falls in, and the row that line starts at.
func (v *rawView) seek(target int) (off, row int) {
	// The last checkpoint at or before the target row. Search returns the
	// first one past it, so the one we want is just before that.
	i := sort.Search(len(v.marks), func(i int) bool { return v.marks[i].row > target }) - 1
	if i < 0 {
		return 0, 0
	}
	off, row = v.marks[i].offset, v.marks[i].row

	for off < len(v.raw) {
		end, next := rawLineAt(v.raw, off)
		n := rawRowCount(v.raw[off:end], v.width)
		if row+n > target {
			return off, row
		}
		row += n
		off = next
	}
	return off, row
}

// rawLineAt returns the end of the source line starting at off, and the offset
// the next line starts at.
//
// A line ends at the next newline or after rawLineLimit bytes, whichever comes
// first. The cut is moved back to a UTF-8 boundary so a multi-byte character is
// never split across two lines.
func rawLineAt(raw []byte, off int) (end, next int) {
	limit := min(off+rawLineLimit, len(raw))

	if i := bytes.IndexByte(raw[off:limit], '\n'); i >= 0 {
		return off + i, off + i + 1
	}
	if limit == len(raw) {
		return limit, limit
	}

	// A rune is at most four bytes, so backing up that far is always enough to
	// find where the next one begins.
	for range 3 {
		if limit <= off+1 || utf8Start(raw[limit]) {
			break
		}
		limit--
	}
	return limit, limit
}

// rawRows walks a source line and reports the byte range of each pane row.
//
// It is the single definition of how a raw line becomes rows, so counting them
// and rendering them can never drift apart and leave the scroll offset pointing
// somewhere other than where the pane draws.
//
// Rows are cut at exactly width display cells rather than at word boundaries:
// this is a byte dump, and a raw view that reflowed words would stop showing
// what the message actually looks like. Control bytes are counted as nothing
// because the view drops them, so a row's byte range can be longer than the row
// is wide.
func rawRows(line []byte, width int, emit func(from, to int)) {
	if width <= 0 {
		return
	}

	start, cells := 0, 0
	for i := 0; i < len(line); {
		size, w := 1, 1

		if c := line[i]; c < utf8.RuneSelf {
			if c < 0x20 || c == 0x7f {
				// Dropped by sanitizeText, except for a tab, which it turns
				// into a single space.
				w = 0
				if c == '\t' {
					w = 1
				}
			}
		} else {
			r, n := utf8.DecodeRune(line[i:])
			size, w = n, runeCells(r)
		}

		// Only break when there is something to put on the next row: a line
		// that ends exactly at the pane edge is one row, not one and a blank.
		if cells+w > width && i > start {
			emit(start, i)
			start, cells = i, 0
		}

		cells += w
		i += size
	}

	// Always at least one row, so an empty line is a blank row rather than
	// nothing at all.
	emit(start, len(line))
}

// rawRowCount is rawRows without the strings: how many pane rows a source line
// occupies. The index is built out of these, so it must allocate nothing.
func rawRowCount(line []byte, width int) int {
	n := 0
	rawRows(line, width, func(int, int) { n++ })
	return n
}

// runeCells is how many terminal columns r occupies.
//
// It is only reached for non-ASCII text, which a raw mail payload rarely
// contains much of, so measuring one rune at a time costs little and saves
// depending on a width table of our own.
func runeCells(r rune) int {
	if r == utf8.RuneError {
		return 1
	}
	return ansi.StringWidth(string(r))
}

// utf8Start reports whether b begins a UTF-8 encoded rune rather than
// continuing one.
func utf8Start(b byte) bool {
	return b&0xC0 != 0x80
}
