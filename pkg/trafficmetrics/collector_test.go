package trafficmetrics

import (
	"fmt"
	"testing"
	"time"

	"github.com/easzlab/ezlb/pkg/config"
	"go.uber.org/zap"
)

// fakeLVSStatsProvider is a mock implementation of LVSStatsProvider for testing.
type fakeLVSStatsProvider struct {
	serviceStats map[string]ServiceTrafficStats
	backendStats map[string]BackendTrafficStats
	err          error
}

func (f *fakeLVSStatsProvider) AllStats() (map[string]ServiceTrafficStats, map[string]BackendTrafficStats, error) {
	if f.err != nil {
		return nil, nil, f.err
	}
	return f.serviceStats, f.backendStats, nil
}

func newTestGlobalConfig(interval string) config.GlobalConfig {
	return config.GlobalConfig{
		MetricsInterval: interval,
	}
}

func newTestServiceConfig(name, listen, protocol, scheduler string) config.ServiceConfig {
	return config.ServiceConfig{
		Name:      name,
		Listen:    listen,
		Protocol:  protocol,
		Scheduler: scheduler,
	}
}

func TestCollector_UpdateConfig(t *testing.T) {
	lvsProvider := &fakeLVSStatsProvider{
		serviceStats: map[string]ServiceTrafficStats{
			"10.0.0.1:80/tcp": {Connections: 100},
		},
	}

	services := []config.ServiceConfig{
		newTestServiceConfig("web", "10.0.0.1:80", "tcp", "rr"),
	}

	c := NewCollector(lvsProvider, zap.NewNop(), services, newTestGlobalConfig("15s"))

	newServices := []config.ServiceConfig{
		newTestServiceConfig("web", "10.0.0.1:80", "tcp", "rr"),
		newTestServiceConfig("api", "10.0.0.2:443", "tcp", "wrr"),
	}

	c.UpdateConfig(newServices, newTestGlobalConfig("30s"))

	c.mu.RLock()
	defer c.mu.RUnlock()

	if len(c.services) != 2 {
		t.Errorf("expected 2 services after update, got %d", len(c.services))
	}
	if c.globalCfg.GetMetricsInterval() != 30*time.Second {
		t.Errorf("expected interval 30s after update, got %v", c.globalCfg.GetMetricsInterval())
	}
}

func TestCollector_StartStop(t *testing.T) {
	lvsProvider := &fakeLVSStatsProvider{
		serviceStats: make(map[string]ServiceTrafficStats),
	}

	c := NewCollector(lvsProvider, zap.NewNop(), nil, newTestGlobalConfig("5s"))

	c.Start()
	time.Sleep(50 * time.Millisecond)
	c.Stop()

	select {
	case <-c.stopped:
		// OK
	default:
		t.Error("stopped channel should be closed after Stop()")
	}
}

func TestBuildServiceConfigMap(t *testing.T) {
	services := []config.ServiceConfig{
		newTestServiceConfig("web", "10.0.0.1:80", "tcp", "rr"),
		newTestServiceConfig("api", "10.0.0.2:443", "tcp", "wrr"),
		newTestServiceConfig("dns", "10.0.0.3:53", "udp", "rr"),
	}

	result := buildServiceConfigMap(services)

	if len(result) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(result))
	}
	if svc, ok := result["10.0.0.1:80/tcp"]; !ok || svc.Name != "web" {
		t.Errorf("expected key '10.0.0.1:80/tcp' -> 'web', got %+v", svc)
	}
	if _, ok := result["10.0.0.3:53/udp"]; !ok {
		t.Error("expected key '10.0.0.3:53/udp'")
	}
}

func TestCollector_StatsProviderError(t *testing.T) {
	lvsProvider := &fakeLVSStatsProvider{
		err: fmt.Errorf("ipvs connection failed"),
	}

	c := NewCollector(lvsProvider, zap.NewNop(), nil, newTestGlobalConfig("15s"))

	// Must not panic when both stats providers fail.
	c.collect()
}

// TestCollector_CounterDelta verifies the central P1 fix: Counter metrics
// receive per-cycle deltas, not cumulative absolute values. We verify by
// inspecting the prev snapshot retained between collect() calls.
func TestCollector_CounterDelta(t *testing.T) {
	lvsProvider := &fakeLVSStatsProvider{
		serviceStats: map[string]ServiceTrafficStats{
			"10.0.0.1:80/tcp": {Connections: 100, InBytes: 1000},
		},
	}

	services := []config.ServiceConfig{
		newTestServiceConfig("web", "10.0.0.1:80", "tcp", "rr"),
	}

	c := NewCollector(lvsProvider, zap.NewNop(), services, newTestGlobalConfig("15s"))

	// First cycle: prev is nil, delta == current cumulative value.
	c.collect()

	c.mu.RLock()
	if c.prev == nil {
		c.mu.RUnlock()
		t.Fatal("expected prev snapshot to be retained after first collect()")
	}
	first := c.prev.Services["10.0.0.1:80/tcp"]
	c.mu.RUnlock()

	if first.Connections != 100 {
		t.Errorf("expected first snapshot Connections=100, got %d", first.Connections)
	}

	// Advance the cumulative counters and collect again.
	lvsProvider.serviceStats["10.0.0.1:80/tcp"] = ServiceTrafficStats{
		Connections: 150,
		InBytes:     2500,
	}
	c.collect()

	c.mu.RLock()
	second := c.prev.Services["10.0.0.1:80/tcp"]
	c.mu.RUnlock()

	if second.Connections != 150 {
		t.Errorf("expected second snapshot Connections=150, got %d", second.Connections)
	}
}

func TestCounterDelta(t *testing.T) {
	tests := []struct {
		name string
		curr uint64
		prev uint64
		want uint64
	}{
		{"normal increase", 100, 50, 50},
		{"no change", 100, 100, 0},
		{"counter reset", 5, 100, 5},
		{"zero curr after reset", 0, 100, 0},
		{"first cycle (prev=0)", 100, 0, 100},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := counterDelta(tt.curr, tt.prev); got != tt.want {
				t.Errorf("counterDelta(%d,%d) = %d, want %d", tt.curr, tt.prev, got, tt.want)
			}
		})
	}
}

func TestExtractBackendAddress(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"10.0.0.1:80/tcp->192.168.1.1:8080", "192.168.1.1:8080"},
		{"plainkey", "plainkey"},
		{"a->b->c", "a->b->c"}, // ambiguous, fallback to full key
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			if got := extractBackendAddress(tt.input); got != tt.want {
				t.Errorf("extractBackendAddress(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestCollector_BackendPacketsDelta(t *testing.T) {
	lvsProvider := &fakeLVSStatsProvider{
		serviceStats: map[string]ServiceTrafficStats{
			"10.0.0.1:80/tcp": {Connections: 10},
		},
		backendStats: map[string]BackendTrafficStats{
			"10.0.0.1:80/tcp->192.168.1.1:8080": {
				ServiceKey:  "10.0.0.1:80/tcp",
				Connections: 50,
				InPkts:      100,
				OutPkts:     75,
				InBytes:     25000,
				OutBytes:    15000,
			},
		},
	}

	services := []config.ServiceConfig{
		newTestServiceConfig("web", "10.0.0.1:80", "tcp", "rr"),
	}

	c := NewCollector(lvsProvider, zap.NewNop(), services, newTestGlobalConfig("15s"))

	// First cycle: establishes baseline.
	c.collect()

	c.mu.RLock()
	if c.prev == nil {
		c.mu.RUnlock()
		t.Fatal("expected prev snapshot after first collect()")
	}
	prevBackend := c.prev.Backends["10.0.0.1:80/tcp->192.168.1.1:8080"]
	c.mu.RUnlock()

	if prevBackend.InPkts != 100 {
		t.Errorf("expected first snapshot InPkts=100, got %d", prevBackend.InPkts)
	}

	// Second cycle: advance counters.
	lvsProvider.backendStats["10.0.0.1:80/tcp->192.168.1.1:8080"] = BackendTrafficStats{
		ServiceKey:  "10.0.0.1:80/tcp",
		Connections: 80,
		InPkts:      160,
		OutPkts:     120,
		InBytes:     40000,
		OutBytes:    30000,
	}
	c.collect()

	c.mu.RLock()
	secondBackend := c.prev.Backends["10.0.0.1:80/tcp->192.168.1.1:8080"]
	c.mu.RUnlock()

	if secondBackend.InPkts != 160 {
		t.Errorf("expected second snapshot InPkts=160, got %d", secondBackend.InPkts)
	}
	if secondBackend.OutPkts != 120 {
		t.Errorf("expected second snapshot OutPkts=120, got %d", secondBackend.OutPkts)
	}
}
