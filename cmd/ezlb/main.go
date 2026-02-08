package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/easzlab/ezlb/pkg/server"
	"github.com/spf13/cobra"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

var (
	version    = "dev"
	configPath string
)

func main() {
	rootCmd := newRootCommand()
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

func newRootCommand() *cobra.Command {
	rootCmd := &cobra.Command{
		Use:   "ezlb",
		Short: "ezlb - IPVS based TCP load balancer",
		Long:  "A lightweight four-layer TCP load balancer using Linux IPVS with declarative reconcile mode.",
		RunE:  runDaemon,
	}

	rootCmd.PersistentFlags().StringVarP(&configPath, "config", "c", "/etc/ezlb/ezlb.yaml", "path to config file")

	rootCmd.AddCommand(newOnceCommand())
	rootCmd.AddCommand(newVersionCommand())

	return rootCmd
}

func newOnceCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "once",
		Short: "Run a single reconcile pass and exit",
		RunE:  runOnce,
	}
}

func newVersionCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print version information",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Printf("ezlb version %s\n", version)
		},
	}
}

// runDaemon starts the server in daemon mode with signal handling.
func runDaemon(cmd *cobra.Command, args []string) error {
	logger := newLogger()
	defer logger.Sync()

	logger.Info("starting ezlb",
		zap.String("version", version),
		zap.String("config", configPath),
	)

	srv, err := server.NewServer(configPath, logger)
	if err != nil {
		logger.Fatal("failed to create server", zap.Error(err))
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Handle OS signals for graceful shutdown
	signalChan := make(chan os.Signal, 1)
	signal.Notify(signalChan, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		sig := <-signalChan
		logger.Info("received signal", zap.String("signal", sig.String()))
		cancel()
	}()

	return srv.Run(ctx)
}

// runOnce performs a single reconcile pass and exits.
func runOnce(cmd *cobra.Command, args []string) error {
	logger := newLogger()
	defer logger.Sync()

	logger.Info("running single reconcile",
		zap.String("version", version),
		zap.String("config", configPath),
	)

	srv, err := server.NewServer(configPath, logger)
	if err != nil {
		return fmt.Errorf("failed to create server: %w", err)
	}

	return srv.RunOnce()
}

// newLogger creates a production zap logger with console encoding for readability.
func newLogger() *zap.Logger {
	encoderConfig := zap.NewProductionEncoderConfig()
	encoderConfig.TimeKey = "time"
	encoderConfig.EncodeTime = zapcore.ISO8601TimeEncoder
	encoderConfig.EncodeLevel = zapcore.CapitalColorLevelEncoder

	loggerConfig := zap.Config{
		Level:            zap.NewAtomicLevelAt(zap.InfoLevel),
		Encoding:         "console",
		EncoderConfig:    encoderConfig,
		OutputPaths:      []string{"stdout"},
		ErrorOutputPaths: []string{"stderr"},
	}

	logger, err := loggerConfig.Build()
	if err != nil {
		panic(fmt.Sprintf("failed to create logger: %v", err))
	}
	return logger
}
