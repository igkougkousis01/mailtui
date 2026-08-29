// Command mailtui runs a local SMTP catcher for development.
package main

import (
	"log"
	"os"

	"github.com/igkougkousis01/mailtui/internal/smtp"
	"github.com/igkougkousis01/mailtui/internal/store"
)

// addr is loopback-only on purpose: the catcher accepts mail without
// authentication and must not be reachable from the network.
const addr = "127.0.0.1:1025"

func main() {
	// The store is created here and injected, so that the TUI and CLI commands
	// added later share this one rather than reaching for a global.
	messages := store.New()

	if err := smtp.ListenAndServe(addr, messages, os.Stdout); err != nil {
		log.Fatalf("mailtui: %v", err)
	}
}
