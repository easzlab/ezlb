package snat

import "fmt"

// SNATRule describes a single SNAT/MASQUERADE rule for a backend destination.
type SNATRule struct {
	BackendIP   string // destination real server IP
	BackendPort uint16 // destination port
	Protocol    string // "tcp" or "udp"
	SnatIP      string // SNAT source IP; empty means use MASQUERADE
}

// Key returns a unique string identifier for this rule.
func (r SNATRule) Key() string {
	return fmt.Sprintf("%s:%d/%s", r.BackendIP, r.BackendPort, r.Protocol)
}

// Manager defines the interface for managing iptables SNAT rules.
// Implementations must be safe for concurrent use.
type Manager interface {
	// Reconcile ensures the actual iptables SNAT rules match the desired state.
	// Rules not in the desired set are removed; missing rules are added.
	Reconcile(desired []SNATRule) error

	// Cleanup removes all SNAT rules and the custom chain managed by this Manager.
	Cleanup() error
}
