// Command mailtui runs a local SMTP catcher for development.
//
// Run with no arguments it shows what it catches in a terminal inbox. Run with
// a command — wait, assert, extract — it answers a question about the mail an
// application under test sends, and exits with a code a script can act on. See
// package cli for the process model that separates the two, and for where
// signals are handled.
//
// A release build sets the version it reports:
//
//	go build -ldflags "-X github.com/igkougkousis01/mailtui/internal/version.Version=0.1.0" ./cmd/mailtui
package main

import (
	"context"
	"os"

	"github.com/igkougkousis01/mailtui/internal/cli"
	"github.com/igkougkousis01/mailtui/internal/interactive"
)

func main() {
	app := &cli.App{
		Stdout:      os.Stdout,
		Stderr:      os.Stderr,
		Interactive: interactive.Run,
	}

	// A plain background context: signal handling belongs to whichever mode
	// the arguments select, and installing a handler here would compete with
	// the one Bubble Tea needs to own in interactive mode.
	os.Exit(app.Run(context.Background(), os.Args[1:]))
}
