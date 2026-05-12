package logutil

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/easzlab/ezlb/pkg/config"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"gopkg.in/natefinch/lumberjack.v2"
)

// Loggers holds the logger instances used throughout the application.
//
// Only one logger (System) is exposed. Traffic stats are emitted as
// Prometheus metrics, not as a separate log file, so there is no
// dedicated traffic logger.
type Loggers struct {
	System *zap.Logger
}

// SyncAll calls Sync() on all loggers to flush any buffered log entries.
func (l *Loggers) SyncAll() {
	if l.System != nil {
		_ = l.System.Sync()
	}
}

// BuildLoggers creates the system logger based on LogConfig.
//
// System logger outputs to stdout + ${home}/ezlb.log when the log directory
// is writable. If the directory cannot be created OR exists but is not
// writable, a warning is printed to stderr and the file core is dropped
// (stdout-only).
func BuildLoggers(cfg config.LogConfig) (*Loggers, error) {
	level, err := parseZapLevel(cfg.GetLevel())
	if err != nil {
		return nil, fmt.Errorf("invalid log level %q: %w", cfg.GetLevel(), err)
	}

	home := cfg.GetHome()
	dirWritable := ensureWritableDir(home)

	consoleEncoderCfg := zap.NewProductionEncoderConfig()
	consoleEncoderCfg.TimeKey = "time"
	consoleEncoderCfg.EncodeTime = zapcore.TimeEncoderOfLayout("2006-01-02 15:04:05.000")
	consoleEncoderCfg.EncodeLevel = zapcore.CapitalColorLevelEncoder
	consoleEncoder := zapcore.NewConsoleEncoder(consoleEncoderCfg)

	jsonEncoderCfg := zap.NewProductionEncoderConfig()
	jsonEncoderCfg.TimeKey = "time"
	jsonEncoderCfg.EncodeTime = zapcore.TimeEncoderOfLayout("2006-01-02 15:04:05.000")
	jsonEncoder := zapcore.NewJSONEncoder(jsonEncoderCfg)

	systemCores := []zapcore.Core{
		zapcore.NewCore(consoleEncoder, zapcore.AddSync(os.Stdout), level),
	}
	if dirWritable {
		systemFileWriter := newLumberjackWriter(filepath.Join(home, "ezlb.log"), cfg)
		systemCores = append(systemCores, zapcore.NewCore(jsonEncoder, zapcore.AddSync(systemFileWriter), level))
	}

	return &Loggers{
		System: zap.New(zapcore.NewTee(systemCores...)),
	}, nil
}

// ensureWritableDir creates the directory if missing and verifies it is
// actually writable by the current process. Returns true only when the
// directory is usable for log files. Any failure is reported to stderr.
//
// The probe is necessary because os.MkdirAll succeeds when the directory
// already exists even with read-only permissions, which would later cause
// lumberjack to silently drop log lines on first write.
func ensureWritableDir(home string) bool {
	if err := os.MkdirAll(home, 0755); err != nil {
		fmt.Fprintf(os.Stderr, "WARNING: failed to create log directory %q: %v, falling back to stdout-only logging\n", home, err)
		return false
	}

	probe, err := os.CreateTemp(home, ".ezlb-write-probe-*")
	if err != nil {
		fmt.Fprintf(os.Stderr, "WARNING: log directory %q is not writable: %v, falling back to stdout-only logging\n", home, err)
		return false
	}
	probeName := probe.Name()
	_ = probe.Close()
	_ = os.Remove(probeName)
	return true
}

// NewBootstrapLogger creates a minimal stdout-only logger for use before config is loaded.
// It uses info level and console encoding.
func NewBootstrapLogger() *zap.Logger {
	encoderConfig := zap.NewProductionEncoderConfig()
	encoderConfig.TimeKey = "time"
	encoderConfig.EncodeTime = zapcore.TimeEncoderOfLayout("2006-01-02 15:04:05.000")
	encoderConfig.EncodeLevel = zapcore.CapitalColorLevelEncoder

	core := zapcore.NewCore(
		zapcore.NewConsoleEncoder(encoderConfig),
		zapcore.AddSync(os.Stdout),
		zap.InfoLevel,
	)
	return zap.New(core)
}

// newLumberjackWriter creates a lumberjack rolling file writer with the given config.
func newLumberjackWriter(filename string, cfg config.LogConfig) *lumberjack.Logger {
	return &lumberjack.Logger{
		Filename:   filename,
		MaxSize:    cfg.GetMaxSize(),
		MaxBackups: cfg.GetMaxBackups(),
		MaxAge:     cfg.GetMaxAge(),
		Compress:   cfg.Compress,
	}
}

// parseZapLevel converts a string log level to a zapcore.Level.
func parseZapLevel(level string) (zapcore.Level, error) {
	switch level {
	case "debug":
		return zapcore.DebugLevel, nil
	case "info":
		return zapcore.InfoLevel, nil
	case "warn":
		return zapcore.WarnLevel, nil
	case "error":
		return zapcore.ErrorLevel, nil
	default:
		return zapcore.InfoLevel, fmt.Errorf("unsupported log level: %s", level)
	}
}
