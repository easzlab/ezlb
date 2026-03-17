package logutil

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/easzlab/ezlb/pkg/config"
)

func TestBuildLoggers_DefaultConfig(t *testing.T) {
	// Use a temp directory so we don't pollute the workspace
	dir := t.TempDir()
	cfg := config.LogConfig{
		Home: dir,
	}

	loggers, err := BuildLoggers(cfg)
	if err != nil {
		t.Fatalf("BuildLoggers failed: %v", err)
	}
	if loggers.System == nil {
		t.Error("expected System logger to be non-nil")
	}
	if loggers.Traffic == nil {
		t.Error("expected Traffic logger to be non-nil")
	}
	if loggers.NAT == nil {
		t.Error("expected NAT logger to be non-nil")
	}
}

func TestBuildLoggers_CreatesLogDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "subdir", "logs")
	cfg := config.LogConfig{
		Home: dir,
	}

	_, err := BuildLoggers(cfg)
	if err != nil {
		t.Fatalf("BuildLoggers failed: %v", err)
	}

	info, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("expected log directory %q to exist, got error: %v", dir, err)
	}
	if !info.IsDir() {
		t.Errorf("expected %q to be a directory", dir)
	}
}

func TestBuildLoggers_FallbackOnBadHome(t *testing.T) {
	// Use /dev/null/impossible as an invalid path that cannot be created
	cfg := config.LogConfig{
		Home: "/dev/null/impossible/path",
	}

	loggers, err := BuildLoggers(cfg)
	if err != nil {
		t.Fatalf("BuildLoggers should not return error on bad home (fallback to stdout), got: %v", err)
	}
	// All loggers should still be non-nil (fallback to stdout)
	if loggers.System == nil {
		t.Error("expected System logger to be non-nil even with bad home")
	}
	if loggers.Traffic == nil {
		t.Error("expected Traffic logger to be non-nil even with bad home")
	}
	if loggers.NAT == nil {
		t.Error("expected NAT logger to be non-nil even with bad home")
	}
}

func TestBuildLoggers_LevelParsing(t *testing.T) {
	for _, level := range []string{"debug", "info", "warn", "error"} {
		dir := t.TempDir()
		cfg := config.LogConfig{
			Level: level,
			Home:  dir,
		}
		loggers, err := BuildLoggers(cfg)
		if err != nil {
			t.Errorf("BuildLoggers failed for level %q: %v", level, err)
			continue
		}
		if loggers.System == nil {
			t.Errorf("expected System logger to be non-nil for level %q", level)
		}
	}
}

func TestBuildLoggers_InvalidLevel(t *testing.T) {
	cfg := config.LogConfig{
		Level: "trace",
		Home:  t.TempDir(),
	}
	_, err := BuildLoggers(cfg)
	if err == nil {
		t.Fatal("expected error for invalid log level 'trace', got nil")
	}
}

func TestNewBootstrapLogger(t *testing.T) {
	logger := NewBootstrapLogger()
	if logger == nil {
		t.Fatal("expected NewBootstrapLogger to return non-nil logger")
	}
	// Verify it can log without panicking
	logger.Info("bootstrap test message")
}

func TestSyncAll(t *testing.T) {
	dir := t.TempDir()
	cfg := config.LogConfig{
		Home: dir,
	}
	loggers, err := BuildLoggers(cfg)
	if err != nil {
		t.Fatalf("BuildLoggers failed: %v", err)
	}
	// SyncAll should not panic
	loggers.SyncAll()
}

func TestBuildLoggers_CreatesLogFiles(t *testing.T) {
	dir := t.TempDir()
	cfg := config.LogConfig{
		Home: dir,
	}

	loggers, err := BuildLoggers(cfg)
	if err != nil {
		t.Fatalf("BuildLoggers failed: %v", err)
	}

	// Write a message to each logger to trigger file creation
	loggers.System.Info("system test")
	loggers.Traffic.Info("traffic test")
	loggers.NAT.Info("nat test")
	loggers.SyncAll()

	// Verify log files were created
	for _, name := range []string{"ezlb.log", "traffic.log", "nat.log"} {
		path := filepath.Join(dir, name)
		if _, err := os.Stat(path); os.IsNotExist(err) {
			t.Errorf("expected log file %q to exist", path)
		}
	}
}
