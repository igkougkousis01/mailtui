//go:build !windows

package smtp

import (
	"errors"
	"syscall"
)

// isAddrInUse recognizes the POSIX errno while leaving the original error
// chain intact for callers that need more detail.
func isAddrInUse(err error) bool {
	return errors.Is(err, syscall.EADDRINUSE)
}
