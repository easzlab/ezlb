package healthcheck

import (
	"context"
	"sync"
	"time"

	"github.com/easzlab/ezlb/pkg/config"
	"go.uber.org/zap"
)

// Target identifies a check by both service and backend. The same backend can
// have different probe settings and health states in different services.
type Target struct {
	Service string
	Address string
}

type checkSpec struct {
	checkType      string
	httpPath       string
	interval       time.Duration
	timeout        time.Duration
	failCount      int
	riseCount      int
	expectedStatus int
}

type backendStatus struct {
	cancel           context.CancelFunc
	check            *serviceCheckConfig
	address          string
	consecutiveFails int
	consecutiveOK    int
	healthy          bool
}

type serviceCheckConfig struct {
	checker   Checker
	spec      checkSpec
	interval  time.Duration
	failCount int
	riseCount int
	enabled   bool
}

// Manager orchestrates checks for all service/backend pairs.
type Manager struct {
	services map[string]*serviceCheckConfig
	statuses map[Target]*backendStatus
	onChange func()
	logger   *zap.Logger
	mu       sync.RWMutex
}

func NewManager(onChange func(), logger *zap.Logger) *Manager {
	return &Manager{
		services: make(map[string]*serviceCheckConfig),
		statuses: make(map[Target]*backendStatus),
		onChange: onChange,
		logger:   logger,
	}
}

// IsHealthy returns true for untracked targets, including disabled checks.
func (m *Manager) IsHealthy(service, address string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	status, exists := m.statuses[Target{Service: service, Address: address}]
	return !exists || status.healthy
}

func newServiceCheckConfig(cfg config.HealthCheckConfig) *serviceCheckConfig {
	spec := checkSpec{
		checkType:      cfg.GetType(),
		httpPath:       cfg.GetHTTPPath(),
		interval:       cfg.GetInterval(),
		timeout:        cfg.GetTimeout(),
		failCount:      cfg.GetFailCount(),
		riseCount:      cfg.GetRiseCount(),
		expectedStatus: cfg.GetHTTPExpectedStatus(),
	}
	var checker Checker
	if spec.checkType == "http" {
		checker = NewHTTPChecker(spec.timeout, spec.httpPath, spec.expectedStatus)
	} else {
		checker = NewTCPChecker(spec.timeout)
	}
	return &serviceCheckConfig{
		checker:   checker,
		spec:      spec,
		interval:  spec.interval,
		failCount: spec.failCount,
		riseCount: spec.riseCount,
		enabled:   true,
	}
}

// UpdateTargets replaces removed or reconfigured checks and starts new ones.
func (m *Manager) UpdateTargets(ctx context.Context, services []config.ServiceConfig) {
	m.mu.Lock()
	defer m.mu.Unlock()

	desired := make(map[Target]*serviceCheckConfig)
	newServices := make(map[string]*serviceCheckConfig, len(services))
	for _, svc := range services {
		if !svc.HealthCheck.IsEnabled() {
			newServices[svc.Name] = &serviceCheckConfig{enabled: false}
			continue
		}
		check := newServiceCheckConfig(svc.HealthCheck)
		newServices[svc.Name] = check
		for _, backend := range svc.Backends {
			desired[Target{Service: svc.Name, Address: backend.Address}] = check
		}
	}

	for target, status := range m.statuses {
		check, exists := desired[target]
		if exists && status.check != nil && status.check.spec == check.spec {
			continue
		}
		if status.cancel != nil {
			status.cancel()
		}
		delete(m.statuses, target)
		m.logger.Info("stopped health check", zap.String("service", target.Service), zap.String("address", target.Address))
	}
	for target, check := range desired {
		if _, exists := m.statuses[target]; !exists {
			m.startBackendCheckLocked(ctx, target, check)
		}
	}
	m.services = newServices
}

// startBackendCheckLocked must be called with m.mu held.
func (m *Manager) startBackendCheckLocked(ctx context.Context, target Target, check *serviceCheckConfig) {
	checkCtx, cancel := context.WithCancel(ctx)
	status := &backendStatus{address: target.Address, healthy: true, cancel: cancel, check: check}
	m.statuses[target] = status
	m.logger.Info("started health check", zap.String("service", target.Service), zap.String("address", target.Address))
	go m.runCheck(checkCtx, target, check)
}

func (m *Manager) runCheck(ctx context.Context, target Target, check *serviceCheckConfig) {
	ticker := time.NewTicker(check.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			err := check.checker.Check(target.Address)
			if ctx.Err() != nil {
				return
			}
			m.handleCheckResult(target, err, check)
		}
	}
}

// handleCheckResult ignores results from checks replaced during a config reload.
func (m *Manager) handleCheckResult(target Target, checkErr error, check *serviceCheckConfig) {
	m.mu.Lock()
	status, exists := m.statuses[target]
	if !exists || (status.check != nil && status.check != check) {
		m.mu.Unlock()
		return
	}
	previouslyHealthy := status.healthy
	if checkErr != nil {
		status.consecutiveFails++
		status.consecutiveOK = 0
		if status.healthy && status.consecutiveFails >= check.failCount {
			status.healthy = false
			m.logger.Warn("backend marked unhealthy", zap.String("service", target.Service), zap.String("address", target.Address), zap.Error(checkErr))
		}
	} else {
		status.consecutiveOK++
		status.consecutiveFails = 0
		if !status.healthy && status.consecutiveOK >= check.riseCount {
			status.healthy = true
			m.logger.Info("backend marked healthy", zap.String("service", target.Service), zap.String("address", target.Address))
		}
	}
	changed := previouslyHealthy != status.healthy
	m.mu.Unlock()
	if changed && m.onChange != nil {
		m.onChange()
	}
}

func (m *Manager) GetAllStatuses() map[Target]bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make(map[Target]bool, len(m.statuses))
	for target, status := range m.statuses {
		result[target] = status.healthy
	}
	return result
}

func (m *Manager) Stop() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, status := range m.statuses {
		if status.cancel != nil {
			status.cancel()
		}
	}
	m.statuses = make(map[Target]*backendStatus)
	m.services = make(map[string]*serviceCheckConfig)
	m.logger.Info("all health checks stopped")
}
