//go:build linux

package lvs

import (
	"net"
	"sync"
	"testing"

	"go.uber.org/zap"
)

// ipvsMu serializes all tests that use the real Linux IPVS handle,
// because IPVS is a global kernel resource shared across all tests.
var ipvsMu sync.Mutex

// newTestManager creates a Manager backed by the real Linux IPVS handle.
// It acquires a global lock to prevent concurrent IPVS access between tests,
// and flushes IPVS rules before and after each test to ensure isolation.
func newTestManager(t *testing.T) *Manager {
	t.Helper()
	ipvsMu.Lock()
	mgr, err := NewManager(zap.NewNop())
	if err != nil {
		ipvsMu.Unlock()
		t.Fatalf("NewManager failed: %v", err)
	}
	// Flush existing IPVS rules to ensure a clean starting state
	if err := mgr.Flush(); err != nil {
		ipvsMu.Unlock()
		t.Fatalf("failed to flush IPVS rules before test: %v", err)
	}
	// Register cleanup to flush after test and release the lock.
	// Use a separate handle for cleanup because the test may call mgr.Close()
	// via defer before t.Cleanup runs (Go executes defers before Cleanup).
	t.Cleanup(func() {
		cleanupHandle, err := NewIPVSHandle("")
		if err == nil {
			cleanupHandle.Flush()
			cleanupHandle.Close()
		}
		ipvsMu.Unlock()
	})
	return mgr
}

func newTestService(address string, port uint16, protocol uint16, scheduler string) *Service {
	ip := net.ParseIP(address)
	if ipv4 := ip.To4(); ipv4 != nil {
		ip = ipv4
	}
	return &Service{
		Address:       ip,
		Protocol:      protocol,
		Port:          port,
		SchedName:     scheduler,
		AddressFamily: 2, // AF_INET
		Netmask:       0xFFFFFFFF,
	}
}

func newTestDestination(address string, port uint16, weight int) *Destination {
	ip := net.ParseIP(address)
	if ipv4 := ip.To4(); ipv4 != nil {
		ip = ipv4
	}
	return &Destination{
		Address:         ip,
		Port:            port,
		Weight:          weight,
		ConnectionFlags: ConnectionFlagMasq,
		AddressFamily:   2, // AF_INET
	}
}
