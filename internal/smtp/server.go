// Package smtp implements the local SMTP catcher that receives mail from
// applications under development.
//
// It owns the SMTP conversation and the envelope it carries. Everything about
// the content of a message belongs to the message package: this package reads
// the DATA payload and hands the raw bytes over unchanged.
package smtp

import (
	"fmt"
	"io"
	"net"
	"strings"
	"time"

	gosmtp "github.com/emersion/go-smtp"

	"github.com/igkougkousis01/mailtui/internal/message"
)

const (
	// maxMessageBytes caps a single message so a runaway sender cannot
	// exhaust memory. 25 MB matches what most real providers accept.
	maxMessageBytes = 25 * 1024 * 1024

	maxRecipients = 100
	readTimeout   = 60 * time.Second
	writeTimeout  = 30 * time.Second
)

// ListenAndServe starts the SMTP catcher on addr and blocks until it stops.
// A summary of each received message is written to out.
func ListenAndServe(addr string, out io.Writer) error {
	s := gosmtp.NewServer(&backend{out: out})

	s.Addr = addr
	s.Domain = "localhost"
	s.MaxMessageBytes = maxMessageBytes
	s.MaxRecipients = maxRecipients
	s.ReadTimeout = readTimeout
	s.WriteTimeout = writeTimeout

	// Bind first so we only claim to be listening once the port is actually
	// ours, and so the caller sees the real address when addr uses port 0.
	l, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", addr, err)
	}

	fmt.Fprintf(out, "mailtui: SMTP catcher listening on %s\n", l.Addr())

	return s.Serve(l)
}

// backend hands out a fresh session per connection.
type backend struct {
	out io.Writer
}

func (b *backend) NewSession(_ *gosmtp.Conn) (gosmtp.Session, error) {
	return &session{out: b.out}, nil
}

// session holds the envelope of the message currently being received: the
// sender from MAIL FROM and the recipients from RCPT TO.
type session struct {
	out  io.Writer
	from string
	to   []string
}

func (s *session) Mail(from string, _ *gosmtp.MailOptions) error {
	s.from = from
	return nil
}

func (s *session) Rcpt(to string, _ *gosmtp.RcptOptions) error {
	s.to = append(s.to, to)
	return nil
}

// Data captures the message. It fails only when the SMTP transaction itself
// does, such as a connection that drops mid-payload: content we cannot parse is
// still accepted, because a malformed message is often exactly the artifact the
// developer is trying to look at.
func (s *session) Data(r io.Reader) error {
	raw, err := io.ReadAll(r)
	if err != nil {
		return err
	}

	env := message.Envelope{From: s.from, To: s.to}

	return s.report(message.Capture(env, raw, time.Now()))
}

// report writes a summary of the captured message to the configured output.
// This is the debugging view for the current milestone and will be replaced by
// the TUI; the structured Message, not this text, is the real result of Data.
func (s *session) report(msg *message.Message) error {
	// Built in one buffer and written once, so summaries from concurrent
	// connections do not interleave line by line.
	var b strings.Builder

	fmt.Fprintf(&b, "\n--- message received %s ---\n", msg.ReceivedAt.Format(time.RFC3339))
	if msg.ParseError != nil {
		// Printed above the fields it qualifies, so the reader knows they are
		// empty or incomplete before reading them. The message was still kept:
		// its raw bytes are intact on the Message.
		//
		// Trimmed because the parser quotes the offending line verbatim, CRLF
		// included, and a warning has to stay on one line to be readable.
		fmt.Fprintf(&b, "warning: captured but not fully parsed: %s\n",
			strings.TrimSpace(msg.ParseError.Error()))
	}
	// The envelope and the headers are printed separately because they can
	// disagree, and the difference is often the thing being debugged.
	fmt.Fprintf(&b, "envelope from: %s\n", msg.EnvelopeFrom)
	fmt.Fprintf(&b, "envelope to:   %s\n", strings.Join(msg.EnvelopeTo, ", "))
	fmt.Fprintf(&b, "header from:   %s\n", msg.HeaderFrom)
	fmt.Fprintf(&b, "header to:     %s\n", strings.Join(msg.HeaderTo, ", "))
	fmt.Fprintf(&b, "subject:       %s\n", msg.Subject)

	if msg.TextBody != "" {
		fmt.Fprintf(&b, "\n%s\n", strings.TrimRight(msg.TextBody, "\r\n"))
	}
	if msg.HTMLBody != "" {
		fmt.Fprintf(&b, "\n[html body, %d bytes]\n", len(msg.HTMLBody))
	}

	fmt.Fprintf(&b, "--- end of message (%d raw bytes) ---\n", len(msg.Raw))

	_, err := io.WriteString(s.out, b.String())
	return err
}

// Reset discards the message currently being built, per RSET.
func (s *session) Reset() {
	s.from = ""
	s.to = nil
}

func (s *session) Logout() error { return nil }
