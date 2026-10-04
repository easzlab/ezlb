package trafficmetrics

import (
	"strings"
	"sync"
	"time"

	"github.com/easzlab/ezlb/pkg/config"
	"github.com/easzlab/ezlb/pkg/lvs"
	"github.com/easzlab/ezlb/pkg/metrics"
	"go.uber.org/zap"
)

// Collector periodically polls IPVS statistics and pushes them to Prometheus
// metrics. Counter metrics receive the per-cycle delta computed from the
// previous snapshot; Gauge metrics receive the current value.
//
// The collector intentionally does NOT write a separate "traffic.log" file:
// raw cumulative counters are exposed exclusively via Prometheus, where
// rate() / increase() are first-class operations. Per-service traffic gating
// has been removed for the same reason — Prometheus label-based filtering
// (e.g. service=~"web-.*") is the right place to do it.
type Collector struct {
	globalCfg config.GlobalConfig
	lvsStats  LVSStatsProvider
	logger    *zap.Logger
	stopCh    chan struct{}
	stopped   chan struct{}
	services  []config.ServiceConfig
	prev      *TrafficSnapshot
	mu        sync.RWMutex
}

// NewCollector creates a new traffic statistics collector.
func NewCollector(
	lvsStats LVSStatsProvider,
	logger *zap.Logger,
	services []config.ServiceConfig,
	globalCfg config.GlobalConfig,
) *Collector {
	return &Collector{
		lvsStats:  lvsStats,
		logger:    logger,
		services:  services,
		globalCfg: globalCfg,
		stopCh:    make(chan struct{}),
		stopped:   make(chan struct{}),
	}
}

// Start begins periodic collection in a background goroutine.
func (c *Collector) Start() {
	go c.run()
}

// Stop stops the collector goroutine and waits for it to finish.
func (c *Collector) Stop() {
	close(c.stopCh)
	<-c.stopped
}

// UpdateConfig dynamically updates the collector's configuration.
// Called by Server when config hot-reload is detected.
func (c *Collector) UpdateConfig(services []config.ServiceConfig, globalCfg config.GlobalConfig) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.services = services
	c.globalCfg = globalCfg
}

// run is the main collection loop.
func (c *Collector) run() {
	defer close(c.stopped)

	c.mu.RLock()
	interval := c.globalCfg.GetMetricsInterval()
	c.mu.RUnlock()

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-c.stopCh:
			return
		case <-ticker.C:
			c.mu.RLock()
			newInterval := c.globalCfg.GetMetricsInterval()
			c.mu.RUnlock()

			if newInterval != interval {
				ticker.Reset(newInterval)
				interval = newInterval
			}

			c.collect()
		}
	}
}

// collect performs a single collection cycle: gather snapshot, compute
// deltas vs. the previous snapshot, and update Prometheus.
func (c *Collector) collect() {
	snapshot := c.gatherSnapshot()
	if snapshot == nil {
		return
	}

	c.updateMetrics(snapshot)

	c.mu.Lock()
	c.prev = snapshot
	c.mu.Unlock()
}

// gatherSnapshot collects current statistics from all providers.
func (c *Collector) gatherSnapshot() *TrafficSnapshot {
	svcStats, backendStats, err := c.lvsStats.AllStats()
	if err != nil {
		c.logger.Warn("failed to collect IPVS stats", zap.Error(err))
		return nil
	}

	return &TrafficSnapshot{
		Services: svcStats,
		Backends: backendStats,
	}
}

// buildServiceConfigMap builds a lookup map from service key (listen/protocol format)
// to ServiceConfig. The key format matches ServiceKeyFromIPVS().String().
func buildServiceConfigMap(services []config.ServiceConfig) map[string]config.ServiceConfig {
	result := make(map[string]config.ServiceConfig, len(services))
	for _, svc := range services {
		key, err := lvs.ServiceKeyFromConfig(svc)
		if err == nil {
			result[key.String()] = svc
		}
	}
	return result
}

// updateMetrics updates Prometheus metrics with the collected snapshot.
// Counter metrics are advanced by the delta against the previous snapshot;
// Gauge metrics are set to the current value.
//
// Counter resets (e.g. IPVS service recreated, host restart) are detected
// when the new cumulative value is smaller than the previous one. In that
// case the counter is advanced by the new value (treating it as the delta
// since reset) instead of going negative.
func (c *Collector) updateMetrics(snapshot *TrafficSnapshot) {
	c.mu.RLock()
	services := c.services
	prev := c.prev
	c.mu.RUnlock()

	svcConfigMap := buildServiceConfigMap(services)

	// Service-level metrics
	for key, stats := range snapshot.Services {
		svcCfg, ok := svcConfigMap[key]
		if !ok {
			continue
		}

		var prevStats ServiceTrafficStats
		if prev != nil {
			prevStats = prev.Services[key]
		}

		metrics.AddServiceTraffic(
			svcCfg.Name,
			svcCfg.Listen,
			svcCfg.Protocol,
			counterDelta(stats.Connections, prevStats.Connections),
			counterDelta(stats.InBytes, prevStats.InBytes),
			counterDelta(stats.OutBytes, prevStats.OutBytes),
			counterDelta(stats.InPkts, prevStats.InPkts),
			counterDelta(stats.OutPkts, prevStats.OutPkts),
		)
	}

	// Backend-level metrics
	for backendKey, stats := range snapshot.Backends {
		svcCfg, ok := svcConfigMap[stats.ServiceKey]
		if !ok {
			continue
		}

		var prevStats BackendTrafficStats
		if prev != nil {
			prevStats = prev.Backends[backendKey]
		}

		backendAddr := extractBackendAddress(backendKey)

		metrics.AddBackendTraffic(
			svcCfg.Name,
			backendAddr,
			svcCfg.Protocol,
			counterDelta(stats.Connections, prevStats.Connections),
			counterDelta(stats.InBytes, prevStats.InBytes),
			counterDelta(stats.OutBytes, prevStats.OutBytes),
			counterDelta(stats.InPkts, prevStats.InPkts),
			counterDelta(stats.OutPkts, prevStats.OutPkts),
		)

		metrics.SetBackendConnections(
			svcCfg.Name,
			backendAddr,
			svcCfg.Protocol,
			stats.ActiveConnections,
			stats.InactiveConnections,
		)
	}
}

// counterDelta returns curr - prev when curr >= prev, otherwise returns curr
// (treating the gap as a counter reset since process restart, IPVS rule
// recreation, or host reboot).
func counterDelta(curr, prev uint64) uint64 {
	if curr >= prev {
		return curr - prev
	}
	return curr
}

// extractBackendAddress extracts the backend address from the full key.
// The full key format is "svcKey->dstKey" where dstKey is "ip:port".
func extractBackendAddress(fullKey string) string {
	parts := strings.Split(fullKey, "->")
	if len(parts) == 2 {
		return parts[1]
	}
	return fullKey
}
