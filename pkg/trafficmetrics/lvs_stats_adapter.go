package trafficmetrics

import (
	"fmt"

	"github.com/easzlab/ezlb/pkg/lvs"
	"go.uber.org/zap"
)

// lvsStatsAdapter implements LVSStatsProvider by adapting lvs.Manager.
// It calls GetServices() once per collection cycle and fans out to
// GetDestinations() for each service, avoiding redundant netlink round-trips.
type lvsStatsAdapter struct {
	manager *lvs.Manager
	logger  *zap.Logger
}

// NewLVSStatsAdapter creates an LVSStatsProvider backed by lvs.Manager.
func NewLVSStatsAdapter(mgr *lvs.Manager, logger *zap.Logger) LVSStatsProvider {
	return &lvsStatsAdapter{manager: mgr, logger: logger}
}

// AllStats retrieves cumulative statistics for all IPVS services and backends
// in a single pass, calling GetServices() only once.
// Individual service destination failures are logged and skipped.
func (a *lvsStatsAdapter) AllStats() (map[string]ServiceTrafficStats, map[string]BackendTrafficStats, error) {
	services, err := a.manager.GetServices()
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get IPVS services: %w", err)
	}

	svcResult := make(map[string]ServiceTrafficStats, len(services))
	backendResult := make(map[string]BackendTrafficStats)

	for _, svc := range services {
		svcKey := lvs.ServiceKeyFromIPVS(svc).String()

		svcResult[svcKey] = ServiceTrafficStats{
			Connections: uint64(svc.Stats.Connections),
			InPkts:      uint64(svc.Stats.PacketsIn),
			OutPkts:     uint64(svc.Stats.PacketsOut),
			InBytes:     svc.Stats.BytesIn,
			OutBytes:    svc.Stats.BytesOut,
		}

		dests, err := a.manager.GetDestinations(svc)
		if err != nil {
			a.logger.Warn("failed to get destinations, skipping service",
				zap.String("service", svcKey), zap.Error(err))
			continue
		}

		for _, dst := range dests {
			dstKey := lvs.DestinationKeyFromIPVS(dst).String()
			fullKey := fmt.Sprintf("%s->%s", svcKey, dstKey)
			backendResult[fullKey] = BackendTrafficStats{
				ServiceKey:          svcKey,
				Connections:         uint64(dst.Stats.Connections),
				ActiveConnections:   connectionCountUint64(dst.ActiveConnections),
				InactiveConnections: connectionCountUint64(dst.InactiveConnections),
				InPkts:              uint64(dst.Stats.PacketsIn),
				OutPkts:             uint64(dst.Stats.PacketsOut),
				InBytes:             dst.Stats.BytesIn,
				OutBytes:            dst.Stats.BytesOut,
			}
		}
	}
	return svcResult, backendResult, nil
}

func connectionCountUint64(n int) uint64 {
	if n < 0 {
		return 0
	}
	return uint64(n)
}
