//go:build unix

package cli

import (
	"context"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestScriptModeStopsOnASignal proves that both Unix signals supported by the
// command end a waiting run with the interrupted code. Platform-neutral parent
// context cancellation is covered in cli_test.go and continues to run on
// Windows.
//
// The signal goes to this process, which is safe because Run installs its
// handler before claiming the port: awaitPort therefore means the signal cannot
// reach the default disposition and take the test binary with it.
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
