// Package interactive wires together the two halves of plain `mailtui`: the
// SMTP catcher that fills the store, and the terminal inbox that reads it.
//
// It is a package rather than a few lines in main so that the lifecycle — bind
// before drawing, stop the catcher after quitting, report the right failure of
// the two that can happen at once — can be tested without a terminal.
//
// # Lifecycle
//
// The port is claimed before the TUI starts. That ordering is the whole reason
// this is written out rather than started in parallel: a bind failure is by
// far the most likely way an interactive run goes wrong, and it must be a line
// on an ordinary terminal, not an error painted onto an alternate screen that
// is being torn down at the same moment.
//
// After that the two run together. The TUI holds the main goroutine and the
// terminal; the catcher accepts mail beside it. Whichever finishes first ends
// the other: quitting the inbox returns and stops the catcher, and a catcher
// that dies cancels the context the program runs under, which returns the
// terminal to how it was found before anything is printed about why.
//
// # Signals
//
// None are handled here. Bubble Tea owns the platform's console interruption
// handling and is the only thing that can restore the terminal, so it keeps
// it. On macOS and Linux that includes SIGINT and SIGTERM; on Windows it covers
// normal console interruption. See the cli package comment on signal ownership.
package interactive

import (
	"context"
	"errors"

	tea "charm.land/bubbletea/v2"

	"github.com/igkougkousis01/mailtui/internal/smtp"
	"github.com/igkougkousis01/mailtui/internal/store"
	"github.com/igkougkousis01/mailtui/internal/tui"
)

// Run catches mail as cfg says to and shows it in the terminal inbox, blocking
// until the user quits.
//
// A failure to claim the port comes back as a *smtp.ListenError and nothing
// has been drawn; any other error means the run started and then stopped.
func Run(ctx context.Context, cfg smtp.Config) error {
	return run(ctx, cfg, tui.Run)
}

// ui is the terminal inbox, as an argument so that the lifecycle around it can
// be tested without one. tui.Run is the only real implementation.
type ui func(ctx context.Context, st *store.Store, smtpAddr string) error

func run(ctx context.Context, cfg smtp.Config, show ui) error {
	messages := store.New()

	// Bind first. See the package comment: nothing has been drawn yet, so a
	// failure here is an ordinary error on an ordinary terminal.
	server, err := smtp.Listen(cfg, messages, nil)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	// Buffered so the goroutine can report and exit even if the user quits at
	// the same moment and nobody ever reads this.
	serverErr := make(chan error, 1)
	go func() {
		// A nil log writer: Bubble Tea owns the terminal, and the per-message
		// notes the catcher would otherwise print would be written straight
		// over the rendered screen. The TUI is the log now.
		serverErr <- server.Serve()

		// A catcher that has stopped leaves an inbox that will never show
		// anything again, so it ends the program rather than being left to be
		// discovered.
		cancel()
	}()

	// The address from the listener, not from the configuration: a --smtp-addr
	// ending in :0 is a real port by now, and the header should say which one.
	showErr := show(ctx, messages, server.Addr().String())

	// Stop before anything is reported, so the port is released and the
	// sessions are done however this run ended. The grace lets a delivery
	// already under way be acknowledged; see smtp.Server.Stop. Serve then
	// returns, which is what the receive waits for: nothing of ours is still
	// running past this line.
	server.Stop(smtp.StopGrace)
	serveErr := <-serverErr

	// The catcher failing is the more useful thing to report: it is why the
	// inbox stopped, rather than something that went wrong in the inbox.
	if serveErr != nil {
		return serveErr
	}

	if errors.Is(showErr, tea.ErrInterrupted) {
		// Ctrl-C. The user asked to leave, and Bubble Tea has already put the
		// terminal back. That is not a failure.
		return nil
	}
	return showErr
}
