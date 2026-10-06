package lvs

import (
	"errors"
	"fmt"
	"net"
	"strconv"
	"sync"

	"github.com/easzlab/ezlb/pkg/config"
	"github.com/easzlab/ezlb/pkg/metrics"
	"github.com/easzlab/ezlb/pkg/snat"
	"go.uber.org/zap"
)

// HealthChecker is the interface used by Reconciler to query backend health status.
// This decouples the lvs package from the healthcheck package.
type HealthChecker interface {
	IsHealthy(service, address string) bool
}

// managedServiceInfo stores config metadata for a managed service,
// used to clean up Prometheus metrics when the service is removed.
type managedServiceInfo struct {
	Name     string
	Listen   string
	Protocol string
	Backends []string // backend addresses
}

// Reconciler implements declarative reconciliation between desired state (config + health)
// and actual state (IPVS kernel rules + iptables SNAT rules).
type Reconciler struct {
	manager   *Manager
	healthMgr HealthChecker
	snatMgr   snat.Manager
	logger    *zap.Logger
	// exclusiveNetns declares whether ezlb owns every IPVS service in the
	// current network namespace. Shared mode is for integrations such as
	// kubeasz, where kube-proxy owns unrelated IPVS services in the host
	// namespace.
	exclusiveNetns bool
	managed        map[ServiceKey]*managedServiceInfo // metadata for metrics cleanup in this process
	mu             sync.Mutex
}

// NewReconciler creates a new Reconciler.
func NewReconciler(manager *Manager, healthMgr HealthChecker, snatMgr snat.Manager, logger *zap.Logger) *Reconciler {
	return newReconciler(manager, healthMgr, snatMgr, logger, true)
}

// NewSharedNetnsReconciler creates a reconciler that only manages services it
// has created or adopted from its current configuration. It is safe to use in
// a network namespace that also contains IPVS services managed by another
// component, such as Kubernetes kube-proxy.
func NewSharedNetnsReconciler(manager *Manager, healthMgr HealthChecker, snatMgr snat.Manager, logger *zap.Logger) *Reconciler {
	return newReconciler(manager, healthMgr, snatMgr, logger, false)
}

func newReconciler(manager *Manager, healthMgr HealthChecker, snatMgr snat.Manager, logger *zap.Logger, exclusiveNetns bool) *Reconciler {
	return &Reconciler{
		manager:        manager,
		healthMgr:      healthMgr,
		snatMgr:        snatMgr,
		logger:         logger,
		exclusiveNetns: exclusiveNetns,
		managed:        make(map[ServiceKey]*managedServiceInfo),
	}
}

// desiredService holds the desired IPVS service and its destinations after health filtering.
type desiredService struct {
	service      *Service
	destinations []*Destination
	config       config.ServiceConfig
}

// Reconcile compares the desired state (from config + health check) with the actual IPVS state
// and applies the necessary changes to bring the kernel in sync.
func (r *Reconciler) Reconcile(desiredConfigs []config.ServiceConfig) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.logger.Info("starting reconcile", zap.Int("desired_services", len(desiredConfigs)))

	// Phase 1: Build desired state
	desiredMap, err := r.buildDesiredState(desiredConfigs)
	if err != nil {
		return fmt.Errorf("failed to build desired state: %w", err)
	}

	// Phase 2: Get actual state from IPVS kernel
	actualServices, err := r.manager.GetServices()
	if err != nil {
		return fmt.Errorf("failed to get current IPVS services: %w", err)
	}

	actualMap := make(map[ServiceKey]*Service)
	for _, svc := range actualServices {
		key := ServiceKeyFromIPVS(svc)
		actualMap[key] = svc
	}

	previouslyManaged := make(map[ServiceKey]*managedServiceInfo, len(r.managed))
	for key, info := range r.managed {
		previouslyManaged[key] = info
	}

	var reconcileErrors []error

	// Phase 3: Service-level diff
	// Create or update services that are in desired but missing or different in actual
	for key, desired := range desiredMap {
		actual, exists := actualMap[key]
		if !exists {
			// Service does not exist in IPVS -> create it
			if err := r.manager.CreateService(desired.service); err != nil {
				reconcileErrors = append(reconcileErrors, fmt.Errorf("create service %s: %w", key, err))
				continue
			}
		} else {
			// Service exists -> mark as managed and check if scheduler needs update
			if actual.SchedName != desired.service.SchedName {
				if err := r.manager.UpdateService(desired.service); err != nil {
					reconcileErrors = append(reconcileErrors, fmt.Errorf("update service %s: %w", key, err))
					continue
				}
			}
		}
		info := newManagedServiceInfo(desired.config)
		if previous := r.managed[key]; previous != nil &&
			(previous.Name != info.Name || previous.Listen != info.Listen || previous.Protocol != info.Protocol) {
			metrics.DeleteServiceMetrics(previous.Name, previous.Listen, previous.Protocol)
			for _, backend := range previous.Backends {
				metrics.DeleteBackendMetrics(previous.Name, backend, previous.Protocol)
			}
		}
		r.managed[key] = info

		// Phase 4: Destination-level diff for this service
		if err := r.reconcileDestinations(desired); err != nil {
			reconcileErrors = append(reconcileErrors, err)
		}
	}

	if r.exclusiveNetns {
		// An exclusive namespace belongs wholly to ezlb, so remove every
		// service no longer declared in the configuration.
		for key, actual := range actualMap {
			if _, exists := desiredMap[key]; !exists {
				r.deleteService(key, actual, &reconcileErrors)
			}
		}
	} else {
		// In a shared namespace, never delete services merely because they are
		// absent from this configuration: another controller may own them.
		// Only remove a service tracked by this reconciler before the current
		// update, which covers a service deleted from a hot-reloaded config.
		for key := range previouslyManaged {
			if _, exists := desiredMap[key]; exists {
				continue
			}
			actual, exists := actualMap[key]
			if !exists {
				delete(r.managed, key)
				continue
			}
			r.deleteService(key, actual, &reconcileErrors)
		}
	}

	// Phase 5: Reconcile SNAT rules for services with full_nat enabled
	if err := r.reconcileSNAT(desiredConfigs); err != nil {
		reconcileErrors = append(reconcileErrors, fmt.Errorf("snat reconcile: %w", err))
	}

	if len(reconcileErrors) > 0 {
		r.logger.Error("reconcile completed with errors", zap.Int("error_count", len(reconcileErrors)))
		// Increment error counter for each error
		for range reconcileErrors {
			metrics.IncReconcileErrors()
		}
		return errors.Join(reconcileErrors...)
	}

	r.logger.Info("reconcile completed successfully")
	return nil
}

// Cleanup removes managed IPVS services. In exclusive mode it removes every
// service in the namespace; in shared mode it preserves services owned by
// other controllers.
func (r *Reconciler) Cleanup() error {
	r.mu.Lock()
	defer r.mu.Unlock()

	actualServices, err := r.manager.GetServices()
	if err != nil {
		return fmt.Errorf("failed to get IPVS services for cleanup: %w", err)
	}

	actualMap := make(map[ServiceKey]*Service)
	for _, svc := range actualServices {
		actualMap[ServiceKeyFromIPVS(svc)] = svc
	}

	var errs []error
	if r.exclusiveNetns {
		for key, svc := range actualMap {
			if err := r.manager.DeleteService(svc); err != nil {
				errs = append(errs, fmt.Errorf("delete service %s: %w", key, err))
			} else {
				delete(r.managed, key)
			}
		}
	} else {
		for key := range r.managed {
			svc, exists := actualMap[key]
			if !exists {
				delete(r.managed, key)
				continue
			}
			if err := r.manager.DeleteService(svc); err != nil {
				errs = append(errs, fmt.Errorf("delete service %s: %w", key, err))
			} else {
				delete(r.managed, key)
			}
		}
	}

	if len(errs) > 0 {
		return errors.Join(errs...)
	}
	r.managed = make(map[ServiceKey]*managedServiceInfo)
	r.logger.Info("cleaned up managed IPVS services")
	return nil
}

func (r *Reconciler) deleteService(key ServiceKey, actual *Service, reconcileErrors *[]error) {
	if err := r.manager.DeleteService(actual); err != nil {
		*reconcileErrors = append(*reconcileErrors, fmt.Errorf("delete service %s: %w", key, err))
		return
	}
	if info := r.managed[key]; info != nil {
		metrics.DeleteServiceMetrics(info.Name, info.Listen, info.Protocol)
		for _, backend := range info.Backends {
			metrics.DeleteBackendMetrics(info.Name, backend, info.Protocol)
		}
	}
	delete(r.managed, key)
}

func newManagedServiceInfo(cfg config.ServiceConfig) *managedServiceInfo {
	backends := make([]string, len(cfg.Backends))
	for i, b := range cfg.Backends {
		backends[i] = b.Address
		if host, port, err := net.SplitHostPort(b.Address); err == nil {
			if ip := net.ParseIP(host); ip != nil {
				if n, err := strconv.Atoi(port); err == nil {
					backends[i] = net.JoinHostPort(ip.String(), strconv.Itoa(n))
				}
			}
		}
	}
	return &managedServiceInfo{
		Name:     cfg.Name,
		Listen:   cfg.Listen,
		Protocol: cfg.Protocol,
		Backends: backends,
	}
}

// reconcileSNAT builds the desired SNAT and FORWARD rules from configs with
// full_nat enabled and delegates to the SNAT manager for declarative reconciliation.
// FORWARD rules are needed because IPVS NAT mode requires packets to traverse
// the FORWARD chain, which may have a DROP policy (e.g. Docker environments).
func (r *Reconciler) reconcileSNAT(configs []config.ServiceConfig) error {
	var desiredSNATRules []snat.SNATRule
	var desiredForwardRules []snat.ForwardRule

	for _, svcCfg := range configs {
		if !svcCfg.FullNAT {
			continue
		}
		vip, vipPortStr, err := net.SplitHostPort(svcCfg.Listen)
		if err != nil {
			return fmt.Errorf("service %q: invalid listen address: %w", svcCfg.Name, err)
		}
		vipPort, err := strconv.Atoi(vipPortStr)
		if err != nil {
			return fmt.Errorf("service %q: invalid listen port: %w", svcCfg.Name, err)
		}
		protocol := svcCfg.Protocol
		if protocol == "" {
			protocol = "tcp"
		}
		hasHealthyBackend := false

		for _, backendCfg := range svcCfg.Backends {
			// Only create rules for healthy backends
			if svcCfg.HealthCheck.IsEnabled() && !r.healthMgr.IsHealthy(svcCfg.Name, backendCfg.Address) {
				continue
			}
			hasHealthyBackend = true

			backendHost, backendPortStr, err := net.SplitHostPort(backendCfg.Address)
			if err != nil {
				return fmt.Errorf("service %q, backend %q: invalid address: %w", svcCfg.Name, backendCfg.Address, err)
			}
			backendPort, err := strconv.Atoi(backendPortStr)
			if err != nil {
				return fmt.Errorf("service %q, backend %q: invalid port: %w", svcCfg.Name, backendCfg.Address, err)
			}

			desiredSNATRules = append(desiredSNATRules, snat.SNATRule{
				VIP:         vip,
				VIPPort:     uint16(vipPort),
				BackendIP:   backendHost,
				BackendPort: uint16(backendPort),
				Protocol:    protocol,
				SnatIP:      svcCfg.SnatIP,
			})

		}
		if hasHealthyBackend {
			desiredForwardRules = append(desiredForwardRules, snat.ForwardRule{
				VIP:      vip,
				VIPPort:  uint16(vipPort),
				Protocol: protocol,
			})
		}
	}

	if err := r.snatMgr.Reconcile(desiredSNATRules); err != nil {
		return fmt.Errorf("snat rules: %w", err)
	}

	if err := r.snatMgr.ReconcileForward(desiredForwardRules); err != nil {
		return fmt.Errorf("forward rules: %w", err)
	}

	return nil
}

// buildDesiredState converts config services into the desired IPVS state,
// filtering out unhealthy backends.
func (r *Reconciler) buildDesiredState(configs []config.ServiceConfig) (map[ServiceKey]*desiredService, error) {
	result := make(map[ServiceKey]*desiredService)

	for _, svcCfg := range configs {
		ipvsSvc, err := ConfigToIPVSService(svcCfg)
		if err != nil {
			return nil, fmt.Errorf("service %q: %w", svcCfg.Name, err)
		}

		key, err := ServiceKeyFromConfig(svcCfg)
		if err != nil {
			return nil, fmt.Errorf("service %q: %w", svcCfg.Name, err)
		}

		var destinations []*Destination
		for _, backendCfg := range svcCfg.Backends {
			// Filter out unhealthy backends (only when health check is enabled)
			if svcCfg.HealthCheck.IsEnabled() && !r.healthMgr.IsHealthy(svcCfg.Name, backendCfg.Address) {
				r.logger.Info("skipping unhealthy backend",
					zap.String("service", svcCfg.Name),
					zap.String("backend", backendCfg.Address),
				)
				continue
			}

			dst, err := ConfigToIPVSDestinationWithMode(backendCfg, svcCfg.GetForwardMode())
			if err != nil {
				return nil, fmt.Errorf("service %q, backend %q: %w", svcCfg.Name, backendCfg.Address, err)
			}
			destinations = append(destinations, dst)
		}

		result[key] = &desiredService{
			service:      ipvsSvc,
			destinations: destinations,
			config:       svcCfg,
		}
	}

	return result, nil
}

// reconcileDestinations performs a diff on destinations for a single service.
func (r *Reconciler) reconcileDestinations(desired *desiredService) error {
	// Get actual destinations from IPVS
	actualDests, err := r.manager.GetDestinations(desired.service)
	if err != nil {
		return fmt.Errorf("get destinations for %s:%d: %w",
			desired.service.Address, desired.service.Port, err)
	}

	// Build maps for comparison
	actualDestMap := make(map[DestinationKey]*Destination)
	for _, dst := range actualDests {
		key := DestinationKeyFromIPVS(dst)
		actualDestMap[key] = dst
	}

	desiredDestMap := make(map[DestinationKey]*Destination)
	for _, dst := range desired.destinations {
		key := DestinationKey{
			Address: dst.Address.String(),
			Port:    dst.Port,
		}
		desiredDestMap[key] = dst
	}

	var reconcileErrors []error

	// Create or update destinations
	for key, desiredDst := range desiredDestMap {
		actualDst, exists := actualDestMap[key]
		if !exists {
			// Destination does not exist -> create
			if err := r.manager.CreateDestination(desired.service, desiredDst); err != nil {
				reconcileErrors = append(reconcileErrors, fmt.Errorf("create destination %s: %w", key, err))
			}
		} else {
			// Destination exists -> check if weight or forward mode needs update
			actualFlag := actualDst.ConnectionFlags & ConnectionFlagFwdMask
			desiredFlag := desiredDst.ConnectionFlags & ConnectionFlagFwdMask
			if actualDst.Weight != desiredDst.Weight || actualFlag != desiredFlag {
				if err := r.manager.UpdateDestination(desired.service, desiredDst); err != nil {
					reconcileErrors = append(reconcileErrors, fmt.Errorf("update destination %s: %w", key, err))
				}
			}
		}
	}

	// Delete destinations that are in actual but not in desired
	for key, actualDst := range actualDestMap {
		if _, exists := desiredDestMap[key]; !exists {
			if err := r.manager.DeleteDestination(desired.service, actualDst); err != nil {
				reconcileErrors = append(reconcileErrors, fmt.Errorf("delete destination %s: %w", key, err))
			} else {
				metrics.DeleteBackendTrafficMetrics(desired.config.Name, key.String(), desired.config.Protocol)
			}
		}
	}

	if len(reconcileErrors) > 0 {
		return errors.Join(reconcileErrors...)
	}
	return nil
}
