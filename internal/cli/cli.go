// Package cli is mailtui's command line: the interactive inbox when it is run
// with no arguments, and the script-mode commands when it is not.
//
// The commands exist so that mailtui is useful to a test as well as to a
// person. A test wants to say "an email should have arrived, and it should
// have said this", and get an exit code back.
//
// # Process model
//
// Captured mail lives in memory in the process that caught it. There is no
// database, no daemon and no control socket, which is deliberate: the whole
// tool is one binary you run and stop, and the messages are meant to die with
// it.
//
// That has a consequence the commands cannot hide. `mailtui wait` in one shell
// cannot look inside a `mailtui` you already have running in another — there
// is nothing to look through. So each script-mode command starts its own
// catcher on the SMTP port, waits for what it was asked about, prints, and
// exits.
//
// What that costs, stated plainly rather than papered over:
//
//   - The command must be running before the application sends. A script
//     starts it in the background, waits for the port, then triggers the mail.
//   - It cannot share the port with an interactive mailtui, or with another
//     script-mode command. Stop the other one, or pass --smtp-addr.
//   - Mail it catches is gone when it exits. Each command sees the mail that
//     arrived during its own run, and nothing else.
//
// The alternative — a control socket on a running instance — would make those
// go away and bring a protocol, a daemon lifetime, a socket to find and clean
// up, and an unauthenticated endpoint on the developer's machine. For the
// thing being bought, which is an exit code in a test script, the one-shot
// catcher is the smaller and the more honest design.
//
// # Signal ownership
//
// SIGINT and SIGTERM are handled in exactly one place per mode, and the two
// places never overlap.
//
// A script-mode command is interrupted through the context Run installs around
// it, and nothing below it registers a handler of its own: the command is
// usually blocked on mail that may never arrive, and cancelling the wait is
// the whole of what stopping it means.
//
// Interactive mode installs nothing. Bubble Tea has its own handler for both
// signals and must keep it, because it is the only thing that can put the
// terminal back — leave the alternate screen, restore the cursor, unset raw
// mode — before the process goes. A second handler racing it is how a terminal
// ends up wrecked after a Ctrl-C.
package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/igkougkousis01/mailtui/internal/smtp"
	"github.com/igkougkousis01/mailtui/internal/version"
)

// Exit codes. They are the interface a script actually consumes, so they are
// fixed and few.
const (
	// ExitOK means the command did what it was asked.
	ExitOK = 0
	// ExitFailure means the answer was no: nothing matched before the
	// timeout, an assertion did not hold, or there was nothing to extract. It
	// is also what an interactive session that died on its own reports. This
	// is the code a test turns into a failed build.
	ExitFailure = 1
	// ExitUsage means the command could not run: an unknown flag, an
	// unparseable value, an address that is not loopback, a port already in
	// use. Distinct from ExitFailure because a broken invocation and a failed
	// assertion call for different reactions.
	ExitUsage = 2
	// ExitInterrupted means the run was stopped by a signal — Ctrl-C, or a
	// SIGTERM from whatever supervises the script. 128+SIGINT, as a shell
	// reports it, so a script can tell "I stopped this" from "this failed".
	ExitInterrupted = 130
)

// App is one run of the command line. The writers and the interactive entry
// point are fields so that a test can run the whole command line in-process
// and read back exactly what each stream received.
type App struct {
	Stdout io.Writer
	Stderr io.Writer

	// Interactive launches the SMTP catcher and the terminal inbox, and
	// returns when the user quits. It is what plain `mailtui` runs. The
	// configuration has already been validated when it is called.
	Interactive func(ctx context.Context, cfg smtp.Config) error
}

// Run executes args, which are the arguments after the program name, and
// returns the process exit code.
//
// Anything that does not begin with a dash is a command; everything else is
// interactive mode and its flags, which is what makes plain `mailtui` and
// `mailtui --smtp-addr :2525` the same invocation with the same meaning.
func (a *App) Run(ctx context.Context, args []string) int {
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		return a.interactive(ctx, args)
	}

	switch args[0] {
	case "wait", "assert", "extract":
		// The one place a script-mode command's signals are handled; see the
		// package comment. Interactive mode is deliberately not routed
		// through here.
		ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
		defer stop()

		switch args[0] {
		case "wait":
			return a.waitCommand(ctx, args[1:])
		case "assert":
			return a.assertCommand(ctx, args[1:])
		default:
			return a.extractCommand(ctx, args[1:])
		}
	case "version":
		fmt.Fprintln(a.Stdout, version.String())
		return ExitOK
	case "help":
		fmt.Fprint(a.Stdout, rootHelp)
		return ExitOK
	default:
		a.errf("unknown command %q", args[0])
		a.errf(`run "mailtui help" for the commands it has`)
		return ExitUsage
	}
}

// interactive parses the flags plain `mailtui` takes and runs the inbox.
//
// The configuration is checked here, before the catcher binds and long before
// the terminal is taken over, so that a bad flag is a line on stderr rather
// than a message painted onto a screen that is about to be torn down.
func (a *App) interactive(ctx context.Context, args []string) int {
	fs := a.newFlagSet("mailtui")

	cfg := smtp.DefaultConfig()
	bindSMTP(fs, &cfg)
	showVersion := fs.Bool("version", false, "print the version and exit")

	if code, ok := a.parseRoot(fs, args); !ok {
		return code
	}
	if *showVersion {
		fmt.Fprintln(a.Stdout, version.String())
		return ExitOK
	}

	if err := validateSMTP(cfg); err != nil {
		a.errf("%v", err)
		return ExitUsage
	}

	if a.Interactive == nil {
		a.errf("interactive mode is not available in this build")
		return ExitUsage
	}

	if err := a.Interactive(ctx, cfg); err != nil {
		a.errf("%v", err)
		a.listenHint(err)

		// A port that is taken or an address that is refused is a
		// configuration error whichever mode hit it, and a script that runs
		// mailtui unattended should be able to tell it from the inbox dying
		// for some other reason.
		var listenErr *smtp.ListenError
		if errors.As(err, &listenErr) {
			return ExitUsage
		}
		return ExitFailure
	}
	return ExitOK
}

// errf writes one diagnostic line to stderr. Everything this package tells a
// human goes through here, which is what keeps stdout clean enough to capture
// in a shell variable.
func (a *App) errf(format string, args ...any) {
	fmt.Fprintf(a.Stderr, "mailtui: "+format+"\n", args...)
}

// hintf writes a follow-up line suggesting what to do about the error just
// reported. It is used sparingly and only where there is a specific thing to
// try; a hint on every error is noise that teaches the reader to skip the
// line under the one that matters.
func (a *App) hintf(format string, args ...any) {
	fmt.Fprintf(a.Stderr, "hint: "+format+"\n", args...)
}

// listenHint adds the one useful sentence about a failure to bind. The two
// cases below are the ones a developer actually hits; everything else is left
// to speak for itself.
func (a *App) listenHint(err error) {
	var listenErr *smtp.ListenError
	if !errors.As(err, &listenErr) {
		return
	}
	switch {
	case errors.Is(err, syscall.EADDRINUSE):
		a.hintf("another mailtui may already be listening there; stop it, or pass --smtp-addr with a free loopback port")
	case errors.Is(err, syscall.EACCES):
		a.hintf("ports below 1024 need privileges; pass --smtp-addr with a higher port")
	}
}

// newFlagSet returns a flag set that prints nothing of its own.
//
// Its errors are reported by the caller instead, so that they carry the same
// "mailtui:" prefix as every other diagnostic; a stray line in the flag
// package's voice in the middle of ours is the kind of seam that makes a tool
// feel assembled rather than written. The help is hand-written for the same
// reason it is not generated from the flag definitions: it has to explain
// matching and output as well as list flags.
func (a *App) newFlagSet(name string) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.Usage = func() {}
	return fs
}

// parse reads args into fs. The second result is false when the command should
// stop, with the returned code — which is ExitOK when the user asked for help
// and got it.
func (a *App) parse(fs *flag.FlagSet, args []string, help string) (int, bool) {
	err := fs.Parse(args)
	if errors.Is(err, flag.ErrHelp) {
		fmt.Fprint(a.Stdout, help)
		return ExitOK, false
	}
	if err != nil {
		a.errf("%v", err)
		a.errf(`run "mailtui %s --help" for the flags it takes`, fs.Name())
		return ExitUsage, false
	}
	if fs.NArg() > 0 {
		a.errf("unexpected argument %q", fs.Arg(0))
		a.errf(`run "mailtui %s --help" for the flags it takes`, fs.Name())
		return ExitUsage, false
	}
	return ExitOK, true
}

// parseRoot is parse for interactive mode, where a leftover argument is a
// mistyped command rather than a stray value.
func (a *App) parseRoot(fs *flag.FlagSet, args []string) (int, bool) {
	err := fs.Parse(args)
	if errors.Is(err, flag.ErrHelp) {
		fmt.Fprint(a.Stdout, rootHelp)
		return ExitOK, false
	}
	if err != nil {
		a.errf("%v", err)
		a.errf(`run "mailtui --help" for the flags it takes`)
		return ExitUsage, false
	}
	if fs.NArg() > 0 {
		a.errf("unknown command %q", fs.Arg(0))
		a.errf(`run "mailtui help" for the commands it has`)
		return ExitUsage, false
	}
	return ExitOK, true
}
