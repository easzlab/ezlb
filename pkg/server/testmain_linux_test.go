//go:build linux && integration

package server

import (
	"fmt"
	"os"
	"testing"

	"github.com/easzlab/ezlb/pkg/lvs"
)

func TestMain(m *testing.M) {
	if os.Getenv("EZLB_TEST_EXCLUSIVE_NETNS") != "1" {
		fmt.Fprintln(os.Stderr, "refusing to flush IPVS: set EZLB_TEST_EXCLUSIVE_NETNS=1 only inside a disposable network namespace")
		os.Exit(1)
	}
	// Flush all IPVS rules before running tests to ensure a clean state.
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

	// Flush all IPVS rules after running tests to leave a clean state.
	handle, err = lvs.NewIPVSHandle("")
	if err == nil {
		handle.Flush()
		handle.Close()
	}

	os.Exit(code)
}
