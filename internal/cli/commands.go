package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/igkougkousis01/mailtui/internal/extract"
	"github.com/igkougkousis01/mailtui/internal/match"
	"github.com/igkougkousis01/mailtui/internal/message"
)

// waitCommand blocks until a message matching the flags arrives.
//
// On success it prints one tab-separated line to stdout: the message ID, the
// envelope sender, the envelope recipients joined by commas, and the subject.
// A script that only wants the exit code can ignore it; one that wants to know
// what it caught can cut a field out of it.
func (a *App) waitCommand(ctx context.Context, args []string) int {
	fs := a.newFlagSet("wait")

	var o options
	bindSelection(fs, &o.selection)
	bindContent(fs, &o.selection)
	bindCommon(fs, &o)

	if code, ok := a.parse(fs, args, waitHelp); !ok {
		return code
	}

	msg, code := a.catch(ctx, o)
	if code != ExitOK {
		return code
	}

	fmt.Fprintf(a.Stdout, "%s\t%s\t%s\t%s\n",
		msg.ID, oneLine(msg.EnvelopeFrom), oneLine(strings.Join(msg.EnvelopeTo, ",")), oneLine(msg.Subject))
	return ExitOK
}

// assertCommand waits for a message that satisfies everything it was asked
// about, and says so with its exit code.
//
// Every criterion counts towards the match, the addressing flags included:
// assert waits for one message that is right in all of them, and a message
// that is right in only some is not a failure, it is just not the message.
// That is what a test needs from it. An application under test sends a
// newsletter and then the password reset; an assert that judged the first
// thing addressed to the right person and gave up would fail a build over the
// order two emails happened to arrive in.
//
// So the whole of assert is a wait with something required of the message and
// nothing printed when it is found. What separates it from wait is what it
// refuses — an assertion that cannot fail — and what it says when the wait
// runs out; see reportObserved.
func (a *App) assertCommand(ctx context.Context, args []string) int {
	fs := a.newFlagSet("assert")

	var o options
	// Bound separately only so that the next check can tell the two halves
	// apart. Once past it they are one set of criteria, because the match has
	// to be all of them at once.
	var want match.Criteria
	bindSelection(fs, &o.selection)
	bindContent(fs, &want)
	bindCommon(fs, &o)

	if code, ok := a.parse(fs, args, assertHelp); !ok {
		return code
	}

	if want.Empty() {
		// An assert with nothing to assert would pass on any mail at all,
		// which is a test that cannot fail. That is a mistake worth naming.
		a.errf("assert needs something to check: pass --subject or --contains")
		a.errf(`to wait for a message without checking it, use "mailtui wait"`)
		return ExitUsage
	}
	o.selection.Subject = want.Subject
	o.selection.Contains = want.Contains

	// Silence on success, as a check should be: the exit code is the result,
	// and stdout stays free for whatever the script is really collecting. The
	// failure has already been explained on stderr by the time this returns.
	_, code := a.catch(ctx, o)
	return code
}

// explain turns one unmet criterion into the line a developer reads when the
// build goes red.
func explain(f match.Failure) string {
	if f.Field == "contains" {
		return fmt.Sprintf("contains: %q is not in the subject or either body", f.Want)
	}
	return fmt.Sprintf("%s: want %q, got %q", f.Field, f.Want, oneLine(f.Got))
}

// extractCommand dispatches the extract subcommands.
func (a *App) extractCommand(ctx context.Context, args []string) int {
	if len(args) == 0 {
		a.errf("extract needs a subcommand: otp or link")
		return ExitUsage
	}

	switch args[0] {
	case "otp":
		return a.extractRun(ctx, "extract otp", args[1:], otpHelp, "one-time code", extract.OTP)
	case "link":
		return a.extractRun(ctx, "extract link", args[1:], linkHelp, "link", extract.Link)
	case "-h", "-help", "--help", "help":
		fmt.Fprint(a.Stdout, extractHelp)
		return ExitOK
	default:
		a.errf("unknown extract subcommand %q; there are otp and link", args[0])
		return ExitUsage
	}
}

// extractRun is both extract subcommands. They differ only in what they look
// for and what they call it when it is not there, so find and noun are the
// whole of the difference.
//
// The extracted value is the only thing on stdout, with a trailing newline and
// nothing else, so that OTP=$(mailtui extract otp ...) holds the code.
func (a *App) extractRun(
	ctx context.Context,
	name string,
	args []string,
	help string,
	noun string,
	find func(message.Message) (string, bool),
) int {
	fs := a.newFlagSet(name)

	var o options
	bindSelection(fs, &o.selection)
	bindContent(fs, &o.selection)
	bindCommon(fs, &o)

	if code, ok := a.parse(fs, args, help); !ok {
		return code
	}

	msg, code := a.catch(ctx, o)
	if code != ExitOK {
		return code
	}

	value, ok := find(msg)
	if !ok {
		// The message is named, because "no code found" is nearly useless
		// without knowing which mail was read.
		a.errf("no %s found in message %s to %s (subject %q)",
			noun, msg.ID, oneLine(strings.Join(msg.EnvelopeTo, ", ")), oneLine(msg.Subject))
		return ExitFailure
	}

	fmt.Fprintln(a.Stdout, value)
	return ExitOK
}
