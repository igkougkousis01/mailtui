package smtp

import (
	"errors"
	"fmt"
	"net"
	"os"
	"time"
)

// The catcher's defaults. They are here, next to the server they configure,
// so that interactive mode, the script-mode commands, the help text and the
// tests all read the same numbers rather than four copies of them.
const (
	// DefaultAddr is where the catcher listens unless told otherwise. It is
	// loopback-only on purpose: the catcher accepts mail without
	// authentication and must not be reachable from the network.
	DefaultAddr = "127.0.0.1:1025"

	// DefaultMaxMessageBytes caps a single message so a runaway sender cannot
	// exhaust memory. 25 MB matches what most real providers accept, so a
	// message this catcher refuses would have been refused in production too.
	DefaultMaxMessageBytes = 25 * 1024 * 1024

	// DefaultMaxRecipients caps one message's RCPT TO list.
	DefaultMaxRecipients = 100
)

// Timeouts on the SMTP conversation itself. They are not configurable: they
// exist to stop a wedged connection holding a session open forever, which is
// not something a user of a local catcher has any reason to tune.
const (
	readTimeout  = 60 * time.Second
	writeTimeout = 30 * time.Second
)

// StopGrace is the wait Stop is normally given. Half a second is far more
// than a loopback acknowledgement needs, and short enough not to be felt by
// someone quitting the inbox while a sender holds its connection open.
const StopGrace = 500 * time.Millisecond

// Config is everything the catcher needs to know about the port it listens on
// and the mail it will accept.
//
// The zero value is not usable; start from DefaultConfig and change what you
// mean to change. That is deliberate — a zero MaxMessageBytes means "no limit"
// to the SMTP library underneath, and a config that silently removed the cap
// because a field went unset is exactly the kind of default nobody chooses.
type Config struct {
	// Addr is the host:port to listen on. It must be a loopback address; the
	// command line enforces that, since the flag is where the mistake is made.
	Addr string

	// MaxMessageBytes is the largest DATA payload accepted, in bytes.
	MaxMessageBytes int64

	// MaxRecipients is the most RCPT TO addresses accepted in one message.
	MaxRecipients int
}

// DefaultConfig returns the configuration the catcher runs with when nothing
// on the command line says otherwise.
func DefaultConfig() Config {
	return Config{
		Addr:            DefaultAddr,
		MaxMessageBytes: DefaultMaxMessageBytes,
		MaxRecipients:   DefaultMaxRecipients,
	}
}

// ListenError says the catcher could not claim its address.
//
// It exists to put one readable sentence in front of the user instead of the
// three nested ones the network stack produces: "listen on 127.0.0.1:1025:
// listen tcp 127.0.0.1:1025: bind: address already in use" says the address
// three times and the interesting part once. The cause is still wrapped. A
// caller uses IsAddrInUse rather than depending on an operating system's errno
// value or localized error text.
type ListenError struct {
	Addr string
	Err  error
}

func (e *ListenError) Error() string {
	if e.IsAddrInUse() {
		return fmt.Sprintf("cannot listen on %s: address already in use", e.Addr)
	}
	return fmt.Sprintf("cannot listen on %s: %s", e.Addr, cause(e.Err))
}

func (e *ListenError) Unwrap() error { return e.Err }

// IsAddrInUse reports whether the listener failed because another socket
// already holds the address. It classifies the wrapped operating-system error
// semantically, so callers do not need to know whether the platform reported
// POSIX EADDRINUSE or the Windows Winsock equivalent.
func (e *ListenError) IsAddrInUse() bool {
	return e != nil && isAddrInUse(e.Err)
}

// cause digs the operating system's own words out of a dial or listen error.
// net wraps a syscall error in an os.SyscallError in a net.OpError, and each
// layer prefixes what the layer below already said.
func cause(err error) error {
	var opErr *net.OpError
	if errors.As(err, &opErr) && opErr.Err != nil {
		err = opErr.Err
	}
	var sysErr *os.SyscallError
	if errors.As(err, &sysErr) && sysErr.Err != nil {
		err = sysErr.Err
	}
	return err
}
