// Command mailtui runs a local SMTP catcher for development and shows what it
// catches in a terminal inbox.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"

	tea "charm.land/bubbletea/v2"

	"github.com/igkougkousis01/mailtui/internal/smtp"
	"github.com/igkougkousis01/mailtui/internal/store"
	"github.com/igkougkousis01/mailtui/internal/tui"
)

// addr is loopback-only on purpose: the catcher accepts mail without
// authentication and must not be reachable from the network.
const addr = "127.0.0.1:1025"

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "mailtui: %v\n", err)
		os.Exit(1)
	}
}

// run wires the two halves of the program together: the SMTP server fills the
// store, the TUI reads it.
//
// Lifecycle, in the smallest shape that is correct. The TUI runs on the main
// goroutine and owns the terminal; the server runs beside it for as long as
// the process does. Quitting the TUI returns from run and from main, which
// ends the process and the listener with it — there is no state to flush and
// nothing to persist, so a graceful SMTP shutdown would buy nothing.
//
// The other direction does need handling: a server that cannot start, or that
// dies, leaves a TUI that will never show anything. Cancelling the context
// stops the program, restores the terminal, and lets the real error be
// reported on a clean screen.
func run() error {
	messages := store.New()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Buffered so the server goroutine can report and exit even if the user
	// quits at the same moment and nobody ever reads this.
	serverErr := make(chan error, 1)
	go func() {
		// A nil log writer: Bubble Tea owns the terminal, and the per-message
		// notes the server would otherwise print would be written straight
		// over the rendered screen. The TUI is the log now.
		serverErr <- smtp.ListenAndServe(addr, messages, nil)
		cancel()
	}()

	runErr := tui.Run(ctx, messages, addr)

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
