package main

import (
	"runtime"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func TestNamespaceMode(t *testing.T) {
	originalMode := netnsMode
	t.Cleanup(func() {
		netnsMode = originalMode
	})

	tests := []struct {
		name       string
		mode       string
		wantShared bool
		wantErr    string
	}{
		{name: "exclusive namespace", mode: netnsModeExclusive},
		{name: "shared namespace", mode: netnsModeShared, wantShared: true},
		{name: "invalid mode", mode: "invalid", wantErr: "must be exclusive or shared"},
	}
	if runtime.GOOS == "linux" {
		tests = append(tests, struct {
			name       string
			mode       string
			wantShared bool
			wantErr    string
		}{name: "missing mode", wantErr: "--netns-mode is required"})
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			netnsMode = test.mode
			gotShared, err := namespaceMode()
			if test.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantErr) {
					t.Fatalf("namespaceMode() error = %v, want substring %q", err, test.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("namespaceMode() returned error: %v", err)
			}
			if gotShared != test.wantShared {
				t.Fatalf("namespaceMode() shared = %t, want %t", gotShared, test.wantShared)
			}
		})
	}
}

func TestNamespaceModeFlag(t *testing.T) {
	for _, command := range []*cobra.Command{newOnceCommand(), newStartCommand()} {
		if command.Flags().Lookup("netns-mode") == nil {
			t.Fatalf("%s does not register --netns-mode", command.Name())
		}
		for _, retiredFlag := range []string{"exclusive-netns", "shared-netns"} {
			if command.Flags().Lookup(retiredFlag) != nil {
				t.Fatalf("%s still registers --%s", command.Name(), retiredFlag)
			}
		}
	}
}
