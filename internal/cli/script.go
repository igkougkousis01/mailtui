package cli

import (
	"context"
	"flag"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/igkougkousis01/mailtui/internal/match"
	"github.com/igkougkousis01/mailtui/internal/message"
	"github.com/igkougkousis01/mailtui/internal/smtp"
	"github.com/igkougkousis01/mailtui/internal/store"
	"github.com/igkougkousis01/mailtui/internal/wait"
)

// defaultTimeout bounds a command that would otherwise wait forever. A finite
// default is the right one for a tool that lives in test scripts: a build that
// fails after thirty seconds is better than one that hangs until someone
// notices. --timeout 0 asks for the other behaviour explicitly.
const defaultTimeout = 30 * time.Second

// options is what every script-mode command needs to know: which message, how
// long to wait for it, and what sort of catcher to run.
type options struct {
	selection match.Criteria
	timeout   time.Duration
	smtp      smtp.Config
}

// bindSelection binds the flags that say which message the command is about.
func bindSelection(fs *flag.FlagSet, c *match.Criteria) {
	fs.StringVar(&c.To, "to", "", "select a message whose envelope or To header contains this")
	fs.StringVar(&c.EnvelopeTo, "envelope-to", "", "select on the SMTP recipient (RCPT TO) only")
	fs.StringVar(&c.HeaderTo, "header-to", "", "select on the To header only")
	fs.StringVar(&c.From, "from", "", "select a message whose envelope or From header contains this")
	fs.StringVar(&c.EnvelopeFrom, "envelope-from", "", "select on the SMTP sender (MAIL FROM) only")
	fs.StringVar(&c.HeaderFrom, "header-from", "", "select on the From header only")
}

// bindContent binds the flags about what the message says. They are separate
// from bindSelection only because assert has to be able to tell whether it was
// given an assertion at all; once past that check every command treats the two
// halves as one set of criteria.
func bindContent(fs *flag.FlagSet, c *match.Criteria) {
	fs.StringVar(&c.Subject, "subject", "", "substring of the Subject header")
	fs.StringVar(&c.Contains, "contains", "", "substring of the subject or of either body")
}

// bindCommon binds the flags every script-mode command shares: the wait, and
// the catcher itself. The listener flags come from bindSMTP, the same function
// interactive mode uses, so the two modes cannot end up with different
// defaults for the same flag.
func bindCommon(fs *flag.FlagSet, o *options) {
	fs.DurationVar(&o.timeout, "timeout", defaultTimeout, "how long to wait; 0 waits until interrupted")

	o.smtp = smtp.DefaultConfig()
	bindSMTP(fs, &o.smtp)
}

// catch runs a catcher configured by o.smtp and returns the first message
// matching o.selection.
//
// The second result is the exit code, ExitOK when a message was found. A
// non-zero code has already been explained on stderr: there is one way each of
// these can go wrong and one sentence to say about each, and threading an
// error type through three commands to reach the same three sentences would be
// ceremony.
func (a *App) catch(ctx context.Context, o options) (message.Message, int) {
	if err := validateSMTP(o.smtp); err != nil {
		a.errf("%v", err)
		return message.Message{}, ExitUsage
	}

	messages := store.New()

	// A nil log writer. The catcher's running commentary would land on stderr
	// next to the diagnostics, and a script that captures stdout does not want
	// to read about every connection on the way past.
	server, err := smtp.Listen(o.smtp, messages, nil)
	if err != nil {
		a.errf("%v", err)
		a.listenHint(err)
		return message.Message{}, ExitUsage
	}

	// Buffered so the server goroutine can report and exit even if nothing
	// reads this, which is what happens when the wait ends first.
	serverErr := make(chan error, 1)
	go func() {
		serverErr <- server.Serve()
	}()
	defer func() {
		// Stop lets a delivery already under way be acknowledged before the
		// listener goes away, then cuts off anything still open, which is what
		// ends the goroutines behind those sessions; the receive then waits
		// for Serve to return. Nothing of ours is still running past this
		// point.
		server.Stop(smtp.StopGrace)
		<-serverErr
	}()

	// Told on stderr, because a command that is waiting should say what it is
	// waiting on, and because it is the moment the port is actually claimed.
	fmt.Fprintf(a.Stderr, "mailtui: catching SMTP on %s\n", server.Addr())

	waitCtx := ctx
	if o.timeout > 0 {
		var cancel context.CancelFunc
		waitCtx, cancel = context.WithTimeout(ctx, o.timeout)
		defer cancel()
	}

	msg, err := wait.Message(waitCtx, messages, o.selection)
	if err == nil {
		return msg, ExitOK
	}

	// A dead server is the more useful thing to report: it is why nothing
	// arrived, rather than something about the mail.
	select {
	case serr := <-serverErr:
		// Put it back for the deferred receive, which would otherwise block.
		serverErr <- serr
		if serr != nil {
			a.errf("the catcher stopped: %v", serr)
			return message.Message{}, ExitUsage
		}
	default:
	}

	if ctx.Err() != nil {
		// SIGINT or SIGTERM; see the package comment on signal ownership.
		a.errf("interrupted while waiting for %s", describe(o.selection))
		return message.Message{}, ExitInterrupted
	}

	a.errf("timed out after %s waiting for %s", o.timeout, describe(o.selection))
	a.reportObserved(oldestFirst(messages.List()), o.selection)
	return message.Message{}, ExitFailure
}

// oldestFirst puts a store listing back into arrival order, which is the order
// a person reading a report about it is thinking in.
func oldestFirst(list []message.Message) []message.Message {
	slices.Reverse(list)
	return list
}

// reportObserved explains a timeout from the mail that did arrive: how much of
// it there was, which criteria nothing satisfied, and the message that came
// nearest, with what it was missing.
//
// It is built from the same Failures the wait itself used, so it can never
// describe a different match than the one that did not happen. What it does
// not do is quote the message back: bodies and raw MIME belong in the
// interactive inbox, not in the diagnostics of a test run.
func (a *App) reportObserved(msgs []message.Message, c match.Criteria) {
	if len(msgs) == 0 {
		a.errf("no messages arrived")
		return
	}

	// Which criteria some message satisfied, and which message came closest.
	// Failures says what one message got wrong; neither of those questions can
	// be answered from a single message, which is why they are gathered here.
	satisfied := make(map[string]bool)
	var closest message.Message
	var closestFailures []match.Failure
	fewest := -1

	for _, msg := range msgs {
		failures := c.Failures(msg)

		failed := make(map[string]bool, len(failures))
		for _, f := range failures {
			failed[f.Field] = true
		}
		for _, field := range c.Fields() {
			if !failed[field] {
				satisfied[field] = true
			}
		}

		// Fewest failures wins, and an earlier message keeps the title against
		// a later one that ties: the report is then the same every run.
		if fewest < 0 || len(failures) < fewest {
			fewest, closest, closestFailures = len(failures), msg, failures
		}
	}

	a.errf("%s arrived, none matching", quantity(len(msgs), "message"))

	var never []string
	for _, field := range c.Fields() {
		if !satisfied[field] {
			never = append(never, "--"+field)
		}
	}
	if len(never) > 0 {
		a.errf("no message satisfied: %s", strings.Join(never, " "))
	}

	if len(closestFailures) == 0 {
		// Unreachable while a match ends the wait, but a report that quietly
		// contradicted itself would be worse than one that says so.
		return
	}

	a.errf("closest was message %s to %s (subject %q)",
		closest.ID, oneLine(strings.Join(closest.EnvelopeTo, ", ")), truncate(oneLine(closest.Subject)))
	for _, f := range closestFailures {
		fmt.Fprintf(a.Stderr, "  %s\n", explain(f))
	}
}

// quantity renders a count with its noun, pluralised.
func quantity(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

// describe renders criteria the way the flags that set them were written, for
// the one line that says what the command was waiting for.
func describe(c match.Criteria) string {
	var parts []string
	add := func(flag, value string) {
		if value != "" {
			parts = append(parts, fmt.Sprintf("--%s %q", flag, value))
		}
	}

	add("to", c.To)
	add("envelope-to", c.EnvelopeTo)
	add("header-to", c.HeaderTo)
	add("from", c.From)
	add("envelope-from", c.EnvelopeFrom)
	add("header-from", c.HeaderFrom)
	add("subject", c.Subject)
	add("contains", c.Contains)

	if len(parts) == 0 {
		return "any message"
	}
	return "a message matching " + strings.Join(parts, " ")
}

// oneLine flattens a header value so it cannot break the format of a line
// printed about it. A subject is folded across lines by the sender often
// enough that this matters.
func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// maxQuoted is how much of a header value a diagnostic will quote. A subject
// is written by whoever sent the mail and can be any length; a diagnostic line
// is read in a test log and should stay one.
const maxQuoted = 120

// truncate shortens a value to something a report can carry.
func truncate(s string) string {
	runes := []rune(s)
	if len(runes) <= maxQuoted {
		return s
	}
	return string(runes[:maxQuoted]) + "…"
}
