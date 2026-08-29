// Package smtp implements the local SMTP catcher that receives mail from
// applications under development.
//
// It owns the SMTP conversation and the envelope it carries. Everything about
// the content of a message belongs to the message package: this package reads
// the DATA payload and hands the raw bytes there. The captured message then
// goes into the store, which is the real outcome of a delivery; anything
// written to the log is a convenience for whoever is watching the terminal.
package smtp

import (
	"fmt"
	"io"
	"net"
	"strings"
	"time"

	gosmtp "github.com/emersion/go-smtp"

	"github.com/igkougkousis01/mailtui/internal/message"
	"github.com/igkougkousis01/mailtui/internal/store"
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
// Captured messages are added to st, which the caller owns and shares with the
// rest of the program. A one-line note about each message is written to log,
// which may be nil to keep quiet.
func ListenAndServe(addr string, st *store.Store, log io.Writer) error {
	s := gosmtp.NewServer(&backend{store: st, log: log})

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

	logf(log, "mailtui: SMTP catcher listening on %s\n", l.Addr())

	return s.Serve(l)
}

// backend hands out a fresh session per connection. The store is shared across
// all of them, which is why it has to be safe for concurrent use.
type backend struct {
	store *store.Store
	log   io.Writer
}

func (b *backend) NewSession(_ *gosmtp.Conn) (gosmtp.Session, error) {
	return &session{store: b.store, log: b.log}, nil
}

// session holds the envelope of the message currently being received: the
// sender from MAIL FROM and the recipients from RCPT TO.
type session struct {
	store *store.Store
	log   io.Writer
	from  string
	to    []string
}

func (s *session) Mail(from string, _ *gosmtp.MailOptions) error {
	s.from = from
	return nil
}

func (s *session) Rcpt(to string, _ *gosmtp.RcptOptions) error {
	s.to = append(s.to, to)
	return nil
}

// Data captures the message and stores it. It fails only when the SMTP
// transaction itself does, such as a connection that drops mid-payload:
// content we cannot parse is still stored, because a malformed message is
// often exactly the artifact the developer is trying to look at.
func (s *session) Data(r io.Reader) error {
	raw, err := io.ReadAll(r)
	if err != nil {
		return err
	}

	env := message.Envelope{From: s.from, To: s.to}
	msg := message.Capture(env, raw, time.Now())

	// Add copies what it is given, so the captured Message is this session's to
	// drop; the store's copy, under the ID returned here, is the real outcome.
	id := s.store.Add(msg)
	s.note(id, msg)

	return nil
}

// note writes the one line that says a message arrived. It is a breadcrumb for
// someone watching the terminal, not a view of the message: the stored Message
// is the result of Data, and the TUI reads it from the store.
func (s *session) note(id string, msg *message.Message) {
	// The envelope, not the headers, because that is what the SMTP transaction
	// actually carried and what this package is responsible for.
	line := fmt.Sprintf("mailtui: stored message %s from %s to %s (%d bytes)",
		id, msg.EnvelopeFrom, strings.Join(msg.EnvelopeTo, ", "), len(msg.Raw))

	if msg.ParseError != nil {
		// Trimmed and kept on the same line because the parser quotes the
		// offending line verbatim, CRLF included.
		line += fmt.Sprintf(" [parse warning: %s]", strings.TrimSpace(msg.ParseError.Error()))
	}

	logf(s.log, "%s\n", line)
}

// logf writes to an optional log. A nil writer means the caller does not want
// the running commentary, which is the normal case once the TUI owns the
// terminal.
func logf(w io.Writer, format string, args ...any) {
	if w == nil {
		return
	}
	fmt.Fprintf(w, format, args...)
}

// Reset discards the message currently being built, per RSET.
func (s *session) Reset() {
	s.from = ""
	s.to = nil
}

func (s *session) Logout() error { return nil }
