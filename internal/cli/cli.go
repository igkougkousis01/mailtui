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
package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"strings"
)

// DefaultSMTPAddr is where the catcher listens unless told otherwise. It is
// loopback-only on purpose: the catcher accepts mail without authentication
// and must not be reachable from the network.
const DefaultSMTPAddr = "127.0.0.1:1025"

// Exit codes. They are the interface a script actually consumes, so they are
// fixed and few.
const (
	// ExitOK means the command did what it was asked.
	ExitOK = 0
	// ExitFailure means the answer was no: nothing matched before the
	// timeout, an assertion did not hold, or there was nothing to extract.
	// This is the code a test turns into a failed build.
	ExitFailure = 1
	// ExitUsage means the command could not run: an unknown flag, an
	// unparseable value, an address that is not loopback, a port already in
	// use. Distinct from ExitFailure because a broken invocation and a failed
	// assertion call for different reactions.
	ExitUsage = 2
	// ExitInterrupted means Ctrl-C. 128+SIGINT, as a shell reports it, so a
	// script can tell "I stopped this" from "this failed".
	ExitInterrupted = 130
)

// App is one run of the command line. The writers and the interactive entry
// point are fields so that a test can run the whole command line in-process
// and read back exactly what each stream received.
type App struct {
	Stdout io.Writer
	Stderr io.Writer

	// Interactive launches the SMTP catcher and the terminal inbox, and
	// returns when the user quits. It is what plain `mailtui` runs.
	Interactive func(ctx context.Context) error
}

// Run executes args, which are the arguments after the program name, and
// returns the process exit code.
func (a *App) Run(ctx context.Context, args []string) int {
	if len(args) == 0 {
		return a.interactive(ctx)
	}

	switch args[0] {
	case "wait":
		return a.waitCommand(ctx, args[1:])
	case "assert":
		return a.assertCommand(ctx, args[1:])
	case "extract":
		return a.extractCommand(ctx, args[1:])
	case "help", "-h", "-help", "--help":
		fmt.Fprint(a.Stdout, rootHelp)
		return ExitOK
	default:
		a.errf("unknown command %q", args[0])
		a.errf(`run "mailtui help" for the commands it has`)
		return ExitUsage
	}
}

// interactive runs the inbox, turning its error into an exit code.
func (a *App) interactive(ctx context.Context) int {
	if a.Interactive == nil {
		a.errf("interactive mode is not available in this build")
		return ExitUsage
	}

	if err := a.Interactive(ctx); err != nil {
		a.errf("%v", err)
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

// newFlagSet returns a flag set that reports its errors on stderr and prints
// nothing else: the help these commands give is written by hand, not generated
// from the flag definitions, because it has to explain matching and output as
// well as list flags.
func (a *App) newFlagSet(name string) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(a.Stderr)
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

// checkLoopback rejects an address the catcher must not listen on.
//
// The catcher takes any mail offered to it, from anyone, with no
// authentication — which is exactly what makes it useful and exactly why it
// belongs on the loopback interface only. --smtp-addr exists to change the
// port, not to publish the catcher, so an address that would accept
// connections from the network is refused rather than trusted to be
// deliberate.
func checkLoopback(addr string) error {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("--smtp-addr %q is not a host:port address: %w", addr, err)
	}

	if host == "" {
		return fmt.Errorf("--smtp-addr %q would listen on every interface; use a loopback address such as %s", addr, DefaultSMTPAddr)
	}
	if strings.EqualFold(host, "localhost") {
		return nil
	}
	if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
		return nil
	}
	return fmt.Errorf("--smtp-addr %q is not a loopback address; the catcher accepts mail without authentication and must not be reachable from the network", addr)
}
