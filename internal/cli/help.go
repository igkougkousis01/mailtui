package cli

import (
	"fmt"
	"net"

	"github.com/igkougkousis01/mailtui/internal/smtp"
)

// The help text is written by hand rather than generated from the flag
// definitions, because the flags are the least of what someone needs to know:
// how matching compares strings, what goes to stdout, and what an exit code
// means are the parts that decide whether a test script is correct.
//
// Every default it quotes is interpolated from the constant the code actually
// uses, so help that has gone stale is a compile-time impossibility rather
// than something to notice in review.

// The defaults as help writes them. defaultPort is pulled out of the address
// on its own because the shell example waits on the port with nc.
var (
	defaultSize = formatSize(smtp.DefaultMaxMessageBytes)
	defaultPort = portOf(smtp.DefaultAddr)
)

// portOf is the port half of a host:port address, or the whole of it if it
// somehow has no port to take.
func portOf(addr string) string {
	if _, port, err := net.SplitHostPort(addr); err == nil {
		return port
	}
	return addr
}

var rootHelp = fmt.Sprintf(`mailtui — a local SMTP catcher for development.

Usage:
  mailtui [flags]                catch mail and show it in the terminal inbox
  mailtui wait [flags]           wait for a matching message to arrive
  mailtui assert [flags]         wait for a message and check what it says
  mailtui extract otp [flags]    print the one-time code from a message
  mailtui extract link [flags]   print a link from a message
  mailtui version                print the version
  mailtui help                   print this

Examples:
  mailtui
  mailtui --smtp-addr 127.0.0.1:2525
  mailtui wait --to user@example.test --subject "Reset"
  OTP=$(mailtui extract otp --to user@example.test --timeout 5s)

Run with no arguments, mailtui listens on %[1]s and shows what it
catches in an interactive inbox; q or Ctrl-C leaves it. The other commands are
for scripts.

Catcher flags (every mode takes these):
  --smtp-addr host:port    where to catch mail (default %[1]s)
                           must be a loopback address
  --max-message-bytes size largest message to accept (default %[2]s)
                           bytes, or with a unit: 512KB, 10MB, 1GB
                           units are powers of 1024
  --max-recipients n       most recipients in one message (default %[3]d)

Process model:
  Captured mail lives in memory in the process that caught it; nothing is
  written to disk and there is no daemon. A script-mode command therefore
  cannot inspect a mailtui you already have running — there is nothing to
  inspect through. Each one starts its own catcher, waits, prints, and exits.

  So: start the command before your application sends, and do not run it while
  an interactive mailtui holds the port (or give it --smtp-addr with another
  loopback port). Mail a command caught is gone when it exits.

  In a test script:

    mailtui wait --to user@example.test --subject Reset --timeout 10s &
    waiter=$!
    until nc -z 127.0.0.1 %[4]s; do sleep 0.05; done   # the port is claimed
    ./trigger-password-reset
    wait $waiter                                      # its exit code is yours

Matching:
  Every flag that takes text is a case-insensitive substring test, and the
  flags are ANDed. --subject Welcome matches a subject of "Welcome back".
  There is no regular expression syntax and no exact-match mode.

Timeouts:
  Waiting commands stop after --timeout, which defaults to %[5]s. --timeout 0
  waits until interrupted. Ctrl-C stops any of them.

Exit codes:
  0    success, and quitting the inbox with q or Ctrl-C
  1    nothing matched in time, an assertion failed, nothing to extract, or
       the inbox stopped on an error of its own
  2    usage or configuration error: a bad flag, an address that is not
       loopback, a port already in use
  130  a waiting command was interrupted (Ctrl-C; SIGTERM on macOS/Linux)

Output:
  stdout carries the result and nothing else — no banners, no logs — so it can
  be captured in a shell variable. Everything else, including the line saying
  the catcher has claimed the port, goes to stderr.

Run "mailtui <command> --help" for a command's flags.
`, smtp.DefaultAddr, defaultSize, smtp.DefaultMaxRecipients, defaultPort, defaultTimeout)

// selectionHelp is the flag list shared by every script-mode command. The
// address flags come in three forms because mailtui keeps the SMTP envelope
// and the message headers apart, and a test may care which one carried an
// address.
const selectionHelp = `Selecting a message:
  --to string              envelope recipient or To header contains this
  --envelope-to string     SMTP recipient (RCPT TO) only
  --header-to string       To header only
  --from string            envelope sender or From header contains this
  --envelope-from string   SMTP sender (MAIL FROM) only
  --header-from string     From header only

  --to and --from match either side, which is what a test usually wants: the
  application knows the address it sent to, not which of the two carried it.
  The explicit forms are there for tests that are about the difference.
`

// commonHelp closes every command's flag list: the flags that are not about
// which message, and the one rule that governs all of them. The catcher flags
// are the same three interactive mode takes, with the same defaults.
var commonHelp = fmt.Sprintf(`Common flags:
  --timeout duration       how long to wait (default %s; 0 waits forever)
  --smtp-addr host:port    where to catch mail (default %s)
                           must be a loopback address
  --max-message-bytes size largest message to accept (default %s)
                           bytes, or with a unit: 512KB, 10MB, 1GB
  --max-recipients n       most recipients in one message (default %d)

All text matching is case-insensitive substring matching.
`, defaultTimeout, smtp.DefaultAddr, defaultSize, smtp.DefaultMaxRecipients)

var waitHelp = `mailtui wait — block until a matching message arrives.

Usage:
  mailtui wait --to john@example.test --subject "Reset password" --timeout 5s

Waits for the first message matching every flag given, including any that
arrived since this command started. With no matching flags at all it waits for
any message.

` + selectionHelp + `
Matching content:
  --subject string         Subject header contains this
  --contains string        the subject or either body contains this

` + commonHelp + `
Output:
  On success, one tab-separated line on stdout:

    <id>	<envelope-from>	<envelope-to,...>	<subject>

Exit codes:
  0    a message matched
  1    nothing matched before the timeout
  2    usage or configuration error
  130  interrupted
`

var assertHelp = `mailtui assert — wait for a message that says what it should.

Usage:
  mailtui assert --to john@example.test \
    --subject "Welcome" --contains "Verify your account" --timeout 5s

Waits for one message that satisfies every flag given — the addressing flags
and the assertions alike — and exits 0 when it arrives. A message that
satisfies only some of them is not a failure; it is simply not the message, and
assert keeps waiting. An application that sends a newsletter before the
password reset must not fail a test over the order the two arrived in.

The assertion fails when the timeout runs out with nothing having satisfied
everything. The report then says how much mail arrived, which criteria nothing
satisfied, and which message came closest and what it was missing. It does not
quote bodies back at you; the interactive inbox is where a message is read.

At least one of --subject or --contains is required — an assert with nothing to
assert is a test that cannot fail. To wait without checking, use mailtui wait.

` + selectionHelp + `
Asserting:
  --subject string         the Subject header must contain this
  --contains string        the subject or either body must contain this

  --contains searches the subject, the text body and the HTML body, so an
  assertion does not fail merely because the application sent HTML only. It
  does not search the raw message: a hit in transfer-encoded bytes would mean
  nothing. For a message that failed to parse, and so has no bodies at all, the
  raw payload is searched instead.

` + commonHelp + `
Output:
  Nothing on stdout, on any path. The exit code is the result, and a failure is
  explained on stderr.

Exit codes:
  0    a message arrived satisfying every criterion
  1    no such message arrived before the timeout
  2    usage or configuration error
  130  interrupted
`

const extractHelp = `mailtui extract — print one thing out of a matching message.

Usage:
  mailtui extract otp [flags]
  mailtui extract link [flags]

Examples:
  OTP=$(mailtui extract otp --to john@example.test --timeout 5s)
  URL=$(mailtui extract link --to john@example.test --timeout 5s)

Run "mailtui extract otp --help" or "mailtui extract link --help" for details.
`

var otpHelp = `mailtui extract otp — print the one-time code from a message.

Usage:
  OTP=$(mailtui extract otp --to john@example.test --timeout 5s)

Waits for a matching message and prints the code it appears to contain, and
nothing else, on stdout.

` + selectionHelp + `
Matching content:
  --subject string         Subject header contains this
  --contains string        the subject or either body contains this

` + commonHelp + `
The heuristic:
  The text read is the text body, or the HTML body flattened to text, or — for
  a message that failed to parse — the raw payload. Whitespace runs collapse to
  a single space first.

  A candidate is a run of 4, 5, 6 or 8 digits with no digit on either side.
  It is rejected if a letter or underscore touches it (id_123456, 1234px), if
  it is part of a longer number written with separators (2026-08-29, 1.234567,
  555-1234), if it sits inside an http(s) URL, or if it is four digits reading
  as a year from 1900 to 2099 — the copyright line in a footer being the most
  common false code there is.

  Of what survives, the winner is the candidate nearest one of the words a code
  is announced with (code, otp, pin, passcode, password, token, verification,
  verify, one-time, 2fa) within thirty characters either side; nearest first,
  earliest to break a tie. With no keyword near any candidate, the first
  candidate in the message wins.

  It is a guess, and it is the same guess every time for the same message.
  There is no model and no network anywhere in it.

Exit codes:
  0    a code was found and printed
  1    no message arrived in time, or the message held no code
  2    usage or configuration error
  130  interrupted
`

var linkHelp = `mailtui extract link — print a link from a message.

Usage:
  URL=$(mailtui extract link --to john@example.test --timeout 5s)

Waits for a matching message and prints one URL, and nothing else, on stdout.
Nothing is opened or fetched: the URL is printed and what happens to it is the
script's business.

` + selectionHelp + `
Matching content:
  --subject string         Subject header contains this
  --contains string        the subject or either body contains this

` + commonHelp + `
Which link:
  The first http or https URL in the text body. Failing that, the first http or
  https href in the HTML body, in document order. Failing that, the first one
  written as plain text in the HTML. For a message that failed to parse, the
  first one in the raw payload.

  Only http and https, and only with a host: mailto:, cid:, tel:, data: and
  javascript: URLs are skipped, as are relative links and anchors. Character
  references in an href are resolved, so a query string written with &amp;
  comes back usable.

  Trailing sentence punctuation is trimmed, so "visit https://x.test/a." gives
  up the full stop. A URL that really ends in punctuation loses it too, which
  is the cost of the far more common case.

Exit codes:
  0    a link was found and printed
  1    no message arrived in time, or the message held no link
  2    usage or configuration error
  130  interrupted
`
