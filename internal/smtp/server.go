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
	"errors"
	"fmt"
	"io"
	stdlog "log"
	"net"
	"strings"
	"sync"
	"time"

	gosmtp "github.com/emersion/go-smtp"

	"github.com/igkougkousis01/mailtui/internal/message"
	"github.com/igkougkousis01/mailtui/internal/store"
)

// Server is a catcher that has claimed its port but is not yet accepting.
//
// It exists for callers that need the port before the mail starts flowing: a
// script-mode command has to report the address it is listening on, has to be
// told at once when the port is already taken, and — when it is given port 0 —
// only learns the real port from the listener. Binding and serving are
// separate calls so that all three are answerable before Serve blocks.
type Server struct {
	srv     *gosmtp.Server
	backend *backend
	l       net.Listener
	log     io.Writer
}

// Listen claims cfg.Addr for a catcher that adds what it receives to st.
//
// The port is held from here on, so a caller that gets a Server back and then
// changes its mind must Close it. Notes about each message go to log, which
// may be nil to keep quiet.
//
// A failure to bind comes back as a *ListenError, so a caller can say which
// address it was and whether the port was already taken.
func Listen(cfg Config, st *store.Store, log io.Writer) (*Server, error) {
	b := &backend{store: st, log: log}
	s := gosmtp.NewServer(b)

	s.Addr = cfg.Addr
	s.Domain = "localhost"
	s.MaxMessageBytes = cfg.MaxMessageBytes
	s.MaxRecipients = cfg.MaxRecipients
	s.ReadTimeout = readTimeout
	s.WriteTimeout = writeTimeout

	// go-smtp logs accept and session errors to stderr by default, which is
	// exactly where they must not go: stderr is the TUI's screen in one mode
	// and a script's diagnostics in the other, and neither wants a library
	// writing to it unbidden. A caller that asked for notes gets these too.
	if log == nil {
		s.ErrorLog = stdlog.New(io.Discard, "", 0)
	} else {
		s.ErrorLog = stdlog.New(log, "smtp: ", 0)
	}

	// Bind first so we only claim to be listening once the port is actually
	// ours, and so the caller sees the real address when addr uses port 0.
	l, err := net.Listen("tcp", cfg.Addr)
	if err != nil {
		return nil, &ListenError{Addr: cfg.Addr, Err: err}
	}

	return &Server{srv: s, backend: b, l: l, log: log}, nil
}

// Addr is the address the catcher is bound to, with the port resolved.
func (s *Server) Addr() net.Addr { return s.l.Addr() }

// Serve accepts mail until Close is called, and returns nil when it is.
func (s *Server) Serve() error {
	logf(s.log, "mailtui: SMTP catcher listening on %s\n", s.l.Addr())

	return s.srv.Serve(s.l)
}

// Stop shuts the catcher down, giving deliveries already under way up to grace
// to finish first. StopGrace is the wait callers normally give it.
//
// The grace is not politeness, it is correctness. A message reaches the store
// while its SMTP session is still open: the server writes its 250 only after
// the handler that stored it returns, and a command watching the store can be
// woken and be finished in between. Closing the listener at that moment drops
// the connection before the acknowledgement, and the application under test —
// which sent the mail perfectly well — reports a failed send. So a stop waits
// for the sessions in flight, and only then cuts off whatever is left, which
// bounds the wait for a client that holds its connection open.
func (s *Server) Stop(grace time.Duration) error {
	s.backend.waitIdle(grace)
	return s.Close()
}

// Close stops the catcher: the port is released and any session still open is
// cut off, which is what ends the goroutines behind them. Serve then returns
// nil. Calling it twice is harmless, and so is calling it before Serve.
func (s *Server) Close() error {
	err := s.srv.Close()

	// And the listener this package bound itself, which the line above may not
	// have touched: the SMTP library only closes the listeners Serve has
	// registered with it, so a Close that arrives before Serve reached that
	// point would leave it about to block in Accept, with the port still held,
	// for as long as the process lived. Closing it here covers that ordering.
	//
	// It comes second so that Serve reads the accept failure as the shutdown
	// it is — the server is already marked closed by then — and returns nil.
	// The error is dropped because the only one it can give is that the
	// listener was already closed, which is the ordinary case.
	s.l.Close()

	if err != nil && !errors.Is(err, gosmtp.ErrServerClosed) {
		return err
	}
	return nil
}

// backend hands out a fresh session per connection. The store is shared across
// all of them, which is why it has to be safe for concurrent use.
type backend struct {
	store *store.Store
	log   io.Writer

	// live counts the sessions that have not logged out yet, so that a
	// stopping server can tell whether anyone is mid-delivery. See Stop.
	live sync.WaitGroup
}

func (b *backend) NewSession(_ *gosmtp.Conn) (gosmtp.Session, error) {
	b.live.Add(1)
	return &session{store: b.store, log: b.log, backend: b}, nil
}

// waitIdle blocks until no session is in flight, or until timeout, whichever
// comes first.
func (b *backend) waitIdle(timeout time.Duration) {
	idle := make(chan struct{})
	go func() {
		defer close(idle)
		b.live.Wait()
	}()

	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case <-idle:
	case <-timer.C:
		// The remaining sessions are about to be cut off by Close, which ends
		// them and so ends the goroutine above with them.
	}
}

// session holds the envelope of the message currently being received: the
// sender from MAIL FROM and the recipients from RCPT TO.
type session struct {
	store   *store.Store
	log     io.Writer
	backend *backend
	from    string
	to      []string

	// loggedOut makes the count in backend.live safe against a Logout that
	// arrives twice, which a counter has no way to survive otherwise.
	loggedOut sync.Once
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

// Logout ends the session, which is what releases it from the count of
// deliveries in flight.
func (s *session) Logout() error {
	if s.backend != nil {
		s.loggedOut.Do(s.backend.live.Done)
	}
	return nil
}
