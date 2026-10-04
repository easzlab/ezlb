package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

const (
	vipURL          = "http://172.30.0.10:8080/"
	adminURL        = "http://172.30.0.10:9095"
	backendBControl = "http://backend-b:8080/control/"
	vipAddress      = "172.30.0.10"
	timeout         = 30 * time.Second
)

type backendResponse struct {
	Backend string `json:"backend"`
	Source  string `json:"source"`
}

func main() {
	checks := []struct {
		name string
		fn   func() error
	}{
		{"admin readiness", waitUntilReady},
		{"health endpoint", expectBothHealthy},
		{"round-robin forwarding and FullNAT source", expectBothBackends},
		{"Prometheus metrics", expectMetrics},
		{"backend failure control", func() error { return setBackendBHealthy(false) }},
		{"unhealthy backend removal", expectBackendBUnhealthy},
		{"traffic failover", func() error { return expectOnlyBackend("backend-a") }},
		{"backend recovery control", func() error { return setBackendBHealthy(true) }},
		{"healthy backend rejoin", expectBackendBHealthy},
		{"traffic recovery", expectBothBackends},
	}

	for _, check := range checks {
		if err := check.fn(); err != nil {
			fmt.Fprintf(os.Stderr, "FAIL %s: %v\n", check.name, err)
			os.Exit(1)
		}
		fmt.Printf("PASS %s\n", check.name)
	}
}

func waitUntilReady() error {
	return eventually("admin /ready", func() error {
		status, _, err := get(adminURL + "/ready")
		if err != nil {
			return err
		}
		if status != http.StatusOK {
			return fmt.Errorf("expected HTTP 200, got %d", status)
		}
		return nil
	})
}

func expectBothHealthy() error {
	return eventually("both health checks", func() error {
		status, body, err := get(adminURL + "/health")
		if err != nil {
			return err
		}
		if status != http.StatusOK {
			return fmt.Errorf("expected HTTP 200, got %d", status)
		}
		for _, target := range []string{`"web/172.30.0.11:8080":true`, `"web/172.30.0.12:8080":true`} {
			if !strings.Contains(body, target) {
				return fmt.Errorf("missing %s in %s", target, body)
			}
		}
		return nil
	})
}

func expectBackendBUnhealthy() error {
	return eventually("backend-b unhealthy", func() error {
		_, body, err := get(adminURL + "/health")
		if err != nil {
			return err
		}
		if !strings.Contains(body, `"web/172.30.0.12:8080":false`) {
			return fmt.Errorf("backend-b is still healthy: %s", body)
		}
		return nil
	})
}

func expectBackendBHealthy() error {
	return eventually("backend-b healthy", func() error {
		_, body, err := get(adminURL + "/health")
		if err != nil {
			return err
		}
		if !strings.Contains(body, `"web/172.30.0.12:8080":true`) {
			return fmt.Errorf("backend-b is still unhealthy: %s", body)
		}
		return nil
	})
}

func expectBothBackends() error {
	return eventually("round-robin responses", func() error {
		seen := make(map[string]bool)
		for range 12 {
			response, err := requestVIP()
			if err != nil {
				return err
			}
			seen[response.Backend] = true
		}
		if !seen["backend-a"] || !seen["backend-b"] {
			return fmt.Errorf("expected both backends, saw %v", seen)
		}
		return nil
	})
}

func expectOnlyBackend(want string) error {
	return eventually("failover responses", func() error {
		for range 8 {
			response, err := requestVIP()
			if err != nil {
				return err
			}
			if response.Backend != want {
				return fmt.Errorf("expected %s, got %s", want, response.Backend)
			}
		}
		return nil
	})
}

func expectMetrics() error {
	return eventually("traffic metrics", func() error {
		status, body, err := get(adminURL + "/metrics")
		if err != nil {
			return err
		}
		if status != http.StatusOK {
			return fmt.Errorf("expected HTTP 200, got %d", status)
		}
		if !strings.Contains(body, `ezlb_service_connections_total{listen="172.30.0.10:8080",protocol="tcp",service="web"}`) {
			return fmt.Errorf("service traffic metric not found")
		}
		return nil
	})
}

func setBackendBHealthy(healthy bool) error {
	state := "down"
	if healthy {
		state = "up"
	}
	return eventually("backend-b control", func() error {
		status, _, err := get(backendBControl + state)
		if err != nil {
			return err
		}
		if status != http.StatusNoContent {
			return fmt.Errorf("expected HTTP 204, got %d", status)
		}
		return nil
	})
}

func requestVIP() (backendResponse, error) {
	status, body, err := get(vipURL)
	if err != nil {
		return backendResponse{}, err
	}
	if status != http.StatusOK {
		return backendResponse{}, fmt.Errorf("VIP returned HTTP %d: %s", status, body)
	}
	var response backendResponse
	if err := json.Unmarshal([]byte(body), &response); err != nil {
		return backendResponse{}, fmt.Errorf("decode backend response: %w", err)
	}
	if response.Source != vipAddress {
		return backendResponse{}, fmt.Errorf("expected backend to observe FullNAT source %s, got %s", vipAddress, response.Source)
	}
	return response, nil
}

func eventually(name string, check func() error) error {
	deadline := time.Now().Add(timeout)
	var lastErr error
	for time.Now().Before(deadline) {
		if err := check(); err == nil {
			return nil
		} else {
			lastErr = err
		}
		time.Sleep(250 * time.Millisecond)
	}
	return fmt.Errorf("%s did not succeed within %s: %w", name, timeout, lastErr)
}

func get(url string) (int, string, error) {
	client := &http.Client{
		Timeout: 2 * time.Second,
		Transport: &http.Transport{
			DisableKeepAlives: true,
		},
	}
	response, err := client.Get(url)
	if err != nil {
		return 0, "", err
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		return 0, "", err
	}
	return response.StatusCode, string(body), nil
}
