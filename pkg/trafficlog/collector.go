package trafficlog

import (
	"sync"
	"time"

	"github.com/easzlab/ezlb/pkg/config"
	"github.com/easzlab/ezlb/pkg/logutil"
	"go.uber.org/zap"
)

// Collector periodically collects IPVS and optional SNAT statistics
// and writes raw cumulative data as debug-level traffic logs.
// Traffic logging is disabled by default per service; only services with
// traffic_log explicitly set to true will be logged.
type Collector struct {
	trafficCfg    config.TrafficLogConfig
	lvsStats      LVSStatsProvider
	snatStats     SNATStatsProvider
	trafficLogger *zap.Logger
	natLogger     *zap.Logger
	systemLogger  *zap.Logger
	stopCh        chan struct{}
	stopped       chan struct{}
	services      []config.ServiceConfig
	mu            sync.RWMutex
}

// NewCollector creates a new traffic statistics collector.
func NewCollector(
	lvsStats LVSStatsProvider,
	snatStats SNATStatsProvider,
	trafficLogger *zap.Logger,
	natLogger *zap.Logger,
	systemLogger *zap.Logger,
	services []config.ServiceConfig,
	trafficCfg config.TrafficLogConfig,
) *Collector {
	return &Collector{
		lvsStats:      lvsStats,
		snatStats:     snatStats,
		trafficLogger: trafficLogger,
		natLogger:     natLogger,
		systemLogger:  systemLogger,
		services:      services,
		trafficCfg:    trafficCfg,
		stopCh:        make(chan struct{}),
		stopped:       make(chan struct{}),
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
func (c *Collector) UpdateConfig(services []config.ServiceConfig, trafficCfg config.TrafficLogConfig) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.services = services
	c.trafficCfg = trafficCfg
}

// run is the main collection loop.
func (c *Collector) run() {
	defer close(c.stopped)

	c.mu.RLock()
	interval := c.trafficCfg.GetInterval()
	c.mu.RUnlock()

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-c.stopCh:
			return
		case <-ticker.C:
			c.mu.RLock()
			newInterval := c.trafficCfg.GetInterval()
			enabled := c.trafficCfg.IsEnabled()
			c.mu.RUnlock()

			// Adjust ticker if interval changed
			if newInterval != interval {
				ticker.Reset(newInterval)
				interval = newInterval
			}

			if !enabled {
				continue
			}

			c.collect()
		}
	}
}

// collect performs a single collection cycle: gather stats and write raw data logs.
func (c *Collector) collect() {
	snapshot := c.gatherSnapshot()
	if snapshot == nil {
		return
	}

	c.logRawStats(snapshot)
}

// gatherSnapshot collects current statistics from all providers.
func (c *Collector) gatherSnapshot() *TrafficSnapshot {
	snapshot := &TrafficSnapshot{
		Services: make(map[string]ServiceTrafficStats),
		Backends: make(map[string]BackendTrafficStats),
		SNAT:     make(map[string]SNATRuleStats),
	}

	// Collect LVS service stats
	svcStats, err := c.lvsStats.ServiceStats()
	if err != nil {
		c.systemLogger.Warn("failed to collect IPVS service stats", zap.Error(err))
	} else {
		snapshot.Services = svcStats
	}

	// Collect LVS backend stats
	backendStats, err := c.lvsStats.BackendStats()
	if err != nil {
		c.systemLogger.Warn("failed to collect IPVS backend stats", zap.Error(err))
	} else {
		snapshot.Backends = backendStats
	}

	// Collect SNAT stats (optional)
	c.mu.RLock()
	includeSNAT := c.trafficCfg.IsIncludeSNAT()
	c.mu.RUnlock()

	if includeSNAT && c.snatStats != nil {
		snatStats, err := c.snatStats.Stats()
		if err != nil {
			c.systemLogger.Warn("failed to collect SNAT stats", zap.Error(err))
		} else {
			snapshot.SNAT = snatStats
		}
	}

	return snapshot
}

// logRawStats writes raw cumulative statistics as debug-level log entries.
// Only services with traffic_log explicitly set to true are logged.
func (c *Collector) logRawStats(snapshot *TrafficSnapshot) {
	c.mu.RLock()
	services := c.services
	c.mu.RUnlock()

	// Build service key -> config lookup
	svcConfigMap := buildServiceConfigMap(services)

	// Log service-level raw stats
	for key, stats := range snapshot.Services {
		svcCfg, ok := svcConfigMap[key]
		if !ok {
			// Service config not found (may have been removed), skip
			continue
		}

		// Default behavior: traffic_log is nil or false means disabled
		if !isTrafficLogEnabled(svcCfg.TrafficLog) {
			continue
		}

		fields := append(logutil.ServiceFields(svcCfg),
			zap.String("source", "ipvs"),
			zap.String("type", "service"),
			zap.Uint64("connections", stats.Connections),
			zap.Uint64("bytes_in", stats.InBytes),
			zap.Uint64("bytes_out", stats.OutBytes),
			zap.Uint64("packets_in", stats.InPkts),
			zap.Uint64("packets_out", stats.OutPkts),
		)
		c.trafficLogger.Debug("traffic raw stats", fields...)
	}

	// Log backend-level raw stats
	for key, stats := range snapshot.Backends {
		svcCfg, ok := svcConfigMap[stats.ServiceKey]
		if !ok {
			continue
		}

		if !isTrafficLogEnabled(svcCfg.TrafficLog) {
			continue
		}

		fields := append(logutil.ServiceFields(svcCfg),
			zap.String("source", "ipvs"),
			zap.String("type", "backend"),
			zap.String("backend_key", key),
			zap.Uint64("connections", stats.Connections),
			zap.Uint64("bytes_in", stats.InBytes),
			zap.Uint64("bytes_out", stats.OutBytes),
			zap.Uint64("packets_in", stats.InPkts),
			zap.Uint64("packets_out", stats.OutPkts),
		)
		c.trafficLogger.Debug("traffic raw stats", fields...)
	}

	// Log SNAT raw stats
	for key, stats := range snapshot.SNAT {
		c.natLogger.Debug("snat raw stats",
			zap.String("source", "snat"),
			zap.String("rule_key", key),
			zap.Uint64("packets", stats.Packets),
			zap.Uint64("bytes", stats.Bytes),
		)
	}
}

// buildServiceConfigMap builds a lookup map from service key (listen/protocol format)
// to ServiceConfig. The key format matches ServiceKeyFromIPVS().String().
func buildServiceConfigMap(services []config.ServiceConfig) map[string]config.ServiceConfig {
	result := make(map[string]config.ServiceConfig, len(services))
	for _, svc := range services {
		// Build key matching IPVS format: "ip:port/protocol"
		key := svc.Listen + "/" + svc.Protocol
		result[key] = svc
	}
	return result
}

// isTrafficLogEnabled returns true if the per-service traffic log flag
// is explicitly set to true. A nil pointer (default) or false means disabled.
func isTrafficLogEnabled(trafficLog *bool) bool {
	return trafficLog != nil && *trafficLog
}
