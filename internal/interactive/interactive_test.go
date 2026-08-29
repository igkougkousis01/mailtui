package interactive

import (
	"context"
	"errors"
	"net"
	netsmtp "net/smtp"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/igkougkousis01/mailtui/internal/smtp"
	"github.com/igkougkousis01/mailtui/internal/store"
)

// The inbox itself needs a terminal, so these drive the lifecycle around it
// with a stand-in: everything this package is responsible for — the order of
// binding and drawing, stopping the catcher, choosing which of two failures to
// report — happens outside the TUI and can be checked without one.

const testMail = "From: App <noreply@header.test>\r\n" +
	"To: John <john@header.test>\r\n" +
	"Subject: Hello\r\n" +
	"\r\n" +
	"A message.\r\n"

// freePort is a loopback address that was free a moment ago.
func freePort(t *testing.T) string {
	t.Helper()

	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserving a port: %v", err)
	}
	addr := l.Addr().String()
	l.Close()
	return addr
}

// config is the defaults on a port nothing else is using.
func config(t *testing.T) smtp.Config {
	t.Helper()

	cfg := smtp.DefaultConfig()
	cfg.Addr = freePort(t)
	return cfg
}

// quitting is an inbox that leaves at once, as a user pressing q does.
func quitting(context.Context, *store.Store, string) error { return nil }

func TestBindFailureIsReportedBeforeAnythingIsDrawn(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("holding a port: %v", err)
	}
	defer l.Close()

	cfg := smtp.DefaultConfig()
	cfg.Addr = l.Addr().String()

	drawn := false
	err = run(context.Background(), cfg, func(context.Context, *store.Store, string) error {
		drawn = true
		return nil
	})

	if drawn {
		t.Error("the inbox was drawn even though the port could not be claimed")
	}

	var listenErr *smtp.ListenError
	if !errors.As(err, &listenErr) {
		t.Fatalf("err = %v, want a *smtp.ListenError", err)
	}
	if !errors.Is(err, syscall.EADDRINUSE) {
		t.Errorf("err = %v, want the address-in-use cause to survive wrapping", err)
	}
	if strings.Contains(err.Error(), "listen tcp") {
		t.Errorf("err = %q, want the network stack's wrapping trimmed off", err)
	}
}

// TestTheInboxIsToldTheResolvedAddress: a --smtp-addr ending in :0 is a real
// port by the time anything is drawn, and the header must say which one.
func TestTheInboxIsToldTheResolvedAddress(t *testing.T) {
	cfg := smtp.DefaultConfig()
	cfg.Addr = "127.0.0.1:0"

	var shown string
	if err := run(context.Background(), cfg, func(_ context.Context, _ *store.Store, addr string) error {
		shown = addr
		return nil
	}); err != nil {
		t.Fatalf("run: %v", err)
	}

	_, port, err := net.SplitHostPort(shown)
	if err != nil {
		t.Fatalf("the inbox was shown %q, want a host:port address", shown)
	}
	if port == "0" || port == "" {
		t.Errorf("the inbox was shown %q, want the port the listener actually took", shown)
	}
}

// TestTheInboxIsToldTheConfiguredAddress covers the ordinary case, where the
// address is exactly what was asked for.
func TestTheInboxIsToldTheConfiguredAddress(t *testing.T) {
	cfg := config(t)

	var shown string
	if err := run(context.Background(), cfg, func(_ context.Context, _ *store.Store, addr string) error {
		shown = addr
		return nil
	}); err != nil {
		t.Fatalf("run: %v", err)
	}
	if shown != cfg.Addr {
		t.Errorf("the inbox was shown %q, want %q", shown, cfg.Addr)
	}
}

// TestTheListenerIsReleasedOnQuit: the port is free the moment the process
// would be — a second mailtui started right after the first must be able to
// take it.
func TestTheListenerIsReleasedOnQuit(t *testing.T) {
	cfg := config(t)

	if err := run(context.Background(), cfg, quitting); err != nil {
		t.Fatalf("run: %v", err)
	}

	l, err := net.Listen("tcp", cfg.Addr)
	if err != nil {
		t.Fatalf("the port was still held after quitting: %v", err)
	}
	l.Close()
}

// TestQuittingTwiceOverDoesNotPanic: the catcher is stopped on every path out
// of run, and a stop that lands on a server already on its way down must be
// harmless rather than a double close.
func TestQuittingTwiceOverDoesNotPanic(t *testing.T) {
	for i := 0; i < 5; i++ {
		cfg := config(t)
		if err := run(context.Background(), cfg, quitting); err != nil {
			t.Fatalf("run: %v", err)
		}
	}
}

// TestCtrlCIsNotAFailure: Bubble Tea reports SIGINT as ErrInterrupted, and the
// user asking to leave is not something to exit non-zero about.
func TestCtrlCIsNotAFailure(t *testing.T) {
	err := run(context.Background(), config(t), func(context.Context, *store.Store, string) error {
		return tea.ErrInterrupted
	})
	if err != nil {
		t.Fatalf("err = %v, want Ctrl-C treated as an ordinary exit", err)
	}
}

// TestAnInboxFailureIsReported: anything else the TUI returns is a real
// failure and has to reach the caller to be printed.
func TestAnInboxFailureIsReported(t *testing.T) {
	want := errors.New("the renderer gave up")

	err := run(context.Background(), config(t), func(context.Context, *store.Store, string) error {
		return want
	})
	if !errors.Is(err, want) {
		t.Fatalf("err = %v, want %v", err, want)
	}
}

// TestCaughtMailReachesTheStoreTheInboxReads is the whole point of the wiring:
// the catcher fills the store the inbox was handed.
func TestCaughtMailReachesTheStoreTheInboxReads(t *testing.T) {
	cfg := config(t)

	var count int
	var sendErr error
	err := run(context.Background(), cfg, func(_ context.Context, st *store.Store, addr string) error {
		sendErr = netsmtp.SendMail(addr, nil, "bounce@envelope.test", []string{"john@example.test"}, []byte(testMail))
		count = len(st.List())
		return nil
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if sendErr != nil {
		t.Fatalf("sending mail to the catcher: %v", sendErr)
	}
	if count != 1 {
		t.Errorf("the inbox saw %d messages, want 1", count)
	}
}

// TestASenderMidDeliveryIsAcknowledged: quitting the inbox while a delivery is
// in flight must not drop the connection before the 250. See smtp.Server.Stop.
func TestASenderMidDeliveryIsAcknowledged(t *testing.T) {
	cfg := config(t)

	// The send runs beside the inbox, and the inbox quits as soon as the
	// message lands — which is while the SMTP session is still open.
	sent := make(chan error, 1)
	err := run(context.Background(), cfg, func(_ context.Context, st *store.Store, addr string) error {
		events, unsubscribe := st.Subscribe()
		defer unsubscribe()

		go func() {
			sent <- netsmtp.SendMail(addr, nil, "bounce@envelope.test", []string{"john@example.test"}, []byte(testMail))
		}()

		select {
		case <-events:
		case <-time.After(10 * time.Second):
			t.Error("no message arrived")
		}
		return nil
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	select {
	case err := <-sent:
		if err != nil {
			t.Fatalf("the sender was cut off mid-delivery: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the sender never finished")
	}
}

// TestNothingIsStillRunningAfterwards: run owns a goroutine for the catcher
// and the sessions under it, and all of them are finished by the time it
// returns. A leak here is a process that will not exit.
func TestNothingIsStillRunningAfterwards(t *testing.T) {
	// One run first, so that anything the SMTP and store packages start once
	// is already started and not counted as a leak.
	if err := run(context.Background(), config(t), quitting); err != nil {
		t.Fatalf("run: %v", err)
	}
	before := settledGoroutines()

	for i := 0; i < 3; i++ {
		cfg := config(t)
		err := run(context.Background(), cfg, func(_ context.Context, _ *store.Store, addr string) error {
			return netsmtp.SendMail(addr, nil, "bounce@envelope.test", []string{"john@example.test"}, []byte(testMail))
		})
		if err != nil {
			t.Fatalf("run: %v", err)
		}
	}

	if after := settledGoroutines(); after > before {
		t.Errorf("%d goroutines were running before and %d after; something was left behind", before, after)
	}
}

// settledGoroutines is the goroutine count once it has stopped falling, since
// a session that has been cut off takes a moment to notice.
func settledGoroutines() int {
	n := runtime.NumGoroutine()
	for i := 0; i < 100; i++ {
		time.Sleep(10 * time.Millisecond)
		runtime.GC()
		if next := runtime.NumGoroutine(); next >= n {
			return n
		} else {
			n = next
		}
	}
	return n
}
