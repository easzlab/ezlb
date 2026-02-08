package healthcheck

import (
	"fmt"
	"net"
	"time"
)

// Checker defines the interface for health check probes.
// This abstraction allows extending with different probe types (e.g., UDP) in the future.
type Checker interface {
	Check(address string) error
}

// TCPChecker implements health checking via TCP connection attempts.
type TCPChecker struct {
	timeout time.Duration
}

// NewTCPChecker creates a new TCPChecker with the given timeout.
func NewTCPChecker(timeout time.Duration) *TCPChecker {
	return &TCPChecker{
		timeout: timeout,
	}
}

// Check attempts to establish a TCP connection to the given address.
// Returns nil if the connection succeeds (healthy), or an error if it fails (unhealthy).
func (c *TCPChecker) Check(address string) error {
	conn, err := net.DialTimeout("tcp", address, c.timeout)
	if err != nil {
		return fmt.Errorf("tcp health check failed for %s: %w", address, err)
	}
	conn.Close()
	return nil
}
