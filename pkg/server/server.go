package server

import (
	"context"
	"fmt"
	"time"

	"github.com/easzlab/ezlb/pkg/admin"
	"github.com/easzlab/ezlb/pkg/config"
	"github.com/easzlab/ezlb/pkg/healthcheck"
	"github.com/easzlab/ezlb/pkg/lvs"
	"github.com/easzlab/ezlb/pkg/metrics"
	"github.com/easzlab/ezlb/pkg/snat"
	"github.com/easzlab/ezlb/pkg/trafficmetrics"
	"go.uber.org/zap"
)

// reconcileDebounceWindow is the window during which multiple reconcile signals
// triggered by rapid health state flips are coalesced into a single reconcile pass.
const reconcileDebounceWindow = 200 * time.Millisecond

// Server coordinates all modules and manages the overall service lifecycle.
type Server struct {
	configMgr            *config.Manager
	lvsMgr               *lvs.Manager
	reconciler           *lvs.Reconciler
	healthMgr            *healthcheck.Manager
	snatMgr              snat.Manager
	adminServer          *admin.Server
	logger               *zap.Logger
	collector            *trafficmetrics.Collector
	reconcileSignal      chan struct{}
	prevHealthBackends   map[healthMetricKey]struct{}
}

type healthMetricKey struct {
	service string
	backend string
}

// NewServer initializes all modules and returns a ready-to-run Server.
func NewServer(configPath string, logger *zap.Logger) (*Server, error) {
	// Initialize IPVS manager
	lvsMgr, err := lvs.NewManager(logger.Named("lvs"))
	if err != nil {
		return nil, fmt.Errorf("failed to initialize IPVS manager: %w", err)
	}

	return newServerWithManager(configPath, lvsMgr, logger)
}

// newServerWithManager initializes a Server with a pre-created LVS Manager.
// This allows tests to inject a platform-appropriate Manager instance.
func newServerWithManager(configPath string, lvsMgr *lvs.Manager, logger *zap.Logger) (*Server, error) {
	// Initialize config manager
	configMgr, err := config.NewManager(configPath, logger.Named("config"))
	if err != nil {
		return nil, fmt.Errorf("failed to initialize config manager: %w", err)
	}

	// Initialize SNAT manager
	snatMgr, err := snat.NewManager(logger.Named("snat"))
	if err != nil {
		return nil, fmt.Errorf("failed to initialize SNAT manager: %w", err)
	}

	server := &Server{
		configMgr:       configMgr,
		lvsMgr:          lvsMgr,
		snatMgr:         snatMgr,
		logger:          logger,
		reconcileSignal: make(chan struct{}, 1),
	}

	// Initialize health check manager with onChange callback that triggers reconcile
	server.healthMgr = healthcheck.NewManager(func() {
		server.triggerReconcile()
		server.updateHealthMetrics()
	}, logger.Named("healthcheck"))

	// Initialize reconciler with health checker and SNAT manager
	server.reconciler = lvs.NewReconciler(lvsMgr, server.healthMgr, snatMgr, logger.Named("reconciler"))

	return server, nil
}

// Run starts the server in daemon mode: performs initial reconcile, starts health checks
// and config watching, then enters the main event loop until context is cancelled.
func (s *Server) Run(ctx context.Context) error {
	cfg := s.configMgr.GetConfig()
	s.logKernelParamPreflight()

	// Initialize admin server if configured
	if cfg.Global.AdminAddress != "" {
		s.initAdminServer(cfg)
	}

	// Set up config reload callback for metrics
	s.configMgr.SetOnReloadCallback(func() {
		metrics.IncConfigReload()
	})

	// Register health check targets and start checking
	s.healthMgr.UpdateTargets(ctx, cfg.Services)

	// Perform initial reconcile
	if err := s.reconciler.Reconcile(cfg.Services); err != nil {
		s.logger.Error("initial reconcile failed", zap.Error(err))
	}

	s.syncTrafficCollector(cfg)

	// Start config file watching
	s.configMgr.WatchConfig()
	s.logger.Info("config watcher started")

	// Main event loop
	s.logger.Info("server started, entering main loop")
	for {
		select {
		case <-s.configMgr.OnChange():
			s.logger.Info("config change detected, triggering reconcile")
			newCfg := s.configMgr.GetConfig()
			s.healthMgr.UpdateTargets(ctx, newCfg.Services)
			if err := s.reconciler.Reconcile(newCfg.Services); err != nil {
				s.logger.Error("reconcile after config change failed", zap.Error(err))
			}
			s.syncTrafficCollector(newCfg)

		case <-s.reconcileSignal:
			// Coalesce rapid health flips: drain any additional signals arriving
			// within the debounce window and perform a single reconcile pass.
			s.drainReconcileSignals(ctx)
			cfg := s.configMgr.GetConfig()
			if err := s.reconciler.Reconcile(cfg.Services); err != nil {
				s.logger.Error("reconcile after health change failed", zap.Error(err))
			}

		case <-ctx.Done():
			s.logger.Info("shutdown signal received, stopping server")
			s.shutdown()
			return nil
		}
	}
}

// drainReconcileSignals waits up to reconcileDebounceWindow and consumes any
// additional reconcile signals that arrive within the window, so that a burst
// of health flips results in a single reconcile pass.
func (s *Server) drainReconcileSignals(ctx context.Context) {
	timer := time.NewTimer(reconcileDebounceWindow)
	defer timer.Stop()
	for {
		select {
		case <-s.reconcileSignal:
			// another signal arrived within the window, keep draining
		case <-timer.C:
			return
		case <-ctx.Done():
			return
		}
	}
}

// RunOnce performs a single reconcile pass and then exits.
// IPVS rules and iptables rules are intentionally preserved after exit —
// cleanup_on_exit does not apply to once mode, whose purpose is to apply
// the desired state and leave it in place.
func (s *Server) RunOnce() error {
	cfg := s.configMgr.GetConfig()
	s.logKernelParamPreflight()

	err := s.reconciler.Reconcile(cfg.Services)
	s.lvsMgr.Close()

	if err != nil {
		return fmt.Errorf("reconcile failed: %w", err)
	}
	return nil
}

// triggerReconcile is called by the health check manager when a backend's health status changes.
// It posts a non-blocking signal to the main event loop which coalesces rapid flips
// into a single reconcile pass (see drainReconcileSignals).
func (s *Server) triggerReconcile() {
	select {
	case s.reconcileSignal <- struct{}{}:
	default:
		// A reconcile signal is already pending; the next reconcile will cover this change too.
	}
}

// updateHealthMetrics updates the health status metrics for all backends
// and removes stale metrics for backends no longer in the config.
func (s *Server) updateHealthMetrics() {
	cfg := s.configMgr.GetConfig()
	statuses := s.healthMgr.GetAllStatuses()

	// Build a map of backend address to service name
	backendToService := make(map[string]string)
	for _, svc := range cfg.Services {
		for _, backend := range svc.Backends {
			backendToService[backend.Address] = svc.Name
		}
	}

	// Update metrics for each backend and track current keys
	currentBackends := make(map[healthMetricKey]struct{})
	for address, healthy := range statuses {
		serviceName := backendToService[address]
		if serviceName == "" {
			serviceName = "unknown"
		}
		metrics.SetBackendHealth(serviceName, address, healthy)
		currentBackends[healthMetricKey{service: serviceName, backend: address}] = struct{}{}
	}

	// Delete stale health metrics for removed backends
	for key := range s.prevHealthBackends {
		if _, exists := currentBackends[key]; !exists {
			metrics.DeleteBackendHealthMetrics(key.service, key.backend)
		}
	}
	s.prevHealthBackends = currentBackends
}

// syncTrafficCollector starts the Prometheus traffic stats collector when
// metrics are enabled, or pushes the new config to an already-running
// collector on hot reload. The collector only runs in daemon mode (Run);
// it is intentionally not started by RunOnce.
func (s *Server) syncTrafficCollector(cfg *config.Config) {
	if cfg == nil {
		return
	}

	if s.collector == nil {
		if !cfg.Global.IsMetricsEnabled() {
			return
		}

		lvsStats := trafficmetrics.NewLVSStatsAdapter(s.lvsMgr, s.logger.Named("lvsstats"))
		s.collector = trafficmetrics.NewCollector(
			lvsStats,
			s.logger.Named("trafficstats"),
			cfg.Services,
			cfg.Global,
		)
		s.collector.Start()
		s.logger.Info("traffic stats collector started",
			zap.Duration("interval", cfg.Global.GetMetricsInterval()),
		)
		return
	}

	if !cfg.Global.IsMetricsEnabled() {
		s.collector.Stop()
		s.collector = nil
		s.logger.Info("traffic stats collector stopped (metrics disabled)")
		return
	}

	s.collector.UpdateConfig(cfg.Services, cfg.Global)
}

// initAdminServer initializes and starts the admin HTTP server.
func (s *Server) initAdminServer(cfg *config.Config) {
	adminCfg := admin.Config{
		ListenAddr:     cfg.Global.AdminAddress,
		MetricsEnabled: cfg.Global.IsMetricsEnabled(),
		MetricsPath:    cfg.Global.GetMetricsPath(),
	}

	s.adminServer = admin.NewServer(adminCfg, s.logger.Named("admin"))

	// Set up health check function for admin server
	s.adminServer.SetHealthCheckFunc(func() map[string]bool {
		return s.healthMgr.GetAllStatuses()
	})

	if err := s.adminServer.Start(); err != nil {
		s.logger.Error("failed to start admin server", zap.Error(err))
	}
}

// shutdown gracefully stops all modules.
func (s *Server) shutdown() {
	// Stop admin server first
	if s.adminServer != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := s.adminServer.Stop(ctx); err != nil {
			s.logger.Error("failed to stop admin server", zap.Error(err))
		}
	}

	// Stop traffic collector
	if s.collector != nil {
		s.collector.Stop()
		s.logger.Info("traffic collector stopped")
	}

	s.healthMgr.Stop()
	cfg := s.configMgr.GetConfig()
	if cfg.Global.IsCleanupOnExit() {
		if err := s.reconciler.Cleanup(); err != nil {
			s.logger.Error("failed to cleanup IPVS rules", zap.Error(err))
		}
		if err := s.snatMgr.Cleanup(); err != nil {
			s.logger.Error("failed to cleanup SNAT rules", zap.Error(err))
		}
	} else {
		s.logger.Info("cleanup_on_exit is false, preserving IPVS and iptables rules")
	}
	s.lvsMgr.Close()
	s.logger.Info("server stopped")
}
