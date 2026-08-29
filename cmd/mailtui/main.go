// Command mailtui runs a local SMTP catcher for development.
//
// Run with no arguments it shows what it catches in a terminal inbox. Run with
// a command — wait, assert, extract — it answers a question about the mail an
// application under test sends, and exits with a code a script can act on. See
// package cli for the process model that separates the two.
package main

import (
	"context"
	"errors"
	"os"
	"os/signal"

	tea "charm.land/bubbletea/v2"

	"github.com/igkougkousis01/mailtui/internal/cli"
	"github.com/igkougkousis01/mailtui/internal/smtp"
	"github.com/igkougkousis01/mailtui/internal/store"
	"github.com/igkougkousis01/mailtui/internal/tui"
)

func main() {
	// Ctrl-C has to reach a waiting command, which is otherwise blocked on
	// mail that may never come. The interactive inbox handles its own
	// interrupt through Bubble Tea and is unaffected by this: the context it
	// is given being cancelled just ends the program, which is what the key
	// press meant anyway.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	app := &cli.App{
		Stdout:      os.Stdout,
		Stderr:      os.Stderr,
		Interactive: runInteractive,
	}

	os.Exit(app.Run(ctx, os.Args[1:]))
}

// runInteractive wires the two halves of the interactive program together: the
// SMTP server fills the store, the TUI reads it.
//
// Lifecycle, in the smallest shape that is correct. The TUI runs on the main
// goroutine and owns the terminal; the server runs beside it for as long as
// the process does. Quitting the TUI returns from here and from main, which
// ends the process and the listener with it — there is no state to flush and
// nothing to persist, so a graceful SMTP shutdown would buy nothing.
//
// The other direction does need handling: a server that cannot start, or that
// dies, leaves a TUI that will never show anything. Cancelling the context
// stops the program, restores the terminal, and lets the real error be
// reported on a clean screen.
func runInteractive(ctx context.Context) error {
	messages := store.New()

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	// Buffered so the server goroutine can report and exit even if the user
	// quits at the same moment and nobody ever reads this.
	serverErr := make(chan error, 1)
	go func() {
		// A nil log writer: Bubble Tea owns the terminal, and the per-message
		// notes the server would otherwise print would be written straight
		// over the rendered screen. The TUI is the log now.
		serverErr <- smtp.ListenAndServe(cli.DefaultSMTPAddr, messages, nil)
		cancel()
	}()

	runErr := tui.Run(ctx, messages, cli.DefaultSMTPAddr)

	// The server failing is the more useful thing to report: it is why the TUI
	// stopped, rather than something that went wrong in the TUI itself.
	select {
	case err := <-serverErr:
		return err
	default:
	}

	if errors.Is(runErr, tea.ErrInterrupted) {
		// SIGINT. The user asked to leave; that is not a failure.
		return nil
	}
	return runErr
}
