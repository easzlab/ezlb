package trafficlog

import (
	"fmt"
	"testing"
	"time"

	"github.com/easzlab/ezlb/pkg/config"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

// fakeLVSStatsProvider is a mock implementation of LVSStatsProvider for testing.
type fakeLVSStatsProvider struct {
	serviceStats map[string]ServiceTrafficStats
	backendStats map[string]BackendTrafficStats
	serviceErr   error
	backendErr   error
}

func (f *fakeLVSStatsProvider) ServiceStats() (map[string]ServiceTrafficStats, error) {
	if f.serviceErr != nil {
		return nil, f.serviceErr
	}
	return f.serviceStats, nil
}

func (f *fakeLVSStatsProvider) BackendStats() (map[string]BackendTrafficStats, error) {
	if f.backendErr != nil {
		return nil, f.backendErr
	}
	return f.backendStats, nil
}

// fakeSNATStatsProvider is a mock implementation of SNATStatsProvider for testing.
type fakeSNATStatsProvider struct {
	stats map[string]SNATRuleStats
	err   error
}

func (f *fakeSNATStatsProvider) Stats() (map[string]SNATRuleStats, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.stats, nil
}

func boolPtr(b bool) *bool {
	return &b
}

func newTestTrafficConfig(enabled bool, interval string, includeSNAT bool) config.TrafficLogConfig {
	return config.TrafficLogConfig{
		Enabled:     boolPtr(enabled),
		Interval:    interval,
		IncludeSNAT: boolPtr(includeSNAT),
	}
}

func newTestServiceConfig(name, listen, protocol, scheduler, trafficLogLevel string) config.ServiceConfig {
	return config.ServiceConfig{
		Name:            name,
		Listen:          listen,
		Protocol:        protocol,
		Scheduler:       scheduler,
		TrafficLogLevel: trafficLogLevel,
	}
}

func TestCollector_FirstSampleBaseline(t *testing.T) {
	core, logs := observer.New(zapcore.DebugLevel)
	trafficLogger := zap.New(core)

	lvsProvider := &fakeLVSStatsProvider{
		serviceStats: map[string]ServiceTrafficStats{
			"10.0.0.1:80/tcp": {Connections: 100, InBytes: 50000, OutBytes: 30000},
		},
	}

	services := []config.ServiceConfig{
		newTestServiceConfig("web", "10.0.0.1:80", "tcp", "rr", ""),
	}
	trafficCfg := newTestTrafficConfig(true, "15s", false)

	c := NewCollector(lvsProvider, nil, trafficLogger, zap.NewNop(), zap.NewNop(), services, trafficCfg)

	// First collect should establish baseline, no logs
	c.collect()

	if logs.Len() != 0 {
		t.Errorf("expected 0 log entries after first sample (baseline), got %d", logs.Len())
		for _, entry := range logs.All() {
			t.Logf("  unexpected log: %s %v", entry.Message, entry.ContextMap())
		}
	}

	if c.prevSnapshot == nil {
		t.Error("prevSnapshot should be set after first collect")
	}
}

func TestCollector_DeltaCalculation(t *testing.T) {
	core, logs := observer.New(zapcore.DebugLevel)
	trafficLogger := zap.New(core)

	lvsProvider := &fakeLVSStatsProvider{
		serviceStats: map[string]ServiceTrafficStats{
			"10.0.0.1:80/tcp": {Connections: 100, InPkts: 200, OutPkts: 150, InBytes: 50000, OutBytes: 30000},
		},
		backendStats: map[string]BackendTrafficStats{
			"10.0.0.1:80/tcp->192.168.1.1:8080": {
				ServiceKey: "10.0.0.1:80/tcp", Connections: 50, InPkts: 100, OutPkts: 75, InBytes: 25000, OutBytes: 15000,
			},
		},
	}

	services := []config.ServiceConfig{
		newTestServiceConfig("web", "10.0.0.1:80", "tcp", "rr", ""),
	}
	trafficCfg := newTestTrafficConfig(true, "15s", false)

	c := NewCollector(lvsProvider, nil, trafficLogger, zap.NewNop(), zap.NewNop(), services, trafficCfg)

	// First collect: baseline
	c.collect()

	// Update stats for second collect
	lvsProvider.serviceStats = map[string]ServiceTrafficStats{
		"10.0.0.1:80/tcp": {Connections: 150, InPkts: 300, OutPkts: 220, InBytes: 75000, OutBytes: 45000},
	}
	lvsProvider.backendStats = map[string]BackendTrafficStats{
		"10.0.0.1:80/tcp->192.168.1.1:8080": {
			ServiceKey: "10.0.0.1:80/tcp", Connections: 80, InPkts: 160, OutPkts: 120, InBytes: 40000, OutBytes: 24000,
		},
	}

	// Second collect: should compute deltas
	c.collect()

	if logs.Len() != 2 {
		t.Fatalf("expected 2 log entries (service + backend), got %d", logs.Len())
	}

	// Verify service delta log
	svcLog := logs.All()[0]
	if svcLog.Message != "traffic stats" {
		t.Errorf("expected message 'traffic stats', got %q", svcLog.Message)
	}
	svcFields := svcLog.ContextMap()
	if svcFields["connections_delta"] != uint64(50) {
		t.Errorf("expected connections_delta=50, got %v (type %T)", svcFields["connections_delta"], svcFields["connections_delta"])
	}
	if svcFields["bytes_in_delta"] != uint64(25000) {
		t.Errorf("expected bytes_in_delta=25000, got %v (type %T)", svcFields["bytes_in_delta"], svcFields["bytes_in_delta"])
	}
	if svcFields["bytes_out_delta"] != uint64(15000) {
		t.Errorf("expected bytes_out_delta=15000, got %v (type %T)", svcFields["bytes_out_delta"], svcFields["bytes_out_delta"])
	}
}

func TestCollector_CounterReset(t *testing.T) {
	core, logs := observer.New(zapcore.DebugLevel)
	trafficLogger := zap.New(core)

	lvsProvider := &fakeLVSStatsProvider{
		serviceStats: map[string]ServiceTrafficStats{
			"10.0.0.1:80/tcp": {Connections: 1000, InBytes: 500000, OutBytes: 300000},
		},
	}

	services := []config.ServiceConfig{
		newTestServiceConfig("web", "10.0.0.1:80", "tcp", "rr", ""),
	}
	trafficCfg := newTestTrafficConfig(true, "15s", false)

	c := NewCollector(lvsProvider, nil, trafficLogger, zap.NewNop(), zap.NewNop(), services, trafficCfg)

	// First collect: baseline
	c.collect()

	// Simulate counter reset (values lower than previous)
	lvsProvider.serviceStats = map[string]ServiceTrafficStats{
		"10.0.0.1:80/tcp": {Connections: 10, InBytes: 1000, OutBytes: 500},
	}

	// Second collect: counter reset detected, delta should be 0 (baseline reset)
	c.collect()

	// safeDelta64 returns 0 when current < previous, so all deltas are 0
	// and the collector should skip logging (only logs when delta is non-zero)
	if logs.Len() != 0 {
		t.Errorf("expected 0 log entries after counter reset (all deltas 0), got %d", logs.Len())
		for _, entry := range logs.All() {
			t.Logf("  unexpected log: %s %v", entry.Message, entry.ContextMap())
		}
	}
}

func TestCollector_RuleDisappearReappear(t *testing.T) {
	core, logs := observer.New(zapcore.DebugLevel)
	trafficLogger := zap.New(core)

	lvsProvider := &fakeLVSStatsProvider{
		serviceStats: map[string]ServiceTrafficStats{
			"10.0.0.1:80/tcp":  {Connections: 100, InBytes: 50000, OutBytes: 30000},
			"10.0.0.2:443/tcp": {Connections: 200, InBytes: 100000, OutBytes: 60000},
		},
	}

	services := []config.ServiceConfig{
		newTestServiceConfig("web", "10.0.0.1:80", "tcp", "rr", ""),
		newTestServiceConfig("api", "10.0.0.2:443", "tcp", "rr", ""),
	}
	trafficCfg := newTestTrafficConfig(true, "15s", false)

	c := NewCollector(lvsProvider, nil, trafficLogger, zap.NewNop(), zap.NewNop(), services, trafficCfg)

	// First collect: baseline
	c.collect()

	// Second collect: service "api" disappears, "web" has delta
	lvsProvider.serviceStats = map[string]ServiceTrafficStats{
		"10.0.0.1:80/tcp": {Connections: 150, InBytes: 75000, OutBytes: 45000},
	}
	c.collect()

	// Should only log delta for "web" (api disappeared, no delta)
	if logs.Len() != 1 {
		t.Fatalf("expected 1 log entry, got %d", logs.Len())
	}

	// Third collect: "api" reappears with new baseline
	logs.TakeAll() // clear logs
	lvsProvider.serviceStats = map[string]ServiceTrafficStats{
		"10.0.0.1:80/tcp":  {Connections: 200, InBytes: 100000, OutBytes: 60000},
		"10.0.0.2:443/tcp": {Connections: 50, InBytes: 10000, OutBytes: 5000},
	}
	c.collect()

	// "web" should have delta, "api" is new baseline (no delta logged)
	if logs.Len() != 1 {
		t.Fatalf("expected 1 log entry (only web delta), got %d", logs.Len())
	}
}

func TestCollector_OnlyLogOnChange(t *testing.T) {
	core, logs := observer.New(zapcore.DebugLevel)
	trafficLogger := zap.New(core)

	lvsProvider := &fakeLVSStatsProvider{
		serviceStats: map[string]ServiceTrafficStats{
			"10.0.0.1:80/tcp": {Connections: 100, InBytes: 50000, OutBytes: 30000},
		},
	}

	services := []config.ServiceConfig{
		newTestServiceConfig("web", "10.0.0.1:80", "tcp", "rr", ""),
	}
	trafficCfg := newTestTrafficConfig(true, "15s", false)

	c := NewCollector(lvsProvider, nil, trafficLogger, zap.NewNop(), zap.NewNop(), services, trafficCfg)

	// First collect: baseline
	c.collect()

	// Second collect: same stats (no change)
	c.collect()

	// No delta, should not log
	if logs.Len() != 0 {
		t.Errorf("expected 0 log entries when stats unchanged, got %d", logs.Len())
	}
}

func TestCollector_TrafficLogLevelNone(t *testing.T) {
	core, logs := observer.New(zapcore.DebugLevel)
	trafficLogger := zap.New(core)

	lvsProvider := &fakeLVSStatsProvider{
		serviceStats: map[string]ServiceTrafficStats{
			"10.0.0.1:80/tcp": {Connections: 100, InBytes: 50000, OutBytes: 30000},
		},
	}

	services := []config.ServiceConfig{
		newTestServiceConfig("web", "10.0.0.1:80", "tcp", "rr", "none"),
	}
	trafficCfg := newTestTrafficConfig(true, "15s", false)

	c := NewCollector(lvsProvider, nil, trafficLogger, zap.NewNop(), zap.NewNop(), services, trafficCfg)

	// First collect: baseline
	c.collect()

	// Update stats
	lvsProvider.serviceStats = map[string]ServiceTrafficStats{
		"10.0.0.1:80/tcp": {Connections: 200, InBytes: 100000, OutBytes: 60000},
	}

	// Second collect: has delta but level is "none", should skip
	c.collect()

	if logs.Len() != 0 {
		t.Errorf("expected 0 log entries for traffic_log_level=none, got %d", logs.Len())
	}
}

func TestCollector_SNATStatsDisabled(t *testing.T) {
	core, logs := observer.New(zapcore.DebugLevel)
	natLogger := zap.New(core)

	snatProvider := &fakeSNATStatsProvider{
		stats: map[string]SNATRuleStats{
			"192.168.1.1:8080/tcp": {Packets: 100, Bytes: 50000},
		},
	}

	lvsProvider := &fakeLVSStatsProvider{
		serviceStats: make(map[string]ServiceTrafficStats),
	}

	services := []config.ServiceConfig{}
	trafficCfg := newTestTrafficConfig(true, "15s", false) // include_snat = false

	c := NewCollector(lvsProvider, snatProvider, zap.NewNop(), natLogger, zap.NewNop(), services, trafficCfg)

	// First collect: baseline
	c.collect()

	// Update SNAT stats
	snatProvider.stats = map[string]SNATRuleStats{
		"192.168.1.1:8080/tcp": {Packets: 200, Bytes: 100000},
	}

	// Second collect: SNAT disabled, should not log
	c.collect()

	if logs.Len() != 0 {
		t.Errorf("expected 0 SNAT log entries when include_snat=false, got %d", logs.Len())
	}
}

func TestCollector_SNATStatsEnabled(t *testing.T) {
	core, logs := observer.New(zapcore.DebugLevel)
	natLogger := zap.New(core)

	snatProvider := &fakeSNATStatsProvider{
		stats: map[string]SNATRuleStats{
			"192.168.1.1:8080/tcp": {Packets: 100, Bytes: 50000},
		},
	}

	lvsProvider := &fakeLVSStatsProvider{
		serviceStats: make(map[string]ServiceTrafficStats),
		backendStats: make(map[string]BackendTrafficStats),
	}

	services := []config.ServiceConfig{}
	trafficCfg := newTestTrafficConfig(true, "15s", true) // include_snat = true

	c := NewCollector(lvsProvider, snatProvider, zap.NewNop(), natLogger, zap.NewNop(), services, trafficCfg)

	// First collect: baseline
	c.collect()

	// Update SNAT stats
	snatProvider.stats = map[string]SNATRuleStats{
		"192.168.1.1:8080/tcp": {Packets: 200, Bytes: 100000},
	}

	// Second collect: should log SNAT delta
	c.collect()

	if logs.Len() != 1 {
		t.Fatalf("expected 1 SNAT log entry, got %d", logs.Len())
	}

	snatLog := logs.All()[0]
	if snatLog.Message != "snat stats" {
		t.Errorf("expected message 'snat stats', got %q", snatLog.Message)
	}
	fields := snatLog.ContextMap()
	if fields["packets_delta"] != uint64(100) {
		t.Errorf("expected packets_delta=100, got %v (type %T)", fields["packets_delta"], fields["packets_delta"])
	}
	if fields["bytes_delta"] != uint64(50000) {
		t.Errorf("expected bytes_delta=50000, got %v (type %T)", fields["bytes_delta"], fields["bytes_delta"])
	}
}

func TestCollector_SNATStatsNilProvider(t *testing.T) {
	lvsProvider := &fakeLVSStatsProvider{
		serviceStats: make(map[string]ServiceTrafficStats),
	}

	services := []config.ServiceConfig{}
	trafficCfg := newTestTrafficConfig(true, "15s", true) // include_snat = true, but provider is nil

	// Should not panic with nil snatStats
	c := NewCollector(lvsProvider, nil, zap.NewNop(), zap.NewNop(), zap.NewNop(), services, trafficCfg)
	c.collect()
	c.collect()
}

func TestCollector_UpdateConfig(t *testing.T) {
	lvsProvider := &fakeLVSStatsProvider{
		serviceStats: map[string]ServiceTrafficStats{
			"10.0.0.1:80/tcp": {Connections: 100, InBytes: 50000, OutBytes: 30000},
		},
	}

	services := []config.ServiceConfig{
		newTestServiceConfig("web", "10.0.0.1:80", "tcp", "rr", ""),
	}
	trafficCfg := newTestTrafficConfig(true, "15s", false)

	c := NewCollector(lvsProvider, nil, zap.NewNop(), zap.NewNop(), zap.NewNop(), services, trafficCfg)

	// Update config
	newServices := []config.ServiceConfig{
		newTestServiceConfig("web", "10.0.0.1:80", "tcp", "rr", "debug"),
		newTestServiceConfig("api", "10.0.0.2:443", "tcp", "wrr", ""),
	}
	newTrafficCfg := newTestTrafficConfig(true, "30s", true)

	c.UpdateConfig(newServices, newTrafficCfg)

	// Verify config was updated
	c.mu.RLock()
	defer c.mu.RUnlock()

	if len(c.services) != 2 {
		t.Errorf("expected 2 services after update, got %d", len(c.services))
	}
	if c.trafficCfg.GetInterval() != 30*time.Second {
		t.Errorf("expected interval 30s after update, got %v", c.trafficCfg.GetInterval())
	}
	if !c.trafficCfg.IsIncludeSNAT() {
		t.Error("expected include_snat=true after update")
	}
}

func TestCollector_StartStop(t *testing.T) {
	lvsProvider := &fakeLVSStatsProvider{
		serviceStats: make(map[string]ServiceTrafficStats),
	}

	services := []config.ServiceConfig{}
	trafficCfg := newTestTrafficConfig(true, "5s", false)

	c := NewCollector(lvsProvider, nil, zap.NewNop(), zap.NewNop(), zap.NewNop(), services, trafficCfg)

	// Start and stop should not panic
	c.Start()

	// Give it a moment to start
	time.Sleep(50 * time.Millisecond)

	c.Stop()

	// Verify stopped channel is closed
	select {
	case <-c.stopped:
		// OK
	default:
		t.Error("stopped channel should be closed after Stop()")
	}
}

func TestSafeDelta64(t *testing.T) {
	tests := []struct {
		name     string
		current  uint64
		previous uint64
		expected uint64
	}{
		{"normal delta", 150, 100, 50},
		{"zero delta", 100, 100, 0},
		{"counter reset", 10, 1000, 0},
		{"large values", 18446744073709551615, 18446744073709551610, 5},
		{"from zero", 100, 0, 100},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := safeDelta64(tt.current, tt.previous)
			if result != tt.expected {
				t.Errorf("safeDelta64(%d, %d) = %d, want %d", tt.current, tt.previous, result, tt.expected)
			}
		})
	}
}

func TestBuildServiceConfigMap(t *testing.T) {
	services := []config.ServiceConfig{
		newTestServiceConfig("web", "10.0.0.1:80", "tcp", "rr", "info"),
		newTestServiceConfig("api", "10.0.0.2:443", "tcp", "wrr", "none"),
		newTestServiceConfig("dns", "10.0.0.3:53", "udp", "rr", "debug"),
	}

	result := buildServiceConfigMap(services)

	if len(result) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(result))
	}

	// Verify key format matches IPVS format: "listen/protocol"
	if svc, ok := result["10.0.0.1:80/tcp"]; !ok {
		t.Error("expected key '10.0.0.1:80/tcp'")
	} else if svc.Name != "web" {
		t.Errorf("expected name 'web', got %q", svc.Name)
	}

	if svc, ok := result["10.0.0.2:443/tcp"]; !ok {
		t.Error("expected key '10.0.0.2:443/tcp'")
	} else if svc.TrafficLogLevel != "none" {
		t.Errorf("expected TrafficLogLevel 'none', got %q", svc.TrafficLogLevel)
	}

	if _, ok := result["10.0.0.3:53/udp"]; !ok {
		t.Error("expected key '10.0.0.3:53/udp'")
	}
}

func TestLogAtLevel(t *testing.T) {
	tests := []struct {
		name          string
		level         string
		expectedLevel zapcore.Level
	}{
		{"debug level", "debug", zapcore.DebugLevel},
		{"info level", "info", zapcore.InfoLevel},
		{"warn level", "warn", zapcore.WarnLevel},
		{"error level", "error", zapcore.ErrorLevel},
		{"empty defaults to info", "", zapcore.InfoLevel},
		{"unknown defaults to info", "unknown", zapcore.InfoLevel},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			core, logs := observer.New(zapcore.DebugLevel)
			logger := zap.New(core)

			logAtLevel(logger, tt.level, "test message", []zap.Field{zap.String("key", "value")})

			if logs.Len() != 1 {
				t.Fatalf("expected 1 log entry, got %d", logs.Len())
			}

			entry := logs.All()[0]
			if entry.Level != tt.expectedLevel {
				t.Errorf("expected level %v, got %v", tt.expectedLevel, entry.Level)
			}
			if entry.Message != "test message" {
				t.Errorf("expected message 'test message', got %q", entry.Message)
			}
		})
	}
}

func TestCollector_StatsProviderError(t *testing.T) {
	sysCore, sysLogs := observer.New(zapcore.DebugLevel)
	systemLogger := zap.New(sysCore)

	trafficCore, trafficLogs := observer.New(zapcore.DebugLevel)
	trafficLogger := zap.New(trafficCore)

	lvsProvider := &fakeLVSStatsProvider{
		serviceErr: fmt.Errorf("ipvs connection failed"),
	}

	services := []config.ServiceConfig{}
	trafficCfg := newTestTrafficConfig(true, "15s", false)

	c := NewCollector(lvsProvider, nil, trafficLogger, zap.NewNop(), systemLogger, services, trafficCfg)

	// Should not panic, should log warning
	c.collect()
	c.collect()

	// System logger should have warnings about failed stats collection
	if sysLogs.Len() == 0 {
		t.Error("expected system logger warnings about failed stats collection")
	}

	// Traffic logger should have no entries (no valid data to compute deltas)
	if trafficLogs.Len() != 0 {
		t.Errorf("expected 0 traffic log entries, got %d", trafficLogs.Len())
	}
}
