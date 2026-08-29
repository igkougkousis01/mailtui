// Package smtp implements the local SMTP catcher that receives mail from
// applications under development and prints it to stdout.
package smtp

import (
	"fmt"
	"io"
	"net"
	"time"

	gosmtp "github.com/emersion/go-smtp"
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
// Received messages are written to out.
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

// session accumulates one message: the envelope sender, its recipients and
// the raw DATA payload.
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

func (s *session) Data(r io.Reader) error {
	body, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	return s.print(body)
}

// print writes the received message to the configured output.
func (s *session) print(body []byte) error {
	_, err := fmt.Fprintf(s.out,
		"\n--- message received %s ---\nFrom: %s\nTo:   %v\n\n%s\n--- end of message (%d bytes) ---\n",
		time.Now().Format(time.RFC3339), s.from, s.to, body, len(body),
	)
	return err
}

// Reset discards the message currently being built, per RSET.
func (s *session) Reset() {
	s.from = ""
	s.to = nil
}

func (s *session) Logout() error { return nil }
