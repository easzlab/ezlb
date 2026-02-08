package lvs

import (
	"errors"
	"fmt"
	"sync"

	"github.com/easzlab/ezlb/pkg/config"
	"go.uber.org/zap"
)

// HealthChecker is the interface used by Reconciler to query backend health status.
// This decouples the lvs package from the healthcheck package.
type HealthChecker interface {
	IsHealthy(address string) bool
}

// Reconciler implements declarative reconciliation between desired state (config + health)
// and actual state (IPVS kernel rules).
type Reconciler struct {
	manager   *Manager
	healthMgr HealthChecker
	logger    *zap.Logger
	managed   map[ServiceKey]bool // tracks services managed by ezlb
	mu        sync.Mutex
}

// NewReconciler creates a new Reconciler.
func NewReconciler(manager *Manager, healthMgr HealthChecker, logger *zap.Logger) *Reconciler {
	return &Reconciler{
		manager:   manager,
		healthMgr: healthMgr,
		logger:    logger,
		managed:   make(map[ServiceKey]bool),
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
		if r.managed[key] {
			actualMap[key] = svc
		}
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
			r.managed[key] = true
		} else {
			// Service exists -> check if scheduler needs update
			if actual.SchedName != desired.service.SchedName {
				if err := r.manager.UpdateService(desired.service); err != nil {
					reconcileErrors = append(reconcileErrors, fmt.Errorf("update service %s: %w", key, err))
					continue
				}
			}
		}

		// Phase 4: Destination-level diff for this service
		if err := r.reconcileDestinations(desired); err != nil {
			reconcileErrors = append(reconcileErrors, err)
		}
	}

	// Delete services that are in actual (and managed by ezlb) but not in desired
	for key, actual := range actualMap {
		if _, exists := desiredMap[key]; !exists {
			if err := r.manager.DeleteService(actual); err != nil {
				reconcileErrors = append(reconcileErrors, fmt.Errorf("delete service %s: %w", key, err))
			} else {
				delete(r.managed, key)
			}
		}
	}

	if len(reconcileErrors) > 0 {
		r.logger.Error("reconcile completed with errors", zap.Int("error_count", len(reconcileErrors)))
		return errors.Join(reconcileErrors...)
	}

	r.logger.Info("reconcile completed successfully")
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
			if svcCfg.HealthCheck.IsEnabled() && !r.healthMgr.IsHealthy(backendCfg.Address) {
				r.logger.Info("skipping unhealthy backend",
					zap.String("service", svcCfg.Name),
					zap.String("backend", backendCfg.Address),
				)
				continue
			}

			dst, err := ConfigToIPVSDestination(backendCfg)
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
			// Destination exists -> check if weight needs update
			if actualDst.Weight != desiredDst.Weight {
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
			}
		}
	}

	if len(reconcileErrors) > 0 {
		return errors.Join(reconcileErrors...)
	}
	return nil
}
