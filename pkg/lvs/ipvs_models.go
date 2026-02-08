package lvs

import (
	"net"
	"time"
)

// Service defines an IPVS service in its entirety.
type Service struct {
	Address       net.IP
	Protocol      uint16
	Port          uint16
	FWMark        uint32
	SchedName     string
	Flags         uint32
	Timeout       uint32
	Netmask       uint32
	AddressFamily uint16
	PEName        string
	Stats         SvcStats
}

// SvcStats defines IPVS service statistics.
type SvcStats struct {
	Connections uint32
	PacketsIn   uint32
	PacketsOut  uint32
	BytesIn     uint64
	BytesOut    uint64
	CPS         uint32
	BPSOut      uint32
	PPSIn       uint32
	PPSOut      uint32
	BPSIn       uint32
}

// Destination defines an IPVS destination (real server) in its entirety.
type Destination struct {
	Address             net.IP
	Port                uint16
	Weight              int
	ConnectionFlags     uint32
	AddressFamily       uint16
	UpperThreshold      uint32
	LowerThreshold      uint32
	ActiveConnections   int
	InactiveConnections int
	Stats               DstStats
}

// DstStats defines IPVS destination (real server) statistics.
type DstStats SvcStats

// Config defines IPVS timeout configuration.
type IPVSConfig struct {
	TimeoutTCP    time.Duration
	TimeoutTCPFin time.Duration
	TimeoutUDP    time.Duration
}

// Destination forwarding method constants.
const (
	ConnectionFlagFwdMask    = 0x0007
	ConnectionFlagMasq       = 0x0000
	ConnectionFlagLocalNode  = 0x0001
	ConnectionFlagTunnel     = 0x0002
	ConnectionFlagDirectRoute = 0x0003
)

// Scheduling algorithm constants.
const (
	RoundRobin             = "rr"
	LeastConnection        = "lc"
	DestinationHashing     = "dh"
	SourceHashing          = "sh"
	WeightedRoundRobin     = "wrr"
	WeightedLeastConnection = "wlc"
)

// Connection forwarding method constants (aliases).
const (
	ConnFwdMask        = 0x0007
	ConnFwdMasq        = 0x0000
	ConnFwdLocalNode   = 0x0001
	ConnFwdTunnel      = 0x0002
	ConnFwdDirectRoute = 0x0003
	ConnFwdBypass      = 0x0004
)
