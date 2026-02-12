//go:build !integration

package lvs

import (
	"testing"

	"github.com/easzlab/ezlb/pkg/config"
	"github.com/easzlab/ezlb/pkg/snat"
)

func TestReconcile_FullNATGeneratesSNATRules(t *testing.T) {
	mgr, _, reconciler := newReconcilerTestEnv(t)
	defer mgr.Close()

	configs := []config.ServiceConfig{
		{
			Name:      "dns-svc",
			Listen:    "10.0.0.1:53",
			Protocol:  "udp",
			Scheduler: "rr",
			FullNAT:   true,
			SnatIP:    "10.0.0.1",
			HealthCheck: config.HealthCheckConfig{
				Enabled: boolPtr(false),
			},
			Backends: []config.BackendConfig{
				makeBackend("192.168.1.1:53", 1),
				makeBackend("192.168.1.2:53", 1),
			},
		},
	}

	if err := reconciler.Reconcile(configs); err != nil {
		t.Fatalf("Reconcile failed: %v", err)
	}

	// Verify IPVS service was created
	services, err := mgr.GetServices()
	if err != nil {
		t.Fatalf("GetServices failed: %v", err)
	}
	if len(services) != 1 {
		t.Fatalf("expected 1 service, got %d", len(services))
	}

	// Verify SNAT rules were created via fake manager
	fakeSnatMgr := reconciler.snatMgr.(*snat.FakeManager)
	managed := fakeSnatMgr.GetManaged()
	if len(managed) != 2 {
		t.Fatalf("expected 2 SNAT rules, got %d", len(managed))
	}
}

func TestReconcile_FullNATDisabledSkipsSNAT(t *testing.T) {
	mgr, _, reconciler := newReconcilerTestEnv(t)
	defer mgr.Close()

	configs := []config.ServiceConfig{
		{
			Name:      "web-svc",
			Listen:    "10.0.0.1:80",
			Protocol:  "tcp",
			Scheduler: "rr",
			FullNAT:   false,
			HealthCheck: config.HealthCheckConfig{
				Enabled: boolPtr(false),
			},
			Backends: []config.BackendConfig{
				makeBackend("192.168.1.1:8080", 1),
			},
		},
	}

	if err := reconciler.Reconcile(configs); err != nil {
		t.Fatalf("Reconcile failed: %v", err)
	}

	// Verify no SNAT rules were created
	fakeSnatMgr := reconciler.snatMgr.(*snat.FakeManager)
	managed := fakeSnatMgr.GetManaged()
	if len(managed) != 0 {
		t.Fatalf("expected 0 SNAT rules when full_nat is disabled, got %d", len(managed))
	}
}
