//go:build !integration

package server

import (
	"testing"

	"github.com/easzlab/ezlb/pkg/config"
	"github.com/easzlab/ezlb/pkg/snat"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

func TestServerSyncTrafficCollectorStartsOnHotEnable(t *testing.T) {
	configYAML := `
global:
  log:
    level: info
    traffic:
      enabled: false
services:
  - name: web-service
    listen: 10.0.0.1:80
    protocol: tcp
    scheduler: rr
    health_check:
      enabled: false
    backends:
      - address: 192.168.1.10:8080
        weight: 1
`
	configPath := writeYAMLFile(t, t.TempDir(), configYAML)

	srv := newTestServer(t, configPath)
	t.Cleanup(func() {
		srv.shutdown()
	})

	initialCfg := srv.configMgr.GetConfig()
	srv.syncTrafficCollector(initialCfg)
	if srv.collector != nil {
		t.Fatal("expected collector to remain nil while traffic logging is disabled")
	}

	enabledCfg := cloneConfig(initialCfg)
	enabledCfg.Global.Log.Traffic.Enabled = boolPtr(true)

	srv.syncTrafficCollector(enabledCfg)
	if srv.collector == nil {
		t.Fatal("expected collector to be created when traffic logging is hot-enabled")
	}
}

func TestNewServerWithManagerUsesNATLoggerForSNATManager(t *testing.T) {
	configYAML := `
global:
  log:
    level: info
services:
  - name: web-service
    listen: 10.0.0.1:80
    protocol: tcp
    scheduler: rr
    health_check:
      enabled: false
    backends:
      - address: 192.168.1.10:8080
        weight: 1
`
	configPath := writeYAMLFile(t, t.TempDir(), configYAML)

	systemCore, systemLogs := observer.New(zapcore.DebugLevel)
	natCore, natLogs := observer.New(zapcore.DebugLevel)

	srv, err := newServerWithManager(
		configPath,
		newTestLVSManager(t),
		zap.New(systemCore),
		zap.NewNop(),
		zap.New(natCore),
	)
	if err != nil {
		t.Fatalf("newServerWithManager failed: %v", err)
	}
	t.Cleanup(func() {
		srv.shutdown()
	})

	if err := srv.snatMgr.Reconcile([]snat.SNATRule{
		{BackendIP: "192.168.1.10", BackendPort: 8080, Protocol: "tcp", SnatIP: "10.0.0.1"},
	}); err != nil {
		t.Fatalf("snat reconcile failed: %v", err)
	}

	if natLogs.FilterMessage("fake: added SNAT rule").Len() != 1 {
		t.Fatalf("expected SNAT manager log to be written via nat logger, got %d entries", natLogs.Len())
	}
	if systemLogs.FilterMessage("fake: added SNAT rule").Len() != 0 {
		t.Fatal("expected SNAT manager log not to be written via system logger")
	}
}

func cloneConfig(cfg *config.Config) *config.Config {
	if cfg == nil {
		return nil
	}

	cloned := *cfg
	cloned.Services = append([]config.ServiceConfig(nil), cfg.Services...)
	for i := range cfg.Services {
		cloned.Services[i].Backends = append([]config.BackendConfig(nil), cfg.Services[i].Backends...)
	}
	return &cloned
}

func boolPtr(v bool) *bool {
	return &v
}
