package trafficlog

import (
	"sync"
	"time"

	"github.com/easzlab/ezlb/pkg/config"
	"github.com/easzlab/ezlb/pkg/logutil"
	"go.uber.org/zap"
)

// Collector periodically collects IPVS and optional SNAT statistics,
// computes deltas from the previous snapshot, and writes traffic logs.
type Collector struct {
	lvsStats      LVSStatsProvider
	snatStats     SNATStatsProvider // may be nil
	trafficLogger *zap.Logger
	natLogger     *zap.Logger
	systemLogger  *zap.Logger

	mu         sync.RWMutex
	services   []config.ServiceConfig
	trafficCfg config.TrafficLogConfig

	prevSnapshot *TrafficSnapshot
	stopCh       chan struct{}
	stopped      chan struct{}
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

// collect performs a single collection cycle: gather stats, compute deltas, write logs.
func (c *Collector) collect() {
	snapshot := c.gatherSnapshot()
	if snapshot == nil {
		return
	}

	if c.prevSnapshot != nil {
		c.computeAndLogDeltas(snapshot)
	}
	// First sample or subsequent: update baseline
	c.prevSnapshot = snapshot
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

// computeAndLogDeltas calculates deltas between current and previous snapshots and writes logs.
func (c *Collector) computeAndLogDeltas(current *TrafficSnapshot) {
	c.mu.RLock()
	services := c.services
	interval := c.trafficCfg.GetInterval()
	c.mu.RUnlock()

	// Build service key -> config lookup
	svcConfigMap := buildServiceConfigMap(services)

	// Log service-level deltas
	for key, cur := range current.Services {
		prev, exists := c.prevSnapshot.Services[key]
		if !exists {
			// New service, establish baseline
			continue
		}

		connDelta := safeDelta64(cur.Connections, prev.Connections)
		bytesInDelta := safeDelta64(cur.InBytes, prev.InBytes)
		bytesOutDelta := safeDelta64(cur.OutBytes, prev.OutBytes)
		pktsInDelta := safeDelta64(cur.InPkts, prev.InPkts)
		pktsOutDelta := safeDelta64(cur.OutPkts, prev.OutPkts)

		// Skip if no change
		if connDelta == 0 && bytesInDelta == 0 && bytesOutDelta == 0 {
			continue
		}

		// Check per-service traffic log level
		if svcCfg, ok := svcConfigMap[key]; ok {
			if svcCfg.TrafficLogLevel == "none" {
				continue
			}
			fields := append(logutil.ServiceFields(svcCfg),
				zap.String("source", "ipvs"),
				zap.String("type", "service"),
				zap.Duration("interval", interval),
				zap.Uint64("connections_delta", connDelta),
				zap.Uint64("bytes_in_delta", bytesInDelta),
				zap.Uint64("bytes_out_delta", bytesOutDelta),
				zap.Uint64("packets_in_delta", pktsInDelta),
				zap.Uint64("packets_out_delta", pktsOutDelta),
			)
			logAtLevel(c.trafficLogger, svcCfg.TrafficLogLevel, "traffic stats", fields)
		} else {
			// Service config not found (may have been removed), log at default level
			c.trafficLogger.Info("traffic stats",
				zap.String("source", "ipvs"),
				zap.String("type", "service"),
				zap.String("service_key", key),
				zap.Duration("interval", interval),
				zap.Uint64("connections_delta", connDelta),
				zap.Uint64("bytes_in_delta", bytesInDelta),
				zap.Uint64("bytes_out_delta", bytesOutDelta),
				zap.Uint64("packets_in_delta", pktsInDelta),
				zap.Uint64("packets_out_delta", pktsOutDelta),
			)
		}
	}

	// Log backend-level deltas
	for key, cur := range current.Backends {
		prev, exists := c.prevSnapshot.Backends[key]
		if !exists {
			continue
		}

		connDelta := safeDelta64(cur.Connections, prev.Connections)
		bytesInDelta := safeDelta64(cur.InBytes, prev.InBytes)
		bytesOutDelta := safeDelta64(cur.OutBytes, prev.OutBytes)
		pktsInDelta := safeDelta64(cur.InPkts, prev.InPkts)
		pktsOutDelta := safeDelta64(cur.OutPkts, prev.OutPkts)

		if connDelta == 0 && bytesInDelta == 0 && bytesOutDelta == 0 {
			continue
		}

		// Check per-service traffic log level
		if svcCfg, ok := svcConfigMap[cur.ServiceKey]; ok {
			if svcCfg.TrafficLogLevel == "none" {
				continue
			}
			fields := append(logutil.ServiceFields(svcCfg),
				zap.String("source", "ipvs"),
				zap.String("type", "backend"),
				zap.String("backend_key", key),
				zap.Duration("interval", interval),
				zap.Uint64("connections_delta", connDelta),
				zap.Uint64("bytes_in_delta", bytesInDelta),
				zap.Uint64("bytes_out_delta", bytesOutDelta),
				zap.Uint64("packets_in_delta", pktsInDelta),
				zap.Uint64("packets_out_delta", pktsOutDelta),
			)
			logAtLevel(c.trafficLogger, svcCfg.TrafficLogLevel, "traffic stats", fields)
		} else {
			c.trafficLogger.Info("traffic stats",
				zap.String("source", "ipvs"),
				zap.String("type", "backend"),
				zap.String("backend_key", key),
				zap.Duration("interval", interval),
				zap.Uint64("connections_delta", connDelta),
				zap.Uint64("bytes_in_delta", bytesInDelta),
				zap.Uint64("bytes_out_delta", bytesOutDelta),
				zap.Uint64("packets_in_delta", pktsInDelta),
				zap.Uint64("packets_out_delta", pktsOutDelta),
			)
		}
	}

	// Log SNAT deltas
	for key, cur := range current.SNAT {
		prev, exists := c.prevSnapshot.SNAT[key]
		if !exists {
			continue
		}

		pktsDelta := safeDelta64(cur.Packets, prev.Packets)
		bytesDelta := safeDelta64(cur.Bytes, prev.Bytes)

		if pktsDelta == 0 && bytesDelta == 0 {
			continue
		}

		c.natLogger.Info("snat stats",
			zap.String("source", "snat"),
			zap.String("rule_key", key),
			zap.Duration("interval", interval),
			zap.Uint64("packets_delta", pktsDelta),
			zap.Uint64("bytes_delta", bytesDelta),
		)
	}
}

// safeDelta64 computes current - previous for uint64 values.
// If current < previous (counter reset/wrap), returns 0 (baseline reset).
func safeDelta64(current, previous uint64) uint64 {
	if current < previous {
		return 0
	}
	return current - previous
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

// logAtLevel logs a message at the specified level. If level is empty, defaults to info.
func logAtLevel(logger *zap.Logger, level string, msg string, fields []zap.Field) {
	switch level {
	case "debug":
		logger.Debug(msg, fields...)
	case "warn":
		logger.Warn(msg, fields...)
	case "error":
		logger.Error(msg, fields...)
	default:
		// "info" or empty (inherit global)
		logger.Info(msg, fields...)
	}
}
