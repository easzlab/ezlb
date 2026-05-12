package logutil

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/easzlab/ezlb/pkg/config"
)

func TestBuildLoggers_DefaultConfig(t *testing.T) {
	dir := t.TempDir()
	loggers, err := BuildLoggers(config.LogConfig{Home: dir})
	if err != nil {
		t.Fatalf("BuildLoggers failed: %v", err)
	}
	if loggers.System == nil {
		t.Error("expected System logger to be non-nil")
	}
}

func TestBuildLoggers_CreatesLogDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "subdir", "logs")
	if _, err := BuildLoggers(config.LogConfig{Home: dir}); err != nil {
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

func TestBuildLoggers_FallbackOnUncreatableHome(t *testing.T) {
	// /dev/null is a character device on Unix; appending a path under it is
	// guaranteed to fail os.MkdirAll.
	loggers, err := BuildLoggers(config.LogConfig{Home: "/dev/null/impossible/path"})
	if err != nil {
		t.Fatalf("BuildLoggers should not return error on bad home (fallback to stdout), got: %v", err)
	}
	if loggers.System == nil {
		t.Error("expected System logger to be non-nil even with bad home")
	}
}

// TestBuildLoggers_FallbackOnReadOnlyDir is the P0-1 #4 regression test:
// when the log directory exists but is not writable, BuildLoggers must
// detect this via the write-probe and fall back to stdout-only, not silently
// drop log lines later via lumberjack.
func TestBuildLoggers_FallbackOnReadOnlyDir(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX-style permission bits don't apply on Windows")
	}
	if os.Geteuid() == 0 {
		t.Skip("read-only directory probing is bypassed by root")
	}

	dir := t.TempDir()
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatalf("chmod failed: %v", err)
	}
	t.Cleanup(func() {
		// Restore writable bit so t.TempDir cleanup can remove the directory.
		_ = os.Chmod(dir, 0o755)
	})

	if ensureWritableDir(dir) {
		t.Fatal("expected ensureWritableDir to return false for read-only dir")
	}

	loggers, err := BuildLoggers(config.LogConfig{Home: dir})
	if err != nil {
		t.Fatalf("BuildLoggers should not return error on read-only home, got: %v", err)
	}
	if loggers.System == nil {
		t.Fatal("expected System logger to be non-nil even on read-only home")
	}

	// No ezlb.log should be created since the file core was dropped.
	loggers.System.Info("probe message")
	loggers.SyncAll()

	if _, err := os.Stat(filepath.Join(dir, "ezlb.log")); !os.IsNotExist(err) {
		t.Fatalf("expected no ezlb.log to be created in read-only dir, got err=%v", err)
	}
}

func TestBuildLoggers_LevelParsing(t *testing.T) {
	for _, level := range []string{"debug", "info", "warn", "error"} {
		dir := t.TempDir()
		loggers, err := BuildLoggers(config.LogConfig{Level: level, Home: dir})
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
	if _, err := BuildLoggers(config.LogConfig{Level: "trace", Home: t.TempDir()}); err == nil {
		t.Fatal("expected error for invalid log level 'trace', got nil")
	}
}

func TestNewBootstrapLogger(t *testing.T) {
	logger := NewBootstrapLogger()
	if logger == nil {
		t.Fatal("expected NewBootstrapLogger to return non-nil logger")
	}
	logger.Info("bootstrap test message")
}

func TestSyncAll(t *testing.T) {
	loggers, err := BuildLoggers(config.LogConfig{Home: t.TempDir()})
	if err != nil {
		t.Fatalf("BuildLoggers failed: %v", err)
	}
	loggers.SyncAll()
}

func TestBuildLoggers_CreatesLogFile(t *testing.T) {
	dir := t.TempDir()
	loggers, err := BuildLoggers(config.LogConfig{Home: dir})
	if err != nil {
		t.Fatalf("BuildLoggers failed: %v", err)
	}

	loggers.System.Info("system test")
	loggers.SyncAll()

	path := filepath.Join(dir, "ezlb.log")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("expected log file %q to exist: %v", path, err)
	}
	if !strings.Contains(string(data), "system test") {
		t.Fatalf("expected log file to contain test message, got %q", string(data))
	}
}
