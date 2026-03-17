package trafficlog

import (
	"testing"
)

func TestTrafficSnapshot_Initialization(t *testing.T) {
	snapshot := &TrafficSnapshot{
		Services: make(map[string]ServiceTrafficStats),
		Backends: make(map[string]BackendTrafficStats),
		SNAT:     make(map[string]SNATRuleStats),
	}

	if snapshot.Services == nil {
		t.Fatal("Services map should not be nil")
	}
	if snapshot.Backends == nil {
		t.Fatal("Backends map should not be nil")
	}
	if snapshot.SNAT == nil {
		t.Fatal("SNAT map should not be nil")
	}

	if len(snapshot.Services) != 0 {
		t.Errorf("expected 0 services, got %d", len(snapshot.Services))
	}
	if len(snapshot.Backends) != 0 {
		t.Errorf("expected 0 backends, got %d", len(snapshot.Backends))
	}
	if len(snapshot.SNAT) != 0 {
		t.Errorf("expected 0 SNAT rules, got %d", len(snapshot.SNAT))
	}
}

func TestServiceTrafficStats_ZeroValue(t *testing.T) {
	var stats ServiceTrafficStats
	if stats.Connections != 0 || stats.InPkts != 0 || stats.OutPkts != 0 || stats.InBytes != 0 || stats.OutBytes != 0 {
		t.Error("zero-value ServiceTrafficStats should have all fields as 0")
	}
}

func TestBackendTrafficStats_ZeroValue(t *testing.T) {
	var stats BackendTrafficStats
	if stats.ServiceKey != "" {
		t.Error("zero-value BackendTrafficStats should have empty ServiceKey")
	}
	if stats.Connections != 0 || stats.InPkts != 0 || stats.OutPkts != 0 || stats.InBytes != 0 || stats.OutBytes != 0 {
		t.Error("zero-value BackendTrafficStats should have all numeric fields as 0")
	}
}

func TestSNATRuleStats_ZeroValue(t *testing.T) {
	var stats SNATRuleStats
	if stats.Packets != 0 || stats.Bytes != 0 {
		t.Error("zero-value SNATRuleStats should have all fields as 0")
	}
}

func TestTrafficSnapshot_PopulateAndRetrieve(t *testing.T) {
	snapshot := &TrafficSnapshot{
		Services: map[string]ServiceTrafficStats{
			"10.0.0.1:80/tcp": {
				Connections: 100,
				InPkts:      200,
				OutPkts:     150,
				InBytes:     50000,
				OutBytes:    30000,
			},
		},
		Backends: map[string]BackendTrafficStats{
			"10.0.0.1:80/tcp->192.168.1.1:8080": {
				ServiceKey:  "10.0.0.1:80/tcp",
				Connections: 50,
				InPkts:      100,
				OutPkts:     75,
				InBytes:     25000,
				OutBytes:    15000,
			},
		},
		SNAT: map[string]SNATRuleStats{
			"192.168.1.1:8080/tcp": {
				Packets: 300,
				Bytes:   60000,
			},
		},
	}

	// Verify service stats
	svcStats, ok := snapshot.Services["10.0.0.1:80/tcp"]
	if !ok {
		t.Fatal("expected service key '10.0.0.1:80/tcp' to exist")
	}
	if svcStats.Connections != 100 {
		t.Errorf("expected 100 connections, got %d", svcStats.Connections)
	}
	if svcStats.InBytes != 50000 {
		t.Errorf("expected 50000 InBytes, got %d", svcStats.InBytes)
	}

	// Verify backend stats
	backendStats, ok := snapshot.Backends["10.0.0.1:80/tcp->192.168.1.1:8080"]
	if !ok {
		t.Fatal("expected backend key to exist")
	}
	if backendStats.ServiceKey != "10.0.0.1:80/tcp" {
		t.Errorf("expected ServiceKey '10.0.0.1:80/tcp', got %q", backendStats.ServiceKey)
	}
	if backendStats.Connections != 50 {
		t.Errorf("expected 50 connections, got %d", backendStats.Connections)
	}

	// Verify SNAT stats
	snatStats, ok := snapshot.SNAT["192.168.1.1:8080/tcp"]
	if !ok {
		t.Fatal("expected SNAT rule key to exist")
	}
	if snatStats.Packets != 300 {
		t.Errorf("expected 300 packets, got %d", snatStats.Packets)
	}
	if snatStats.Bytes != 60000 {
		t.Errorf("expected 60000 bytes, got %d", snatStats.Bytes)
	}
}
