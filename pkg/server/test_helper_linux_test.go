//go:build linux

package server

import (
	"sync"
	"testing"

	"github.com/easzlab/ezlb/pkg/lvs"
	"go.uber.org/zap"
)

// ipvsMu serializes all tests that use the real Linux IPVS handle,
// because IPVS is a global kernel resource shared across all tests.
var ipvsMu sync.Mutex

// newTestServer creates a Server backed by the real Linux IPVS handle.
// It acquires a global lock to prevent concurrent IPVS access between tests,
// and flushes IPVS rules before and after each test to ensure isolation.
func newTestServer(t *testing.T, configPath string) *Server {
	t.Helper()
	ipvsMu.Lock()
	logger := zap.NewNop()

	lvsMgr, err := lvs.NewManager(logger)
	if err != nil {
		ipvsMu.Unlock()
		t.Fatalf("lvs.NewManager failed: %v", err)
	}

	// Flush existing IPVS rules to ensure a clean starting state
	if err := lvsMgr.Flush(); err != nil {
		ipvsMu.Unlock()
		t.Fatalf("failed to flush IPVS rules before test: %v", err)
	}
	// Register cleanup to flush after test and release the lock
	t.Cleanup(func() {
		lvsMgr.Flush()
		ipvsMu.Unlock()
	})

	srv, err := newServerWithManager(configPath, lvsMgr, logger)
	if err != nil {
		ipvsMu.Unlock()
		t.Fatalf("newServerWithManager failed: %v", err)
	}
	return srv
}

// newTestLVSManager creates an LVS Manager backed by the real Linux IPVS handle.
// It acquires a global lock to prevent concurrent IPVS access between tests,
// and flushes IPVS rules before and after each test to ensure isolation.
func newTestLVSManager(t *testing.T) *lvs.Manager {
	t.Helper()
	ipvsMu.Lock()
	mgr, err := lvs.NewManager(zap.NewNop())
	if err != nil {
		ipvsMu.Unlock()
		t.Fatalf("lvs.NewManager failed: %v", err)
	}
	// Flush existing IPVS rules to ensure a clean starting state
	if err := mgr.Flush(); err != nil {
		ipvsMu.Unlock()
		t.Fatalf("failed to flush IPVS rules before test: %v", err)
	}
	// Register cleanup to flush after test and release the lock
	t.Cleanup(func() {
		mgr.Flush()
		ipvsMu.Unlock()
	})
	return mgr
}
