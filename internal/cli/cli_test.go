package cli

import (
	"bytes"
	"context"
	"errors"
	"net"
	netsmtp "net/smtp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/igkougkousis01/mailtui/internal/smtp"
)

// Mail the tests send. The envelope and the headers differ so that a value in
// the output can be traced to the one it came from, and the body carries both
// a code and a link so one message can serve both extract commands.
const (
	resetMail = "From: App <noreply@header.test>\r\n" +
		"To: John <john@header.test>\r\n" +
		"Subject: Reset password\r\n" +
		"\r\n" +
		"Hi John,\r\n\r\n" +
		"Your verification code is 483921.\r\n\r\n" +
		"Or open https://example.test/reset/abc to continue.\r\n"

	// The right recipient and the right subject, and the wrong code: the
	// message an assert must decline to accept and then keep waiting past.
	decoyResetMail = "From: App <noreply@header.test>\r\n" +
		"To: John <john@header.test>\r\n" +
		"Subject: Reset password\r\n" +
		"\r\n" +
		"Hi John,\r\n\r\nYour verification code is 111222.\r\n"

	plainMail = "From: App <noreply@header.test>\r\n" +
		"To: John <john@header.test>\r\n" +
		"Subject: Welcome aboard\r\n" +
		"\r\n" +
		"Nothing to extract here.\r\n"

	// No header block at all, which is what the parser rejects.
	malformedMail = "this is not a mail message at all\r\njust some loose text\r\n"
)

const (
	envelopeFrom = "bounce@envelope.test"
	envelopeTo   = "john@example.test"
)

// result is everything one run of the command line produced.
type result struct {
	code   int
	stdout string
	stderr string
}

// newApp returns an App writing into buffers, with an interactive mode that
// records being called rather than taking over the terminal.
func newApp() (*App, *bytes.Buffer, *bytes.Buffer, *bool) {
	var stdout, stderr bytes.Buffer
	launched := false

	app := &App{
		Stdout: &stdout,
		Stderr: &stderr,
		Interactive: func(ctx context.Context, cfg smtp.Config) error {
			launched = true
			return nil
		},
	}
	return app, &stdout, &stderr, &launched
}

// run executes args to completion. It is for commands that never wait for
// mail; anything that binds a port goes through runCatching.
func run(t *testing.T, args ...string) result {
	t.Helper()

	app, stdout, stderr, _ := newApp()
	code := app.Run(context.Background(), args)
	return result{code, stdout.String(), stderr.String()}
}

// freeAddr returns a loopback address that was free a moment ago.
//
// Nothing can promise it still is — but the command under test reports a bind
// failure as a usage error, so a lost race shows up as a clear failure rather
// than a mysterious one.
func freeAddr(t *testing.T) string {
	t.Helper()

	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserving a port: %v", err)
	}
	addr := l.Addr().String()
	l.Close()
	return addr
}

// awaitPort blocks until something is listening on addr, which is how a script
// knows the catcher has claimed the port and it is safe to send.
func awaitPort(t *testing.T, addr string) {
	t.Helper()

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", addr, 100*time.Millisecond)
		if err == nil {
			conn.Close()
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("nothing is listening on %s", addr)
}

// send delivers one message over SMTP, as an application under test would.
func send(t *testing.T, addr, from string, to []string, raw string) {
	t.Helper()

	if err := netsmtp.SendMail(addr, nil, from, to, []byte(raw)); err != nil {
		t.Fatalf("sending mail to %s: %v", addr, err)
	}
}

// runCatching runs a command that waits for mail: it starts the command, waits
// for the catcher to claim its port, runs deliver, and returns what the
// command produced. deliver may be nil, for the commands that are meant to
// time out.
func runCatching(t *testing.T, ctx context.Context, args []string, deliver func(addr string)) result {
	t.Helper()

	addr := freeAddr(t)
	args = append(args, "--smtp-addr", addr)

	app, stdout, stderr, _ := newApp()

	codes := make(chan int, 1)
	go func() {
		codes <- app.Run(ctx, args)
	}()

	awaitPort(t, addr)
	if deliver != nil {
		deliver(addr)
	}

	select {
	case code := <-codes:
		// Reading the buffers only after the command has returned is what
		// keeps this free of a race with the goroutine that wrote them.
		return result{code, stdout.String(), stderr.String()}
	case <-time.After(30 * time.Second):
		t.Fatal("the command did not return")
		return result{}
	}
}

// resultLine splits the single line wait prints into its four fields, failing
// the test if the output is not one line of four.
func resultLine(t *testing.T, stdout string) []string {
	t.Helper()

	line, ok := strings.CutSuffix(stdout, "\n")
	if !ok || strings.Contains(line, "\n") {
		t.Fatalf("stdout = %q, want exactly one newline-terminated line", stdout)
	}

	fields := strings.Split(line, "\t")
	if len(fields) != 4 {
		t.Fatalf("stdout = %q, want four tab-separated fields", stdout)
	}
	return fields
}

// deliverOne is the usual case: one message, sent once the catcher is up.
func deliverOne(t *testing.T, raw string) func(string) {
	return func(addr string) {
		send(t, addr, envelopeFrom, []string{envelopeTo}, raw)
	}
}

func TestPlainMailtuiLaunchesTheInteractiveInbox(t *testing.T) {
	app, stdout, stderr, launched := newApp()

	code := app.Run(context.Background(), nil)

	if code != ExitOK {
		t.Fatalf("exit code = %d, want %d", code, ExitOK)
	}
	if !*launched {
		t.Fatal("mailtui with no arguments did not launch the interactive inbox")
	}
	if stdout.Len() != 0 || stderr.Len() != 0 {
		t.Fatalf("interactive mode wrote to the command line's streams: stdout %q, stderr %q", stdout.String(), stderr.String())
	}
}

func TestInteractiveFailureIsReportedAndFails(t *testing.T) {
	var stdout, stderr bytes.Buffer
	app := &App{
		Stdout: &stdout,
		Stderr: &stderr,
		Interactive: func(context.Context, smtp.Config) error {
			return errors.New("the inbox stopped")
		},
	}

	if code := app.Run(context.Background(), nil); code != ExitFailure {
		t.Fatalf("exit code = %d, want %d", code, ExitFailure)
	}
	if !strings.Contains(stderr.String(), "the inbox stopped") {
		t.Fatalf("stderr = %q, want the reason the inbox stopped", stderr.String())
	}
}

// TestUsageErrors covers every way an invocation can be wrong. All of them
// exit 2 — distinct from a failed assertion — and none of them put anything on
// stdout.
func TestUsageErrors(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{"an unknown command", []string{"waaait"}, "unknown command"},
		{"an unknown flag", []string{"wait", "--nope", "x"}, "not defined"},
		{"an unparseable duration", []string{"wait", "--timeout", "soon"}, "invalid value"},
		{"a stray argument", []string{"wait", "john@example.test"}, "unexpected argument"},
		{"assert with nothing to assert", []string{"assert", "--to", "john@example.test"}, "needs something to check"},
		{"extract with no subcommand", []string{"extract"}, "otp or link"},
		{"an unknown extract subcommand", []string{"extract", "code"}, "unknown extract subcommand"},
		{"an address on every interface", []string{"wait", "--smtp-addr", ":1025"}, "every interface"},
		{"an address off loopback", []string{"wait", "--smtp-addr", "0.0.0.0:1025"}, "not a loopback address"},
		{"an address that is not host:port", []string{"wait", "--smtp-addr", "1025"}, "not a host:port"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := run(t, tt.args...)

			if got.code != ExitUsage {
				t.Errorf("exit code = %d, want %d\nstderr: %s", got.code, ExitUsage, got.stderr)
			}
			if got.stdout != "" {
				t.Errorf("stdout = %q, want nothing; a broken invocation is not a result", got.stdout)
			}
			if !strings.Contains(got.stderr, tt.want) {
				t.Errorf("stderr = %q, want it to mention %q", got.stderr, tt.want)
			}
		})
	}
}

// TestHelpGoesToStdout: help was asked for, so it is the result, and a user
// piping it into a pager should get it.
func TestHelpGoesToStdout(t *testing.T) {
	for _, args := range [][]string{
		{"help"},
		{"--help"},
		{"wait", "--help"},
		{"assert", "-h"},
		{"extract", "--help"},
		{"extract", "otp", "--help"},
		{"extract", "link", "--help"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			got := run(t, args...)

			if got.code != ExitOK {
				t.Errorf("exit code = %d, want %d", got.code, ExitOK)
			}
			if !strings.Contains(got.stdout, "mailtui") {
				t.Errorf("stdout = %q, want the help text", got.stdout)
			}
			if got.stderr != "" {
				t.Errorf("stderr = %q, want nothing", got.stderr)
			}
		})
	}
}

// TestHelpDocumentsTheContract keeps the promises this milestone makes where a
// user can find them. The exact wording is free to change; that each subject
// is covered is not.
func TestHelpDocumentsTheContract(t *testing.T) {
	help := run(t, "help").stdout

	for _, want := range []string{
		"Process model",    // that each command catches its own mail
		"in memory",        // why it cannot query a running mailtui
		"Exit codes",       // 0, 1, 2, 130
		"130",              //
		"case-insensitive", // how matching compares
		"--timeout",        // the default and how to change it
		"stdout",           // what a script may capture
	} {
		if !strings.Contains(help, want) {
			t.Errorf("the top-level help does not mention %q", want)
		}
	}
}

func TestWaitSucceedsOnAMatchingMessage(t *testing.T) {
	got := runCatching(t, context.Background(),
		[]string{"wait", "--to", envelopeTo, "--subject", "Reset", "--timeout", "10s"},
		deliverOne(t, resetMail))

	if got.code != ExitOK {
		t.Fatalf("exit code = %d, want %d\nstderr: %s", got.code, ExitOK, got.stderr)
	}

	// One line, tab-separated: id, envelope sender, envelope recipients,
	// subject. The envelope values, not the headers, which differ on purpose.
	// The ID is whatever the store assigned and is not pinned here; that it is
	// there, and first, is the part of the format a script depends on.
	fields := resultLine(t, got.stdout)
	if fields[0] == "" {
		t.Errorf("the first field is empty, want the message ID")
	}
	if want := []string{envelopeFrom, envelopeTo, "Reset password"}; !slices.Equal(fields[1:], want) {
		t.Errorf("stdout fields after the ID = %q, want %q", fields[1:], want)
	}
}

// TestWaitMatchesOnEverySelector walks the flags one at a time against a
// message whose envelope and headers disagree, which is the only way to see
// that each reads the side it claims to.
func TestWaitMatchesOnEverySelector(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{"envelope or header recipient", []string{"--to", envelopeTo}},
		{"header recipient through --to", []string{"--to", "john@header.test"}},
		{"envelope recipient only", []string{"--envelope-to", envelopeTo}},
		{"header recipient only", []string{"--header-to", "john@header.test"}},
		{"envelope or header sender", []string{"--from", envelopeFrom}},
		{"header sender only", []string{"--header-from", "noreply@header.test"}},
		{"envelope sender only", []string{"--envelope-from", envelopeFrom}},
		{"subject", []string{"--subject", "reset password"}},
		{"body content", []string{"--contains", "verification code"}},
		{"several at once", []string{"--to", envelopeTo, "--subject", "Reset", "--contains", "483921"}},
		{"nothing at all", nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			args := append([]string{"wait", "--timeout", "10s"}, tt.args...)
			got := runCatching(t, context.Background(), args, deliverOne(t, resetMail))

			if got.code != ExitOK {
				t.Fatalf("exit code = %d, want %d\nstderr: %s", got.code, ExitOK, got.stderr)
			}
		})
	}
}

func TestWaitTimesOut(t *testing.T) {
	got := runCatching(t, context.Background(),
		[]string{"wait", "--subject", "Never sent", "--timeout", "100ms"},
		deliverOne(t, resetMail))

	if got.code != ExitFailure {
		t.Fatalf("exit code = %d, want %d\nstderr: %s", got.code, ExitFailure, got.stderr)
	}
	if got.stdout != "" {
		t.Fatalf("stdout = %q, want nothing on a timeout", got.stdout)
	}
	if !strings.Contains(got.stderr, "timed out") || !strings.Contains(got.stderr, "Never sent") {
		t.Fatalf("stderr = %q, want it to say what it timed out waiting for", got.stderr)
	}
}

// TestWaitIsInterruptible covers Ctrl-C: the exit code says the run was
// stopped, not that the assertion failed.
func TestWaitIsInterruptible(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	got := runCatching(t, ctx,
		[]string{"wait", "--subject", "Never sent", "--timeout", "0"},
		func(string) { cancel() })
	cancel()

	if got.code != ExitInterrupted {
		t.Fatalf("exit code = %d, want %d\nstderr: %s", got.code, ExitInterrupted, got.stderr)
	}
	if got.stdout != "" {
		t.Fatalf("stdout = %q, want nothing when interrupted", got.stdout)
	}
	if !strings.Contains(got.stderr, "interrupted") {
		t.Fatalf("stderr = %q, want it to say the run was interrupted", got.stderr)
	}
}

// TestPortAlreadyInUseIsAConfigurationError is the cost of the process model,
// and it has to be reported as something the user can act on.
func TestPortAlreadyInUseIsAConfigurationError(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("holding a port: %v", err)
	}
	defer l.Close()

	got := run(t, "wait", "--smtp-addr", l.Addr().String(), "--timeout", "1s")

	if got.code != ExitUsage {
		t.Fatalf("exit code = %d, want %d\nstderr: %s", got.code, ExitUsage, got.stderr)
	}
	if !strings.Contains(got.stderr, "another mailtui") {
		t.Fatalf("stderr = %q, want it to suggest what is holding the port", got.stderr)
	}
}

func TestAssertSucceeds(t *testing.T) {
	got := runCatching(t, context.Background(),
		[]string{"assert", "--to", envelopeTo, "--subject", "Reset password", "--contains", "verification code", "--timeout", "10s"},
		deliverOne(t, resetMail))

	if got.code != ExitOK {
		t.Fatalf("exit code = %d, want %d\nstderr: %s", got.code, ExitOK, got.stderr)
	}
	if got.stdout != "" {
		t.Fatalf("stdout = %q; a check that passed says so with its exit code", got.stdout)
	}
}

// TestAssertWaitsPastAPartialMatch is the heart of what assert means: a
// message that satisfies some of the criteria is not the message, and it must
// not end the wait. Every case here delivers something partial first and the
// real thing second.
func TestAssertWaitsPastAPartialMatch(t *testing.T) {
	tests := []struct {
		name   string
		args   []string
		before []string // delivered first, none of them a full match
	}{
		{
			"the recipient matches but the subject does not",
			[]string{"--to", envelopeTo, "--subject", "Reset password"},
			[]string{plainMail},
		},
		{
			"the subject matches but the body does not",
			[]string{"--to", envelopeTo, "--subject", "Reset password", "--contains", "483921"},
			[]string{decoyResetMail},
		},
		{
			"several partial matches in a row",
			[]string{"--to", envelopeTo, "--subject", "Reset password", "--contains", "483921"},
			[]string{plainMail, decoyResetMail, plainMail, decoyResetMail},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			args := append([]string{"assert", "--timeout", "15s"}, tt.args...)

			got := runCatching(t, context.Background(), args, func(addr string) {
				for _, raw := range tt.before {
					send(t, addr, envelopeFrom, []string{envelopeTo}, raw)
				}
				// The one that satisfies everything, last.
				send(t, addr, envelopeFrom, []string{envelopeTo}, resetMail)
			})

			if got.code != ExitOK {
				t.Fatalf("exit code = %d, want %d\nstderr: %s", got.code, ExitOK, got.stderr)
			}
			if got.stdout != "" {
				t.Errorf("stdout = %q, want nothing", got.stdout)
			}
		})
	}
}

// TestAssertFailures covers the ways it says no. They share an exit code, and
// the report is what tells them apart.
func TestAssertFailures(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		deliver func(string)
		want    []string
	}{
		{
			"nothing arrives at all",
			[]string{"--to", envelopeTo, "--subject", "Reset", "--timeout", "300ms"},
			nil,
			[]string{"timed out", "no messages arrived"},
		},
		{
			"only partial matches, until the timeout",
			[]string{"--to", envelopeTo, "--subject", "Reset password", "--contains", "483921", "--timeout", "800ms"},
			func(addr string) {
				send(t, addr, envelopeFrom, []string{envelopeTo}, plainMail)
				send(t, addr, envelopeFrom, []string{envelopeTo}, decoyResetMail)
			},
			[]string{
				"2 messages arrived, none matching",
				// --to and --subject were each satisfied by a message, so the
				// report names only the criterion that never was.
				"no message satisfied: --contains",
				"closest was message",
				`contains: "483921" is not in the subject or either body`,
			},
		},
		{
			"nothing relevant, until the timeout",
			[]string{"--to", "someone@else.test", "--subject", "Reset", "--timeout", "800ms"},
			func(addr string) {
				send(t, addr, envelopeFrom, []string{envelopeTo}, plainMail)
			},
			[]string{
				"1 message arrived, none matching",
				"no message satisfied: --to --subject",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := runCatching(t, context.Background(), append([]string{"assert"}, tt.args...), tt.deliver)

			if got.code != ExitFailure {
				t.Fatalf("exit code = %d, want %d\nstderr: %s", got.code, ExitFailure, got.stderr)
			}
			if got.stdout != "" {
				t.Errorf("stdout = %q, want nothing on a failed assertion", got.stdout)
			}
			for _, want := range tt.want {
				if !strings.Contains(got.stderr, want) {
					t.Errorf("stderr does not contain %q:\n%s", want, got.stderr)
				}
			}
		})
	}
}

// TestAssertReportsNoBodies keeps the diagnostics to a size a test log can
// carry: the report names what was missing, it does not paste the mail.
func TestAssertReportsNoBodies(t *testing.T) {
	got := runCatching(t, context.Background(),
		[]string{"assert", "--to", envelopeTo, "--contains", "Verify your account", "--timeout", "800ms"},
		deliverOne(t, resetMail))

	if got.code != ExitFailure {
		t.Fatalf("exit code = %d, want %d", got.code, ExitFailure)
	}
	for _, unwanted := range []string{
		"Your verification code", // the text body
		"Content-Transfer",       // any part of the raw MIME
		"https://example.test",   // a link out of the body
	} {
		if strings.Contains(got.stderr, unwanted) {
			t.Errorf("stderr quoted %q back from the message:\n%s", unwanted, got.stderr)
		}
	}
}

// TestAssertIsInterruptibleAfterAPartialMatch: Ctrl-C is still Ctrl-C once the
// waiting has started in earnest, and it is not reported as a failed
// assertion.
func TestAssertIsInterruptibleAfterAPartialMatch(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	got := runCatching(t, ctx,
		[]string{"assert", "--to", envelopeTo, "--subject", "Reset password", "--contains", "483921", "--timeout", "0"},
		func(addr string) {
			send(t, addr, envelopeFrom, []string{envelopeTo}, plainMail)
			send(t, addr, envelopeFrom, []string{envelopeTo}, decoyResetMail)
			cancel()
		})
	cancel()

	if got.code != ExitInterrupted {
		t.Fatalf("exit code = %d, want %d\nstderr: %s", got.code, ExitInterrupted, got.stderr)
	}
	if got.stdout != "" {
		t.Errorf("stdout = %q, want nothing when interrupted", got.stdout)
	}
	if !strings.Contains(got.stderr, "interrupted") {
		t.Errorf("stderr = %q, want it to say the run was interrupted", got.stderr)
	}
}

// TestAssertNeverWritesToStdout states the guarantee in one place, over every
// path assert has: the result of a check is its exit code, and a script is
// free to use assert's stdout for something else entirely.
func TestAssertNeverWritesToStdout(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		got := runCatching(t, context.Background(),
			[]string{"assert", "--to", envelopeTo, "--contains", "483921", "--timeout", "15s"},
			deliverOne(t, resetMail))
		if got.code != ExitOK || got.stdout != "" {
			t.Fatalf("exit %d, stdout %q; want exit 0 and no output", got.code, got.stdout)
		}
	})

	t.Run("timeout", func(t *testing.T) {
		got := runCatching(t, context.Background(),
			[]string{"assert", "--to", envelopeTo, "--contains", "483921", "--timeout", "300ms"},
			deliverOne(t, plainMail))
		if got.code != ExitFailure || got.stdout != "" {
			t.Fatalf("exit %d, stdout %q; want exit 1 and no output", got.code, got.stdout)
		}
	})

	t.Run("usage error", func(t *testing.T) {
		got := run(t, "assert", "--to", envelopeTo)
		if got.code != ExitUsage || got.stdout != "" {
			t.Fatalf("exit %d, stdout %q; want exit 2 and no output", got.code, got.stdout)
		}
	})
}

// TestExtractOTPPrintsOnlyTheCode is the contract that makes
// OTP=$(mailtui extract otp ...) work.
func TestExtractOTPPrintsOnlyTheCode(t *testing.T) {
	got := runCatching(t, context.Background(),
		[]string{"extract", "otp", "--to", envelopeTo, "--timeout", "10s"},
		deliverOne(t, resetMail))

	if got.code != ExitOK {
		t.Fatalf("exit code = %d, want %d\nstderr: %s", got.code, ExitOK, got.stderr)
	}
	if got.stdout != "483921\n" {
		t.Fatalf("stdout = %q, want %q and nothing else", got.stdout, "483921\n")
	}
	if got.stderr == "" {
		t.Error("stderr held nothing; the line saying the port was claimed belongs there")
	}
}

func TestExtractLinkPrintsOnlyTheURL(t *testing.T) {
	got := runCatching(t, context.Background(),
		[]string{"extract", "link", "--to", envelopeTo, "--timeout", "10s"},
		deliverOne(t, resetMail))

	if got.code != ExitOK {
		t.Fatalf("exit code = %d, want %d\nstderr: %s", got.code, ExitOK, got.stderr)
	}
	if got.stdout != "https://example.test/reset/abc\n" {
		t.Fatalf("stdout = %q, want the URL and nothing else", got.stdout)
	}
}

// TestExtractFindsNothing: the message arrived and simply holds no code or
// link. That is the same answer as a failed assertion — exit 1 — and stdout
// stays empty so a script capturing it gets an empty string rather than a
// diagnostic.
func TestExtractFindsNothing(t *testing.T) {
	for _, what := range []string{"otp", "link"} {
		t.Run(what, func(t *testing.T) {
			got := runCatching(t, context.Background(),
				[]string{"extract", what, "--to", envelopeTo, "--timeout", "10s"},
				deliverOne(t, plainMail))

			if got.code != ExitFailure {
				t.Fatalf("exit code = %d, want %d\nstderr: %s", got.code, ExitFailure, got.stderr)
			}
			if got.stdout != "" {
				t.Fatalf("stdout = %q, want nothing", got.stdout)
			}
			if !strings.Contains(got.stderr, "no ") || !strings.Contains(got.stderr, "Welcome aboard") {
				t.Fatalf("stderr = %q, want it to name what was missing and which message was read", got.stderr)
			}
		})
	}
}

// TestMalformedMessageIsHandled: a message the parser cannot read is still
// caught, still matched on its envelope, and still searched for what was
// asked. Nothing here may panic.
func TestMalformedMessageIsHandled(t *testing.T) {
	got := runCatching(t, context.Background(),
		[]string{"wait", "--to", envelopeTo, "--timeout", "10s"},
		deliverOne(t, malformedMail))

	if got.code != ExitOK {
		t.Fatalf("exit code = %d, want %d\nstderr: %s", got.code, ExitOK, got.stderr)
	}
	// A message with no headers to parse still has an envelope, so the line
	// carries everything but the subject.
	fields := resultLine(t, got.stdout)
	if fields[1] != envelopeFrom || fields[2] != envelopeTo || fields[3] != "" {
		t.Fatalf("stdout fields = %q, want the envelope of the malformed message and no subject", fields)
	}

	// And the extractors read its raw payload, since it has no bodies.
	got = runCatching(t, context.Background(),
		[]string{"extract", "otp", "--to", envelopeTo, "--timeout", "10s"},
		deliverOne(t, "your code is 483921 and this has no header block\r\n"))

	if got.code != ExitOK {
		t.Fatalf("exit code = %d, want %d\nstderr: %s", got.code, ExitOK, got.stderr)
	}
	if got.stdout != "483921\n" {
		t.Fatalf("stdout = %q, want the code from the raw payload", got.stdout)
	}
}

// TestTheFirstMatchingMessageWins: several messages arrive and the command
// answers about the first one that matched, not the last.
func TestTheFirstMatchingMessageWins(t *testing.T) {
	got := runCatching(t, context.Background(),
		[]string{"wait", "--to", envelopeTo, "--subject", "Reset", "--timeout", "10s"},
		func(addr string) {
			send(t, addr, envelopeFrom, []string{envelopeTo}, plainMail)
			send(t, addr, envelopeFrom, []string{envelopeTo}, resetMail)
		})

	if got.code != ExitOK {
		t.Fatalf("exit code = %d, want %d\nstderr: %s", got.code, ExitOK, got.stderr)
	}
	// The second message is the one that matched, so it is the one reported.
	if fields := resultLine(t, got.stdout); fields[3] != "Reset password" {
		t.Fatalf("stdout reported %q, want the matching message", fields[3])
	}
}
