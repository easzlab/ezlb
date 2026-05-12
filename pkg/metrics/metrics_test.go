package metrics

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

// labelsForService is a small helper used by the delta-semantics tests.
func labelsForService(service, listen, protocol string) prometheus.Labels {
	return prometheus.Labels{
		"service":  service,
		"listen":   listen,
		"protocol": protocol,
	}
}

func labelsForBackend(service, backend, protocol string) prometheus.Labels {
	return prometheus.Labels{
		"service":  service,
		"backend":  backend,
		"protocol": protocol,
	}
}

// TestAddServiceTraffic_AccumulatesDeltas is the central P1-1 regression test:
// the metrics layer must treat its inputs as per-cycle deltas. Two successive
// Add(50) + Add(75) calls must result in a Counter value of 125, never 75.
func TestAddServiceTraffic_AccumulatesDeltas(t *testing.T) {
	const (
		svc      = "metrics-test-add-service"
		listen   = "10.0.0.1:80"
		protocol = "tcp"
	)

	AddServiceTraffic(svc, listen, protocol, 50, 100, 200, 5, 10)
	AddServiceTraffic(svc, listen, protocol, 75, 150, 300, 7, 14)

	got := testutil.ToFloat64(serviceConnectionsTotal.With(labelsForService(svc, listen, protocol)))
	if got != 125 {
		t.Errorf("expected service connections counter = 125 (50+75), got %v", got)
	}

	gotIn := testutil.ToFloat64(serviceBytesInTotal.With(labelsForService(svc, listen, protocol)))
	if gotIn != 250 {
		t.Errorf("expected service bytes_in counter = 250 (100+150), got %v", gotIn)
	}

	t.Cleanup(func() { DeleteServiceMetrics(svc, listen, protocol) })
}

func TestAddBackendTraffic_AccumulatesDeltas(t *testing.T) {
	const (
		svc      = "metrics-test-add-backend"
		backend  = "192.168.1.10:8080"
		protocol = "tcp"
	)

	AddBackendTraffic(svc, backend, protocol, 10, 1000, 2000, 5, 3)
	AddBackendTraffic(svc, backend, protocol, 5, 500, 1000, 2, 1)

	got := testutil.ToFloat64(backendConnectionsTotal.With(labelsForBackend(svc, backend, protocol)))
	if got != 15 {
		t.Errorf("expected backend connections counter = 15 (10+5), got %v", got)
	}

	gotPktsIn := testutil.ToFloat64(backendPacketsInTotal.With(labelsForBackend(svc, backend, protocol)))
	if gotPktsIn != 7 {
		t.Errorf("expected backend packets_in counter = 7 (5+2), got %v", gotPktsIn)
	}

	t.Cleanup(func() { DeleteBackendMetrics(svc, backend, protocol) })
}

// TestSetBackendConnections_OverwritesNotAccumulates verifies the Gauge
// semantic: each call replaces the previous value.
func TestSetBackendConnections_OverwritesNotAccumulates(t *testing.T) {
	const (
		svc      = "metrics-test-set-conns"
		backend  = "192.168.1.10:8080"
		protocol = "tcp"
	)

	SetBackendConnections(svc, backend, protocol, 10, 5)
	SetBackendConnections(svc, backend, protocol, 20, 7)

	active := testutil.ToFloat64(backendActiveConnections.With(labelsForBackend(svc, backend, protocol)))
	if active != 20 {
		t.Errorf("expected active connections gauge = 20 (latest), got %v", active)
	}
	inactive := testutil.ToFloat64(backendInactiveConnections.With(labelsForBackend(svc, backend, protocol)))
	if inactive != 7 {
		t.Errorf("expected inactive connections gauge = 7 (latest), got %v", inactive)
	}

	t.Cleanup(func() { DeleteBackendMetrics(svc, backend, protocol) })
}

func TestSetBackendHealth(t *testing.T) {
	const (
		svc     = "metrics-test-health"
		backend = "192.168.1.10:8080"
	)
	labels := prometheus.Labels{"service": svc, "backend": backend}

	SetBackendHealth(svc, backend, true)
	if got := testutil.ToFloat64(backendHealthStatus.With(labels)); got != 1 {
		t.Errorf("expected health=1 for healthy, got %v", got)
	}

	SetBackendHealth(svc, backend, false)
	if got := testutil.ToFloat64(backendHealthStatus.With(labels)); got != 0 {
		t.Errorf("expected health=0 for unhealthy, got %v", got)
	}

	t.Cleanup(func() {
		backendHealthStatus.Delete(labels)
	})
}

func TestIncConfigReload(t *testing.T) {
	initial := testutil.ToFloat64(configReloadTotal)
	IncConfigReload()
	if got := testutil.ToFloat64(configReloadTotal); got != initial+1 {
		t.Errorf("expected config reload counter to increment by 1, got %v -> %v", initial, got)
	}
}

func TestIncReconcileErrors(t *testing.T) {
	initial := testutil.ToFloat64(reconcileErrorsTotal)
	IncReconcileErrors()
	if got := testutil.ToFloat64(reconcileErrorsTotal); got != initial+1 {
		t.Errorf("expected reconcile errors counter to increment by 1, got %v -> %v", initial, got)
	}
}

func TestDeleteBackendMetrics(t *testing.T) {
	const (
		svc      = "metrics-test-delete-backend"
		backend  = "192.168.1.10:8080"
		protocol = "tcp"
	)

	AddBackendTraffic(svc, backend, protocol, 10, 100, 200, 5, 3)
	SetBackendConnections(svc, backend, protocol, 5, 2)
	SetBackendHealth(svc, backend, true)

	DeleteBackendMetrics(svc, backend, protocol)

	// After deletion, accessing the counter for the same labels should
	// recreate it at zero (Prometheus client behavior).
	got := testutil.ToFloat64(backendConnectionsTotal.With(labelsForBackend(svc, backend, protocol)))
	if got != 0 {
		t.Errorf("expected backend connections counter = 0 after delete (recreated), got %v", got)
	}

	t.Cleanup(func() { DeleteBackendMetrics(svc, backend, protocol) })
}

func TestDeleteServiceMetrics(t *testing.T) {
	const (
		svc      = "metrics-test-delete-service"
		listen   = "10.0.0.1:80"
		protocol = "tcp"
	)

	AddServiceTraffic(svc, listen, protocol, 100, 5000, 3000, 50, 40)
	DeleteServiceMetrics(svc, listen, protocol)

	got := testutil.ToFloat64(serviceConnectionsTotal.With(labelsForService(svc, listen, protocol)))
	if got != 0 {
		t.Errorf("expected service connections counter = 0 after delete (recreated), got %v", got)
	}

	t.Cleanup(func() { DeleteServiceMetrics(svc, listen, protocol) })
}

func TestDeleteBackendHealthMetrics(t *testing.T) {
	const (
		svc     = "metrics-test-delete-health"
		backend = "192.168.1.10:8080"
	)

	SetBackendHealth(svc, backend, true)
	labels := prometheus.Labels{"service": svc, "backend": backend}
	if got := testutil.ToFloat64(backendHealthStatus.With(labels)); got != 1 {
		t.Errorf("expected health=1 before delete, got %v", got)
	}

	DeleteBackendHealthMetrics(svc, backend)

	// After deletion, re-accessing the gauge recreates it at zero.
	if got := testutil.ToFloat64(backendHealthStatus.With(labels)); got != 0 {
		t.Errorf("expected health=0 after delete (recreated), got %v", got)
	}

	t.Cleanup(func() { backendHealthStatus.Delete(labels) })
}

func TestDeleteBackendMetrics_IncludesPackets(t *testing.T) {
	const (
		svc      = "metrics-test-delete-backend-pkts"
		backend  = "192.168.1.20:9090"
		protocol = "tcp"
	)

	AddBackendTraffic(svc, backend, protocol, 10, 100, 200, 30, 20)

	labels := labelsForBackend(svc, backend, protocol)
	if got := testutil.ToFloat64(backendPacketsInTotal.With(labels)); got != 30 {
		t.Errorf("expected packets_in=30 before delete, got %v", got)
	}

	DeleteBackendMetrics(svc, backend, protocol)

	if got := testutil.ToFloat64(backendPacketsInTotal.With(labels)); got != 0 {
		t.Errorf("expected packets_in=0 after delete (recreated), got %v", got)
	}
	if got := testutil.ToFloat64(backendPacketsOutTotal.With(labels)); got != 0 {
		t.Errorf("expected packets_out=0 after delete (recreated), got %v", got)
	}

	t.Cleanup(func() { DeleteBackendMetrics(svc, backend, protocol) })
}
