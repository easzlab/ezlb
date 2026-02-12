//go:build integration

package snat

import (
	"fmt"
	"strconv"
	"sync"

	"github.com/coreos/go-iptables/iptables"
	"go.uber.org/zap"
)

const (
	natTable  = "nat"
	snatChain = "EZLB-SNAT"
)

// linuxManager manages iptables SNAT rules on Linux using coreos/go-iptables.
type linuxManager struct {
	ipt     *iptables.IPTables
	managed map[string]SNATRule
	mu      sync.Mutex
	logger  *zap.Logger
}

// NewManager creates a new SNAT Manager backed by real iptables operations.
func NewManager(logger *zap.Logger) (Manager, error) {
	ipt, err := iptables.New()
	if err != nil {
		return nil, fmt.Errorf("failed to create iptables handle: %w", err)
	}

	mgr := &linuxManager{
		ipt:     ipt,
		managed: make(map[string]SNATRule),
		logger:  logger,
	}

	if err := mgr.ensureChain(); err != nil {
		return nil, fmt.Errorf("failed to initialize SNAT chain: %w", err)
	}

	return mgr, nil
}

// ensureChain creates the EZLB-SNAT chain and adds a jump rule from POSTROUTING.
func (m *linuxManager) ensureChain() error {
	exists, err := m.ipt.ChainExists(natTable, snatChain)
	if err != nil {
		return fmt.Errorf("failed to check chain existence: %w", err)
	}
	if !exists {
		if err := m.ipt.NewChain(natTable, snatChain); err != nil {
			return fmt.Errorf("failed to create chain %s: %w", snatChain, err)
		}
		m.logger.Info("created iptables chain", zap.String("chain", snatChain))
	}

	jumpRule := []string{"-j", snatChain}
	if err := m.ipt.AppendUnique(natTable, "POSTROUTING", jumpRule...); err != nil {
		return fmt.Errorf("failed to add jump rule to POSTROUTING: %w", err)
	}

	return nil
}

// Reconcile compares desired SNAT rules with the currently managed set,
// adding missing rules and removing stale ones.
func (m *linuxManager) Reconcile(desired []SNATRule) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	desiredMap := make(map[string]SNATRule, len(desired))
	for _, rule := range desired {
		desiredMap[rule.Key()] = rule
	}

	// Remove rules that are no longer desired
	for key, rule := range m.managed {
		if _, exists := desiredMap[key]; !exists {
			if err := m.deleteRule(rule); err != nil {
				m.logger.Error("failed to delete SNAT rule", zap.String("key", key), zap.Error(err))
			} else {
				delete(m.managed, key)
				m.logger.Info("deleted SNAT rule", zap.String("key", key))
			}
		}
	}

	// Add rules that are missing or have changed snat_ip
	for key, rule := range desiredMap {
		existing, exists := m.managed[key]
		if exists && existing.SnatIP == rule.SnatIP {
			continue
		}
		// If snat_ip changed, remove the old rule first
		if exists {
			if err := m.deleteRule(existing); err != nil {
				m.logger.Error("failed to delete old SNAT rule for update", zap.String("key", key), zap.Error(err))
				continue
			}
		}
		if err := m.addRule(rule); err != nil {
			m.logger.Error("failed to add SNAT rule", zap.String("key", key), zap.Error(err))
		} else {
			m.managed[key] = rule
			m.logger.Info("added SNAT rule", zap.String("key", key), zap.String("snat_ip", rule.SnatIP))
		}
	}

	return nil
}

// Cleanup removes all managed SNAT rules, the jump rule, and the custom chain.
func (m *linuxManager) Cleanup() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.ipt.ClearChain(natTable, snatChain); err != nil {
		m.logger.Error("failed to clear SNAT chain", zap.Error(err))
	}

	jumpRule := []string{"-j", snatChain}
	if err := m.ipt.DeleteIfExists(natTable, "POSTROUTING", jumpRule...); err != nil {
		m.logger.Error("failed to delete jump rule from POSTROUTING", zap.Error(err))
	}

	if err := m.ipt.DeleteChain(natTable, snatChain); err != nil {
		m.logger.Error("failed to delete SNAT chain", zap.Error(err))
	}

	m.managed = make(map[string]SNATRule)
	m.logger.Info("cleaned up all SNAT rules")
	return nil
}

// buildRuleSpec constructs the iptables rule arguments for a given SNATRule.
func buildRuleSpec(rule SNATRule) []string {
	portStr := strconv.Itoa(int(rule.BackendPort))
	spec := []string{
		"-d", rule.BackendIP,
		"-p", rule.Protocol,
		"--dport", portStr,
	}
	if rule.SnatIP != "" {
		spec = append(spec, "-j", "SNAT", "--to-source", rule.SnatIP)
	} else {
		spec = append(spec, "-j", "MASQUERADE")
	}
	return spec
}

func (m *linuxManager) addRule(rule SNATRule) error {
	spec := buildRuleSpec(rule)
	return m.ipt.AppendUnique(natTable, snatChain, spec...)
}

func (m *linuxManager) deleteRule(rule SNATRule) error {
	spec := buildRuleSpec(rule)
	return m.ipt.DeleteIfExists(natTable, snatChain, spec...)
}
