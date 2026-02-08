//go:build linux

package server

import (
	"testing"

	"github.com/easzlab/ezlb/pkg/lvs"
	"go.uber.org/zap"
)

// newTestServer creates a Server backed by the real Linux IPVS handle.
// It flushes any existing IPVS rules before and after each test to ensure isolation.
func newTestServer(t *testing.T, configPath string) *Server {
	t.Helper()
	logger := zap.NewNop()

	lvsMgr, err := lvs.NewManager(logger)
	if err != nil {
		t.Fatalf("lvs.NewManager failed: %v", err)
	}

	// Flush existing IPVS rules to ensure a clean starting state
	if err := lvsMgr.Flush(); err != nil {
		t.Fatalf("failed to flush IPVS rules before test: %v", err)
	}
	// Register cleanup to flush after test completes
	t.Cleanup(func() {
		lvsMgr.Flush()
	})

	srv, err := newServerWithManager(configPath, lvsMgr, logger)
	if err != nil {
		t.Fatalf("newServerWithManager failed: %v", err)
	}
	return srv
}

// newTestLVSManager creates an LVS Manager backed by the real Linux IPVS handle.
// It flushes any existing IPVS rules before and after each test to ensure isolation.
func newTestLVSManager(t *testing.T) *lvs.Manager {
	t.Helper()
	mgr, err := lvs.NewManager(zap.NewNop())
	if err != nil {
		t.Fatalf("lvs.NewManager failed: %v", err)
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
