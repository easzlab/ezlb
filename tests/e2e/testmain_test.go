//go:build linux

package e2e

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/easzlab/ezlb/pkg/lvs"
)

// ezlbBinary holds the path to the compiled ezlb binary used by all e2e tests.
var ezlbBinary string

func TestMain(m *testing.M) {
	// Build the ezlb binary into a temporary directory
	tmpDir, err := os.MkdirTemp("", "ezlb-e2e-*")
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to create temp dir: %v\n", err)
		os.Exit(1)
	}
	defer os.RemoveAll(tmpDir)

	ezlbBinary = filepath.Join(tmpDir, "ezlb")

	// Compile the binary from the project root
	// The test runs from tests/e2e/, so the module root is two levels up
	buildCmd := exec.Command("go", "build", "-o", ezlbBinary, "github.com/easzlab/ezlb/cmd/ezlb")
	buildCmd.Stdout = os.Stdout
	buildCmd.Stderr = os.Stderr
	if err := buildCmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "failed to build ezlb binary: %v\n", err)
		os.Exit(1)
	}

	// Flush all IPVS rules before running tests to ensure a clean state
	handle, err := lvs.NewIPVSHandle("")
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to create IPVS handle for pre-test flush: %v\n", err)
		os.Exit(1)
	}
	if err := handle.Flush(); err != nil {
		fmt.Fprintf(os.Stderr, "failed to flush IPVS rules before tests: %v\n", err)
		handle.Close()
		os.Exit(1)
	}
	handle.Close()

	code := m.Run()

	// Flush all IPVS rules after running tests to leave a clean state
	handle, err = lvs.NewIPVSHandle("")
	if err == nil {
		handle.Flush()
		handle.Close()
	}

	os.Exit(code)
}
