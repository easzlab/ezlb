package healthcheck

import (
	"net"
	"testing"
	"time"
)

func TestTCPChecker_ConnectionSuccess(t *testing.T) {
	// Start a local TCP listener
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to start TCP listener: %v", err)
	}
	defer listener.Close()

	// Accept connections in background to avoid blocking
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			conn.Close()
		}
	}()

	checker := NewTCPChecker(3 * time.Second)
	if err := checker.Check(listener.Addr().String()); err != nil {
		t.Fatalf("expected successful health check, got error: %v", err)
	}
}

func TestTCPChecker_ConnectionRefused(t *testing.T) {
	// Use a port that is very unlikely to be in use
	checker := NewTCPChecker(1 * time.Second)
	err := checker.Check("127.0.0.1:1")
	if err == nil {
		t.Fatal("expected error for connection refused, got nil")
	}
}

func TestTCPChecker_Timeout(t *testing.T) {
	// Create a listener that accepts but never responds (simulates slow server)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to start TCP listener: %v", err)
	}
	defer listener.Close()

	// Use an unreachable address with very short timeout to test timeout behavior
	checker := NewTCPChecker(50 * time.Millisecond)
	// 192.0.2.1 is a TEST-NET address (RFC 5737) that should be unreachable
	err = checker.Check("192.0.2.1:80")
	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}
}

func TestNewTCPChecker(t *testing.T) {
	timeout := 5 * time.Second
	checker := NewTCPChecker(timeout)
	if checker == nil {
		t.Fatal("expected non-nil checker")
	}
	if checker.timeout != timeout {
		t.Errorf("expected timeout %v, got %v", timeout, checker.timeout)
	}
}
