// Command mailtui runs a local SMTP catcher for development.
package main

import (
	"log"
	"os"

	"github.com/igkougkousis01/mailtui/internal/smtp"
)

// addr is loopback-only on purpose: the catcher accepts mail without
// authentication and must not be reachable from the network.
const addr = "127.0.0.1:1025"

func main() {
	if err := smtp.ListenAndServe(addr, os.Stdout); err != nil {
		log.Fatalf("mailtui: %v", err)
	}
}
