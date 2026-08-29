package extract

import (
	"unicode"

	"github.com/igkougkousis01/mailtui/internal/message"
)

// otpKeywords are the words a one-time code is announced with. Lower case;
// the text is folded before they are looked for.
var otpKeywords = []string{
	"code", "otp", "pin", "passcode", "password", "token",
	"verification", "verify", "one-time", "one time", "2fa",
}

// otpKeywordWindow is how many characters may sit between a keyword and the
// number it refers to. Thirty is roughly "the same sentence" once whitespace
// has been collapsed — it covers "Your verification code is: 483921" and
// "483921 is your login code", and stops a keyword in one paragraph from
// claiming a number in the next.
const otpKeywordWindow = 30

// OTP returns the one-time code in m, and whether one was found.
//
// The heuristic, in full:
//
//   - The text read is the text body, or the HTML body flattened to text, or —
//     for a message that failed to parse — the raw payload. Runs of whitespace
//     collapse to a single space first.
//   - A candidate is a run of 4, 5, 6 or 8 digits with no digit either side.
//     Those are the lengths one-time codes come in; a 7 or 11 digit run is
//     something else.
//   - A candidate is rejected if a letter or underscore touches it (id_123456,
//     1234px), if it is part of a longer number written with separators
//     (2026-08-29, 1.234567, 555-1234), if it sits inside an http(s) URL, or
//     if it is four digits that read as a year between 1900 and 2099 — the
//     copyright line in a mail footer is the most common false code there is.
//   - Of what survives, the winner is the candidate closest to one of the
//     words a code is usually announced with (code, otp, pin, passcode,
//     password, token, verification, verify, one-time, 2fa), within thirty
//     characters either side, nearest first and earliest to break a tie. So
//     "Order #12345, your code is 483921" yields 483921.
//   - With no keyword anywhere near, the first surviving candidate in the
//     message wins.
//
// It is a guess, and it is deterministic: the same message always yields the
// same answer. It reads nothing but the message — there is no model here, and
// no network.
func OTP(m message.Message) (string, bool) {
	text := []rune(normalizeSpace(bodyText(m)))
	if len(text) == 0 {
		return "", false
	}

	found := otpCandidates(text)
	if len(found) == 0 {
		return "", false
	}

	if best, ok := nearestKeyword(text, found); ok {
		return string(text[best.start:best.end]), true
	}
	return string(text[found[0].start:found[0].end]), true
}

// span is a half-open range of runes within the text being read.
type span struct{ start, end int }

// otpCandidates returns the digit runs that could be a code, in the order they
// appear.
func otpCandidates(text []rune) []span {
	urls := urlSpans(text)

	var found []span
	for i := 0; i < len(text); {
		if !isDigit(text[i]) {
			i++
			continue
		}

		start := i
		for i < len(text) && isDigit(text[i]) {
			i++
		}
		s := span{start, i}

		if plausibleOTP(text, s) && !within(urls, s) {
			found = append(found, s)
		}
	}
	return found
}

// plausibleOTP applies the rejections listed on OTP to one digit run.
func plausibleOTP(text []rune, s span) bool {
	switch s.end - s.start {
	case 4, 5, 6, 8:
	default:
		return false
	}

	before, after := runeAt(text, s.start-1), runeAt(text, s.end)

	// Part of a word: an identifier, a CSS length, a hex-ish blob.
	if isWordRune(before) || isWordRune(after) {
		return false
	}
	// Part of a longer number written with separators: a date, a decimal, a
	// phone number, a time.
	if isNumberJoiner(before) && isDigit(runeAt(text, s.start-2)) {
		return false
	}
	if isNumberJoiner(after) && isDigit(runeAt(text, s.end+1)) {
		return false
	}
	// A year in a footer, a copyright line, a date written out.
	if s.end-s.start == 4 && looksLikeYear(text[s.start:s.end]) {
		return false
	}

	return true
}

// nearestKeyword returns the candidate closest to a code keyword, if any is
// close enough.
func nearestKeyword(text []rune, found []span) (span, bool) {
	lower := foldRunes(text)

	best, bestGap := span{}, otpKeywordWindow+1
	for _, keyword := range otpKeywords {
		for _, at := range occurrences(lower, []rune(keyword)) {
			for _, cand := range found {
				gap := gapBetween(at, cand)
				// Strictly nearer wins, so a tie falls to the candidate found
				// first — which, because found is in document order and the
				// keywords are scanned in a fixed order, is the earlier one.
				if gap <= otpKeywordWindow && gap < bestGap {
					best, bestGap = cand, gap
				}
			}
		}
	}

	return best, bestGap <= otpKeywordWindow
}

// gapBetween is how many characters separate two spans, and 0 if they touch or
// overlap.
func gapBetween(a, b span) int {
	if a.end <= b.start {
		return b.start - a.end
	}
	if b.end <= a.start {
		return a.start - b.end
	}
	return 0
}

// occurrences returns the spans of sub within text.
func occurrences(text, sub []rune) []span {
	if len(sub) == 0 || len(sub) > len(text) {
		return nil
	}

	var out []span
	for i := 0; i+len(sub) <= len(text); i++ {
		match := true
		for j, r := range sub {
			if text[i+j] != r {
				match = false
				break
			}
		}
		if match {
			out = append(out, span{i, i + len(sub)})
		}
	}
	return out
}

// foldRunes lowercases rune by rune, which keeps the result the same length as
// its input and so keeps every offset into it valid.
func foldRunes(text []rune) []rune {
	out := make([]rune, len(text))
	for i, r := range text {
		out[i] = unicode.ToLower(r)
	}
	return out
}

// within reports whether s falls inside any of the given spans.
func within(spans []span, s span) bool {
	for _, outer := range spans {
		if s.start >= outer.start && s.end <= outer.end {
			return true
		}
	}
	return false
}

// runeAt returns the rune at i, or 0 when i is outside the text. The zero rune
// is never a digit, a letter or a separator, so the edges of the text behave
// like the whitespace they effectively are.
func runeAt(text []rune, i int) rune {
	if i < 0 || i >= len(text) {
		return 0
	}
	return text[i]
}

func isDigit(r rune) bool { return r >= '0' && r <= '9' }

// isWordRune reports whether r would make a digit run part of a longer token.
func isWordRune(r rune) bool { return r == '_' || unicode.IsLetter(r) }

// isNumberJoiner reports whether r is punctuation that joins numbers into a
// bigger one: a date, a decimal, a time, a phone number, a version.
func isNumberJoiner(r rune) bool {
	switch r {
	case '.', ',', '-', '/', ':':
		return true
	}
	return false
}

// looksLikeYear reports whether four digits are a year anyone would print.
func looksLikeYear(digits []rune) bool {
	year := 0
	for _, r := range digits {
		year = year*10 + int(r-'0')
	}
	return year >= 1900 && year <= 2099
}
