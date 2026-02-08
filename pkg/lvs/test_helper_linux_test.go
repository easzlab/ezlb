//go:build linux

package lvs

import (
	"net"
	"testing"

	"go.uber.org/zap"
)

// newTestManager creates a Manager backed by the real Linux IPVS handle.
// It flushes any existing IPVS rules before and after each test to ensure isolation.
func newTestManager(t *testing.T) *Manager {
	t.Helper()
	mgr, err := NewManager(zap.NewNop())
	if err != nil {
		t.Fatalf("NewManager failed: %v", err)
	}
	// Flush existing IPVS rules to ensure a clean starting state
	if err := mgr.Flush(); err != nil {
		t.Fatalf("failed to flush IPVS rules before test: %v", err)
	}
	// Register cleanup to flush after test completes
	t.Cleanup(func() {
		mgr.Flush()
	})
	return mgr
}

func newTestService(address string, port uint16, protocol uint16, scheduler string) *Service {
	return &Service{
		Address:       net.ParseIP(address),
		Protocol:      protocol,
		Port:          port,
		SchedName:     scheduler,
		AddressFamily: 2, // AF_INET
		Netmask:       0xFFFFFFFF,
	}
}

func newTestDestination(address string, port uint16, weight int) *Destination {
	return &Destination{
		Address:         net.ParseIP(address),
		Port:            port,
		Weight:          weight,
		ConnectionFlags: ConnectionFlagMasq,
		AddressFamily:   2, // AF_INET
	}
}
