package snat

import "fmt"

// SNATRule describes a single SNAT/MASQUERADE rule for a backend destination.
type SNATRule struct {
	VIP         string
	BackendIP   string
	Protocol    string
	SnatIP      string
	VIPPort     uint16
	BackendPort uint16
}

// Key returns a unique string identifier for this rule.
func (r SNATRule) Key() string {
	return fmt.Sprintf("%s:%d/%s->%s:%d", r.VIP, r.VIPPort, r.Protocol, r.BackendIP, r.BackendPort)
}

// ForwardRule allows both directions of one IPVS service through FORWARD.
type ForwardRule struct {
	VIP      string
	Protocol string
	VIPPort  uint16
}

// Key returns a unique string identifier for this forward rule.
func (r ForwardRule) Key() string {
	return fmt.Sprintf("%s:%d/%s", r.VIP, r.VIPPort, r.Protocol)
}

// Manager defines the interface for managing iptables SNAT and FORWARD rules.
// Implementations must be safe for concurrent use.
type Manager interface {
	// Reconcile ensures the actual iptables SNAT rules match the desired state.
	// Rules not in the desired set are removed; missing rules are added.
	Reconcile(desired []SNATRule) error

	// ReconcileForward ensures the FORWARD chain ACCEPT rules match the desired state.
	// This allows IPVS NAT traffic to pass through the FORWARD chain even when
	// the default policy is DROP (e.g. Docker environments).
	ReconcileForward(desired []ForwardRule) error

	// Cleanup removes all SNAT/FORWARD rules and custom chains managed by this Manager.
	Cleanup() error
}
