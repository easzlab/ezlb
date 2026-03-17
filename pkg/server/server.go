package server

import (
	"context"
	"fmt"

	"github.com/easzlab/ezlb/pkg/config"
	"github.com/easzlab/ezlb/pkg/healthcheck"
	"github.com/easzlab/ezlb/pkg/lvs"
	"github.com/easzlab/ezlb/pkg/snat"
	"github.com/easzlab/ezlb/pkg/trafficlog"
	"go.uber.org/zap"
)

// Server coordinates all modules and manages the overall service lifecycle.
type Server struct {
	configMgr     *config.Manager
	lvsMgr        *lvs.Manager
	reconciler    *lvs.Reconciler
	healthMgr     *healthcheck.Manager
	snatMgr       snat.Manager
	logger        *zap.Logger
	trafficLogger *zap.Logger
	natLogger     *zap.Logger
	collector     *trafficlog.Collector
}

// NewServer initializes all modules and returns a ready-to-run Server.
func NewServer(configPath string, logger *zap.Logger, trafficLogger *zap.Logger, natLogger *zap.Logger) (*Server, error) {
	// Initialize IPVS manager
	lvsMgr, err := lvs.NewManager(logger.Named("lvs"))
	if err != nil {
		return nil, fmt.Errorf("failed to initialize IPVS manager: %w", err)
	}

	return newServerWithManager(configPath, lvsMgr, logger, trafficLogger, natLogger)
}

// newServerWithManager initializes a Server with a pre-created LVS Manager.
// This allows tests to inject a platform-appropriate Manager instance.
func newServerWithManager(configPath string, lvsMgr *lvs.Manager, logger *zap.Logger, trafficLogger *zap.Logger, natLogger *zap.Logger) (*Server, error) {
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
		configMgr:     configMgr,
		lvsMgr:        lvsMgr,
		snatMgr:       snatMgr,
		logger:        logger,
		trafficLogger: trafficLogger,
		natLogger:     natLogger,
	}

	// Initialize health check manager with onChange callback that triggers reconcile
	server.healthMgr = healthcheck.NewManager(func() {
		server.triggerReconcile()
	}, logger.Named("healthcheck"))

	// Initialize reconciler with health checker and SNAT manager
	server.reconciler = lvs.NewReconciler(lvsMgr, server.healthMgr, snatMgr, logger.Named("reconciler"))

	return server, nil
}

// Run starts the server in daemon mode: performs initial reconcile, starts health checks
// and config watching, then enters the main event loop until context is cancelled.
func (s *Server) Run(ctx context.Context) error {
	cfg := s.configMgr.GetConfig()

	// Register health check targets and start checking
	s.healthMgr.UpdateTargets(ctx, cfg.Services)

	// Perform initial reconcile
	if err := s.reconciler.Reconcile(cfg.Services); err != nil {
		s.logger.Error("initial reconcile failed", zap.Error(err))
	}

	// Start traffic collector if enabled (daemon mode only)
	if cfg.Global.Log.Traffic.IsEnabled() {
		lvsStats := trafficlog.NewLVSStatsAdapter(s.lvsMgr)

		// Check if snat manager supports statistics via type assertion
		var snatStats trafficlog.SNATStatsProvider
		if sp, ok := s.snatMgr.(snat.StatsProvider); ok {
			snatStats = &snatStatsAdapter{provider: sp}
		}

		s.collector = trafficlog.NewCollector(
			lvsStats,
			snatStats,
			s.trafficLogger,
			s.natLogger,
			s.logger,
			cfg.Services,
			cfg.Global.Log.Traffic,
		)
		s.collector.Start()
		s.logger.Info("traffic collector started",
			zap.Duration("interval", cfg.Global.Log.Traffic.GetInterval()),
			zap.Bool("include_snat", cfg.Global.Log.Traffic.IsIncludeSNAT()),
		)
	}

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
			// Update traffic collector config
			if s.collector != nil {
				s.collector.UpdateConfig(newCfg.Services, newCfg.Global.Log.Traffic)
			}

		case <-ctx.Done():
			s.logger.Info("shutdown signal received, stopping server")
			s.shutdown()
			return nil
		}
	}
}

// RunOnce performs a single reconcile pass and then exits.
// IPVS rules and iptables rules are intentionally preserved after exit —
// cleanup_on_exit does not apply to once mode, whose purpose is to apply
// the desired state and leave it in place.
func (s *Server) RunOnce() error {
	cfg := s.configMgr.GetConfig()

	err := s.reconciler.Reconcile(cfg.Services)
	s.lvsMgr.Close()

	if err != nil {
		return fmt.Errorf("reconcile failed: %w", err)
	}
	return nil
}

// triggerReconcile is called by the health check manager when a backend's health status changes.
func (s *Server) triggerReconcile() {
	cfg := s.configMgr.GetConfig()
	if err := s.reconciler.Reconcile(cfg.Services); err != nil {
		s.logger.Error("reconcile after health change failed", zap.Error(err))
	}
}

// shutdown gracefully stops all modules.
func (s *Server) shutdown() {
	// Stop traffic collector first
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

// snatStatsAdapter adapts snat.StatsProvider to trafficlog.SNATStatsProvider.
type snatStatsAdapter struct {
	provider snat.StatsProvider
}

func (a *snatStatsAdapter) Stats() (map[string]trafficlog.SNATRuleStats, error) {
	raw, err := a.provider.Stats()
	if err != nil {
		return nil, err
	}
	result := make(map[string]trafficlog.SNATRuleStats, len(raw))
	for k, v := range raw {
		result[k] = trafficlog.SNATRuleStats{
			Packets: v.Packets,
			Bytes:   v.Bytes,
		}
	}
	return result, nil
}
