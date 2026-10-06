//go:build !linux || fake || integration

package lvs

import (
	"syscall"
	"testing"

	"github.com/easzlab/ezlb/pkg/config"
	"github.com/easzlab/ezlb/pkg/metrics"
	"github.com/easzlab/ezlb/pkg/snat"
	"github.com/prometheus/client_golang/prometheus"
	"go.uber.org/zap"
)

// mockHealthChecker is a test double for the HealthChecker interface.
type mockHealthChecker struct {
	status map[string]bool
}

func newMockHealthChecker() *mockHealthChecker {
	return &mockHealthChecker{
		status: make(map[string]bool),
	}
}

func (m *mockHealthChecker) IsHealthy(_ string, address string) bool {
	healthy, ok := m.status[address]
	if !ok {
		return true
	}
	return healthy
}

// boolPtr creates a pointer to a bool value.
func boolPtr(b bool) *bool {
	return &b
}

// newReconcilerTestEnv creates a Manager, mock HealthChecker, and Reconciler for testing.
// It uses newTestManager which handles platform-specific setup and IPVS cleanup.
func newReconcilerTestEnv(t *testing.T) (*Manager, *mockHealthChecker, *Reconciler) {
	t.Helper()
	mgr := newTestManager(t)
	healthMgr := newMockHealthChecker()
	snatMgr, _ := snat.NewManager(zap.NewNop())
	reconciler := NewReconciler(mgr, healthMgr, snatMgr, zap.NewNop())
	return mgr, healthMgr, reconciler
}

// makeServiceConfig creates a ServiceConfig for testing.
func makeServiceConfig(name, listen, scheduler string, healthEnabled bool, backends ...config.BackendConfig) config.ServiceConfig {
	return config.ServiceConfig{
		Name:      name,
		Listen:    listen,
		Protocol:  "tcp",
		Scheduler: scheduler,
		HealthCheck: config.HealthCheckConfig{
			Enabled: boolPtr(healthEnabled),
		},
		Backends: backends,
	}
}

// makeBackend creates a BackendConfig for testing.
func makeBackend(address string, weight int) config.BackendConfig {
	return config.BackendConfig{
		Address: address,
		Weight:  weight,
	}
}

// --- First Reconcile (empty IPVS -> create) ---

func TestReconcile_SingleServiceSingleBackend(t *testing.T) {
	mgr, healthMgr, reconciler := newReconcilerTestEnv(t)
	defer mgr.Close()

	healthMgr.status["192.168.1.1:8080"] = true

	configs := []config.ServiceConfig{
		makeServiceConfig("svc1", "10.0.0.1:80", "rr", true,
			makeBackend("192.168.1.1:8080", 5)),
	}

	if err := reconciler.Reconcile(configs); err != nil {
		t.Fatalf("Reconcile failed: %v", err)
	}

	services, err := mgr.GetServices()
	if err != nil {
		t.Fatalf("GetServices failed: %v", err)
	}
	if len(services) != 1 {
		t.Fatalf("expected 1 service, got %d", len(services))
	}

	dests, err := mgr.GetDestinations(services[0])
	if err != nil {
		t.Fatalf("GetDestinations failed: %v", err)
	}
	if len(dests) != 1 {
		t.Fatalf("expected 1 destination, got %d", len(dests))
	}
	if dests[0].Weight != 5 {
		t.Errorf("expected weight 5, got %d", dests[0].Weight)
	}
}

func TestReconcile_SingleServiceMultiBackend(t *testing.T) {
	mgr, healthMgr, reconciler := newReconcilerTestEnv(t)
	defer mgr.Close()

	healthMgr.status["192.168.1.1:8080"] = true
	healthMgr.status["192.168.1.2:8080"] = true
	healthMgr.status["192.168.1.3:8080"] = true

	configs := []config.ServiceConfig{
		makeServiceConfig("svc1", "10.0.0.1:80", "wrr", true,
			makeBackend("192.168.1.1:8080", 5),
			makeBackend("192.168.1.2:8080", 3),
			makeBackend("192.168.1.3:8080", 2)),
	}

	if err := reconciler.Reconcile(configs); err != nil {
		t.Fatalf("Reconcile failed: %v", err)
	}

	services, _ := mgr.GetServices()
	dests, _ := mgr.GetDestinations(services[0])
	if len(dests) != 3 {
		t.Fatalf("expected 3 destinations, got %d", len(dests))
	}
}

func TestReconcile_MultiService(t *testing.T) {
	mgr, healthMgr, reconciler := newReconcilerTestEnv(t)
	defer mgr.Close()

	healthMgr.status["192.168.1.1:8080"] = true
	healthMgr.status["192.168.2.1:9090"] = true

	configs := []config.ServiceConfig{
		makeServiceConfig("svc1", "10.0.0.1:80", "rr", true,
			makeBackend("192.168.1.1:8080", 1)),
		makeServiceConfig("svc2", "10.0.0.2:443", "wrr", true,
			makeBackend("192.168.2.1:9090", 2)),
	}

	if err := reconciler.Reconcile(configs); err != nil {
		t.Fatalf("Reconcile failed: %v", err)
	}

	services, _ := mgr.GetServices()
	if len(services) != 2 {
		t.Fatalf("expected 2 services, got %d", len(services))
	}
}

// --- Idempotency ---

func TestReconcile_Idempotent(t *testing.T) {
	mgr, healthMgr, reconciler := newReconcilerTestEnv(t)
	defer mgr.Close()

	healthMgr.status["192.168.1.1:8080"] = true

	configs := []config.ServiceConfig{
		makeServiceConfig("svc1", "10.0.0.1:80", "rr", true,
			makeBackend("192.168.1.1:8080", 5)),
	}

	if err := reconciler.Reconcile(configs); err != nil {
		t.Fatalf("first Reconcile failed: %v", err)
	}

	// Second reconcile with same config should be a no-op
	if err := reconciler.Reconcile(configs); err != nil {
		t.Fatalf("second Reconcile failed: %v", err)
	}

	services, _ := mgr.GetServices()
	if len(services) != 1 {
		t.Fatalf("expected 1 service after idempotent reconcile, got %d", len(services))
	}

	dests, _ := mgr.GetDestinations(services[0])
	if len(dests) != 1 {
		t.Fatalf("expected 1 destination after idempotent reconcile, got %d", len(dests))
	}
}

// --- Service-level diff ---

func TestReconcile_AddService(t *testing.T) {
	mgr, healthMgr, reconciler := newReconcilerTestEnv(t)
	defer mgr.Close()

	healthMgr.status["192.168.1.1:8080"] = true
	healthMgr.status["192.168.2.1:9090"] = true

	// First reconcile with 1 service
	configs1 := []config.ServiceConfig{
		makeServiceConfig("svc1", "10.0.0.1:80", "rr", true,
			makeBackend("192.168.1.1:8080", 1)),
	}
	if err := reconciler.Reconcile(configs1); err != nil {
		t.Fatalf("first Reconcile failed: %v", err)
	}

	// Second reconcile adds a new service
	configs2 := []config.ServiceConfig{
		makeServiceConfig("svc1", "10.0.0.1:80", "rr", true,
			makeBackend("192.168.1.1:8080", 1)),
		makeServiceConfig("svc2", "10.0.0.2:443", "wrr", true,
			makeBackend("192.168.2.1:9090", 2)),
	}
	if err := reconciler.Reconcile(configs2); err != nil {
		t.Fatalf("second Reconcile failed: %v", err)
	}

	services, _ := mgr.GetServices()
	if len(services) != 2 {
		t.Fatalf("expected 2 services, got %d", len(services))
	}
}

func TestReconcile_DeleteService(t *testing.T) {
	mgr, healthMgr, reconciler := newReconcilerTestEnv(t)
	defer mgr.Close()

	healthMgr.status["192.168.1.1:8080"] = true
	healthMgr.status["192.168.2.1:9090"] = true

	// First reconcile with 2 services
	configs1 := []config.ServiceConfig{
		makeServiceConfig("svc1", "10.0.0.1:80", "rr", true,
			makeBackend("192.168.1.1:8080", 1)),
		makeServiceConfig("svc2", "10.0.0.2:443", "wrr", true,
			makeBackend("192.168.2.1:9090", 2)),
	}
	if err := reconciler.Reconcile(configs1); err != nil {
		t.Fatalf("first Reconcile failed: %v", err)
	}

	// Second reconcile removes svc2
	configs2 := []config.ServiceConfig{
		makeServiceConfig("svc1", "10.0.0.1:80", "rr", true,
			makeBackend("192.168.1.1:8080", 1)),
	}
	if err := reconciler.Reconcile(configs2); err != nil {
		t.Fatalf("second Reconcile failed: %v", err)
	}

	services, _ := mgr.GetServices()
	if len(services) != 1 {
		t.Fatalf("expected 1 service after deletion, got %d", len(services))
	}
}

func TestReconcile_UpdateScheduler(t *testing.T) {
	mgr, healthMgr, reconciler := newReconcilerTestEnv(t)
	defer mgr.Close()

	healthMgr.status["192.168.1.1:8080"] = true

	configs1 := []config.ServiceConfig{
		makeServiceConfig("svc1", "10.0.0.1:80", "rr", true,
			makeBackend("192.168.1.1:8080", 1)),
	}
	if err := reconciler.Reconcile(configs1); err != nil {
		t.Fatalf("first Reconcile failed: %v", err)
	}

	// Change scheduler from rr to wrr
	configs2 := []config.ServiceConfig{
		makeServiceConfig("svc1", "10.0.0.1:80", "wrr", true,
			makeBackend("192.168.1.1:8080", 1)),
	}
	if err := reconciler.Reconcile(configs2); err != nil {
		t.Fatalf("second Reconcile failed: %v", err)
	}

	services, _ := mgr.GetServices()
	if services[0].SchedName != "wrr" {
		t.Errorf("expected scheduler 'wrr', got %q", services[0].SchedName)
	}
}

// --- Destination-level diff ---

func TestReconcile_AddBackend(t *testing.T) {
	mgr, healthMgr, reconciler := newReconcilerTestEnv(t)
	defer mgr.Close()

	healthMgr.status["192.168.1.1:8080"] = true
	healthMgr.status["192.168.1.2:8080"] = true

	configs1 := []config.ServiceConfig{
		makeServiceConfig("svc1", "10.0.0.1:80", "rr", true,
			makeBackend("192.168.1.1:8080", 1)),
	}
	if err := reconciler.Reconcile(configs1); err != nil {
		t.Fatalf("first Reconcile failed: %v", err)
	}

	// Add a second backend
	configs2 := []config.ServiceConfig{
		makeServiceConfig("svc1", "10.0.0.1:80", "rr", true,
			makeBackend("192.168.1.1:8080", 1),
			makeBackend("192.168.1.2:8080", 3)),
	}
	if err := reconciler.Reconcile(configs2); err != nil {
		t.Fatalf("second Reconcile failed: %v", err)
	}

	services, _ := mgr.GetServices()
	dests, _ := mgr.GetDestinations(services[0])
	if len(dests) != 2 {
		t.Fatalf("expected 2 destinations after adding backend, got %d", len(dests))
	}
}

func TestReconcile_DeleteBackend(t *testing.T) {
	mgr, healthMgr, reconciler := newReconcilerTestEnv(t)
	defer mgr.Close()

	healthMgr.status["192.168.1.1:8080"] = true
	healthMgr.status["192.168.1.2:8080"] = true

	configs1 := []config.ServiceConfig{
		makeServiceConfig("svc1", "10.0.0.1:80", "rr", true,
			makeBackend("192.168.1.1:8080", 1),
			makeBackend("192.168.1.2:8080", 3)),
	}
	if err := reconciler.Reconcile(configs1); err != nil {
		t.Fatalf("first Reconcile failed: %v", err)
	}

	// Remove second backend
	configs2 := []config.ServiceConfig{
		makeServiceConfig("svc1", "10.0.0.1:80", "rr", true,
			makeBackend("192.168.1.1:8080", 1)),
	}
	if err := reconciler.Reconcile(configs2); err != nil {
		t.Fatalf("second Reconcile failed: %v", err)
	}

	services, _ := mgr.GetServices()
	dests, _ := mgr.GetDestinations(services[0])
	if len(dests) != 1 {
		t.Fatalf("expected 1 destination after removing backend, got %d", len(dests))
	}
}

func TestReconcile_UpdateWeight(t *testing.T) {
	mgr, healthMgr, reconciler := newReconcilerTestEnv(t)
	defer mgr.Close()

	healthMgr.status["192.168.1.1:8080"] = true

	configs1 := []config.ServiceConfig{
		makeServiceConfig("svc1", "10.0.0.1:80", "rr", true,
			makeBackend("192.168.1.1:8080", 5)),
	}
	if err := reconciler.Reconcile(configs1); err != nil {
		t.Fatalf("first Reconcile failed: %v", err)
	}

	// Change weight from 5 to 10
	configs2 := []config.ServiceConfig{
		makeServiceConfig("svc1", "10.0.0.1:80", "rr", true,
			makeBackend("192.168.1.1:8080", 10)),
	}
	if err := reconciler.Reconcile(configs2); err != nil {
		t.Fatalf("second Reconcile failed: %v", err)
	}

	services, _ := mgr.GetServices()
	dests, _ := mgr.GetDestinations(services[0])
	if dests[0].Weight != 10 {
		t.Errorf("expected weight 10, got %d", dests[0].Weight)
	}
}

// --- Health check filtering ---

func TestReconcile_HealthCheckEnabled_UnhealthyBackendExcluded(t *testing.T) {
	mgr, healthMgr, reconciler := newReconcilerTestEnv(t)
	defer mgr.Close()

	healthMgr.status["192.168.1.1:8080"] = true
	healthMgr.status["192.168.1.2:8080"] = false // unhealthy

	configs := []config.ServiceConfig{
		makeServiceConfig("svc1", "10.0.0.1:80", "rr", true,
			makeBackend("192.168.1.1:8080", 1),
			makeBackend("192.168.1.2:8080", 1)),
	}

	if err := reconciler.Reconcile(configs); err != nil {
		t.Fatalf("Reconcile failed: %v", err)
	}

	services, _ := mgr.GetServices()
	dests, _ := mgr.GetDestinations(services[0])
	if len(dests) != 1 {
		t.Fatalf("expected 1 destination (unhealthy excluded), got %d", len(dests))
	}
}

func TestReconcile_HealthCheckEnabled_AllHealthy(t *testing.T) {
	mgr, healthMgr, reconciler := newReconcilerTestEnv(t)
	defer mgr.Close()

	healthMgr.status["192.168.1.1:8080"] = true
	healthMgr.status["192.168.1.2:8080"] = true

	configs := []config.ServiceConfig{
		makeServiceConfig("svc1", "10.0.0.1:80", "rr", true,
			makeBackend("192.168.1.1:8080", 1),
			makeBackend("192.168.1.2:8080", 1)),
	}

	if err := reconciler.Reconcile(configs); err != nil {
		t.Fatalf("Reconcile failed: %v", err)
	}

	services, _ := mgr.GetServices()
	dests, _ := mgr.GetDestinations(services[0])
	if len(dests) != 2 {
		t.Fatalf("expected 2 destinations (all healthy), got %d", len(dests))
	}
}

func TestReconcile_HealthCheckDisabled_AllBackendsIncluded(t *testing.T) {
	mgr, healthMgr, reconciler := newReconcilerTestEnv(t)
	defer mgr.Close()

	// Even though healthMgr says unhealthy, health check is disabled
	healthMgr.status["192.168.1.1:8080"] = false
	healthMgr.status["192.168.1.2:8080"] = false

	configs := []config.ServiceConfig{
		makeServiceConfig("svc1", "10.0.0.1:80", "rr", false,
			makeBackend("192.168.1.1:8080", 1),
			makeBackend("192.168.1.2:8080", 1)),
	}

	if err := reconciler.Reconcile(configs); err != nil {
		t.Fatalf("Reconcile failed: %v", err)
	}

	services, _ := mgr.GetServices()
	dests, _ := mgr.GetDestinations(services[0])
	if len(dests) != 2 {
		t.Fatalf("expected 2 destinations (health check disabled), got %d", len(dests))
	}
}

func TestReconcile_BackendRecovery(t *testing.T) {
	mgr, healthMgr, reconciler := newReconcilerTestEnv(t)
	defer mgr.Close()

	healthMgr.status["192.168.1.1:8080"] = true
	healthMgr.status["192.168.1.2:8080"] = false // initially unhealthy

	configs := []config.ServiceConfig{
		makeServiceConfig("svc1", "10.0.0.1:80", "rr", true,
			makeBackend("192.168.1.1:8080", 1),
			makeBackend("192.168.1.2:8080", 1)),
	}

	// First reconcile: only 1 destination (second is unhealthy)
	if err := reconciler.Reconcile(configs); err != nil {
		t.Fatalf("first Reconcile failed: %v", err)
	}

	services, _ := mgr.GetServices()
	dests, _ := mgr.GetDestinations(services[0])
	if len(dests) != 1 {
		t.Fatalf("expected 1 destination before recovery, got %d", len(dests))
	}

	// Mark backend as healthy and reconcile again
	healthMgr.status["192.168.1.2:8080"] = true
	if err := reconciler.Reconcile(configs); err != nil {
		t.Fatalf("second Reconcile failed: %v", err)
	}

	services, _ = mgr.GetServices()
	dests, _ = mgr.GetDestinations(services[0])
	if len(dests) != 2 {
		t.Fatalf("expected 2 destinations after recovery, got %d", len(dests))
	}
}

// --- UDP protocol tests ---

func TestReconcile_UDPService(t *testing.T) {
	mgr, _, reconciler := newReconcilerTestEnv(t)
	defer mgr.Close()

	configs := []config.ServiceConfig{
		{
			Name:      "dns-svc",
			Listen:    "10.0.0.1:53",
			Protocol:  "udp",
			Scheduler: "rr",
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

	services, err := mgr.GetServices()
	if err != nil {
		t.Fatalf("GetServices failed: %v", err)
	}
	if len(services) != 1 {
		t.Fatalf("expected 1 service, got %d", len(services))
	}
	if services[0].Protocol != syscall.IPPROTO_UDP {
		t.Errorf("expected protocol IPPROTO_UDP (%d), got %d", syscall.IPPROTO_UDP, services[0].Protocol)
	}

	dests, err := mgr.GetDestinations(services[0])
	if err != nil {
		t.Fatalf("GetDestinations failed: %v", err)
	}
	if len(dests) != 2 {
		t.Fatalf("expected 2 destinations, got %d", len(dests))
	}
}

func TestReconcile_TCPAndUDPSameAddress(t *testing.T) {
	mgr, _, reconciler := newReconcilerTestEnv(t)
	defer mgr.Close()

	configs := []config.ServiceConfig{
		{
			Name:      "dns-tcp",
			Listen:    "10.0.0.1:53",
			Protocol:  "tcp",
			Scheduler: "rr",
			HealthCheck: config.HealthCheckConfig{
				Enabled: boolPtr(false),
			},
			Backends: []config.BackendConfig{
				makeBackend("192.168.1.1:53", 1),
			},
		},
		{
			Name:      "dns-udp",
			Listen:    "10.0.0.1:53",
			Protocol:  "udp",
			Scheduler: "rr",
			HealthCheck: config.HealthCheckConfig{
				Enabled: boolPtr(false),
			},
			Backends: []config.BackendConfig{
				makeBackend("192.168.1.2:53", 2),
			},
		},
	}

	if err := reconciler.Reconcile(configs); err != nil {
		t.Fatalf("Reconcile failed: %v", err)
	}

	services, err := mgr.GetServices()
	if err != nil {
		t.Fatalf("GetServices failed: %v", err)
	}
	if len(services) != 2 {
		t.Fatalf("expected 2 services (TCP + UDP), got %d", len(services))
	}

	// Verify each service has its own destinations
	for _, svc := range services {
		dests, err := mgr.GetDestinations(svc)
		if err != nil {
			t.Fatalf("GetDestinations failed: %v", err)
		}
		if len(dests) != 1 {
			t.Errorf("expected 1 destination per service, got %d for protocol %d", len(dests), svc.Protocol)
		}
	}
}

// --- Error handling ---

func TestReconcile_InvalidListenAddress(t *testing.T) {
	mgr, _, reconciler := newReconcilerTestEnv(t)
	defer mgr.Close()

	configs := []config.ServiceConfig{
		makeServiceConfig("svc1", "invalid-address", "rr", false,
			makeBackend("192.168.1.1:8080", 1)),
	}

	err := reconciler.Reconcile(configs)
	if err == nil {
		t.Fatal("expected error for invalid listen address, got nil")
	}
}

// --- Reconciler.Cleanup tests ---

func TestReconciler_Cleanup_RemovesManagedServices(t *testing.T) {
	mgr, healthMgr, reconciler := newReconcilerTestEnv(t)
	defer mgr.Close()

	healthMgr.status["192.168.1.1:8080"] = true
	healthMgr.status["192.168.2.1:9090"] = true

	configs := []config.ServiceConfig{
		makeServiceConfig("svc1", "10.0.0.1:80", "rr", false,
			makeBackend("192.168.1.1:8080", 1)),
		makeServiceConfig("svc2", "10.0.0.2:443", "wrr", false,
			makeBackend("192.168.2.1:9090", 1)),
	}

	if err := reconciler.Reconcile(configs); err != nil {
		t.Fatalf("Reconcile failed: %v", err)
	}

	services, _ := mgr.GetServices()
	if len(services) != 2 {
		t.Fatalf("expected 2 services before cleanup, got %d", len(services))
	}

	if err := reconciler.Cleanup(); err != nil {
		t.Fatalf("Cleanup failed: %v", err)
	}

	services, _ = mgr.GetServices()
	if len(services) != 0 {
		t.Fatalf("expected 0 services after cleanup, got %d", len(services))
	}
}

func TestReconciler_ExclusiveNamespace_RemovesUnconfiguredService(t *testing.T) {
	mgr, healthMgr, reconciler := newReconcilerTestEnv(t)
	defer mgr.Close()

	// A service left in the namespace by a previous ezlb process.
	unmanaged := newTestService("10.99.0.1", 9999, 6, "rr")
	if err := mgr.CreateService(unmanaged); err != nil {
		t.Fatalf("failed to create unmanaged service: %v", err)
	}

	// Reconcile the current configuration, pruning the old service.
	healthMgr.status["192.168.1.1:8080"] = true
	configs := []config.ServiceConfig{
		makeServiceConfig("svc1", "10.0.0.1:80", "rr", false,
			makeBackend("192.168.1.1:8080", 1)),
	}
	if err := reconciler.Reconcile(configs); err != nil {
		t.Fatalf("Reconcile failed: %v", err)
	}

	services, _ := mgr.GetServices()
	if len(services) != 1 {
		t.Fatalf("expected 1 service after reconcile, got %d", len(services))
	}
	if services[0].Address.Equal(unmanaged.Address) {
		t.Fatal("old unconfigured service was not removed")
	}

	// Cleanup owns all remaining services in the namespace.
	if err := reconciler.Cleanup(); err != nil {
		t.Fatalf("Cleanup failed: %v", err)
	}

	services, _ = mgr.GetServices()
	if len(services) != 0 {
		t.Fatalf("expected empty namespace after cleanup, got %d services", len(services))
	}
}

func TestReconciler_SharedNamespacePreservesUnmanagedServices(t *testing.T) {
	mgr := newTestManager(t)
	defer mgr.Close()

	healthMgr := newMockHealthChecker()
	snatMgr, _ := snat.NewManager(zap.NewNop())
	reconciler := NewSharedNetnsReconciler(mgr, healthMgr, snatMgr, zap.NewNop())

	// Simulate an IPVS service owned by kube-proxy before ezlb starts.
	unmanaged := newTestService("10.96.0.10", 443, syscall.IPPROTO_TCP, "rr")
	if err := mgr.CreateService(unmanaged); err != nil {
		t.Fatalf("failed to create unmanaged service: %v", err)
	}

	configs := []config.ServiceConfig{
		makeServiceConfig("kube-apiserver", "127.0.0.1:6443", "rr", false,
			makeBackend("192.168.1.1:6443", 1)),
	}
	if err := reconciler.Reconcile(configs); err != nil {
		t.Fatalf("Reconcile failed: %v", err)
	}

	services, err := mgr.GetServices()
	if err != nil {
		t.Fatalf("GetServices failed: %v", err)
	}
	if len(services) != 2 {
		t.Fatalf("expected shared namespace to retain both services, got %d", len(services))
	}

	// Removing ezlb's service from its desired state must leave kube-proxy's
	// service untouched.
	if err := reconciler.Reconcile(nil); err != nil {
		t.Fatalf("Reconcile removal failed: %v", err)
	}
	services, err = mgr.GetServices()
	if err != nil {
		t.Fatalf("GetServices after removal failed: %v", err)
	}
	if len(services) != 1 || !services[0].Address.Equal(unmanaged.Address) || services[0].Port != unmanaged.Port {
		t.Fatalf("shared namespace did not preserve unmanaged service: %#v", services)
	}

	if err := reconciler.Cleanup(); err != nil {
		t.Fatalf("Cleanup failed: %v", err)
	}
	services, err = mgr.GetServices()
	if err != nil {
		t.Fatalf("GetServices after cleanup failed: %v", err)
	}
	if len(services) != 1 || !services[0].Address.Equal(unmanaged.Address) || services[0].Port != unmanaged.Port {
		t.Fatalf("Cleanup removed an unmanaged service: %#v", services)
	}
}

func TestReconcile_UnhealthyBackendKeepsHealthMetric(t *testing.T) {
	mgr, health, reconciler := newReconcilerTestEnv(t)
	defer mgr.Close()
	const service = "health-metric-reconcile-test"
	const backend = "192.168.1.31:8080"
	configs := []config.ServiceConfig{
		makeServiceConfig(service, "10.0.0.31:80", "rr", true, makeBackend(backend, 1)),
	}
	t.Cleanup(func() { metrics.DeleteBackendMetrics(service, backend, "tcp") })
	if err := reconciler.Reconcile(configs); err != nil {
		t.Fatal(err)
	}
	metrics.SetBackendHealth(service, backend, true)
	health.status[backend] = false
	metrics.SetBackendHealth(service, backend, false)
	if err := reconciler.Reconcile(configs); err != nil {
		t.Fatal(err)
	}
	if value, found := metricValue(t, "ezlb_backend_health_status", map[string]string{"service": service, "backend": backend}); !found || value != 0 {
		t.Fatalf("unhealthy backend must retain health=0, found=%v value=%v", found, value)
	}
}

func TestReconcile_ServiceRenameRemovesOldMetrics(t *testing.T) {
	mgr, _, reconciler := newReconcilerTestEnv(t)
	defer mgr.Close()
	const oldName = "metric-rename-old"
	const newName = "metric-rename-new"
	const listen = "10.0.0.32:80"
	const backend = "192.168.1.32:8080"
	configs := []config.ServiceConfig{
		makeServiceConfig(oldName, listen, "rr", false, makeBackend(backend, 1)),
	}
	t.Cleanup(func() {
		metrics.DeleteServiceMetrics(oldName, listen, "tcp")
		metrics.DeleteServiceMetrics(newName, listen, "tcp")
		metrics.DeleteBackendMetrics(oldName, backend, "tcp")
		metrics.DeleteBackendMetrics(newName, backend, "tcp")
	})
	if err := reconciler.Reconcile(configs); err != nil {
		t.Fatal(err)
	}
	metrics.AddServiceTraffic(oldName, listen, "tcp", 1, 0, 0, 0, 0)
	metrics.AddBackendTraffic(oldName, backend, "tcp", 1, 0, 0, 0, 0)
	configs[0].Name = newName
	if err := reconciler.Reconcile(configs); err != nil {
		t.Fatal(err)
	}
	if _, found := metricValue(t, "ezlb_service_connections_total", map[string]string{"service": oldName}); found {
		t.Fatal("old service metrics remained after rename")
	}
	if _, found := metricValue(t, "ezlb_backend_connections_total", map[string]string{"service": oldName}); found {
		t.Fatal("old backend metrics remained after service rename")
	}
}

func metricValue(t *testing.T, name string, wantLabels map[string]string) (float64, bool) {
	t.Helper()
	families, err := prometheus.DefaultGatherer.Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, family := range families {
		if family.GetName() != name {
			continue
		}
		for _, metric := range family.Metric {
			labels := make(map[string]string, len(metric.Label))
			for _, label := range metric.Label {
				labels[label.GetName()] = label.GetValue()
			}
			matched := true
			for key, value := range wantLabels {
				if labels[key] != value {
					matched = false
					break
				}
			}
			if matched {
				if metric.Gauge != nil {
					return metric.Gauge.GetValue(), true
				}
				return metric.Counter.GetValue(), true
			}
		}
	}
	return 0, false
}

func TestReconciler_Cleanup_EmptyManaged(t *testing.T) {
	mgr, _, reconciler := newReconcilerTestEnv(t)
	defer mgr.Close()

	// No reconcile performed — managed map is empty
	if err := reconciler.Cleanup(); err != nil {
		t.Fatalf("Cleanup on empty managed map should not fail, got: %v", err)
	}

	services, _ := mgr.GetServices()
	if len(services) != 0 {
		t.Fatalf("expected 0 services after cleanup of empty managed, got %d", len(services))
	}
}

func TestReconciler_Cleanup_WithFullNATService(t *testing.T) {
	mgr, _, reconciler := newReconcilerTestEnv(t)
	defer mgr.Close()

	// FullNAT service with health check disabled
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

	services, _ := mgr.GetServices()
	if len(services) != 1 {
		t.Fatalf("expected 1 IPVS service before cleanup, got %d", len(services))
	}

	// Cleanup should remove the IPVS service (SNAT cleanup is snatMgr's responsibility)
	if err := reconciler.Cleanup(); err != nil {
		t.Fatalf("Cleanup failed: %v", err)
	}

	services, _ = mgr.GetServices()
	if len(services) != 0 {
		t.Fatalf("expected 0 IPVS services after cleanup, got %d", len(services))
	}
}
