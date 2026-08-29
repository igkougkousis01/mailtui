//go:build windows

package smtp

import (
	"errors"
	"syscall"
)

// wsaeaddrinuse is the Winsock error returned when bind finds an address
// already occupied. syscall's EADDRINUSE on Windows is a compatibility value,
// not the Winsock value carried by net.Listen errors.
const wsaeaddrinuse syscall.Errno = 10048

func isAddrInUse(err error) bool {
	return errors.Is(err, wsaeaddrinuse)
}
