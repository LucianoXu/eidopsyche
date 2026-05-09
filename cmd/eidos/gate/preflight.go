package gate

import (
	"errors"
	"net"
	"strings"
	"syscall"
)

// isAddrInUse covers the syscall code on POSIX plus a substring fallback
// for hosts where errors.Is doesn't match (or the syscall package shape
// differs across platforms in unexpected ways).
func isAddrInUse(err error) bool {
	if errors.Is(err, syscall.EADDRINUSE) {
		return true
	}
	return strings.Contains(strings.ToLower(err.Error()), "address already in use")
}

// portFromHostPort returns the port component of a host:port string, or
// the original string if it doesn't parse.
func portFromHostPort(addr string) string {
	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		return addr
	}
	return port
}
