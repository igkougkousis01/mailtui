package cli

import (
	"bytes"
	"context"
	"net"
	netsmtp "net/smtp"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/igkougkousis01/mailtui/internal/smtp"
	"github.com/igkougkousis01/mailtui/internal/version"
)

// TestVersionIsOnStdoutAndScriptFriendly: the version was asked for, so it is
// the result. One line, on stdout, with nothing else anywhere.
func TestVersionIsOnStdoutAndScriptFriendly(t *testing.T) {
	for _, args := range [][]string{{"version"}, {"--version"}, {"-version"}} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			got := run(t, args...)

			if got.code != ExitOK {
				t.Fatalf("exit code = %d, want %d", got.code, ExitOK)
			}
			if want := version.String() + "\n"; got.stdout != want {
				t.Errorf("stdout = %q, want %q", got.stdout, want)
			}
			if got.stderr != "" {
				t.Errorf("stderr = %q, want nothing", got.stderr)
			}
			if fields := strings.Fields(got.stdout); len(fields) != 2 || fields[0] != "mailtui" {
				t.Errorf("stdout = %q, want two fields beginning with the program name", got.stdout)
			}
		})
	}
}

// TestVersionFallsBackToDev: a binary built without the linker flag says so
// rather than claiming a release number it is not.
func TestVersionFallsBackToDev(t *testing.T) {
	if version.Version == "" {
		t.Fatal("the version is empty; it should be a release number or dev")
	}
}

// TestRootFlagErrorsAreUsageErrors: a mistyped flag on plain mailtui must not
// reach the terminal takeover.
func TestRootFlagErrorsAreUsageErrors(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{"an unknown flag", []string{"--nope"}, "not defined"},
		{"a mistyped command", []string{"maiiltui"}, "unknown command"},
		{"a command after the flags", []string{"--smtp-addr", "127.0.0.1:2525", "waat"}, "unknown command"},
		{"an address off loopback", []string{"--smtp-addr", "0.0.0.0:1025"}, "not a loopback"},
		{"an address on every interface", []string{"--smtp-addr", ":1025"}, "every interface"},
		{"a zero message cap", []string{"--max-message-bytes", "0"}, "--max-message-bytes must be positive"},
		{"a negative message cap", []string{"--max-message-bytes", "-1"}, "--max-message-bytes must be positive"},
		{"an unparseable size", []string{"--max-message-bytes", "big"}, "not a size"},
		{"zero recipients", []string{"--max-recipients", "0"}, "--max-recipients must be positive"},
		{"negative recipients", []string{"--max-recipients", "-3"}, "--max-recipients must be positive"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			app, stdout, stderr, launched := newApp()

			code := app.Run(context.Background(), tt.args)

			if code != ExitUsage {
				t.Errorf("exit code = %d, want %d\nstderr: %s", code, ExitUsage, stderr.String())
			}
			if *launched {
				t.Error("the inbox was launched on a bad configuration; it must fail before the terminal is taken over")
			}
			if stdout.Len() != 0 {
				t.Errorf("stdout = %q, want nothing; a broken invocation is not a result", stdout.String())
			}
			if !strings.Contains(stderr.String(), tt.want) {
				t.Errorf("stderr = %q, want it to mention %q", stderr.String(), tt.want)
			}
		})
	}
}

// TestInteractiveGetsTheConfiguredCatcher: the flags reach the inbox as given,
// and the defaults reach it when they are not.
func TestInteractiveGetsTheConfiguredCatcher(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want smtp.Config
	}{
		{"no flags", nil, smtp.DefaultConfig()},
		{
			"all three",
			[]string{"--smtp-addr", "127.0.0.1:2525", "--max-message-bytes", "10485760", "--max-recipients", "50"},
			smtp.Config{Addr: "127.0.0.1:2525", MaxMessageBytes: 10 * 1024 * 1024, MaxRecipients: 50},
		},
		{
			"a size with a unit",
			[]string{"--max-message-bytes", "10MB"},
			cfgWith(func(c *smtp.Config) { c.MaxMessageBytes = 10 * 1024 * 1024 }),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got smtp.Config
			var stdout, stderr bytes.Buffer
			app := &App{
				Stdout: &stdout,
				Stderr: &stderr,
				Interactive: func(_ context.Context, cfg smtp.Config) error {
					got = cfg
					return nil
				},
			}

			if code := app.Run(context.Background(), tt.args); code != ExitOK {
				t.Fatalf("exit code = %d, want %d\nstderr: %s", code, ExitOK, stderr.String())
			}
			if got != tt.want {
				t.Errorf("the inbox was given %+v, want %+v", got, tt.want)
			}
		})
	}
}

// TestInteractiveBindFailureIsAConfigurationError: a port already in use is
// the same kind of problem in interactive mode as in a script, and it gets the
// same exit code and the same hint.
func TestInteractiveBindFailureIsAConfigurationError(t *testing.T) {
	var stdout, stderr bytes.Buffer
	app := &App{
		Stdout: &stdout,
		Stderr: &stderr,
		Interactive: func(context.Context, smtp.Config) error {
			// What smtp.Listen returns when the port is taken.
			return &smtp.ListenError{
				Addr: "127.0.0.1:1025",
				Err:  &net.OpError{Op: "listen", Err: syscall.EADDRINUSE},
			}
		},
	}

	code := app.Run(context.Background(), nil)

	if code != ExitUsage {
		t.Fatalf("exit code = %d, want %d", code, ExitUsage)
	}
	if stdout.Len() != 0 {
		t.Errorf("stdout = %q, want nothing", stdout.String())
	}
	for _, want := range []string{
		"mailtui: cannot listen on 127.0.0.1:1025: address already in use",
		"hint: another mailtui",
	} {
		if !strings.Contains(stderr.String(), want) {
			t.Errorf("stderr does not contain %q:\n%s", want, stderr.String())
		}
	}
	// The operating system's own wrapping must not come through with it.
	if strings.Contains(stderr.String(), "listen tcp") {
		t.Errorf("stderr repeats the network stack's wrapping:\n%s", stderr.String())
	}
}

// TestHintsAreSparing: a hint under every error teaches the reader to skip the
// line under the one that matters, so only the failures with a specific thing
// to try get one.
func TestHintsAreSparing(t *testing.T) {
	var stdout, stderr bytes.Buffer
	app := &App{
		Stdout: &stdout,
		Stderr: &stderr,
		Interactive: func(context.Context, smtp.Config) error {
			return &smtp.ListenError{
				Addr: "127.0.0.1:1025",
				Err:  &net.OpError{Op: "listen", Err: syscall.EINVAL},
			}
		},
	}

	app.Run(context.Background(), nil)

	if strings.Contains(stderr.String(), "hint:") {
		t.Errorf("stderr offered a hint for a failure with nothing specific to suggest:\n%s", stderr.String())
	}
}

// TestEachHelpCarriesItsOwnUsage: a command's help has to be about that
// command, not the same page five times.
func TestEachHelpCarriesItsOwnUsage(t *testing.T) {
	tests := []struct {
		args []string
		want []string
	}{
		{[]string{"--help"}, []string{"mailtui wait", "mailtui extract otp", "mailtui version", "Examples:"}},
		{[]string{"wait", "--help"}, []string{"mailtui wait —", "mailtui wait --to", "Exit codes:"}},
		{[]string{"assert", "--help"}, []string{"mailtui assert —", "mailtui assert --to", "Exit codes:"}},
		{[]string{"extract", "otp", "--help"}, []string{"extract otp —", "OTP=$(mailtui extract otp", "Exit codes:"}},
		{[]string{"extract", "link", "--help"}, []string{"extract link —", "URL=$(mailtui extract link", "Exit codes:"}},
	}

	for _, tt := range tests {
		t.Run(strings.Join(tt.args, " "), func(t *testing.T) {
			got := run(t, tt.args...)

			for _, want := range tt.want {
				if !strings.Contains(got.stdout, want) {
					t.Errorf("the help does not contain %q:\n%s", want, got.stdout)
				}
			}
		})
	}
}

// TestHelpQuotesTheRealDefaults: help that has drifted from the code is worse
// than no help, so every default it names is interpolated from the constant
// the code uses and this checks the result rather than a duplicated literal.
func TestHelpQuotesTheRealDefaults(t *testing.T) {
	defaults := []string{
		smtp.DefaultAddr,
		formatSize(smtp.DefaultMaxMessageBytes),
		defaultTimeout.String(),
	}

	for _, args := range [][]string{{"help"}, {"wait", "--help"}, {"assert", "--help"}, {"extract", "otp", "--help"}, {"extract", "link", "--help"}} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			help := run(t, args...).stdout

			for _, want := range defaults {
				if !strings.Contains(help, want) {
					t.Errorf("the help does not quote the default %q", want)
				}
			}
			for _, want := range []string{"--max-message-bytes", "--max-recipients", "--smtp-addr"} {
				if !strings.Contains(help, want) {
					t.Errorf("the help does not list %s", want)
				}
			}
		})
	}
}

// TestScriptModeStopsOnASignal: SIGINT and SIGTERM both end a waiting command
// with the interrupted code, which is the one thing a script can tell from a
// failed assertion.
//
// The signal goes to this process, which is safe because the handler is
// installed by Run before the port is claimed: awaitPort therefore means the
// signal cannot reach the default disposition and take the test binary with
// it.
func TestScriptModeStopsOnASignal(t *testing.T) {
	for _, sig := range []syscall.Signal{syscall.SIGINT, syscall.SIGTERM} {
		t.Run(sig.String(), func(t *testing.T) {
			addr := freeAddr(t)
			app, stdout, stderr, _ := newApp()

			codes := make(chan int, 1)
			go func() {
				codes <- app.Run(context.Background(),
					[]string{"wait", "--subject", "Never sent", "--timeout", "0", "--smtp-addr", addr})
			}()

			awaitPort(t, addr)
			if err := syscall.Kill(syscall.Getpid(), sig); err != nil {
				t.Fatalf("signalling this process: %v", err)
			}

			select {
			case code := <-codes:
				if code != ExitInterrupted {
					t.Fatalf("exit code = %d, want %d\nstderr: %s", code, ExitInterrupted, stderr.String())
				}
				if stdout.String() != "" {
					t.Errorf("stdout = %q, want nothing when interrupted", stdout.String())
				}
				if !strings.Contains(stderr.String(), "interrupted") {
					t.Errorf("stderr = %q, want it to say the run was interrupted", stderr.String())
				}
			case <-time.After(15 * time.Second):
				t.Fatalf("%s did not stop the command", sig)
			}
		})
	}
}

// TestSenderIsAcknowledgedBeforeShutdown is the reason Stop has a grace at
// all. The command finishes the moment the message reaches the store, which is
// before the 250 goes back; if the listener closed there, an application that
// sent perfectly good mail would report a failed send.
func TestSenderIsAcknowledgedBeforeShutdown(t *testing.T) {
	addr := freeAddr(t)
	app, _, stderr, _ := newApp()

	codes := make(chan int, 1)
	go func() {
		codes <- app.Run(context.Background(),
			[]string{"wait", "--to", envelopeTo, "--timeout", "10s", "--smtp-addr", addr})
	}()

	awaitPort(t, addr)

	// The error is kept rather than fataled: whether the sender was
	// acknowledged is the assertion, not a precondition of one.
	sendErr := netsmtp.SendMail(addr, nil, envelopeFrom, []string{envelopeTo}, []byte(resetMail))

	select {
	case code := <-codes:
		if code != ExitOK {
			t.Fatalf("exit code = %d, want %d\nstderr: %s", code, ExitOK, stderr.String())
		}
	case <-time.After(15 * time.Second):
		t.Fatal("the command did not return")
	}

	if sendErr != nil {
		t.Fatalf("the sender was cut off mid-delivery: %v", sendErr)
	}
}

// TestScriptStdoutStaysMachineOnly restates the guarantee that makes
// OTP=$(mailtui extract otp ...) safe across every script-mode outcome:
// a version or a startup line leaking here would be caught as a changed value.
func TestScriptStdoutStaysMachineOnly(t *testing.T) {
	got := runCatching(t, context.Background(),
		[]string{"extract", "otp", "--to", envelopeTo, "--max-message-bytes", "10MB", "--max-recipients", "50", "--timeout", "10s"},
		deliverOne(t, resetMail))

	if got.code != ExitOK {
		t.Fatalf("exit code = %d, want %d\nstderr: %s", got.code, ExitOK, got.stderr)
	}
	if got.stdout != "483921\n" {
		t.Fatalf("stdout = %q, want the code and nothing else", got.stdout)
	}
	if !strings.Contains(got.stderr, "catching SMTP on") {
		t.Errorf("stderr = %q, want the line saying the port was claimed", got.stderr)
	}
}
