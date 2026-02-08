package server

import (
	"context"
	"fmt"

	"github.com/easzlab/ezlb/pkg/config"
	"github.com/easzlab/ezlb/pkg/healthcheck"
	"github.com/easzlab/ezlb/pkg/lvs"
	"go.uber.org/zap"
)

// Server coordinates all modules and manages the overall service lifecycle.
type Server struct {
	configMgr  *config.Manager
	lvsMgr     *lvs.Manager
	reconciler *lvs.Reconciler
	healthMgr  *healthcheck.Manager
	logger     *zap.Logger
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

	server := &Server{
		configMgr: configMgr,
		lvsMgr:    lvsMgr,
		logger:    logger,
	}

	// Initialize health check manager with onChange callback that triggers reconcile
	server.healthMgr = healthcheck.NewManager(func() {
		server.triggerReconcile()
	}, logger.Named("healthcheck"))

	// Initialize reconciler with health checker
	server.reconciler = lvs.NewReconciler(lvsMgr, server.healthMgr, logger.Named("reconciler"))

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

		case <-ctx.Done():
			s.logger.Info("shutdown signal received, stopping server")
			s.shutdown()
			return nil
		}
	}
}

// RunOnce performs a single reconcile pass and then shuts down.
// This is used for manual one-shot reconciliation (e.g., via CLI or cron).
func (s *Server) RunOnce() error {
	cfg := s.configMgr.GetConfig()

	err := s.reconciler.Reconcile(cfg.Services)
	s.shutdown()

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
	s.healthMgr.Stop()
	s.lvsMgr.Close()
	s.logger.Info("server stopped")
}
