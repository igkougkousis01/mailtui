package smtp

import (
	"bytes"
	"errors"
	"net"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/igkougkousis01/mailtui/internal/message"
	"github.com/igkougkousis01/mailtui/internal/store"
)

// rawMessage is CRLF-terminated like a real DATA payload. Its From and To
// headers deliberately differ from the envelope used in the tests, so a value
// on a stored message can be traced to the one it came from.
const rawMessage = "From: Header Sender <header-from@example.test>\r\n" +
	"To: Header Recipient <header-to@example.test>\r\n" +
	"Subject: hello\r\n" +
	"\r\n" +
	"body line one\r\nbody line two\r\n"

// newSession returns a session wired to a fresh store and a log buffer, which
// together are everything a delivery produces.
func newSession(t *testing.T) (*session, *store.Store, *bytes.Buffer) {
	t.Helper()

	st := store.New()
	var log bytes.Buffer
	return &session{store: st, log: &log}, st, &log
}

// deliver runs one full transaction, which is what the SMTP server does per
// message.
func deliver(t *testing.T, s *session, from string, to []string, raw string) {
	t.Helper()

	if err := s.Mail(from, nil); err != nil {
		t.Fatalf("Mail(%q): %v", from, err)
	}
	for _, rcpt := range to {
		if err := s.Rcpt(rcpt, nil); err != nil {
			t.Fatalf("Rcpt(%q): %v", rcpt, err)
		}
	}
	if err := s.Data(strings.NewReader(raw)); err != nil {
		t.Fatalf("Data: %v", err)
	}
}

// only returns a snapshot of the single stored message, failing if the store
// holds anything other than exactly one.
func only(t *testing.T, st *store.Store) message.Message {
	t.Helper()

	list := st.List()
	if len(list) != 1 {
		t.Fatalf("store holds %d messages, want 1", len(list))
	}
	return list[0]
}

// TestDataAddsMessageToStore pins the catcher's central contract: the outcome
// of DATA is a stored message, not a line of output.
func TestDataAddsMessageToStore(t *testing.T) {
	s, st, _ := newSession(t)

	// Nothing is stored until the message is complete; the envelope alone is
	// not a delivery.
	if err := s.Mail("app@example.test", nil); err != nil {
		t.Fatalf("Mail: %v", err)
	}
	if err := s.Rcpt("alice@example.test", nil); err != nil {
		t.Fatalf("Rcpt: %v", err)
	}
	if got := len(st.List()); got != 0 {
		t.Fatalf("store holds %d messages before DATA, want 0", got)
	}

	if err := s.Data(strings.NewReader(rawMessage)); err != nil {
		t.Fatalf("Data: %v", err)
	}

	msg := only(t, st)
	if msg.ID == "" {
		t.Error("stored message has no ID")
	}
	if msg.ParseError != nil {
		t.Errorf("ParseError = %v on a well-formed message, want nil", msg.ParseError)
	}
	if msg.ReceivedAt.IsZero() {
		t.Error("ReceivedAt is zero")
	}
	got, ok := st.Get(msg.ID)
	if !ok {
		t.Fatalf("Get(%q): the listed message is not indexed", msg.ID)
	}
	if got.Subject != msg.Subject || string(got.Raw) != string(msg.Raw) {
		t.Errorf("Get(%q) does not agree with List() about the stored message", msg.ID)
	}
}

// TestDataStoresEnvelopeApartFromHeaders is the reason the session hands the
// envelope to the parser instead of letting the parser infer it: both survive
// into the store, separately, so a consumer can show the difference.
func TestDataStoresEnvelopeApartFromHeaders(t *testing.T) {
	s, st, _ := newSession(t)

	deliver(t, s, "app@example.test",
		[]string{"alice@example.test", "bob@example.test"}, rawMessage)

	msg := only(t, st)

	if want := "app@example.test"; msg.EnvelopeFrom != want {
		t.Errorf("EnvelopeFrom = %q, want %q", msg.EnvelopeFrom, want)
	}
	if want := "alice@example.test, bob@example.test"; strings.Join(msg.EnvelopeTo, ", ") != want {
		t.Errorf("EnvelopeTo = %q, want %q", msg.EnvelopeTo, want)
	}
	if want := "Header Sender <header-from@example.test>"; msg.HeaderFrom != want {
		t.Errorf("HeaderFrom = %q, want %q", msg.HeaderFrom, want)
	}
	if want := "Header Recipient <header-to@example.test>"; strings.Join(msg.HeaderTo, ", ") != want {
		t.Errorf("HeaderTo = %q, want %q", msg.HeaderTo, want)
	}
	if want := "hello"; msg.Subject != want {
		t.Errorf("Subject = %q, want %q", msg.Subject, want)
	}
	if !strings.Contains(msg.TextBody, "body line one") {
		t.Errorf("TextBody = %q, want it to contain the body", msg.TextBody)
	}
}

// TestDataStoresRawBytesExactly guards the promise the catcher is built on: the
// payload reaches the store byte for byte, CRLFs included.
func TestDataStoresRawBytesExactly(t *testing.T) {
	s, st, _ := newSession(t)

	deliver(t, s, "app@example.test", []string{"alice@example.test"}, rawMessage)

	if got := string(only(t, st).Raw); got != rawMessage {
		t.Errorf("Raw = %q, want %q", got, rawMessage)
	}
}

// TestDataStoresUnparsableMessage pins the catcher's whole point: a message we
// cannot parse is still stored, with the reason attached, because it is often
// the artifact the developer is trying to inspect. Only a broken SMTP
// transaction fails.
func TestDataStoresUnparsableMessage(t *testing.T) {
	const malformed = "this is not a mail message\r\nnor is this\r\n"

	s, st, log := newSession(t)

	deliver(t, s, "app@example.test",
		[]string{"alice@example.test", "bob@example.test"}, malformed)

	msg := only(t, st)

	// The failure is recorded rather than swallowed, and it says what broke.
	if msg.ParseError == nil {
		t.Fatal("ParseError = nil on a malformed message")
	}
	if !strings.Contains(msg.ParseError.Error(), "malformed MIME header line") {
		t.Errorf("ParseError = %v, want it to say what could not be parsed", msg.ParseError)
	}

	// Everything the SMTP layer is responsible for survives the parse failure.
	if want := "app@example.test"; msg.EnvelopeFrom != want {
		t.Errorf("EnvelopeFrom = %q, want %q", msg.EnvelopeFrom, want)
	}
	if want := "alice@example.test, bob@example.test"; strings.Join(msg.EnvelopeTo, ", ") != want {
		t.Errorf("EnvelopeTo = %q, want %q", msg.EnvelopeTo, want)
	}
	if got := string(msg.Raw); got != malformed {
		t.Errorf("Raw = %q, want %q", got, malformed)
	}
	if msg.ID == "" {
		t.Error("a malformed message was stored without an ID")
	}

	// The warning reaches whoever is watching the terminal, on one line: the
	// parser quotes the offending line verbatim, CRLF included, and an
	// untrimmed warning would split across lines.
	got := log.String()
	if !strings.Contains(got, "parse warning") {
		t.Errorf("log does not mention the parse failure:\n%s", got)
	}
	for _, line := range strings.Split(got, "\n") {
		if strings.Contains(line, "parse warning") && strings.ContainsAny(line, "\r") {
			t.Errorf("warning line contains a carriage return: %q", line)
		}
	}
}

// TestDataNotifiesSubscribers covers the end of the capture flow: DATA reaches
// a consumer without anyone polling for it. The consumer is handed an ID and
// fetches the message itself.
func TestDataNotifiesSubscribers(t *testing.T) {
	s, st, _ := newSession(t)

	ch, cancel := s.store.Subscribe()
	defer cancel()

	deliver(t, s, "app@example.test", []string{"alice@example.test"}, rawMessage)

	select {
	case id := <-ch:
		if want := only(t, st).ID; id != want {
			t.Errorf("notified ID = %q, want %q", id, want)
		}
		msg, ok := st.Get(id)
		if !ok {
			t.Fatalf("Get(%q): a notified message is not in the store", id)
		}
		if string(msg.Raw) != rawMessage {
			t.Errorf("the notified message has the wrong payload: %q", msg.Raw)
		}
	default:
		t.Fatal("storing a message did not notify the subscriber")
	}
}

// TestSessionLogIsOptional covers the nil writer, which is what the TUI will
// pass once it owns the terminal.
func TestSessionLogIsOptional(t *testing.T) {
	st := store.New()
	s := &session{store: st}

	deliver(t, s, "app@example.test", []string{"alice@example.test"}, rawMessage)

	only(t, st)
}

// TestSessionResetDiscardsEnvelope covers the case Reset exists for: a client
// that abandons a message with RSET and sends another on the same connection
// must not inherit the abandoned sender or recipients.
func TestSessionResetDiscardsEnvelope(t *testing.T) {
	s, st, _ := newSession(t)

	if err := s.Mail("stale@example.test", nil); err != nil {
		t.Fatalf("Mail: %v", err)
	}
	if err := s.Rcpt("discarded@example.test", nil); err != nil {
		t.Fatalf("Rcpt: %v", err)
	}

	s.Reset()

	if s.from != "" {
		t.Errorf("from = %q after Reset, want empty", s.from)
	}
	if len(s.to) != 0 {
		t.Errorf("to = %v after Reset, want empty", s.to)
	}

	deliver(t, s, "second@example.test", []string{"kept@example.test"}, rawMessage)

	// An abandoned envelope must not reach the store, and nothing was stored
	// for the abandoned transaction either.
	msg := only(t, st)
	if msg.EnvelopeFrom != "second@example.test" {
		t.Errorf("EnvelopeFrom = %q, want %q", msg.EnvelopeFrom, "second@example.test")
	}
	if strings.Join(msg.EnvelopeTo, ", ") != "kept@example.test" {
		t.Errorf("EnvelopeTo = %q, want [kept@example.test]", msg.EnvelopeTo)
	}
}

// TestBackendGivesEachSessionTheSameStore pins the injection: sessions are
// per-connection, but they all deliver into the one store the caller owns.
func TestBackendGivesEachSessionTheSameStore(t *testing.T) {
	st := store.New()
	b := &backend{store: st}

	for i := 0; i < 2; i++ {
		sess, err := b.NewSession(nil)
		if err != nil {
			t.Fatalf("NewSession: %v", err)
		}
		deliver(t, sess.(*session), "app@example.test", []string{"alice@example.test"}, rawMessage)
	}

	if got := len(st.List()); got != 2 {
		t.Errorf("store holds %d messages after two sessions, want 2", got)
	}
}

// TestStopBeforeServeStillEndsServe covers the ordering a short-lived run
// actually hits: the caller is finished before the goroutine that serves has
// got as far as accepting.
//
// The SMTP library only closes the listeners Serve has registered with it, so
// a stop that lands first closes nothing there and Serve would block in Accept
// with the port held for the life of the process. Server.Close closes the
// listener it bound itself for exactly this reason.
func TestStopBeforeServeStillEndsServe(t *testing.T) {
	srv, err := Listen(testConfig(t), store.New(), nil)
	if err != nil {
		t.Fatalf("listening: %v", err)
	}
	addr := srv.Addr().String()

	// Stopped before anything starts serving, which is the case under test.
	if err := srv.Stop(10 * time.Millisecond); err != nil {
		t.Fatalf("stopping: %v", err)
	}

	served := make(chan error, 1)
	go func() { served <- srv.Serve() }()

	select {
	case err := <-served:
		if err != nil {
			t.Errorf("Serve returned %v, want nil after a stop", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Serve never returned; the listener was left accepting")
	}

	// And the port really is free, not merely unattended.
	l, err := net.Listen("tcp", addr)
	if err != nil {
		t.Fatalf("the port was still held after stopping: %v", err)
	}
	l.Close()
}

// TestCloseIsIdempotent: every path out of a run stops the catcher, and some
// of them overlap. None of that may panic or report a failure.
func TestCloseIsIdempotent(t *testing.T) {
	srv, err := Listen(testConfig(t), store.New(), nil)
	if err != nil {
		t.Fatalf("listening: %v", err)
	}

	served := make(chan error, 1)
	go func() { served <- srv.Serve() }()

	for i := 0; i < 3; i++ {
		if err := srv.Close(); err != nil {
			t.Errorf("Close call %d returned %v, want nil", i+1, err)
		}
	}
	if err := srv.Stop(10 * time.Millisecond); err != nil {
		t.Errorf("Stop after Close returned %v, want nil", err)
	}

	select {
	case err := <-served:
		if err != nil {
			t.Errorf("Serve returned %v, want nil", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Serve never returned")
	}
}

// TestListenReportsAPortAlreadyTaken: the failure a developer meets most
// often, in the words they can act on rather than the network stack's three
// nested repetitions of the same address.
func TestListenReportsAPortAlreadyTaken(t *testing.T) {
	held, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("holding a port: %v", err)
	}
	defer held.Close()

	cfg := DefaultConfig()
	cfg.Addr = held.Addr().String()

	srv, err := Listen(cfg, store.New(), nil)
	if err == nil {
		srv.Close()
		t.Fatal("Listen took a port that was already held")
	}

	var listenErr *ListenError
	if !errors.As(err, &listenErr) {
		t.Fatalf("err = %v, want a *ListenError", err)
	}
	if want := "cannot listen on " + cfg.Addr + ": address already in use"; err.Error() != want {
		t.Errorf("err = %q, want %q", err, want)
	}
	if !errors.Is(err, syscall.EADDRINUSE) {
		t.Errorf("err = %v, want the cause to survive wrapping so a caller can offer a hint", err)
	}
}

// TestConfigIsApplied: the caps a caller sets are the caps the SMTP
// conversation runs with, so that --max-recipients means something.
func TestConfigIsApplied(t *testing.T) {
	cfg := testConfig(t)
	cfg.MaxMessageBytes = 4096
	cfg.MaxRecipients = 2

	srv, err := Listen(cfg, store.New(), nil)
	if err != nil {
		t.Fatalf("listening: %v", err)
	}
	defer srv.Close()

	if srv.srv.MaxMessageBytes != 4096 {
		t.Errorf("MaxMessageBytes = %d, want 4096", srv.srv.MaxMessageBytes)
	}
	if srv.srv.MaxRecipients != 2 {
		t.Errorf("MaxRecipients = %d, want 2", srv.srv.MaxRecipients)
	}
}

// TestDefaultConfig pins the values every mode starts from.
func TestDefaultConfig(t *testing.T) {
	want := Config{Addr: "127.0.0.1:1025", MaxMessageBytes: 25 * 1024 * 1024, MaxRecipients: 100}
	if got := DefaultConfig(); got != want {
		t.Errorf("DefaultConfig() = %+v, want %+v", got, want)
	}
}

// testConfig is the defaults on a port the kernel picks, so that tests never
// contend for the real one.
func testConfig(t *testing.T) Config {
	t.Helper()

	cfg := DefaultConfig()
	cfg.Addr = "127.0.0.1:0"
	return cfg
}
