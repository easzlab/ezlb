//go:build linux

package lvs

import (
	"net"
	"testing"

	"go.uber.org/zap"
)

// newTestManager creates a Manager backed by the real Linux IPVS handle.
// It flushes any existing IPVS rules before returning to ensure a clean state.
func newTestManager(t *testing.T) *Manager {
	t.Helper()
	mgr, err := NewManager(zap.NewNop())
	if err != nil {
		t.Fatalf("NewManager failed: %v", err)
	}
	// Flush existing IPVS rules to ensure test isolation
	handle, _ := NewIPVSHandle("")
	if err := handle.Flush(); err != nil {
		t.Fatalf("failed to flush IPVS rules: %v", err)
	}
	handle.Close()
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
