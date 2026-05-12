package trafficmetrics

// ServiceTrafficStats holds cumulative IPVS service-level statistics.
type ServiceTrafficStats struct {
	Connections uint64
	InPkts      uint64
	OutPkts     uint64
	InBytes     uint64
	OutBytes    uint64
}

// BackendTrafficStats holds IPVS backend-level traffic and connection statistics.
//
// Field semantics (all values come directly from IPVS, see lvs_stats_adapter.go):
//   - Connections: cumulative connection count since IPVS counter start.
//   - ActiveConnections / InactiveConnections: instantaneous gauges sourced
//     from IPVS destination ActConns / InActConns.
//   - InPkts / OutPkts / InBytes / OutBytes: cumulative counters.
//
// CurrentConnections has been removed; consumers should use
// ActiveConnections + InactiveConnections directly when they need a
// "current connection" gauge. Removing the field eliminates the
// "if zero, fall back to active+inactive" pattern that hid data gaps.
type BackendTrafficStats struct {
	ServiceKey          string
	Connections         uint64
	ActiveConnections   uint64
	InactiveConnections uint64
	InPkts              uint64
	OutPkts             uint64
	InBytes             uint64
	OutBytes            uint64
}

// TrafficSnapshot holds a point-in-time snapshot of all statistics.
type TrafficSnapshot struct {
	Services map[string]ServiceTrafficStats
	Backends map[string]BackendTrafficStats
}

// LVSStatsProvider abstracts IPVS statistics retrieval.
type LVSStatsProvider interface {
	AllStats() (services map[string]ServiceTrafficStats, backends map[string]BackendTrafficStats, err error)
}
